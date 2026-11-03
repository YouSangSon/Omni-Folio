package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestManualEntrySharedCSVPreviewAndIdempotentApply(t *testing.T) {
	raw, err := os.ReadFile("../../contracts/fixtures/manual-entry-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SchemaVersion string `json:"schema_version"`
		Cases         []struct {
			Name        string            `json:"name"`
			SeedCSV     string            `json:"seed_csv"`
			CSV         string            `json:"expected_csv"`
			Transaction map[string]string `json:"expected_transaction"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || fixture.SchemaVersion != "manual-entry-cases.v1" || len(fixture.Cases) < 10 {
		t.Fatalf("invalid manual-entry fixture: %v", err)
	}
	for _, entry := range fixture.Cases {
		t.Run(entry.Name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			applyTestCSV(t, svc, "manual-seed", entry.SeedCSV)
			count := func() int {
				var n int
				if err := svc.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}
			before := count()
			preview, appErr := svc.preview(context.Background(), []byte(entry.CSV))
			if appErr != nil || !preview.CanApply || len(preview.Rows) != 1 || preview.Rows[0].Status != "new" {
				t.Fatalf("manual entry did not reach the existing import boundary: preview=%+v err=%v", preview, appErr)
			}
			if count() != before {
				t.Fatal("preview wrote ledger events")
			}
			encoded, err := json.Marshal(preview.Rows[0].Transaction)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			for key, want := range entry.Transaction {
				if got[key] != want {
					t.Fatalf("normalized %s: got=%v want=%s", key, got[key], want)
				}
			}
			request := ApplyRequest{PreviewID: preview.PreviewID, IdempotencyKey: "manual-" + entry.Name}
			receipt, appErr := svc.apply(context.Background(), request)
			if appErr != nil || receipt.AppliedRows != 1 || count() != before+1 {
				t.Fatalf("manual entry was not applied exactly once: receipt=%+v err=%v", receipt, appErr)
			}
			retried, appErr := svc.apply(context.Background(), request)
			if appErr != nil || retried.ReceiptID != receipt.ReceiptID || retried.LedgerRevisionAfter != receipt.LedgerRevisionAfter || count() != before+1 {
				t.Fatalf("same request produced a duplicate: receipt=%+v err=%v", retried, appErr)
			}
			duplicate, appErr := svc.preview(context.Background(), []byte(entry.CSV))
			if appErr != nil || !duplicate.CanApply || duplicate.Totals.DuplicateRows != 1 {
				t.Fatalf("same stable source identity was not a duplicate: preview=%+v err=%v", duplicate, appErr)
			}
			repeated, appErr := svc.apply(context.Background(), ApplyRequest{PreviewID: duplicate.PreviewID, IdempotencyKey: request.IdempotencyKey + "-repreview"})
			if appErr != nil || repeated.AppliedRows != 0 || repeated.SkippedDuplicateRows != 1 || count() != before+1 {
				t.Fatalf("repreview duplicated the source event: receipt=%+v err=%v", repeated, appErr)
			}
		})
	}
}
