package main

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestPaperPromotionRejectsDisjointPreResearchBars(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	old := paperPromotionForwardBars(t, mustTime("2025-01-01T00:00:00Z"), 24*time.Hour)
	if _, _, err := f.svc.claimPaperPromotion(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, "pre-research", old); err == nil {
		t.Fatal("nonoverlapping input before the research period was accepted")
	}
	var claims int
	if err := f.svc.db.QueryRow(`SELECT COUNT(*) FROM paper_promotion_claims`).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("rejected input created a claim: %d %v", claims, err)
	}
	claim, prior, err := f.svc.claimPaperPromotion(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, "after-research", f.bars)
	if err != nil || claim == nil || prior != nil {
		t.Fatalf("valid forward comparison rejected: %+v %+v %v", claim, prior, err)
	}
}

func TestPaperPromotionRejectsEarlierDisjointClaimAndClockRegression(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	claim, prior, err := f.svc.claimPaperPromotion(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, "first-claim", f.bars)
	if err != nil || claim == nil || prior != nil {
		t.Fatalf("first claim: %+v %+v %v", claim, prior, err)
	}
	claimedAt, err := time.Parse(time.RFC3339Nano, claim.RecordedAt)
	if err != nil {
		t.Fatal(err)
	}
	clock := claimedAt.Add(10 * time.Nanosecond)
	f.svc.now = func() time.Time { return clock }
	if _, err := f.svc.abortPaperPromotion(ctx, claim.AttemptID); err != nil {
		t.Fatal(err)
	}
	earlier := paperPromotionForwardBars(t, mustTime("2026-05-01T00:00:00Z"), time.Hour)
	if _, _, err := f.svc.claimPaperPromotion(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, "earlier-disjoint", earlier); err == nil {
		t.Fatal("a disjoint interval before the prior comparison was accepted")
	}
	fresh := paperPromotionForwardBars(t, mustTime("2026-08-05T00:00:00Z"), 12*time.Hour)
	clock = claimedAt.Add(9 * time.Nanosecond)
	if _, _, err := f.svc.claimPaperPromotion(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, "next-claim", fresh); err == nil {
		t.Fatal("a claim recorded before the previous decision was accepted")
	}
	var claims int
	if err := f.svc.db.QueryRow(`SELECT COUNT(*) FROM paper_promotion_claims`).Scan(&claims); err != nil || claims != 1 {
		t.Fatalf("rejected attempts changed claims: %d %v", claims, err)
	}
	clock = claimedAt.Add(10 * time.Nanosecond)
	next, prior, err := f.svc.claimPaperPromotion(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, "next-claim", fresh)
	if err != nil || next == nil || prior != nil {
		t.Fatalf("claim at previous decision time rejected: %+v %+v %v", next, prior, err)
	}
}

func TestPaperPromotionResearchAppendRequiresTimeAfterClaim(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	additional := paperPromotionBoundaryArtifact(t)
	claim, prior, err := f.svc.claimPaperPromotion(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, "research-clock", f.bars)
	if err != nil || claim == nil || prior != nil {
		t.Fatalf("claim: %+v %+v %v", claim, prior, err)
	}
	claimedAt, err := time.Parse(time.RFC3339Nano, claim.RecordedAt)
	if err != nil {
		t.Fatal(err)
	}
	clock := claimedAt
	f.svc.now = func() time.Time { return clock }
	for _, offset := range []time.Duration{0, -time.Nanosecond} {
		clock = claimedAt.Add(offset)
		if _, err := f.svc.registerStrategyEvidence(ctx, additional); err == nil {
			t.Fatalf("new research recorded at claim offset %s", offset)
		}
		retry, err := f.svc.registerStrategyEvidence(ctx, []byte(f.challenger.artifactJSON))
		if err != nil || !reflect.DeepEqual(retry, f.challenger) {
			t.Fatalf("existing evidence retry at offset %s: %+v %v", offset, retry, err)
		}
		proof, err := provePaperPromotionRecovery(ctx, f.svc.db)
		if err != nil || proof.Claims != 1 || proof.Decisions != 0 {
			t.Fatalf("rejected or idempotent registration changed claim proof: %+v %v", proof, err)
		}
	}
	clock = claimedAt.Add(time.Nanosecond)
	registered, err := f.svc.registerStrategyEvidence(ctx, additional)
	if err != nil || registered == nil || registered.RecordedAt != clock.Format(time.RFC3339Nano) {
		t.Fatalf("new research one nanosecond after claim: %+v %v", registered, err)
	}
	if _, err := provePaperPromotionRecovery(ctx, f.svc.db); err != nil {
		t.Fatalf("later research invalidated the claim: %v", err)
	}
	var laterSequence int64
	if err := f.svc.db.QueryRow(`SELECT sequence FROM strategy_research_evidence WHERE result_sha256=?`, registered.ResultSHA256).Scan(&laterSequence); err != nil {
		t.Fatal(err)
	}
	claim.ResearchSequenceCutoff = laterSequence
	overwritePaperPromotionClaimForBoundaryTest(t, f.svc, *claim)
	if _, err := provePaperPromotionRecovery(ctx, f.svc.db); err == nil {
		t.Fatal("rehashed claim included research recorded one nanosecond later")
	}
}

func TestPaperPromotionRecoveryRejectsExcludedResearchPrefix(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	extra, err := f.svc.registerStrategyEvidence(ctx, paperPromotionBoundaryArtifact(t))
	if err != nil || extra == nil {
		t.Fatalf("additional research: %+v %v", extra, err)
	}
	var incumbentSeq, challengerSeq, extraSeq int64
	for _, item := range []struct {
		sha string
		seq *int64
	}{{f.incumbent.ResultSHA256, &incumbentSeq}, {f.challenger.ResultSHA256, &challengerSeq}, {extra.ResultSHA256, &extraSeq}} {
		if err := f.svc.db.QueryRow(`SELECT sequence FROM strategy_research_evidence WHERE result_sha256=?`, item.sha).Scan(item.seq); err != nil {
			t.Fatal(err)
		}
	}
	claim, prior, err := f.svc.claimPaperPromotion(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, "research-prefix", f.bars)
	if err != nil || claim == nil || prior != nil || extraSeq <= max(incumbentSeq, challengerSeq) || claim.ResearchSequenceCutoff != extraSeq {
		t.Fatalf("research prefix fixture: %+v %+v sequences=%d/%d/%d err=%v", claim, prior, incumbentSeq, challengerSeq, extraSeq, err)
	}
	if _, err := provePaperPromotionRecovery(ctx, f.svc.db); err != nil {
		t.Fatalf("original claim is not recoverable: %v", err)
	}
	claim.ResearchSequenceCutoff = max(incumbentSeq, challengerSeq)
	overwritePaperPromotionClaimForBoundaryTest(t, f.svc, *claim)
	if _, err := provePaperPromotionRecovery(ctx, f.svc.db); err == nil {
		t.Fatal("rehashed claim excluded research recorded before the claim")
	}
}

func TestPaperPromotionOrderPrefixAndAppendTime(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	intent := k2aIntent("promotion-existing-order")
	intent.AccountRef = "kiwoom_account_BBBBBBBBBBBBBBBBBBBBBBBB"
	order, err := f.svc.recordOrderIntent(ctx, intent)
	if err != nil || order == nil {
		t.Fatalf("existing order: %+v %v", order, err)
	}
	terminal, err := f.svc.appendOrderEvent(ctx, k2aEvent("promotion-risk-rejected", order.OrderID, "RISK_REJECTED"))
	if err != nil || terminal == nil || terminal.Status != "RISK_REJECTED" {
		t.Fatalf("terminal order fixture: %+v %v", terminal, err)
	}
	claim, prior, err := f.svc.claimPaperPromotion(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, "order-prefix", f.bars)
	if err != nil || claim == nil || prior != nil || claim.OrderSequenceCutoff < 2 {
		t.Fatalf("order prefix claim: %+v %+v %v", claim, prior, err)
	}
	claimedAt, err := time.Parse(time.RFC3339Nano, claim.RecordedAt)
	if err != nil {
		t.Fatal(err)
	}
	clock := claimedAt
	f.svc.now = func() time.Time { return clock }
	newIntent := intent
	newIntent.ClientOrderID = "promotion-later-order"
	for _, offset := range []time.Duration{0, -time.Nanosecond} {
		clock = claimedAt.Add(offset)
		if _, err := f.svc.recordOrderIntent(ctx, newIntent); err == nil {
			t.Fatalf("new order recorded at claim offset %s", offset)
		}
		var created int
		if err := f.svc.db.QueryRow(`SELECT COUNT(*) FROM order_idempotency WHERE client_order_id=?`, newIntent.ClientOrderID).Scan(&created); err != nil || created != 0 {
			t.Fatalf("rejected order left an idempotency record: %d %v", created, err)
		}
		retry, err := f.svc.recordOrderIntent(ctx, intent)
		if err != nil || !reflect.DeepEqual(retry, terminal) {
			t.Fatalf("existing order retry at offset %s: %+v %v", offset, retry, err)
		}
	}
	clock = claimedAt.Add(time.Nanosecond)
	later, err := f.svc.recordOrderIntent(ctx, newIntent)
	if err != nil || later == nil {
		t.Fatalf("new order one nanosecond after claim: %v", err)
	}
	event := k2aEvent("promotion-later-risk-rejected", later.OrderID, "RISK_REJECTED")
	clock = claimedAt
	if _, err := f.svc.appendOrderEvent(ctx, event); err == nil {
		t.Fatal("new event for an existing order was recorded at claim time")
	}
	state, err := f.svc.loadOrderState(ctx, later.OrderID)
	if err != nil || state == nil || state.Status != "RECORDED" {
		t.Fatalf("rejected event changed order state: %+v %v", state, err)
	}
	clock = claimedAt.Add(time.Nanosecond)
	laterTerminal, err := f.svc.appendOrderEvent(ctx, event)
	if err != nil || laterTerminal == nil || laterTerminal.Status != "RISK_REJECTED" {
		t.Fatalf("event one nanosecond after claim: %+v %v", laterTerminal, err)
	}
	clock = claimedAt.Add(-time.Nanosecond)
	if retry, err := f.svc.appendOrderEvent(ctx, event); err != nil || !reflect.DeepEqual(retry, laterTerminal) {
		t.Fatalf("existing order event retry: %+v %v", retry, err)
	}
	if _, err := provePaperPromotionRecovery(ctx, f.svc.db); err != nil {
		t.Fatalf("later order invalidated the claim: %v", err)
	}
	original := *claim
	if err := f.svc.db.QueryRow(`SELECT MAX(sequence) FROM order_events`).Scan(&claim.OrderSequenceCutoff); err != nil {
		t.Fatal(err)
	}
	overwritePaperPromotionClaimForBoundaryTest(t, f.svc, *claim)
	if _, err := provePaperPromotionRecovery(ctx, f.svc.db); err == nil {
		t.Fatal("rehashed claim included an order recorded one nanosecond later")
	}
	overwritePaperPromotionClaimForBoundaryTest(t, f.svc, original)
	if _, err := provePaperPromotionRecovery(ctx, f.svc.db); err != nil {
		t.Fatalf("restored original claim did not recover: %v", err)
	}
	claim.OrderSequenceCutoff = original.OrderSequenceCutoff - 1
	overwritePaperPromotionClaimForBoundaryTest(t, f.svc, *claim)
	if _, err := provePaperPromotionRecovery(ctx, f.svc.db); err == nil {
		t.Fatal("rehashed claim excluded a terminal order event recorded before the claim")
	}
}

func paperPromotionBoundaryArtifact(t *testing.T) []byte {
	t.Helper()
	return strategyArtifact(t, func(config map[string]any) {
		strategy := config["strategy"].(map[string]any)
		strategy["fast_windows"], strategy["slow_windows"] = []int{4}, []int{8}
	})
}

func overwritePaperPromotionClaimForBoundaryTest(t *testing.T, svc *Service, claim paperPromotionClaim) {
	t.Helper()
	raw, hash, err := orderJSONHash(claim)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := svc.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var trigger string
	if err := tx.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name='paper_promotion_claims_no_update'`).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DROP TRIGGER paper_promotion_claims_no_update`); err != nil {
		t.Fatal(err)
	}
	result, err := tx.Exec(`UPDATE paper_promotion_claims SET record_json=?,record_sha256=? WHERE attempt_id=?`, string(raw), hash, claim.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("claim rewrite affected %d rows: %v", count, err)
	}
	if _, err := tx.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
