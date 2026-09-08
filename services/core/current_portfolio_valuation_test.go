package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const currentPortfolioValuationAsOf = "2026-01-11T14:00:00Z"

func TestCurrentPortfolioValuationUsesOneExactKRWCutoff(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "current-portfolio-valuation-seed", testCSV(
		"krw,account-main,DEPOSIT,2026-01-10T09:00:00Z,,,,,KRW,1000",
		"usd,account-main,DEPOSIT,2026-01-10T09:01:00Z,,,,,USD,100",
		"aapl-buy,account-main,BUY,2026-01-10T10:00:00Z,AAPL,3,10,1,USD,-31",
	))
	recordCurrentPortfolioFX(t, svc, "usd-krw", "USD", "KRW", "1370", "2026-01-11T13:00:00Z")
	recordHoldingValuationPrice(t, svc, "aapl-xnas", "XNAS", "15", "2026-01-11T13:00:00Z", "2026-01-11T13:00:01Z", "2026-01-11T13:00:02Z")
	svc.now = func() time.Time { return mustTime(currentPortfolioValuationAsOf) }

	valuation, appErr := svc.currentPortfolioValuation(context.Background())
	if appErr != nil {
		t.Fatal(appErr)
	}
	if valuation.Scope != "whole_portfolio" || valuation.PolicyVersion != "current_base_portfolio_valuation_v1" ||
		valuation.MaxObservationAgeSeconds != 86400 || valuation.BaseCurrency != "KRW" ||
		valuation.ValuationAsOf != currentPortfolioValuationAsOf || valuation.LedgerRevision != "rev_0000000003" ||
		valuation.Status != "stale_sample" || !valuation.Sample {
		t.Fatalf("valuation authority drifted: %+v", valuation)
	}
	if valuation.CashValue == nil || *valuation.CashValue != (Money{Currency: "KRW", Amount: "95530"}) ||
		valuation.HoldingsValue == nil || *valuation.HoldingsValue != (Money{Currency: "KRW", Amount: "61650"}) ||
		valuation.TotalValue == nil || *valuation.TotalValue != (Money{Currency: "KRW", Amount: "157180"}) {
		t.Fatalf("exact portfolio math drifted: %+v", valuation)
	}
	if len(valuation.Issues) != 1 || valuation.Issues[0].Code != "sample_data" {
		t.Fatalf("sample provenance is not explicit: %+v", valuation.Issues)
	}

	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil || snapshot.ValuationStatus != "unavailable" {
		t.Fatalf("point-in-time read changed snapshot authority: snapshot=%+v err=%v", snapshot, err)
	}
}

func TestCurrentPortfolioValuationIsAllOrNone(t *testing.T) {
	tests := []struct {
		name       string
		addPrice   bool
		addFX      bool
		offsetCash bool
		priceTime  string
		fxTime     string
		wantCode   string
	}{
		{name: "missing price", addFX: true, fxTime: "2026-01-11T13:00:00Z", wantCode: "missing_security_price"},
		{name: "stale price", addPrice: true, addFX: true, priceTime: "2026-01-10T13:59:59.999999999Z", fxTime: "2026-01-11T13:00:00Z", wantCode: "stale_security_price"},
		{name: "missing holding currency FX", addPrice: true, offsetCash: true, priceTime: "2026-01-11T13:00:00Z", wantCode: "missing_direct_fx"},
		{name: "stale holding currency FX", addPrice: true, addFX: true, offsetCash: true, priceTime: "2026-01-11T13:00:00Z", fxTime: "2026-01-10T13:59:59.999999999Z", wantCode: "stale_direct_fx"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			rows := []string{"krw,account-main,DEPOSIT,2026-01-10T09:00:00Z,,,,,KRW,1000"}
			if test.offsetCash {
				rows = append(rows, "usd-offset,account-main,DEPOSIT,2026-01-10T09:30:00Z,,,,,USD,31")
			}
			rows = append(rows, "aapl-buy,account-main,BUY,2026-01-10T10:00:00Z,AAPL,3,10,1,USD,-31")
			applyTestCSV(t, svc, "coverage-seed", testCSV(rows...))
			if test.addFX {
				recordCurrentPortfolioFX(t, svc, "usd-krw", "USD", "KRW", "1370", test.fxTime)
			}
			if test.addPrice {
				recordHoldingValuationPrice(t, svc, "aapl-xnas", "XNAS", "15", test.priceTime, "2026-01-11T13:00:01Z", "2026-01-11T13:00:02Z")
			}
			svc.now = func() time.Time { return mustTime(currentPortfolioValuationAsOf) }

			valuation, appErr := svc.currentPortfolioValuation(context.Background())
			if appErr != nil {
				t.Fatal(appErr)
			}
			if valuation.Status != "unavailable" || valuation.CashValue != nil || valuation.HoldingsValue != nil || valuation.TotalValue != nil {
				t.Fatalf("partial aggregate escaped: %+v", valuation)
			}
			found := false
			for _, issue := range valuation.Issues {
				found = found || issue.Code == test.wantCode
			}
			if !found {
				t.Fatalf("missing issue %q: %+v", test.wantCode, valuation.Issues)
			}
		})
	}
}

func TestCurrentPortfolioValuationCallsServerClockOnce(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "one-clock", testCSV("krw,account-main,DEPOSIT,2026-01-10T09:00:00Z,,,,,KRW,1000"))
	calls := 0
	svc.now = func() time.Time {
		calls++
		return mustTime(currentPortfolioValuationAsOf)
	}
	valuation, appErr := svc.currentPortfolioValuation(context.Background())
	if appErr != nil || valuation == nil || calls != 1 {
		t.Fatalf("server cutoff was not read exactly once: calls=%d valuation=%+v err=%v", calls, valuation, appErr)
	}
}

func TestCurrentPortfolioValuationAcceptsExact24HourBoundary(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "boundary-seed", testCSV(
		"krw,account-main,DEPOSIT,2026-01-10T09:00:00Z,,,,,KRW,1000",
		"usd-offset,account-main,DEPOSIT,2026-01-10T09:30:00Z,,,,,USD,31",
		"aapl-buy,account-main,BUY,2026-01-10T10:00:00Z,AAPL,3,10,1,USD,-31",
	))
	recordCurrentPortfolioFX(t, svc, "usd-krw", "USD", "KRW", "1370", "2026-01-10T14:00:00Z")
	recordHoldingValuationPrice(t, svc, "aapl-xnas", "XNAS", "15", "2026-01-10T14:00:00Z", "2026-01-10T14:00:00Z", "2026-01-11T13:00:02Z")
	svc.now = func() time.Time { return mustTime(currentPortfolioValuationAsOf) }

	valuation, appErr := svc.currentPortfolioValuation(context.Background())
	if appErr != nil || valuation.Status != "stale_sample" || valuation.TotalValue == nil || valuation.TotalValue.Amount != "62650" {
		t.Fatalf("exact freshness boundary was excluded: valuation=%+v err=%v", valuation, appErr)
	}
}

func TestCurrentPortfolioValuationEmptyAndKRWCashOnly(t *testing.T) {
	empty, _ := testService(t, nil, nil)
	empty.now = func() time.Time { return mustTime(currentPortfolioValuationAsOf) }
	valuation, appErr := empty.currentPortfolioValuation(context.Background())
	if appErr != nil || valuation.Status != "empty" || valuation.Sample || valuation.TotalValue != nil || len(valuation.Issues) != 0 {
		t.Fatalf("empty portfolio drifted: valuation=%+v err=%v", valuation, appErr)
	}

	krw, _ := testService(t, nil, nil)
	applyTestCSV(t, krw, "krw-only", testCSV("krw,account-main,DEPOSIT,2026-01-10T09:00:00Z,,,,,KRW,1000"))
	krw.now = func() time.Time { return mustTime(currentPortfolioValuationAsOf) }
	valuation, appErr = krw.currentPortfolioValuation(context.Background())
	if appErr != nil || valuation.Status != "complete" || valuation.Sample ||
		valuation.CashValue == nil || valuation.CashValue.Amount != "1000" ||
		valuation.HoldingsValue == nil || valuation.HoldingsValue.Amount != "0" ||
		valuation.TotalValue == nil || valuation.TotalValue.Amount != "1000" {
		t.Fatalf("KRW cash-only portfolio drifted: valuation=%+v err=%v", valuation, appErr)
	}
}

func TestCurrentPortfolioValuationHTTPIsClosedQuerylessAndSanitized(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "http-seed", testCSV("krw,account-main,DEPOSIT,2026-01-10T09:00:00Z,,,,,KRW,1000"))
	svc.now = func() time.Time { return mustTime(currentPortfolioValuationAsOf) }

	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/current-valuation", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"account-main", "account_id", "instrument_id", "observation_id", "source_observation_id", "record_sha256", "cost_basis", "unrealized_pnl", "performance"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("private or unsupported field exposed: %s", private)
		}
	}
	for _, query := range []string{"?as_of=2026-01-11T14:00:00Z", "?base_currency=USD", "?secret=value", "?%ZZ"} {
		w = httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/current-valuation"+query, nil))
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "value") {
			t.Fatalf("query boundary failed for %q: %d %s", query, w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/portfolio/current-valuation", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d", w.Code)
	}

	raw, err := os.ReadFile("../../contracts/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	path := document["paths"].(map[string]any)["/v1/portfolio/current-valuation"].(map[string]any)
	if len(path) != 1 || path["get"] == nil {
		t.Fatal("current valuation must be GET-only")
	}
	schema := document["components"].(map[string]any)["schemas"].(map[string]any)["CurrentPortfolioValuation"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	if schema["additionalProperties"] != false || len(properties) != len(body) || len(schema["required"].([]any)) != len(body) {
		t.Fatalf("OpenAPI must match closed runtime view: schema=%v body=%v", properties, body)
	}
	for key := range body {
		if properties[key] == nil {
			t.Fatalf("runtime field is undocumented: %s", key)
		}
	}
}

func TestCurrentPortfolioValuationHTTPFailsCorruptionClosed(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *Service)
	}{
		{
			name: "ledger proof",
			setup: func(t *testing.T, svc *Service) {
				applyTestCSV(t, svc, "ledger-corrupt", testCSV("krw,account-main,DEPOSIT,2026-01-10T09:00:00Z,,,,,KRW,1"))
				if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=99 WHERE singleton=1`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "FX hash",
			setup: func(t *testing.T, svc *Service) {
				applyTestCSV(t, svc, "fx-corrupt", testCSV("usd,account-main,DEPOSIT,2026-01-10T09:00:00Z,,,,,USD,1"))
				recordCurrentPortfolioFX(t, svc, "usd-krw", "USD", "KRW", "1370", "2026-01-11T13:00:00Z")
				if _, err := svc.db.Exec(`DROP TRIGGER fx_observations_no_update`); err != nil {
					t.Fatal(err)
				}
				if _, err := svc.db.Exec(`UPDATE fx_observations SET rate='1371'`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "security price hash",
			setup: func(t *testing.T, svc *Service) {
				applyTestCSV(t, svc, "price-corrupt", testCSV("aapl-buy,account-main,BUY,2026-01-10T10:00:00Z,AAPL,3,10,1,USD,-31"))
				recordHoldingValuationPrice(t, svc, "aapl-xnas", "XNAS", "15", "2026-01-11T13:00:00Z", "2026-01-11T13:00:01Z", "2026-01-11T13:00:02Z")
				if _, err := svc.db.Exec(`DROP TRIGGER security_price_observations_no_update`); err != nil {
					t.Fatal(err)
				}
				if _, err := svc.db.Exec(`UPDATE security_price_observations SET price='16'`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "server cutoff before ledger",
			setup: func(t *testing.T, svc *Service) {
				applyTestCSV(t, svc, "future-ledger", testCSV("krw,account-main,DEPOSIT,2026-01-11T15:00:00Z,,,,,KRW,1"))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			test.setup(t, svc)
			svc.now = func() time.Time { return mustTime(currentPortfolioValuationAsOf) }
			w := httptest.NewRecorder()
			svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/current-valuation", nil))
			if w.Code != http.StatusInternalServerError || w.Body.String() != "{\"code\":\"internal_error\",\"message\":\"internal server error\"}\n" {
				t.Fatalf("corruption was not redacted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func recordCurrentPortfolioFX(t *testing.T, svc *Service, sourceID, base, quote, rate, observedAt string) {
	t.Helper()
	svc.now = func() time.Time { return mustTime("2026-01-11T13:00:02Z") }
	if _, err := svc.recordFXObservation(context.Background(), FXObservationInput{
		Source: "local_fixture", SourceObservationID: sourceID, BaseCurrency: base, QuoteCurrency: quote,
		Rate: rate, ObservedAt: observedAt, FetchedAt: "2026-01-11T13:00:01Z",
	}); err != nil {
		t.Fatal(err)
	}
}
