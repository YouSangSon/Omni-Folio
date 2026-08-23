package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
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
	if first.SnapshotID == "" || first.SnapshotSHA256 == "" || first.LedgerRevision != "rev_0000000002" ||
		first.AllPositionsMatch || !reflect.DeepEqual(first.PositionDifferences, wantDiffs) {
		t.Fatalf("unexpected reconciliation: %+v", first)
	}

	replayed, err := svc.recordKiwoomSnapshot(ctx, "account-main", snapshot)
	if err != nil || !reflect.DeepEqual(first, replayed) {
		t.Fatalf("idempotent replay changed result: first=%+v replay=%+v err=%v", first, replayed, err)
	}
	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM broker_snapshots`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("replay created duplicate snapshots: count=%d err=%v", count, err)
	}

	latest, err := svc.latestKiwoomSnapshot(ctx, KiwoomMock, KiwoomKRX, snapshot.AccountRef)
	if err != nil || !reflect.DeepEqual(first, latest) {
		t.Fatalf("latest known-good snapshot differs: got=%+v err=%v", latest, err)
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
	if err != nil || !reflect.DeepEqual(first, latest) {
		t.Fatalf("failed sync replaced last known-good: got=%+v err=%v", latest, err)
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
	if _, err := svc.db.Exec(`DROP TRIGGER broker_snapshots_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE broker_snapshots SET record_sha256=? WHERE snapshot_id=?`, strings.Repeat("0", 64), stored.SnapshotID); err != nil {
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
	if manifest.FormatVersion != "omni-folio-backup.v3" || manifest.SchemaVersion != "omni-folio.sqlite.v3" ||
		manifest.BrokerSnapshotCount != 1 || manifest.BrokerStateSHA256 == "" ||
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
	if _, err := candidate.Exec(`DROP TRIGGER broker_snapshots_no_delete`); err != nil {
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
	if _, err := svc.db.Exec(`UPDATE broker_snapshots SET record_sha256=? WHERE snapshot_id=?`, strings.Repeat("0", 64), stored.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := proveBrokerRecovery(context.Background(), svc.db); err == nil {
		t.Fatal("broker snapshot with a mismatched durable hash was certified")
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
