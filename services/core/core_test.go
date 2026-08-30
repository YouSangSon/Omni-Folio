package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGoldenVerticalSliceAndBackupRestore(t *testing.T) {
	svc, dbPath := testService(t,
		[]time.Time{
			mustTime("2026-01-10T15:00:30Z"),
			mustTime("2026-01-10T15:01:00Z"),
			mustTime("2026-01-10T15:01:00.125Z"),
		},
		map[string][]string{
			"event":   {"event_cash_001", "event_trade_001", "event_trade_002"},
			"preview": {"preview_golden_001"},
			"receipt": {"receipt_golden_001"},
		},
	)
	csvBody := fixture(t, "golden-import.csv")
	preview, appErr := svc.preview(context.Background(), csvBody)
	if appErr != nil {
		t.Fatal(appErr)
	}
	assertJSONFixture(t, preview, "golden-preview.json")

	var req ApplyRequest
	decodeFixture(t, "golden-apply-request.json", &req)
	receipt, appErr := svc.apply(context.Background(), req)
	if appErr != nil {
		t.Fatal(appErr)
	}
	assertJSONFixture(t, receipt, "golden-apply.json")
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONFixture(t, snapshot, "golden-snapshot.json")

	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := backupAndVerify(svc.db, backup, fixturePath("golden-snapshot.json")); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(backup, fixturePath("golden-snapshot.json"), backup+".manifest.json"); err != nil {
		t.Fatal(err)
	}
	legacyGolden := bytes.Replace(
		fixture(t, "golden-snapshot.json"),
		[]byte("  \"cost_basis_policy\": \"fifo_exact_else_half_even_residual_8_v1\",\n"),
		nil,
		1,
	)
	legacyGoldenPath := filepath.Join(t.TempDir(), "legacy-golden-without-cost-policy.json")
	if err := os.WriteFile(legacyGoldenPath, legacyGolden, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyRestoreProof(backup, legacyGoldenPath); err != nil {
		t.Fatal("compatible legacy golden was rejected:", err)
	}
	if err := verifyManifest(backup, fixturePath("golden-snapshot.json"), fixturePath("invalid-backup-manifest.json")); err == nil {
		t.Fatal("rejected backup manifest was accepted")
	}
	if err := svc.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, fixturePath("golden-snapshot.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatal("backup changed the active database:", err)
	}
}

func TestVerifyRestoreMismatchRedactsPortfolioData(t *testing.T) {
	svc, _ := testService(t,
		[]time.Time{
			mustTime("2026-01-10T15:00:30Z"),
			mustTime("2026-01-10T15:01:00Z"),
			mustTime("2026-01-10T15:01:00.125Z"),
		},
		map[string][]string{
			"event":   {"event_cash_001", "event_trade_001", "event_trade_002"},
			"preview": {"preview_golden_001"},
			"receipt": {"receipt_golden_001"},
		},
	)
	preview, appErr := svc.preview(context.Background(), fixture(t, "golden-import.csv"))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(context.Background(), ApplyRequest{PreviewID: preview.PreviewID, IdempotencyKey: "restore-redaction"}); appErr != nil {
		t.Fatal(appErr)
	}

	backup := filepath.Join(t.TempDir(), "restore-redaction.db")
	manifestPath := backup + ".manifest.json"
	if _, err := createBackup(svc.db, backup, fixturePath("golden-snapshot.json"), manifestPath, time.Now, randomID); err != nil {
		t.Fatal(err)
	}
	mismatched := bytes.Replace(fixture(t, "golden-snapshot.json"), []byte(`"amount": "778"`), []byte(`"amount": "779"`), 1)
	goldenPath := filepath.Join(t.TempDir(), "mismatched-golden.json")
	if err := os.WriteFile(goldenPath, mismatched, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshotSHA, _, err := hashFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := readJSONMap(t, manifestPath)
	manifest["expected_snapshot_sha256"] = snapshotSHA
	manifest["verification_receipt"].(map[string]any)["candidate_snapshot_sha256"] = snapshotSHA
	tamperedManifestPath := filepath.Join(t.TempDir(), "mismatched-golden-manifest.json")
	writeJSONFile(t, tamperedManifestPath, manifest)

	err = run([]string{"verify-restore", "-db", backup, "-golden", goldenPath, "-manifest", tamperedManifestPath})
	if err == nil {
		t.Fatal("mismatched restore snapshot was accepted")
	}
	if got, want := err.Error(), "restored snapshot does not match golden"; got != want {
		t.Fatal("restore mismatch error was not redacted")
	}
	for _, forbidden := range []string{`"cash"`, "778", "AAPL", "78.6", "event_cash_001", "receipt_golden_001"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("restore mismatch error leaked %q", forbidden)
		}
	}
}

func TestCreateBackupDiscardsFailedCandidate(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	backup := filepath.Join(t.TempDir(), "failed-candidate.db")
	manifest := backup + ".manifest.json"

	if _, err := createBackup(svc.db, backup, fixturePath("golden-snapshot.json"), manifest, time.Now, randomID); err == nil {
		t.Fatal("backup unexpectedly matched a non-empty golden snapshot")
	}
	for _, path := range []string{backup, manifest} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed backup artifact was not discarded: path=%s err=%v", path, err)
		}
	}
}

func TestBackupManifestContractFieldsMatchRuntimeAndFixtures(t *testing.T) {
	var schema map[string]any
	contract, err := os.ReadFile(filepath.Join("..", "..", "contracts", "backup-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contract, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	if properties["format_version"].(map[string]any)["const"] != "omni-folio-backup.v11" ||
		properties["schema_version"].(map[string]any)["const"] != "omni-folio.sqlite.v17" {
		t.Fatal("backup contract version drifted from the runtime")
	}

	var runtimeManifest, golden map[string]any
	runtimeJSON, err := json.Marshal(BackupManifest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(runtimeJSON, &runtimeManifest); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixture(t, "golden-backup-manifest.json"), &golden); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(jsonKeySet(runtimeManifest), jsonStringSet(schema["required"])) ||
		!reflect.DeepEqual(jsonKeySet(golden), jsonStringSet(schema["required"])) {
		t.Fatal("backup manifest schema, runtime fields, and golden fixture disagree")
	}
	const emptyV17PaperAccountingSHA = "35b3fd7f1273cf0a42f510f6a7536925488e0f195d26a006c74b520128482080"
	svc, _ := testService(t, nil, nil)
	emptyProof, err := provePaperAccountingRecovery(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if emptyProof.SHA256 != emptyV17PaperAccountingSHA ||
		golden["paper_accounting_state_sha256"] != emptyV17PaperAccountingSHA {
		t.Fatalf("golden manifest does not encode the empty v17 paper accounting proof: runtime=%s golden=%v", emptyProof.SHA256, golden["paper_accounting_state_sha256"])
	}
	if properties["paper_execution_authorization_count"].(map[string]any)["minimum"] != float64(0) {
		t.Fatal("schema v17 authorization count is not non-negative")
	}
	if properties["paper_capitalized_fill_count"].(map[string]any)["const"] != float64(0) {
		t.Fatal("schema v17 capitalized fill count is not reserved at exactly zero")
	}

	receiptSchema := schema["$defs"].(map[string]any)["verification_receipt"].(map[string]any)
	var runtimeReceipt map[string]any
	receiptJSON, err := json.Marshal(VerificationReceipt{})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(receiptJSON, &runtimeReceipt); err != nil {
		t.Fatal(err)
	}
	goldenReceipt := golden["verification_receipt"].(map[string]any)
	if !reflect.DeepEqual(jsonKeySet(runtimeReceipt), jsonStringSet(receiptSchema["required"])) ||
		!reflect.DeepEqual(jsonKeySet(goldenReceipt), jsonStringSet(receiptSchema["required"])) {
		t.Fatal("verification receipt schema, runtime fields, and golden fixture disagree")
	}
	if goldenReceipt["candidate_paper_accounting_state_sha256"] != emptyV17PaperAccountingSHA {
		t.Fatal("golden receipt candidate does not encode the empty v16 paper accounting proof")
	}

	var invalid map[string]any
	if err := json.Unmarshal(fixture(t, "invalid-backup-manifest.json"), &invalid); err != nil {
		t.Fatal(err)
	}
	rejected := invalid["verification_receipt"].(map[string]any)
	if rejected["status"] != "rejected" || rejected["eligible_for_activation"] != false || len(rejected["errors"].([]any)) == 0 {
		t.Fatal("invalid backup fixture no longer proves fail-closed activation")
	}
}

func TestInvalidAndDuplicatePreviewMatchesFixture(t *testing.T) {
	svc, _ := testService(t,
		[]time.Time{
			mustTime("2026-01-10T15:00:30Z"), mustTime("2026-01-10T15:01:00Z"), mustTime("2026-01-10T15:01:00.125Z"),
			mustTime("2026-01-10T15:02:00Z"),
		},
		map[string][]string{
			"event":   {"event_cash_001", "event_trade_001", "event_trade_002", "unused_duplicate", "unused_invalid"},
			"preview": {"preview_golden_001", "preview_edge_001"},
			"receipt": {"receipt_golden_001"},
		},
	)
	golden, appErr := svc.preview(context.Background(), fixture(t, "golden-import.csv"))
	if appErr != nil {
		t.Fatal(appErr)
	}
	_, appErr = svc.apply(context.Background(), ApplyRequest{golden.PreviewID, "apply_golden_001"})
	if appErr != nil {
		t.Fatal(appErr)
	}
	edge, appErr := svc.preview(context.Background(), fixture(t, "edge-import.csv"))
	if appErr != nil {
		t.Fatal(appErr)
	}
	assertJSONFixture(t, edge, "edge-preview.json")
	if _, appErr := svc.apply(context.Background(), ApplyRequest{edge.PreviewID, "edge"}); appErr == nil || appErr.status != http.StatusBadRequest || appErr.body.Code != "preview_has_errors" {
		t.Fatalf("expected preview_has_errors, got %#v", appErr)
	}
}

func TestIdempotencyReplayConflictAndStalePreview(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	ctx := context.Background()
	first, appErr := svc.preview(ctx, []byte(testCSV("d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,10")))
	if appErr != nil {
		t.Fatal(appErr)
	}
	req := ApplyRequest{first.PreviewID, "same-key"}
	receipt, appErr := svc.apply(ctx, req)
	if appErr != nil {
		t.Fatal(appErr)
	}
	replay, appErr := svc.apply(ctx, req)
	if appErr != nil || !reflect.DeepEqual(receipt, replay) {
		t.Fatalf("replay differs: %#v %#v %v", receipt, replay, appErr)
	}
	other, appErr := svc.preview(ctx, []byte(testCSV("d2,account-main,DEPOSIT,2026-01-02T00:00:00Z,,,,,USD,5")))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(ctx, ApplyRequest{other.PreviewID, "same-key"}); appErr == nil || appErr.status != http.StatusConflict || appErr.body.Code != "idempotency_conflict" {
		t.Fatalf("expected idempotency conflict, got %#v", appErr)
	}

	stale, appErr := svc.preview(ctx, []byte(testCSV("d3,account-main,DEPOSIT,2026-01-03T00:00:00Z,,,,,USD,5")))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(ctx, ApplyRequest{other.PreviewID, "other-key"}); appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(ctx, ApplyRequest{stale.PreviewID, "stale-key"}); appErr == nil || appErr.status != http.StatusConflict || appErr.body.Code != "stale_preview" {
		t.Fatalf("expected stale preview, got %#v", appErr)
	}

	expired, appErr := svc.preview(ctx, []byte(testCSV("d4,account-main,DEPOSIT,2026-01-04T00:00:00Z,,,,,USD,5")))
	if appErr != nil {
		t.Fatal(appErr)
	}
	svc.ttl = -time.Second
	if _, err := svc.db.Exec(`UPDATE previews SET expires_at=? WHERE preview_id=?`, zeroTime, expired.PreviewID); err != nil {
		t.Fatal(err)
	}
	if _, appErr := svc.apply(ctx, ApplyRequest{expired.PreviewID, "expired-key"}); appErr == nil || appErr.status != http.StatusConflict || appErr.body.Code != "stale_preview" {
		t.Fatalf("expected expired stale preview, got %#v", appErr)
	}
}

func TestApplyRollbackHasNoPartialMutation(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	preview, appErr := svc.preview(context.Background(), []byte(testCSV(
		"d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,100",
		"s1,account-main,SELL,2026-01-02T00:00:00Z,AAPL,1,20,1,USD,19",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(context.Background(), ApplyRequest{preview.PreviewID, "rollback"}); appErr == nil || appErr.body.Code != "invalid_ledger" {
		t.Fatalf("expected invalid_ledger, got %#v", appErr)
	}
	var events, revision int
	if err := svc.db.QueryRow(`SELECT count(*) FROM events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT revision FROM ledger_meta`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if events != 0 || revision != 0 {
		t.Fatalf("partial mutation: events=%d revision=%d", events, revision)
	}
}

func TestFIFOAllocatesBuyAndSellFeesExactly(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	preview, appErr := svc.preview(context.Background(), []byte(testCSV(
		"d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,1000",
		"b1,account-main,BUY,2026-01-02T00:00:00Z,AAPL,3,10,3,USD,-33",
		"b2,account-main,BUY,2026-01-03T00:00:00Z,AAPL,2,20,2,USD,-42",
		"s1,account-main,SELL,2026-01-04T00:00:00Z,AAPL,4,30,4,USD,116",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(context.Background(), ApplyRequest{preview.PreviewID, "fifo"}); appErr != nil {
		t.Fatal(appErr)
	}
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Cash, []Money{{"USD", "1041"}}) ||
		!reflect.DeepEqual(snapshot.Holdings, []Holding{{"instrument_aapl", "AAPL", "1", "21", "USD"}}) ||
		!reflect.DeepEqual(snapshot.RealizedPnL, []Money{{"USD", "62"}}) {
		t.Fatalf("unexpected FIFO snapshot: %+v", snapshot)
	}
}

func TestRecurringFIFOAllocationUsesVersionedResidualPolicy(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "recurring-fifo-first", testCSV(
		"b1,account-main,BUY,2026-01-02T00:00:00Z,AAPL,3,0.3,0.1,USD,-1",
		"s1,account-main,SELL,2026-01-03T00:00:00Z,AAPL,1,1,0,USD,1",
	))
	assertRecurringFIFOSnapshot(t, svc, []Holding{{"instrument_aapl", "AAPL", "2", "0.66666667", "USD"}}, "0.66666667")

	applyTestCSV(t, svc, "recurring-fifo-second", testCSV(
		"s2,account-main,SELL,2026-01-04T00:00:00Z,AAPL,1,1,0,USD,1",
	))
	assertRecurringFIFOSnapshot(t, svc, []Holding{{"instrument_aapl", "AAPL", "1", "0.333333335", "USD"}}, "1.333333335")

	applyTestCSV(t, svc, "recurring-fifo-final", testCSV(
		"s3,account-main,SELL,2026-01-05T00:00:00Z,AAPL,1,1,0,USD,1",
	))
	assertRecurringFIFOSnapshot(t, svc, []Holding{}, "2")
}

func assertRecurringFIFOSnapshot(t *testing.T, svc *Service, holdings []Holding, realized string) {
	t.Helper()
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"cost_basis_policy":"fifo_exact_else_half_even_residual_8_v1"`) ||
		!reflect.DeepEqual(snapshot.Holdings, holdings) ||
		!reflect.DeepEqual(snapshot.RealizedPnL, []Money{{"USD", realized}}) {
		t.Fatalf("recurring FIFO policy drifted: %s", encoded)
	}
}

func TestFIFOQuantizationUsesHalfEvenAndCannotExceedTinyLotCost(t *testing.T) {
	for _, test := range []struct {
		value, want string
	}{
		{"1.234567845", "1.23456784"},
		{"1.234567855", "1.23456786"},
		{"-1.234567845", "-1.23456784"},
	} {
		value, err := parseDecimal(test.value)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := formatDecimal(quantizeHalfEven(value, 8)); err != nil || got != test.want {
			t.Fatalf("half-even %s: got=%q err=%v want=%q", test.value, got, err, test.want)
		}
	}

	cost, _ := parseDecimal("0.000000006")
	take, _ := parseDecimal("6")
	quantity, _ := parseDecimal("7")
	allocation, err := fifoCostAllocation(cost, take, quantity)
	formatted, formatErr := formatDecimal(allocation)
	if err != nil || formatErr != nil || formatted != "0.000000005" || allocation.Sign() < 0 || allocation.Cmp(cost) > 0 {
		t.Fatalf("tiny partial lot over-allocated: allocation=%v cost=%v err=%v", allocation, cost, err)
	}

	exactCost, _ := parseDecimal("1")
	exactTake, _ := parseDecimal("1")
	exactQuantity, _ := parseDecimal("2048")
	exactAllocation, err := fifoCostAllocation(exactCost, exactTake, exactQuantity)
	exactFormatted, formatErr := formatDecimal(exactAllocation)
	if err != nil || formatErr != nil || exactFormatted != "0.00048828125" {
		t.Fatalf("previously exact FIFO allocation changed: allocation=%v err=%v", exactAllocation, err)
	}
}

func TestFIFOResidualConservesCostAcrossSplitAndMultipleLots(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "fifo-split-multi-lot", testCSV(
		"b1,account-main,BUY,2026-01-01T00:00:00Z,AAPL,3,0.3,0.1,USD,-1",
		"b2,account-main,BUY,2026-01-02T00:00:00Z,AAPL,7,0.1,0.3,USD,-1",
		"split,account-main,SPLIT,2026-01-03T00:00:00Z,AAPL,2,,,USD,0",
		"s1,account-main,SELL,2026-01-04T00:00:00Z,AAPL,8,1,0,USD,8",
		"s2,account-main,SELL,2026-01-05T00:00:00Z,AAPL,12,1,0,USD,12",
	))
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Holdings) != 0 || !reflect.DeepEqual(snapshot.RealizedPnL, []Money{{"USD", "18"}}) {
		t.Fatalf("split/multi-lot cost was not conserved: %+v", snapshot)
	}
}

func TestOpenAPIPinsAnalyticalFIFOAllocationPolicy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	snapshot := document["components"].(map[string]any)["schemas"].(map[string]any)["PortfolioSnapshot"].(map[string]any)
	policy := snapshot["properties"].(map[string]any)["cost_basis_policy"].(map[string]any)
	required := snapshot["required"].([]any)
	if !containsJSONText(required, "cost_basis_policy") || policy["const"] != fifoCostBasisPolicy ||
		!strings.Contains(policy["description"].(string), "not a jurisdictional tax-basis claim") {
		t.Fatal("PortfolioSnapshot does not pin the analytical FIFO policy")
	}
}

func TestCashFlowsAndSplitReplayExactly(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	preview, appErr := svc.preview(context.Background(), []byte(testCSV(
		"d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,1000",
		"b1,account-main,BUY,2026-01-02T00:00:00Z,AAPL,10,10,2,USD,-102",
		"v1,account-main,DIVIDEND,2026-01-03T00:00:00Z,AAPL,,,,USD,5",
		"t1,account-main,TAX,2026-01-03T00:01:00Z,,,,,USD,-1",
		"s1,account-main,SPLIT,2026-01-04T00:00:00Z,AAPL,2,,,USD,0",
		"x1,account-main,SELL,2026-01-05T00:00:00Z,AAPL,5,12,1,USD,59",
		"f1,account-main,FEE,2026-01-05T00:01:00Z,,,,,USD,-2",
		"w1,account-main,WITHDRAWAL,2026-01-06T00:00:00Z,,,,,USD,-100",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(context.Background(), ApplyRequest{preview.PreviewID, "cash-flow-split"}); appErr != nil {
		t.Fatal(appErr)
	}
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Cash, []Money{{"USD", "859"}}) ||
		!reflect.DeepEqual(snapshot.Holdings, []Holding{{"instrument_aapl", "AAPL", "15", "76.5", "USD"}}) ||
		!reflect.DeepEqual(snapshot.RealizedPnL, []Money{{"USD", "33.5"}}) {
		t.Fatalf("unexpected cash-flow/split snapshot: %+v", snapshot)
	}
}

func TestExtendedLedgerTypesRejectInvalidCashDirection(t *testing.T) {
	for _, row := range []string{
		"w1,account-main,WITHDRAWAL,2026-01-01T00:00:00Z,,,,,USD,1",
		"v1,account-main,DIVIDEND,2026-01-01T00:00:00Z,AAPL,,,,USD,-1",
		"f1,account-main,FEE,2026-01-01T00:00:00Z,,,,,USD,1",
		"t1,account-main,TAX,2026-01-01T00:00:00Z,,,,,USD,1",
		"s1,account-main,SPLIT,2026-01-01T00:00:00Z,AAPL,2,,,USD,1",
	} {
		t.Run(strings.SplitN(row, ",", 2)[0], func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			preview, appErr := svc.preview(context.Background(), []byte(testCSV(row)))
			if appErr != nil {
				t.Fatal(appErr)
			}
			if preview.CanApply || preview.Totals.ErrorRows != 1 || preview.Rows[0].Errors[0].Code != "invalid_amount" {
				t.Fatalf("invalid cash direction was accepted: %+v", preview)
			}
		})
	}
}

func TestSplitWithoutOpenHoldingRollsBack(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	preview, appErr := svc.preview(context.Background(), []byte(testCSV(
		"s1,account-main,SPLIT,2026-01-01T00:00:00Z,AAPL,2,,,USD,0",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(context.Background(), ApplyRequest{preview.PreviewID, "split-without-holding"}); appErr == nil || appErr.body.Code != "invalid_ledger" {
		t.Fatalf("expected invalid_ledger, got %#v", appErr)
	}
	var events, revision int
	if err := svc.db.QueryRow(`SELECT count(*) FROM events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT revision FROM ledger_meta`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if events != 0 || revision != 0 {
		t.Fatalf("partial split mutation: events=%d revision=%d", events, revision)
	}
}

func TestSchemaV5RejectsInvalidExtendedLedgerRows(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	for _, row := range []struct {
		name, typ, instrument, symbol, quantity, price, fee, amount string
	}{
		{"positive withdrawal", "WITHDRAWAL", "", "", "", "", "", "1"},
		{"negative dividend", "DIVIDEND", "instrument_aapl", "AAPL", "", "", "", "-1"},
		{"cash-changing split", "SPLIT", "instrument_aapl", "AAPL", "2", "", "", "1"},
	} {
		t.Run(row.name, func(t *testing.T) {
			_, err := svc.db.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,instrument_id,symbol,quantity,price,fee,currency,amount,receipt_id,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				"event_"+row.name, "source_"+row.name, "account-main", row.typ, "2026-01-01T00:00:00Z",
				nullable(row.instrument), nullable(row.symbol), nullable(row.quantity), nullable(row.price), nullable(row.fee), "USD", row.amount, "receipt", "2026-01-01T00:00:00Z")
			if err == nil {
				t.Fatalf("schema accepted invalid %s row", row.typ)
			}
		})
	}
}

func TestHTTPBodyAndStructuredErrors(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/imports/apply", strings.NewReader(`{"preview_id":"x","idempotency_key":"y","extra":true}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("financial API response was cacheable: %v", w.Header())
	}
	var apiErr APIError
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil || apiErr.Code != "invalid_json" || apiErr.Message == "" {
		t.Fatalf("unstructured error: %s (%v)", w.Body.String(), err)
	}
}

func TestInternalErrorsAreLoggedButNotExposed(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	if _, err := svc.db.Exec(`DROP TABLE ledger_meta`); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousOutput) })

	r := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var apiErr APIError
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
		t.Fatal(err)
	}
	if apiErr != (APIError{Code: "internal_error", Message: "internal server error"}) {
		t.Fatalf("unstable internal response: %+v", apiErr)
	}
	if strings.Contains(w.Body.String(), "ledger_meta") || strings.Contains(w.Body.String(), "no such table") {
		t.Fatalf("internal details leaked: %s", w.Body.String())
	}
	if !strings.Contains(logs.String(), "ledger_meta") {
		t.Fatalf("internal cause was not logged: %s", logs.String())
	}
}

func TestHealthAndReadinessAreSeparate(t *testing.T) {
	svc, _ := testService(t, nil, nil)

	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	if w := request("/healthz"); w.Code != http.StatusOK || w.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("health status=%d body=%s", w.Code, w.Body.String())
	}
	if w := request("/readyz"); w.Code != http.StatusOK || w.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("ready status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := svc.db.Exec(`DELETE FROM schema_migrations WHERE version=1`); err != nil {
		t.Fatal(err)
	}
	if w := request("/readyz"); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"code":"not_ready"`) {
		t.Fatalf("readiness accepted an incomplete migration history: status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := svc.db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(1, ?)`, "2026-01-10T15:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(18, ?)`, "2026-01-10T15:01:00Z"); err != nil {
		t.Fatal(err)
	}
	if w := request("/readyz"); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"code":"not_ready"`) {
		t.Fatalf("unready status=%d body=%s", w.Code, w.Body.String())
	}
	if w := request("/healthz"); w.Code != http.StatusOK {
		t.Fatalf("process health depended on schema: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCORSAllowsOnlyConfiguredExactOrigin(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	const allowed = "http://localhost:7357"
	handler := withCORS(svc.routes(), allowed)

	request := func(method, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/v1/imports/apply", nil)
		r.Header.Set("Origin", origin)
		if method == http.MethodOptions {
			r.Header.Set("Access-Control-Request-Method", http.MethodPost)
			r.Header.Set("Access-Control-Request-Headers", "content-type")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	preflight := request(http.MethodOptions, allowed)
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Origin") != allowed ||
		preflight.Header().Get("Access-Control-Allow-Methods") != "GET, POST, OPTIONS" ||
		preflight.Header().Get("Access-Control-Allow-Headers") != "Content-Type" {
		t.Fatalf("allowed preflight failed: status=%d headers=%v", preflight.Code, preflight.Header())
	}
	allowedRequest := request(http.MethodPost, allowed)
	if allowedRequest.Header().Get("Access-Control-Allow-Origin") != allowed {
		t.Fatalf("configured origin omitted: %v", allowedRequest.Header())
	}
	for _, denied := range []string{"http://localhost:7357/", "http://127.0.0.1:7357", "https://evil.example"} {
		response := request(http.MethodOptions, denied)
		if response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("origin %q was allowed: %v", denied, response.Header())
		}
	}
	defaultHandler := withCORS(svc.routes(), "")
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Header.Set("Origin", allowed)
	w := httptest.NewRecorder()
	defaultHandler.ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("default widened CORS access: %v", w.Header())
	}
}

func TestAllowedOriginValidation(t *testing.T) {
	for _, valid := range []string{"", "http://localhost:7357", "https://app.example"} {
		if err := validateAllowedOrigin(valid); err != nil {
			t.Fatalf("valid origin %q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{"*", "localhost:7357", "ftp://localhost", "http://user@localhost", "http://localhost/path", "http://localhost?x=1"} {
		if err := validateAllowedOrigin(invalid); err == nil {
			t.Fatalf("invalid origin %q accepted", invalid)
		}
	}
}

func TestStatusUnresolvedLimitsAndFingerprintBinding(t *testing.T) {
	svc, _ := testService(t, nil, nil)

	r := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"trust_state":"never_verified"`) || !strings.Contains(w.Body.String(), `"last_verified_at":null`) {
		t.Fatalf("unexpected first-launch status: %s", w.Body.String())
	}

	unresolvedPreview, appErr := svc.preview(context.Background(), []byte(testCSV(
		"u1,unknown-account,BUY,2026-01-01T00:00:00Z,MSFT,1,10,0,USD,-10",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if unresolvedPreview.CanApply || unresolvedPreview.Totals.UnresolvedRows != 1 || unresolvedPreview.Rows[0].Status != "unresolved" || unresolvedPreview.Rows[0].Resolution == nil {
		t.Fatalf("unresolved row did not fail closed: %+v", unresolvedPreview)
	}

	tooMany := make([]string, maxImportRows+1)
	for i := range tooMany {
		tooMany[i] = "x,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,1"
	}
	if _, appErr := svc.preview(context.Background(), []byte(testCSV(tooMany...))); appErr == nil || appErr.body.Code != "too_many_rows" {
		t.Fatalf("expected row cap, got %#v", appErr)
	}
	if _, appErr := svc.preview(context.Background(), bytes.Repeat([]byte("x"), maxBodyBytes+1)); appErr == nil || appErr.body.Code != "csv_too_large" {
		t.Fatalf("expected byte cap, got %#v", appErr)
	}

	valid, appErr := svc.preview(context.Background(), []byte(testCSV(
		"d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,10",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if valid.PreviewFingerprint != previewFingerprint(valid.FileSHA256, csvSchema, mappingSchema, valid.LedgerRevision) {
		t.Fatal("preview fingerprint did not bind contract versions")
	}
	var stored string
	if err := svc.db.QueryRow(`SELECT preview_json FROM previews WHERE preview_id=?`, valid.PreviewID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	stored = strings.Replace(stored, `"mapping_version":"canonical-transaction.v4"`, `"mapping_version":"changed.v5"`, 1)
	if _, err := svc.db.Exec(`UPDATE previews SET preview_json=? WHERE preview_id=?`, stored, valid.PreviewID); err != nil {
		t.Fatal(err)
	}
	if _, appErr := svc.apply(context.Background(), ApplyRequest{valid.PreviewID, "tampered"}); appErr == nil || appErr.status != http.StatusConflict || appErr.body.Code != "stale_preview" {
		t.Fatalf("expected fingerprint conflict, got %#v", appErr)
	}
}

func testService(t *testing.T, times []time.Time, ids map[string][]string) (*Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "core.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	clockIndex := 0
	clock := func() time.Time {
		if len(times) == 0 {
			return mustTime("2026-01-10T15:01:00Z").Add(time.Duration(clockIndex) * time.Millisecond)
		}
		if clockIndex >= len(times) {
			return times[len(times)-1].Add(time.Duration(clockIndex-len(times)+1) * time.Millisecond)
		}
		v := times[clockIndex]
		clockIndex++
		return v
	}
	counters := map[string]int{}
	id := func(prefix string) string {
		i := counters[prefix]
		counters[prefix]++
		if i < len(ids[prefix]) {
			return ids[prefix][i]
		}
		return prefix + "_test_" + time.Unix(0, int64(i)).Format("150405.000000000")
	}
	return newService(db, clock, id), path
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(fixturePath(name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixturePath(name string) string { return filepath.Join("..", "..", "contracts", "fixtures", name) }

func jsonKeySet(value map[string]any) map[string]bool {
	result := make(map[string]bool, len(value))
	for key := range value {
		result[key] = true
	}
	return result
}

func jsonStringSet(value any) map[string]bool {
	items := value.([]any)
	result := make(map[string]bool, len(items))
	for _, item := range items {
		result[item.(string)] = true
	}
	return result
}

func decodeFixture(t *testing.T, name string, dst any) {
	t.Helper()
	if err := json.Unmarshal(fixture(t, name), dst); err != nil {
		t.Fatal(err)
	}
}

func assertJSONFixture(t *testing.T, actual any, name string) {
	t.Helper()
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var actualValue, expectedValue any
	if err := json.Unmarshal(actualJSON, &actualValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixture(t, name), &expectedValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actualValue, expectedValue) {
		prettyActual := &bytes.Buffer{}
		_ = json.Indent(prettyActual, actualJSON, "", "  ")
		t.Fatalf("does not match %s\nactual: %s", name, prettyActual)
	}
}

func testCSV(rows ...string) string {
	return "source_event_id,account_id,type,occurred_at,symbol,quantity,price,fee,currency,amount\n" + strings.Join(rows, "\n") + "\n"
}

func mustTime(raw string) time.Time {
	v, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		panic(err)
	}
	return v
}
