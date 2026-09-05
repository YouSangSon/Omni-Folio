package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHoldingValuationHTTPStoredPriceView(t *testing.T) {
	svc := holdingValuationService(t)
	recordHoldingValuationPrice(t, svc, "private_observation", "XNAS", "15", "2026-01-10T14:00:00Z", "2026-01-10T14:00:01Z", "2026-01-11T13:00:00Z")
	svc.now = func() time.Time { return mustTime(holdingValuationAsOf) }
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/holding-valuation", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "stale_sample" || body["scope"] != "holdings_only" || body["sample"] != true || body["valuation_as_of"] != holdingValuationAsOf {
		t.Fatalf("wrong authority: %v", body)
	}
	line := body["lines"].([]any)[0].(map[string]any)
	price := line["price"].(map[string]any)
	if body["ledger_revision"] != "rev_0000000001" || body["issues"].([]any)[0].(map[string]any)["code"] != "sample_data" {
		t.Fatalf("wrong ledger/sample authority: %v", body)
	}
	for key, want := range map[string]any{
		"source": "local_fixture", "venue": "XNAS", "currency": "USD", "price": "15",
		"price_adjustment": "unspecified", "observed_at": "2026-01-10T14:00:00Z",
		"fetched_at": "2026-01-10T14:00:01Z", "recorded_at": "2026-01-11T13:00:00Z",
		"sample": true, "state": "stale",
	} {
		if price[key] != want {
			t.Fatalf("price %s=%v want %v", key, price[key], want)
		}
	}
	if line["quantity"] != "3" || line["cost_basis"] != "31" || line["market_value"] != "45" || line["unrealized_pnl"] != "14" {
		t.Fatalf("wrong calculation: %v", line)
	}
	for _, private := range []string{"instrument_id", "observation_id", "private_observation", "account_id", "record_sha256"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("private field exposed: %s", private)
		}
	}
	raw, err := os.ReadFile("../../contracts/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	path := doc["paths"].(map[string]any)["/v1/portfolio/holding-valuation"].(map[string]any)
	if len(path) != 1 || path["get"] == nil {
		t.Fatal("valuation must be GET-only")
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	for name, response := range map[string]map[string]any{
		"HoldingValuation": body, "HoldingValuationLine": line,
		"HoldingValuationPrice": line["price"].(map[string]any),
		"HoldingValuationTotal": body["totals"].([]any)[0].(map[string]any),
	} {
		schema := schemas[name].(map[string]any)
		properties := schema["properties"].(map[string]any)
		if schema["additionalProperties"] != false || len(properties) != len(response) || len(schema["required"].([]any)) != len(response) {
			t.Fatalf("%s must match closed runtime view", name)
		}
		for key := range response {
			if properties[key] == nil {
				t.Fatalf("%s has undocumented field %s", name, key)
			}
		}
	}
}

func TestHoldingValuationHTTPQueryBoundary(t *testing.T) {
	svc := holdingValuationService(t)
	svc.now = func() time.Time { return mustTime(holdingValuationAsOf) }
	for query, want := range map[string]int{
		"": 200, "?as_of=" + holdingValuationAsOf: 200,
		"?as_of=": 400, "?as_of": 400, "?as_of=x": 400,
		"?as_of=" + holdingValuationAsOf + "&as_of=" + holdingValuationAsOf: 400,
		"?private_query_name=secret":                                        400,
		"?as_of=" + holdingValuationAsOf + "&private_query_name=secret":     400,
		"?as_of=%ZZ": 400, "?as_of=x;y": 400,
		"?as_of=2026-01-11T15:00:00Z":     400,
		"?as_of=2026-01-11T14:00:00.000Z": 400,
		"?as_of=2026-01-01T00:00:00Z":     409,
	} {
		t.Run(query, func(t *testing.T) {
			w := httptest.NewRecorder()
			svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/holding-valuation"+query, nil))
			if w.Code != want {
				t.Fatalf("status %d want %d: %s", w.Code, want, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "private_query_name") || strings.Contains(w.Body.String(), "secret") {
				t.Fatal("query reflected")
			}
		})
	}
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/portfolio/holding-valuation", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d", w.Code)
	}
}

func TestHoldingValuationHTTPUnavailableAndFailure(t *testing.T) {
	for _, state := range []string{"empty", "missing", "stale", "corrupt"} {
		t.Run(state, func(t *testing.T) {
			var svc *Service
			if state == "empty" {
				svc, _ = testService(t, nil, nil)
			} else {
				svc = holdingValuationService(t)
			}
			if state == "stale" {
				recordHoldingValuationPrice(t, svc, "private_observation", "XNAS", "15", "2026-01-10T13:59:59Z", "2026-01-10T14:00:00Z", "2026-01-11T13:00:00Z")
			}
			if state == "corrupt" {
				if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=99 WHERE singleton=1`); err != nil {
					t.Fatal(err)
				}
			}
			svc.now = func() time.Time { return mustTime(holdingValuationAsOf) }
			w := httptest.NewRecorder()
			svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/holding-valuation", nil))
			if state == "corrupt" {
				if w.Code != 500 || strings.Contains(w.Body.String(), "proof") || strings.Contains(w.Body.String(), "revision") {
					t.Fatalf("unredacted corruption: %d %s", w.Code, w.Body.String())
				}
				return
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || body["totals"] != nil {
				t.Fatalf("invalid unavailable view: %d %v", w.Code, body)
			}
			lines := body["lines"].([]any)
			if state == "empty" {
				if body["status"] != "empty" || len(lines) != 0 {
					t.Fatal(body)
				}
			} else {
				line := lines[0].(map[string]any)
				if body["status"] != "unavailable" || line["status"] != state || line["market_value"] != nil || line["unrealized_pnl"] != nil {
					t.Fatal(body)
				}
			}
		})
	}
}
