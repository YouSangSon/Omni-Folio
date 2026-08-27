package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestG4LLocalOrderLifecycleHTTPReplaysUnknownWithoutAuthorityIdentifiers(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-10T15:01:00Z") }
	order := recordK2COrder(t, svc, "client-local-order-read", "2", "70000")
	lease := mustK2CLease(t, svc, order.AccountRef)
	mustAuthorizeK2C(t, svc, order.OrderID, lease.FencingToken)

	response := httptest.NewRecorder()
	svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/orders", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("local order route status=%d body=%s", response.Code, response.Body.String())
	}
	var actual any
	if err := json.Unmarshal(response.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"source":           "local_order_log",
		"broker_freshness": "unverified",
		"orders": []any{map[string]any{
			"mode":             "synthetic",
			"symbol":           "005930",
			"side":             "BUY",
			"order_type":       "LIMIT",
			"quantity":         "2",
			"limit_price":      "70000",
			"filled_quantity":  "0",
			"currency":         "KRW",
			"status":           "SUBMIT_UNKNOWN",
			"pending_action":   "SUBMIT",
			"last_recorded_at": "2026-01-10T15:01:00Z",
		}},
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("local order response mismatch:\n got: %#v\nwant: %#v", actual, want)
	}
	for _, secret := range []string{order.OrderID, order.ClientOrderID, order.AccountRef, "risk_reservation", "fencing_token", "provider_order_ref"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("local order response leaked %q: %s", secret, response.Body.String())
		}
	}
}

func TestG4LLocalOrderLifecycleHTTPKeepsPendingCancelOnFilledOrder(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	order := mustReadyAndDispatchK2AOrder(t, svc, "client-local-filled-cancel-pending", "filled-cancel-pending")
	ack := k2aEvent("filled-cancel-pending-ack", order.OrderID, "SUBMIT_ACKNOWLEDGED")
	ack.ProviderOrderRef = k2aOrderRef
	mustAppendK2AEvent(t, svc, ack, "OPEN")
	mustAppendK2AEvent(t, svc, k2aEvent("filled-cancel-pending-cancel", order.OrderID, "CANCEL_DISPATCHED"), "CANCEL_UNKNOWN")
	mustAppendK2AEvent(t, svc, k2aFill("filled-cancel-pending-fill", order.OrderID, "kiwoom_execution_ZZZZZZZZZZZZZZZZZZZZZZZZ", "10", "1000"), "FILLED")

	response := httptest.NewRecorder()
	svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/orders", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("local order route status=%d body=%s", response.Code, response.Body.String())
	}
	var actual struct {
		Orders []struct {
			Status        string `json:"status"`
			PendingAction string `json:"pending_action"`
		} `json:"orders"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if len(actual.Orders) != 1 || actual.Orders[0].Status != "FILLED" || actual.Orders[0].PendingAction != "CANCEL" {
		t.Fatalf("filled order lost pending cancel: %+v body=%s", actual.Orders, response.Body.String())
	}
	mustAppendK2AEvent(t, svc, k2aEvent("filled-cancel-resolved-ack", order.OrderID, "CANCEL_ACKNOWLEDGED"), "FILLED")
	response = httptest.NewRecorder()
	svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/orders", nil))
	if err := json.Unmarshal(response.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if len(actual.Orders) != 1 || actual.Orders[0].Status != "FILLED" || actual.Orders[0].PendingAction != "none" {
		t.Fatalf("resolved cancel did not map to none: %+v body=%s", actual.Orders, response.Body.String())
	}
	for _, secret := range []string{order.OrderID, order.ClientOrderID, order.AccountRef, k2aOrderRef, "kiwoom_execution_ZZZZZZZZZZZZZZZZZZZZZZZZ"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("local order response leaked %q: %s", secret, response.Body.String())
		}
	}
}

func TestG4LOpenAPIExposesClosedLocalOrderLifecycle(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	paths := document["paths"].(map[string]any)
	if paths["/v1/orders"] == nil {
		t.Fatal("OpenAPI does not expose the local order lifecycle route")
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	logSchema, logOK := schemas["LocalOrderLog"].(map[string]any)
	viewSchema, viewOK := schemas["LocalOrderView"].(map[string]any)
	if !logOK || !viewOK || logSchema["additionalProperties"] != false || viewSchema["additionalProperties"] != false {
		t.Fatal("OpenAPI local order lifecycle schemas are not closed")
	}
	properties := viewSchema["properties"].(map[string]any)
	required := viewSchema["required"].([]any)
	if properties["pending_action"] == nil || !reflect.DeepEqual(required, []any{"mode", "symbol", "side", "order_type", "quantity", "limit_price", "filled_quantity", "currency", "status", "pending_action", "last_recorded_at"}) {
		t.Fatalf("OpenAPI local order view does not require pending_action")
	}
	for _, forbidden := range []string{"order_id", "client_order_id", "account_ref", "provider_order_ref", "provider_execution_ref", "risk_reservation_id", "fencing_token"} {
		if properties[forbidden] != nil {
			t.Fatalf("OpenAPI local order view exposes %s", forbidden)
		}
	}
}

func TestG4LLocalOrderLifecycleHTTPIsEmptyAndFailsClosedOnCorruption(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		response := httptest.NewRecorder()
		svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/orders", nil))
		if response.Code != http.StatusOK || response.Body.String() != "{\"source\":\"local_order_log\",\"broker_freshness\":\"unverified\",\"orders\":[]}\n" {
			t.Fatalf("empty local order response status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("corrupt event", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		order := mustRecordK2AOrder(t, svc, "client-corrupt-local-order")
		if _, err := svc.db.Exec(`DROP TRIGGER order_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE order_events SET event_json='{}' WHERE order_id=?`, order.OrderID); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/orders", nil))
		if response.Code != http.StatusInternalServerError || response.Body.String() != "{\"code\":\"internal_error\",\"message\":\"internal server error\"}\n" {
			t.Fatalf("corrupt local order response status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("corrupt event hash", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		order := mustRecordK2AOrder(t, svc, "client-corrupt-local-order-hash")
		if _, err := svc.db.Exec(`DROP TRIGGER order_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE order_events SET event_sha256=? WHERE order_id=?`, strings.Repeat("a", 64), order.OrderID); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/orders", nil))
		if response.Code != http.StatusInternalServerError || response.Body.String() != "{\"code\":\"internal_error\",\"message\":\"internal server error\"}\n" {
			t.Fatalf("corrupt local order hash response status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("invalid local timestamp", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		order := mustRecordK2AOrder(t, svc, "client-invalid-local-order-time")
		if _, err := svc.db.Exec(`DROP TRIGGER order_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE order_events SET recorded_at='not-a-time' WHERE order_id=?`, order.OrderID); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/orders", nil))
		if response.Code != http.StatusInternalServerError || response.Body.String() != "{\"code\":\"internal_error\",\"message\":\"internal server error\"}\n" {
			t.Fatalf("invalid local order timestamp response status=%d body=%s", response.Code, response.Body.String())
		}
	})
}
