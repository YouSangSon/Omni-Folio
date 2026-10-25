package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPortfolioXIRRUsesRecordedExternalCashFlows(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	applyTestCSV(t, svc, "xirr-seed", testCSV(
		"capital,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100",
		"income,account-main,DIVIDEND,2026-01-01T00:00:00Z,AAPL,,,,KRW,10",
	))
	response := httptest.NewRecorder()
	svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/portfolio/xirr", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("XIRR report unavailable: status=%d body=%s", response.Code, response.Body.String())
	}
	var report map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report["status"] != "calculated" || report["annual_rate"] != "0.1" || report["uniqueness"] != "unique" {
		t.Fatalf("deposit or dividend misclassified: %s", response.Body.String())
	}
}
