package main

import (
	"context"
	"testing"
	"time"
)

func TestPaperPromotionUsesCashAfterLossAndRejectsEarlierCashPrefix(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	bar := recordG38C2FillBar(t, f.svc, "promotion-cash-signal", "100", "5", "2026-10-01")
	signal := PaperSignal{SchemaVersion: capitalizedPaperSignalSchema, SignalID: "promotion-cash-buy", SignalBarObservationID: bar.ObservationID,
		StrategyResultSHA256: f.incumbent.ResultSHA256, StrategySelectionEventID: f.selection.CurrentEventID, DataSHA256: bar.InputDataSHA256,
		Symbol: bar.Symbol, TargetQuantity: "2", DataAsOf: bar.CloseAt, GeneratedAt: bar.SourceAvailableAt, ExpiresAt: "2026-10-29T00:00:00Z"}
	lease := mustK2CLease(t, f.svc, k2aAccountRef)
	_, buy, err := f.svc.admitPaperSignal(ctx, k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	buyBar := recordG38C2FillBar(t, f.svc, "promotion-cash-buy-fill", "100", "5", "2026-10-02")
	if buy, err = f.svc.runPaperOrder(ctx, buy.OrderID, lease.FencingToken); err != nil || buy.Status != "FILLED" {
		t.Fatalf("buy: %+v %v", buy, err)
	}
	signal.SignalID, signal.TargetQuantity = "promotion-cash-sell", "0"
	signal.SignalBarObservationID, signal.DataSHA256, signal.DataAsOf = buyBar.ObservationID, buyBar.InputDataSHA256, buyBar.CloseAt
	signal.GeneratedAt = buyBar.SourceAvailableAt
	_, sell, err := f.svc.admitPaperSignal(ctx, k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	recordG38C2FillBar(t, f.svc, "promotion-cash-sell-fill", "90", "5", "2026-10-03")
	if sell, err = f.svc.runPaperOrder(ctx, sell.OrderID, lease.FencingToken); err != nil || sell.Status != "FILLED" {
		t.Fatalf("sell: %+v %v", sell, err)
	}
	if _, err := f.svc.setSyntheticExecutionArmed(ctx, k2aAccountRef, false); err != nil {
		t.Fatal(err)
	}
	claim, prior, err := f.svc.claimPaperPromotion(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, "after-loss", f.bars)
	if err != nil || claim == nil || prior != nil {
		t.Fatalf("claim after loss: %+v %+v %v", claim, prior, err)
	}
	if claim.StartingCash != "9977.44018" || claim.StartingCash == f.session.StartingCash {
		t.Fatalf("comparison reset actual cash: %s", claim.StartingCash)
	}
	if _, err := scorePaperPromotionClaim(ctx, f.svc.db, *claim); err != nil {
		t.Fatal(err)
	}
	if _, err := provePaperPromotionRecovery(ctx, f.svc.db); err != nil {
		t.Fatal(err)
	}
	claim.OrderSequenceCutoff, claim.StartingCash = 0, f.session.StartingCash
	overwritePaperPromotionClaimForBoundaryTest(t, f.svc, *claim)
	if _, err := replayStrategyRegistry(ctx, f.svc.db); err == nil {
		t.Fatal("rehashing pre-loss cash and cutoff hid the recorded loss")
	}
}

func TestPaperPromotionCancellationConsumesClaimWithoutSelection(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	claim, _, err := f.svc.claimPaperPromotion(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, "cancelled-comparison", f.bars)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	comparison, err := scorePaperPromotionClaim(cancelled, f.svc.db, *claim)
	if err != context.Canceled {
		t.Fatalf("cancelled score: %v", err)
	}
	receipt, err := f.svc.finishPaperPromotion(ctx, *claim, comparison, err, false)
	if err != nil || receipt.Decision != "ERROR" || receipt.SelectionEventID != "" {
		t.Fatalf("cancelled finish: %+v %v", receipt, err)
	}
	f.svc.now = func() time.Time { return mustTime("2026-10-28T00:00:00Z") }
	if _, _, err := f.svc.claimPaperPromotion(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, "reused-cancelled", f.bars); err == nil {
		t.Fatal("cancelled evaluation interval was reused")
	}
}

func TestPaperPromotionLaterPromotionPreservesEarlierSignalHistory(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	first, err := f.svc.promotePaperCandidate(ctx, k2aAccountRef, f.challenger.ResultSHA256, f.selection.CurrentEventID, "first-promotion", f.bars)
	if err != nil || first.Decision != "PROMOTE" {
		t.Fatalf("first promotion: %+v %v", first, err)
	}
	end, err := time.Parse(time.RFC3339, first.ComparisonEnd)
	if err != nil {
		t.Fatal(err)
	}
	checkPaperPromotionSignalAdmission(t, f, first.SelectionEventID, end, nil)
	rollback, err := f.svc.rollbackPaperCandidate(ctx, first.SelectionEventID, first.SelectionEventID)
	if err != nil {
		t.Fatal(err)
	}
	fresh := paperPromotionForwardBars(t, mustTime("2026-08-05T00:00:00Z"), 12*time.Hour)
	second, err := f.svc.promotePaperCandidate(ctx, k2aAccountRef, f.challenger.ResultSHA256, rollback.CurrentEventID, "second-promotion", fresh)
	if err != nil || second.Decision != "PROMOTE" {
		t.Fatalf("second promotion: %+v %v", second, err)
	}
	if _, err := provePaperAccountingRecovery(ctx, f.svc.db); err != nil {
		t.Fatalf("later promotion invalidated earlier signals: %v", err)
	}
	if err := validatePaperPromotionSignalCutoff(ctx, f.svc.db, f.challenger.ResultSHA256, second.SelectionEventID, end.Add(time.Second).Format(time.RFC3339)); err == nil {
		t.Fatal("new selection used the first comparison cutoff")
	}
}
