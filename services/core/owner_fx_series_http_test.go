package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
