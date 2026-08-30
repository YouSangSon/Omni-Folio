package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omni-folio/services/core/internal/paperdomain"
)

func TestG38C2PaperAccountingExactBuyAndSell(t *testing.T) {
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	svc.now = func() time.Time { return mustTime("2026-01-15T07:00:00Z") }
	signal.TargetQuantity, signal.ExpiresAt = "2", "2026-01-20T00:00:00.000000000Z"
	lease := mustK2CLease(t, svc, k2aAccountRef)
	_, buy, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	buyBar := recordG38C2FillBar(t, svc, "fixture-005930-buy", "100", "5", "2026-01-10")
	buy, err = svc.runPaperOrder(context.Background(), buy.OrderID, lease.FencingToken)
	if err != nil || buy.Status != "FILLED" || buy.FilledQuantity != "2" {
		t.Fatalf("BUY state=%+v err=%v", buy, err)
	}
	states, err := replayPaperAccounting(context.Background(), svc.db)
	wantBuy := paperAccountState{
		AccountRef: k2aAccountRef, Cash: "9798.8", Fees: "1", Taxes: "0", Slippage: "0.2", RealizedPnL: "0", CapitalizedFills: 1,
		Lots: map[string][]paperLotState{"005930": {{Quantity: "2", Cost: "201.2"}}},
	}
	wantBuy.PaperAccountingSessionID = states[k2aAccountRef].PaperAccountingSessionID
	if err != nil || !samePaperAccountState(states[k2aAccountRef], wantBuy) {
		t.Fatalf("BUY accounting=%+v err=%v", states[k2aAccountRef], err)
	}

	sellSignal := signal
	sellSignal.SignalID, sellSignal.TargetQuantity = "g38c2-sell-all", "0"
	sellSignal.SignalBarObservationID, sellSignal.DataSHA256, sellSignal.DataAsOf = buyBar.ObservationID, buyBar.InputDataSHA256, buyBar.CloseAt
	sellSignal.GeneratedAt = buyBar.SourceAvailableAt
	_, sell, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, sellSignal, lease.FencingToken)
	if err != nil || sell == nil || sell.SideForTest(t, svc) != "SELL" || sell.Quantity != "2" {
		t.Fatalf("SELL order=%+v err=%v", sell, err)
	}
	recordG38C2FillBar(t, svc, "fixture-005930-sell", "120", "5", "2026-01-11")
	sell, err = svc.runPaperOrder(context.Background(), sell.OrderID, lease.FencingToken)
	if err != nil || sell.Status != "FILLED" {
		t.Fatalf("SELL state=%+v err=%v", sell, err)
	}
	states, err = replayPaperAccounting(context.Background(), svc.db)
	wantSell := paperAccountState{
		AccountRef: k2aAccountRef, PaperAccountingSessionID: wantBuy.PaperAccountingSessionID,
		Cash: "10037.32024", Lots: map[string][]paperLotState{}, Fees: "2", Taxes: "0.23976",
		Slippage: "0.44", RealizedPnL: "37.32024", CapitalizedFills: 2,
	}
	if err != nil || !samePaperAccountState(states[k2aAccountRef], wantSell) {
		t.Fatalf("SELL accounting=%+v err=%v", states[k2aAccountRef], err)
	}
	proof, err := provePaperAccountingRecovery(context.Background(), svc.db)
	if err != nil || proof.Sessions != 1 || proof.MarketBars != 3 || proof.Signals != 2 || proof.Authorizations != 2 ||
		proof.CapitalizedFills != 2 || proof.SHA256 == "" {
		t.Fatalf("accounting proof=%+v err=%v", proof, err)
	}
	var ledgerEvents, reservations, fillTables int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&ledgerEvents); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM risk_reservations`).Scan(&reservations); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE '%paper%fill%'`).Scan(&fillTables); err != nil {
		t.Fatal(err)
	}
	if ledgerEvents != 0 || reservations != 0 || fillTables != 0 {
		t.Fatalf("paper accounting escaped its journal: ledger=%d reservations=%d fill_tables=%d", ledgerEvents, reservations, fillTables)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "paper-fills.db")
	manifest, err := createBackup(svc.db, backup, golden, backup+".manifest.json", svc.now, svc.id)
	if err != nil || manifest.PaperAccountingStateSHA256 != proof.SHA256 ||
		manifest.VerificationReceipt.CandidatePaperAccountingStateSHA256 != proof.SHA256 ||
		manifest.PaperAccountingSessionCount != 1 || manifest.PaperMarketBarObservationCount != 3 ||
		manifest.PaperSignalEventCount != 2 || manifest.PaperExecutionAuthorizationCount != 2 || manifest.PaperCapitalizedFillCount != 2 {
		t.Fatalf("fill backup manifest=%+v err=%v", manifest, err)
	}
	restored, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	restoredStates, err := replayPaperAccounting(context.Background(), restored)
	if err != nil || !samePaperAccountState(restoredStates[k2aAccountRef], wantSell) {
		t.Fatalf("restored accounting=%+v err=%v", restoredStates[k2aAccountRef], err)
	}
}

func TestG38C2PaperFillEligibleRetryAndFence(t *testing.T) {
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	svc.now = func() time.Time { return mustTime("2026-01-15T07:00:00Z") }
	signal.TargetQuantity, signal.ExpiresAt = "4", "2026-01-20T00:00:00.000000000Z"
	lease := mustK2CLease(t, svc, k2aAccountRef)
	_, order, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := svc.runPaperOrder(context.Background(), order.OrderID, lease.FencingToken); err != nil || got.Status != "OPEN" {
		t.Fatalf("pre-bar fill state=%+v err=%v", got, err)
	}
	recordG38C2FillBar(t, svc, "fixture-005930-zero-capacity", "100", "0", "2026-01-10")
	fillInput := g38c2PaperMarketBar("fixture-005930-partial", "2026-01-11T00:00:00Z", "2026-01-11T06:30:00Z")
	fillInput.Open, fillInput.High, fillInput.Low, fillInput.Close, fillInput.Volume = "100", "125", "95", "120", "5"
	fillInput.SourceAvailableAt, fillInput.FetchedAt = "2026-01-11T06:31:00Z", "2026-01-11T06:32:00Z"
	fillBar, err := svc.recordPaperMarketBar(context.Background(), fillInput)
	if err != nil {
		t.Fatal(err)
	}
	before := paperAdmissionCountsForTest(t, svc)
	if _, err := svc.runPaperOrder(context.Background(), order.OrderID, lease.FencingToken+1); err == nil {
		t.Fatal("foreign fence appended a fill")
	}
	if after := paperAdmissionCountsForTest(t, svc); after != before {
		t.Fatalf("fence failure leaked rows: before=%+v after=%+v", before, after)
	}
	first, err := svc.runPaperOrder(context.Background(), order.OrderID, lease.FencingToken)
	if err != nil || first.Status != "PARTIALLY_FILLED" || first.FilledQuantity != "2" {
		t.Fatalf("partial fill state=%+v err=%v", first, err)
	}
	var raw string
	if err := svc.db.QueryRow(`SELECT event_json FROM order_events WHERE order_id=? AND event_type='FILL_RECORDED'`, order.OrderID).Scan(&raw); err != nil ||
		!strings.Contains(raw, fillBar.ObservationID) || !strings.Contains(raw, `"reference_price":"100"`) ||
		!strings.Contains(raw, `"price":"100.1"`) {
		t.Fatalf("runner did not deterministically skip the zero-capacity bar: raw=%s err=%v", raw, err)
	}
	counts := paperAdmissionCountsForTest(t, svc)
	retry, err := svc.runPaperOrder(context.Background(), order.OrderID, lease.FencingToken)
	if err != nil || retry.FilledQuantity != "2" || paperAdmissionCountsForTest(t, svc) != counts {
		t.Fatalf("same-bar retry state=%+v err=%v", retry, err)
	}
}

func TestG38C2PaperReductionDirectPartialGuard(t *testing.T) {
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	svc.now = func() time.Time { return mustTime("2026-01-15T07:00:00Z") }
	signal.TargetQuantity, signal.ExpiresAt = "4", "2026-01-20T00:00:00.000000000Z"
	lease := mustK2CLease(t, svc, k2aAccountRef)
	_, order, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	bar := recordG38C2FillBar(t, svc, "fixture-005930-partial-direct", "100", "5", "2026-01-10")
	state, err := svc.runPaperOrder(context.Background(), order.OrderID, lease.FencingToken)
	if err != nil || state.Status != "PARTIALLY_FILLED" || state.FilledQuantity != "2" {
		t.Fatalf("partial state=%+v err=%v", state, err)
	}
	reduction := signal
	reduction.SignalID, reduction.TargetQuantity = "g38c2-direct-partial-reduction", "1"
	reduction.SignalBarObservationID, reduction.DataSHA256, reduction.DataAsOf = bar.ObservationID, bar.InputDataSHA256, bar.CloseAt
	reduction.GeneratedAt = bar.SourceAvailableAt
	event, err := recordG38C2PaperSignalForTest(svc, k2aAccountRef, reduction)
	if err != nil {
		t.Fatal(err)
	}
	direct := capitalizedPaperOrderIntent(*event, "SELL", "1")
	direct.ClientOrderID = "paper_direct_partial_reduction"
	before := paperAdmissionCountsForTest(t, svc)
	if err := insertPaperOrderIntentDirectForTest(svc, direct, "order_direct_partial_reduction"); err == nil {
		t.Fatal("direct target reduction crossed a partially filled order")
	}
	if after := paperAdmissionCountsForTest(t, svc); after != before {
		t.Fatalf("rejected direct reduction leaked rows: before=%+v after=%+v", before, after)
	}
}

func TestG38C2PaperAccountingRecurringFIFOResidual(t *testing.T) {
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	svc.now = func() time.Time { return mustTime("2026-01-15T07:00:00Z") }
	signal.TargetQuantity, signal.ExpiresAt = "3", "2026-01-20T00:00:00.000000000Z"
	lease := mustK2CLease(t, svc, k2aAccountRef)
	_, buy, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	buyBar := recordG38C2FillBar(t, svc, "fixture-005930-fifo-buy", "100", "6", "2026-01-10")
	if buy, err = svc.runPaperOrder(context.Background(), buy.OrderID, lease.FencingToken); err != nil || buy.Status != "FILLED" {
		t.Fatalf("FIFO BUY=%+v err=%v", buy, err)
	}
	sellSignal := signal
	sellSignal.SignalID, sellSignal.TargetQuantity = "g38c2-fifo-sell", "0"
	sellSignal.SignalBarObservationID, sellSignal.DataSHA256, sellSignal.DataAsOf = buyBar.ObservationID, buyBar.InputDataSHA256, buyBar.CloseAt
	sellSignal.GeneratedAt = buyBar.SourceAvailableAt
	_, sell, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, sellSignal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	for index, day := range []string{"2026-01-11", "2026-01-12", "2026-01-13"} {
		recordG38C2FillBar(t, svc, "fixture-005930-fifo-sell-"+day, "120", "2", day)
		sell, err = svc.runPaperOrder(context.Background(), sell.OrderID, lease.FencingToken)
		if err != nil || sell.FilledQuantity != []string{"1", "2", "3"}[index] {
			t.Fatalf("FIFO SELL %d state=%+v err=%v", index+1, sell, err)
		}
		states, err := replayPaperAccounting(context.Background(), svc.db)
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			want := []paperLotState{{Quantity: "2", Cost: "200.86666667"}}
			if got := states[k2aAccountRef].Lots[signal.Symbol]; len(got) != 1 || got[0] != want[0] {
				t.Fatalf("first FIFO residual=%+v want=%+v", got, want)
			}
		}
		if index == 1 {
			want := paperLotState{Quantity: "1", Cost: "100.433333335"}
			if got := states[k2aAccountRef].Lots[signal.Symbol]; len(got) != 1 || got[0] != want {
				t.Fatalf("second FIFO residual=%+v want=%+v", got, want)
			}
		}
	}
	states, err := replayPaperAccounting(context.Background(), svc.db)
	want := paperAccountState{
		AccountRef: k2aAccountRef, PaperAccountingSessionID: states[k2aAccountRef].PaperAccountingSessionID,
		Cash: "10054.98036", Lots: map[string][]paperLotState{}, Fees: "4", Taxes: "0.35964",
		Slippage: "0.66", RealizedPnL: "54.98036", CapitalizedFills: 4,
	}
	if err != nil || !samePaperAccountState(states[k2aAccountRef], want) {
		t.Fatalf("FIFO final=%+v err=%v", states[k2aAccountRef], err)
	}
}

func TestG38C2PaperAccountingIgnoresLegacyFills(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	downgradePaperAuthorizationForTest(t, svc.db)
	svc.now = func() time.Time { return mustTime("2026-01-10T15:00:00Z") }
	evidence, err := svc.registerStrategyEvidence(context.Background(), strategyArtifact(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, noStrategySelectionEvent)
	if err != nil {
		t.Fatal(err)
	}
	signal := PaperSignal{
		SchemaVersion: paperSignalSchema, SignalID: "g38c2-legacy-accounting-fill",
		StrategyResultSHA256: evidence.ResultSHA256, StrategySelectionEventID: selected.CurrentEventID,
		DataSHA256: strings.Repeat("d", 64), Symbol: "005930", TargetQuantity: "2",
		DataAsOf: "2026-01-10T14:59:00Z", GeneratedAt: "2026-01-10T14:59:01Z", ExpiresAt: "2026-01-10T15:01:00Z",
	}
	observation := PaperMarketObservation{
		Source: "local_fixture", Symbol: signal.Symbol, ObservedAt: "2026-01-10T15:00:00Z",
		AskPrice: "999", AvailableQuantity: "2",
	}
	lease := mustK2CLease(t, svc, k2aAccountRef)
	state, err := svc.runPaperSignal(context.Background(), k2aAccountRef, signal, observation, lease.FencingToken)
	if err != nil || state.Status != "FILLED" {
		t.Fatalf("legacy fill state=%+v err=%v", state, err)
	}
	if err := migrate(svc.db); err != nil {
		t.Fatal(err)
	}
	states, err := replayPaperAccounting(context.Background(), svc.db)
	proof, proofErr := provePaperAccountingRecovery(context.Background(), svc.db)
	if err != nil || proofErr != nil || len(states) != 0 || proof.CapitalizedFills != 0 {
		t.Fatalf("legacy fill changed capitalized accounting: states=%+v proof=%+v errors=(%v,%v)", states, proof, err, proofErr)
	}
}

func TestG38C2PaperReductionTenToFourToZero(t *testing.T) {
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	svc.now = func() time.Time { return mustTime("2026-01-15T07:00:00Z") }
	signal.TargetQuantity, signal.ExpiresAt = "10", "2026-01-20T00:00:00.000000000Z"
	lease := mustK2CLease(t, svc, k2aAccountRef)
	_, buy, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	buyBar := recordG38C2FillBar(t, svc, "fixture-005930-reduction-buy", "100", "20", "2026-01-10")
	if buy, err = svc.runPaperOrder(context.Background(), buy.OrderID, lease.FencingToken); err != nil || buy.Status != "FILLED" {
		t.Fatalf("target 10 BUY=%+v err=%v", buy, err)
	}
	toFour := signal
	toFour.SignalID, toFour.TargetQuantity = "g38c2-target-four", "4"
	toFour.SignalBarObservationID, toFour.DataSHA256, toFour.DataAsOf = buyBar.ObservationID, buyBar.InputDataSHA256, buyBar.CloseAt
	toFour.GeneratedAt = buyBar.SourceAvailableAt
	_, sellSix, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, toFour, lease.FencingToken)
	if err != nil || sellSix == nil || sellSix.Quantity != "6" || sellSix.SideForTest(t, svc) != "SELL" {
		t.Fatalf("target 4 SELL=%+v err=%v", sellSix, err)
	}
	sellSixBar := recordG38C2FillBar(t, svc, "fixture-005930-reduction-six", "120", "20", "2026-01-11")
	if sellSix, err = svc.runPaperOrder(context.Background(), sellSix.OrderID, lease.FencingToken); err != nil || sellSix.Status != "FILLED" {
		t.Fatalf("SELL 6=%+v err=%v", sellSix, err)
	}
	toZero := toFour
	toZero.SignalID, toZero.TargetQuantity = "g38c2-target-zero", "0"
	toZero.SignalBarObservationID, toZero.DataSHA256, toZero.DataAsOf = sellSixBar.ObservationID, sellSixBar.InputDataSHA256, sellSixBar.CloseAt
	toZero.GeneratedAt = sellSixBar.SourceAvailableAt
	_, sellFour, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, toZero, lease.FencingToken)
	if err != nil || sellFour == nil || sellFour.Quantity != "4" || sellFour.SideForTest(t, svc) != "SELL" {
		t.Fatalf("target zero SELL=%+v err=%v", sellFour, err)
	}
	recordG38C2FillBar(t, svc, "fixture-005930-reduction-four", "120", "20", "2026-01-12")
	if sellFour, err = svc.runPaperOrder(context.Background(), sellFour.OrderID, lease.FencingToken); err != nil || sellFour.Status != "FILLED" {
		t.Fatalf("SELL 4=%+v err=%v", sellFour, err)
	}
	states, err := replayPaperAccounting(context.Background(), svc.db)
	if err != nil || len(states[k2aAccountRef].Lots) != 0 || states[k2aAccountRef].CapitalizedFills != 3 {
		t.Fatalf("reduced account=%+v err=%v", states[k2aAccountRef], err)
	}
}

func TestG38C2PaperFenceRequiresExplicitReLease(t *testing.T) {
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	clock := mustTime("2026-01-15T07:00:00Z")
	svc.now = func() time.Time { return clock }
	signal.TargetQuantity, signal.ExpiresAt = "2", "2026-01-20T00:00:00.000000000Z"
	lease := mustK2CLease(t, svc, k2aAccountRef)
	_, order, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	recordG38C2FillBar(t, svc, "fixture-005930-release", "100", "5", "2026-01-10")
	clock = clock.Add(syntheticExecutionLeaseTTL)
	before := paperAdmissionCountsForTest(t, svc)
	if _, err := svc.runPaperOrder(context.Background(), order.OrderID, lease.FencingToken); err == nil {
		t.Fatal("expired lease appended a local fill")
	}
	if after := paperAdmissionCountsForTest(t, svc); after != before {
		t.Fatalf("expired lease leaked rows: before=%+v after=%+v", before, after)
	}
	released := mustK2CLease(t, svc, k2aAccountRef)
	if released.FencingToken == lease.FencingToken {
		t.Fatal("expired lease was not explicitly fenced")
	}
	filled, err := svc.runPaperOrder(context.Background(), order.OrderID, released.FencingToken)
	if err != nil || filled.Status != "FILLED" {
		t.Fatalf("re-leased fill=%+v err=%v", filled, err)
	}
}

func TestG38C2PaperConcurrencyCashAndLots(t *testing.T) {
	t.Run("BUY orders serialize shared cash across symbols", func(t *testing.T) {
		primary, firstSignal, _ := g38c2PaperSignalFixture(t, true)
		fixedNow := mustTime("2026-01-15T07:00:00Z")
		primary.now = func() time.Time { return fixedNow }
		secondSignalBar := g38c2PaperMarketBar("fixture-000660-20260109", "2026-01-09T00:00:00Z", "2026-01-09T06:30:00Z")
		secondSignalBar.Symbol, secondSignalBar.InputDataSHA256 = "000660", strings.Repeat("b", 64)
		recordedSecondSignalBar, err := primary.recordPaperMarketBar(context.Background(), secondSignalBar)
		if err != nil {
			t.Fatal(err)
		}
		firstSignal.TargetQuantity, firstSignal.ExpiresAt = "100", "2026-01-20T00:00:00.000000000Z"
		secondSignal := firstSignal
		secondSignal.SignalID, secondSignal.Symbol = "g38c2-concurrent-cash-second", recordedSecondSignalBar.Symbol
		secondSignal.SignalBarObservationID, secondSignal.DataSHA256 = recordedSecondSignalBar.ObservationID, recordedSecondSignalBar.InputDataSHA256
		secondSignal.DataAsOf, secondSignal.GeneratedAt = recordedSecondSignalBar.CloseAt, recordedSecondSignalBar.SourceAvailableAt
		lease := mustK2CLease(t, primary, k2aAccountRef)
		_, firstOrder, err := primary.admitPaperSignal(context.Background(), k2aAccountRef, firstSignal, lease.FencingToken)
		if err != nil {
			t.Fatal(err)
		}
		_, secondOrder, err := primary.admitPaperSignal(context.Background(), k2aAccountRef, secondSignal, lease.FencingToken)
		if err != nil {
			t.Fatal(err)
		}
		recordG38C2SymbolFillBar(t, primary, firstSignal.Symbol, "fixture-005930-concurrent-cash", "100", "1000", "2026-01-10")
		recordG38C2SymbolFillBar(t, primary, secondSignal.Symbol, "fixture-000660-concurrent-cash", "100", "1000", "2026-01-10")
		secondary := secondG38C2Service(t, primary, fixedNow)
		type result struct {
			state *OrderState
			err   error
		}
		start := make(chan struct{})
		results := make(chan result, 2)
		for index, candidate := range []struct {
			service *Service
			orderID string
		}{{primary, firstOrder.OrderID}, {secondary, secondOrder.OrderID}} {
			index, candidate := index, candidate
			go func() {
				<-start
				state, err := candidate.service.runPaperOrder(context.Background(), candidate.orderID, lease.FencingToken)
				if err != nil {
					err = fmt.Errorf("runner %d: %w", index, err)
				}
				results <- result{state: state, err: err}
			}()
		}
		close(start)
		filled := 0
		for range 2 {
			result := <-results
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.state.FilledQuantity == "99" {
				filled++
			} else if result.state.FilledQuantity != "0" {
				t.Fatalf("unexpected concurrent BUY state=%+v", result.state)
			}
		}
		states, err := replayPaperAccounting(context.Background(), primary.db)
		account := states[k2aAccountRef]
		lotCount, held := 0, ""
		for _, lots := range account.Lots {
			for _, lot := range lots {
				lotCount++
				held = lot.Quantity
			}
		}
		if err != nil || filled != 1 || account.Cash != "89.1" || account.CapitalizedFills != 1 || lotCount != 1 || held != "99" {
			t.Fatalf("serialized shared cash state=%+v filled=%d lots=(%d,%s) err=%v", account, filled, lotCount, held, err)
		}
	})

	t.Run("SELL retries cannot consume a FIFO lot twice", func(t *testing.T) {
		primary, signal, _ := g38c2PaperSignalFixture(t, true)
		fixedNow := mustTime("2026-01-15T07:00:00Z")
		primary.now = func() time.Time { return fixedNow }
		signal.TargetQuantity, signal.ExpiresAt = "3", "2026-01-20T00:00:00.000000000Z"
		lease := mustK2CLease(t, primary, k2aAccountRef)
		_, buy, err := primary.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
		if err != nil {
			t.Fatal(err)
		}
		buyBar := recordG38C2FillBar(t, primary, "fixture-005930-concurrent-lot-buy", "100", "6", "2026-01-10")
		if buy, err = primary.runPaperOrder(context.Background(), buy.OrderID, lease.FencingToken); err != nil || buy.Status != "FILLED" {
			t.Fatalf("concurrent lot BUY=%+v err=%v", buy, err)
		}
		sellSignal := signal
		sellSignal.SignalID, sellSignal.TargetQuantity = "g38c2-concurrent-lot-sell", "0"
		sellSignal.SignalBarObservationID, sellSignal.DataSHA256, sellSignal.DataAsOf = buyBar.ObservationID, buyBar.InputDataSHA256, buyBar.CloseAt
		sellSignal.GeneratedAt = buyBar.SourceAvailableAt
		_, sell, err := primary.admitPaperSignal(context.Background(), k2aAccountRef, sellSignal, lease.FencingToken)
		if err != nil {
			t.Fatal(err)
		}
		recordG38C2FillBar(t, primary, "fixture-005930-concurrent-lot-sell", "120", "6", "2026-01-11")
		secondary := secondG38C2Service(t, primary, fixedNow)
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, service := range []*Service{primary, secondary} {
			service := service
			go func() {
				<-start
				state, err := service.runPaperOrder(context.Background(), sell.OrderID, lease.FencingToken)
				if err == nil && state.Status != "FILLED" {
					err = fmt.Errorf("concurrent SELL state=%+v", state)
				}
				results <- err
			}()
		}
		close(start)
		for range 2 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		states, err := replayPaperAccounting(context.Background(), primary.db)
		account := states[k2aAccountRef]
		if err != nil || len(account.Lots) != 0 || account.CapitalizedFills != 2 || paperAdmissionCountsForTest(t, primary).Fills != 2 {
			t.Fatalf("concurrent SELL consumed lot more than once: state=%+v err=%v", account, err)
		}
	})
}

func TestG38C2PaperFillRejectsGenericApplicationAppend(t *testing.T) {
	t.Run("shared transaction writer", func(t *testing.T) {
		svc, forged := g38c2ForgedPaperFill(t, 1)
		before := paperAdmissionCountsForTest(t, svc)
		tx, err := svc.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := appendOrderEventTx(context.Background(), tx, forged, svc.now().UTC().Format(time.RFC3339Nano)); err == nil {
			tx.Rollback()
			t.Fatal("shared transaction append admitted a complete capitalized fill")
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if after := paperAdmissionCountsForTest(t, svc); after != before {
			t.Fatalf("rejected shared fill leaked rows: before=%+v after=%+v", before, after)
		}
	})
	t.Run("wrong delay bar", func(t *testing.T) {
		svc, forged := g38c2ForgedPaperFill(t, 2)
		before := paperAdmissionCountsForTest(t, svc)
		if _, err := svc.appendOrderEvent(context.Background(), forged); err == nil {
			t.Fatal("generic application append admitted a complete capitalized fill on a pre-delay bar")
		}
		if after := paperAdmissionCountsForTest(t, svc); after != before {
			t.Fatalf("rejected generic fill leaked rows: before=%+v after=%+v", before, after)
		}
	})
	for _, test := range []struct {
		name   string
		mutate func(*OrderEvent)
	}{
		{"wrong price", func(event *OrderEvent) { event.Price = "101" }},
		{"wrong tax", func(event *OrderEvent) { event.Tax = "0.25" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, forged := g38c2ForgedPaperFill(t, 1)
			test.mutate(&forged)
			before := paperAdmissionCountsForTest(t, svc)
			if _, err := svc.appendOrderEvent(context.Background(), forged); err == nil {
				t.Fatal("generic application append admitted a miscalculated capitalized fill")
			}
			if after := paperAdmissionCountsForTest(t, svc); after != before {
				t.Fatalf("rejected generic fill leaked rows: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestG38C2PaperFillDirectSQLRejectsWrongDelayOrdinal(t *testing.T) {
	svc, forged := g38c2ForgedPaperFill(t, 2)
	tx, err := svc.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertOrderEvent(context.Background(), tx, forged, svc.now().UTC().Format(time.RFC3339Nano)); err == nil {
		tx.Rollback()
		t.Fatal("raw SQL admitted a complete capitalized fill before delay_bars")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if counts := paperAdmissionCountsForTest(t, svc); counts.Fills != 0 {
		t.Fatalf("rejected raw fill rows=%+v", counts)
	}
}

func TestG38C2PaperFillRawSQLThreatBoundaryFailsRecoveryAndActivation(t *testing.T) {
	svc, forged := g38c2ForgedPaperFill(t, 1)
	golden := writeCurrentSnapshot(t, svc.db)
	forged.Price = "101"
	tx, err := svc.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertOrderEvent(context.Background(), tx, forged, svc.now().UTC().Format(time.RFC3339Nano)); err != nil {
		tx.Rollback()
		t.Fatalf("structurally complete raw fill did not reach the documented recovery boundary: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := replayPaperAccounting(context.Background(), svc.db); err == nil {
		t.Fatal("recovery accepted raw SQL fill with economically wrong price")
	}
	candidate := filepath.Join(t.TempDir(), "raw-sql-wrong-price.db")
	if _, err := svc.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(candidate, "'", "''") + `'`); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(candidate, golden); err == nil {
		t.Fatal("restore activation accepted raw SQL fill with economically wrong price")
	}
}

func TestG38C2PaperFillLeaseNanosecondBoundary(t *testing.T) {
	for _, test := range []struct {
		name   string
		offset time.Duration
		accept bool
	}{
		{"expiry minus one nanosecond", -time.Nanosecond, true},
		{"at expiry", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, signal, _ := g38c2PaperSignalFixture(t, true)
			signal.TargetQuantity, signal.ExpiresAt = "2", "2026-01-20T00:00:00.000000000Z"
			lease := mustK2CLease(t, svc, k2aAccountRef)
			_, order, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
			if err != nil {
				t.Fatal(err)
			}
			recordG38C2FillBar(t, svc, "fixture-005930-lease-boundary", "100", "5", "2026-01-10")
			expires, _ := canonicalUTCTime(lease.LeaseExpiresAt)
			svc.now = func() time.Time { return expires.Add(test.offset) }
			before := paperAdmissionCountsForTest(t, svc)
			state, err := svc.runPaperOrder(context.Background(), order.OrderID, lease.FencingToken)
			if test.accept {
				if err != nil || state.Status != "FILLED" {
					t.Fatalf("fill before lease expiry state=%+v err=%v", state, err)
				}
				return
			}
			if err == nil {
				t.Fatal("fill at lease expiry was accepted")
			}
			if after := paperAdmissionCountsForTest(t, svc); after != before {
				t.Fatalf("expired fill leaked rows: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestG38C2PaperAccountingRejectsCorruption(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*OrderEvent)
	}{
		{"fee", func(event *OrderEvent) { event.Fee = "2" }},
		{"tax", func(event *OrderEvent) { event.Tax = "0.1" }},
		{"price", func(event *OrderEvent) { event.Price = "101" }},
		{"quantity", func(event *OrderEvent) { event.Quantity = "1" }},
		{"bar", func(event *OrderEvent) { event.PaperBarObservationID = "paper_market_bar_wrong" }},
		{"session", func(event *OrderEvent) { event.PaperAccountingSessionID = "paper_accounting_session_wrong" }},
		{"policy", func(event *OrderEvent) { event.PaperFillPolicyVersion = "paper_bar_open_v2" }},
		{"authorization", func(event *OrderEvent) { event.PaperAuthorizationID = "paper_authorization_wrong" }},
		{"authority", func(event *OrderEvent) { event.ExecutionAuthorityEventID = "execution_authority_wrong" }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := g38c2FilledBuyForTest(t)
			var raw string
			if err := svc.db.QueryRow(`SELECT event_json FROM order_events WHERE event_type='FILL_RECORDED'`).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var event OrderEvent
			if err := json.Unmarshal([]byte(raw), &event); err != nil {
				t.Fatal(err)
			}
			test.mutate(&event)
			canonical, hash, err := orderJSONHash(event)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.db.Exec(`DROP TRIGGER order_events_no_update`); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.db.Exec(`UPDATE order_events SET event_json=?,event_sha256=? WHERE event_type='FILL_RECORDED'`, string(canonical), hash); err != nil {
				t.Fatal(err)
			}
			if _, err := replayPaperAccounting(context.Background(), svc.db); err == nil {
				t.Fatal("corrupt capitalized fill replayed")
			}
		})
	}
	t.Run("event hash", func(t *testing.T) {
		svc, _ := g38c2FilledBuyForTest(t)
		if _, err := svc.db.Exec(`DROP TRIGGER order_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE order_events SET event_sha256=? WHERE event_type='FILL_RECORDED'`, strings.Repeat("0", 64)); err != nil {
			t.Fatal(err)
		}
		if _, err := replayPaperAccounting(context.Background(), svc.db); err == nil {
			t.Fatal("corrupt fill hash replayed")
		}
	})
	t.Run("event order", func(t *testing.T) {
		svc, _ := g38c2FilledBuyForTest(t)
		if _, err := svc.db.Exec(`DROP TRIGGER order_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE order_events SET sequence=0 WHERE event_type='FILL_RECORDED'`); err != nil {
			t.Fatal(err)
		}
		if _, err := replayPaperAccounting(context.Background(), svc.db); err == nil {
			t.Fatal("out-of-order fill replayed")
		}
	})
	t.Run("signal cutoff", func(t *testing.T) {
		svc, _ := g38c2FilledBuyForTest(t)
		event, found, err := loadPaperSignalEvent(context.Background(), svc.db, k2aAccountRef, "g38c2-signal-001")
		if err != nil || !found {
			t.Fatalf("signal found=%t err=%v", found, err)
		}
		event.MarketObservationSequenceCutoff++
		raw, hash, err := orderJSONHash(event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`DROP TRIGGER paper_signal_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE paper_signal_events SET market_observation_sequence_cutoff=?,record_json=?,record_sha256=? WHERE event_id=?`, event.MarketObservationSequenceCutoff, string(raw), hash, event.EventID); err != nil {
			t.Fatal(err)
		}
		if _, err := replayPaperAccounting(context.Background(), svc.db); err == nil {
			t.Fatal("corrupt signal cutoff replayed")
		}
	})
}

func TestG38C2PaperFillQuantityCeiling(t *testing.T) {
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	signal.TargetQuantity = "4611686018427387904"
	if _, err := recordG38C2PaperSignalForTest(svc, k2aAccountRef, signal); err == nil {
		t.Fatal("application accepted a target beyond the exact SQLite boundary")
	}
	valid := signal
	valid.SignalID, valid.TargetQuantity = "g38c2-direct-overflow-target", paperMaxQuantity
	event, err := recordG38C2PaperSignalForTest(svc, k2aAccountRef, valid)
	if err != nil {
		t.Fatal(err)
	}
	overflow := *event
	overflow.EventID, overflow.SignalID, overflow.TargetQuantity = "paper_signal_event_direct_overflow", "g38c2-direct-overflow-row", "4611686018427387904"
	if err := insertPaperSignalEventDirectForTest(svc, overflow); err == nil {
		t.Fatal("SQLite accepted a target beyond the exact quantity boundary")
	}
}

func g38c2FilledBuyForTest(t *testing.T) (*Service, *OrderState) {
	t.Helper()
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	svc.now = func() time.Time { return mustTime("2026-01-15T07:00:00Z") }
	signal.TargetQuantity, signal.ExpiresAt = "2", "2026-01-20T00:00:00.000000000Z"
	lease := mustK2CLease(t, svc, k2aAccountRef)
	_, order, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	recordG38C2FillBar(t, svc, "fixture-005930-corruption", "100", "5", "2026-01-10")
	order, err = svc.runPaperOrder(context.Background(), order.OrderID, lease.FencingToken)
	if err != nil || order.Status != "FILLED" {
		t.Fatalf("filled fixture=%+v err=%v", order, err)
	}
	return svc, order
}

func g38c2ForgedPaperFill(t *testing.T, delayBars int64) (*Service, OrderEvent) {
	t.Helper()
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-10T07:00:00Z") }
	artifact := rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(result map[string]any) {
		result["experiment_id"] = fmt.Sprintf("g38c2-forged-fill-delay-%d", delayBars)
		result["execution"].(map[string]any)["delay_bars"] = fmt.Sprint(delayBars)
	})
	evidence, err := svc.registerStrategyEvidence(context.Background(), artifact)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, noStrategySelectionEvent)
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.openPaperAccountingSession(context.Background(), k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	signalBar, err := svc.recordPaperMarketBar(context.Background(), g38c2PaperMarketBar("fixture-005930-forged-signal", "2026-01-09T00:00:00Z", "2026-01-09T06:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	signal := PaperSignal{
		SchemaVersion: capitalizedPaperSignalSchema, SignalID: fmt.Sprintf("g38c2-forged-delay-%d", delayBars),
		SignalBarObservationID: signalBar.ObservationID, StrategyResultSHA256: evidence.ResultSHA256,
		StrategySelectionEventID: selected.CurrentEventID, DataSHA256: signalBar.InputDataSHA256,
		Symbol: signalBar.Symbol, TargetQuantity: "2", DataAsOf: signalBar.CloseAt,
		GeneratedAt: signalBar.SourceAvailableAt, ExpiresAt: "2026-01-20T00:00:00.000000000Z",
	}
	svc.now = func() time.Time { return mustTime("2026-01-15T07:00:00Z") }
	lease := mustK2CLease(t, svc, k2aAccountRef)
	signalEvent, order, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	bar := recordG38C2FillBar(t, svc, fmt.Sprintf("fixture-005930-forged-fill-%d", delayBars), "100", "5", "2026-01-10")
	authorization, found, err := loadPaperExecutionAuthorizationByOrder(context.Background(), svc.db, order.OrderID)
	if err != nil || !found {
		t.Fatalf("authorization found=%t err=%v", found, err)
	}
	authority, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := validatePaperAccountingSession(*session)
	if err != nil {
		t.Fatal(err)
	}
	calculated, ok, err := paperdomain.CalculateFill(paperExecutionPolicy(policy), paperdomain.FillInput{
		Side: "BUY", Open: bar.Open, Volume: bar.Volume, RemainingQuantity: order.Quantity,
		Cash: session.StartingCash, ConsumedCapacity: "0",
	})
	if err != nil || !ok {
		t.Fatalf("forged fixture calculation=%+v ok=%t err=%v", calculated, ok, err)
	}
	return svc, OrderEvent{
		EventID: paperEventID("fill", order.OrderID, bar.ObservationID, paperFillPolicyVersion), OrderID: order.OrderID,
		Type: "FILL_RECORDED", Source: "synthetic", ProviderOrderRef: paperProviderAlias("order", order.OrderID),
		ProviderExecutionRef: paperProviderAlias("execution", order.OrderID, bar.ObservationID, paperFillPolicyVersion),
		Quantity:             calculated.Quantity, Price: calculated.Price, OccurredAt: bar.OpenAt,
		PaperAuthorizationID: authorization.AuthorizationID, FencingToken: lease.FencingToken,
		PaperAccountingSessionID: session.SessionID, PaperSignalEventID: signalEvent.EventID,
		PaperBarObservationID: bar.ObservationID, PaperFillPolicyVersion: paperFillPolicyVersion,
		ExecutionAuthorityEventID: authority.EventID, ReferencePrice: calculated.ReferencePrice,
		Fee: calculated.Fee, Tax: calculated.Tax, Slippage: calculated.Slippage,
	}
}

func recordG38C2FillBar(t testing.TB, svc *Service, sourceID, open, volume, day string) *PaperMarketBarObservation {
	return recordG38C2SymbolFillBar(t, svc, "005930", sourceID, open, volume, day)
}

func recordG38C2SymbolFillBar(t testing.TB, svc *Service, symbol, sourceID, open, volume, day string) *PaperMarketBarObservation {
	t.Helper()
	bar := g38c2PaperMarketBar(sourceID, day+"T00:00:00Z", day+"T06:30:00Z")
	bar.Symbol = symbol
	bar.Open, bar.High, bar.Low, bar.Close, bar.Volume = open, open, open, open, volume
	bar.SourceAvailableAt, bar.FetchedAt = day+"T06:31:00Z", day+"T06:32:00Z"
	recorded, err := svc.recordPaperMarketBar(context.Background(), bar)
	if err != nil {
		t.Fatal(err)
	}
	return recorded
}

func secondG38C2Service(t testing.TB, primary *Service, now time.Time) *Service {
	t.Helper()
	var databasePath string
	if err := primary.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&databasePath); err != nil {
		t.Fatal(err)
	}
	secondDB, err := openExistingDB(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondDB.Close() })
	secondary := newService(secondDB, func() time.Time { return now }, func(prefix string) string { return prefix + "_g38c2_secondary" })
	secondary.executionOwner = primary.executionOwner
	return secondary
}

func (s *OrderState) SideForTest(t testing.TB, svc *Service) string {
	t.Helper()
	intent, err := loadOrderIntentFrom(context.Background(), svc.db, s.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	return intent.Side
}

func samePaperAccountState(left, right paperAccountState) bool {
	if left.AccountRef != right.AccountRef || left.PaperAccountingSessionID != right.PaperAccountingSessionID ||
		left.Cash != right.Cash || left.Fees != right.Fees || left.Taxes != right.Taxes || left.Slippage != right.Slippage ||
		left.RealizedPnL != right.RealizedPnL || left.CapitalizedFills != right.CapitalizedFills || len(left.Lots) != len(right.Lots) {
		return false
	}
	for symbol, lots := range left.Lots {
		want := right.Lots[symbol]
		if len(lots) != len(want) {
			return false
		}
		for index := range lots {
			if lots[index] != want[index] {
				return false
			}
		}
	}
	return true
}
