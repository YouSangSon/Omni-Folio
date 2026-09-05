package main

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestLocalPaperLostGlobalClaimCannotUseRetainedExecutionLease(t *testing.T) {
	svc, signal, _ := paperProposalFixture(t)
	ctx := context.Background()
	now := mustTime("2026-01-10T07:00:00Z")
	svc.now = func() time.Time { return now }

	claim, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := svc.startLocalPaperExecutionWithClaim(ctx, k2aAccountRef, signal.StrategySelectionEventID, claim)
	if err != nil {
		t.Fatal(err)
	}
	proposal := paperProposalForTest(t, svc, signal, "golden_cross")
	_, order, err := svc.processPaperProposal(ctx, k2aAccountRef, signal.StrategySelectionEventID, proposal,
		signal.SignalBarObservationID, lease.FencingToken, true, claim)
	if err != nil || order == nil || order.Status != "OPEN" {
		t.Fatalf("initial claimed admission order=%+v err=%v", order, err)
	}

	now = now.Add(paperRunnerLeaseTTL + time.Nanosecond)
	lease, err = svc.acquireSyntheticExecutionLease(ctx, k2aAccountRef)
	if err != nil {
		t.Fatalf("primary execution lease refresh=%+v err=%v", lease, err)
	}
	other := secondG38C2Service(t, svc, now)
	taken, err := other.acquirePaperRunnerLease(ctx, k2aAccountRef)
	if err != nil || taken.FencingToken <= claim.FencingToken || taken.OwnerID == claim.OwnerID {
		t.Fatalf("global claim takeover=%+v original=%+v err=%v", taken, claim, err)
	}

	before := paperAdmissionCountsForTest(t, svc)
	if _, err := svc.startLocalPaperExecutionWithClaim(ctx, k2aAccountRef, signal.StrategySelectionEventID, claim); err == nil {
		t.Fatal("lost global claim started local execution")
	}
	if _, err := svc.runPaperOrderWithClaim(ctx, order.OrderID, lease.FencingToken, claim); err == nil {
		t.Fatal("lost global claim filled paper order")
	}
	if _, _, err := svc.processPaperProposal(ctx, k2aAccountRef, signal.StrategySelectionEventID, proposal,
		signal.SignalBarObservationID, lease.FencingToken, true, claim); err == nil {
		t.Fatal("lost global claim admitted paper proposal")
	}
	if after := paperAdmissionCountsForTest(t, svc); !reflect.DeepEqual(after, before) {
		t.Fatalf("lost global claim wrote commands: before=%+v after=%+v", before, after)
	}
	if err := validatePaperRunnerLeaseTx(ctx, other.db, taken, k2aAccountRef, other.paperRunnerOwner, now); err != nil {
		t.Fatalf("takeover global claim was disturbed: %v", err)
	}

	if err := svc.haltOwnedSyntheticExecutionLease(ctx, k2aAccountRef, lease.FencingToken); err != nil {
		t.Fatalf("primary exact execution cleanup: %v", err)
	}
	if err := other.releasePaperRunnerLease(ctx, taken); err != nil {
		t.Fatalf("takeover exact global cleanup: %v", err)
	}
}
