package main

import "testing"

func TestG38C2PaperFillPolicyExactBuyAndSell(t *testing.T) {
	policy := strategyExecutionPolicy{Fee: "1", Tax: "0.001", SlippageBPS: "10", MaxParticipation: "0.5"}
	tests := []struct {
		name  string
		input paperFillInput
		want  paperCalculatedFill
	}{
		{
			name:  "BUY",
			input: paperFillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "10000", ConsumedCapacity: "0"},
			want:  paperCalculatedFill{Quantity: "2", ReferencePrice: "100", Price: "100.1", Notional: "200.2", Fee: "1", Tax: "0", Slippage: "0.2", CashDelta: "-201.2"},
		},
		{
			name:  "SELL",
			input: paperFillInput{Side: "SELL", Open: "120", Volume: "5", RemainingQuantity: "10", PositionQuantity: "2", ConsumedCapacity: "0"},
			want:  paperCalculatedFill{Quantity: "2", ReferencePrice: "120", Price: "119.88", Notional: "239.76", Fee: "1", Tax: "0.23976", Slippage: "0.24", CashDelta: "238.52024"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Catches wrong direction, missing floor, missing fee, and BUY tax.
			got, ok, err := calculatePaperFill(policy, test.input)
			if err != nil || !ok || got != test.want {
				t.Fatalf("fill=%+v ok=%v err=%v, want %+v", got, ok, err, test.want)
			}
		})
	}
}

func TestG38C2PaperFillPolicyCapsAndNoFill(t *testing.T) {
	policy := strategyExecutionPolicy{Fee: "1", Tax: "0.001", SlippageBPS: "10", MaxParticipation: "0.5"}
	tests := []struct {
		name  string
		input paperFillInput
		want  string
	}{
		{"BUY affordable exactly once", paperFillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "101.1", ConsumedCapacity: "0"}, "1"},
		{"BUY unaffordable", paperFillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "101.09", ConsumedCapacity: "0"}, ""},
		{"zero volume", paperFillInput{Side: "BUY", Open: "100", Volume: "0", RemainingQuantity: "10", Cash: "10000", ConsumedCapacity: "0"}, ""},
		{"exhausted capacity", paperFillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "10000", ConsumedCapacity: "2"}, ""},
		{"SELL position", paperFillInput{Side: "SELL", Open: "120", Volume: "5", RemainingQuantity: "10", PositionQuantity: "1", ConsumedCapacity: "0"}, "1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Catches missing affordability cap.
			got, ok, err := calculatePaperFill(policy, test.input)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if ok || got != (paperCalculatedFill{}) {
					t.Fatalf("fill=%+v ok=%v, want all-zero no fill", got, ok)
				}
				return
			}
			if !ok || got.Quantity != test.want {
				t.Fatalf("fill=%+v ok=%v, want quantity %s", got, ok, test.want)
			}
		})
	}
}

func TestG38C2PaperFillPolicyRejectsInvalidInputs(t *testing.T) {
	policy := strategyExecutionPolicy{Fee: "1", Tax: "0.001", SlippageBPS: "10", MaxParticipation: "0.5"}
	tests := []struct {
		name   string
		policy strategyExecutionPolicy
		input  paperFillInput
	}{
		{"invalid side", policy, paperFillInput{Side: "HOLD", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "10000", ConsumedCapacity: "0"}},
		{"fractional remaining", policy, paperFillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "1.5", Cash: "10000", ConsumedCapacity: "0"}},
		{"fractional consumed capacity", policy, paperFillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "10000", ConsumedCapacity: "0.5"}},
		{"consumed capacity above capacity", policy, paperFillInput{Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "10000", ConsumedCapacity: "3"}},
		{"non-positive sell price", strategyExecutionPolicy{Fee: "1", Tax: "0.001", SlippageBPS: "10000", MaxParticipation: "0.5"}, paperFillInput{Side: "SELL", Open: "100", Volume: "5", RemainingQuantity: "10", PositionQuantity: "10", ConsumedCapacity: "0"}},
		{"negative sell proceeds", strategyExecutionPolicy{Fee: "1000", Tax: "0.001", SlippageBPS: "10", MaxParticipation: "0.5"}, paperFillInput{Side: "SELL", Open: "100", Volume: "5", RemainingQuantity: "10", PositionQuantity: "10", ConsumedCapacity: "0"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Catches negative price/proceeds admission.
			if _, _, err := calculatePaperFill(test.policy, test.input); err == nil {
				t.Fatal("invalid paper fill was admitted")
			}
		})
	}
}
