package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"omni-folio/services/core/internal/paperdomain"
	"omni-folio/services/core/internal/researchdomain"
)

func TestPaperPromotionFoldMatchesActualPythonV2MetricsAndFills(t *testing.T) {
	partial := make([]researchdomain.Bar, 8)
	for index := range partial {
		partial[index] = researchdomain.Bar{
			At:   time.Date(2026, 1, index+1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			Open: "100", Close: "100", Volume: "5",
		}
	}
	partial[len(partial)-1].Volume = "10"
	partialExecution := researchdomain.Execution{
		StartingCash: "1000", DelayBars: 1,
		Policy: paperdomain.ExecutionPolicy{Fee: "1", Tax: "0.01", SlippageBPS: "10", MaxParticipation: "0.5"},
	}
	series := paperPromotionComparisonBarsForTest()
	candidate := researchdomain.Strategy{Quantity: "10", FastWindow: 2, SlowWindow: 5}
	incumbent := researchdomain.Strategy{Quantity: "10", FastWindow: 3, SlowWindow: 6}
	const commonWarmup, windowBars = 6, 30
	const holdoutStart = commonWarmup + 2*windowBars
	cases := []struct {
		Name       string                   `json:"name"`
		Bars       []researchdomain.Bar     `json:"bars"`
		Warmup     []researchdomain.Bar     `json:"warmup"`
		Strategy   researchdomain.Strategy  `json:"strategy"`
		Execution  researchdomain.Execution `json:"execution"`
		BuyAndHold bool                     `json:"buy_and_hold"`
	}{
		{"partial_fill_buy_and_hold", partial, nil, researchdomain.Strategy{Quantity: "4", FastWindow: 2, SlowWindow: 3}, partialExecution, true},
		{"first_window_candidate", series[commonWarmup : commonWarmup+windowBars], series[:commonWarmup], candidate, paperPromotionExecutionForTest(), false},
		{"holdout_candidate", series[holdoutStart : holdoutStart+windowBars], series[holdoutStart-commonWarmup : holdoutStart], candidate, paperPromotionExecutionForTest(), false},
		{"holdout_incumbent", series[holdoutStart : holdoutStart+windowBars], series[holdoutStart-commonWarmup : holdoutStart], incumbent, paperPromotionExecutionForTest(), false},
		{"holdout_buy_and_hold", series[holdoutStart : holdoutStart+windowBars], series[holdoutStart-commonWarmup : holdoutStart], candidate, paperPromotionExecutionForTest(), true},
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", "-c", `import copy,json,sys
from decimal import Decimal,localcontext
from pathlib import Path
from omni_research.engine import Bar
from omni_research.improve import Parameters,parse_config,simulate_fold

template=json.loads(Path(sys.argv[1]).read_text())
assert template["schema_version"]=="strategy-improvement.v2"
def bars(rows):
    return [Bar(row["At"],"005930",Decimal(row["Open"]),Decimal(row["Open"]),Decimal(row["Open"]),Decimal(row["Close"]),Decimal(row["Volume"])) for row in rows or []]

results=[]
with localcontext() as context:
    context.prec=6
    for case in json.load(sys.stdin):
        raw=copy.deepcopy(template)
        strategy,execution=case["strategy"],case["execution"]
        policy=execution["Policy"]
        fast,slow=strategy["FastWindow"],strategy["SlowWindow"]
        raw["strategy"].update(quantity=strategy["Quantity"],fast_windows=[fast],slow_windows=[slow])
        raw["execution"].update(starting_cash=execution["StartingCash"],fee=policy["Fee"],tax=policy["Tax"],slippage_bps=policy["SlippageBPS"],delay_bars=str(execution["DelayBars"]),max_participation=policy["MaxParticipation"])
        parameters=None if case["buy_and_hold"] else Parameters(fast,slow)
        metrics,trace=simulate_fold(bars(case["bars"]),parse_config(raw),parameters,warmup=bars(case["warmup"]))
        results.append({"name":case["name"],"metrics":metrics,"trace":trace})
print(json.dumps(results))
`, filepath.Join(root, "contracts", "fixtures", "strategy-improvement-paper-config.json"))
	command.Stdin = bytes.NewReader(input)
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONPATH="+filepath.Join(root, "services", "research"))
	raw, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("actual Python v2 fold comparison: %v\n%s", err, raw)
	}
	var expected []struct {
		Name    string                     `json:"name"`
		Metrics researchdomain.Metrics     `json:"metrics"`
		Trace   []researchdomain.FillTrace `json:"trace"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&expected); err != nil {
		t.Fatalf("Python v2 fold output: %v\n%s", err, raw)
	}
	if len(expected) != len(cases) {
		t.Fatalf("Python returned %d cases; want %d", len(expected), len(cases))
	}
	for index, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			want := expected[index]
			if want.Name != test.Name {
				t.Fatalf("Python case = %q; want %q", want.Name, test.Name)
			}
			metrics, trace, err := researchdomain.SimulateFold(context.Background(), test.Bars, test.Warmup, test.Strategy, test.Execution, test.BuyAndHold)
			if err != nil {
				t.Fatal(err)
			}
			if metrics != want.Metrics {
				t.Fatalf("Go metrics = %+v; Python v2 = %+v", metrics, want.Metrics)
			}
			if !reflect.DeepEqual(trace, want.Trace) {
				t.Fatalf("Go fill trace = %+v; Python v2 = %+v", trace, want.Trace)
			}
		})
	}
}
