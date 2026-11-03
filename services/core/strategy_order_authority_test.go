package main

import (
	"context"
	"testing"
	"time"
)

func TestG3StrategyOrderRequiresCurrentSelectionAtRecordAndDispatch(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	now := mustTime("2026-01-10T15:00:00Z")
	svc.now = func() time.Time { return now }
	evidence, err := svc.registerStrategyEvidence(context.Background(), strategyArtifact(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, noStrategySelectionEvent)
	if err != nil {
		t.Fatal(err)
	}
	intent := k2aIntent("client-strategy-fenced")
	intent.StrategyResultSHA256 = evidence.ResultSHA256
	intent.StrategySelectionEventID = selected.CurrentEventID
	order, err := svc.recordOrderIntent(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	lease := mustK2CLease(t, svc, order.AccountRef)
	rolledBack, err := svc.rollbackPaperCandidate(context.Background(), selected.CurrentEventID, selected.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := svc.recordOrderIntent(context.Background(), intent)
	if err != nil || replayed.OrderID != order.OrderID {
		t.Fatalf("idempotent retry lost the already-recorded order: replay=%+v err=%v", replayed, err)
	}
	if _, err := svc.authorizeSyntheticDispatch(context.Background(), order.OrderID, lease.FencingToken); err == nil {
		t.Fatal("rolled-back strategy reached dispatch")
	}
	state, err := svc.loadOrderState(context.Background(), order.OrderID)
	if err != nil || state.Status != "RECORDED" {
		t.Fatalf("stale strategy mutated order: state=%+v err=%v", state, err)
	}

	stale := k2aIntent("client-strategy-stale-record")
	stale.StrategyResultSHA256 = evidence.ResultSHA256
	stale.StrategySelectionEventID = selected.CurrentEventID
	if _, err := svc.recordOrderIntent(context.Background(), stale); err == nil {
		t.Fatal("stale strategy selection recorded a new order")
	}
	reselected, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, rolledBack.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	current := k2aIntent("client-strategy-current")
	current.StrategyResultSHA256 = evidence.ResultSHA256
	current.StrategySelectionEventID = reselected.CurrentEventID
	currentOrder, err := svc.recordOrderIntent(context.Background(), current)
	if err != nil {
		t.Fatal(err)
	}
	lease = mustK2CLease(t, svc, currentOrder.AccountRef)
	state, err = svc.authorizeSyntheticDispatch(context.Background(), currentOrder.OrderID, lease.FencingToken)
	if err != nil || state.Status != "SUBMIT_UNKNOWN" {
		t.Fatalf("current strategy did not reach guarded dispatch: state=%+v err=%v", state, err)
	}
}
