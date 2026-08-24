package main

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestG3PaperRunnerUsesSelectedStrategyRiskOrderReplayAndBackup(t *testing.T) {
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
	signal := PaperSignal{
		SchemaVersion: paperSignalSchema, SignalID: "paper-signal-001",
		StrategyResultSHA256: evidence.ResultSHA256, StrategySelectionEventID: selected.CurrentEventID,
		DataSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Symbol:     "005930", TargetQuantity: "10",
		DataAsOf: "2026-01-10T14:59:00Z", GeneratedAt: "2026-01-10T14:59:01Z", ExpiresAt: "2026-01-10T15:01:00Z",
	}
	lease := mustK2CLease(t, svc, k2aAccountRef)
	first := PaperMarketObservation{
		Source: "local_fixture", Symbol: signal.Symbol, ObservedAt: "2026-01-10T15:00:00Z",
		AskPrice: "999", AvailableQuantity: "4",
	}
	state, err := svc.runPaperSignal(context.Background(), k2aAccountRef, signal, first, lease.FencingToken)
	if err != nil || state.Status != "PARTIALLY_FILLED" || state.FilledQuantity != "4" {
		t.Fatalf("first paper fill state=%+v err=%v", state, err)
	}
	client := newSyntheticKiwoomClient(t, KiwoomMock, kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("paper order reached Kiwoom transport")
		return nil, nil
	}))
	if submitted, err := svc.submitAuthorizedKiwoomMockOrder(context.Background(), client, state.OrderID, lease.FencingToken); err == nil || submitted != nil {
		t.Fatalf("paper order entered Kiwoom submit: state=%+v err=%v", submitted, err)
	}
	replayed, err := svc.runPaperSignal(context.Background(), k2aAccountRef, signal, first, lease.FencingToken)
	if err != nil || replayed.OrderID != state.OrderID || replayed.FilledQuantity != "4" {
		t.Fatalf("paper observation was not idempotent: state=%+v err=%v", replayed, err)
	}
	intent, err := loadOrderIntentFrom(context.Background(), svc.db, state.OrderID)
	if err != nil || intent.Mode != "paper" || intent.StrategyResultSHA256 != evidence.ResultSHA256 ||
		intent.StrategySelectionEventID != selected.CurrentEventID || intent.SignalID != signal.SignalID || intent.SignalDataSHA256 != signal.DataSHA256 ||
		intent.SignalTargetQuantity != signal.TargetQuantity || intent.Side != "BUY" || intent.Quantity != "10" || intent.LimitPrice != first.AskPrice {
		t.Fatalf("paper intent lost strategy binding: intent=%+v err=%v", intent, err)
	}
	if _, err := svc.rollbackPaperCandidate(context.Background(), selected.CurrentEventID, selected.CurrentEventID); err != nil {
		t.Fatal(err)
	}
	now = mustTime("2026-01-10T15:02:00Z")
	second := PaperMarketObservation{
		Source: "local_fixture", Symbol: signal.Symbol, ObservedAt: "2026-01-10T15:02:00Z",
		AskPrice: "998", AvailableQuantity: "10",
	}
	state, err = svc.runPaperSignal(context.Background(), k2aAccountRef, signal, second, lease.FencingToken)
	if err != nil || state.Status != "FILLED" || state.FilledQuantity != "10" {
		t.Fatalf("dispatched paper order did not finish after rollback/expiry: state=%+v err=%v", state, err)
	}

	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "paper-backup.db")
	manifest, err := createBackup(svc.db, backup, golden, backup+".manifest.json", svc.now, svc.id)
	if err != nil || manifest.SchemaVersion != "omni-folio.sqlite.v7" {
		t.Fatalf("paper-aware backup manifest=%+v err=%v", manifest, err)
	}
	restoredDB, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close()
	restored, err := newService(restoredDB, time.Now, randomID).loadOrderState(context.Background(), state.OrderID)
	if err != nil || restored.Status != "FILLED" || restored.FilledQuantity != "10" {
		t.Fatalf("paper order did not survive restore: state=%+v err=%v", restored, err)
	}
}

func TestG3PaperRunnerRejectsExpiredStaleOrUnsafeInputsBeforeOrderCreation(t *testing.T) {
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
	base := PaperSignal{
		SchemaVersion: paperSignalSchema, SignalID: "paper-signal-rejected",
		StrategyResultSHA256: evidence.ResultSHA256, StrategySelectionEventID: selected.CurrentEventID,
		DataSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Symbol:     "005930", TargetQuantity: "10",
		DataAsOf: "2026-01-10T14:58:00Z", GeneratedAt: "2026-01-10T14:59:00Z", ExpiresAt: "2026-01-10T14:59:59Z",
	}
	observation := PaperMarketObservation{
		Source: "local_fixture", Symbol: base.Symbol, ObservedAt: "2026-01-10T15:00:00Z",
		AskPrice: "999", AvailableQuantity: "10",
	}
	if _, err := svc.runPaperSignal(context.Background(), k2aAccountRef, base, observation, 1); err == nil {
		t.Fatal("expired paper signal created an order")
	}
	if _, err := svc.rollbackPaperCandidate(context.Background(), selected.CurrentEventID, selected.CurrentEventID); err != nil {
		t.Fatal(err)
	}
	base.SignalID = "paper-signal-stale"
	base.ExpiresAt = "2026-01-10T15:01:00Z"
	if _, err := svc.runPaperSignal(context.Background(), k2aAccountRef, base, observation, 1); err == nil {
		t.Fatal("rolled-back strategy created a paper order")
	}
	var orders int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_idempotency WHERE mode='paper'`).Scan(&orders); err != nil || orders != 0 {
		t.Fatalf("rejected signal left paper orders: count=%d err=%v", orders, err)
	}
}

func TestG3PaperRunnerNetsTargetsAgainstFilledAndOutstandingOrders(t *testing.T) {
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
	signal := PaperSignal{
		SchemaVersion: paperSignalSchema, SignalID: "paper-target-10",
		StrategyResultSHA256: evidence.ResultSHA256, StrategySelectionEventID: selected.CurrentEventID,
		DataSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Symbol:     "005930", TargetQuantity: "10", DataAsOf: "2026-01-10T14:59:00Z",
		GeneratedAt: "2026-01-10T14:59:01Z", ExpiresAt: "2026-01-10T15:10:00Z",
	}
	lease := mustK2CLease(t, svc, k2aAccountRef)
	observation := PaperMarketObservation{Source: "local_fixture", Symbol: signal.Symbol, ObservedAt: "2026-01-10T15:00:00Z", AskPrice: "1000", AvailableQuantity: "4"}
	first, err := svc.runPaperSignal(context.Background(), k2aAccountRef, signal, observation, lease.FencingToken)
	if err != nil || first.Status != "PARTIALLY_FILLED" || first.FilledQuantity != "4" {
		t.Fatalf("initial target state=%+v err=%v", first, err)
	}
	sameTarget := signal
	sameTarget.SignalID = "paper-target-10-again"
	if state, err := svc.runPaperSignal(context.Background(), k2aAccountRef, sameTarget, observation, lease.FencingToken); err != nil || state != nil {
		t.Fatalf("outstanding quantity was not netted: state=%+v err=%v", state, err)
	}

	observation.ObservedAt = "2026-01-10T15:00:01Z"
	observation.AvailableQuantity = "10"
	now = mustTime(observation.ObservedAt)
	if state, err := svc.runPaperSignal(context.Background(), k2aAccountRef, signal, observation, lease.FencingToken); err != nil || state.Status != "FILLED" {
		t.Fatalf("initial target did not finish: state=%+v err=%v", state, err)
	}
	higherTarget := signal
	higherTarget.SignalID = "paper-target-15"
	higherTarget.TargetQuantity = "15"
	higherTarget.GeneratedAt = "2026-01-10T14:59:02Z"
	observation.ObservedAt = "2026-01-10T15:00:02Z"
	now = mustTime(observation.ObservedAt)
	second, err := svc.runPaperSignal(context.Background(), k2aAccountRef, higherTarget, observation, lease.FencingToken)
	if err != nil || second.Status != "FILLED" || second.Quantity != "5" {
		t.Fatalf("higher target did not produce delta order: state=%+v err=%v", second, err)
	}
}

func TestG3PaperRunnerSerializesConcurrentTargets(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	now := mustTime("2026-01-10T15:00:00Z")
	svc.now = func() time.Time { return now }
	svc.id = randomID
	evidence, err := svc.registerStrategyEvidence(context.Background(), strategyArtifact(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, noStrategySelectionEvent)
	if err != nil {
		t.Fatal(err)
	}
	base := PaperSignal{
		SchemaVersion: paperSignalSchema, StrategyResultSHA256: evidence.ResultSHA256,
		StrategySelectionEventID: selected.CurrentEventID,
		DataSHA256:               "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Symbol:                   "005930", TargetQuantity: "10", DataAsOf: "2026-01-10T14:59:00Z",
		GeneratedAt: "2026-01-10T14:59:01Z", ExpiresAt: "2026-01-10T15:10:00Z",
	}
	lease := mustK2CLease(t, svc, k2aAccountRef)
	observation := PaperMarketObservation{Source: "local_fixture", Symbol: base.Symbol, ObservedAt: "2026-01-10T15:00:00Z", AskPrice: "1000", AvailableQuantity: "10"}
	type result struct {
		state *OrderState
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, signalID := range []string{"concurrent-target-a", "concurrent-target-b"} {
		signal := base
		signal.SignalID = signalID
		go func() {
			<-start
			state, err := svc.runPaperSignal(context.Background(), k2aAccountRef, signal, observation, lease.FencingToken)
			results <- result{state: state, err: err}
		}()
	}
	close(start)
	filled, noOp := 0, 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.state == nil {
			noOp++
		} else if result.state.Status == "FILLED" && result.state.Quantity == "10" {
			filled++
		}
	}
	var orders int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_idempotency WHERE mode='paper'`).Scan(&orders); err != nil || filled != 1 || noOp != 1 || orders != 1 {
		t.Fatalf("concurrent targets were not serialized: filled=%d no_op=%d orders=%d err=%v", filled, noOp, orders, err)
	}
}
