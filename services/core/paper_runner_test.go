package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestG38C2PaperSignalCutoffOwnsLatestClosedBar(t *testing.T) {
	svc, signal, bar := g38c2PaperSignalFixture(t, true)
	event, err := recordG38C2PaperSignalForTest(svc, k2aAccountRef, signal)
	if err != nil {
		t.Fatal(err)
	}
	if event.SchemaVersion != "paper-signal.v3" || event.AccountRef != k2aAccountRef ||
		event.SignalBarObservationID != bar.ObservationID || event.DataAsOf != bar.CloseAt || event.DataSHA256 != bar.InputDataSHA256 ||
		event.TargetQuantity != "0" || event.MarketObservationSequenceCutoff != 1 || event.RecordedAt != "2026-01-10T07:00:00Z" {
		t.Fatalf("signal cutoff event=%+v bar=%+v", event, bar)
	}
	replayed, err := recordG38C2PaperSignalForTest(svc, k2aAccountRef, signal)
	if err != nil || *replayed != *event {
		t.Fatalf("signal retry event=%+v replay=%+v err=%v", event, replayed, err)
	}
	svc.now = func() time.Time { return mustTime("2026-01-12T00:00:00Z") }
	replayed, err = recordG38C2PaperSignalForTest(svc, k2aAccountRef, signal)
	if err != nil || *replayed != *event {
		t.Fatalf("expired exact signal retry event=%+v replay=%+v err=%v", event, replayed, err)
	}

	svc2, retroactive, _ := g38c2PaperSignalFixture(t, true)
	svc2.now = func() time.Time { return mustTime("2026-01-11T07:00:00Z") }
	next := g38c2PaperMarketBar("fixture-005930-20260110", "2026-01-10T00:00:00Z", "2026-01-10T06:30:00Z")
	next.SourceAvailableAt, next.FetchedAt = "2026-01-10T06:31:00Z", "2026-01-10T06:32:00Z"
	if _, err := svc2.recordPaperMarketBar(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	retroactive.GeneratedAt, retroactive.ExpiresAt = "2026-01-10T06:31:00Z", "2026-01-12T00:00:00Z"
	before := g38c2PaperSignalSideEffects(t, svc2)
	if _, err := recordG38C2PaperSignalForTest(svc2, k2aAccountRef, retroactive); err == nil {
		t.Fatal("retroactive signal used a bar that was no longer latest at the transaction cutoff")
	}
	assertG38C2PaperSignalSideEffects(t, before, g38c2PaperSignalSideEffects(t, svc2))
}

func TestG38C2PaperSignalCutoffFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*testing.T) (*Service, string, PaperSignal)
		mutate func(*testing.T, *Service, *string, *PaperSignal)
	}{
		{
			name: "missing session",
			setup: func(t *testing.T) (*Service, string, PaperSignal) {
				svc, signal, _ := g38c2PaperSignalFixture(t, false)
				return svc, k2aAccountRef, signal
			},
		},
		{
			name: "policy mismatch",
			setup: func(t *testing.T) (*Service, string, PaperSignal) {
				svc, signal, _ := g38c2PaperSignalFixture(t, true)
				ctx := context.Background()
				next, err := svc.registerStrategyEvidence(ctx, rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(result map[string]any) {
					result["experiment_id"] = "g38c2-policy-mismatch"
					result["execution"].(map[string]any)["starting_cash"] = "20000"
				}))
				if err != nil {
					t.Fatal(err)
				}
				selected, err := svc.selectPaperCandidate(ctx, next.ResultSHA256, signal.StrategySelectionEventID)
				if err != nil {
					t.Fatal(err)
				}
				signal.StrategyResultSHA256, signal.StrategySelectionEventID = next.ResultSHA256, selected.CurrentEventID
				return svc, k2aAccountRef, signal
			},
		},
		{
			name: "stale selection",
			setup: func(t *testing.T) (*Service, string, PaperSignal) {
				svc, signal, _ := g38c2PaperSignalFixture(t, true)
				ctx := context.Background()
				next, err := svc.registerStrategyEvidence(ctx, strategyArtifact(t, func(config map[string]any) {
					config["experiment_id"] = "g38c2-stale-selection"
					config["strategy"].(map[string]any)["version"] = "1.0.9"
				}))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := svc.selectPaperCandidate(ctx, next.ResultSHA256, signal.StrategySelectionEventID); err != nil {
					t.Fatal(err)
				}
				return svc, k2aAccountRef, signal
			},
		},
		{name: "data hash mismatch", mutate: func(_ *testing.T, _ *Service, _ *string, signal *PaperSignal) {
			signal.DataSHA256 = strings.Repeat("b", 64)
		}},
		{name: "data as of mismatch", mutate: func(_ *testing.T, _ *Service, _ *string, signal *PaperSignal) {
			signal.DataAsOf = "2026-01-09T06:29:59Z"
		}},
		{name: "generation before availability", mutate: func(_ *testing.T, _ *Service, _ *string, signal *PaperSignal) {
			signal.GeneratedAt = "2026-01-09T06:30:59Z"
		}},
		{name: "future generation", mutate: func(_ *testing.T, _ *Service, _ *string, signal *PaperSignal) {
			signal.GeneratedAt, signal.ExpiresAt = "2026-01-10T07:00:00.000000001Z", "2026-01-11T00:00:00Z"
		}},
		{name: "expired signal", mutate: func(_ *testing.T, _ *Service, _ *string, signal *PaperSignal) {
			signal.ExpiresAt = "2026-01-10T06:59:59Z"
		}},
		{name: "noncanonical zero", mutate: func(_ *testing.T, _ *Service, _ *string, signal *PaperSignal) { signal.TargetQuantity = "00" }},
		{name: "negative target", mutate: func(_ *testing.T, _ *Service, _ *string, signal *PaperSignal) { signal.TargetQuantity = "-1" }},
		{
			name: "legacy paper order",
			mutate: func(t *testing.T, svc *Service, _ *string, signal *PaperSignal) {
				insertG38C2LegacyPaperOrder(t, svc, *signal)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var svc *Service
			var account string
			var signal PaperSignal
			if test.setup != nil {
				svc, account, signal = test.setup(t)
			} else {
				svc, signal, _ = g38c2PaperSignalFixture(t, true)
				account = k2aAccountRef
			}
			if test.mutate != nil {
				test.mutate(t, svc, &account, &signal)
			}
			before := g38c2PaperSignalSideEffects(t, svc)
			if _, err := recordG38C2PaperSignalForTest(svc, account, signal); err == nil {
				t.Fatal("invalid signal cutoff was accepted")
			}
			assertG38C2PaperSignalSideEffects(t, before, g38c2PaperSignalSideEffects(t, svc))
		})
	}
}

func g38c2PaperSignalFixture(t *testing.T, openSession bool) (*Service, PaperSignal, *PaperMarketBarObservation) {
	t.Helper()
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-10T07:00:00Z") }
	ctx := context.Background()
	evidence, selected := selectedPaperStrategy(t, svc)
	if openSession {
		if _, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID); err != nil {
			t.Fatal(err)
		}
	}
	bar, err := svc.recordPaperMarketBar(ctx, g38c2PaperMarketBar("fixture-005930-20260109", "2026-01-09T00:00:00Z", "2026-01-09T06:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	return svc, PaperSignal{
		SchemaVersion: "paper-signal.v3", SignalID: "g38c2-signal-001", SignalBarObservationID: bar.ObservationID,
		StrategyResultSHA256: evidence.ResultSHA256, StrategySelectionEventID: selected.CurrentEventID,
		DataSHA256: bar.InputDataSHA256, Symbol: bar.Symbol, TargetQuantity: "0", DataAsOf: bar.CloseAt,
		GeneratedAt: bar.SourceAvailableAt, ExpiresAt: "2026-01-11T00:00:00Z",
	}, bar
}

func recordG38C2PaperSignalForTest(svc *Service, account string, signal PaperSignal) (*PaperSignalEvent, error) {
	tx, err := svc.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	event, err := svc.recordPaperSignalEventTx(context.Background(), tx, account, signal)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return event, nil
}

type g38c2SignalSideEffects struct {
	Signals, Orders, Reservations, Fills int
}

func g38c2PaperSignalSideEffects(t testing.TB, svc *Service) g38c2SignalSideEffects {
	t.Helper()
	var result g38c2SignalSideEffects
	for query, destination := range map[string]*int{
		`SELECT COUNT(*) FROM paper_signal_events`:                           &result.Signals,
		`SELECT COUNT(*) FROM order_idempotency`:                             &result.Orders,
		`SELECT COUNT(*) FROM risk_reservations`:                             &result.Reservations,
		`SELECT COUNT(*) FROM order_events WHERE event_type='FILL_RECORDED'`: &result.Fills,
	} {
		if err := svc.db.QueryRow(query).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func assertG38C2PaperSignalSideEffects(t testing.TB, before, after g38c2SignalSideEffects) {
	t.Helper()
	if before != after {
		t.Fatalf("rejected signal side effects before=%+v after=%+v", before, after)
	}
}

func insertG38C2LegacyPaperOrder(t testing.TB, svc *Service, signal PaperSignal) {
	t.Helper()
	legacy := signal
	legacy.SchemaVersion, legacy.TargetQuantity, legacy.SignalBarObservationID = legacyPaperSignalSchema, "", ""
	intent := paperOrderIntent(k2aAccountRef, legacy, "1", "1000")
	intent.SignalTargetQuantity = ""
	intentJSON, requestSHA, err := orderJSONHash(intent)
	if err != nil {
		t.Fatal(err)
	}
	orderID := "order_g38c2_legacy"
	if _, err := svc.db.Exec(`INSERT INTO order_idempotency(provider,mode,account_ref,client_order_id,request_sha256,order_id,intent_json,recorded_at)
		VALUES(?,?,?,?,?,?,?,?)`, intent.Provider, intent.Mode, intent.AccountRef, intent.ClientOrderID, requestSHA, orderID, string(intentJSON), svc.now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	event := OrderEvent{EventID: "event_g38c2_legacy", OrderID: orderID, Type: "INTENT_RECORDED", Source: "synthetic"}
	eventJSON, eventSHA, err := orderJSONHash(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO order_events(event_id,event_sha256,order_id,event_type,source,event_json,recorded_at)
		VALUES(?,?,?,?,?,?,?)`, event.EventID, eventSHA, event.OrderID, event.Type, event.Source, string(eventJSON), svc.now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

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
	if err != nil || manifest.SchemaVersion != "omni-folio.sqlite.v16" {
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

func TestG3PaperRunnerRequiresCurrentLeaseBeforeRecording(t *testing.T) {
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
		SchemaVersion: paperSignalSchema, SignalID: "paper-requires-lease",
		StrategyResultSHA256: evidence.ResultSHA256, StrategySelectionEventID: selected.CurrentEventID,
		DataSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Symbol:     "005930", TargetQuantity: "1", DataAsOf: "2026-01-10T14:59:00Z",
		GeneratedAt: "2026-01-10T14:59:01Z", ExpiresAt: "2026-01-10T15:01:00Z",
	}
	observation := PaperMarketObservation{
		Source: "local_fixture", Symbol: signal.Symbol, ObservedAt: "2026-01-10T15:00:00Z",
		AskPrice: "999", AvailableQuantity: "1",
	}
	armed, err := svc.setSyntheticExecutionArmed(context.Background(), k2aAccountRef, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.runPaperSignal(context.Background(), k2aAccountRef, signal, observation, armed.FencingToken); err == nil {
		t.Fatal("paper signal without a current lease was recorded")
	}
	var orders int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_idempotency WHERE mode='paper'`).Scan(&orders); err != nil || orders != 0 {
		t.Fatalf("lease rejection left a paper order: count=%d err=%v", orders, err)
	}
	lease, err := svc.acquireSyntheticExecutionLease(context.Background(), k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	state, err := svc.runPaperSignal(context.Background(), k2aAccountRef, signal, observation, lease.FencingToken)
	if err != nil || state.Status != "FILLED" {
		t.Fatalf("current lease did not admit paper order: state=%+v err=%v", state, err)
	}
}

func TestG3PaperRunnerRollsBackIntentWhenDispatchAuthorizationFails(t *testing.T) {
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
	lease := mustK2CLease(t, svc, k2aAccountRef)
	unresolved, err := svc.recordOrderIntent(context.Background(), k2aIntent("paper-account-blocker"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.authorizeSyntheticDispatch(context.Background(), unresolved.OrderID, lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	signal := PaperSignal{
		SchemaVersion: paperSignalSchema, SignalID: "paper-authorization-fails",
		StrategyResultSHA256: evidence.ResultSHA256, StrategySelectionEventID: selected.CurrentEventID,
		DataSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Symbol:     "000660", TargetQuantity: "1", DataAsOf: "2026-01-10T14:59:00Z",
		GeneratedAt: "2026-01-10T14:59:01Z", ExpiresAt: "2026-01-10T15:01:00Z",
	}
	observation := PaperMarketObservation{
		Source: "local_fixture", Symbol: signal.Symbol, ObservedAt: "2026-01-10T15:00:00Z",
		AskPrice: "999", AvailableQuantity: "1",
	}
	if _, err := svc.runPaperSignal(context.Background(), k2aAccountRef, signal, observation, lease.FencingToken); err == nil {
		t.Fatal("account block did not reject paper dispatch")
	}
	var orders int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_idempotency WHERE mode='paper'`).Scan(&orders); err != nil || orders != 0 {
		t.Fatalf("failed dispatch left a paper intent: count=%d err=%v", orders, err)
	}
}

func TestG3PaperRollbackAtomicallyHaltsExecution(t *testing.T) {
	setup := func(t *testing.T) (*Service, *StrategySelectionState, *ExecutionAuthorityState) {
		t.Helper()
		svc, _ := testService(t, nil, nil)
		svc.now = func() time.Time { return mustTime("2026-01-10T15:00:00Z") }
		evidence, err := svc.registerStrategyEvidence(context.Background(), strategyArtifact(t, nil))
		if err != nil {
			t.Fatal(err)
		}
		selected, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, noStrategySelectionEvent)
		if err != nil {
			t.Fatal(err)
		}
		return svc, selected, mustK2CLease(t, svc, k2aAccountRef)
	}

	t.Run("success", func(t *testing.T) {
		svc, selected, lease := setup(t)
		secondAccount := "kiwoom_account_BBBBBBBBBBBBBBBBBBBBBBBB"
		secondLease := mustK2CLease(t, svc, secondAccount)
		rolledBack, err := svc.rollbackPaperCandidate(context.Background(), selected.CurrentEventID, selected.CurrentEventID)
		if err != nil || rolledBack.SelectedResultSHA256 != noStrategySelection {
			t.Fatalf("strategy rollback state=%+v err=%v", rolledBack, err)
		}
		authority, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
		if err != nil || authority.Armed || authority.LeaseOwner != "" || authority.LeaseExpiresAt != "" || authority.FencingToken != lease.FencingToken+1 {
			t.Fatalf("rollback did not halt and fence execution: authority=%+v err=%v", authority, err)
		}
		authority, err = loadExecutionAuthoritySnapshot(context.Background(), svc.db, secondAccount)
		if err != nil || authority.Armed || authority.LeaseOwner != "" || authority.LeaseExpiresAt != "" || authority.FencingToken != secondLease.FencingToken+1 {
			t.Fatalf("rollback did not halt every execution account: authority=%+v err=%v", authority, err)
		}
	})

	t.Run("failure rolls back halt", func(t *testing.T) {
		svc, selected, lease := setup(t)
		svc.id = func(string) string { return selected.CurrentEventID }
		if _, err := svc.rollbackPaperCandidate(context.Background(), selected.CurrentEventID, selected.CurrentEventID); err == nil {
			t.Fatal("duplicate rollback event unexpectedly committed")
		}
		authority, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
		if err != nil || !authority.Armed || authority.LeaseOwner == "" || authority.FencingToken != lease.FencingToken {
			t.Fatalf("failed rollback leaked execution halt: authority=%+v err=%v", authority, err)
		}
		registry, err := replayStrategyRegistry(context.Background(), svc.db)
		if err != nil || registry.CurrentEventID != selected.CurrentEventID || registry.SelectedResultSHA256 != selected.SelectedResultSHA256 {
			t.Fatalf("failed rollback changed strategy registry: state=%+v err=%v", registry, err)
		}
	})
}
