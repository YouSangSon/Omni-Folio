package orderdomain_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"omni-folio/services/core/internal/orderdomain"
)

func TestTransitionOwnsStateAndWireCompatibility(t *testing.T) {
	state := orderdomain.NewState("order-1", "client-1", "account-1", "10", "1000")
	events := []orderdomain.Event{
		{Type: "INTENT_RECORDED"},
		{Type: "RISK_APPROVED"},
		{Type: "SUBMIT_DISPATCHED"},
		{Type: "FILL_RECORDED", ProviderOrderRef: "provider-order-1", Quantity: "4"},
	}
	for _, event := range events {
		before := *state
		if state.ProviderOrderRefs != nil {
			before.ProviderOrderRefs = append([]string{}, state.ProviderOrderRefs...)
		}
		next, err := orderdomain.Transition(state, event)
		if err != nil {
			t.Fatalf("transition %s: %v", event.Type, err)
		}
		if !reflect.DeepEqual(*state, before) {
			t.Fatalf("transition mutated input: before=%+v after=%+v", before, *state)
		}
		state = next
	}

	want := orderdomain.State{
		OrderID: "order-1", ClientOrderID: "client-1", AccountRef: "account-1", Status: "PARTIALLY_FILLED",
		Quantity: "10", LimitPrice: "1000", FilledQuantity: "4", ProviderOrderRefs: []string{"provider-order-1"},
	}
	if !reflect.DeepEqual(*state, want) {
		t.Fatalf("state=%+v want=%+v", *state, want)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	const wantJSON = `{"order_id":"order-1","client_order_id":"client-1","account_ref":"account-1","status":"PARTIALLY_FILLED","quantity":"10","limit_price":"1000","filled_quantity":"4","provider_order_refs":["provider-order-1"]}`
	if string(raw) != wantJSON {
		t.Fatalf("state JSON=%s want=%s", raw, wantJSON)
	}
}

func TestTransitionValidSequences(t *testing.T) {
	tests := []struct {
		name   string
		events []orderdomain.Event
		want   orderdomain.State
	}{
		{
			name: "risk rejection is terminal", events: []orderdomain.Event{{Type: "INTENT_RECORDED"}, {Type: "RISK_REJECTED"}},
			want: transitionState("RISK_REJECTED", "0", "", nil),
		},
		{
			name: "unknown submit is explicitly rejected",
			events: []orderdomain.Event{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"}, {Type: "SUBMIT_REJECTED"},
			},
			want: transitionState("REJECTED", "0", "", nil),
		},
		{
			name: "unknown submit fill accumulates canonical decimals",
			events: []orderdomain.Event{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"},
				transitionFill("3"), transitionFill("2"), transitionFill("5"),
			},
			want: transitionState("FILLED", "10", "", []string{transitionProviderOrderRef}),
		},
		{
			name: "acknowledged open order is canceled",
			events: []orderdomain.Event{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"}, transitionAcknowledgement(),
				{Type: "CANCEL_DISPATCHED"}, {Type: "CANCEL_ACKNOWLEDGED"},
			},
			want: transitionState("CANCELED", "0", "", []string{transitionProviderOrderRef}),
		},
		{
			name: "cancel rejection restores open state",
			events: []orderdomain.Event{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"}, transitionAcknowledgement(),
				{Type: "CANCEL_DISPATCHED"}, {Type: "CANCEL_REJECTED"},
			},
			want: transitionState("OPEN", "0", "", []string{transitionProviderOrderRef}),
		},
		{
			name: "cancel rejection restores partial state",
			events: []orderdomain.Event{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"}, transitionAcknowledgement(),
				transitionFill("4"), {Type: "CANCEL_DISPATCHED"}, {Type: "CANCEL_REJECTED"},
			},
			want: transitionState("PARTIALLY_FILLED", "4", "", []string{transitionProviderOrderRef}),
		},
		{
			name: "fill during unknown cancel wins before cancel rejection",
			events: []orderdomain.Event{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"}, transitionAcknowledgement(),
				transitionFill("8"), {Type: "CANCEL_DISPATCHED"}, transitionFill("2"), {Type: "CANCEL_REJECTED"},
			},
			want: transitionState("FILLED", "10", "", []string{transitionProviderOrderRef}),
		},
		{
			name: "late partial fill preserves canceled status",
			events: []orderdomain.Event{
				{Type: "INTENT_RECORDED"}, {Type: "RISK_APPROVED"}, {Type: "SUBMIT_DISPATCHED"}, transitionAcknowledgement(),
				transitionFill("3"), {Type: "CANCEL_DISPATCHED"}, {Type: "CANCEL_ACKNOWLEDGED"}, transitionFill("2"),
			},
			want: transitionState("CANCELED", "5", "", []string{transitionProviderOrderRef}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := transitionState("", "0", "", nil)
			for _, event := range test.events {
				before := cloneState(state)
				next, err := orderdomain.Transition(&state, event)
				if err != nil {
					t.Fatalf("event %s: %v", event.Type, err)
				}
				if !reflect.DeepEqual(state, before) {
					t.Fatalf("transition mutated input: before=%+v after=%+v", before, state)
				}
				state = *next
			}
			if !reflect.DeepEqual(state, test.want) {
				t.Fatalf("state=%+v want=%+v", state, test.want)
			}
		})
	}
}

func TestTransitionAccumulatesCanonicalDecimalsExactly(t *testing.T) {
	state := orderdomain.State{
		Status: "OPEN", Quantity: "0.3", FilledQuantity: "0.1", ProviderOrderRefs: []string{transitionProviderOrderRef},
	}
	next, err := orderdomain.Transition(&state, transitionFill("0.2"))
	if err != nil {
		t.Fatal(err)
	}
	if next.Status != "FILLED" || next.FilledQuantity != "0.3" {
		t.Fatalf("state=%+v", next)
	}
}

func TestTransitionInvalidMatrixLeavesStateUnchanged(t *testing.T) {
	tests := []struct {
		name    string
		state   orderdomain.State
		event   orderdomain.Event
		wantErr string
	}{
		{"duplicate intent", transitionState("RECORDED", "0", "", nil), orderdomain.Event{Type: "INTENT_RECORDED"}, "intent was already recorded"},
		{"risk approval before intent", transitionState("", "0", "", nil), orderdomain.Event{Type: "RISK_APPROVED"}, "risk approval requires a recorded order"},
		{"risk rejection after approval", transitionState("READY", "0", "", nil), orderdomain.Event{Type: "RISK_REJECTED"}, "risk rejection requires a recorded order"},
		{"submit before risk", transitionState("RECORDED", "0", "", nil), orderdomain.Event{Type: "SUBMIT_DISPATCHED"}, "submit dispatch requires a ready order"},
		{"submit ack outside unknown submit", transitionState("OPEN", "0", "", []string{transitionProviderOrderRef}), transitionAcknowledgement(), "submit acknowledgement requires an unknown submit"},
		{"submit reject without pending submit", transitionState("SUBMIT_UNKNOWN", "0", "", nil), orderdomain.Event{Type: "SUBMIT_REJECTED"}, "submit rejection requires an unknown submit"},
		{"fill before submit", transitionState("READY", "0", "", nil), transitionFill("1"), "fill is not valid for the current order state"},
		{"fill changes provider order ref", transitionState("OPEN", "0", "", []string{transitionProviderOrderRef}), orderdomain.Event{Type: "FILL_RECORDED", ProviderOrderRef: "provider-order-2", Quantity: "1"}, "provider order reference changed"},
		{"overfill", transitionState("OPEN", "9", "", []string{transitionProviderOrderRef}), transitionFill("2"), "fill exceeds order quantity"},
		{"cancel before open", transitionState("READY", "0", "", nil), orderdomain.Event{Type: "CANCEL_DISPATCHED"}, "cancel dispatch requires an open order"},
		{"cancel ack outside unknown cancel", transitionState("OPEN", "0", "", []string{transitionProviderOrderRef}), orderdomain.Event{Type: "CANCEL_ACKNOWLEDGED"}, "cancel acknowledgement requires an unknown cancel"},
		{"cancel reject without pending cancel", transitionState("CANCEL_UNKNOWN", "0", "", []string{transitionProviderOrderRef}), orderdomain.Event{Type: "CANCEL_REJECTED"}, "cancel rejection requires an unknown cancel"},
		{"unsupported event", transitionState("RECORDED", "0", "", nil), orderdomain.Event{Type: "PROVIDER_NOT_FOUND"}, `unsupported order event "PROVIDER_NOT_FOUND"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := cloneState(test.state)
			if _, err := orderdomain.Transition(&test.state, test.event); err == nil || err.Error() != test.wantErr {
				t.Fatalf("error=%v want=%q", err, test.wantErr)
			}
			if !reflect.DeepEqual(test.state, before) {
				t.Fatalf("rejected transition mutated input: before=%+v after=%+v", before, test.state)
			}
		})
	}
}

func TestSameProviderExecutionIgnoresOnlyEventID(t *testing.T) {
	left := orderdomain.Event{EventID: "left"}
	right := left
	right.EventID = "right"
	if !orderdomain.SameProviderExecution(left, right) {
		t.Fatal("EventID changed provider execution equality")
	}

	eventType := reflect.TypeOf(left)
	for index := 0; index < eventType.NumField(); index++ {
		field := eventType.Field(index)
		if field.Name == "EventID" {
			continue
		}
		t.Run(field.Name, func(t *testing.T) {
			changed := right
			value := reflect.ValueOf(&changed).Elem().Field(index)
			switch value.Kind() {
			case reflect.String:
				value.SetString("changed")
			case reflect.Int64:
				value.SetInt(1)
			default:
				t.Fatalf("uncovered Event field kind %s", value.Kind())
			}
			if orderdomain.SameProviderExecution(left, changed) {
				t.Fatalf("SameProviderExecution ignored %s", field.Name)
			}
		})
	}
}

func TestTransitionRejectsMalformedQuantitiesWithoutPanicking(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*orderdomain.State, *orderdomain.Event)
		wantErr string
	}{
		{"order quantity", func(state *orderdomain.State, _ *orderdomain.Event) { state.Quantity = "invalid" }, "order quantity is invalid"},
		{"zero order quantity", func(state *orderdomain.State, _ *orderdomain.Event) { state.Quantity = "0" }, "order quantity is invalid"},
		{"filled quantity", func(state *orderdomain.State, _ *orderdomain.Event) { state.FilledQuantity = "invalid" }, "filled quantity is invalid"},
		{"negative filled quantity", func(state *orderdomain.State, _ *orderdomain.Event) { state.FilledQuantity = "-1" }, "filled quantity is invalid"},
		{"fill quantity", func(_ *orderdomain.State, event *orderdomain.Event) { event.Quantity = "invalid" }, "fill quantity is invalid"},
		{"zero fill quantity", func(_ *orderdomain.State, event *orderdomain.Event) { event.Quantity = "0" }, "fill quantity is invalid"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := orderdomain.State{
				Status: "OPEN", Quantity: "10", FilledQuantity: "1", ProviderOrderRefs: []string{"provider-order-1"},
			}
			event := orderdomain.Event{Type: "FILL_RECORDED", ProviderOrderRef: "provider-order-1", Quantity: "1"}
			test.mutate(&state, &event)
			before := state
			before.ProviderOrderRefs = append([]string{}, state.ProviderOrderRefs...)

			var err error
			panicked := false
			func() {
				defer func() { panicked = recover() != nil }()
				_, err = orderdomain.Transition(&state, event)
			}()
			if panicked {
				t.Fatal("Transition panicked")
			}
			if err == nil || err.Error() != test.wantErr {
				t.Fatalf("error=%v want=%q", err, test.wantErr)
			}
			if !reflect.DeepEqual(state, before) {
				t.Fatalf("rejected transition mutated input: before=%+v after=%+v", before, state)
			}
		})
	}
}

func TestTransitionRejectsMissingStateWithoutPanicking(t *testing.T) {
	var err error
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		_, err = orderdomain.Transition(nil, orderdomain.Event{Type: "INTENT_RECORDED"})
	}()
	if panicked {
		t.Fatal("Transition panicked")
	}
	if err == nil || err.Error() != "order state is required" {
		t.Fatalf("error=%v", err)
	}
}

const transitionProviderOrderRef = "provider-order-1"

func transitionState(status, filled, pending string, refs []string) orderdomain.State {
	return orderdomain.State{
		OrderID: "order-1", ClientOrderID: "client-1", AccountRef: "account-1", Status: status,
		Quantity: "10", LimitPrice: "1000", FilledQuantity: filled,
		ProviderOrderRefs: append([]string(nil), refs...), PendingAction: pending,
	}
}

func transitionAcknowledgement() orderdomain.Event {
	return orderdomain.Event{Type: "SUBMIT_ACKNOWLEDGED", ProviderOrderRef: transitionProviderOrderRef}
}

func transitionFill(quantity string) orderdomain.Event {
	return orderdomain.Event{Type: "FILL_RECORDED", ProviderOrderRef: transitionProviderOrderRef, Quantity: quantity}
}

func cloneState(state orderdomain.State) orderdomain.State {
	state.ProviderOrderRefs = append([]string(nil), state.ProviderOrderRefs...)
	return state
}
