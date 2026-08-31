package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestG38DBackupV14StrategyPerformanceProof(t *testing.T) {
	svc, asOf := g38c3CashPerformanceFixture(t)
	event, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf)
	if err != nil {
		t.Fatal(err)
	}
	strategyEvent, err := svc.evaluatePaperStrategyPerformance(context.Background(), k2aAccountRef,
		event.StrategySelectionEventID, event.PerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := provePaperPerformanceRecovery(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	strategyProof, err := provePaperStrategyPerformanceRecovery(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "paper-strategy-performance-v14.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v15" || manifest.SchemaVersion != "omni-folio.sqlite.v21" ||
		manifest.PaperPerformanceStateSHA256 != proof.SHA256 || manifest.PaperPerformanceEventCount != 1 ||
		manifest.PaperPerformanceMarkCount != event.MarkCount || manifest.VerificationReceipt.PaperPerformanceCheck != "ok" ||
		manifest.VerificationReceipt.CandidatePaperPerformanceStateSHA256 != proof.SHA256 ||
		manifest.PaperStrategyPerformanceStateSHA256 != strategyProof.SHA256 || manifest.PaperStrategyPerformanceEventCount != 1 ||
		manifest.PaperStrategyPerformanceSampleCount != strategyEvent.SampleCount ||
		manifest.VerificationReceipt.PaperStrategyPerformanceCheck != "ok" ||
		manifest.VerificationReceipt.CandidatePaperStrategyPerformanceStateSHA256 != strategyProof.SHA256 {
		t.Fatalf("v14 manifest omitted strategy performance proof: %+v", manifest)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"paper_performance_state_sha256", "paper_performance_event_count", "paper_performance_mark_count",
		"paper_strategy_performance_state_sha256", "paper_strategy_performance_event_count", "paper_strategy_performance_sample_count",
	} {
		tampered := readJSONMap(t, manifestPath)
		delete(tampered, field)
		path := filepath.Join(t.TempDir(), "missing-"+field+".json")
		writeJSONFile(t, path, tampered)
		if err := verifyManifest(backup, golden, path); err == nil {
			t.Fatalf("v14 manifest without %s was accepted", field)
		}
	}
	for _, field := range []string{
		"candidate_paper_performance_state_sha256", "paper_performance_check",
		"candidate_paper_strategy_performance_state_sha256", "paper_strategy_performance_check",
	} {
		tampered := readJSONMap(t, manifestPath)
		delete(tampered["verification_receipt"].(map[string]any), field)
		path := filepath.Join(t.TempDir(), "missing-"+field+".json")
		writeJSONFile(t, path, tampered)
		if err := verifyManifest(backup, golden, path); err == nil {
			t.Fatalf("v14 receipt without %s was accepted", field)
		}
	}
	for name, mutate := range map[string]func(map[string]any){
		"digest":      func(value map[string]any) { value["paper_performance_state_sha256"] = strings.Repeat("0", 64) },
		"event count": func(value map[string]any) { value["paper_performance_event_count"] = float64(2) },
		"mark count":  func(value map[string]any) { value["paper_performance_mark_count"] = float64(2) },
		"status": func(value map[string]any) {
			value["verification_receipt"].(map[string]any)["paper_performance_check"] = "failed"
		},
		"strategy digest":       func(value map[string]any) { value["paper_strategy_performance_state_sha256"] = strings.Repeat("0", 64) },
		"strategy event count":  func(value map[string]any) { value["paper_strategy_performance_event_count"] = float64(2) },
		"strategy sample count": func(value map[string]any) { value["paper_strategy_performance_sample_count"] = float64(2) },
		"strategy status": func(value map[string]any) {
			value["verification_receipt"].(map[string]any)["paper_strategy_performance_check"] = "failed"
		},
	} {
		t.Run(name, func(t *testing.T) {
			tampered := readJSONMap(t, manifestPath)
			mutate(tampered)
			path := filepath.Join(t.TempDir(), "forged.json")
			writeJSONFile(t, path, tampered)
			if err := verifyManifest(backup, golden, path); err == nil {
				t.Fatal("forged performance proof was eligible for activation")
			}
		})
	}
}

func TestG38C3LegacyV11OwnedCopyMigration(t *testing.T) {
	ctx := context.Background()
	svc, _ := testService(t, nil, nil)
	golden := writeCurrentSnapshot(t, svc.db)
	currentBackup := filepath.Join(t.TempDir(), "paper-performance-current.db")
	currentManifestPath := currentBackup + ".manifest.json"
	if _, err := createBackup(svc.db, currentBackup, golden, currentManifestPath, svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	downgradePaperPerformanceForTest(t, svc.db)
	legacyBackup := filepath.Join(t.TempDir(), "paper-performance-v11.db")
	if _, err := svc.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(legacyBackup, "'", "''") + `'`); err != nil {
		t.Fatal(err)
	}
	legacy := readJSONMap(t, currentManifestPath)
	legacy["format_version"], legacy["schema_version"] = "omni-folio-backup.v11", "omni-folio.sqlite.v17"
	delete(legacy, "paper_performance_state_sha256")
	delete(legacy, "paper_performance_event_count")
	delete(legacy, "paper_performance_mark_count")
	delete(legacy, "paper_strategy_performance_state_sha256")
	delete(legacy, "paper_strategy_performance_event_count")
	delete(legacy, "paper_strategy_performance_sample_count")
	receipt := legacy["verification_receipt"].(map[string]any)
	delete(receipt, "candidate_paper_performance_state_sha256")
	delete(receipt, "paper_performance_check")
	delete(receipt, "candidate_paper_strategy_performance_state_sha256")
	delete(receipt, "paper_strategy_performance_check")
	sha, size, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	legacy["db_sha256"], legacy["size_bytes"], receipt["candidate_db_sha256"] = sha, size, sha
	legacyManifestPath := filepath.Join(t.TempDir(), "paper-performance-v11.manifest.json")
	writeJSONFile(t, legacyManifestPath, legacy)
	manifestSHA, manifestSize, err := hashFile(legacyManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeSHA, beforeSize, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(legacyBackup, golden, legacyManifestPath); err != nil {
		t.Fatal(err)
	}
	afterSHA, afterSize, err := hashFile(legacyBackup)
	afterManifestSHA, afterManifestSize, manifestErr := hashFile(legacyManifestPath)
	if err != nil || manifestErr != nil || beforeSHA != afterSHA || beforeSize != afterSize ||
		manifestSHA != afterManifestSHA || manifestSize != afterManifestSize {
		t.Fatalf("v11 source artifacts changed: db=(%s,%d)->(%s,%d) manifest=(%s,%d)->(%s,%d) errors=(%v,%v)",
			beforeSHA, beforeSize, afterSHA, afterSize, manifestSHA, manifestSize, afterManifestSHA, afterManifestSize, err, manifestErr)
	}
	owned := filepath.Join(t.TempDir(), "paper-performance-v11-owned.db")
	if err := copyFile(legacyBackup, owned); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(owned)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	proof, proofErr := provePaperPerformanceRecovery(ctx, db)
	closeErr := db.Close()
	if proofErr != nil || closeErr != nil || proof.Events != 0 || proof.Marks != 0 ||
		proof.SHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("v11 owned-copy performance proof=%+v errors=(%v,%v)", proof, proofErr, closeErr)
	}
}

func TestG38DBackupLegacyV12OwnedCopyMigration(t *testing.T) {
	ctx := context.Background()
	svc, asOf := g38c3CashPerformanceFixture(t)
	if _, err := svc.evaluatePaperPerformance(ctx, k2aAccountRef, asOf); err != nil {
		t.Fatal(err)
	}
	performanceProof, err := provePaperPerformanceRecovery(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	currentBackup := filepath.Join(t.TempDir(), "paper-strategy-performance-current.db")
	currentManifestPath := currentBackup + ".manifest.json"
	if _, err := createBackup(svc.db, currentBackup, golden, currentManifestPath, svc.now, svc.id); err != nil {
		t.Fatal(err)
	}

	downgradePaperStrategyPerformanceForTest(t, svc.db)
	legacyBackup := filepath.Join(t.TempDir(), "paper-strategy-performance-v12.db")
	if _, err := svc.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(legacyBackup, "'", "''") + `'`); err != nil {
		t.Fatal(err)
	}
	legacy := readJSONMap(t, currentManifestPath)
	legacy["format_version"], legacy["schema_version"] = "omni-folio-backup.v12", "omni-folio.sqlite.v18"
	delete(legacy, "paper_strategy_performance_state_sha256")
	delete(legacy, "paper_strategy_performance_event_count")
	delete(legacy, "paper_strategy_performance_sample_count")
	receipt := legacy["verification_receipt"].(map[string]any)
	delete(receipt, "candidate_paper_strategy_performance_state_sha256")
	delete(receipt, "paper_strategy_performance_check")
	sha, size, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	legacy["db_sha256"], legacy["size_bytes"], receipt["candidate_db_sha256"] = sha, size, sha
	legacyManifestPath := filepath.Join(t.TempDir(), "paper-strategy-performance-v12.manifest.json")
	writeJSONFile(t, legacyManifestPath, legacy)
	beforeSHA, beforeSize, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(legacyBackup, golden, legacyManifestPath); err != nil {
		t.Fatal(err)
	}
	afterSHA, afterSize, err := hashFile(legacyBackup)
	if err != nil || beforeSHA != afterSHA || beforeSize != afterSize {
		t.Fatalf("v12 source changed during owned-copy migration: before=(%s,%d) after=(%s,%d) err=%v", beforeSHA, beforeSize, afterSHA, afterSize, err)
	}
	owned := filepath.Join(t.TempDir(), "paper-strategy-performance-v12-owned.db")
	if err := copyFile(legacyBackup, owned); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(owned)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	ownedPerformance, performanceErr := provePaperPerformanceRecovery(ctx, db)
	ownedStrategy, strategyErr := provePaperStrategyPerformanceRecovery(ctx, db)
	closeErr := db.Close()
	if performanceErr != nil || strategyErr != nil || closeErr != nil || ownedPerformance != performanceProof ||
		ownedStrategy.Events != 0 || ownedStrategy.Samples != 0 ||
		ownedStrategy.SHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("v12 owned-copy proofs performance=%+v strategy=%+v errors=(%v,%v,%v)", ownedPerformance, ownedStrategy, performanceErr, strategyErr, closeErr)
	}
}

func downgradePaperPerformanceForTest(t testing.TB, db *sql.DB) {
	t.Helper()
	downgradePaperStrategyPerformanceForTest(t, db)
	if _, err := db.Exec(`DROP TRIGGER paper_performance_events_state_guard;
		DROP TRIGGER paper_performance_events_no_update;
		DROP TRIGGER paper_performance_events_no_delete;
		DROP INDEX paper_performance_events_account_idx;
		DROP TABLE paper_performance_events;
		DELETE FROM schema_migrations WHERE version=18`); err != nil {
		t.Fatal(err)
	}
}

func downgradePaperStrategyPerformanceForTest(t testing.TB, db *sql.DB) {
	t.Helper()
	var currentVersion int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&currentVersion); err != nil {
		t.Fatal(err)
	}
	if currentVersion >= 20 {
		downgradePaperPerformancePolicyForTest(t, db)
	}
	if _, err := db.Exec(`DROP TRIGGER IF EXISTS paper_strategy_performance_events_state_guard;
		DROP TRIGGER IF EXISTS paper_strategy_performance_events_no_update;
		DROP TRIGGER IF EXISTS paper_strategy_performance_events_no_delete;
		DROP INDEX IF EXISTS paper_strategy_performance_events_window_idx;
		DROP TABLE IF EXISTS paper_strategy_performance_events;
		DELETE FROM schema_migrations WHERE version=19`); err != nil {
		t.Fatal(err)
	}
}

func TestK2ABackupProvesOrderRecoveryState(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	unknown := mustReadyAndDispatchK2AOrder(t, svc, "client-backup-unknown", "backup-unknown")
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "backup.db")
	manifestPath := backup + ".manifest.json"

	manifest, err := createBackup(svc.db, backup, golden, manifestPath,
		func() time.Time { return mustTime("2026-01-10T15:03:00Z") },
		func(prefix string) string { return prefix + "_backup_test" },
	)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v15" || manifest.SchemaVersion != "omni-folio.sqlite.v21" {
		t.Fatalf("backup did not declare the order-aware schema: %+v", manifest)
	}
	if manifest.OrderStateSHA256 == "" || manifest.OrderCount != 1 || manifest.OrderEventCount != 3 ||
		manifest.ExecutionAuthorityEventCount != 2 || manifest.RiskReservationCount != 1 {
		t.Fatalf("backup omitted order recovery proof: %+v", manifest)
	}
	if manifest.VerificationReceipt.OrderStateCheck != "ok" ||
		manifest.VerificationReceipt.CandidateOrderStateSHA256 != manifest.OrderStateSHA256 {
		t.Fatalf("restore receipt omitted order recovery proof: %+v", manifest.VerificationReceipt)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}

	restoredDB, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	restored := newService(restoredDB, time.Now, randomID)
	state, err := restored.loadOrderState(context.Background(), unknown.OrderID)
	if closeErr := restoredDB.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil || state.Status != "SUBMIT_UNKNOWN" || state.PendingAction != "SUBMIT" {
		t.Fatalf("backup lost unknown-submit recovery: state=%+v err=%v", state, err)
	}

	tampered := readJSONMap(t, manifestPath)
	tampered["order_state_sha256"] = strings.Repeat("0", 64)
	tamperedPath := filepath.Join(t.TempDir(), "tampered-manifest.json")
	writeJSONFile(t, tamperedPath, tampered)
	if err := verifyManifest(backup, golden, tamperedPath); err == nil {
		t.Fatal("manifest with a different order state digest was accepted")
	}
}

func TestK2ARestoreRejectsMissingOrderSchemaOrProtection(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	mustReadyAndDispatchK2AOrder(t, svc, "client-backup-safety", "backup-safety")
	golden := writeCurrentSnapshot(t, svc.db)

	v1Path := filepath.Join(t.TempDir(), "v1.db")
	v1, err := openDB(v1Path)
	if err != nil {
		t.Fatal(err)
	}
	script, err := migrationFiles.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v1.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	if _, err := v1.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(1, ?)`, "2026-01-10T15:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := v1.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(v1Path, golden); err == nil {
		t.Fatal("schema v1 candidate was eligible for order-aware restore")
	}

	backup := filepath.Join(t.TempDir(), "missing-trigger.db")
	if _, err := createBackup(svc.db, backup, golden, backup+".manifest.json", time.Now, randomID); err != nil {
		t.Fatal(err)
	}
	candidate, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`DROP TRIGGER order_events_no_delete`); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err == nil {
		t.Fatal("candidate without insert-only order protection was eligible for restore")
	}
}

func TestK2ABackupRejectsCorruptOrderRows(t *testing.T) {
	t.Run("missing order storage", func(t *testing.T) {
		db, err := openDB(filepath.Join(t.TempDir(), "unmigrated.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := proveOrderRecovery(context.Background(), db); err == nil {
			t.Fatal("database without order storage was certified")
		}
	})

	t.Run("malformed intent", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		if _, err := svc.db.Exec(`INSERT INTO order_idempotency(provider,mode,account_ref,client_order_id,request_sha256,order_id,intent_json,recorded_at) VALUES(?,?,?,?,?,?,?,?)`,
			"kiwoom", "synthetic", k2aAccountRef, "client-malformed-intent", strings.Repeat("0", 64), "order_malformed_intent", "{", "2026-01-10T15:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("malformed durable order intent was certified")
		}
	})

	t.Run("intent hash mismatch", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		intent := k2aIntent("client-corrupt-intent")
		intentJSON, _, err := orderJSONHash(intent)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`INSERT INTO order_idempotency(provider,mode,account_ref,client_order_id,request_sha256,order_id,intent_json,recorded_at) VALUES(?,?,?,?,?,?,?,?)`,
			intent.Provider, intent.Mode, intent.AccountRef, intent.ClientOrderID, strings.Repeat("0", 64), "order_corrupt_intent", string(intentJSON), "2026-01-10T15:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("order intent with a mismatched durable hash was certified")
		}
	})

	t.Run("orphan event", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		event := k2aEvent("orphan-event", "order_orphan", "INTENT_RECORDED")
		eventJSON, eventSHA, err := orderJSONHash(event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`INSERT INTO order_events(event_id,event_sha256,order_id,event_type,source,event_json,recorded_at) VALUES(?,?,?,?,?,?,?)`,
			event.EventID, eventSHA, event.OrderID, event.Type, event.Source, string(eventJSON), "2026-01-10T15:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("order event without a durable intent was certified")
		}
	})

	t.Run("event hash mismatch", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		state := mustRecordK2AOrder(t, svc, "client-corrupt-event")
		if _, err := svc.db.Exec(`DROP TRIGGER order_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE order_events SET event_sha256=? WHERE order_id=?`, strings.Repeat("0", 64), state.OrderID); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("order event with a mismatched durable hash was certified")
		}
	})

	t.Run("malformed event", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		state := mustRecordK2AOrder(t, svc, "client-malformed-event")
		if _, err := svc.db.Exec(`DROP TRIGGER order_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE order_events SET event_json='{' WHERE order_id=?`, state.OrderID); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("malformed durable order event was certified")
		}
	})

	t.Run("missing event storage", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		for _, name := range []string{"order_events_no_update", "order_events_no_delete"} {
			if _, err := svc.db.Exec(`DROP TRIGGER ` + name); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := svc.db.Exec(`DROP TABLE order_events`); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("database without order event storage was certified")
		}
	})

	t.Run("invalid event replay", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		state := mustRecordK2AOrder(t, svc, "client-invalid-replay")
		event := k2aEvent("duplicate-intent-event", state.OrderID, "INTENT_RECORDED")
		eventJSON, eventSHA, err := orderJSONHash(event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`INSERT INTO order_events(event_id,event_sha256,order_id,event_type,source,event_json,recorded_at) VALUES(?,?,?,?,?,?,?)`,
			event.EventID, eventSHA, event.OrderID, event.Type, event.Source, string(eventJSON), "2026-01-10T15:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("non-replayable order event sequence was certified")
		}
	})
}

func TestG38C1PaperAccountingBackupProofAndV9OwnedCopyMigration(t *testing.T) {
	ctx := context.Background()
	svc, _ := testService(t, nil, nil)
	evidence, selected := selectedPaperStrategy(t, svc)
	if _, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID); err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "paper-accounting-v10.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v15" || manifest.SchemaVersion != "omni-folio.sqlite.v21" ||
		manifest.PaperAccountingSessionCount != 1 || manifest.PaperAccountingStateSHA256 == "" ||
		manifest.VerificationReceipt.CandidatePaperAccountingStateSHA256 != manifest.PaperAccountingStateSHA256 {
		t.Fatalf("backup omitted paper accounting proof: %+v", manifest)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}
	for _, remove := range []string{"paper_accounting_state_sha256", "paper_accounting_session_count"} {
		tampered := readJSONMap(t, manifestPath)
		delete(tampered, remove)
		path := filepath.Join(t.TempDir(), "missing-"+remove+".manifest.json")
		writeJSONFile(t, path, tampered)
		if err := verifyManifest(backup, golden, path); err == nil {
			t.Fatalf("current manifest without %s was accepted", remove)
		}
	}
	tamperedReceipt := readJSONMap(t, manifestPath)
	delete(tamperedReceipt["verification_receipt"].(map[string]any), "candidate_paper_accounting_state_sha256")
	missingReceiptPath := filepath.Join(t.TempDir(), "missing-paper-accounting-receipt.manifest.json")
	writeJSONFile(t, missingReceiptPath, tamperedReceipt)
	if err := verifyManifest(backup, golden, missingReceiptPath); err == nil {
		t.Fatal("current manifest without candidate paper accounting digest was accepted")
	}

	legacySvc, _ := testService(t, nil, nil)
	legacyEvidence, legacySelection := selectedPaperStrategy(t, legacySvc)
	legacySignal := paperEvaluationSignal(legacyEvidence.ResultSHA256, legacySelection.CurrentEventID, "paper-accounting-v9-order")
	downgradePaperAuthorizationForTest(t, legacySvc.db)
	if _, err := legacySvc.recordOrderIntent(ctx, paperOrderIntent(k2aAccountRef, legacySignal, "1", "1000")); err != nil {
		t.Fatal(err)
	}
	if err := migrate(legacySvc.db); err != nil {
		t.Fatal(err)
	}
	legacyOrderProof, err := proveOrderRecovery(ctx, legacySvc.db)
	if err != nil {
		t.Fatal(err)
	}
	legacyGolden := writeCurrentSnapshot(t, legacySvc.db)
	currentBackup := filepath.Join(t.TempDir(), "paper-accounting-current-before-downgrade.db")
	currentManifestPath := currentBackup + ".manifest.json"
	if _, err := createBackup(legacySvc.db, currentBackup, legacyGolden, currentManifestPath, legacySvc.now, legacySvc.id); err != nil {
		t.Fatal(err)
	}
	downgradePaperMarketSignalsForTest(t, legacySvc.db)
	downgradePaperAccountingForTest(t, legacySvc.db)
	var legacyVersion, legacySessionTable int
	if err := legacySvc.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&legacyVersion); err != nil || legacyVersion != 14 {
		t.Fatalf("legacy source version=%d, want 14: %v", legacyVersion, err)
	}
	if err := legacySvc.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='paper_accounting_sessions'`).Scan(&legacySessionTable); err != nil || legacySessionTable != 0 {
		t.Fatalf("legacy v9 source retained sessions table: exists=%d err=%v", legacySessionTable, err)
	}
	legacyBackup := filepath.Join(t.TempDir(), "paper-accounting-v9.db")
	if _, err := legacySvc.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(legacyBackup, "'", "''") + `'`); err != nil {
		t.Fatal(err)
	}
	legacyManifest := readJSONMap(t, currentManifestPath)
	legacyManifest["format_version"] = "omni-folio-backup.v9"
	legacyManifest["schema_version"] = "omni-folio.sqlite.v14"
	delete(legacyManifest, "paper_accounting_state_sha256")
	delete(legacyManifest, "paper_accounting_session_count")
	delete(legacyManifest, "paper_market_bar_observation_count")
	delete(legacyManifest, "paper_signal_event_count")
	delete(legacyManifest, "paper_execution_authorization_count")
	delete(legacyManifest, "paper_capitalized_fill_count")
	delete(legacyManifest["verification_receipt"].(map[string]any), "candidate_paper_accounting_state_sha256")
	legacySHA, legacySize, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	legacyManifest["db_sha256"] = legacySHA
	legacyManifest["size_bytes"] = legacySize
	legacyManifest["verification_receipt"].(map[string]any)["candidate_db_sha256"] = legacySHA
	legacyManifestPath := filepath.Join(t.TempDir(), "paper-accounting-v9.manifest.json")
	writeJSONFile(t, legacyManifestPath, legacyManifest)
	beforeSHA, beforeSize, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(legacyBackup, legacyGolden, legacyManifestPath); err != nil {
		t.Fatal(err)
	}
	afterSHA, afterSize, err := hashFile(legacyBackup)
	if err != nil || beforeSHA != afterSHA || beforeSize != afterSize {
		t.Fatalf("v9 source changed during owned-copy migration: before=(%s,%d) after=(%s,%d) err=%v", beforeSHA, beforeSize, afterSHA, afterSize, err)
	}
	ownedCopy := filepath.Join(t.TempDir(), "paper-accounting-v9-owned-copy.db")
	if err := copyFile(legacyBackup, ownedCopy); err != nil {
		t.Fatal(err)
	}
	candidate, err := openExistingDB(ownedCopy)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(candidate); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	ownedOrderProof, orderErr := proveOrderRecovery(ctx, candidate)
	ownedPaperProof, paperErr := provePaperAccountingRecovery(ctx, candidate)
	closeErr := candidate.Close()
	if orderErr != nil || paperErr != nil || closeErr != nil || ownedOrderProof != legacyOrderProof ||
		ownedPaperProof.Sessions != 0 || ownedPaperProof.MarketBars != 0 || ownedPaperProof.Signals != 0 {
		t.Fatalf("v9 owned copy proof order=%+v paper=%+v errors=(%v,%v,%v)", ownedOrderProof, ownedPaperProof, orderErr, paperErr, closeErr)
	}
}

func TestG38C2PaperMarketBackupV11AndV10OwnedCopyMigration(t *testing.T) {
	ctx := context.Background()
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	if _, err := recordG38C2PaperSignalForTest(svc, k2aAccountRef, signal); err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "paper-market-v11.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v15" || manifest.SchemaVersion != "omni-folio.sqlite.v21" ||
		manifest.PaperAccountingSessionCount != 1 || manifest.PaperMarketBarObservationCount != 1 || manifest.PaperSignalEventCount != 1 ||
		manifest.PaperExecutionAuthorizationCount != 0 || manifest.PaperCapitalizedFillCount != 0 ||
		manifest.PaperAccountingStateSHA256 == "" || manifest.VerificationReceipt.CandidatePaperAccountingStateSHA256 != manifest.PaperAccountingStateSHA256 {
		t.Fatalf("v11 manifest omitted paper market proof: %+v", manifest)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"paper_market_bar_observation_count", "paper_signal_event_count",
		"paper_execution_authorization_count", "paper_capitalized_fill_count",
	} {
		tampered := readJSONMap(t, manifestPath)
		delete(tampered, field)
		path := filepath.Join(t.TempDir(), "missing-"+field+".manifest.json")
		writeJSONFile(t, path, tampered)
		if err := verifyManifest(backup, golden, path); err == nil {
			t.Fatalf("v11 manifest without %s was accepted", field)
		}
	}

	legacySvc, _ := testService(t, nil, nil)
	legacyEvidence, legacySelection := selectedPaperStrategy(t, legacySvc)
	if _, err := legacySvc.openPaperAccountingSession(ctx, k2aAccountRef, legacyEvidence.ResultSHA256, legacySelection.CurrentEventID); err != nil {
		t.Fatal(err)
	}
	legacySignal := paperEvaluationSignal(legacyEvidence.ResultSHA256, legacySelection.CurrentEventID, "g38c2-v10-legacy-order")
	downgradePaperAuthorizationForTest(t, legacySvc.db)
	insertG38C2LegacyPaperOrder(t, legacySvc, legacySignal)
	if err := migrate(legacySvc.db); err != nil {
		t.Fatal(err)
	}
	legacyOrderProof, err := proveOrderRecovery(ctx, legacySvc.db)
	if err != nil {
		t.Fatal(err)
	}
	legacyPaperProof, err := proveLegacyPaperAccountingRecovery(ctx, legacySvc.db)
	if err != nil {
		t.Fatal(err)
	}
	legacyGolden := writeCurrentSnapshot(t, legacySvc.db)
	currentBackup := filepath.Join(t.TempDir(), "paper-market-before-v10-downgrade.db")
	currentManifestPath := currentBackup + ".manifest.json"
	if _, err := createBackup(legacySvc.db, currentBackup, legacyGolden, currentManifestPath, legacySvc.now, legacySvc.id); err != nil {
		t.Fatal(err)
	}
	downgradePaperMarketSignalsForTest(t, legacySvc.db)
	legacyBackup := filepath.Join(t.TempDir(), "paper-market-v10.db")
	if _, err := legacySvc.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(legacyBackup, "'", "''") + `'`); err != nil {
		t.Fatal(err)
	}
	legacyManifest := readJSONMap(t, currentManifestPath)
	legacyManifest["format_version"] = "omni-folio-backup.v10"
	legacyManifest["schema_version"] = "omni-folio.sqlite.v15"
	legacyManifest["paper_accounting_state_sha256"] = legacyPaperProof.SHA256
	legacyManifest["paper_accounting_session_count"] = legacyPaperProof.Sessions
	delete(legacyManifest, "paper_market_bar_observation_count")
	delete(legacyManifest, "paper_signal_event_count")
	delete(legacyManifest, "paper_execution_authorization_count")
	delete(legacyManifest, "paper_capitalized_fill_count")
	legacyManifest["verification_receipt"].(map[string]any)["candidate_paper_accounting_state_sha256"] = legacyPaperProof.SHA256
	legacySHA, legacySize, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	legacyManifest["db_sha256"], legacyManifest["size_bytes"] = legacySHA, legacySize
	legacyManifest["verification_receipt"].(map[string]any)["candidate_db_sha256"] = legacySHA
	legacyManifestPath := filepath.Join(t.TempDir(), "paper-market-v10.manifest.json")
	writeJSONFile(t, legacyManifestPath, legacyManifest)
	beforeSHA, beforeSize, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(legacyBackup, legacyGolden, legacyManifestPath); err != nil {
		t.Fatal(err)
	}
	afterSHA, afterSize, err := hashFile(legacyBackup)
	if err != nil || beforeSHA != afterSHA || beforeSize != afterSize {
		t.Fatalf("v10 source changed during owned-copy migration: before=(%s,%d) after=(%s,%d) err=%v", beforeSHA, beforeSize, afterSHA, afterSize, err)
	}
	ownedCopy := filepath.Join(t.TempDir(), "paper-market-v10-owned.db")
	if err := copyFile(legacyBackup, ownedCopy); err != nil {
		t.Fatal(err)
	}
	candidate, err := openExistingDB(ownedCopy)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(candidate); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	ownedOrders, orderErr := proveOrderRecovery(ctx, candidate)
	ownedPaper, paperErr := provePaperAccountingRecovery(ctx, candidate)
	closeErr := candidate.Close()
	if orderErr != nil || paperErr != nil || closeErr != nil || ownedOrders != legacyOrderProof || ownedPaper.Sessions != 1 ||
		ownedPaper.MarketBars != 0 || ownedPaper.Signals != 0 || ownedPaper.Authorizations != 0 || ownedPaper.CapitalizedFills != 0 {
		t.Fatalf("v10 owned proof orders=%+v paper=%+v errors=(%v,%v,%v)", ownedOrders, ownedPaper, orderErr, paperErr, closeErr)
	}
}

func downgradePaperMarketSignalsForTest(t testing.TB, db *sql.DB) {
	t.Helper()
	downgradePaperAuthorizationForTest(t, db)
	if _, err := db.Exec(`DROP TRIGGER order_idempotency_legacy_paper_signal_guard;
		DROP TRIGGER paper_signal_events_no_update;
		DROP TRIGGER paper_signal_events_no_delete;
		DROP TRIGGER paper_signal_events_state_guard;
		DROP TABLE paper_signal_events;
		DROP TRIGGER paper_market_bar_observations_no_update;
		DROP TRIGGER paper_market_bar_observations_no_delete;
		DROP TABLE paper_market_bar_observations;
		DELETE FROM schema_migrations WHERE version=16`); err != nil {
		t.Fatal(err)
	}
}

func writeCurrentSnapshot(t *testing.T, db queryer) string {
	t.Helper()
	snapshot, err := snapshotFrom(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "golden-snapshot.json")
	writeJSONFile(t, path, snapshot)
	return path
}

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
