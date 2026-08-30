package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestG4HKnownGoodSnapshotPersistsAndDiffsLedger(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	seedKRXLedger(t, svc)
	ctx := context.Background()
	snapshot := g4hSnapshot("2026-01-10T15:00:59Z")

	first, err := svc.recordKiwoomSnapshot(ctx, "account-main", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	wantDiffs := []BrokerPositionDifference{
		{Symbol: "000660", BrokerQuantity: "2", LedgerQuantity: "0", Difference: "2", Match: false},
		{Symbol: "005930", BrokerQuantity: "10", LedgerQuantity: "7", Difference: "3", Match: false},
	}
	if first.SnapshotID == "" || first.ReconciliationID == "" || first.SnapshotSHA256 == "" || first.LedgerRevision != "rev_0000000002" ||
		first.AllPositionsMatch || !reflect.DeepEqual(first.PositionDifferences, wantDiffs) {
		t.Fatalf("unexpected reconciliation: %+v", first)
	}

	replayed, err := svc.recordKiwoomSnapshot(ctx, "account-main", snapshot)
	if err != nil || !reflect.DeepEqual(first, replayed) {
		t.Fatalf("idempotent replay changed result: first=%+v replay=%+v err=%v", first, replayed, err)
	}
	var snapshots, reconciliations int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM broker_snapshots`).Scan(&snapshots); err != nil || snapshots != 1 {
		t.Fatalf("replay created duplicate snapshots: count=%d err=%v", snapshots, err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM broker_snapshot_reconciliations`).Scan(&reconciliations); err != nil || reconciliations != 1 {
		t.Fatalf("replay created duplicate reconciliations: count=%d err=%v", reconciliations, err)
	}

	advanceKRXLedger(t, svc)
	refreshed, err := svc.recordKiwoomSnapshot(ctx, "account-main", snapshot)
	wantRefreshedDiffs := []BrokerPositionDifference{
		{Symbol: "000660", BrokerQuantity: "2", LedgerQuantity: "0", Difference: "2", Match: false},
		{Symbol: "005930", BrokerQuantity: "10", LedgerQuantity: "10", Difference: "0", Match: true},
	}
	if err != nil || refreshed.SnapshotID != first.SnapshotID || refreshed.ReconciliationID == first.ReconciliationID ||
		refreshed.LedgerRevision != "rev_0000000003" || refreshed.AllPositionsMatch ||
		!reflect.DeepEqual(refreshed.PositionDifferences, wantRefreshedDiffs) {
		t.Fatalf("ledger revision did not create a fresh reconciliation: first=%+v refreshed=%+v err=%v", first, refreshed, err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM broker_snapshots`).Scan(&snapshots); err != nil || snapshots != 1 {
		t.Fatalf("ledger refresh duplicated raw snapshot: count=%d err=%v", snapshots, err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM broker_snapshot_reconciliations`).Scan(&reconciliations); err != nil || reconciliations != 2 {
		t.Fatalf("ledger refresh did not append reconciliation: count=%d err=%v", reconciliations, err)
	}
	replayed, err = svc.recordKiwoomSnapshot(ctx, "account-main", snapshot)
	if err != nil || !reflect.DeepEqual(refreshed, replayed) {
		t.Fatalf("same snapshot and ledger revision changed result: refreshed=%+v replay=%+v err=%v", refreshed, replayed, err)
	}

	latest, err := svc.latestKiwoomSnapshot(ctx, KiwoomMock, KiwoomKRX, snapshot.AccountRef)
	if err != nil || !reflect.DeepEqual(refreshed, latest) {
		t.Fatalf("latest known-good reconciliation differs: got=%+v err=%v", latest, err)
	}

	conflict := *snapshot
	conflict.Positions = append([]KiwoomPosition(nil), snapshot.Positions...)
	conflict.Positions[1].Quantity = "11"
	if _, err := svc.recordKiwoomSnapshot(ctx, "account-main", &conflict); err == nil {
		t.Fatal("same fetched_at with changed snapshot was accepted")
	}
	incomplete := *snapshot
	incomplete.FetchedAt = "2026-01-10T15:01:00Z"
	incomplete.Complete = false
	if _, err := svc.recordKiwoomSnapshot(ctx, "account-main", &incomplete); err == nil {
		t.Fatal("incomplete snapshot was persisted")
	}
	invalidID := *snapshot
	invalidID.FetchedAt = "2026-01-10T15:01:01Z"
	svc.id = func(string) string { return "" }
	if _, err := svc.recordKiwoomSnapshot(ctx, "account-main", &invalidID); err == nil {
		t.Fatal("snapshot with an invalid generated ID was persisted")
	}
	latest, err = svc.latestKiwoomSnapshot(ctx, KiwoomMock, KiwoomKRX, snapshot.AccountRef)
	if err != nil || !reflect.DeepEqual(refreshed, latest) {
		t.Fatalf("failed sync replaced last known-good: got=%+v err=%v", latest, err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM broker_snapshots`).Scan(&snapshots); err != nil || snapshots != 1 {
		t.Fatalf("failed sync mutated raw snapshots: count=%d err=%v", snapshots, err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM broker_snapshot_reconciliations`).Scan(&reconciliations); err != nil || reconciliations != 2 {
		t.Fatalf("failed sync mutated reconciliations: count=%d err=%v", reconciliations, err)
	}
}

func TestG4KLatestBrokerReconciliationHTTPIsSanitized(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	seedKRXLedger(t, svc)
	if _, err := svc.recordKiwoomSnapshot(context.Background(), "account-main", g4hSnapshot("2026-01-10T15:00:59Z")); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/broker-reconciliation/latest", nil))
	const want = "{\"provider\":\"kiwoom\",\"environment\":\"mock\",\"exchange\":\"KRX\",\"freshness\":\"unverified\",\"fetched_at\":\"2026-01-10T15:00:59Z\",\"recorded_at\":\"2026-01-10T15:01:00Z\",\"ledger_revision\":\"rev_0000000002\",\"all_positions_match\":false,\"position_differences\":[{\"symbol\":\"000660\",\"broker_quantity\":\"2\",\"ledger_quantity\":\"0\",\"difference\":\"2\",\"match\":false},{\"symbol\":\"005930\",\"broker_quantity\":\"10\",\"ledger_quantity\":\"7\",\"difference\":\"3\",\"match\":false}]}\n"
	if w.Code != http.StatusOK || w.Body.String() != want {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestG4KLatestBrokerReconciliationHTTPDistinguishesMissingFromCorrupt(t *testing.T) {
	request := func(t *testing.T, svc *Service) (int, string) {
		t.Helper()
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/broker-reconciliation/latest", nil))
		return w.Code, w.Body.String()
	}
	t.Run("missing", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		status, body := request(t, svc)
		if status != http.StatusNotFound || body != "{\"code\":\"broker_reconciliation_not_found\",\"message\":\"broker reconciliation was not found\"}\n" {
			t.Fatalf("status=%d body=%s", status, body)
		}
	})
	t.Run("corrupt", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		seedKRXLedger(t, svc)
		stored, err := svc.recordKiwoomSnapshot(context.Background(), "account-main", g4hSnapshot("2026-01-10T15:00:59Z"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`DROP TRIGGER broker_snapshot_reconciliations_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE broker_snapshot_reconciliations SET record_sha256=? WHERE reconciliation_id=?`, strings.Repeat("0", 64), stored.ReconciliationID); err != nil {
			t.Fatal(err)
		}
		status, body := request(t, svc)
		if status != http.StatusInternalServerError || body != "{\"code\":\"internal_error\",\"message\":\"internal server error\"}\n" {
			t.Fatalf("status=%d body=%s", status, body)
		}
	})
	t.Run("newest snapshot has no reconciliation", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		seedKRXLedger(t, svc)
		if _, err := svc.recordKiwoomSnapshot(context.Background(), "account-main", g4hSnapshot("2026-01-10T15:00:59Z")); err != nil {
			t.Fatal(err)
		}
		orphan := g4hSnapshot("2026-01-10T15:01:01Z")
		raw, sha, err := orderJSONHash(orphan)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`INSERT INTO broker_snapshots(snapshot_id,provider,environment,exchange,account_ref,fetched_at,snapshot_sha256,snapshot_json,recorded_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			"broker_snapshot_orphan", "kiwoom", orphan.Environment, orphan.Exchange, orphan.AccountRef, orphan.FetchedAt, sha, string(raw), "2026-01-10T15:01:01Z"); err != nil {
			t.Fatal(err)
		}
		status, body := request(t, svc)
		if status != http.StatusInternalServerError || body != "{\"code\":\"internal_error\",\"message\":\"internal server error\"}\n" {
			t.Fatalf("status=%d body=%s", status, body)
		}
	})
}

func TestG4KOpenAPIExposesOnlyTheSanitizedReadModel(t *testing.T) {
	raw, err := os.ReadFile("../../contracts/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	paths := document["paths"].(map[string]any)
	path, ok := paths["/v1/broker-reconciliation/latest"].(map[string]any)
	if !ok {
		t.Fatal("broker reconciliation path is missing")
	}
	operation, ok := path["get"].(map[string]any)
	if !ok {
		t.Fatal("broker reconciliation path is missing")
	}
	response := operation["responses"].(map[string]any)["200"].(map[string]any)
	schemaRef := response["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"]
	if schemaRef != "#/components/schemas/BrokerReconciliation" {
		t.Fatalf("unexpected response schema: %v", schemaRef)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	schema := schemas["BrokerReconciliation"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	for _, forbidden := range []string{"snapshot_id", "reconciliation_id", "account_ref", "ledger_account_id", "snapshot_sha256", "snapshot"} {
		if _, found := properties[forbidden]; found {
			t.Fatalf("public schema exposes %s", forbidden)
		}
	}
	if schema["additionalProperties"] != false {
		t.Fatal("broker reconciliation schema must be closed")
	}
}

func TestG4HConcurrentReplayAcrossSQLiteHandles(t *testing.T) {
	svc, path := testService(t, nil, nil)
	seedKRXLedger(t, svc)
	secondDB, err := openExistingDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondDB.Close() })
	second := newService(secondDB, func() time.Time { return mustTime("2026-01-10T15:01:01Z") }, func(prefix string) string { return prefix + "_parallel_second" })
	snapshot := g4hSnapshot("2026-01-10T15:00:59Z")

	start := make(chan struct{})
	results := make([]*BrokerKnownGoodSnapshot, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, service := range []*Service{svc, second} {
		wg.Add(1)
		go func(index int, service *Service) {
			defer wg.Done()
			<-start
			results[index], errs[index] = service.recordKiwoomSnapshot(context.Background(), "account-main", snapshot)
		}(i, service)
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || !reflect.DeepEqual(results[0], results[1]) {
		t.Fatalf("concurrent replay diverged: first=%+v second=%+v errors=%v", results[0], results[1], errs)
	}
	var snapshots, reconciliations int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM broker_snapshots`).Scan(&snapshots); err != nil || snapshots != 1 {
		t.Fatalf("concurrent replay duplicated snapshots: count=%d err=%v", snapshots, err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM broker_snapshot_reconciliations`).Scan(&reconciliations); err != nil || reconciliations != 1 {
		t.Fatalf("concurrent replay duplicated reconciliations: count=%d err=%v", reconciliations, err)
	}
}

func TestG4HSnapshotRowsAndLedgerEventsAreInsertOnly(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	seedKRXLedger(t, svc)
	stored, err := svc.recordKiwoomSnapshot(context.Background(), "account-main", g4hSnapshot("2026-01-10T15:00:59Z"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE broker_snapshots SET snapshot_id=snapshot_id WHERE snapshot_id='` + stored.SnapshotID + `'`,
		`DELETE FROM broker_snapshots WHERE snapshot_id='` + stored.SnapshotID + `'`,
		`UPDATE broker_snapshot_reconciliations SET reconciliation_id=reconciliation_id WHERE reconciliation_id='` + stored.ReconciliationID + `'`,
		`DELETE FROM broker_snapshot_reconciliations WHERE reconciliation_id='` + stored.ReconciliationID + `'`,
		`UPDATE events SET event_id=event_id WHERE account_id='account-main'`,
		`DELETE FROM events WHERE account_id='account-main'`,
	} {
		if _, err := svc.db.Exec(statement); err == nil {
			t.Fatalf("insert-only storage accepted mutation: %s", statement)
		}
	}
	if _, err := proveBrokerRecovery(context.Background(), svc.db); err != nil {
		t.Fatal("insert-only checks damaged broker recovery:", err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER broker_snapshot_reconciliations_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE broker_snapshot_reconciliations SET record_sha256=? WHERE reconciliation_id=?`, strings.Repeat("0", 64), stored.ReconciliationID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.latestKiwoomSnapshot(context.Background(), KiwoomMock, KiwoomKRX, stored.AccountRef); err == nil {
		t.Fatal("latest snapshot accepted a mismatched durable record hash")
	}
	if _, err := proveBrokerRecovery(context.Background(), svc.db); err == nil {
		t.Fatal("broker recovery certified a mismatched durable record hash")
	}
}

func TestG4HBackupRestoresBrokerSnapshotProof(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	seedKRXLedger(t, svc)
	stored, err := svc.recordKiwoomSnapshot(context.Background(), "account-main", g4hSnapshot("2026-01-10T15:00:59Z"))
	if err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "backup.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath,
		func() time.Time { return mustTime("2026-01-10T15:03:00Z") },
		func(prefix string) string { return prefix + "_g4h_test" },
	)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v12" || manifest.SchemaVersion != "omni-folio.sqlite.v18" ||
		manifest.BrokerSnapshotCount != 1 || manifest.BrokerReconciliationCount != 1 || manifest.BrokerStateSHA256 == "" ||
		manifest.VerificationReceipt.BrokerStateCheck != "ok" ||
		manifest.VerificationReceipt.CandidateBrokerStateSHA256 != manifest.BrokerStateSHA256 {
		t.Fatalf("backup omitted broker snapshot recovery proof: %+v", manifest)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}

	restoredDB, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	restored := newService(restoredDB, time.Now, randomID)
	latest, err := restored.latestKiwoomSnapshot(context.Background(), KiwoomMock, KiwoomKRX, stored.AccountRef)
	closeErr := restoredDB.Close()
	if err != nil || closeErr != nil || !reflect.DeepEqual(stored, latest) {
		t.Fatalf("backup lost known-good broker snapshot: got=%+v err=%v close=%v", latest, err, closeErr)
	}

	tampered := readJSONMap(t, manifestPath)
	tampered["broker_state_sha256"] = strings.Repeat("0", 64)
	tamperedPath := filepath.Join(t.TempDir(), "tampered-manifest.json")
	writeJSONFile(t, tamperedPath, tampered)
	if err := verifyManifest(backup, golden, tamperedPath); err == nil {
		t.Fatal("manifest with a different broker state digest was accepted")
	}
	candidate, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`DROP TRIGGER broker_snapshot_reconciliations_no_delete`); err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`CREATE TRIGGER broker_snapshot_reconciliations_no_delete
		BEFORE DELETE ON broker_snapshot_reconciliations WHEN 0
		BEGIN SELECT RAISE(ABORT, 'broker_snapshot_reconciliations is insert-only'); END`); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err == nil {
		t.Fatal("restore accepted a broker snapshot table without insert-only protection")
	}
}

func TestG4HBrokerRecoveryRejectsCorruptRows(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	seedKRXLedger(t, svc)
	stored, err := svc.recordKiwoomSnapshot(context.Background(), "account-main", g4hSnapshot("2026-01-10T15:00:59Z"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER broker_snapshots_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE broker_snapshots SET snapshot_sha256=? WHERE snapshot_id=?`, strings.Repeat("0", 64), stored.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := proveBrokerRecovery(context.Background(), svc.db); err == nil {
		t.Fatal("broker snapshot with a mismatched durable hash was certified")
	}
}

func TestG4HLatestRejectsReconciliationMetadataMismatch(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	seedKRXLedger(t, svc)
	stored, err := svc.recordKiwoomSnapshot(context.Background(), "account-main", g4hSnapshot("2026-01-10T15:00:59Z"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER broker_snapshot_reconciliations_no_update`); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := svc.db.QueryRow(`SELECT record_json FROM broker_snapshot_reconciliations WHERE reconciliation_id=?`, stored.ReconciliationID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var record brokerReconciliationRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatal(err)
	}
	record.SnapshotID = "broker_snapshot_different"
	encoded, hash, err := orderJSONHash(&record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE broker_snapshot_reconciliations SET record_sha256=?,record_json=? WHERE reconciliation_id=?`, hash, string(encoded), stored.ReconciliationID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.latestKiwoomSnapshot(context.Background(), KiwoomMock, KiwoomKRX, stored.AccountRef); err == nil {
		t.Fatal("latest snapshot accepted reconciliation JSON bound to another snapshot")
	}
}

func TestG4HBrokerRecoveryRejectsSnapshotWithoutReconciliation(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	seedKRXLedger(t, svc)
	stored, err := svc.recordKiwoomSnapshot(context.Background(), "account-main", g4hSnapshot("2026-01-10T15:00:59Z"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER broker_snapshot_reconciliations_no_delete`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DELETE FROM broker_snapshot_reconciliations WHERE reconciliation_id=?`, stored.ReconciliationID); err != nil {
		t.Fatal(err)
	}
	if _, err := proveBrokerRecovery(context.Background(), svc.db); err == nil {
		t.Fatal("broker recovery certified a snapshot without reconciliation evidence")
	}
}

func advanceKRXLedger(t *testing.T, svc *Service) {
	t.Helper()
	tx, err := svc.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,instrument_id,symbol,quantity,price,fee,currency,amount,receipt_id,recorded_at)
		VALUES('g4h-buy-more','buy-005930-more','account-main','BUY','2026-01-03T00:00:00Z','instrument_005930','005930','3','1000','0','KRW','-3000','g4h-receipt-2','2026-01-10T15:00:30Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE ledger_meta SET revision=3,recorded_at='2026-01-10T15:00:30Z' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func seedKRXLedger(t *testing.T, svc *Service) {
	t.Helper()
	tx, err := svc.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, args := range [][]any{
		{"g4h-cash", "cash-krw", "DEPOSIT", "2026-01-01T00:00:00Z", nil, nil, nil, nil, "100000", "g4h-receipt"},
		{"g4h-buy", "buy-005930", "BUY", "2026-01-02T00:00:00Z", "005930", "7", "1000", "0", "-7000", "g4h-receipt"},
	} {
		if _, err := tx.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,instrument_id,symbol,quantity,price,fee,currency,amount,receipt_id,recorded_at)
			VALUES(?,?,'account-main',?,?,CASE WHEN ? IS NULL THEN NULL ELSE 'instrument_' || ? END,?,?,?,?, 'KRW',?,?,?)`,
			args[0], args[1], args[2], args[3], args[4], args[4], args[4], args[5], args[6], args[7], args[8], args[9], "2026-01-10T15:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`UPDATE ledger_meta SET revision=2,recorded_at='2026-01-10T15:00:00Z' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func g4hSnapshot(fetchedAt string) *KiwoomSnapshot {
	return &KiwoomSnapshot{
		Source: "kiwoom", Environment: KiwoomMock, Exchange: KiwoomKRX,
		AccountRef: "kiwoom_account_AAAAAAAAAAAAAAAAAAAAAAAA", MaskedAccount: "******3210",
		FetchedAt: fetchedAt, Complete: true, Currency: "KRW",
		Totals: KiwoomTotals{
			PurchaseAmount: "10000", EvaluationAmount: "11000", UnrealizedPnL: "1000", ReturnRatePercent: "10",
			EstimatedAssets: "111000", LoanAmount: "0", CreditLoanAmount: "0", CreditLendingAmount: "0",
		},
		Positions: []KiwoomPosition{
			{Symbol: "000660", Name: "SK하이닉스", Quantity: "2", TradableQuantity: "2", AveragePurchasePrice: "1000", CurrentPrice: "1100", PurchaseAmount: "2000", EvaluationAmount: "2200", UnrealizedPnL: "200", ReturnRatePercent: "10", WeightPercent: "20"},
			{Symbol: "005930", Name: "삼성전자", Quantity: "10", TradableQuantity: "10", AveragePurchasePrice: "800", CurrentPrice: "880", PurchaseAmount: "8000", EvaluationAmount: "8800", UnrealizedPnL: "800", ReturnRatePercent: "10", WeightPercent: "80"},
		},
		OpenOrders: []KiwoomOpenOrder{},
	}
}
