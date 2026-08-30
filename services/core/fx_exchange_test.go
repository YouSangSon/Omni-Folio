package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFXExchangePreviewApplyReplayAndBackupRestore(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "fx-seed", testCSV("deposit,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,200"))

	preview, appErr := svc.preview(context.Background(), []byte(fxCSV(
		"fx-1,account-main,FX_EXCHANGE,2026-01-02T00:00:00Z,,,,,USD,-100,KRW,137000,",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if !preview.CanApply || preview.MappingVersion != "canonical-transaction.v4" || preview.Totals.NewRows != 1 {
		t.Fatalf("FX preview was not applicable: %+v", preview)
	}
	fx := preview.Rows[0].Transaction
	if fx == nil || fx.Currency != "USD" || fx.Amount != "-100" || fx.CounterCurrency != "KRW" || fx.CounterAmount != "137000" {
		t.Fatalf("FX counter leg was not preserved: %+v", fx)
	}
	if _, appErr := svc.apply(context.Background(), ApplyRequest{PreviewID: preview.PreviewID, IdempotencyKey: "fx-apply"}); appErr != nil {
		t.Fatal(appErr)
	}
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	wantCash := []Money{{Currency: "KRW", Amount: "137000"}, {Currency: "USD", Amount: "100"}}
	if !reflect.DeepEqual(snapshot.Cash, wantCash) || len(snapshot.Holdings) != 0 || len(snapshot.RealizedPnL) != 0 {
		t.Fatalf("FX replay drifted: got=%+v want_cash=%+v", snapshot, wantCash)
	}

	replay, appErr := svc.preview(context.Background(), []byte(fxCSV(
		"fx-1,account-main,FX_EXCHANGE,2026-01-02T00:00:00Z,,,,,USD,-100,KRW,137000,",
	)))
	if appErr != nil || !replay.CanApply || replay.Rows[0].Status != "duplicate" {
		t.Fatalf("exact FX replay was not idempotent: preview=%+v err=%v", replay, appErr)
	}
	conflict, appErr := svc.preview(context.Background(), []byte(fxCSV(
		"fx-1,account-main,FX_EXCHANGE,2026-01-02T00:00:00Z,,,,,USD,-100,KRW,136999,",
	)))
	if appErr != nil || conflict.CanApply || conflict.Rows[0].Errors[0].Code != "source_event_conflict" {
		t.Fatalf("changed FX counter leg was silently deduplicated: preview=%+v err=%v", conflict, appErr)
	}

	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "fx.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v9" || manifest.SchemaVersion != "omni-folio.sqlite.v14" {
		t.Fatalf("FX backup versions drifted: %+v", manifest)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}
	candidate, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`PRAGMA writable_schema=ON`); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`UPDATE sqlite_master SET sql=replace(sql, 'counter_currency <> currency', '1') WHERE type='table' AND name='events'`); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`PRAGMA writable_schema=OFF`); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err == nil {
		t.Fatal("restore accepted an events table without the distinct-currency FX guard")
	}
}

func TestFXExchangeRejectsInvalidLegs(t *testing.T) {
	tests := []string{
		"fx,account-main,FX_EXCHANGE,2026-01-02T00:00:00Z,,,,,USD,-100,,137000,",
		"fx,account-main,FX_EXCHANGE,2026-01-02T00:00:00Z,,,,,USD,-100,KRW,,",
		"fx,account-main,FX_EXCHANGE,2026-01-02T00:00:00Z,,,,,USD,-100,USD,100,",
		"fx,account-main,FX_EXCHANGE,2026-01-02T00:00:00Z,,,,,USD,100,KRW,137000,",
		"fx,account-main,FX_EXCHANGE,2026-01-02T00:00:00Z,,,,,USD,-0,KRW,137000,",
		"fx,account-main,FX_EXCHANGE,2026-01-02T00:00:00Z,,,,,USD,-100,KRW,-137000,",
		"fx,account-main,FX_EXCHANGE,2026-01-02T00:00:00Z,AAPL,,,,USD,-100,KRW,137000,",
		"fx,account-main,FX_EXCHANGE,2026-01-02T00:00:00Z,,,,,USD,-100.0,KRW,137000,",
		"fx,account-main,FX_EXCHANGE,2026-01-02T00:00:00Z,,,,,USD,-100,KRW,137000,target",
		"deposit,account-main,DEPOSIT,2026-01-02T00:00:00Z,,,,,USD,100,KRW,137000,",
	}
	for index, row := range tests {
		t.Run(fmt.Sprintf("case-%d", index), func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			preview, appErr := svc.preview(context.Background(), []byte(fxCSV(row)))
			if appErr != nil {
				t.Fatal(appErr)
			}
			if preview.CanApply || preview.Totals.ErrorRows != 1 || preview.Rows[0].Status != "error" {
				t.Fatalf("invalid FX row was accepted: %+v", preview)
			}
		})
	}
}

func TestFXExchangeSchemaGuard(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	statements := []string{
		`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,currency,amount,receipt_id,recorded_at) VALUES('missing','missing','account-main','FX_EXCHANGE','2026-01-01T00:00:00Z','USD','-1','receipt','2026-01-01T00:00:00Z')`,
		`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,currency,amount,counter_currency,counter_amount,receipt_id,recorded_at) VALUES('same','same','account-main','FX_EXCHANGE','2026-01-01T00:00:00Z','USD','-1','USD','1','receipt','2026-01-01T00:00:00Z')`,
		`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,currency,amount,counter_currency,counter_amount,receipt_id,recorded_at) VALUES('source-sign','source-sign','account-main','FX_EXCHANGE','2026-01-01T00:00:00Z','USD','1','KRW','1','receipt','2026-01-01T00:00:00Z')`,
		`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,currency,amount,counter_currency,counter_amount,receipt_id,recorded_at) VALUES('source-zero','source-zero','account-main','FX_EXCHANGE','2026-01-01T00:00:00Z','USD','-0','KRW','1','receipt','2026-01-01T00:00:00Z')`,
		`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,currency,amount,counter_currency,counter_amount,receipt_id,recorded_at) VALUES('counter-sign','counter-sign','account-main','FX_EXCHANGE','2026-01-01T00:00:00Z','USD','-1','KRW','-1','receipt','2026-01-01T00:00:00Z')`,
		`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,currency,amount,counter_currency,counter_amount,receipt_id,recorded_at) VALUES('non-fx','non-fx','account-main','DEPOSIT','2026-01-01T00:00:00Z','USD','1','KRW','1','receipt','2026-01-01T00:00:00Z')`,
	}
	for _, statement := range statements {
		if _, err := svc.db.Exec(statement); err == nil {
			t.Fatalf("schema accepted invalid FX shape: %s", statement)
		}
	}
}

func TestOpenAPIExposesClosedFXExchangeContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	transaction := document["components"].(map[string]any)["schemas"].(map[string]any)["NormalizedTransaction"].(map[string]any)
	properties := transaction["properties"].(map[string]any)
	types := properties["type"].(map[string]any)["enum"].([]any)
	conditions, err := json.Marshal(transaction["allOf"])
	if err != nil {
		t.Fatal(err)
	}
	if transaction["additionalProperties"] != false || !containsJSONText(types, "FX_EXCHANGE") ||
		properties["counter_currency"] == nil || properties["counter_amount"] == nil ||
		!strings.Contains(string(conditions), `"const":"FX_EXCHANGE"`) ||
		!strings.Contains(string(conditions), `"required":["counter_currency","counter_amount"]`) {
		t.Fatal("NormalizedTransaction does not expose the closed FX counter-leg contract")
	}
}

func TestVerifyManifestMigratesV8BackupCopy(t *testing.T) {
	db, backup, golden, manifestPath := legacyV8Backup(t)
	verifyLegacyBackupCopy(t, db, 8, backup, golden, manifestPath)
}

func TestVerifyManifestMigratesV9BackupCopy(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	golden := writeCurrentSnapshot(t, svc.db)
	downgradePaperEvaluationForTest(t, svc.db)
	if _, err := svc.db.Exec(`DROP TRIGGER instrument_listing_events_no_update; DROP TRIGGER instrument_listing_events_no_delete; DROP TRIGGER instrument_listing_events_state_guard; DROP TABLE instrument_listing_events; DELETE FROM schema_migrations WHERE version=13; DROP TRIGGER security_price_observations_no_update; DROP TRIGGER security_price_observations_no_delete; DROP TABLE security_price_observations; DELETE FROM schema_migrations WHERE version IN (11,12); DROP TRIGGER fx_observations_no_update; DROP TRIGGER fx_observations_no_delete; DROP TABLE fx_observations; DELETE FROM schema_migrations WHERE version=10`); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "legacy-v9.db")
	if _, err := svc.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(backup, "'", "''") + `'`); err != nil {
		t.Fatal(err)
	}
	manifestPath := writeLegacyV5Manifest(t, svc.db, backup, golden, "omni-folio.sqlite.v9")
	verifyLegacyBackupCopy(t, svc.db, 9, backup, golden, manifestPath)
	mixed := readJSONMap(t, manifestPath)
	mixed["fx_observation_state_sha256"] = strings.Repeat("0", 64)
	mixed["fx_observation_count"] = float64(0)
	receipt := mixed["verification_receipt"].(map[string]any)
	receipt["fx_observation_check"] = "ok"
	receipt["candidate_fx_observation_state_sha256"] = strings.Repeat("0", 64)
	mixedPath := filepath.Join(t.TempDir(), "mixed-v5-v6-manifest.json")
	writeJSONFile(t, mixedPath, mixed)
	if err := verifyManifest(backup, golden, mixedPath); err == nil {
		t.Fatal("legacy manifest with v6-only FX fields was accepted")
	}
}

func verifyLegacyBackupCopy(t *testing.T, db *sql.DB, wantVersion int, backup, golden, manifestPath string) {
	t.Helper()
	temporaryBefore, err := filepath.Glob(filepath.Join(os.TempDir(), "omni-folio-restore-legacy.*"))
	if err != nil {
		t.Fatal(err)
	}
	beforeSHA, beforeSize, err := hashFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}
	afterSHA, afterSize, err := hashFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if beforeSHA != afterSHA || beforeSize != afterSize {
		t.Fatal("legacy backup was modified during verification")
	}
	temporaryAfter, err := filepath.Glob(filepath.Join(os.TempDir(), "omni-folio-restore-legacy.*"))
	if err != nil || !reflect.DeepEqual(temporaryAfter, temporaryBefore) {
		t.Fatalf("legacy verification left a temporary candidate: before=%v after=%v err=%v", temporaryBefore, temporaryAfter, err)
	}
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != wantVersion {
		t.Fatalf("legacy source database was migrated in place: version=%d err=%v", version, err)
	}
}

func legacyV8Backup(t *testing.T) (*sql.DB, string, string, string) {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "source-v8.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	files := []string{"001_init.sql", "002_orders.sql", "003_broker_snapshots.sql", "004_execution_authority.sql", "005_ledger_events.sql", "006_strategy_registry.sql", "007_paper_orders.sql", "008_cash_void.sql"}
	for index, name := range files {
		if index == 6 {
			if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
				t.Fatal(err)
			}
		}
		script, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(script)); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, index+1, "2026-01-01T00:00:00Z"); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if index == 6 {
			if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec(`INSERT INTO events(sequence,event_id,source_event_id,account_id,type,occurred_at,currency,amount,receipt_id,recorded_at) VALUES(1,'legacy-event','legacy-source','account-main','DEPOSIT','2026-01-02T00:00:00Z','USD','10','legacy-receipt','2026-01-02T00:01:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE ledger_meta SET revision=1, recorded_at='2026-01-02T00:01:00Z', last_verified_at='2026-01-02T00:01:00Z' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	backup := filepath.Join(dir, "legacy-v8.db")
	if _, err := db.Exec(`VACUUM INTO '` + strings.ReplaceAll(backup, "'", "''") + `'`); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join(dir, "legacy-golden.json")
	writeJSONFile(t, golden, &PortfolioSnapshot{
		PortfolioID: "portfolio_main", LedgerRevision: "rev_0000000001", CostBasisPolicy: fifoCostBasisPolicy, AsOf: "2026-01-02T00:00:00Z", RecordedAt: "2026-01-02T00:01:00Z",
		LiveEnabled: false, ValuationStatus: "unavailable", Cash: []Money{{Currency: "USD", Amount: "10"}}, Holdings: []Holding{}, RealizedPnL: []Money{},
		Provenance: Provenance{EventIDs: []string{"legacy-event"}, ReceiptIDs: []string{"legacy-receipt"}},
	})

	manifestPath := writeLegacyV5Manifest(t, db, backup, golden, "omni-folio.sqlite.v8")
	return db, backup, golden, manifestPath
}

func writeLegacyV5Manifest(t *testing.T, db *sql.DB, backup, golden, schema string) string {
	t.Helper()
	orders, err := proveOrderRecovery(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	broker, err := proveBrokerRecovery(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	strategy, err := proveStrategyRegistryRecovery(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	dbSHA, size, err := hashFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	snapshotSHA, _, err := hashFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	var sourceRevision int64
	if err := db.QueryRow(`SELECT revision FROM ledger_meta WHERE singleton=1`).Scan(&sourceRevision); err != nil {
		t.Fatal(err)
	}
	manifest := &BackupManifest{
		FormatVersion: "omni-folio-backup.v5", SchemaVersion: schema, CreatedAt: "2026-01-02T00:02:00Z", SourceLedgerRevision: revision(sourceRevision),
		OrderStateSHA256: orders.SHA256, OrderCount: orders.Orders, OrderEventCount: orders.Events,
		ExecutionAuthoritySHA256: orders.ExecutionAuthoritySHA256, ExecutionAuthorityEventCount: orders.ExecutionAuthorityEvents,
		RiskReservationSHA256: orders.RiskReservationSHA256, RiskReservationCount: orders.RiskReservations,
		BrokerStateSHA256: broker.SHA256, BrokerSnapshotCount: broker.Snapshots, BrokerReconciliationCount: broker.Reconciliations,
		StrategyRegistrySHA256: strategy.SHA256, StrategyEvidenceCount: strategy.Evidence, StrategySelectionEventCount: strategy.Events, SelectedStrategyResultSHA256: strategy.SelectedResultSHA256,
		DBSHA256: dbSHA, SizeBytes: size, ExpectedSnapshotSHA256: snapshotSHA, Encryption: BackupEncryption{Encrypted: false, Algorithm: "none"},
		VerificationReceipt: VerificationReceipt{
			ReceiptID: "legacy-verification", CandidateID: "legacy-candidate", VerifiedAt: "2026-01-02T00:02:00Z", Status: "verified",
			IntegrityCheck: "ok", GoldenSnapshotCheck: "ok", OrderStateCheck: "ok", BrokerStateCheck: "ok", StrategyRegistryCheck: "ok",
			CandidateDBSHA256: dbSHA, CandidateSnapshotSHA256: snapshotSHA, CandidateOrderStateSHA256: orders.SHA256,
			CandidateBrokerStateSHA256: broker.SHA256, CandidateStrategyRegistrySHA256: strategy.SHA256, EligibleForActivation: true, Errors: []string{},
		},
	}
	manifestPath := backup + ".manifest.json"
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "fx_observation_state_sha256")
	delete(legacy, "fx_observation_count")
	delete(legacy, "security_price_observation_state_sha256")
	delete(legacy, "security_price_observation_count")
	delete(legacy, "instrument_listing_state_sha256")
	delete(legacy, "paper_evaluation_event_count")
	delete(legacy, "instrument_listing_event_count")
	delete(legacy, "active_instrument_listing_count")
	receipt := legacy["verification_receipt"].(map[string]any)
	delete(receipt, "fx_observation_check")
	delete(receipt, "candidate_fx_observation_state_sha256")
	delete(receipt, "security_price_observation_check")
	delete(receipt, "candidate_security_price_observation_state_sha256")
	delete(receipt, "instrument_listing_check")
	delete(receipt, "candidate_instrument_listing_state_sha256")
	writeJSONFile(t, manifestPath, legacy)
	return manifestPath
}

func fxCSV(rows ...string) string {
	return "source_event_id,account_id,type,occurred_at,symbol,quantity,price,fee,currency,amount,counter_currency,counter_amount,corrects_source_event_id\n" + strings.Join(rows, "\n") + "\n"
}
