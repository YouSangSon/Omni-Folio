package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestInstrumentLedgerCurrentCostAndOriginalTrades(t *testing.T) {
	svc := holdingValuationService(t) // 3 shares, total remaining cost 31 USD including fee.
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/instrument-ledger?instrument_id=instrument_aapl&currency=USD", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"instrument_id": "instrument_aapl", "symbol": "AAPL", "currency": "USD",
		"quantity": "3", "cost_basis": "31", "average_cost": "10.33333333",
		"average_cost_policy": "remaining_fifo_cost_per_unit_half_even_8_v1",
		"cost_basis_policy":   fifoCostBasisPolicy, "source": "local_ledger", "broker_freshness": "unverified",
		"ledger_revision": "rev_0000000001", "has_more": false, "trade_limit": float64(50),
	} {
		if body[key] != want {
			t.Errorf("%s=%v want %v", key, body[key], want)
		}
	}
	trades := body["trades"].([]any)
	if len(trades) != 1 {
		t.Fatalf("trades=%v", trades)
	}
	trade := trades[0].(map[string]any)
	if trade["type"] != "BUY" || trade["quantity"] != "3" || trade["price"] != "10" || trade["fee"] != "1" {
		t.Fatalf("original trade changed: %v", trade)
	}
	for _, private := range []string{"account_id", "event_id", "source_event_id", "receipt_id", "sequence"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("private field exposed: %s", private)
		}
	}
}

func TestInstrumentLedgerSplitSellAndExactIdentity(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "split-trades", testCSV(
		"buy,account-main,BUY,2026-01-01T00:00:00Z,AAPL,3,10,0,USD,-30",
		"split,account-main,SPLIT,2026-01-02T00:00:00Z,AAPL,2,,,USD,0",
		"sell,account-main,SELL,2026-01-03T00:00:00Z,AAPL,2,7,1,USD,13",
		"other-currency,account-main,BUY,2026-01-04T00:00:00Z,AAPL,1,100,0,KRW,-100",
		"cash,account-main,DEPOSIT,2026-01-05T00:00:00Z,,,,,USD,200",
	))
	view, appErr := svc.instrumentLedger(context.Background(), "instrument_aapl", "USD")
	if appErr != nil {
		t.Fatal(appErr)
	}
	if view.Quantity != "4" || view.CostBasis != "20" || *view.AverageCost != "5" || len(view.Trades) != 2 ||
		view.LedgerRevision != "rev_0000000005" || view.LedgerAsOf != "2026-01-05T00:00:00Z" {
		t.Fatalf("split/current revision mismatch: %+v", view)
	}
	if *view.Trades[1].Quantity != "3" || *view.Trades[1].Price != "10" || view.Trades[0].Type != "SELL" {
		t.Fatalf("original trades adjusted: %+v", view.Trades)
	}
	applyTestCSV(t, svc, "close", testCSV("close,account-main,SELL,2026-01-06T00:00:00Z,AAPL,4,1,5,USD,-1"))
	closed, appErr := svc.instrumentLedger(context.Background(), "instrument_aapl", "USD")
	if appErr != nil || closed.Quantity != "0" || closed.CostBasis != "0" || closed.AverageCost != nil || len(closed.Trades) != 3 || closed.Trades[0].Amount != "-1" {
		t.Fatalf("sold out or negative net sale mismatch: %+v %v", closed, appErr)
	}
	if view.Quantity != "4" || len(view.Trades) != 2 {
		t.Fatal("previous observation mutated")
	}
}

func TestInstrumentLedgerRounding(t *testing.T) {
	for _, tc := range []struct{ quantity, price, fee, amount, want string }{
		{"2", "1", "0.00000001", "-2.00000001", "1"},
		{"2", "1", "0.00000003", "-2.00000003", "1.00000002"},
		{"1", "0.000000005", "0", "-0.000000005", "0"},
	} {
		t.Run(tc.amount, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			applyTestCSV(t, svc, "round", testCSV(fmt.Sprintf("buy,account-main,BUY,2026-01-01T00:00:00Z,AAPL,%s,%s,%s,USD,%s", tc.quantity, tc.price, tc.fee, tc.amount)))
			view, appErr := svc.instrumentLedger(context.Background(), "instrument_aapl", "USD")
			if appErr != nil || view.AverageCost == nil || *view.AverageCost != tc.want || view.CostBasis != strings.TrimPrefix(tc.amount, "-") {
				t.Fatalf("display-only rounding: %+v %v", view, appErr)
			}
		})
	}
}

func TestInstrumentLedgerBoundAndNanosecondOrder(t *testing.T) {
	for _, count := range []int{50, 51} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			rows := []string{}
			for i := 1; i <= count; i++ {
				at := fmt.Sprintf("2026-01-01T00:00:00.%09dZ", i)
				if i == 1 {
					at = "2026-01-01T00:00:00Z"
				}
				if i == count {
					at = fmt.Sprintf("2026-01-01T00:00:00.%09dZ", i-1)
				}
				rows = append(rows, fmt.Sprintf("buy-%d,account-main,BUY,%s,AAPL,1,%d,0,USD,-%d", i, at, i, i))
			}
			rows = append(rows, "other,account-main,BUY,2026-01-02T00:00:00Z,AAPL,1,1,0,KRW,-1")
			applyTestCSV(t, svc, "history", testCSV(rows...))
			view, appErr := svc.instrumentLedger(context.Background(), "instrument_aapl", "USD")
			if appErr != nil || len(view.Trades) != 50 || view.HasMore != (count > 50) {
				t.Fatalf("bound mismatch: %+v %v", view, appErr)
			}
			for i, trade := range view.Trades {
				if *trade.Price != fmt.Sprint(count-i) {
					t.Fatalf("order[%d]=%s", i, *trade.Price)
				}
			}
		})
	}
}

func TestInstrumentLedgerQueryAndProofBoundary(t *testing.T) {
	svc := holdingValuationService(t)
	for _, query := range []string{"", "?instrument_id=instrument_aapl", "?currency=USD", "?instrument_id=&currency=USD", "?instrument_id=%FF&currency=USD", "?instrument_id=instrument_aapl&currency=usd", "?instrument_id=instrument_aapl&currency=USD&currency=USD", "?instrument_id=instrument_aapl&currency=USD&secret=value", "?instrument_id=%ZZ&currency=USD", "?instrument_id=x;y&currency=USD"} {
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/instrument-ledger"+query, nil))
		if w.Code != 400 || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("query boundary %s: %d %s", query, w.Code, w.Body.String())
		}
	}
	for _, pair := range [][2]string{{"instrument_unknown", "USD"}, {"instrument_aapl", "KRW"}, {"instrument_msft", "USD"}} {
		_, appErr := svc.instrumentLedger(context.Background(), pair[0], pair[1])
		if appErr == nil || appErr.status != 404 {
			t.Fatalf("missing trade accepted: %v", appErr)
		}
	}
	dividendOnly, _ := testService(t, nil, nil)
	applyTestCSV(t, dividendOnly, "dividend", testCSV("div,account-main,DIVIDEND,2026-01-11T00:00:00Z,AAPL,,,,USD,1"))
	if _, appErr := dividendOnly.instrumentLedger(context.Background(), "instrument_aapl", "USD"); appErr == nil || appErr.status != 404 {
		t.Fatalf("dividend-only instrument accepted: %v", appErr)
	}
	if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=0 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/instrument-ledger?instrument_id=instrument_aapl&currency=USD", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "proof") || strings.Contains(w.Body.String(), "revision") {
		t.Fatalf("proof failure leaked: %d %s", w.Code, w.Body.String())
	}
}

func TestInstrumentLedgerDuplicateImportKeepsEventRevision(t *testing.T) {
	svc := holdingValuationService(t)
	svc.now = func() time.Time { return mustTime("2026-01-12T00:00:00Z") }
	applyTestCSV(t, svc, "duplicate-buy", testCSV("aapl-buy,account-main,BUY,2026-01-10T10:00:00Z,AAPL,3,10,1,USD,-31"))
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/instrument-ledger?instrument_id=instrument_aapl&currency=USD", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("duplicate-only import broke detail: %d %s", w.Code, w.Body.String())
	}
	var view InstrumentLedger
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.LedgerRevision != "rev_0000000001" || view.LedgerRecordedAt != "2026-01-10T15:01:00Z" || view.Quantity != "3" || len(view.Trades) != 1 {
		t.Fatalf("duplicate import changed event observation: %+v", view)
	}
}

func TestInstrumentLedgerCanonicalLabelsAndOpaqueIdentity(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	longSymbol := "A " + strings.Repeat("B", 130)
	for i, symbol := range []string{"AAPL", "aapl", "MSFT", longSymbol} {
		at := fmt.Sprintf("2026-01-0%dT00:00:00Z", i+1)
		recordedAt := fmt.Sprintf("2026-01-%02dT00:00:00Z", 20-i)
		if _, err := svc.db.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,instrument_id,symbol,quantity,price,fee,currency,amount,receipt_id,recorded_at)
   VALUES(?, ?, ?, 'BUY', ?, ?, ?, '1', '10', '0', 'USD', '-10', 'receipt-canonical', ?)`, fmt.Sprint("event-", i), fmt.Sprint("source-", i), fmt.Sprint("account-", i), at, instrumentIDForSymbol(symbol), symbol, recordedAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=4,recorded_at='2026-01-17T00:00:00Z' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	view, appErr := svc.instrumentLedger(context.Background(), "instrument_aapl", "USD")
	if appErr != nil || view.Symbol != "AAPL" || view.Quantity != "2" || len(view.Trades) != 2 || *view.Trades[0].Symbol != "aapl" || view.LedgerRecordedAt != "2026-01-17T00:00:00Z" || view.Trades[0].RecordedAt != "2026-01-19T00:00:00Z" {
		t.Fatalf("canonical label/clock/identity mismatch: %+v %v", view, appErr)
	}
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/instrument-ledger?instrument_id="+url.QueryEscape(instrumentIDForSymbol(longSymbol))+"&currency=USD", nil))
	if w.Code != 200 {
		t.Fatalf("snapshot opaque identity rejected: %d %s", w.Code, w.Body.String())
	}
	if _, err := svc.db.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,instrument_id,symbol,quantity,price,fee,currency,amount,receipt_id,recorded_at)
  VALUES('close-label','close-label','account-0','SELL','2026-01-05T00:00:00Z','instrument_aapl','aapl','2','10','0','USD','20','receipt-close','2026-01-21T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=5,recorded_at='2026-01-21T00:00:00Z' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	closed, appErr := svc.instrumentLedger(context.Background(), "instrument_aapl", "USD")
	if appErr != nil || closed.Symbol != "aapl" || closed.Quantity != "0" || len(closed.Trades) != 3 {
		t.Fatalf("closed label is not latest trade: %+v %v", closed, appErr)
	}
}

func TestInstrumentLedgerOpenAPI(t *testing.T) {
	svc := holdingValuationService(t)
	view, appErr := svc.instrumentLedger(context.Background(), "instrument_aapl", "USD")
	if appErr != nil {
		t.Fatal(appErr)
	}
	raw, err := os.ReadFile("../../contracts/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc, body map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(view)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	path, ok := doc["paths"].(map[string]any)["/v1/portfolio/instrument-ledger"].(map[string]any)
	if !ok || len(path) != 1 || path["get"] == nil {
		t.Fatal("GET contract missing")
	}
	schema := doc["components"].(map[string]any)["schemas"].(map[string]any)["InstrumentLedger"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	if schema["additionalProperties"] != false || len(properties) != len(body) || len(schema["required"].([]any)) != len(body) {
		t.Fatal("closed response mismatch")
	}
	for key := range body {
		if properties[key] == nil {
			t.Fatalf("undocumented %s", key)
		}
	}
}
