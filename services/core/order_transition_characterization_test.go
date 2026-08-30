package main

import (
	"context"
	"reflect"
	"testing"
)

func TestArchitectureOrderTransitionValidSequences(t *testing.T) {
	tests := []struct {
		name   string
		events []OrderEvent
		want   OrderState
	}{
		{
			name:   "risk rejection is terminal",
			events: []OrderEvent{{Type: "INTENT_RECORDED"}, {Type: "RISK_REJECTED"}},
			want:   characterizedOrderState("RISK_REJECTED", "0", "", nil),
		},
		{
			name: "unknown submit is explicitly rejected",
			events: []OrderEvent{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"}, {Type: "SUBMIT_REJECTED"},
			},
			want: characterizedOrderState("REJECTED", "0", "", nil),
		},
		{
			name: "fill resolves unknown submit and accumulates exactly",
			events: []OrderEvent{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"},
				characterizedFill("3"), characterizedFill("2"), characterizedFill("5"),
			},
			want: characterizedOrderState("FILLED", "10", "", []string{k2aOrderRef}),
		},
		{
			name: "acknowledged open order is canceled",
			events: []OrderEvent{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"}, characterizedAck(),
				{Type: "CANCEL_DISPATCHED"}, {Type: "CANCEL_ACKNOWLEDGED"},
			},
			want: characterizedOrderState("CANCELED", "0", "", []string{k2aOrderRef}),
		},
		{
			name: "cancel rejection restores open state",
			events: []OrderEvent{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"}, characterizedAck(),
				{Type: "CANCEL_DISPATCHED"}, {Type: "CANCEL_REJECTED"},
			},
			want: characterizedOrderState("OPEN", "0", "", []string{k2aOrderRef}),
		},
		{
			name: "cancel rejection restores partial state",
			events: []OrderEvent{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"}, characterizedAck(),
				characterizedFill("4"), {Type: "CANCEL_DISPATCHED"}, {Type: "CANCEL_REJECTED"},
			},
			want: characterizedOrderState("PARTIALLY_FILLED", "4", "", []string{k2aOrderRef}),
		},
		{
			name: "fill during unknown cancel wins before cancel rejection",
			events: []OrderEvent{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"}, characterizedAck(),
				characterizedFill("8"), {Type: "CANCEL_DISPATCHED"}, characterizedFill("2"), {Type: "CANCEL_REJECTED"},
			},
			want: characterizedOrderState("FILLED", "10", "", []string{k2aOrderRef}),
		},
		{
			name: "late partial fill preserves canceled status",
			events: []OrderEvent{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"}, characterizedAck(),
				characterizedFill("3"), {Type: "CANCEL_DISPATCHED"}, {Type: "CANCEL_ACKNOWLEDGED"}, characterizedFill("2"),
			},
			want: characterizedOrderState("CANCELED", "5", "", []string{k2aOrderRef}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := characterizedOrderState("", "0", "", nil)
			for _, event := range test.events {
				before := state
				before.ProviderOrderRefs = append([]string(nil), state.ProviderOrderRefs...)
				next, err := applyOrderEvent(&state, event)
				if err != nil {
					t.Fatalf("event %s: %v", event.Type, err)
				}
				if !reflect.DeepEqual(state, before) {
					t.Fatalf("transition mutated its input: before=%+v after=%+v", before, state)
				}
				state = *next
			}
			if !reflect.DeepEqual(state, test.want) {
				t.Fatalf("state=%+v want=%+v", state, test.want)
			}
		})
	}
}

func TestArchitectureOrderTransitionInvalidInputsLeaveStateUnchanged(t *testing.T) {
	tests := []struct {
		name  string
		state OrderState
		event OrderEvent
	}{
		{"duplicate intent", characterizedOrderState("RECORDED", "0", "", nil), OrderEvent{Type: "INTENT_RECORDED"}},
		{"risk approval before intent", characterizedOrderState("", "0", "", nil), OrderEvent{Type: "RISK_APPROVED"}},
		{"risk rejection after approval", characterizedOrderState("READY", "0", "", nil), OrderEvent{Type: "RISK_REJECTED"}},
		{"submit before risk", characterizedOrderState("RECORDED", "0", "", nil), OrderEvent{Type: "SUBMIT_DISPATCHED"}},
		{"submit ack outside unknown submit", characterizedOrderState("OPEN", "0", "", []string{k2aOrderRef}), characterizedAck()},
		{"submit reject without pending submit", characterizedOrderState("SUBMIT_UNKNOWN", "0", "", nil), OrderEvent{Type: "SUBMIT_REJECTED"}},
		{"fill before submit", characterizedOrderState("READY", "0", "", nil), characterizedFill("1")},
		{"fill changes provider order ref", characterizedOrderState("OPEN", "0", "", []string{k2aOrderRef}), func() OrderEvent {
			event := characterizedFill("1")
			event.ProviderOrderRef = "kiwoom_order_CCCCCCCCCCCCCCCCCCCCCCCC"
			return event
		}()},
		{"overfill", characterizedOrderState("OPEN", "9", "", []string{k2aOrderRef}), characterizedFill("2")},
		{"cancel before open", characterizedOrderState("READY", "0", "", nil), OrderEvent{Type: "CANCEL_DISPATCHED"}},
		{"cancel ack outside unknown cancel", characterizedOrderState("OPEN", "0", "", []string{k2aOrderRef}), OrderEvent{Type: "CANCEL_ACKNOWLEDGED"}},
		{"cancel reject without pending cancel", characterizedOrderState("CANCEL_UNKNOWN", "0", "", []string{k2aOrderRef}), OrderEvent{Type: "CANCEL_REJECTED"}},
		{"unsupported event", characterizedOrderState("RECORDED", "0", "", nil), OrderEvent{Type: "PROVIDER_NOT_FOUND"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := test.state
			before.ProviderOrderRefs = append([]string(nil), test.state.ProviderOrderRefs...)
			if _, err := applyOrderEvent(&test.state, test.event); err == nil {
				t.Fatal("invalid transition was accepted")
			}
			if !reflect.DeepEqual(test.state, before) {
				t.Fatalf("rejected transition mutated state: before=%+v after=%+v", before, test.state)
			}
		})
	}
}

func TestArchitectureOrderEventReplayIsIdempotentAbovePureTransition(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	state := mustReadyAndDispatchK2AOrder(t, svc, "architecture-idempotency", "unused")
	event := k2aFill("architecture-idempotent-fill", state.OrderID, "kiwoom_execution_DDDDDDDDDDDDDDDDDDDDDDDD", "2", "1000")

	first := mustAppendK2AEvent(t, svc, event, "PARTIALLY_FILLED")
	replayed := mustAppendK2AEvent(t, svc, event, "PARTIALLY_FILLED")
	var rows int
	if err := svc.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM order_events WHERE event_id=?`, event.EventID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || !reflect.DeepEqual(first, replayed) || replayed.FilledQuantity != "2" {
		t.Fatalf("rows=%d first=%+v replayed=%+v", rows, first, replayed)
	}
}

func characterizedOrderState(status, filled, pending string, refs []string) OrderState {
	if refs == nil {
		refs = []string{}
	}
	return OrderState{
		OrderID: "architecture-order", ClientOrderID: "architecture-client", AccountRef: k2aAccountRef,
		Status: status, Quantity: "10", LimitPrice: "1000", FilledQuantity: filled,
		ProviderOrderRefs: append([]string(nil), refs...), PendingAction: pending,
	}
}

func characterizedAck() OrderEvent {
	return OrderEvent{Type: "SUBMIT_ACKNOWLEDGED", ProviderOrderRef: k2aOrderRef}
}

func characterizedFill(quantity string) OrderEvent {
	return OrderEvent{Type: "FILL_RECORDED", ProviderOrderRef: k2aOrderRef, Quantity: quantity}
}
