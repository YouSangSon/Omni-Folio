package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestG38C3BoundedAccounting(t *testing.T) {
	t.Run("excludes fills after the order sequence cutoff", func(t *testing.T) {
		fixture := g38c3TwoFillFixture(t)
		states, err := replayPaperAccountingAt(context.Background(), fixture.svc.db, paperAccountingCutoff{
			OrderSequence: fixture.firstFillSequence,
			AsOf:          fixture.secondBar.CloseAt,
		})
		if err != nil || states[k2aAccountRef].CapitalizedFills != 1 {
			t.Fatalf("bounded state=%+v err=%v", states[k2aAccountRef], err)
		}
	})

	t.Run("excludes a fill whose bar has not closed", func(t *testing.T) {
		fixture := g38c3TwoFillFixture(t)
		states, err := replayPaperAccountingAt(context.Background(), fixture.svc.db, paperAccountingCutoff{
			OrderSequence: fixture.secondFillSequence,
			AsOf:          "2026-01-11T03:00:00.000000000Z",
		})
		if err != nil || states[k2aAccountRef].CapitalizedFills != 1 {
			t.Fatalf("bounded state=%+v err=%v", states[k2aAccountRef], err)
		}
	})

	t.Run("validates a corrupt fill after the cutoff", func(t *testing.T) {
		fixture := g38c3TwoFillFixture(t)
		var raw string
		if err := fixture.svc.db.QueryRow(`SELECT event_json FROM order_events WHERE sequence=?`, fixture.secondFillSequence).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var event OrderEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		event.Price = "101"
		canonical, hash, err := orderJSONHash(event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.svc.db.Exec(`DROP TRIGGER order_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.svc.db.Exec(`UPDATE order_events SET event_json=?,event_sha256=? WHERE sequence=?`, string(canonical), hash, fixture.secondFillSequence); err != nil {
			t.Fatal(err)
		}
		if _, err := replayPaperAccountingAt(context.Background(), fixture.svc.db, paperAccountingCutoff{
			OrderSequence: fixture.firstFillSequence,
			AsOf:          fixture.firstBar.CloseAt,
		}); err == nil {
			t.Fatal("bounded replay hid a corrupt fill after its cutoff")
		}
	})

	t.Run("rejects a legacy paper account instead of returning cash only", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		svc.now = func() time.Time { return mustTime("2026-01-10T15:00:00Z") }
		evidence, selected := selectedPaperStrategy(t, svc)
		if _, err := svc.openPaperAccountingSession(context.Background(), k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID); err != nil {
			t.Fatal(err)
		}
		downgradePaperAuthorizationForTest(t, svc.db)
		legacy := PaperSignal{
			SchemaVersion: paperSignalSchema, SignalID: "g38c3-legacy-performance",
			StrategyResultSHA256: evidence.ResultSHA256, StrategySelectionEventID: selected.CurrentEventID,
			DataSHA256: strings.Repeat("d", 64), Symbol: "005930", TargetQuantity: "2",
			DataAsOf: "2026-01-10T14:59:00Z", GeneratedAt: "2026-01-10T14:59:01Z", ExpiresAt: "2026-01-10T15:01:00Z",
		}
		lease := mustK2CLease(t, svc, k2aAccountRef)
		if state, err := svc.runPaperSignal(context.Background(), k2aAccountRef, legacy, PaperMarketObservation{
			Source: "local_fixture", Symbol: legacy.Symbol, ObservedAt: "2026-01-10T15:00:00Z", AskPrice: "999", AvailableQuantity: "2",
		}, lease.FencingToken); err != nil || state.Status != "FILLED" {
			t.Fatalf("legacy state=%+v err=%v", state, err)
		}
		if err := migrate(svc.db); err != nil {
			t.Fatal(err)
		}
		var cutoff int64
		if err := svc.db.QueryRow(`SELECT COALESCE(MAX(sequence),0) FROM order_events`).Scan(&cutoff); err != nil {
			t.Fatal(err)
		}
		if _, err := replayPaperAccountingAt(context.Background(), svc.db, paperAccountingCutoff{
			OrderSequence: cutoff,
			AsOf:          "2026-01-10T15:00:00.000000000Z",
		}); err == nil {
			t.Fatal("bounded replay accepted a legacy paper account")
		}
	})
}

func TestG38C3CompleteMarks(t *testing.T) {
	t.Run("selects one complete close per symbol in symbol order", func(t *testing.T) {
		svc, state, session := g38c3TwoSymbolState(t)
		asOf := "2026-01-13T06:30:00.000000000Z"
		first := recordG38C3MarkBar(t, svc, "005930", "g38c3-mark-005930", asOf, "120")
		second := recordG38C3MarkBar(t, svc, "000660", "g38c3-mark-000660", asOf, "110")
		_, cutoff, err := loadPaperMarketBarByID(context.Background(), svc.db, second.ObservationID)
		if err != nil {
			t.Fatal(err)
		}
		marks, valuation, err := derivePaperPerformanceMarks(context.Background(), svc.db, state, session.StartingCash, asOf, second.RecordedAt, cutoff)
		if err != nil {
			t.Fatal(err)
		}
		if len(marks) != 2 || marks[0].Symbol != "000660" || marks[0].ObservationID != second.ObservationID ||
			marks[1].Symbol != "005930" || marks[1].ObservationID != first.ObservationID ||
			marks[0].Quantity != "2" || marks[0].OpenCost != "201.2" || marks[0].MarketValue != "220" || marks[0].UnrealizedPnL != "18.8" ||
			marks[1].Quantity != "2" || marks[1].OpenCost != "201.2" || marks[1].MarketValue != "240" || marks[1].UnrealizedPnL != "38.8" ||
			valuation.OpenCost != "402.4" || valuation.MarketValue != "460" || valuation.UnrealizedPnL != "57.6" || valuation.Equity != "10057.6" {
			t.Fatalf("marks=%+v valuation=%+v", marks, valuation)
		}
	})

	for _, test := range []struct {
		name   string
		setup  func(t *testing.T, svc *Service, asOf string)
		record string
	}{
		{"missing", func(t *testing.T, svc *Service, asOf string) {}, "2026-01-15T07:00:00.000000000Z"},
		{"ambiguous", func(t *testing.T, svc *Service, asOf string) {
			recordG38C3MarkBar(t, svc, "005930", "g38c3-ambiguous-a", asOf, "120")
			recordG38C3MarkBarAt(t, svc, "005930", "g38c3-ambiguous-b", "2026-01-11T00:00:01Z", asOf, "121")
		}, "2026-01-15T07:00:00.000000000Z"},
		{"wrong symbol series", func(t *testing.T, svc *Service, asOf string) {
			recordG38C3MarkBar(t, svc, "000660", "g38c3-wrong-series", asOf, "120")
		}, "2026-01-15T07:00:00.000000000Z"},
		{"future availability", func(t *testing.T, svc *Service, asOf string) {
			recordG38C3MarkBar(t, svc, "005930", "g38c3-future-availability", asOf, "120")
		}, "2026-01-11T06:30:00.000000000Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, state, session := g38c3HeldState(t)
			asOf := "2026-01-11T06:30:00.000000000Z"
			test.setup(t, svc, asOf)
			var cutoff int64
			if err := svc.db.QueryRow(`SELECT COALESCE(MAX(sequence),0) FROM paper_market_bar_observations`).Scan(&cutoff); err != nil {
				t.Fatal(err)
			}
			marks, valuation, err := derivePaperPerformanceMarks(context.Background(), svc.db, state, session.StartingCash, asOf, test.record, cutoff)
			if err == nil || len(marks) != 0 || valuation.Equity != "" {
				t.Fatalf("marks=%+v valuation=%+v err=%v", marks, valuation, err)
			}
		})
	}
}

func TestG38C3CashOnly(t *testing.T) {
	svc, _, anchor := g38c2PaperSignalFixture(t, true)
	states, err := replayPaperAccounting(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	session := g38c3Session(t, svc)
	_, cutoff, err := loadPaperMarketBarByID(context.Background(), svc.db, anchor.ObservationID)
	if err != nil {
		t.Fatal(err)
	}
	marks, valuation, err := derivePaperPerformanceMarks(context.Background(), svc.db, states[k2aAccountRef], session.StartingCash, anchor.CloseAt, anchor.RecordedAt, cutoff)
	if err != nil || len(marks) != 0 || valuation.Cash != session.StartingCash || valuation.Equity != session.StartingCash {
		t.Fatalf("marks=%+v valuation=%+v err=%v", marks, valuation, err)
	}
	for _, test := range []struct {
		name   string
		asOf   string
		cutoff int64
	}{
		{"bar after cutoff", anchor.CloseAt, cutoff - 1},
		{"arbitrary future", "2026-01-12T06:30:00.000000000Z", cutoff},
	} {
		t.Run(test.name, func(t *testing.T) {
			marks, valuation, err := derivePaperPerformanceMarks(context.Background(), svc.db, states[k2aAccountRef], session.StartingCash, test.asOf, anchor.RecordedAt, test.cutoff)
			if err == nil || len(marks) != 0 || valuation.Equity != "" {
				t.Fatalf("marks=%+v valuation=%+v err=%v", marks, valuation, err)
			}
		})
	}
}

type g38c3TwoFill struct {
	svc                                   *Service
	firstBar, secondBar                   *PaperMarketBarObservation
	firstFillSequence, secondFillSequence int64
}

func g38c3TwoFillFixture(t *testing.T) g38c3TwoFill {
	t.Helper()
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	svc.now = func() time.Time { return mustTime("2026-01-15T07:00:00Z") }
	signal.TargetQuantity, signal.ExpiresAt = "4", "2026-01-20T00:00:00.000000000Z"
	lease := mustK2CLease(t, svc, k2aAccountRef)
	_, order, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	first := recordG38C2FillBar(t, svc, "g38c3-first-fill", "100", "5", "2026-01-10")
	if state, err := svc.runPaperOrder(context.Background(), order.OrderID, lease.FencingToken); err != nil || state.FilledQuantity != "2" {
		t.Fatalf("first fill state=%+v err=%v", state, err)
	}
	second := recordG38C2FillBar(t, svc, "g38c3-second-fill", "100", "5", "2026-01-11")
	if state, err := svc.runPaperOrder(context.Background(), order.OrderID, lease.FencingToken); err != nil || state.FilledQuantity != "4" {
		t.Fatalf("second fill state=%+v err=%v", state, err)
	}
	rows, err := svc.db.Query(`SELECT sequence FROM order_events WHERE order_id=? AND event_type='FILL_RECORDED' ORDER BY sequence`, order.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var sequences []int64
	for rows.Next() {
		var sequence int64
		if err := rows.Scan(&sequence); err != nil {
			t.Fatal(err)
		}
		sequences = append(sequences, sequence)
	}
	if err := rows.Err(); err != nil || len(sequences) != 2 {
		t.Fatalf("fill sequences=%v err=%v", sequences, err)
	}
	return g38c3TwoFill{svc: svc, firstBar: first, secondBar: second, firstFillSequence: sequences[0], secondFillSequence: sequences[1]}
}

func g38c3HeldState(t *testing.T) (*Service, paperAccountState, *PaperAccountingSession) {
	t.Helper()
	svc, _ := g38c2FilledBuyForTest(t)
	states, err := replayPaperAccounting(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	return svc, states[k2aAccountRef], g38c3Session(t, svc)
}

func g38c3TwoSymbolState(t *testing.T) (*Service, paperAccountState, *PaperAccountingSession) {
	t.Helper()
	svc, _, session := g38c3HeldState(t)
	first, found, err := loadPaperSignalEvent(context.Background(), svc.db, k2aAccountRef, "g38c2-signal-001")
	if err != nil || !found {
		t.Fatalf("first signal found=%t err=%v", found, err)
	}
	initial := g38c2PaperMarketBar("g38c3-000660-signal", "2026-01-09T00:00:00Z", "2026-01-09T06:30:00Z")
	initial.Symbol = "000660"
	initial = *recordPaperMarketBarForG38C3(t, svc, initial)
	signal := PaperSignal{
		SchemaVersion: capitalizedPaperSignalSchema, SignalID: "g38c3-000660-signal",
		SignalBarObservationID: initial.ObservationID, StrategyResultSHA256: first.StrategyResultSHA256,
		StrategySelectionEventID: first.StrategySelectionEventID, DataSHA256: initial.InputDataSHA256,
		Symbol: initial.Symbol, TargetQuantity: "2", DataAsOf: initial.CloseAt,
		GeneratedAt: initial.SourceAvailableAt, ExpiresAt: "2026-01-20T00:00:00.000000000Z",
	}
	lease := mustK2CLease(t, svc, k2aAccountRef)
	_, order, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	recordG38C2SymbolFillBar(t, svc, "000660", "g38c3-000660-fill", "100", "5", "2026-01-12")
	if state, err := svc.runPaperOrder(context.Background(), order.OrderID, lease.FencingToken); err != nil || state.Status != "FILLED" {
		t.Fatalf("second symbol fill state=%+v err=%v", state, err)
	}
	states, err := replayPaperAccounting(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	return svc, states[k2aAccountRef], session
}

func recordG38C3MarkBar(t *testing.T, svc *Service, symbol, sourceID, closeAt, close string) *PaperMarketBarObservation {
	t.Helper()
	day := closeAt[:10]
	return recordG38C3MarkBarAt(t, svc, symbol, sourceID, day+"T00:00:00Z", closeAt, close)
}

func recordG38C3MarkBarAt(t *testing.T, svc *Service, symbol, sourceID, openAt, closeAt, close string) *PaperMarketBarObservation {
	t.Helper()
	day := closeAt[:10]
	bar := g38c2PaperMarketBar(sourceID, openAt, closeAt)
	bar.Symbol, bar.Open, bar.High, bar.Low, bar.Close, bar.Volume = symbol, close, close, close, close, "100"
	bar.SourceAvailableAt, bar.FetchedAt = day+"T06:31:00Z", day+"T06:32:00Z"
	return recordPaperMarketBarForG38C3(t, svc, bar)
}

func recordPaperMarketBarForG38C3(t *testing.T, svc *Service, bar PaperMarketBarObservation) *PaperMarketBarObservation {
	t.Helper()
	recorded, err := svc.recordPaperMarketBar(context.Background(), bar)
	if err != nil {
		t.Fatal(err)
	}
	return recorded
}

func g38c3Session(t *testing.T, svc *Service) *PaperAccountingSession {
	t.Helper()
	session, found, err := loadPaperAccountingSession(context.Background(), svc.db, k2aAccountRef)
	if err != nil || !found {
		t.Fatalf("session found=%t err=%v", found, err)
	}
	return session
}
