package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestPortfolioTWRCheckpointsExposeTheSameObservation(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	applyTestCSV(t, svc, "twr-checkpoints", testCSV(
		"first,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100",
		"gain-one,account-main,DIVIDEND,2025-06-30T00:00:00Z,AAPL,,,,KRW,10",
		"second,account-main,DEPOSIT,2025-07-01T00:00:00Z,,,,,KRW,100",
		"gain-two,account-main,DIVIDEND,2026-01-01T00:00:00Z,AAPL,,,,KRW,21",
	))
	clocks := 0
	svc.now = func() time.Time { clocks++; return mustTime("2026-01-01T00:00:00Z") }
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/twr/checkpoints", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("TWR checkpoints unavailable: status=%d body=%s", w.Code, w.Body.String())
	}
	if clocks != 1 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("checkpoint observation did not retain one server cutoff and no-store")
	}
	var wire map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(fixture(t, "golden-portfolio-twr.json"), &report); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"policy_version": "twr_cash_flow_checkpoints.v1",
		"report":         report,
		"points": []any{
			map[string]any{"at": "2025-01-01T00:00:00Z", "kind": "external_flow", "cumulative_rate": "0"},
			map[string]any{"at": "2025-07-01T00:00:00Z", "kind": "external_flow", "cumulative_rate": "0.1"},
			map[string]any{"at": "2026-01-01T00:00:00Z", "kind": "valuation", "cumulative_rate": "0.21"},
		},
	}
	if !reflect.DeepEqual(wire, want) {
		t.Fatalf("cash contributions became plotted return or scalar/curve observation drifted: %s", w.Body.String())
	}
	old := httptest.NewRecorder()
	svc.routes().ServeHTTP(old, httptest.NewRequest("GET", "/v1/portfolio/twr", nil))
	var scalar map[string]any
	if old.Code != 200 || json.Unmarshal(old.Body.Bytes(), &scalar) != nil || !reflect.DeepEqual(scalar, report) {
		t.Fatal("existing scalar TWR contract changed")
	}
}
