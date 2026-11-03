package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOwnerFXImportFeedsCurrentPerformanceAndBenchmark(t *testing.T) {
	svc, dbPath := testService(t, nil, nil)
	svc.now = time.Now
	fetched := time.Now().UTC()
	start := fetched.Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano)
	end := fetched.Add(-time.Hour).Format(time.RFC3339Nano)
	applyTestCSV(t, svc, "owner-fx-capital", testCSV(fmt.Sprintf("capital,account-main,DEPOSIT,%s,,,,,USD,100", start)))
	raw, err := json.Marshal(map[string]any{
		"schema_version": "owner-fx-series.v1", "source_version": "manual-v1",
		"base_currency": "USD", "quote_currency": "KRW", "sample": false,
		"fetched_at": fetched.Format(time.RFC3339Nano),
		"points":     []map[string]string{{"observed_at": start, "rate": "1000"}, {"observed_at": end, "rate": "900"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(t.TempDir(), "owner-fx.json")
	if err := os.WriteFile(inputPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runContext(context.Background(), []string{"owner-fx-import", "-db", dbPath, "-input", inputPath}); err != nil {
		t.Fatalf("owner FX ingress unavailable: %v", err)
	}
	importBenchmarkForTest(t, svc, benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) {
		i.Currency, i.Sample, i.FetchedAt = "USD", false, fetched.Format(time.RFC3339Nano)
		i.Points = []BenchmarkIndexPoint{{ObservedAt: start, Level: "100"}, {ObservedAt: end, Level: "110"}}
	}))
	r := benchmarkHTTPReport(t, svc)
	if r.Portfolio.Valuation.TotalValue == nil || r.Portfolio.Valuation.TotalValue.Amount != "90000" || r.Portfolio.CumulativeRate == nil || *r.Portfolio.CumulativeRate != "-0.1" {
		t.Fatalf("manual FX did not reach current value and TWR: %+v", r.Portfolio)
	}
	row := r.Benchmarks[0]
	if row.Native.Rate == nil || *row.Native.Rate != "0.1" || row.KRW.Rate == nil || *row.KRW.Rate != "-0.01" || row.DifferencePercentagePoints == nil || *row.DifferencePercentagePoints != "-9" {
		t.Fatalf("native/KRW comparison mismatch: %+v", row)
	}
	if row.Sample || r.Portfolio.Sample || !strings.HasSuffix(r.PolicyVersion, "_v2") {
		t.Fatal("owner authority was confused with sample or legacy policy")
	}
	for _, issues := range [][]APIError{r.Portfolio.Issues, r.Portfolio.Valuation.Issues} {
		found := false
		for _, issue := range issues {
			found = found || issue.Code == "owner_declared_fx"
		}
		if !found {
			t.Fatal("manual FX authority warning was lost")
		}
	}
	encoded, err := json.Marshal(row.StartFX)
	if err != nil {
		t.Fatal(err)
	}
	var mark map[string]any
	if json.Unmarshal(encoded, &mark) != nil || mark["source"] != "owner_declared" || mark["source_version"] != "manual-v1" || mark["sample"] != false {
		t.Fatalf("manual FX mark lost its version/source/sample: %s", encoded)
	}
}

func ownerFXReportServiceForTest(t *testing.T, sample bool) *Service {
	t.Helper()
	svc := ownerFXServiceForTest(t)
	applyTestCSV(t, svc, "owner-capital", testCSV("capital,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,USD,100"))
	importOwnerFXForTest(t, svc, ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.Sample = sample }))
	importBenchmarkForTest(t, svc, benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.Currency = "USD"; i.Sample = false }))
	return svc
}

func ownerFXGoldenForTest(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	var wire struct {
		Reports map[string]json.RawMessage `json:"reports"`
	}
	if err := json.Unmarshal(fixture(t, "golden-owner-fx-reports.json"), &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Reports
}

func checkOwnerFXHTTPGolden(t *testing.T, svc *Service, path string, expected json.RawMessage) {
	t.Helper()
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("owner HTTP %d: %s", w.Code, w.Body.String())
	}
	var got, want any
	if json.Unmarshal(w.Body.Bytes(), &got) != nil || json.Unmarshal(expected, &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("shared owner response differs: got %s want %s", w.Body.String(), expected)
	}
	for _, private := range []string{"account-main", "source_event_id", "instrument_id", `\u0000`} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("private metadata leaked: %s", private)
		}
	}
}

func TestOwnerFXSharedProducerResponsesAndOneClock(t *testing.T) {
	svc := ownerFXReportServiceForTest(t, false)
	golden := ownerFXGoldenForTest(t)
	for name, path := range map[string]string{"current": "/v1/portfolio/current-valuation", "twr": "/v1/portfolio/twr", "xirr": "/v1/portfolio/xirr", "cash": "/v1/portfolio/cash-valuation?base_currency=KRW&as_of=2026-01-01T00:00:00Z", "benchmarks": "/v1/portfolio/benchmarks"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			svc.now = func() time.Time { calls++; return mustTime("2026-01-01T00:00:00Z") }
			checkOwnerFXHTTPGolden(t, svc, path, golden[name])
			if calls != 1 {
				t.Fatalf("report mixed clocks: %d", calls)
			}
		})
	}
}

func TestOwnerFXSharedSampleAndPartialMarks(t *testing.T) {
	golden := ownerFXGoldenForTest(t)
	checkOwnerFXHTTPGolden(t, ownerFXReportServiceForTest(t, true), "/v1/portfolio/benchmarks", golden["owner_sample"])
	for _, name := range []string{"partial_marks", "stale_marks"} {
		t.Run(name, func(t *testing.T) {
			svc := benchmarkPortfolioForTest(t)
			importOwnerFXForTest(t, svc, ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) {
				if name == "partial_marks" {
					i.Points = i.Points[1:]
				} else {
					i.Points[0].ObservedAt = "2024-12-30T23:59:59.999999999Z"
				}
			}))
			importBenchmarkForTest(t, svc, benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.Currency = "USD"; i.Sample = false }))
			checkOwnerFXHTTPGolden(t, svc, "/v1/portfolio/benchmarks", golden[name])
		})
	}
}

func TestOwnerFXCorrectionDoesNotBorrowRemovedPoints(t *testing.T) {
	svc := ownerFXReportServiceForTest(t, false)
	importOwnerFXForTest(t, svc, ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.SourceVersion = "v2"; i.Points = i.Points[1:] }))
	r := benchmarkHTTPReport(t, svc)
	if r.Portfolio.Status != "unavailable" || r.Portfolio.CumulativeRate != nil || r.Benchmarks[0].StartFX != nil || r.Benchmarks[0].KRW.Rate != nil {
		t.Fatal("removed historical point was borrowed from the prior version")
	}
	if r.Benchmarks[0].EndFX != nil || !usesOwnerFX(r.Portfolio.Issues) || !usesOwnerFX(r.Portfolio.Valuation.Issues) {
		t.Fatal("unavailable comparison invented marks or lost selected valuation authority")
	}
}

func TestOwnerFXUnusedPairLeavesFixturePolicyUnchanged(t *testing.T) {
	svc := benchmarkPortfolioForTest(t)
	before := benchmarkHTTPReport(t, svc)
	importOwnerFXForTest(t, svc, ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.BaseCurrency = "EUR" }))
	after := benchmarkHTTPReport(t, svc)
	if !reflect.DeepEqual(before, after) || strings.HasSuffix(after.PolicyVersion, "_v2") {
		t.Fatal("unused declared pair changed the public report")
	}
}

func TestOwnerFXNonSampleRateKeepsFixtureSecuritySampleWarning(t *testing.T) {
	svc := ownerFXReportServiceForTest(t, false)
	applyTestCSV(t, svc, "buy", testCSV("buy,account-main,BUY,2025-01-02T00:00:00Z,AAPL,1,10,0,USD,-10"))
	recordHoldingValuationPrice(t, svc, "terminal", "XNAS", "15", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	r := benchmarkHTTPReport(t, svc).Portfolio
	if r.Status != "calculated" || !r.Sample || !r.Valuation.Sample || r.Valuation.TotalValue == nil || r.Valuation.TotalValue.Amount != "94500" {
		t.Fatalf("mixed-source sample declaration lost: %+v", r)
	}
	for _, issues := range [][]APIError{r.Issues, r.Valuation.Issues} {
		sample := false
		for _, issue := range issues {
			sample = sample || issue.Code == "sample_data"
		}
		if !sample || !usesOwnerFX(issues) {
			t.Fatal("security sample or FX authority warning lost")
		}
	}
}
