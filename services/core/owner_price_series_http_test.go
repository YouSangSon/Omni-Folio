package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnerPriceImportFeedsCurrentAndPerformance(t *testing.T) {
	svc, dbPath := testService(t, nil, nil)
	svc.now = time.Now
	fetched := time.Now().UTC()
	start := fetched.Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano)
	buy := fetched.Add(-365*24*time.Hour + time.Second).Format(time.RFC3339Nano)
	end := fetched.Add(-time.Hour).Format(time.RFC3339Nano)
	applyTestCSV(t, svc, "owner-price-capital", testCSV(fmt.Sprintf("capital,account-main,DEPOSIT,%s,,,,,USD,100", start), fmt.Sprintf("buy,account-main,BUY,%s,AAPL,1,100,0,USD,-100", buy)))
	importOwnerFXForTest(t, svc, ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) {
		i.FetchedAt = fetched.Format(time.RFC3339Nano)
		i.Points = []OwnerFXPoint{{ObservedAt: start, Rate: "1"}, {ObservedAt: end, Rate: "1"}}
	}))
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil || len(snapshot.Holdings) != 1 {
		t.Fatalf("price identity fixture: %v", err)
	}
	raw, err := json.Marshal(map[string]any{
		"schema_version": "owner-price-series.v1", "source_version": "manual-v1",
		"instrument_id": snapshot.Holdings[0].InstrumentID, "symbol": "AAPL", "venue": "XNAS", "currency": "USD",
		"price_adjustment": "unadjusted", "sample": false, "fetched_at": fetched.Format(time.RFC3339Nano),
		"points": []map[string]string{{"observed_at": start, "price": "100"}, {"observed_at": end, "price": "200"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "owner-price.json")
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runContext(context.Background(), []string{"owner-price-import", "-db", dbPath, "-input", input}); err != nil {
		t.Fatalf("owner price ingress unavailable: %v", err)
	}
	value, appErr := svc.currentPortfolioValuation(context.Background())
	if appErr != nil || value.TotalValue == nil || value.TotalValue.Amount != "200" || value.Sample || value.Status != "complete" {
		t.Fatalf("owner price did not reach current value: %+v %v", value, appErr)
	}
	report, appErr := svc.portfolioTWR(context.Background())
	if appErr != nil || report.CumulativeRate == nil || *report.CumulativeRate != "1" || report.Sample {
		t.Fatalf("owner price did not reach TWR: %+v %v", report, appErr)
	}
	for _, issues := range [][]APIError{value.Issues, report.Issues} {
		found := false
		for _, issue := range issues {
			found = found || issue.Code == "owner_declared_price"
		}
		if !found {
			t.Fatal("owner price authority warning lost")
		}
	}
}

func ownerPriceReportServiceForTest(t *testing.T, change func(*OwnerPriceSeriesInput)) *Service {
	t.Helper()
	svc := ownerPriceServiceForTest(t)
	importOwnerFXForTest(t, svc, []byte(ownerFXTestInput))
	importOwnerPriceForTest(t, svc, ownerPriceInputForTest(t, change))
	importBenchmarkForTest(t, svc, benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.Currency = "USD"; i.Sample = false }))
	return svc
}

func ownerPriceGoldenForTest(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	var wire struct {
		Reports map[string]json.RawMessage `json:"reports"`
	}
	if err := json.Unmarshal(fixture(t, "golden-owner-price-reports.json"), &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Reports
}

func TestOwnerPriceSharedProducerReportsAndOneClock(t *testing.T) {
	svc := ownerPriceReportServiceForTest(t, nil)
	golden := ownerPriceGoldenForTest(t)
	for name, path := range map[string]string{"holding": "/v1/portfolio/holding-valuation?as_of=2026-01-01T00:00:00Z", "current": "/v1/portfolio/current-valuation", "twr": "/v1/portfolio/twr", "xirr": "/v1/portfolio/xirr", "benchmarks": "/v1/portfolio/benchmarks"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			svc.now = func() time.Time { calls++; return mustTime("2026-01-01T00:00:00Z") }
			checkOwnerFXHTTPGolden(t, svc, path, golden[name])
			if name != "holding" && calls != 1 {
				t.Fatalf("mixed report clocks: %d", calls)
			}
		})
	}
}

func TestOwnerPriceSharedSampleStaleAndSplitProvenance(t *testing.T) {
	golden := ownerPriceGoldenForTest(t)
	for _, name := range []string{"sample_holding", "stale_holding", "split_holding"} {
		t.Run(name, func(t *testing.T) {
			svc := ownerPriceServiceForTest(t)
			if name == "split_holding" {
				applyTestCSV(t, svc, "split", testCSV("split,account-main,SPLIT,2025-12-31T23:00:00Z,AAPL,2,,,USD,0"))
			}
			importOwnerPriceForTest(t, svc, ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) {
				switch name {
				case "sample_holding":
					i.Sample = true
				case "stale_holding":
					i.Points[1].ObservedAt = "2025-12-30T23:59:59.999999999Z"
				case "split_holding":
					i.Points[1].ObservedAt = "2025-12-31T23:00:00Z"
					i.Points[1].Price = "50"
				}
			}))
			checkOwnerFXHTTPGolden(t, svc, "/v1/portfolio/holding-valuation?as_of=2026-01-01T00:00:00Z", golden[name])
		})
	}
}
