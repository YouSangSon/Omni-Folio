package researchdomain

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"omni-folio/services/core/internal/paperdomain"
)

func foldBars(closes ...string) []Bar {
	bars := make([]Bar, len(closes))
	for index, close := range closes {
		bars[index] = Bar{At: fmt.Sprintf("2026-01-%02dT00:00:00Z", index+1), Open: close, Close: close, Volume: "100"}
	}
	return bars
}

func foldExecution() Execution {
	return Execution{
		StartingCash: "1000", DelayBars: 1,
		Policy: paperdomain.ExecutionPolicy{Fee: "0", Tax: "0", SlippageBPS: "0", MaxParticipation: "1"},
	}
}

func TestSimulateFoldCarriesExactCashAndPartialOrders(t *testing.T) {
	bars := foldBars("100", "100", "100", "100", "100", "100", "100", "100")
	for index := range bars {
		bars[index].Volume = "5"
	}
	bars[len(bars)-1].Volume = "10"
	execution := foldExecution()
	execution.Policy = paperdomain.ExecutionPolicy{Fee: "1", Tax: "0.01", SlippageBPS: "10", MaxParticipation: "0.5"}
	strategy := Strategy{Quantity: "4", FastWindow: 2, SlowWindow: 3}
	metrics, trace, err := SimulateFold(context.Background(), bars, nil, strategy, execution, true)
	want := Metrics{
		AfterCostReturn: "-0.007796", MaxDrawdown: "0.007796", TradeCount: "1",
		TradeCountDefinition: "completed_long_only_round_trips", FillCount: "3", Turnover: "0.8",
		Fees: "3", Taxes: "3.996", SlippageCost: "0.8", PartialFillCount: "1", LookaheadViolations: "0",
	}
	wantTrace := []FillTrace{
		{SignalAt: bars[0].At, EligibleAt: bars[1].At, FillAt: bars[1].At, Side: "BUY", Quantity: "2", Price: "100.1"},
		{SignalAt: bars[0].At, EligibleAt: bars[1].At, FillAt: bars[2].At, Side: "BUY", Quantity: "2", Price: "100.1"},
		{SignalAt: bars[6].At, EligibleAt: bars[7].At, FillAt: bars[7].At, Side: "SELL", Quantity: "4", Price: "99.9"},
	}
	if err != nil || metrics != want || !reflect.DeepEqual(trace, wantTrace) {
		t.Fatalf("SimulateFold() = %+v, %+v, %v; want %+v, %+v", metrics, trace, err, want, wantTrace)
	}
	for _, startingCash := range []string{"401.39", "1"} {
		execution.StartingCash = startingCash
		metrics, trace, err := SimulateFold(context.Background(), bars, nil, strategy, execution, true)
		if err == nil || !strings.Contains(err.Error(), "cannot complete every delayed") || metrics != (Metrics{}) || trace != nil {
			t.Fatalf("unfinished fold with cash %s = %+v, %+v, %v", startingCash, metrics, trace, err)
		}
	}
	execution.StartingCash = "1000"
	bars[len(bars)-1].Volume = "0"
	if _, _, err := SimulateFold(context.Background(), bars, nil, strategy, execution, true); err == nil || !strings.Contains(err.Error(), "cannot complete every delayed") {
		t.Fatalf("unfinished liquidation error = %v", err)
	}
}

func TestSimulateFoldWarmupIsSignalContextOnly(t *testing.T) {
	series := foldBars("3", "2", "1", "4", "5", "6", "7", "8", "9", "10")
	strategy := Strategy{Quantity: "1", FastWindow: 2, SlowWindow: 3}
	execution := foldExecution()
	execution.StartingCash = "100"
	metrics, trace, err := SimulateFold(context.Background(), series[3:], series[:3], strategy, execution, false)
	wantTrace := []FillTrace{
		{SignalAt: series[3].At, EligibleAt: series[4].At, FillAt: series[4].At, Side: "BUY", Quantity: "1", Price: "5"},
		{SignalAt: series[8].At, EligibleAt: series[9].At, FillAt: series[9].At, Side: "SELL", Quantity: "1", Price: "10"},
	}
	if err != nil || metrics.AfterCostReturn != "0.05" || metrics.MaxDrawdown != "0" || !reflect.DeepEqual(trace, wantTrace) {
		t.Fatalf("warmup crossover = %+v, %+v, %v; want %+v", metrics, trace, err, wantTrace)
	}
	metrics, trace, err = SimulateFold(context.Background(), series[4:], series[:4], strategy, execution, false)
	if err != nil || metrics.TradeCount != "0" || len(trace) != 0 || metrics.AfterCostReturn != "0" {
		t.Fatalf("warmup-only crossover traded: %+v, %+v, %v", metrics, trace, err)
	}
}

func TestSimulateFoldUsesTheDeclaredExecutionDelay(t *testing.T) {
	bars := foldBars("100", "100", "100", "100", "100", "100")
	execution := foldExecution()
	execution.DelayBars = 2
	metrics, trace, err := SimulateFold(context.Background(), bars, nil, Strategy{Quantity: "1", FastWindow: 2, SlowWindow: 3}, execution, true)
	want := []FillTrace{
		{SignalAt: bars[0].At, EligibleAt: bars[2].At, FillAt: bars[2].At, Side: "BUY", Quantity: "1", Price: "100"},
		{SignalAt: bars[3].At, EligibleAt: bars[5].At, FillAt: bars[5].At, Side: "SELL", Quantity: "1", Price: "100"},
	}
	if err != nil || metrics.LookaheadViolations != "0" || !reflect.DeepEqual(trace, want) {
		t.Fatalf("delayed fold = %+v, %+v, %v; want %+v", metrics, trace, err, want)
	}
}

func TestSimulateFoldIgnoresOppositeSignalsWhilePending(t *testing.T) {
	series := foldBars("3", "2", "4", "1", "4", "1", "4", "4", "4", "4")
	for index := range series {
		series[index].Open, series[index].Volume = "100", "1"
	}
	metrics, trace, err := SimulateFold(context.Background(), series[2:], series[:2], Strategy{Quantity: "3", FastWindow: 1, SlowWindow: 2}, foldExecution(), false)
	if err != nil || len(trace) != 6 || metrics.TradeCount != "1" || metrics.PartialFillCount != "4" || metrics.MaxDrawdown != "0.297" {
		t.Fatalf("pending orders = %+v, %+v, %v", metrics, trace, err)
	}
	for index, fill := range trace {
		wantSide, signalIndex := "BUY", 2
		if index >= 3 {
			wantSide, signalIndex = "SELL", 5
		}
		if fill.Side != wantSide || fill.SignalAt != series[signalIndex].At || fill.EligibleAt != series[signalIndex+1].At || fill.FillAt != series[index+3].At || fill.Quantity != "1" {
			t.Fatalf("fill %d = %+v; opposite signal changed the pending order", index, fill)
		}
	}
}

func TestSimulateFoldChargesNoFeesWithoutFills(t *testing.T) {
	execution := foldExecution()
	execution.Policy.Fee = "1"
	metrics, trace, err := SimulateFold(context.Background(), foldBars("1", "1", "1", "1", "1", "1"), nil, Strategy{Quantity: "1", FastWindow: 2, SlowWindow: 3}, execution, false)
	want := Metrics{
		AfterCostReturn: "0", MaxDrawdown: "0", TradeCount: "0", TradeCountDefinition: "completed_long_only_round_trips",
		FillCount: "0", Turnover: "0", Fees: "0", Taxes: "0", SlippageCost: "0", PartialFillCount: "0", LookaheadViolations: "0",
	}
	if err != nil || metrics != want || trace == nil || len(trace) != 0 {
		t.Fatalf("no-fill fold = %+v, %+v, %v; want %+v and empty trace", metrics, trace, err, want)
	}
}

func TestSimulateFoldRatiosRoundHalfEvenAt28Places(t *testing.T) {
	for _, test := range []struct {
		name, startingCash, entry, exit, want string
	}{
		{"repeating ratio", "3", "1", "2", "0.3333333333333333333333333333"},
		{"positive even tie", "100000000000000000000000000000", "1", "6", "0"},
		{"positive odd tie", "100000000000000000000000000000", "1", "16", "0.0000000000000000000000000002"},
		{"negative even tie", "100000000000000000000000000000", "6", "1", "0"},
		{"negative odd tie", "100000000000000000000000000000", "16", "1", "-0.0000000000000000000000000002"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bars := foldBars("1", "1", "1", "1")
			bars[1].Open, bars[3].Open = test.entry, test.exit
			execution := foldExecution()
			execution.StartingCash = test.startingCash
			metrics, _, err := SimulateFold(context.Background(), bars, nil, Strategy{Quantity: "1", FastWindow: 1, SlowWindow: 2}, execution, true)
			if err != nil || metrics.AfterCostReturn != test.want {
				t.Fatalf("after-cost return = %q, %v; want %q", metrics.AfterCostReturn, err, test.want)
			}
		})
	}
}

func TestSimulateFoldMoneyDoesNotRoundToRatioPrecision(t *testing.T) {
	execution := foldExecution()
	execution.StartingCash = "12345678901234567890123456789.01"
	execution.Policy.Fee = "0.00000000000000000000000000001"
	metrics, _, err := SimulateFold(context.Background(), foldBars("1", "1", "1", "1"), nil, Strategy{Quantity: "1", FastWindow: 1, SlowWindow: 2}, execution, true)
	if err != nil || metrics.Fees != "0.00000000000000000000000000002" || metrics.AfterCostReturn != "0" {
		t.Fatalf("exact money = %+v, %v", metrics, err)
	}
}

func TestSimulateFoldRejectsUnsupportedInputs(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*[]Bar, *[]Bar, *Strategy, *Execution)
	}{
		{"empty evaluation", func(b, _ *[]Bar, _ *Strategy, _ *Execution) { *b = nil }},
		{"too many bars", func(b, _ *[]Bar, _ *Strategy, _ *Execution) { *b = make([]Bar, 501) }},
		{"too many warmup bars", func(_ *[]Bar, w *[]Bar, _ *Strategy, _ *Execution) { *w = make([]Bar, 495) }},
		{"zero fast", func(_, _ *[]Bar, s *Strategy, _ *Execution) { s.FastWindow = 0 }},
		{"equal windows", func(_, _ *[]Bar, s *Strategy, _ *Execution) { s.FastWindow = s.SlowWindow }},
		{"oversized slow", func(_, _ *[]Bar, s *Strategy, _ *Execution) { s.SlowWindow = 366 }},
		{"zero quantity", func(_, _ *[]Bar, s *Strategy, _ *Execution) { s.Quantity = "0" }},
		{"fractional quantity", func(_, _ *[]Bar, s *Strategy, _ *Execution) { s.Quantity = "0.5" }},
		{"oversized quantity", func(_, _ *[]Bar, s *Strategy, _ *Execution) { s.Quantity = "4611686018427387904" }},
		{"noncanonical quantity", func(_, _ *[]Bar, s *Strategy, _ *Execution) { s.Quantity = "01" }},
		{"zero cash", func(_, _ *[]Bar, _ *Strategy, e *Execution) { e.StartingCash = "0" }},
		{"negative cash", func(_, _ *[]Bar, _ *Strategy, e *Execution) { e.StartingCash = "-1" }},
		{"noncanonical cash", func(_, _ *[]Bar, _ *Strategy, e *Execution) { e.StartingCash = "1.0" }},
		{"zero delay", func(_, _ *[]Bar, _ *Strategy, e *Execution) { e.DelayBars = 0 }},
		{"negative delay", func(_, _ *[]Bar, _ *Strategy, e *Execution) { e.DelayBars = -1 }},
		{"negative fee", func(_, _ *[]Bar, _ *Strategy, e *Execution) { e.Policy.Fee = "-1" }},
		{"excess tax", func(_, _ *[]Bar, _ *Strategy, e *Execution) { e.Policy.Tax = "1.01" }},
		{"excess slippage", func(_, _ *[]Bar, _ *Strategy, e *Execution) { e.Policy.SlippageBPS = "10000" }},
		{"zero participation", func(_, _ *[]Bar, _ *Strategy, e *Execution) { e.Policy.MaxParticipation = "0" }},
		{"excess participation", func(_, _ *[]Bar, _ *Strategy, e *Execution) { e.Policy.MaxParticipation = "1.01" }},
		{"noncanonical open", func(b, _ *[]Bar, _ *Strategy, _ *Execution) { (*b)[0].Open = "1.0" }},
		{"zero open", func(b, _ *[]Bar, _ *Strategy, _ *Execution) { (*b)[0].Open = "0" }},
		{"negative volume", func(b, _ *[]Bar, _ *Strategy, _ *Execution) { (*b)[0].Volume = "-1" }},
		{"noncanonical volume", func(b, _ *[]Bar, _ *Strategy, _ *Execution) { (*b)[0].Volume = "01" }},
		{"zero close", func(b, _ *[]Bar, _ *Strategy, _ *Execution) { (*b)[0].Close = "0" }},
		{"excess close", func(b, _ *[]Bar, _ *Strategy, _ *Execution) { (*b)[0].Close = "1000000000000" }},
		{"excess close precision", func(b, _ *[]Bar, _ *Strategy, _ *Execution) { (*b)[0].Close = "1.000000001" }},
		{"noncanonical close", func(b, _ *[]Bar, _ *Strategy, _ *Execution) { (*b)[0].Close = "1.0" }},
		{"invalid warmup", func(_ *[]Bar, w *[]Bar, _ *Strategy, _ *Execution) { *w = foldBars("-1") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bars, warmup := foldBars("1", "1", "1", "1", "1", "1"), []Bar(nil)
			strategy := Strategy{Quantity: "1", FastWindow: 2, SlowWindow: 3}
			execution := foldExecution()
			test.change(&bars, &warmup, &strategy, &execution)
			for _, buyAndHold := range []bool{false, true} {
				if metrics, trace, err := SimulateFold(context.Background(), bars, warmup, strategy, execution, buyAndHold); err == nil || metrics != (Metrics{}) || trace != nil {
					t.Fatalf("invalid input with buyAndHold=%t = %+v, %+v, %v", buyAndHold, metrics, trace, err)
				}
			}
		})
	}
	strategy := Strategy{Quantity: "1", FastWindow: 2, SlowWindow: 3}
	for _, delay := range []int{1, int(^uint(0) >> 1)} {
		execution := foldExecution()
		execution.DelayBars = delay
		if _, _, err := SimulateFold(context.Background(), foldBars("1", "1", "1", "1", "1"), nil, strategy, execution, false); err == nil || !strings.Contains(err.Error(), "insufficient bars") {
			t.Fatalf("insufficient bars with delay %d: %v", delay, err)
		}
	}
}

func TestSimulateFoldAcceptsPaperSMABoundaries(t *testing.T) {
	bars := make([]Bar, 500)
	for index := range bars {
		bars[index] = Bar{Open: "0.00000001", Close: "999999999999.99999999", Volume: "0"}
	}
	execution := foldExecution()
	execution.StartingCash = "0.00000001"
	metrics, trace, err := SimulateFold(context.Background(), bars, nil, Strategy{Quantity: paperdomain.MaxQuantity, FastWindow: 364, SlowWindow: 365}, execution, false)
	if err != nil || metrics.TradeCount != "0" || len(trace) != 0 {
		t.Fatalf("supported boundary fold = %+v, %+v, %v", metrics, trace, err)
	}
}

type cancelOnCheckContext struct {
	context.Context
	cancel   context.CancelFunc
	checks   int
	cancelAt int
}

func (c *cancelOnCheckContext) Err() error {
	c.checks++
	if c.checks == c.cancelAt {
		c.cancel()
	}
	return c.Context.Err()
}

func TestSimulateFoldHonorsCancellationBeforeAndDuringEvaluation(t *testing.T) {
	bars := foldBars("1", "1", "1", "1", "1", "1")
	strategy := Strategy{Quantity: "1", FastWindow: 2, SlowWindow: 3}
	for _, cancelAt := range []int{1, 3} {
		base, cancel := context.WithCancel(context.Background())
		ctx := &cancelOnCheckContext{Context: base, cancel: cancel, cancelAt: cancelAt}
		metrics, trace, err := SimulateFold(ctx, bars, nil, strategy, foldExecution(), false)
		cancel()
		if !errors.Is(err, context.Canceled) || metrics != (Metrics{}) || trace != nil || ctx.checks != cancelAt {
			t.Fatalf("canceled at check %d = %+v, %+v, %v; observed checks=%d", cancelAt, metrics, trace, err, ctx.checks)
		}
	}
}
