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
	applyTestCSV(t, svc, "owner-price-capital", testCSV(fmt.Sprintf("capital,account-main,DEPOSIT,%s,,,,,KRW,100", start), fmt.Sprintf("buy,account-main,BUY,%s,005930,1,100,0,KRW,-100", buy)))
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil || len(snapshot.Holdings) != 1 {
		t.Fatalf("price identity fixture: %v", err)
	}
	raw, err := json.Marshal(map[string]any{
		"schema_version": "owner-price-series.v1", "source_version": "manual-v1",
		"instrument_id": snapshot.Holdings[0].InstrumentID, "symbol": "005930", "venue": "XKRX", "currency": "KRW",
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
