package paperdomain

import (
	"reflect"
	"testing"
)

func TestValueAccountReconcilesExactMoney(t *testing.T) {
	state := AccountState{Cash: "39.9", RealizedPnL: "0", Lots: map[string][]Lot{
		"005930": {{Quantity: "1", Cost: "60.1"}},
	}}

	got, err := ValueAccount("100", state, map[string]string{"005930": "70"})
	want := Valuation{
		Cash: "39.9", OpenCost: "60.1", MarketValue: "70", RealizedPnL: "0",
		UnrealizedPnL: "9.9", TotalPnL: "9.9", Equity: "109.9",
		Positions: map[string]PositionValuation{
			"005930": {Quantity: "1", OpenCost: "60.1", MarketValue: "70", UnrealizedPnL: "9.9"},
		},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ValueAccount() = %+v, %v; want %+v, nil", got, err, want)
	}
}

func TestValueAccountCashOnlyAndMultiSymbol(t *testing.T) {
	tests := []struct {
		name         string
		startingCash string
		state        AccountState
		closes       map[string]string
		want         Valuation
	}{
		{
			name:         "cash only",
			startingCash: "100",
			state:        AccountState{Cash: "101.25", RealizedPnL: "1.25"},
			closes:       map[string]string{},
			want: Valuation{
				Cash: "101.25", RealizedPnL: "1.25", TotalPnL: "1.25", Equity: "101.25",
				Positions: map[string]PositionValuation{},
			},
		},
		{
			name:         "multiple symbols",
			startingCash: "1000",
			state: AccountState{Cash: "100", RealizedPnL: "0", Lots: map[string][]Lot{
				"005930": {{Quantity: "2", Cost: "120.2"}, {Quantity: "1", Cost: "60.1"}},
				"000660": {{Quantity: "3", Cost: "90"}},
			}},
			closes: map[string]string{"000660": "40.5", "005930": "70.25"},
			want: Valuation{
				Cash: "100", OpenCost: "270.3", MarketValue: "261.5", RealizedPnL: "0",
				UnrealizedPnL: "-8.8", TotalPnL: "-8.8", Equity: "361.5",
				Positions: map[string]PositionValuation{
					"000660": {Quantity: "3", OpenCost: "90", MarketValue: "121.5", UnrealizedPnL: "31.5"},
					"005930": {Quantity: "3", OpenCost: "180.3", MarketValue: "140.5", UnrealizedPnL: "-39.8"},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ValueAccount(test.startingCash, test.state, test.closes)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ValueAccount() = %+v, %v; want %+v, nil", got, err, test.want)
			}
		})
	}
}

func TestValueAccountIsIndependentOfMapOrder(t *testing.T) {
	state := AccountState{Cash: "100", RealizedPnL: "0", Lots: map[string][]Lot{
		"005930": {{Quantity: "1", Cost: "60"}},
		"000660": {{Quantity: "2", Cost: "80"}},
	}}
	closes := map[string]string{"005930": "70", "000660": "45"}
	want := Valuation{
		Cash: "100", OpenCost: "140", MarketValue: "160", RealizedPnL: "0",
		UnrealizedPnL: "20", TotalPnL: "20", Equity: "260",
		Positions: map[string]PositionValuation{
			"000660": {Quantity: "2", OpenCost: "80", MarketValue: "90", UnrealizedPnL: "10"},
			"005930": {Quantity: "1", OpenCost: "60", MarketValue: "70", UnrealizedPnL: "10"},
		},
	}
	got, err := ValueAccount("100", state, closes)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ValueAccount() = %+v, %v; want %+v, nil", got, err, want)
	}
	gotAgain, err := ValueAccount("100", state, map[string]string{"000660": "45", "005930": "70"})
	if err != nil || !reflect.DeepEqual(gotAgain, got) {
		t.Fatalf("ValueAccount() with reordered marks = %+v, %v; want %+v, nil", gotAgain, err, got)
	}
}

func TestValueAccountRejectsInvalidInputWithoutPartialOutput(t *testing.T) {
	validState := AccountState{Cash: "39.9", RealizedPnL: "0", Lots: map[string][]Lot{
		"005930": {{Quantity: "1", Cost: "60.1"}},
	}}
	tests := []struct {
		name         string
		startingCash string
		state        AccountState
		closes       map[string]string
	}{
		{name: "missing mark", startingCash: "100", state: validState, closes: map[string]string{}},
		{name: "extra mark", startingCash: "100", state: AccountState{Cash: "100", RealizedPnL: "0"}, closes: map[string]string{"005930": "70"}},
		{name: "zero mark", startingCash: "100", state: validState, closes: map[string]string{"005930": "0"}},
		{name: "negative mark", startingCash: "100", state: validState, closes: map[string]string{"005930": "-1"}},
		{name: "non-canonical mark", startingCash: "100", state: validState, closes: map[string]string{"005930": "70.0"}},
		{name: "negative quantity", startingCash: "100", state: AccountState{Cash: "39.9", RealizedPnL: "0", Lots: map[string][]Lot{
			"005930": {{Quantity: "-1", Cost: "60.1"}},
		}}, closes: map[string]string{"005930": "70"}},
		{name: "zero quantity", startingCash: "100", state: AccountState{Cash: "100", RealizedPnL: "0", Lots: map[string][]Lot{
			"005930": {{Quantity: "0", Cost: "0"}},
		}}, closes: map[string]string{"005930": "70"}},
		{name: "negative cost", startingCash: "100", state: AccountState{Cash: "39.9", RealizedPnL: "0", Lots: map[string][]Lot{
			"005930": {{Quantity: "1", Cost: "-60.1"}},
		}}, closes: map[string]string{"005930": "70"}},
		{name: "negative cash", startingCash: "100", state: AccountState{Cash: "-1", RealizedPnL: "0"}, closes: map[string]string{}},
		{name: "malformed realized PnL", startingCash: "100", state: AccountState{Cash: "100", RealizedPnL: "bad"}, closes: map[string]string{}},
		{name: "malformed fee", startingCash: "100", state: AccountState{Cash: "100", RealizedPnL: "0", Fees: "bad"}, closes: map[string]string{}},
		{name: "zero starting cash", startingCash: "0", state: AccountState{Cash: "0", RealizedPnL: "0"}, closes: map[string]string{}},
		{name: "negative starting cash", startingCash: "-1", state: AccountState{Cash: "0", RealizedPnL: "0"}, closes: map[string]string{}},
		{name: "non-canonical starting cash", startingCash: "100.0", state: AccountState{Cash: "100", RealizedPnL: "0"}, closes: map[string]string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ValueAccount(test.startingCash, test.state, test.closes)
			if err == nil || len(got.Positions) != 0 || !reflect.DeepEqual(got, Valuation{}) {
				t.Fatalf("ValueAccount() = %+v, %v; want zero valuation and an error", got, err)
			}
		})
	}
}

func TestCalculatePerformanceUsesRawPeakAndHalfEvenScale8(t *testing.T) {
	got, err := CalculatePerformance("100", []string{"120", "90", "110", "130", "100"})
	want := []PerformancePoint{
		{Equity: "120", PeakEquity: "120", PeriodReturnState: "defined", PeriodReturn: "0.2", CumulativeReturn: "0.2", Drawdown: "0", MaxDrawdown: "0"},
		{Equity: "90", PeakEquity: "120", PeriodReturnState: "defined", PeriodReturn: "-0.25", CumulativeReturn: "-0.1", Drawdown: "0.25", MaxDrawdown: "0.25"},
		{Equity: "110", PeakEquity: "120", PeriodReturnState: "defined", PeriodReturn: "0.22222222", CumulativeReturn: "0.1", Drawdown: "0.08333333", MaxDrawdown: "0.25"},
		{Equity: "130", PeakEquity: "130", PeriodReturnState: "defined", PeriodReturn: "0.18181818", CumulativeReturn: "0.3", Drawdown: "0", MaxDrawdown: "0.25"},
		{Equity: "100", PeakEquity: "130", PeriodReturnState: "defined", PeriodReturn: "-0.23076923", CumulativeReturn: "0", Drawdown: "0.23076923", MaxDrawdown: "0.25"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("CalculatePerformance() = %+v, %v; want %+v, nil", got, err, want)
	}
}

func TestCalculatePerformanceRecurringAndHalfEvenScale8(t *testing.T) {
	tests := []struct {
		name         string
		startingCash string
		equities     []string
		want         []PerformancePoint
	}{
		{
			name:         "recurring one third",
			startingCash: "3",
			equities:     []string{"4"},
			want:         []PerformancePoint{{Equity: "4", PeakEquity: "4", PeriodReturnState: "defined", PeriodReturn: "0.33333333", CumulativeReturn: "0.33333333", Drawdown: "0", MaxDrawdown: "0"}},
		},
		{
			name:         "positive tie rounds to even",
			startingCash: "100",
			equities:     []string{"100.0000015"},
			want:         []PerformancePoint{{Equity: "100.0000015", PeakEquity: "100.0000015", PeriodReturnState: "defined", PeriodReturn: "0.00000002", CumulativeReturn: "0.00000002", Drawdown: "0", MaxDrawdown: "0"}},
		},
		{
			name:         "negative tie rounds to even",
			startingCash: "100",
			equities:     []string{"99.9999985"},
			want:         []PerformancePoint{{Equity: "99.9999985", PeakEquity: "100", PeriodReturnState: "defined", PeriodReturn: "-0.00000002", CumulativeReturn: "-0.00000002", Drawdown: "0.00000002", MaxDrawdown: "0.00000002"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := CalculatePerformance(test.startingCash, test.equities)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("CalculatePerformance() = %+v, %v; want %+v, nil", got, err, test.want)
			}
		})
	}
}

func TestCalculatePerformanceTotalLossAndZeroPreviousEquity(t *testing.T) {
	got, err := CalculatePerformance("100", []string{"0", "10"})
	want := []PerformancePoint{
		{Equity: "0", PeakEquity: "100", PeriodReturnState: "defined", PeriodReturn: "-1", CumulativeReturn: "-1", Drawdown: "1", MaxDrawdown: "1"},
		{Equity: "10", PeakEquity: "100", PeriodReturnState: "undefined_zero_denominator", PeriodReturn: "", CumulativeReturn: "-0.9", Drawdown: "0.9", MaxDrawdown: "1"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("CalculatePerformance() = %+v, %v; want %+v, nil", got, err, want)
	}
}

func TestCalculatePerformanceEmptyAndMalformedInput(t *testing.T) {
	got, err := CalculatePerformance("100", nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("CalculatePerformance(empty) = %+v, %v; want empty series, nil", got, err)
	}

	tests := []struct {
		name         string
		startingCash string
		equities     []string
	}{
		{name: "zero starting cash", startingCash: "0", equities: []string{"1"}},
		{name: "negative starting cash", startingCash: "-1", equities: []string{"1"}},
		{name: "non-canonical starting cash", startingCash: "100.0", equities: []string{"101"}},
		{name: "malformed equity after valid point", startingCash: "100", equities: []string{"110", "bad"}},
		{name: "non-canonical equity", startingCash: "100", equities: []string{"100.0"}},
		{name: "negative equity", startingCash: "100", equities: []string{"-1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := CalculatePerformance(test.startingCash, test.equities)
			if err == nil || len(got) != 0 {
				t.Fatalf("CalculatePerformance() = %+v, %v; want no partial output and an error", got, err)
			}
		})
	}
}
