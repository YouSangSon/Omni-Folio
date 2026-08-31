package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestG38F2FencedC3RejectsClaimSelectionMismatchWithoutWriting(t *testing.T) {
	svc, claim, asOf := g38F2ClaimedDueWindow(t)
	before := scheduledPerformanceCount(t, svc)
	stale := *claim
	stale.StrategySelectionEventID = "strategy_selection_stale_claim"

	if event, err := svc.evaluatePaperPerformanceWithClaim(context.Background(), k2aAccountRef, asOf, &stale); err == nil || event != nil {
		t.Fatalf("selection-mismatched claim wrote C3 event=%+v err=%v", event, err)
	}
	if after := scheduledPerformanceCount(t, svc); after != before {
		t.Fatalf("selection-mismatched claim leaked C3 rows before=%d after=%d", before, after)
	}
}

func TestG38F2FencedC3RollsBackAtLeaseBoundaries(t *testing.T) {
	t.Run("expired before entry", func(t *testing.T) {
		svc, claim, asOf := g38F2ClaimedDueWindow(t)
		before := scheduledPerformanceCount(t, svc)
		expires := time.Unix(0, claim.LeaseExpiresAtNS).UTC()
		svc.now = func() time.Time { return expires }

		if event, err := svc.evaluatePaperPerformanceWithClaim(context.Background(), k2aAccountRef, asOf, claim); err == nil || event != nil {
			t.Fatalf("expired claim entered C3 event=%+v err=%v", event, err)
		}
		if after := scheduledPerformanceCount(t, svc); after != before {
			t.Fatalf("entry expiry leaked C3 rows before=%d after=%d", before, after)
		}
	})

	t.Run("expires before final renewal", func(t *testing.T) {
		svc, claim, asOf := g38F2ClaimedDueWindow(t)
		before := scheduledPerformanceCount(t, svc)
		start := time.Unix(0, claim.LeaseExpiresAtNS).UTC().Add(-30 * time.Second)
		expires := start.Add(30*time.Second + time.Nanosecond)
		calls := 0
		svc.now = func() time.Time {
			calls++
			if calls <= 3 {
				return start
			}
			return expires
		}

		if event, err := svc.evaluatePaperPerformanceWithClaim(context.Background(), k2aAccountRef, asOf, claim); err == nil || event != nil {
			t.Fatalf("claim loss before final renewal committed C3 event=%+v err=%v calls=%d", event, err, calls)
		}
		if after := scheduledPerformanceCount(t, svc); after != before {
			t.Fatalf("final-boundary lease loss leaked C3 rows before=%d after=%d calls=%d", before, after, calls)
		}
	})
}

func TestG38F2FencedDAndERollBackAtLeaseBoundaries(t *testing.T) {
	t.Run("D expires before entry", func(t *testing.T) {
		svc, claim, asOf := g38F2ClaimedDueWindow(t)
		point, err := svc.evaluatePaperPerformanceWithClaim(context.Background(), k2aAccountRef, asOf, claim)
		if err != nil {
			t.Fatal(err)
		}
		before := strategyPerformanceCount(t, svc)
		svc.now = func() time.Time { return time.Unix(0, claim.LeaseExpiresAtNS).UTC() }
		if event, err := svc.evaluatePaperStrategyPerformanceWithClaim(context.Background(), k2aAccountRef, point.StrategySelectionEventID, point.PerformanceID, claim); err == nil || event != nil {
			t.Fatalf("expired claim entered D event=%+v err=%v", event, err)
		}
		if after := strategyPerformanceCount(t, svc); after != before {
			t.Fatalf("entry expiry leaked D rows before=%d after=%d", before, after)
		}
	})

	t.Run("D expires before final renewal", func(t *testing.T) {
		svc, claim, asOf := g38F2ClaimedDueWindow(t)
		point, err := svc.evaluatePaperPerformanceWithClaim(context.Background(), k2aAccountRef, asOf, claim)
		if err != nil {
			t.Fatal(err)
		}
		before := strategyPerformanceCount(t, svc)
		g38F2ExpireOnFinalStageClock(svc, claim)
		if event, err := svc.evaluatePaperStrategyPerformanceWithClaim(context.Background(), k2aAccountRef, point.StrategySelectionEventID, point.PerformanceID, claim); err == nil || event != nil {
			t.Fatalf("claim loss before final renewal committed D event=%+v err=%v", event, err)
		}
		if after := strategyPerformanceCount(t, svc); after != before {
			t.Fatalf("final-boundary lease loss leaked D rows before=%d after=%d", before, after)
		}
	})

	t.Run("E expires before entry", func(t *testing.T) {
		svc, claim, point, window := g38F2ClaimedPolicyInput(t)
		before := g38EJournalCounts(t, svc).Policy
		svc.now = func() time.Time { return time.Unix(0, claim.LeaseExpiresAtNS).UTC() }
		if event, err := svc.applyPaperPerformancePolicyWithClaim(context.Background(), k2aAccountRef, point.StrategySelectionEventID, window.StrategyPerformanceID, claim); err == nil || event != nil {
			t.Fatalf("expired claim entered E event=%+v err=%v", event, err)
		}
		if after := g38EJournalCounts(t, svc).Policy; after != before {
			t.Fatalf("entry expiry leaked E rows before=%d after=%d", before, after)
		}
	})

	t.Run("E expires before final renewal", func(t *testing.T) {
		svc, claim, point, window := g38F2ClaimedPolicyInput(t)
		before := g38EJournalCounts(t, svc).Policy
		g38F2ExpireOnFinalStageClock(svc, claim)
		if event, err := svc.applyPaperPerformancePolicyWithClaim(context.Background(), k2aAccountRef, point.StrategySelectionEventID, window.StrategyPerformanceID, claim); err == nil || event != nil {
			t.Fatalf("claim loss before final renewal committed E event=%+v err=%v", event, err)
		}
		if after := g38EJournalCounts(t, svc).Policy; after != before {
			t.Fatalf("final-boundary lease loss leaked E rows before=%d after=%d", before, after)
		}
	})
}

func g38F2ClaimedPolicyInput(t *testing.T) (*Service, *paperRunnerClaim, *PaperPerformanceEvent, *PaperStrategyPerformanceEvent) {
	t.Helper()
	svc, claim, asOf := g38F2ClaimedDueWindow(t)
	point, err := svc.evaluatePaperPerformanceWithClaim(context.Background(), k2aAccountRef, asOf, claim)
	if err != nil {
		t.Fatal(err)
	}
	window, err := svc.evaluatePaperStrategyPerformanceWithClaim(context.Background(), k2aAccountRef, point.StrategySelectionEventID, point.PerformanceID, claim)
	if err != nil {
		t.Fatal(err)
	}
	return svc, claim, point, window
}

func g38F2ExpireOnFinalStageClock(svc *Service, claim *paperRunnerClaim) {
	start := time.Unix(0, claim.LeaseExpiresAtNS).UTC().Add(-paperRunnerLeaseTTL)
	expires := start.Add(paperRunnerLeaseTTL + time.Nanosecond)
	calls := 0
	svc.now = func() time.Time {
		calls++
		if calls <= 3 {
			return start
		}
		return expires
	}
}

func TestG38F2HigherFenceRejectsStaleOwnerAndResumesPrefixesOnce(t *testing.T) {
	primary, staleClaim, asOf := g38F2ClaimedDueWindow(t)
	expires := time.Unix(0, staleClaim.LeaseExpiresAtNS).UTC()
	takeover := secondG38C2Service(t, primary, expires)
	claim, err := takeover.acquirePaperRunnerLease(context.Background(), k2aAccountRef)
	if err != nil || claim.FencingToken <= staleClaim.FencingToken {
		t.Fatalf("takeover claim=%+v stale=%+v err=%v", claim, staleClaim, err)
	}
	if result, err := primary.runDuePaperPerformancePolicyWithClaim(context.Background(), k2aAccountRef, staleClaim); err == nil || result != nil {
		t.Fatalf("stale owner continued after higher fence result=%+v err=%v", result, err)
	}

	point, err := takeover.evaluatePaperPerformanceWithClaim(context.Background(), k2aAccountRef, asOf, claim)
	if err != nil || point == nil {
		t.Fatalf("takeover did not resume C3 point=%+v err=%v", point, err)
	}
	window, err := takeover.evaluatePaperStrategyPerformanceWithClaim(context.Background(), k2aAccountRef, point.StrategySelectionEventID, point.PerformanceID, claim)
	if err != nil || window == nil {
		t.Fatalf("takeover did not resume C3+D window=%+v err=%v", window, err)
	}
	if got := scheduledPerformanceCount(t, takeover); got != 2 {
		t.Fatalf("C3 prefix was not exactly once: events=%d", got)
	}
	if got := strategyPerformanceCount(t, takeover); got != 2 {
		t.Fatalf("C3+D prefix was not exactly once: events=%d", got)
	}

	resumer := secondG38C2Service(t, takeover, time.Unix(0, claim.LeaseExpiresAtNS).UTC())
	resumedClaim, err := resumer.acquirePaperRunnerLease(context.Background(), k2aAccountRef)
	if err != nil || resumedClaim.FencingToken <= claim.FencingToken {
		t.Fatalf("C3+D takeover claim=%+v prior=%+v err=%v", resumedClaim, claim, err)
	}
	result, err := resumer.runDuePaperPerformancePolicyWithClaim(context.Background(), k2aAccountRef, resumedClaim)
	if err != nil || result == nil || result.PolicyEventID == "" {
		t.Fatalf("C3+D resumer did not finish policy result=%+v err=%v", result, err)
	}
	if got := scheduledPerformanceCount(t, resumer); got != 2 {
		t.Fatalf("resumer duplicated C3 events=%d", got)
	}
	if got := strategyPerformanceCount(t, resumer); got != 2 {
		t.Fatalf("resumer duplicated C3+D events=%d", got)
	}
	if got := g38EJournalCounts(t, resumer).Policy; got != 1 {
		t.Fatalf("resumer policy events=%d, want 1", got)
	}
}

func TestG38F2ExactHaltRollbackIsTheOnlySelectionChangeAllowedInClaim(t *testing.T) {
	svc, claim, _ := g38F2ClaimedDueWindow(t)
	result, err := svc.runDuePaperPerformancePolicyWithClaim(context.Background(), k2aAccountRef, claim)
	if err != nil || result == nil || result.Decision != "HALT_AND_ROLLBACK" || result.RollbackSelectionEventID == "" {
		t.Fatalf("exact policy rollback was rejected result=%+v err=%v", result, err)
	}
	_, currentEventID, selectedResult, err := currentPaperPerformanceSelection(context.Background(), svc.db)
	if err != nil || currentEventID != result.RollbackSelectionEventID || selectedResult != noStrategySelection {
		t.Fatalf("policy rollback was not the exact in-claim selection transition event=%s selected=%s err=%v", currentEventID, selectedResult, err)
	}
}

func TestG38F2GlobalClaimBlocksAnotherAccountPrefix(t *testing.T) {
	svc, primary := g38EPerformanceWindow(t, []string{"100"})
	secondary := g38ELaterOtherAccountStrategyPerformance(t, svc, primary)
	const secondAccount = "kiwoom_account_BBBBBBBBBBBBBBBBBBBBBBBB"
	asOf := "2026-01-16T06:30:00.000000000Z"
	svc.now = func() time.Time { return mustTime("2026-01-16T07:00:00Z") }
	recordG38C3MarkBar(t, svc, "005930", "g38f2-global-claim-second-account", asOf, "100")
	claim, err := svc.acquirePaperRunnerLease(context.Background(), k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := svc.acquirePaperRunnerLease(context.Background(), secondAccount); err == nil || other != nil {
		t.Fatalf("second account acquired overlapping global claim other=%+v err=%v", other, err)
	}
	before := scheduledPerformanceCount(t, svc)
	otherClaim := *claim
	otherClaim.AccountRef = secondAccount
	if event, err := svc.evaluatePaperPerformanceWithClaim(context.Background(), secondAccount, asOf, &otherClaim); err == nil || event != nil {
		t.Fatalf("second account advanced a prefix under global claim event=%+v err=%v selection=%s", event, err, secondary.StrategySelectionEventID)
	}
	if after := scheduledPerformanceCount(t, svc); after != before {
		t.Fatalf("global claim leak into second account before=%d after=%d", before, after)
	}
}

func TestG38F2ClaimedCachedRunStillRejectsCorruptedRoot(t *testing.T) {
	svc, claim, _ := g38F2ClaimedDueWindow(t)
	first, err := svc.runDuePaperPerformancePolicyWithClaim(context.Background(), k2aAccountRef, claim)
	if err != nil || first == nil {
		t.Fatalf("claimed run result=%+v err=%v", first, err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER paper_market_bar_observations_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE paper_market_bar_observations SET record_sha256=? WHERE sequence=1`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if retry, err := svc.runDuePaperPerformancePolicyWithClaim(context.Background(), k2aAccountRef, claim); err == nil || retry != nil {
		t.Fatalf("corrupted root returned cached claim result first=%+v retry=%+v err=%v", first, retry, err)
	}
}

func g38F2ClaimedDueWindow(t *testing.T) (*Service, *paperRunnerClaim, string) {
	t.Helper()
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	asOf := "2026-01-12T06:30:00.000000000Z"
	now := mustTime("2026-01-12T07:00:00Z")
	svc.now = func() time.Time { return now }
	recordG38C3MarkBar(t, svc, "005930", "g38f2-claimed-due-close", asOf, "10")
	claim, err := svc.acquirePaperRunnerLease(context.Background(), k2aAccountRef)
	if err != nil || claim == nil {
		t.Fatalf("acquire paper runner claim=%+v err=%v", claim, err)
	}
	return svc, claim, asOf
}
