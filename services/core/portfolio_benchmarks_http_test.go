package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPortfolioBenchmarksComparesKRWAndUSDWithOneTWRObservation(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	applyTestCSV(t, svc, "benchmark-portfolio", testCSV(
		"first,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100",
		"gain-one,account-main,DIVIDEND,2025-06-30T00:00:00Z,AAPL,,,,KRW,10",
		"second,account-main,DEPOSIT,2025-07-01T00:00:00Z,,,,,KRW,100",
		"gain-two,account-main,DIVIDEND,2026-01-01T00:00:00Z,AAPL,,,,KRW,21",
	))
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
}
