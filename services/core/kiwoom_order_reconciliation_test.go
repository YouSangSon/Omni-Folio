package main

import (
	"context"
	"strings"
	"testing"
)

func TestK2B0LookupCannotAcknowledgeUnknownSubmitFromTupleMatch(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	unknown := mustReadyAndDispatchK2AOrder(t, svc, "client-k2b0-ack", "k2b0-ack")

	result, err := svc.reconcileKiwoomOrderLookup(context.Background(), unknown.OrderID, k2b0Lookup(k2aAccountRef))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "UNCORRELATED" || result.State.Status != "SUBMIT_UNKNOWN" || result.State.PendingAction != "SUBMIT" {
		t.Fatalf("lookup-only inference resolved an unknown submit: %+v", result)
	}
	if len(result.State.ProviderOrderRefs) != 0 {
		t.Fatalf("lookup-only inference bound a provider order reference: %+v", result.State)
	}
	var acknowledgements int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_events WHERE order_id=? AND event_type='SUBMIT_ACKNOWLEDGED' AND source='reconciliation'`, unknown.OrderID).Scan(&acknowledgements); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 0 {
		t.Fatalf("lookup-only inference appended %d acknowledgements", acknowledgements)
	}
	blocked := mustRecordK2AOrder(t, svc, "client-k2b0-still-blocked")
	if _, err := svc.appendOrderEvent(context.Background(), k2aEvent("k2b0-still-blocked-risk", blocked.OrderID, "RISK_APPROVED")); err == nil {
		t.Fatal("direct risk approval bypassed execution authority")
	}
	if _, err := svc.appendOrderEvent(context.Background(), k2aEvent("k2b0-still-blocked-submit", blocked.OrderID, "SUBMIT_DISPATCHED")); err == nil {
		t.Fatal("lookup-only inference unblocked the account")
	}
}

func TestK2B0KnownOrderConservativeLookupOutcomesDoNotMutate(t *testing.T) {
	tests := []struct {
		name    string
		outcome string
		lookup  func() KiwoomOrderLookup
	}{
		{
			name:    "not found",
			outcome: "NOT_FOUND",
			lookup: func() KiwoomOrderLookup {
				lookup := k2b0Lookup(k2aAccountRef)
				lookup.Orders = nil
				return lookup
			},
		},
		{
			name:    "incomplete",
			outcome: "INCOMPLETE",
			lookup: func() KiwoomOrderLookup {
				lookup := k2b0Lookup(k2aAccountRef)
				lookup.Complete = false
				return lookup
			},
		},
		{
			name:    "execution history incomplete",
			outcome: "INCOMPLETE",
			lookup: func() KiwoomOrderLookup {
				lookup := k2b0Lookup(k2aAccountRef)
				lookup.Orders[0].ExecutionsComplete = false
				return lookup
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			state := mustReadyAndDispatchK2AOrder(t, svc, "client-k2b0-"+test.outcome, "k2b0-"+test.outcome)
			ack := k2aEvent("k2b0-known-"+test.outcome, state.OrderID, "SUBMIT_ACKNOWLEDGED")
			ack.ProviderOrderRef = k2aOrderRef
			state = mustAppendK2AEvent(t, svc, ack, "OPEN")
			var before int
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_events WHERE order_id=?`, state.OrderID).Scan(&before); err != nil {
				t.Fatal(err)
			}

			result, err := svc.reconcileKiwoomOrderLookup(context.Background(), state.OrderID, test.lookup())
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome != test.outcome || result.State.Status != "OPEN" || result.State.PendingAction != "" {
				t.Fatalf("unexpected conservative reconciliation result: %+v", result)
			}
			var after int
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_events WHERE order_id=?`, state.OrderID).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("conservative lookup changed event count: before=%d after=%d", before, after)
			}
		})
	}
}

func TestK2B0ReconcileCompleteExecutionsAreAtomicDeterministicAndIdempotent(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	unknown := mustReadyAndDispatchK2AOrder(t, svc, "client-k2b0-fills", "k2b0-fills")
	ack := k2aEvent("k2b0-fills-known", unknown.OrderID, "SUBMIT_ACKNOWLEDGED")
	ack.ProviderOrderRef = k2aOrderRef
	mustAppendK2AEvent(t, svc, ack, "OPEN")
	partial := k2b0Lookup(k2aAccountRef)
	partial.Orders[0].Executions = []KiwoomExecutionObservation{
		{ProviderExecutionRef: "kiwoom_execution_DDDDDDDDDDDDDDDDDDDDDDDD", Quantity: "1", Price: "1001", OccurredAt: "2026-01-10T15:02:00.1Z"},
		{ProviderExecutionRef: "kiwoom_execution_CCCCCCCCCCCCCCCCCCCCCCCC", Quantity: "2", Price: "1000", OccurredAt: "2026-01-10T15:02:00Z"},
	}
	partial.Orders[0].RemainingQuantity = "7"

	result, err := svc.reconcileKiwoomOrderLookup(context.Background(), unknown.OrderID, partial)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Status != "PARTIALLY_FILLED" || result.State.FilledQuantity != "3" {
		t.Fatalf("partial executions were not reconciled: %+v", result)
	}
	var refs []string
	rows, err := svc.db.Query(`SELECT provider_execution_ref FROM order_events WHERE order_id=? AND event_type='FILL_RECORDED' ORDER BY sequence`, unknown.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(refs, ","), "kiwoom_execution_CCCCCCCCCCCCCCCCCCCCCCCC,kiwoom_execution_DDDDDDDDDDDDDDDDDDDDDDDD"; got != want {
		t.Fatalf("fills were not recorded deterministically: got %q want %q", got, want)
	}

	full := partial
	full.Orders[0].Executions = append(append([]KiwoomExecutionObservation(nil), partial.Orders[0].Executions...),
		KiwoomExecutionObservation{ProviderExecutionRef: "kiwoom_execution_EEEEEEEEEEEEEEEEEEEEEEEE", Quantity: "7", Price: "1002", OccurredAt: "2026-01-10T15:04:00Z"})
	full.Orders[0].RemainingQuantity = "0"
	result, err = svc.reconcileKiwoomOrderLookup(context.Background(), unknown.OrderID, full)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Status != "FILLED" || result.State.FilledQuantity != "10" {
		t.Fatalf("full executions were not reconciled: %+v", result)
	}
	var beforeReplay int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_events WHERE order_id=?`, unknown.OrderID).Scan(&beforeReplay); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.reconcileKiwoomOrderLookup(context.Background(), unknown.OrderID, full); err != nil {
		t.Fatal(err)
	}
	var afterReplay int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_events WHERE order_id=?`, unknown.OrderID).Scan(&afterReplay); err != nil {
		t.Fatal(err)
	}
	if afterReplay != beforeReplay {
		t.Fatalf("same lookup replay appended events: before=%d after=%d", beforeReplay, afterReplay)
	}
	conflict := full
	conflict.Orders = append([]KiwoomOrderObservation(nil), full.Orders...)
	conflict.Orders[0].Executions = append([]KiwoomExecutionObservation(nil), full.Orders[0].Executions...)
	conflict.Orders[0].Executions[2].Price = "1003"
	if _, err := svc.reconcileKiwoomOrderLookup(context.Background(), unknown.OrderID, conflict); err == nil {
		t.Fatal("changed provider execution replay was accepted")
	}
	afterConflict, err := svc.loadOrderState(context.Background(), unknown.OrderID)
	if err != nil || afterConflict.Status != "FILLED" || afterConflict.FilledQuantity != "10" {
		t.Fatalf("changed execution replay mutated state: state=%+v err=%v", afterConflict, err)
	}

	atomic := mustReadyAndDispatchK2AOrder(t, svc, "client-k2b0-atomic", "k2b0-atomic")
	atomicAck := k2aEvent("k2b0-atomic-known", atomic.OrderID, "SUBMIT_ACKNOWLEDGED")
	atomicAck.ProviderOrderRef = "kiwoom_order_ZZZZZZZZZZZZZZZZZZZZZZZZ"
	mustAppendK2AEvent(t, svc, atomicAck, "OPEN")
	invalid := k2b0Lookup(k2aAccountRef)
	invalid.Orders[0].ProviderOrderRef = atomicAck.ProviderOrderRef
	invalid.Orders[0].Executions = []KiwoomExecutionObservation{
		{ProviderExecutionRef: "kiwoom_execution_FFFFFFFFFFFFFFFFFFFFFFFF", Quantity: "1", Price: "1000", OccurredAt: "2026-01-10T15:05:00Z"},
		{ProviderExecutionRef: "raw-execution-id", Quantity: "1", Price: "1000", OccurredAt: "2026-01-10T15:06:00Z"},
	}
	invalid.Orders[0].RemainingQuantity = "8"
	if _, err := svc.reconcileKiwoomOrderLookup(context.Background(), atomic.OrderID, invalid); err == nil {
		t.Fatal("malformed execution lookup was accepted")
	} else if strings.Contains(err.Error(), "raw-execution-id") {
		t.Fatalf("error leaked raw identifier: %q", err)
	}
	state, err := svc.loadOrderState(context.Background(), atomic.OrderID)
	if err != nil || state.Status != "OPEN" || state.FilledQuantity != "0" {
		t.Fatalf("malformed execution lookup partially mutated order: state=%+v err=%v", state, err)
	}
}

func TestK2B0KnownProviderObservationConflictFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*KiwoomOrderObservation)
	}{
		{"different symbol", func(order *KiwoomOrderObservation) { order.Symbol = "000660" }},
		{"submitted before dispatch", func(order *KiwoomOrderObservation) { order.SubmittedAt = "2026-01-10T15:00:00Z" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			state := mustReadyAndDispatchK2AOrder(t, svc, "client-k2b0-conflict-"+strings.ReplaceAll(test.name, " ", "-"), "k2b0-conflict")
			ack := k2aEvent("k2b0-conflict-ack", state.OrderID, "SUBMIT_ACKNOWLEDGED")
			ack.ProviderOrderRef = k2aOrderRef
			state = mustAppendK2AEvent(t, svc, ack, "OPEN")
			lookup := k2b0Lookup(k2aAccountRef)
			test.mutate(&lookup.Orders[0])
			var before int
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_events WHERE order_id=?`, state.OrderID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.reconcileKiwoomOrderLookup(context.Background(), state.OrderID, lookup); err == nil {
				t.Fatal("conflicting known provider observation was accepted")
			}
			var after int
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_events WHERE order_id=?`, state.OrderID).Scan(&after); err != nil {
				t.Fatal(err)
			}
			loaded, err := svc.loadOrderState(context.Background(), state.OrderID)
			if err != nil || after != before || loaded.Status != "OPEN" || loaded.FilledQuantity != "0" {
				t.Fatalf("conflicting observation mutated state: before=%d after=%d state=%+v err=%v", before, after, loaded, err)
			}
		})
	}
}

func TestK2B0ProviderExecutionConflictRollsBackEarlierFills(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	foreignIntent := k2aIntent("client-k2b0-foreign-execution")
	foreignIntent.AccountRef = "kiwoom_account_YYYYYYYYYYYYYYYYYYYYYYYY"
	foreign, err := svc.recordOrderIntent(context.Background(), foreignIntent)
	if err != nil {
		t.Fatal(err)
	}
	foreignLease := mustK2CLease(t, svc, foreign.AccountRef)
	mustAuthorizeK2C(t, svc, foreign.OrderID, foreignLease.FencingToken)
	foreignAck := k2aEvent("k2b0-foreign-ack", foreign.OrderID, "SUBMIT_ACKNOWLEDGED")
	foreignAck.ProviderOrderRef = "kiwoom_order_YYYYYYYYYYYYYYYYYYYYYYYY"
	mustAppendK2AEvent(t, svc, foreignAck, "OPEN")
	foreignFill := k2aFill("k2b0-foreign-fill", foreign.OrderID, "kiwoom_execution_QQQQQQQQQQQQQQQQQQQQQQQQ", "1", "999")
	foreignFill.ProviderOrderRef = foreignAck.ProviderOrderRef
	mustAppendK2AEvent(t, svc, foreignFill, "PARTIALLY_FILLED")

	target := mustReadyAndDispatchK2AOrder(t, svc, "client-k2b0-rollback", "k2b0-rollback")
	targetAck := k2aEvent("k2b0-rollback-ack", target.OrderID, "SUBMIT_ACKNOWLEDGED")
	targetAck.ProviderOrderRef = k2aOrderRef
	mustAppendK2AEvent(t, svc, targetAck, "OPEN")
	lookup := k2b0Lookup(k2aAccountRef)
	lookup.Orders[0].Executions = []KiwoomExecutionObservation{
		{ProviderExecutionRef: "kiwoom_execution_FFFFFFFFFFFFFFFFFFFFFFFF", Quantity: "1", Price: "1000", OccurredAt: "2026-01-10T15:02:00Z"},
		{ProviderExecutionRef: foreignFill.ProviderExecutionRef, Quantity: "1", Price: "1000", OccurredAt: "2026-01-10T15:03:00Z"},
	}
	lookup.Orders[0].RemainingQuantity = "8"
	if _, err := svc.reconcileKiwoomOrderLookup(context.Background(), target.OrderID, lookup); err == nil {
		t.Fatal("conflicting provider execution was accepted")
	}
	state, err := svc.loadOrderState(context.Background(), target.OrderID)
	if err != nil || state.Status != "OPEN" || state.FilledQuantity != "0" {
		t.Fatalf("execution conflict partially mutated target: state=%+v err=%v", state, err)
	}
	var leaked int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_events WHERE provider_execution_ref='kiwoom_execution_FFFFFFFFFFFFFFFFFFFFFFFF'`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("earlier fill escaped rolled-back reconciliation: count=%d", leaked)
	}
}

func TestK2B0ReconcileFailsClosedWithoutLeakingRawOrCrossAccountIdentifiers(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*KiwoomOrderLookup)
		raw    string
	}{
		{"raw account", func(lookup *KiwoomOrderLookup) { lookup.AccountRef = "9876543210" }, "9876543210"},
		{"raw order", func(lookup *KiwoomOrderLookup) { lookup.Orders[0].ProviderOrderRef = "0000042" }, "0000042"},
		{"cross account", func(lookup *KiwoomOrderLookup) { lookup.AccountRef = "kiwoom_account_ZZZZZZZZZZZZZZZZZZZZZZZZ" }, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			unknown := mustReadyAndDispatchK2AOrder(t, svc, "client-k2b0-reject-"+strings.ReplaceAll(test.name, " ", "-"), "k2b0-reject")
			lookup := k2b0Lookup(k2aAccountRef)
			test.mutate(&lookup)

			_, err := svc.reconcileKiwoomOrderLookup(context.Background(), unknown.OrderID, lookup)
			if err == nil {
				t.Fatal("invalid lookup was accepted")
			}
			if test.raw != "" && strings.Contains(err.Error(), test.raw) {
				t.Fatalf("error leaked raw identifier: %q", err)
			}
			state, loadErr := svc.loadOrderState(context.Background(), unknown.OrderID)
			if loadErr != nil || state.Status != "SUBMIT_UNKNOWN" || state.PendingAction != "SUBMIT" {
				t.Fatalf("invalid lookup changed order: state=%+v err=%v", state, loadErr)
			}
		})
	}
}

func k2b0Lookup(accountRef string) KiwoomOrderLookup {
	return KiwoomOrderLookup{
		Provider:   "kiwoom",
		Mode:       "synthetic",
		AccountRef: accountRef,
		ObservedAt: "2026-01-10T15:10:00Z",
		Complete:   true,
		Orders: []KiwoomOrderObservation{{
			ProviderOrderRef:   k2aOrderRef,
			Symbol:             "005930",
			Exchange:           "KRX",
			Side:               "BUY",
			OrderType:          "LIMIT",
			Currency:           "KRW",
			Quantity:           "10",
			LimitPrice:         "1000",
			RemainingQuantity:  "10",
			SubmittedAt:        "2026-01-10T15:01:30Z",
			ExecutionsComplete: true,
		}},
	}
}
