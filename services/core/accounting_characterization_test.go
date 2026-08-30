package main

import (
	"math/big"
	"testing"
)

func TestArchitectureFIFOAllocationExactExamples(t *testing.T) {
	tests := []struct {
		name, cost, take, quantity, want string
	}{
		{"finite decimal", "10", "1", "4", "2.5"},
		{"recurring decimal uses half even", "1", "1", "3", "0.33333333"},
		{"last fill receives the exact residual", "1", "3", "3", "1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allocation, err := fifoCostAllocation(characterizedRat(t, test.cost), characterizedRat(t, test.take), characterizedRat(t, test.quantity))
			got, formatErr := formatDecimal(allocation)
			if err != nil || formatErr != nil || got != test.want {
				t.Fatalf("allocation=%q err=%v formatErr=%v want=%q", got, err, formatErr, test.want)
			}
		})
	}

	if _, err := fifoCostAllocation(big.NewRat(1, 3), big.NewRat(1, 1), big.NewRat(2, 1)); err == nil {
		t.Fatal("non-decimal lot cost was accepted")
	}
}

func TestArchitecturePaperFillAndAccountingPureExamples(t *testing.T) {
	policy := strategyExecutionPolicy{Fee: "1", Tax: "0.001", SlippageBPS: "10", MaxParticipation: "0.5"}
	calculated, ok, err := calculatePaperFill(policy, paperFillInput{
		Side: "BUY", Open: "100", Volume: "5", RemainingQuantity: "10", Cash: "10000", ConsumedCapacity: "0",
	})
	wantCalculated := paperCalculatedFill{
		Quantity: "2", ReferencePrice: "100", Price: "100.1", Notional: "200.2",
		Fee: "1", Tax: "0", Slippage: "0.2", CashDelta: "-201.2",
	}
	if err != nil || !ok || calculated != wantCalculated {
		t.Fatalf("fill=%+v ok=%v err=%v want=%+v", calculated, ok, err, wantCalculated)
	}

	account := &paperReplayAccount{
		session: &PaperAccountingSession{SessionID: "architecture-paper-session"},
		cash:    big.NewRat(1000, 1), fees: new(big.Rat), taxes: new(big.Rat), slippage: new(big.Rat), pnl: new(big.Rat),
		lots: map[string][]paperReplayLot{},
	}
	steps := []struct {
		side string
		fill paperCalculatedFill
	}{
		{"BUY", paperCalculatedFill{Quantity: "2", Fee: "1", Tax: "0", Slippage: "0.2", CashDelta: "-201.2"}},
		{"BUY", paperCalculatedFill{Quantity: "1", Fee: "1", Tax: "0", Slippage: "0.15", CashDelta: "-151.15"}},
		{"SELL", paperCalculatedFill{Quantity: "1", Fee: "1", Tax: "0.12", Slippage: "0.1", CashDelta: "120"}},
		{"SELL", paperCalculatedFill{Quantity: "2", Fee: "1", Tax: "0.3", Slippage: "0.2", CashDelta: "300"}},
	}
	for _, step := range steps {
		if err := applyPaperCalculatedFill(account, "005930", step.side, step.fill); err != nil {
			t.Fatal(err)
		}
	}
	state, err := formatPaperAccountState("paper-account", account)
	if err != nil {
		t.Fatal(err)
	}
	if state.Cash != "1067.65" || state.Fees != "4" || state.Taxes != "0.42" || state.Slippage != "0.65" ||
		state.RealizedPnL != "67.65" || state.CapitalizedFills != 4 || len(state.Lots) != 0 {
		t.Fatalf("paper accounting=%+v", state)
	}
}

func characterizedRat(t *testing.T, raw string) *big.Rat {
	t.Helper()
	value, err := parseDecimal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
