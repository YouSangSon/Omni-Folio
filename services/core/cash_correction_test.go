package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCashVoidPreservesOriginalAndReplaysExactly(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "cash-void-base", testCSV(
		"d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,100",
		"f1,account-main,FEE,2026-01-02T00:00:00Z,,,,,USD,-5",
	))

	preview, appErr := svc.preview(context.Background(), []byte(cashVoidCSV(
		"c1,account-main,CASH_VOID,2026-01-03T00:00:00Z,,,,,USD,5,f1",
		"f2,account-main,FEE,2026-01-03T00:01:00Z,,,,,USD,-2,",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if !preview.CanApply || preview.MappingVersion != "canonical-transaction.v3" || preview.Totals.NewRows != 2 {
		t.Fatalf("cash void preview was not applicable: %+v", preview)
	}
	encoded, err := json.Marshal(preview.Rows[0])
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(encoded, &row); err != nil {
		t.Fatal(err)
	}
	target, ok := row["correction_target"].(map[string]any)
	if !ok || target["source_event_id"] != "f1" || target["type"] != "FEE" || target["currency"] != "USD" || target["amount"] != "-5" {
		t.Fatalf("preview omitted the safe correction target: %s", encoded)
	}

	receipt, appErr := svc.apply(context.Background(), ApplyRequest{PreviewID: preview.PreviewID, IdempotencyKey: "cash-void-apply"})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if receipt.AppliedRows != 2 || receipt.LedgerRevisionAfter != revision(4) {
		t.Fatalf("unexpected cash void receipt: %+v", receipt)
	}
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Cash, []Money{{Currency: "USD", Amount: "98"}}) || len(snapshot.Provenance.EventIDs) != 4 || len(snapshot.Provenance.ReceiptIDs) != 2 {
		t.Fatalf("cash void did not replay exactly: %+v", snapshot)
	}
	var typ, amount, targetSource string
	if err := svc.db.QueryRow(`SELECT type, amount, corrects_source_event_id FROM events WHERE account_id='account-main' AND source_event_id='c1'`).Scan(&typ, &amount, &targetSource); err != nil {
		t.Fatal(err)
	}
	if typ != "CASH_VOID" || amount != "5" || targetSource != "f1" {
		t.Fatalf("cash void linkage drifted: type=%s amount=%s target=%s", typ, amount, targetSource)
	}
	var originalAmount string
	if err := svc.db.QueryRow(`SELECT amount FROM events WHERE account_id='account-main' AND source_event_id='f1'`).Scan(&originalAmount); err != nil || originalAmount != "-5" {
		t.Fatalf("original event was mutated: amount=%s err=%v", originalAmount, err)
	}
	replay, appErr := svc.preview(context.Background(), []byte(cashVoidCSV(
		"c1,account-main,CASH_VOID,2026-01-03T00:00:00Z,,,,,USD,5,f1",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if !replay.CanApply || replay.Totals.DuplicateRows != 1 || replay.Rows[0].Status != "duplicate" || replay.Rows[0].CorrectionTarget == nil {
		t.Fatalf("exact cash void replay was not idempotent: %+v", replay)
	}

	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "cash-void.db")
	if _, err := createBackup(svc.db, backup, golden, backup+".manifest.json", svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err != nil {
		t.Fatal(err)
	}
	candidate, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`DROP TRIGGER events_cash_void_guard`); err != nil {
		candidate.Close()
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err == nil {
		t.Fatal("restore accepted a candidate without the cash-void guard")
	}
}

func TestCashVoidPreviewRejectsUnsafeTargets(t *testing.T) {
	tests := []struct {
		name       string
		seed       []string
		correction []string
	}{
		{"missing target", nil, []string{"c1,account-main,CASH_VOID,2026-01-03T00:00:00Z,,,,,USD,5,missing"}},
		{"wrong currency", []string{"f1,account-main,FEE,2026-01-02T00:00:00Z,,,,,USD,-5"}, []string{"c1,account-main,CASH_VOID,2026-01-03T00:00:00Z,,,,,KRW,5,f1"}},
		{"wrong amount", []string{"f1,account-main,FEE,2026-01-02T00:00:00Z,,,,,USD,-5"}, []string{"c1,account-main,CASH_VOID,2026-01-03T00:00:00Z,,,,,USD,4,f1"}},
		{"target from future", []string{"f1,account-main,FEE,2026-01-04T00:00:00Z,,,,,USD,-5"}, []string{"c1,account-main,CASH_VOID,2026-01-03T00:00:00Z,,,,,USD,5,f1"}},
		{"trade target", []string{"d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,100", "b1,account-main,BUY,2026-01-02T00:00:00Z,AAPL,1,10,0,USD,-10"}, []string{"c1,account-main,CASH_VOID,2026-01-03T00:00:00Z,,,,,USD,10,b1"}},
		{"same preview target", nil, []string{"f1,account-main,FEE,2026-01-02T00:00:00Z,,,,,USD,-5,", "c1,account-main,CASH_VOID,2026-01-03T00:00:00Z,,,,,USD,5,f1"}},
		{"same preview double void", []string{"f1,account-main,FEE,2026-01-02T00:00:00Z,,,,,USD,-5"}, []string{"c1,account-main,CASH_VOID,2026-01-03T00:00:00Z,,,,,USD,5,f1", "c2,account-main,CASH_VOID,2026-01-03T00:01:00Z,,,,,USD,5,f1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			if len(tt.seed) != 0 {
				applyTestCSV(t, svc, "seed-"+strings.ReplaceAll(tt.name, " ", "-"), testCSV(tt.seed...))
			}
			preview, appErr := svc.preview(context.Background(), []byte(cashVoidCSV(tt.correction...)))
			if appErr != nil {
				t.Fatal(appErr)
			}
			if preview.CanApply || preview.Totals.ErrorRows != 1 {
				t.Fatalf("unsafe cash void was accepted: %+v", preview)
			}
			last := preview.Rows[len(preview.Rows)-1]
			if len(last.Errors) != 1 || last.Errors[0].Code != "invalid_correction" || last.Errors[0].Field != "corrects_source_event_id" {
				t.Fatalf("unsafe cash void did not return a sanitized correction error: %+v", last)
			}
		})
	}

	t.Run("already corrected and correction chaining", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		applyTestCSV(t, svc, "seed", testCSV("f1,account-main,FEE,2026-01-01T00:00:00Z,,,,,USD,-5"))
		applyTestCSV(t, svc, "first-void", cashVoidCSV("c1,account-main,CASH_VOID,2026-01-02T00:00:00Z,,,,,USD,5,f1"))
		for _, row := range []string{
			"c2,account-main,CASH_VOID,2026-01-03T00:00:00Z,,,,,USD,5,f1",
			"c3,account-main,CASH_VOID,2026-01-03T00:00:00Z,,,,,USD,-5,c1",
		} {
			preview, appErr := svc.preview(context.Background(), []byte(cashVoidCSV(row)))
			if appErr != nil {
				t.Fatal(appErr)
			}
			if preview.CanApply || preview.Rows[0].Errors[0].Code != "invalid_correction" {
				t.Fatalf("repeated/chained cash void was accepted: %+v", preview)
			}
		}
	})
}

func TestImportSourceEventConflictIsNotSilentlyDuplicate(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "source-conflict-base", testCSV("d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,10"))

	exact, appErr := svc.preview(context.Background(), []byte(testCSV("d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,10")))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if !exact.CanApply || exact.Rows[0].Status != "duplicate" {
		t.Fatalf("exact replay was not idempotent: %+v", exact)
	}

	conflict, appErr := svc.preview(context.Background(), []byte(testCSV("d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,11")))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if conflict.CanApply || conflict.Rows[0].Status != "error" || conflict.Rows[0].Errors[0].Code != "source_event_conflict" {
		t.Fatalf("conflicting source_event_id was silently accepted: %+v", conflict)
	}

	mixed, appErr := svc.preview(context.Background(), []byte(testCSV(
		"d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,11",
		"d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,10",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if mixed.Rows[0].Status != "error" || mixed.Rows[1].Status != "duplicate" || mixed.Totals.ErrorRows != 1 || mixed.Totals.DuplicateRows != 1 {
		t.Fatalf("a conflicting row poisoned a later exact replay: %+v", mixed)
	}
}

func TestSchemaV8EnforcesCashVoidGuard(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "schema-cash-void-base", testCSV("f1,account-main,FEE,2026-01-01T00:00:00Z,,,,,USD,-5"))
	insert := func(eventID, sourceID, occurredAt, currency, amount, target string) error {
		_, err := svc.db.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,instrument_id,symbol,quantity,price,fee,currency,amount,corrects_source_event_id,receipt_id,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			eventID, sourceID, "account-main", "CASH_VOID", occurredAt, nil, nil, nil, nil, nil, currency, amount, target, "receipt-schema", "2026-01-02T00:00:00Z")
		return err
	}
	for _, invalid := range []struct {
		name, occurredAt, currency, amount, target string
	}{
		{"wrong amount", "2026-01-02T00:00:00Z", "USD", "4", "f1"},
		{"wrong currency", "2026-01-02T00:00:00Z", "KRW", "5", "f1"},
		{"before target", "2025-12-31T00:00:00Z", "USD", "5", "f1"},
		{"missing target", "2026-01-02T00:00:00Z", "USD", "5", "missing"},
	} {
		if err := insert("event-"+invalid.name, invalid.name, invalid.occurredAt, invalid.currency, invalid.amount, invalid.target); err == nil {
			t.Fatalf("schema accepted invalid cash void: %s", invalid.name)
		}
	}
	if err := insert("event-valid", "valid", "2026-01-02T00:00:00Z", "USD", "5", "f1"); err != nil {
		t.Fatal(err)
	}
	if err := insert("event-second", "second", "2026-01-02T00:00:00Z", "USD", "5", "f1"); err == nil {
		t.Fatal("schema accepted a second cash void for one target")
	}
	for _, statement := range []string{
		`UPDATE events SET amount=amount WHERE source_event_id='valid'`,
		`DELETE FROM events WHERE source_event_id='valid'`,
	} {
		if _, err := svc.db.Exec(statement); err == nil {
			t.Fatalf("cash void storage was mutable: %s", statement)
		}
	}

	tradeSvc, _ := testService(t, nil, nil)
	applyTestCSV(t, tradeSvc, "schema-trade-base", testCSV(
		"d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,100",
		"b1,account-main,BUY,2026-01-02T00:00:00Z,AAPL,1,10,0,USD,-10",
	))
	if _, err := tradeSvc.db.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,currency,amount,corrects_source_event_id,receipt_id,recorded_at) VALUES('event-trade-void','trade-void','account-main','CASH_VOID','2026-01-03T00:00:00Z','USD','10','b1','receipt','2026-01-03T00:00:00Z')`); err == nil {
		t.Fatal("schema accepted a cash void for a trade")
	}
}

func TestOpenAPIExposesClosedCashVoidContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	transaction := schemas["NormalizedTransaction"].(map[string]any)
	properties := transaction["properties"].(map[string]any)
	types := properties["type"].(map[string]any)["enum"].([]any)
	if !containsJSONText(types, "CASH_VOID") || properties["corrects_source_event_id"] == nil || transaction["additionalProperties"] != false || transaction["allOf"] == nil {
		t.Fatal("NormalizedTransaction does not expose the closed cash-void contract")
	}
	row := schemas["PreviewRow"].(map[string]any)
	if row["additionalProperties"] != false || row["properties"].(map[string]any)["correction_target"] == nil || row["allOf"] == nil {
		t.Fatal("PreviewRow does not expose a closed correction target")
	}
}

func applyTestCSV(t *testing.T, svc *Service, key, body string) {
	t.Helper()
	preview, appErr := svc.preview(context.Background(), []byte(body))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if !preview.CanApply {
		t.Fatalf("test setup preview was rejected: %+v", preview)
	}
	if _, appErr := svc.apply(context.Background(), ApplyRequest{PreviewID: preview.PreviewID, IdempotencyKey: key}); appErr != nil {
		t.Fatal(appErr)
	}
}

func cashVoidCSV(rows ...string) string {
	return "source_event_id,account_id,type,occurred_at,symbol,quantity,price,fee,currency,amount,corrects_source_event_id\n" + strings.Join(rows, "\n") + "\n"
}

func containsJSONText(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
