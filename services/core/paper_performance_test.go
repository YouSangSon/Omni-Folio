package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestG38C3PaperPerformanceSeriesAndRetry(t *testing.T) {
	t.Run("cash only baseline", func(t *testing.T) {
		svc, asOf := g38c3CashPerformanceFixture(t)
		event, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf)
		if err != nil {
			t.Fatal(err)
		}
		if event.SchemaVersion != "paper-performance-evaluation.v1" || event.PolicyVersion != "paper-performance-account-v1" ||
			event.ExpectedPreviousPerformanceID != "no_performance" || event.SelectedStrategyResultRef == "" ||
			event.MarkCount != 0 || event.MarksJSON != "[]" || event.Cash != "10000" || event.OpenCost != "0" ||
			event.MarketValue != "0" || event.RealizedPnL != "0" || event.UnrealizedPnL != "0" || event.TotalPnL != "0" ||
			event.Equity != "10000" || event.PeakEquity != "10000" || event.PeriodReturnState != "defined" ||
			event.PeriodReturn != "0" || event.CumulativeReturn != "0" || event.Drawdown != "0" || event.MaxDrawdown != "0" {
			t.Fatalf("cash-only event=%+v", event)
		}
		var selectionCutoff, orderCutoff, marketCutoff int64
		if err := svc.db.QueryRow(`SELECT COALESCE(MAX(sequence),0) FROM strategy_selection_events`).Scan(&selectionCutoff); err != nil {
			t.Fatal(err)
		}
		if err := svc.db.QueryRow(`SELECT COALESCE(MAX(sequence),0) FROM order_events`).Scan(&orderCutoff); err != nil {
			t.Fatal(err)
		}
		if err := svc.db.QueryRow(`SELECT COALESCE(MAX(sequence),0) FROM paper_market_bar_observations`).Scan(&marketCutoff); err != nil {
			t.Fatal(err)
		}
		if event.StrategySelectionSequenceCutoff != selectionCutoff || event.OrderEventSequenceCutoff != orderCutoff ||
			event.PaperMarketSequenceCutoff != marketCutoff {
			t.Fatalf("non-current transaction cutoffs: event=%+v current=(%d,%d,%d)", event, selectionCutoff, orderCutoff, marketCutoff)
		}
		proof, err := provePaperPerformanceRecovery(context.Background(), svc.db)
		if err != nil || proof.Events != 1 || proof.Marks != 0 || proof.SHA256 == "" {
			t.Fatalf("cash-only proof=%+v err=%v", proof, err)
		}
	})

	t.Run("up down recovery series and exact retry", func(t *testing.T) {
		svc, asOfs := g38c3HeldPerformanceFixture(t)
		want := []struct {
			equity, peak, period, cumulative, drawdown, maxDrawdown string
		}{
			{"10038.8", "10038.8", "0.00388", "0.00388", "0", "0"},
			{"9958.8", "10038.8", "-0.00796908", "-0.00412", "0.00796908", "0.00796908"},
			{"10018.8", "10038.8", "0.00602482", "0.00188", "0.00199227", "0.00796908"},
		}
		var previous string
		for index, asOf := range asOfs {
			event, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf)
			if err != nil {
				t.Fatal(err)
			}
			if event.ExpectedPreviousPerformanceID != map[bool]string{true: "no_performance", false: previous}[index == 0] ||
				event.MarkCount != 1 || event.Equity != want[index].equity || event.PeakEquity != want[index].peak ||
				event.PeriodReturn != want[index].period || event.CumulativeReturn != want[index].cumulative ||
				event.Drawdown != want[index].drawdown || event.MaxDrawdown != want[index].maxDrawdown {
				t.Fatalf("point %d=%+v", index, event)
			}
			previous = event.PerformanceID
		}
		first, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOfs[0])
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_performance_events`).Scan(&count); err != nil || count != 3 {
			t.Fatalf("retry rows=%d err=%v", count, err)
		}
		if first.AsOf != asOfs[0] {
			t.Fatalf("retry returned wrong event: %+v", first)
		}
	})
}

func TestG38C3PaperPerformanceWindowSelectionAndDurability(t *testing.T) {
	t.Run("requires session close before as-of and record time at or after it", func(t *testing.T) {
		svc, _ := g38c3CashPerformanceFixture(t)
		session := g38c3Session(t, svc)
		for _, asOf := range []string{"2026-01-09T06:30:00.000000000Z", session.RecordedAt} {
			if _, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf); err == nil {
				t.Fatalf("invalid as_of %s was accepted", asOf)
			}
		}
		svc.now = func() time.Time { return mustTime("2026-01-11T06:29:59Z") }
		if _, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, "2026-01-11T06:30:00.000000000Z"); err == nil {
			t.Fatal("future as_of was accepted")
		}
	})

	t.Run("captures current selection and rollback to no_strategy", func(t *testing.T) {
		svc, asOf := g38c3CashPerformanceFixture(t)
		first, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf)
		if err != nil {
			t.Fatal(err)
		}
		rolled, err := svc.rollbackPaperCandidate(context.Background(), first.StrategySelectionEventID, first.StrategySelectionEventID)
		if err != nil || rolled.SelectedResultSHA256 != noStrategySelection {
			t.Fatalf("rollback=%+v err=%v", rolled, err)
		}
		svc.now = func() time.Time { return mustTime("2026-01-12T07:00:00Z") }
		nextAsOf := "2026-01-12T06:30:00.000000000Z"
		recordG38C3MarkBar(t, svc, "005930", "g38c3-cash-rollback", nextAsOf, "100")
		second, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, nextAsOf)
		if err != nil {
			t.Fatal(err)
		}
		if second.SelectedStrategyResultRef != noStrategySelection || second.StrategySelectionEventID != rolled.CurrentEventID ||
			second.StrategySelectionSequenceCutoff <= first.StrategySelectionSequenceCutoff {
			t.Fatalf("rollback event=%+v", second)
		}
	})

	t.Run("captures a changed current strategy", func(t *testing.T) {
		svc, asOf := g38c3CashPerformanceFixture(t)
		first, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf)
		if err != nil {
			t.Fatal(err)
		}
		artifact := rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(result map[string]any) {
			result["experiment_id"] = "g38c3-selection-change"
		})
		evidence, err := svc.registerStrategyEvidence(context.Background(), artifact)
		if err != nil {
			t.Fatal(err)
		}
		selected, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, first.StrategySelectionEventID)
		if err != nil {
			t.Fatal(err)
		}
		svc.now = func() time.Time { return mustTime("2026-01-12T07:00:00Z") }
		nextAsOf := "2026-01-12T06:30:00.000000000Z"
		recordG38C3MarkBar(t, svc, "005930", "g38c3-selection-change", nextAsOf, "100")
		second, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, nextAsOf)
		if err != nil {
			t.Fatal(err)
		}
		if second.SelectedStrategyResultRef != evidence.ResultSHA256 || second.StrategySelectionEventID != selected.CurrentEventID {
			t.Fatalf("strategy change event=%+v", second)
		}
	})

	t.Run("is insert only and rejects a changed duplicate", func(t *testing.T) {
		svc, asOf := g38c3CashPerformanceFixture(t)
		if _, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf); err != nil {
			t.Fatal(err)
		}
		for _, statement := range []string{
			`UPDATE paper_performance_events SET cash='1'`,
			`DELETE FROM paper_performance_events`,
			`INSERT INTO paper_performance_events(
				performance_id,schema_version,policy_version,account_ref,paper_accounting_session_id,
				strategy_selection_event_id,selected_strategy_result_ref,expected_previous_performance_id,
				strategy_selection_sequence_cutoff,order_event_sequence_cutoff,paper_market_sequence_cutoff,as_of,
				paper_account_state_sha256,marks_sha256,marks_json,mark_count,cash,open_cost,market_value,realized_pnl,
				unrealized_pnl,total_pnl,equity,peak_equity,period_return_state,period_return,cumulative_return,drawdown,
				max_drawdown,record_sha256,record_json,recorded_at)
			 SELECT 'paper_performance_changed',schema_version,policy_version,account_ref,paper_accounting_session_id,
				strategy_selection_event_id,selected_strategy_result_ref,expected_previous_performance_id,
				strategy_selection_sequence_cutoff,order_event_sequence_cutoff,paper_market_sequence_cutoff,as_of,
				paper_account_state_sha256,marks_sha256,marks_json,mark_count,cash,open_cost,market_value,realized_pnl,
				unrealized_pnl,total_pnl,equity,peak_equity,period_return_state,period_return,cumulative_return,drawdown,
				max_drawdown,record_sha256,record_json,recorded_at FROM paper_performance_events`,
		} {
			if _, err := svc.db.Exec(statement); err == nil {
				t.Fatalf("durability mutation was accepted: %s", statement)
			}
		}
	})
}

func TestG38C3PaperPerformanceRejectsCorruption(t *testing.T) {
	mutations := []struct {
		name, statement string
	}{
		{"cutoff", `UPDATE paper_performance_events SET order_event_sequence_cutoff=0 WHERE sequence=2`},
		{"mark", `UPDATE paper_performance_events SET marks_sha256='0000000000000000000000000000000000000000000000000000000000000000' WHERE sequence=2`},
		{"mark JSON", `UPDATE paper_performance_events SET marks_json='[{}]' WHERE sequence=2`},
		{"value", `UPDATE paper_performance_events SET equity='1' WHERE sequence=2`},
		{"ratio", `UPDATE paper_performance_events SET cumulative_return='0.1' WHERE sequence=2`},
		{"predecessor", `UPDATE paper_performance_events SET expected_previous_performance_id='no_performance' WHERE sequence=2`},
		{"record JSON", `UPDATE paper_performance_events SET record_json='{}' WHERE sequence=2`},
		{"record hash", `UPDATE paper_performance_events SET record_sha256='0000000000000000000000000000000000000000000000000000000000000000' WHERE sequence=2`},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			svc, asOfs := g38c3HeldPerformanceFixture(t)
			for _, asOf := range asOfs[:2] {
				if _, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := svc.db.Exec(`DROP TRIGGER paper_performance_events_no_update`); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.db.Exec(test.statement); err != nil {
				t.Fatal(err)
			}
			if _, err := provePaperPerformanceRecovery(context.Background(), svc.db); err == nil {
				t.Fatal("corrupt performance log was certified")
			}
		})
	}
}

func TestG38C3PaperPerformanceRejectsLaterCorruptionAndLegacy(t *testing.T) {
	t.Run("older retry accepts valid later evidence", func(t *testing.T) {
		svc, asOf := g38c3CashPerformanceFixture(t)
		first, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf)
		if err != nil {
			t.Fatal(err)
		}
		svc.now = func() time.Time { return mustTime("2026-01-12T07:00:00Z") }
		recordG38C3MarkBar(t, svc, "005930", "g38c3-later-valid", "2026-01-12T06:30:00.000000000Z", "100")
		retry, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf)
		if err != nil || retry.PerformanceID != first.PerformanceID {
			t.Fatalf("older retry=%+v err=%v", retry, err)
		}
	})

	t.Run("older retry proves later order evidence", func(t *testing.T) {
		svc, asOf := g38c3CashPerformanceFixture(t)
		if _, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf); err != nil {
			t.Fatal(err)
		}
		state := mustRecordK2AOrder(t, svc, "g38c3-later-corrupt-order")
		if _, err := svc.db.Exec(`DROP TRIGGER order_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE order_events SET event_sha256=? WHERE order_id=?`, strings.Repeat("0", 64), state.OrderID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf); err == nil {
			t.Fatal("older retry hid later corrupt order evidence")
		}
	})

	t.Run("older retry proves later market evidence", func(t *testing.T) {
		svc, asOf := g38c3CashPerformanceFixture(t)
		if _, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf); err != nil {
			t.Fatal(err)
		}
		svc.now = func() time.Time { return mustTime("2026-01-12T07:00:00Z") }
		later := recordG38C3MarkBar(t, svc, "005930", "g38c3-later-corrupt", "2026-01-12T06:30:00.000000000Z", "100")
		if _, err := svc.db.Exec(`DROP TRIGGER paper_market_bar_observations_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE paper_market_bar_observations SET record_sha256=? WHERE observation_id=?`, strings.Repeat("0", 64), later.ObservationID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf); err == nil {
			t.Fatal("older retry hid later corrupt market evidence")
		}
	})

	t.Run("account-scoped legacy v1 and v2 orders fail closed", func(t *testing.T) {
		for _, schema := range []string{paperSignalSchema, "paper-signal.v2"} {
			svc, _ := testService(t, nil, nil)
			svc.now = func() time.Time { return mustTime("2026-01-10T15:00:00Z") }
			evidence, selected := selectedPaperStrategy(t, svc)
			if _, err := svc.openPaperAccountingSession(context.Background(), k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID); err != nil {
				t.Fatal(err)
			}
			downgradePaperAuthorizationForTest(t, svc.db)
			legacy := paperEvaluationSignal(evidence.ResultSHA256, selected.CurrentEventID, "g38c3-legacy-"+schema)
			legacy.SchemaVersion = schema
			intent := paperOrderIntent(k2aAccountRef, legacy, "1", "1000")
			intent.ClientOrderID = "g38c3-legacy-" + schema
			if err := insertG38C2LegacyPaperOrderDirect(svc, intent, "order_g38c3_legacy"); err != nil {
				t.Fatal(err)
			}
			if err := migrate(svc.db); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, "2026-01-11T06:30:00.000000000Z"); err == nil {
				t.Fatalf("legacy %s account was evaluated", schema)
			}
		}
	})
}

func TestG38C3ConcurrentSameAndDifferentAsOf(t *testing.T) {
	t.Run("same key writers converge", func(t *testing.T) {
		primary, asOf := g38c3CashPerformanceFixture(t)
		secondary := secondG38C2Service(t, primary, mustTime("2026-01-11T07:00:00Z"))
		start := make(chan struct{})
		results := make(chan *PaperPerformanceEvent, 2)
		errors := make(chan error, 2)
		var wg sync.WaitGroup
		for _, svc := range []*Service{primary, secondary} {
			wg.Add(1)
			go func(svc *Service) {
				defer wg.Done()
				<-start
				event, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf)
				results <- event
				errors <- err
			}(svc)
		}
		close(start)
		wg.Wait()
		close(results)
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
		var id string
		for event := range results {
			if event == nil || (id != "" && event.PerformanceID != id) {
				t.Fatalf("same-key results diverged: prior=%s event=%+v", id, event)
			}
			id = event.PerformanceID
		}
		var count int
		if err := primary.db.QueryRow(`SELECT COUNT(*) FROM paper_performance_events`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("same-key rows=%d err=%v", count, err)
		}
	})

	for _, laterFirst := range []bool{false, true} {
		name := map[bool]string{false: "earlier first chains", true: "later first rejects backfill"}[laterFirst]
		t.Run(name, func(t *testing.T) {
			svc, asOfs := g38c3HeldPerformanceFixture(t)
			first, second := asOfs[0], asOfs[1]
			if laterFirst {
				first, second = second, first
			}
			if _, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, first); err != nil {
				t.Fatal(err)
			}
			_, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, second)
			if laterFirst && err == nil {
				t.Fatal("later-first writer admitted a historical backfill")
			}
			if !laterFirst && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func g38c3CashPerformanceFixture(t *testing.T) (*Service, string) {
	t.Helper()
	svc, _, _ := g38c2PaperSignalFixture(t, true)
	svc.now = func() time.Time { return mustTime("2026-01-11T07:00:00Z") }
	asOf := "2026-01-11T06:30:00.000000000Z"
	recordG38C3MarkBar(t, svc, "005930", "g38c3-cash-anchor", asOf, "100")
	return svc, asOf
}

func g38c3HeldPerformanceFixture(t *testing.T) (*Service, []string) {
	t.Helper()
	svc, _, _ := g38c3HeldState(t)
	asOfs := []string{
		"2026-01-11T06:30:00.000000000Z",
		"2026-01-12T06:30:00.000000000Z",
		"2026-01-13T06:30:00.000000000Z",
	}
	for index, close := range []string{"120", "80", "110"} {
		recordG38C3MarkBar(t, svc, "005930", fmt.Sprintf("g38c3-series-%d", index), asOfs[index], close)
	}
	return svc, asOfs
}

var _ orderQuerier = (*sql.Tx)(nil)

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
