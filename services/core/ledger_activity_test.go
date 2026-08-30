package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestG1ELedgerActivitiesArePagedNewestFirstAndRedacted(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	seedLedgerActivities(t, svc)
	if _, err := svc.db.Exec(`UPDATE ledger_meta SET recorded_at='2026-01-10T16:00:00Z' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}

	first := httptest.NewRecorder()
	svc.routes().ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/v1/ledger/activities?limit=2", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first activity page status=%d body=%s", first.Code, first.Body.String())
	}
	var page map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page["source"] != "local_ledger" || page["broker_freshness"] != "unverified" || page["ledger_revision"] != "rev_0000000004" || page["recorded_at"] != "2026-01-10T15:01:00Z" {
		t.Fatalf("activity page metadata drifted: %#v", page)
	}
	events := page["events"].([]any)
	if len(events) != 2 || events[0].(map[string]any)["type"] != "FEE" || events[1].(map[string]any)["type"] != "FX_EXCHANGE" {
		t.Fatalf("activity order drifted: %#v", events)
	}
	fx := events[1].(map[string]any)
	if fx["currency"] != "USD" || fx["amount"] != "-100" || fx["counter_currency"] != "KRW" || fx["counter_amount"] != "137000" {
		t.Fatalf("FX activity lost an exact leg: %#v", fx)
	}
	for _, forbidden := range []string{"event-fee", "source-fee", "account-main", "receipt-seed", "event_id", "source_event_id", "account_id", "instrument_id", "receipt_id", "corrects_source_event_id", "sequence"} {
		if strings.Contains(first.Body.String(), forbidden) {
			t.Fatalf("activity response leaked %q: %s", forbidden, first.Body.String())
		}
	}
	cursor, ok := page["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("first page did not return a cursor: %#v", page["next_cursor"])
	}
	if _, err := svc.db.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,currency,amount,receipt_id,recorded_at) VALUES('event-late-import','source-late-import','account-main','DEPOSIT','2026-01-01T12:00:00Z','KRW','1','receipt-late','2026-01-11T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=5,recorded_at='2026-01-11T00:00:00Z',last_verified_at='2026-01-11T00:00:00Z' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}

	second := httptest.NewRecorder()
	svc.routes().ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/v1/ledger/activities?limit=2&cursor="+url.QueryEscape(cursor), nil))
	if second.Code != http.StatusOK {
		t.Fatalf("second activity page status=%d body=%s", second.Code, second.Body.String())
	}
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	events = page["events"].([]any)
	if len(events) != 2 || events[0].(map[string]any)["type"] != "BUY" || events[1].(map[string]any)["type"] != "DEPOSIT" || page["next_cursor"] != nil {
		t.Fatalf("activity continuation drifted: %#v", page)
	}
	if page["ledger_revision"] != "rev_0000000004" || page["recorded_at"] != "2026-01-10T15:01:00Z" || strings.Contains(second.Body.String(), "event-late-import") {
		t.Fatalf("cursor was not bound to its first-page ledger snapshot: %s", second.Body.String())
	}
}

func TestG1ELedgerActivitiesEmptyValidationAndCorruption(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		response := httptest.NewRecorder()
		svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/ledger/activities", nil))
		want := `{"source":"local_ledger","broker_freshness":"unverified","ledger_revision":"rev_0000000000","recorded_at":"1970-01-01T00:00:00Z","events":[],"next_cursor":null}` + "\n"
		if response.Code != http.StatusOK || response.Body.String() != want {
			t.Fatalf("empty activity response status=%d body=%s", response.Code, response.Body.String())
		}
	})

	for _, path := range []string{
		"/v1/ledger/activities?limit=0",
		"/v1/ledger/activities?limit=101",
		"/v1/ledger/activities?limit=2&limit=3",
		"/v1/ledger/activities?cursor=not-a-cursor",
		"/v1/ledger/activities?limit=1;unknown=1",
		"/v1/ledger/activities?unknown=1",
	} {
		t.Run(path, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			response := httptest.NewRecorder()
			svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "not-a-cursor") {
				t.Fatalf("invalid activity query status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	t.Run("forged canonical cursor", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		seedLedgerActivities(t, svc)
		foreign := newService(svc.db, svc.now, svc.id)
		for _, cursor := range []ledgerActivityCursor{
			{Version: 1, LedgerRevision: 5, LedgerRecordedAt: "2026-01-10T15:01:00Z", OccurredAt: "2026-01-03T00:00:00Z", Sequence: 4},
			{Version: 1, LedgerRevision: 4, LedgerRecordedAt: "2026-01-10T15:01:00Z", OccurredAt: "2026-01-01T00:00:00Z", Sequence: 4},
		} {
			response := httptest.NewRecorder()
			path := "/v1/ledger/activities?cursor=" + url.QueryEscape(svc.encodeLedgerActivityCursor(cursor))
			svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("forged cursor status=%d body=%s", response.Code, response.Body.String())
			}
		}
		rowValidForgery := foreign.encodeLedgerActivityCursor(ledgerActivityCursor{
			Version: 1, LedgerRevision: 4, LedgerRecordedAt: "2026-01-10T15:01:00Z",
			OccurredAt: "2026-01-02T00:00:00Z", Sequence: 2,
		})
		response := httptest.NewRecorder()
		svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/ledger/activities?cursor="+url.QueryEscape(rowValidForgery), nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("foreign cursor status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("issued cursor is opaque and tamper evident", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		seedLedgerActivities(t, svc)
		first := httptest.NewRecorder()
		svc.routes().ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/v1/ledger/activities?limit=1", nil))
		var page map[string]any
		if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		cursor := page["next_cursor"].(string)
		wire, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			t.Fatal(err)
		}
		var exposed map[string]any
		if json.Unmarshal(wire, &exposed) == nil {
			t.Fatalf("cursor exposed plaintext JSON: %#v", exposed)
		}
		tampered := cursor[:len(cursor)-1] + map[bool]string{true: "A", false: "B"}[cursor[len(cursor)-1] != 'A']
		response := httptest.NewRecorder()
		svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/ledger/activities?cursor="+tampered, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("tampered cursor status=%d body=%s", response.Code, response.Body.String())
		}
	})

	for _, mutation := range []string{
		`UPDATE events SET amount='01' WHERE source_event_id='source-deposit'`,
		`UPDATE events SET occurred_at='not-a-time' WHERE source_event_id='source-deposit'`,
		`UPDATE events SET recorded_at='2026-01-10T15:01:00+00:00' WHERE source_event_id='source-deposit'`,
		`UPDATE events SET sequence=0 WHERE sequence=1`,
	} {
		t.Run(mutation, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			seedLedgerActivities(t, svc)
			if _, err := svc.db.Exec(`DROP TRIGGER events_no_update`); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/ledger/activities", nil))
			want := `{"code":"internal_error","message":"internal server error"}` + "\n"
			if response.Code != http.StatusInternalServerError || response.Body.String() != want {
				t.Fatalf("corrupt activity response status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	t.Run("huge corrupt revision stays fail closed", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		seedLedgerActivities(t, svc)
		if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=9223372036854775807 WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/ledger/activities", nil))
		want := `{"code":"internal_error","message":"internal server error"}` + "\n"
		if response.Code != http.StatusInternalServerError || response.Body.String() != want {
			t.Fatalf("corrupt revision status=%d body=%s", response.Code, response.Body.String())
		}
		health := httptest.NewRecorder()
		svc.routes().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if health.Code != http.StatusOK {
			t.Fatalf("health after corrupt revision status=%d", health.Code)
		}
	})
}

func TestG1EOpenAPIExposesClosedLedgerActivities(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	paths := document["paths"].(map[string]any)
	activityPath, pathOK := paths["/v1/ledger/activities"].(map[string]any)
	if !pathOK || len(activityPath) != 1 || activityPath["get"] == nil {
		t.Fatal("OpenAPI does not expose ledger activities")
	}
	operation := activityPath["get"].(map[string]any)
	parameters := operation["parameters"].([]any)
	limitSchema := parameters[0].(map[string]any)["schema"].(map[string]any)
	cursorSchema := parameters[1].(map[string]any)["schema"].(map[string]any)
	if limitSchema["minimum"] != float64(1) || limitSchema["maximum"] != float64(100) || limitSchema["default"] != float64(50) || cursorSchema["maxLength"] != float64(1024) {
		t.Fatalf("OpenAPI activity query bounds drifted: limit=%#v cursor=%#v", limitSchema, cursorSchema)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	page, pageOK := schemas["LedgerActivityPage"].(map[string]any)
	event, eventOK := schemas["LedgerActivity"].(map[string]any)
	if !pageOK || !eventOK || page["additionalProperties"] != false || event["additionalProperties"] != false {
		t.Fatal("OpenAPI ledger activity schemas are not closed")
	}
	wantPage := []any{"source", "broker_freshness", "ledger_revision", "recorded_at", "events", "next_cursor"}
	wantEvent := []any{"type", "occurred_at", "recorded_at", "symbol", "quantity", "price", "fee", "currency", "amount", "counter_currency", "counter_amount", "is_correction"}
	if !reflect.DeepEqual(page["required"], wantPage) || !reflect.DeepEqual(event["required"], wantEvent) {
		t.Fatalf("OpenAPI ledger activity required fields drifted: page=%#v event=%#v", page["required"], event["required"])
	}
	properties := event["properties"].(map[string]any)
	if len(event["oneOf"].([]any)) != 7 || properties["occurred_at"].(map[string]any)["$ref"] != "#/components/schemas/LedgerActivityTimestamp" {
		t.Fatal("OpenAPI activity type or timestamp constraints drifted")
	}
	if !strings.Contains(properties["counter_currency"].(map[string]any)["description"].(string), "distinct from currency") {
		t.Fatal("OpenAPI lost the cross-field FX currency invariant")
	}
	timestamp := schemas["LedgerActivityTimestamp"].(map[string]any)
	if timestamp["pattern"] != `^\d{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])T(?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d{0,8}[1-9])?Z$` {
		t.Fatal("OpenAPI activity timestamp precision drifted")
	}
	pageProperties := page["properties"].(map[string]any)
	if pageProperties["next_cursor"].(map[string]any)["maxLength"] != float64(1024) {
		t.Fatal("OpenAPI response cursor bound drifted")
	}
	for _, forbidden := range []string{"sequence", "event_id", "source_event_id", "account_id", "instrument_id", "receipt_id", "corrects_source_event_id"} {
		if properties[forbidden] != nil {
			t.Fatalf("OpenAPI ledger activity exposes %s", forbidden)
		}
	}
}

func seedLedgerActivities(t *testing.T, svc *Service) {
	t.Helper()
	tx, err := svc.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows := [][]any{
		{"event-deposit", "source-deposit", "DEPOSIT", "2026-01-01T00:00:00Z", nil, nil, nil, nil, "USD", "200", nil, nil},
		{"event-buy", "source-buy", "BUY", "2026-01-02T00:00:00Z", "instrument_aapl", "AAPL", "2", "10", "USD", "-20", nil, nil},
		{"event-fx", "source-fx", "FX_EXCHANGE", "2026-01-03T00:00:00Z", nil, nil, nil, nil, "USD", "-100", "KRW", "137000"},
		{"event-fee", "source-fee", "FEE", "2026-01-03T00:00:00Z", nil, nil, nil, nil, "KRW", "-1000", nil, nil},
	}
	for _, row := range rows {
		fee := any(nil)
		if row[2] == "BUY" {
			fee = "0"
		}
		if _, err := tx.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,instrument_id,symbol,quantity,price,fee,currency,amount,counter_currency,counter_amount,receipt_id,recorded_at) VALUES(?,?,'account-main',?,?,?,?,?,?,?,?,?,?,?,'receipt-seed','2026-01-10T15:01:00Z')`, row[0], row[1], row[2], row[3], row[4], row[5], row[6], row[7], fee, row[8], row[9], row[10], row[11]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`UPDATE ledger_meta SET revision=4, recorded_at='2026-01-10T15:01:00Z', last_verified_at='2026-01-10T15:01:00Z' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
