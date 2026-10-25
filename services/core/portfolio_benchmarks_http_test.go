package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func benchmarkPortfolioForTest(t *testing.T) *Service {
	t.Helper()
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	applyTestCSV(t, svc, "benchmark-portfolio", testCSV(
		"first,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100",
		"gain-one,account-main,DIVIDEND,2025-06-30T00:00:00Z,AAPL,,,,KRW,10",
		"second,account-main,DEPOSIT,2025-07-01T00:00:00Z,,,,,KRW,100",
		"gain-two,account-main,DIVIDEND,2026-01-01T00:00:00Z,AAPL,,,,KRW,21",
	))
	return svc
}

func benchmarkHTTPReport(t *testing.T, svc *Service) *PortfolioBenchmarks {
	t.Helper()
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/benchmarks", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("benchmark HTTP %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("financial response may be cached")
	}
	for _, private := range []string{"account-main", "source_event_id", "observation_id", "receipt_id", "instrument_id"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("private field leaked: %s", private)
		}
	}
	var report PortfolioBenchmarks
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	return &report
}

func assertBenchmarkGoldenState(t *testing.T, name string, report *PortfolioBenchmarks) {
	t.Helper()
	var states struct {
		Reports map[string]json.RawMessage `json:"reports"`
	}
	if err := json.Unmarshal(fixture(t, "golden-portfolio-benchmark-states.json"), &states); err != nil {
		t.Fatal(err)
	}
	var expected, actual map[string]any
	if err := json.Unmarshal(states.Reports[name], &expected); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("%s shared state differs: %s", name, raw)
	}
}

func TestPortfolioBenchmarksComparesKRWAndUSDWithOneTWRObservation(t *testing.T) {
	svc := benchmarkPortfolioForTest(t)
	for _, currency := range []string{"krw", "usd"} {
		importBenchmarkForTest(t, svc, fixture(t, "benchmark-"+currency+"-sample.json"))
	}
	recordXIRRFX(t, svc, "benchmark-start", "1000", "2025-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	recordXIRRFX(t, svc, "benchmark-end", "900", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	calls := 0
	svc.now = func() time.Time { calls++; return mustTime("2026-01-01T00:00:00Z") }
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/benchmarks", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("same-period benchmark comparison unavailable: HTTP %d %s", w.Code, w.Body.String())
	}
	var report struct {
		Portfolio  PortfolioTWR `json:"portfolio"`
		Benchmarks []struct {
			Series struct {
				Currency string `json:"currency"`
			} `json:"series"`
			Native struct {
				Rate *string `json:"rate"`
			} `json:"native"`
			KRW struct {
				Rate *string `json:"rate"`
			} `json:"krw"`
			Difference *string `json:"difference_percentage_points"`
		} `json:"benchmarks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || report.Portfolio.CumulativeRate == nil || *report.Portfolio.CumulativeRate != "0.21" || len(report.Benchmarks) != 2 {
		t.Fatalf("different TWR observation or missing currencies: %s clocks=%d", w.Body.String(), calls)
	}
	want := map[string][3]string{"KRW": {"0.1", "0.1", "11"}, "USD": {"-0.1", "-0.19", "40"}}
	for _, row := range report.Benchmarks {
		expected, ok := want[row.Series.Currency]
		if !ok || row.Native.Rate == nil || row.KRW.Rate == nil || row.Difference == nil || [3]string{*row.Native.Rate, *row.KRW.Rate, *row.Difference} != expected {
			t.Fatalf("native/KRW/difference units mismatch: %s", w.Body.String())
		}
		delete(want, row.Series.Currency)
	}
	if len(want) != 0 {
		t.Fatal("missing benchmark currency")
	}
	var actual, golden map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixture(t, "golden-portfolio-benchmarks.json"), &golden); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, golden) {
		t.Fatalf("shared benchmark response differs: %s", w.Body.String())
	}
	var twr map[string]any
	if err := json.Unmarshal(fixture(t, "golden-portfolio-twr.json"), &twr); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual["portfolio"], twr) {
		t.Fatal("comparison changed the preserved TWR contract")
	}
}

func TestPortfolioBenchmarksSelectsLatestKnownWholeVersion(t *testing.T) {
	svc := benchmarkPortfolioForTest(t)
	importBenchmarkForTest(t, svc, benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.SourceVersion = "z-old" }))
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00.1Z") }
	importBenchmarkForTest(t, svc, benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.SourceVersion = "a-new"; i.Points[1].Level = "120" }))
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	r := benchmarkHTTPReport(t, svc)
	if len(r.Benchmarks) != 1 || r.Benchmarks[0].Series.SourceVersion != "z-old" || *r.Benchmarks[0].Native.Rate != "0.1" {
		t.Fatalf("future version selected: %+v", r.Benchmarks)
	}
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00.1Z") }
	r = benchmarkHTTPReport(t, svc)
	if r.Benchmarks[0].Series.SourceVersion != "a-new" || *r.Benchmarks[0].Native.Rate != "0.2" {
		t.Fatal("source version sorted lexically instead of append order")
	}
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00.2Z") }
	importBenchmarkForTest(t, svc, benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.SourceVersion = "end-only"; i.Points = i.Points[1:] }))
	r = benchmarkHTTPReport(t, svc)
	row := r.Benchmarks[0]
	if row.Series.SourceVersion != "end-only" || row.Start != nil || row.Native.Rate != nil || row.KRW.Rate != nil || row.Native.Issue == nil || *row.Native.Issue != "missing_benchmark_point" {
		t.Fatalf("versions mixed at start: %+v", row)
	}
}

func TestPortfolioBenchmarksPointAgeAndNanosecondBoundaries(t *testing.T) {
	for _, age := range []time.Duration{24 * time.Hour, 24*time.Hour + time.Nanosecond} {
		svc := benchmarkPortfolioForTest(t)
		importBenchmarkForTest(t, svc, benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) {
			i.Points[0].ObservedAt = mustTime("2025-01-01T00:00:00Z").Add(-age).Format(time.RFC3339Nano)
		}))
		row := benchmarkHTTPReport(t, svc).Benchmarks[0]
		if age == 24*time.Hour {
			if row.Native.Rate == nil || *row.Native.Rate != "0.1" {
				t.Fatal("exact 24h point rejected")
			}
		} else if row.Native.Rate != nil || row.Native.Issue == nil || *row.Native.Issue != "stale_benchmark_point" {
			t.Fatal("point older than24h admitted")
		}
	}
	svc := benchmarkPortfolioForTest(t)
	importBenchmarkForTest(t, svc, benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) {
		i.Points = append(i.Points[:1], append([]BenchmarkIndexPoint{{ObservedAt: "2025-01-01T00:00:00.000000001Z", Level: "999"}}, i.Points[1:]...)...)
	}))
	row := benchmarkHTTPReport(t, svc).Benchmarks[0]
	if row.Start.ObservedAt != "2025-01-01T00:00:00Z" || *row.Native.Rate != "0.1" {
		t.Fatal("future economic point used at exact start")
	}
}

func TestPortfolioBenchmarksFXFailureKeepsNativeAndSelectedSample(t *testing.T) {
	svc := benchmarkPortfolioForTest(t)
	importBenchmarkForTest(t, svc, benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.Currency = "USD"; i.Sample = false }))
	row := benchmarkHTTPReport(t, svc).Benchmarks[0]
	if *row.Native.Rate != "0.1" || row.KRW.Rate != nil || *row.KRW.Issue != "missing_historical_fx" || row.Sample {
		t.Fatal("missing FX fabricated KRW or changed native/sample")
	}
	recordXIRRFX(t, svc, "end", "900", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	row = benchmarkHTTPReport(t, svc).Benchmarks[0]
	if *row.Native.Rate != "0.1" || row.KRW.Rate != nil || row.StartFX != nil || row.EndFX == nil || !row.Sample {
		t.Fatal("partial selected FX sample was lost")
	}
	recordXIRRFX(t, svc, "start-stale", "1000", "2024-12-30T23:59:59.999999999Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	row = benchmarkHTTPReport(t, svc).Benchmarks[0]
	if *row.Native.Rate != "0.1" || row.KRW.Rate != nil || *row.KRW.Issue != "stale_historical_fx" || !row.Sample {
		t.Fatal("stale FX hid native or sample provenance")
	}
	recordXIRRFX(t, svc, "start", "1000", "2025-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if _, err := svc.recordFXObservation(context.Background(), FXObservationInput{Source: "local_fixture", SourceObservationID: "start-tie", BaseCurrency: "USD", QuoteCurrency: "KRW", Rate: "2000", ObservedAt: "2025-01-01T00:00:00Z", FetchedAt: "2026-01-01T00:00:00Z"}); err == nil {
		t.Fatal("duplicate FX economic timestamp accepted")
	}
	r := benchmarkHTTPReport(t, svc)
	row = r.Benchmarks[0]
	if *row.KRW.Rate != "-0.01" || *row.DifferencePercentagePoints != "22" || row.StartFX.Rate != "1000" || !row.Sample || r.Portfolio.Sample {
		t.Fatal("FX priority/conversion/sample scope mismatch")
	}
}

func TestPortfolioBenchmarksLimitsWithoutTruncatingIntegrityProof(t *testing.T) {
	svc := benchmarkPortfolioForTest(t)
	for n := 0; n < 33; n++ {
		importBenchmarkForTest(t, svc, benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.BenchmarkID = fmt.Sprintf("index-%02d", n) }))
		if n == 31 {
			r := benchmarkHTTPReport(t, svc)
			if len(r.Benchmarks) != 32 || len(r.Issues) != 0 {
				t.Fatal("32 indices unsupported")
			}
		}
	}
	r := benchmarkHTTPReport(t, svc)
	if len(r.Benchmarks) != 0 || len(r.Issues) != 1 || r.Issues[0].Code != "benchmark_limit" || *r.Portfolio.CumulativeRate != "0.21" {
		t.Fatal("33rd index silently truncated or erased TWR")
	}
	assertBenchmarkGoldenState(t, "benchmark_limit", r)
	var trigger string
	if err := svc.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='benchmark_series_versions_no_update'`).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER benchmark_series_versions_no_update; UPDATE benchmark_series_versions SET record_sha256=? WHERE sequence=33`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/benchmarks", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatal("33rd record corruption bypassed full proof")
	}
}

func TestPortfolioBenchmarksAbsenceQueryCancellationAndUnavailableIntegrity(t *testing.T) {
	svc := benchmarkPortfolioForTest(t)
	r := benchmarkHTTPReport(t, svc)
	if len(r.Benchmarks) != 0 || len(r.Issues) != 1 || r.Issues[0].Code != "no_benchmarks" {
		t.Fatal("absent indices fabricated a zero rate")
	}
	assertBenchmarkGoldenState(t, "no_benchmarks", r)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00.1Z") }
	importBenchmarkForTest(t, svc, []byte(benchmarkSeriesTestInput))
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	r = benchmarkHTTPReport(t, svc)
	if len(r.Benchmarks) != 0 || r.Issues[0].Code != "no_known_benchmarks" {
		t.Fatal("future-only dataset counted as no stored data")
	}
	assertBenchmarkGoldenState(t, "no_known_benchmarks", r)
	for _, query := range []string{"?as_of=2026-01-01T00:00:00Z", "?benchmark_id=test_reference", "?"} {
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/benchmarks"+query, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatal("caller changed the observation scope")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, appErr := svc.portfolioBenchmarks(ctx); appErr == nil {
		t.Fatal("cancellation returned a financial report")
	}
	for _, state := range []string{"empty", "unavailable"} {
		svc, _ := testService(t, nil, nil)
		if state == "unavailable" {
			applyTestCSV(t, svc, "no-external", testCSV("d,account-main,DIVIDEND,2025-01-01T00:00:00Z,AAPL,,,,KRW,10"))
		}
		if _, err := svc.db.Exec(`DROP INDEX benchmark_series_latest_idx`); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/benchmarks", nil))
		if w.Code != http.StatusInternalServerError {
			t.Fatal("unavailable TWR bypassed benchmark schema proof")
		}
	}
}

func TestPortfolioBenchmarksSharedUnavailableAndZeroStates(t *testing.T) {
	for _, name := range []string{"missing_fx", "portfolio_empty", "portfolio_unavailable", "zero_start", "zero_end", "zero_return", "stale_fx", "native_limit"} {
		t.Run(name, func(t *testing.T) {
			var svc *Service
			if name == "portfolio_empty" || name == "portfolio_unavailable" {
				svc, _ = testService(t, nil, nil)
				svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
				if name == "portfolio_unavailable" {
					applyTestCSV(t, svc, "full-withdrawal", testCSV("a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100", "b,account-main,WITHDRAWAL,2025-07-01T00:00:00Z,,,,,KRW,-100"))
				}
			} else {
				svc = benchmarkPortfolioForTest(t)
			}
			currencies := []string{"krw", "usd"}
			if strings.HasPrefix(name, "zero_") {
				currencies = currencies[:1]
			}
			if name == "stale_fx" || name == "native_limit" {
				currencies = currencies[1:]
			}
			for _, currency := range currencies {
				var input BenchmarkSeriesInput
				if err := json.Unmarshal(fixture(t, "benchmark-"+currency+"-sample.json"), &input); err != nil {
					t.Fatal(err)
				}
				if name == "zero_start" {
					input.Points[0].Level = "0"
				}
				if name == "zero_end" {
					input.Points[1].Level = "0"
				}
				if name == "zero_return" {
					input.Points[1].Level = "100"
				}
				if name == "native_limit" {
					input.Sample = false
					input.Points[0].Level = "0.0000000000000000000000000000000000000000000000000000000000003"
					input.Points[1].Level = "1000000000000000000000000000000000000000000000000000000000001"
				}
				raw, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				importBenchmarkForTest(t, svc, raw)
			}
			if name == "stale_fx" {
				recordXIRRFX(t, svc, "stale-start", "1000", "2024-12-30T23:59:59.999999999Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
				recordXIRRFX(t, svc, "fresh-end", "900", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
			}
			if name == "native_limit" {
				recordXIRRFX(t, svc, "limit-start", "1000000000000000000000000000000000000000000000000000000000001", "2025-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
				recordXIRRFX(t, svc, "limit-end", "0.0000000000000000000000000000000000000000000000000000000000003", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
			}
			assertBenchmarkGoldenState(t, name, benchmarkHTTPReport(t, svc))
		})
	}
}

func TestPortfolioBenchmarksOpenAPIContractMatchesRuntimeShapes(t *testing.T) {
	raw, err := os.ReadFile("../../contracts/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var api map[string]any
	if err := json.Unmarshal(raw, &api); err != nil {
		t.Fatal(err)
	}
	schemas := api["components"].(map[string]any)["schemas"].(map[string]any)
	for name, value := range map[string]any{"PortfolioBenchmarks": PortfolioBenchmarks{}, "PortfolioBenchmark": PortfolioBenchmark{}, "BenchmarkSeriesSummary": BenchmarkSeriesReceipt{}, "BenchmarkIndexMark": BenchmarkIndexPoint{}, "BenchmarkFXMark": BenchmarkFXMark{}, "BenchmarkRateResult": BenchmarkRateResult{}} {
		runtime, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(runtime, &fields); err != nil {
			t.Fatal(err)
		}
		schema := schemas[name].(map[string]any)
		if schema["additionalProperties"] != false || !reflect.DeepEqual(jsonKeySet(fields), jsonStringSet(schema["required"])) {
			t.Fatalf("closed %s shape differs from actual DTO", name)
		}
	}
	op := api["paths"].(map[string]any)["/v1/portfolio/benchmarks"].(map[string]any)["get"].(map[string]any)
	if op["operationId"] != "getPortfolioBenchmarks" {
		t.Fatal("comparison GET contract missing")
	}
}
