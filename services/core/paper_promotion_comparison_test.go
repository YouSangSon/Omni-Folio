package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"omni-folio/services/core/internal/paperdomain"
	"omni-folio/services/core/internal/researchdomain"
)

func paperPromotionCSVForTest(t *testing.T, count int) []byte {
	t.Helper()
	var output strings.Builder
	writer := csv.NewWriter(&output)
	if err := writer.Write([]string{"bar_at", "symbol", "open", "high", "low", "close", "volume"}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for index := range count {
		if err := writer.Write([]string{start.Add(time.Duration(index) * time.Second).Format(time.RFC3339), "005930", "100", "101", "99", "100", "5"}); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	return []byte(output.String())
}

func TestPaperPromotionBarsPreserveCanonicalValuesAndAcceptExistingVolumeDomain(t *testing.T) {
	raw := paperPromotionCSVForTest(t, 2)
	symbol, bars, err := parsePaperPromotionBars(context.Background(), raw)
	want := []researchdomain.Bar{
		{At: "2026-01-01T00:00:00Z", Open: "100", Close: "100", Volume: "5"},
		{At: "2026-01-01T00:00:01Z", Open: "100", Close: "100", Volume: "5"},
	}
	if err != nil || symbol != "005930" || !reflect.DeepEqual(bars, want) {
		t.Fatalf("parsed bars = %q, %+v, %v; want %+v", symbol, bars, err, want)
	}
	largeVolume := "1000000000000000000000000000000.000000001"
	raw = []byte(strings.ReplaceAll(string(raw), ",100,101,99,100,5", ",100,1000000000000,99,100,"+largeVolume))
	_, bars, err = parsePaperPromotionBars(context.Background(), raw)
	if err != nil || len(bars) != 2 || bars[0].Volume != largeVolume {
		t.Fatalf("existing volume domain was narrowed: %+v, %v", bars, err)
	}
	for _, count := range []int{500, 501} {
		_, bars, err = parsePaperPromotionBars(context.Background(), paperPromotionCSVForTest(t, count))
		if count == 500 && (err != nil || len(bars) != count) || count == 501 && err == nil {
			t.Fatalf("%d rows: parsed=%d err=%v", count, len(bars), err)
		}
	}
}

func TestPaperPromotionBarsRejectMalformedOrUnboundData(t *testing.T) {
	header := "bar_at,symbol,open,high,low,close,volume\n"
	first := "2026-01-01T00:00:00Z,005930,100,101,99,100,5\n"
	second := "2026-01-01T00:00:01Z,005930,100,101,99,100,5\n"
	valid := header + first + second
	for name, raw := range map[string][]byte{
		"empty":                 nil,
		"header only":           []byte(header),
		"oversized":             []byte(strings.Repeat("x", maxMarketDataBytes+1)),
		"invalid UTF-8":         append([]byte(valid), 0xff),
		"wrong column order":    []byte(strings.Replace(valid, "open,high,low", "high,open,low", 1)),
		"extra column":          []byte(strings.Replace(valid, "volume\n", "volume,source\n", 1)),
		"extra field":           []byte(header + strings.TrimSpace(first) + ",extra\n"),
		"missing field":         []byte(header + strings.Replace(first, ",100,5\n", ",100\n", 1)),
		"invalid CSV":           []byte(header + strings.Replace(first, ",100,101", ",\"100,101", 1)),
		"non-stock symbol":      []byte(strings.ReplaceAll(valid, "005930", "AAPL")),
		"different symbols":     []byte(header + first + strings.Replace(second, "005930", "000660", 1)),
		"duplicate timestamp":   []byte(header + first + first),
		"reversed timestamps":   []byte(header + second + first),
		"duplicate zero time":   []byte(header + strings.ReplaceAll(first+first, "2026-01-01T00:00:00Z", "0001-01-01T00:00:00Z")),
		"UTC offset spelling":   []byte(strings.Replace(valid, "Z,", "+00:00,", 1)),
		"fractional timestamp":  []byte(strings.Replace(valid, "00:00:00Z", "00:00:00.000Z", 1)),
		"nonzero fraction":      []byte(strings.Replace(valid, "00:00:00Z", "00:00:00.001Z", 1)),
		"noncanonical close":    []byte(strings.Replace(valid, ",99,100,5", ",99,100.0,5", 1)),
		"close precision":       []byte(strings.Replace(valid, ",99,100,5", ",99,100.000000001,5", 1)),
		"close upper bound":     []byte(header + "2026-01-01T00:00:00Z,005930,1000000000000,1000000000000,1000000000000,1000000000000,5\n"),
		"noncanonical volume":   []byte(strings.Replace(valid, ",100,5\n", ",100,5.0\n", 1)),
		"negative volume":       []byte(strings.Replace(valid, ",100,5\n", ",100,-1\n", 1)),
		"invalid OHLC":          []byte(strings.Replace(valid, ",100,101,99,100,5", ",100,99,98,100,5", 1)),
		"nonpositive open":      []byte(strings.Replace(valid, ",100,101,99,100,5", ",0,101,0,100,5", 1)),
		"negative-zero decimal": []byte(strings.Replace(valid, ",100,5\n", ",100,-0\n", 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if symbol, bars, err := parsePaperPromotionBars(context.Background(), raw); err == nil || symbol != "" || bars != nil {
				t.Fatalf("invalid bars = %q, %+v, %v", symbol, bars, err)
			}
		})
	}
}

func paperPromotionEvidenceForTest(t *testing.T, mutate func(map[string]any)) StrategyEvidence {
	t.Helper()
	strategy := map[string]any{
		"name": "long_only_sma_crossover", "version": "1.0.0",
		"parameters": map[string]any{"fast_window": json.Number("2"), "slow_window": json.Number("5"), "quantity": "10"},
	}
	if mutate != nil {
		mutate(strategy)
	}
	parameters, _ := strategy["parameters"].(map[string]any)
	canonical, err := strategyCanonicalJSON(parameters)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(canonical)
	parameterSHA := hex.EncodeToString(hash[:])
	strategy["parameter_hash"] = parameterSHA
	artifact, err := strategyCanonicalJSON(map[string]any{
		"manifest":   map[string]any{"strategy": strategy},
		"challenger": map[string]any{"final_holdout_metrics": map[string]any{"after_cost_return": "untrusted reported metric"}},
		"promotion":  map[string]any{"target": "no_promotion", "failed_gates": []string{"untrusted old threshold"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return StrategyEvidence{
		StrategyName: stringField(strategy, "name"), StrategyVersion: stringField(strategy, "version"),
		ParameterSHA256: parameterSHA, artifactJSON: string(artifact), Target: "no_promotion",
	}
}

func TestPaperPromotionStrategyReadsBoundParametersWithoutTrustingReportedMetrics(t *testing.T) {
	evidence := paperPromotionEvidenceForTest(t, nil)
	want := researchdomain.Strategy{Quantity: "10", FastWindow: 2, SlowWindow: 5}
	if got, err := paperPromotionStrategy(evidence); err != nil || got != want {
		t.Fatalf("strategy = %+v, %v; want %+v", got, err, want)
	}
	maximum := paperPromotionEvidenceForTest(t, func(strategy map[string]any) {
		strategy["parameters"] = map[string]any{"fast_window": json.Number("364"), "slow_window": json.Number("365"), "quantity": paperdomain.MaxQuantity}
	})
	if got, err := paperPromotionStrategy(maximum); err != nil || got.Quantity != paperdomain.MaxQuantity || got.FastWindow != 364 || got.SlowWindow != 365 {
		t.Fatalf("supported boundary strategy = %+v, %v", got, err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"strategy name": func(strategy map[string]any) { strategy["name"] = "different" },
		"version":       func(strategy map[string]any) { strategy["version"] = "2.0.0" },
		"missing quantity": func(strategy map[string]any) {
			delete(strategy["parameters"].(map[string]any), "quantity")
		},
		"extra parameter": func(strategy map[string]any) { strategy["parameters"].(map[string]any)["unused"] = true },
		"string fast":     func(strategy map[string]any) { strategy["parameters"].(map[string]any)["fast_window"] = "2" },
		"decimal fast": func(strategy map[string]any) {
			strategy["parameters"].(map[string]any)["fast_window"] = json.Number("2.0")
		},
		"zero fast": func(strategy map[string]any) {
			strategy["parameters"].(map[string]any)["fast_window"] = json.Number("0")
		},
		"equal windows": func(strategy map[string]any) {
			strategy["parameters"].(map[string]any)["fast_window"] = json.Number("5")
		},
		"oversized slow": func(strategy map[string]any) {
			strategy["parameters"].(map[string]any)["slow_window"] = json.Number("366")
		},
		"zero quantity": func(strategy map[string]any) { strategy["parameters"].(map[string]any)["quantity"] = "0" },
		"fractional quantity": func(strategy map[string]any) {
			strategy["parameters"].(map[string]any)["quantity"] = "0.5"
		},
		"oversized quantity": func(strategy map[string]any) {
			strategy["parameters"].(map[string]any)["quantity"] = "4611686018427387904"
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := paperPromotionStrategy(paperPromotionEvidenceForTest(t, mutate)); err == nil || got != (researchdomain.Strategy{}) {
				t.Fatalf("unsupported strategy = %+v, %v", got, err)
			}
		})
	}
	for name, change := range map[string]func(*StrategyEvidence){
		"metadata mismatch": func(e *StrategyEvidence) { e.StrategyVersion = "2.0.0" },
		"parameter hash":    func(e *StrategyEvidence) { e.ParameterSHA256 = strings.Repeat("0", 64) },
		"parameter tampering": func(e *StrategyEvidence) {
			e.artifactJSON = strings.Replace(e.artifactJSON, `"quantity":"10"`, `"quantity":"11"`, 1)
		},
		"trailing JSON":        func(e *StrategyEvidence) { e.artifactJSON += " {}" },
		"malformed JSON":       func(e *StrategyEvidence) { e.artifactJSON = "{" },
		"oversized artifact":   func(e *StrategyEvidence) { e.artifactJSON = strings.Repeat("x", maxBodyBytes+1) },
		"invalid artifact UTF": func(e *StrategyEvidence) { e.artifactJSON += string([]byte{0xff}) },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := evidence
			change(&invalid)
			if got, err := paperPromotionStrategy(invalid); err == nil || got != (researchdomain.Strategy{}) {
				t.Fatalf("unbound strategy = %+v, %v", got, err)
			}
		})
	}
}

func paperPromotionPolicyWindowsForTest() []paperPromotionWindow {
	windows := make([]paperPromotionWindow, 3)
	for index := range windows {
		windows[index] = paperPromotionWindow{
			Candidate:  researchdomain.Metrics{AfterCostReturn: "0.01", MaxDrawdown: "0.02", Turnover: "0.1", TradeCount: "1"},
			Incumbent:  researchdomain.Metrics{AfterCostReturn: "0.01", MaxDrawdown: "0.02", Turnover: "0.1", TradeCount: "1"},
			BuyAndHold: researchdomain.Metrics{AfterCostReturn: "0"},
		}
	}
	windows[2].Candidate.AfterCostReturn = "0.02"
	return windows
}

func TestPaperPromotionPolicyBoundariesAreExact(t *testing.T) {
	for _, test := range []struct {
		name, decision, reason string
		change                 func([]paperPromotionWindow)
	}{
		{"equal non-holdout gates pass", "PROMOTE", "all_forward_gates_passed", func([]paperPromotionWindow) {}},
		{"candidate must trade each window", "HOLD", "insufficient_round_trips", func(w []paperPromotionWindow) { w[1].Candidate.TradeCount = "0" }},
		{"incumbent must trade holdout", "HOLD", "insufficient_round_trips", func(w []paperPromotionWindow) { w[2].Incumbent.TradeCount = "0" }},
		{"negative return fails", "HOLD", "candidate_negative_return", func(w []paperPromotionWindow) {
			w[0].Candidate.AfterCostReturn, w[0].Incumbent.AfterCostReturn = "-0.0000000000000000000000000001", "-0.0000000000000000000000000001"
		}},
		{"return below incumbent fails", "HOLD", "candidate_return_below_incumbent", func(w []paperPromotionWindow) { w[0].Candidate.AfterCostReturn = "0.0099999999999999999999999999" }},
		{"higher drawdown fails", "HOLD", "candidate_drawdown_above_incumbent", func(w []paperPromotionWindow) { w[1].Candidate.MaxDrawdown = "0.0200000000000000000000000001" }},
		{"higher turnover fails", "HOLD", "candidate_turnover_above_incumbent", func(w []paperPromotionWindow) { w[1].Candidate.Turnover = "0.1000000000000000000000000001" }},
		{"all ties hold", "HOLD", "holdout_not_better_than_incumbent", func(w []paperPromotionWindow) { w[2].Candidate.AfterCostReturn = "0.01" }},
		{"holdout must beat baseline", "HOLD", "holdout_not_better_than_buy_and_hold", func(w []paperPromotionWindow) { w[2].BuyAndHold.AfterCostReturn = "0.02" }},
		{"zero candidate return may beat both", "PROMOTE", "all_forward_gates_passed", func(w []paperPromotionWindow) {
			for index := range w {
				w[index].Candidate.AfterCostReturn, w[index].Incumbent.AfterCostReturn = "0", "0"
			}
			w[2].Incumbent.AfterCostReturn = "-0.0000000000000000000000000001"
			w[2].BuyAndHold.AfterCostReturn = "-0.0000000000000000000000000002"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			windows := paperPromotionPolicyWindowsForTest()
			test.change(windows)
			decision, reason, err := evaluatePaperPromotionWindows(windows)
			if err != nil || decision != test.decision || reason != test.reason {
				t.Fatalf("policy = %q, %q, %v; want %q, %q", decision, reason, err, test.decision, test.reason)
			}
		})
	}
}

func paperPromotionComparisonBarsForTest() []researchdomain.Bar {
	closes := []int{20, 20, 20, 20, 20, 20}
	for range 3 {
		for price := 20; price >= 9; price-- {
			closes = append(closes, price)
		}
		for price := 10; price <= 27; price++ {
			closes = append(closes, price)
		}
	}
	bars := make([]researchdomain.Bar, len(closes))
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for index, close := range closes {
		price := strconv.Itoa(close)
		bars[index] = researchdomain.Bar{At: start.Add(time.Duration(index) * 24 * time.Hour).Format(time.RFC3339), Open: price, Close: price, Volume: "100"}
	}
	return bars
}

func paperPromotionExecutionForTest() researchdomain.Execution {
	return researchdomain.Execution{
		StartingCash: "10000", DelayBars: 1,
		Policy: paperdomain.ExecutionPolicy{Fee: "1", Tax: "0.001", SlippageBPS: "10", MaxParticipation: "0.5"},
	}
}

func TestPaperPromotionComparisonScoresThreeFixedFreshWindows(t *testing.T) {
	bars := paperPromotionComparisonBarsForTest()
	candidate := researchdomain.Strategy{Quantity: "10", FastWindow: 2, SlowWindow: 5}
	incumbent := researchdomain.Strategy{Quantity: "10", FastWindow: 3, SlowWindow: 6}
	comparison, err := comparePaperPromotion(context.Background(), bars, candidate, incumbent, paperPromotionExecutionForTest())
	if err != nil || comparison.PolicyVersion != paperPromotionPolicyVersion || comparison.Decision != "PROMOTE" || comparison.Reason != "all_forward_gates_passed" || len(comparison.Windows) != 3 {
		t.Fatalf("comparison = %+v, %v", comparison, err)
	}
	for index, window := range comparison.Windows {
		if window.Start != bars[6+index*30].At || window.End != bars[6+index*30+29].At ||
			window.Candidate.AfterCostReturn != "0.014734027" || window.Incumbent.AfterCostReturn != "0.013733027" || window.BuyAndHold.AfterCostReturn != "0.007727027" ||
			window.Candidate.MaxDrawdown != "0.000112" || window.Incumbent.MaxDrawdown != "0.000113" ||
			window.Candidate.Turnover != "0.038985" || window.Incumbent.Turnover != "0.039986" ||
			window.Candidate.TradeCount != "1" || window.Incumbent.TradeCount != "1" {
			t.Fatalf("window %d = %+v", index, window)
		}
	}
	comparison, err = comparePaperPromotion(context.Background(), bars, incumbent, incumbent, paperPromotionExecutionForTest())
	if err != nil || comparison.Decision != "HOLD" || comparison.Reason != "holdout_not_better_than_incumbent" {
		t.Fatalf("identical strategies = %+v, %v", comparison, err)
	}
	for index := range bars {
		bars[index].Open, bars[index].Close = "20", "20"
	}
	comparison, err = comparePaperPromotion(context.Background(), bars, candidate, incumbent, paperPromotionExecutionForTest())
	if err != nil || comparison.Decision != "HOLD" || comparison.Reason != "insufficient_round_trips" || len(comparison.Windows) != 3 {
		t.Fatalf("inactive strategies = %+v, %v", comparison, err)
	}
}

func TestPaperPromotionComparisonRejectsIncompleteOrDifferentInputs(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*[]researchdomain.Bar, *researchdomain.Strategy, *researchdomain.Execution)
	}{
		{"missing bar", func(b *[]researchdomain.Bar, _ *researchdomain.Strategy, _ *researchdomain.Execution) {
			*b = (*b)[:len(*b)-1]
		}},
		{"extra bar", func(b *[]researchdomain.Bar, _ *researchdomain.Strategy, _ *researchdomain.Execution) {
			*b = append(*b, (*b)[len(*b)-1])
		}},
		{"different quantity", func(_ *[]researchdomain.Bar, s *researchdomain.Strategy, _ *researchdomain.Execution) {
			s.Quantity = "11"
		}},
		{"invalid strategy", func(_ *[]researchdomain.Bar, s *researchdomain.Strategy, _ *researchdomain.Execution) {
			s.SlowWindow = 366
		}},
		{"unfinished liquidation", func(b *[]researchdomain.Bar, _ *researchdomain.Strategy, _ *researchdomain.Execution) {
			(*b)[35].Volume = "0"
		}},
		{"insufficient cash", func(_ *[]researchdomain.Bar, _ *researchdomain.Strategy, e *researchdomain.Execution) {
			e.StartingCash = "1"
		}},
		{"invalid execution", func(_ *[]researchdomain.Bar, _ *researchdomain.Strategy, e *researchdomain.Execution) {
			e.DelayBars = 0
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bars := paperPromotionComparisonBarsForTest()
			candidate := researchdomain.Strategy{Quantity: "10", FastWindow: 2, SlowWindow: 5}
			incumbent := researchdomain.Strategy{Quantity: "10", FastWindow: 3, SlowWindow: 6}
			execution := paperPromotionExecutionForTest()
			test.change(&bars, &candidate, &execution)
			if comparison, err := comparePaperPromotion(context.Background(), bars, candidate, incumbent, execution); err == nil || comparison.PolicyVersion != "" || comparison.Windows != nil {
				t.Fatalf("invalid comparison = %+v, %v", comparison, err)
			}
		})
	}
}

func TestPaperPromotionParsingAndComparisonHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := parsePaperPromotionBars(ctx, paperPromotionCSVForTest(t, 2)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled parser error = %v", err)
	}
	strategy := researchdomain.Strategy{Quantity: "10", FastWindow: 3, SlowWindow: 6}
	if _, err := comparePaperPromotion(ctx, paperPromotionComparisonBarsForTest(), strategy, strategy, paperPromotionExecutionForTest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled comparison error = %v", err)
	}
}
