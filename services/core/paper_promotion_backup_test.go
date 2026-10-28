package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dropPaperPromotionSchemaForLegacy(t testing.TB, db *sql.DB) {
	t.Helper()
	var present int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='paper_promotion_claims'`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present == 0 {
		return
	}
	var count int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM paper_promotion_claims) + (SELECT COUNT(*) FROM paper_promotion_decisions) +
		(SELECT COUNT(*) FROM strategy_selection_events WHERE paper_promotion_attempt_id IS NOT NULL)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("legacy fixture cannot discard paper promotion history", err)
	}
	_, indexes, triggers, err := migration27SchemaInventory(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	script, err := migrationFiles.ReadFile("migrations/020_paper_performance_policy_events.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, table, found := strings.Cut(string(script), "CREATE TABLE strategy_selection_events_new")
	if !found {
		t.Fatal("legacy selection definition missing")
	}
	table, _, found = strings.Cut(table, "INSERT INTO execution_authority_events_new")
	if !found {
		t.Fatal("legacy selection definition boundary missing")
	}
	_, guard, found := strings.Cut(string(script), "CREATE TRIGGER strategy_selection_events_state_guard")
	if !found {
		t.Fatal("legacy selection guard missing")
	}
	guard, _, found = strings.Cut(guard, "CREATE TRIGGER execution_authority_events_state_guard")
	if !found {
		t.Fatal("legacy selection guard boundary missing")
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := restoreForeignKeysForTest(db); err != nil {
			t.Fatal(err)
		}
	}()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := dropMigration20DependentTriggers(tx, triggers); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("CREATE TABLE strategy_selection_events_new" + table); err != nil {
		t.Fatal(err)
	}
	const columns = "sequence,event_id,event_type,candidate_result_sha256,expected_current_event_id,source_event_id,previous_selected_result_sha256,selected_result_sha256,reason_code,paper_evaluation_sequence,paper_performance_policy_event_id,record_sha256,record_json,recorded_at"
	if _, err := tx.Exec(`INSERT INTO strategy_selection_events_new(` + columns + `) SELECT ` + columns + ` FROM strategy_selection_events ORDER BY sequence`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DROP TRIGGER strategy_selection_events_no_update;
		DROP TRIGGER strategy_selection_events_no_delete;
		DROP TRIGGER strategy_selection_events_state_guard;
		DROP TABLE strategy_selection_events;
		ALTER TABLE strategy_selection_events_new RENAME TO strategy_selection_events;
		DROP TABLE paper_promotion_decisions;
		DROP TABLE paper_promotion_claims;
		CREATE TRIGGER strategy_selection_events_no_update BEFORE UPDATE ON strategy_selection_events
		BEGIN SELECT RAISE(ABORT, 'strategy_selection_events is insert-only'); END;
		CREATE TRIGGER strategy_selection_events_no_delete BEFORE DELETE ON strategy_selection_events
		BEGIN SELECT RAISE(ABORT, 'strategy_selection_events is insert-only'); END;
		DELETE FROM schema_migrations WHERE version=27;`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("CREATE TRIGGER strategy_selection_events_state_guard" + guard); err != nil {
		t.Fatal(err)
	}
	for _, index := range indexes {
		if _, err := tx.Exec(index.SQL); err != nil {
			t.Fatal(err)
		}
	}
	if err := restoreMigration20DependentTriggers(tx, triggers); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func stripPaperPromotionProofForLegacy(manifest map[string]any) {
	for _, key := range []string{"paper_promotion_state_sha256", "paper_promotion_claim_count", "paper_promotion_decision_count"} {
		delete(manifest, key)
	}
	delete(manifest["verification_receipt"].(map[string]any), "candidate_paper_promotion_state_sha256")
}

func TestPaperPromotionMigration27PreservesJournalsAndPerformanceRollback(t *testing.T) {
	for _, beforeMigration := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback after migration", true: "existing rollback"}[beforeMigration], func(t *testing.T) {
			ctx := context.Background()
			svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
			if beforeMigration {
				if _, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef, performance.StrategySelectionEventID, performance.StrategyPerformanceID); err != nil {
					t.Fatal(err)
				}
			}
			dropPaperPromotionSchemaForLegacy(t, svc.db)
			before, err := captureMigration27Snapshot(ctx, svc.db)
			if err != nil {
				t.Fatal(err)
			}
			if err := migrate(svc.db); err != nil {
				t.Fatal(err)
			}
			orders, err := proveOrderRecovery(ctx, svc.db)
			if err != nil || orders != before.Orders {
				t.Fatal("migration changed orders", err)
			}
			strategy, err := proveStrategyRegistryRecovery(ctx, svc.db)
			if err != nil || !sameStrategyRegistryProof(strategy, before.Strategy) {
				t.Fatal("migration changed registry", err)
			}
			objects, indexes, triggers, err := migration27SchemaInventory(ctx, svc.db)
			if err != nil || !sameMigration27SchemaObjects(before.SchemaObjects, objects) || !sameMigration27SchemaObjects(before.SelectionIndexes, indexes) || !sameMigration20Triggers(before.DependentTriggers, triggers) {
				t.Fatal("migration changed existing schema or dependent triggers", err)
			}
			var strict int
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_list WHERE name IN ('paper_promotion_claims','paper_promotion_decisions') AND strict=1`).Scan(&strict); err != nil || strict != 2 {
				t.Fatal("promotion tables are not strict", err)
			}
			proof, err := provePaperPromotionRecovery(ctx, svc.db)
			if err != nil || proof.Claims != 0 || proof.Decisions != 0 || proof.SHA256 != emptySHA256 {
				t.Fatal("migration introduced promotion history", err)
			}
			event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef, performance.StrategySelectionEventID, performance.StrategyPerformanceID)
			if err != nil || event.Decision != "HALT_AND_ROLLBACK" {
				t.Fatal("performance rollback failed after migration", err)
			}
			var link sql.NullString
			if err := svc.db.QueryRow(`SELECT paper_promotion_attempt_id FROM strategy_selection_events WHERE event_id=?`, event.RollbackSelectionEventID).Scan(&link); err != nil || link.Valid {
				t.Fatal("performance rollback acquired promotion provenance", err)
			}
			if _, err := provePaperPerformancePolicyRecovery(ctx, svc.db); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPaperPromotionMigration27FailureRestoresTriggersAndForeignKeys(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	dropPaperPromotionSchemaForLegacy(t, svc.db)
	if _, err := svc.db.Exec(`CREATE TABLE paper_promotion_claims(blocked INTEGER) STRICT`); err != nil {
		t.Fatal(err)
	}
	before, _, triggers, err := migration27SchemaInventory(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(svc.db); err == nil {
		t.Fatal("colliding promotion schema was accepted")
	}
	after, _, restored, err := migration27SchemaInventory(context.Background(), svc.db)
	if err != nil || !sameMigration27SchemaObjects(before, after) || !sameMigration20Triggers(triggers, restored) {
		t.Fatal("failed migration did not restore existing schema", err)
	}
	var version, foreignKeys, newColumn int
	if err := svc.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 26 {
		t.Fatal("failed migration advanced history", err)
	}
	if err := svc.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatal("failed migration left foreign keys disabled", err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('strategy_selection_events') WHERE name='paper_promotion_attempt_id'`).Scan(&newColumn); err != nil || newColumn != 0 {
		t.Fatal("failed migration changed selection schema", err)
	}
}

func TestPaperPromotionMigration27RejectsUnknownSelectionTriggerBeforeDDL(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	dropPaperPromotionSchemaForLegacy(t, svc.db)
	const triggerName = "user_strategy_selection_guard"
	if _, err := svc.db.Exec(`CREATE TRIGGER user_strategy_selection_guard BEFORE INSERT ON strategy_selection_events
		BEGIN SELECT RAISE(ABORT, 'user selection guard'); END;`); err != nil {
		t.Fatal(err)
	}
	var beforeSQL string
	if err := svc.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name=?`, triggerName).Scan(&beforeSQL); err != nil {
		t.Fatal(err)
	}
	if err := migrate(svc.db); err == nil || !strings.Contains(err.Error(), "migration 27 pre-rebuild proof") || !strings.Contains(err.Error(), triggerName) {
		t.Fatal("unknown selection trigger did not stop migration before DDL", err)
	}
	var afterSQL string
	if err := svc.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name=?`, triggerName).Scan(&afterSQL); err != nil || afterSQL != beforeSQL {
		t.Fatal("rejected migration changed user trigger", err)
	}
	if err := requireSchemaVersionContext(context.Background(), svc.db, 26); err != nil {
		t.Fatal("rejected migration changed schema history", err)
	}
	var foreignKeys, promotionTables, newColumn int
	if err := svc.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatal("rejected migration left foreign keys disabled", err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('paper_promotion_claims','paper_promotion_decisions')`).Scan(&promotionTables); err != nil || promotionTables != 0 {
		t.Fatal("rejected migration created promotion tables", err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('strategy_selection_events') WHERE name='paper_promotion_attempt_id'`).Scan(&newColumn); err != nil || newColumn != 0 {
		t.Fatal("rejected migration changed selection schema", err)
	}
}

func TestPaperPromotionMigration27EnforcesBoundsAndAppendOnly(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	const recordedAt = "2026-01-10T15:00:00.000000000Z"
	for _, table := range []string{"paper_promotion_claims", "paper_promotion_decisions"} {
		if _, err := svc.db.Exec(`INSERT INTO `+table+`(attempt_id,record_sha256,record_json,recorded_at) VALUES(?,?,?,?)`, "attempt", emptySHA256, "{}", recordedAt); err != nil {
			t.Fatal(err)
		}
		for _, statement := range []string{"UPDATE " + table + " SET record_json='{}'", "DELETE FROM " + table} {
			if _, err := svc.db.Exec(statement); err == nil {
				t.Fatal("promotion history was mutable", statement)
			}
		}
		for _, test := range []struct {
			name, id, sha, raw, at string
		}{
			{"id length", strings.Repeat("x", 129), emptySHA256, "{}", recordedAt},
			{"hash", "bad-hash", strings.Repeat("A", 64), "{}", recordedAt},
			{"record byte bound", "large-record", emptySHA256, strings.Repeat("가", 1<<20), recordedAt},
			{"timestamp", "bad-time", emptySHA256, "{}", "2026-01-10T15:00:00Z"},
		} {
			if table == "paper_promotion_decisions" {
				if _, err := svc.db.Exec(`INSERT INTO paper_promotion_claims(attempt_id,record_sha256,record_json,recorded_at) VALUES(?,?,?,?)`, test.id, emptySHA256, "{}", recordedAt); err != nil && test.name != "id length" {
					t.Fatal(err)
				}
			}
			if _, err := svc.db.Exec(`INSERT INTO `+table+`(attempt_id,record_sha256,record_json,recorded_at) VALUES(?,?,?,?)`, test.id, test.sha, test.raw, test.at); err == nil {
				t.Fatal("invalid promotion row accepted", table, test.name)
			}
		}
	}
}

func TestPaperPromotionMigration27RequiresMatchingPromoteDecisionBeforeSelection(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	state, err := proveStrategyRegistryRecovery(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, decision, candidate, expected, selection, reason string
		accepted                                               bool
	}{
		{"linked promote", "PROMOTE", state.SelectedResultSHA256, state.CurrentEventID, "promoted", "auto_comparison", true},
		{"rejected decision", "REJECT", state.SelectedResultSHA256, state.CurrentEventID, "promoted", "auto_comparison", false},
		{"wrong candidate", "PROMOTE", emptySHA256, state.CurrentEventID, "promoted", "auto_comparison", false},
		{"wrong expected", "PROMOTE", state.SelectedResultSHA256, "other", "promoted", "auto_comparison", false},
		{"wrong selection", "PROMOTE", state.SelectedResultSHA256, state.CurrentEventID, "other", "auto_comparison", false},
		{"manual provenance", "PROMOTE", state.SelectedResultSHA256, state.CurrentEventID, "promoted", "manual_selection", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := svc.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			const at = "2026-01-10T15:00:00.000000000Z"
			if _, err := tx.Exec(`INSERT INTO paper_promotion_claims(attempt_id,record_sha256,record_json,recorded_at) VALUES('attempt',?,'{}',?)`, emptySHA256, at); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(map[string]string{"decision": test.decision, "candidate_result_sha256": test.candidate, "expected_current_event_id": test.expected})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO paper_promotion_decisions(attempt_id,selection_event_id,record_sha256,record_json,recorded_at) VALUES('attempt',?,?,?,?)`, test.selection, emptySHA256, string(raw), at); err != nil {
				t.Fatal("decision must precede its deferred selection link", err)
			}
			_, err = tx.Exec(`INSERT INTO strategy_selection_events(event_id,event_type,candidate_result_sha256,expected_current_event_id,previous_selected_result_sha256,selected_result_sha256,reason_code,paper_evaluation_sequence,paper_promotion_attempt_id,record_sha256,record_json,recorded_at)
				VALUES('promoted','SELECT',?,?,?,?,?,(SELECT COALESCE(MAX(sequence),0) FROM paper_evaluation_events),'attempt',?,'{}',?)`,
				state.SelectedResultSHA256, state.CurrentEventID, state.SelectedResultSHA256, state.SelectedResultSHA256, test.reason, emptySHA256, at)
			if (err == nil) != test.accepted {
				t.Fatalf("selection accepted=%v want=%v err=%v", err == nil, test.accepted, err)
			}
		})
	}
}

func TestPaperPromotionBackupV21BindsProofAndRejectsMissingNullOrForgedFields(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "promotion.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := provePaperPromotionRecovery(context.Background(), svc.db)
	if err != nil || manifest.FormatVersion != "omni-folio-backup.v21" || manifest.SchemaVersion != "omni-folio.sqlite.v27" || !paperPromotionManifestMatches(*manifest, proof) {
		t.Fatal("v21 manifest omitted promotion proof", err)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"paper_promotion_state_sha256", "paper_promotion_claim_count", "paper_promotion_decision_count", "candidate_paper_promotion_state_sha256"} {
		for _, kind := range []string{"missing", "null", "forged"} {
			value := readJSONMap(t, manifestPath)
			target := value
			if strings.HasPrefix(key, "candidate_") {
				target = value["verification_receipt"].(map[string]any)
			}
			switch kind {
			case "missing":
				delete(target, key)
			case "null":
				target[key] = nil
			default:
				if strings.HasSuffix(key, "sha256") {
					target[key] = strings.Repeat("0", 64)
				} else {
					target[key] = 1
				}
			}
			altered := filepath.Join(t.TempDir(), "altered.json")
			writeJSONFile(t, altered, value)
			if err := verifyManifest(backup, golden, altered); err == nil {
				t.Fatal("unproved promotion field accepted", key, kind)
			}
		}
	}
}

func TestPaperPromotionLegacyV20PreservesWatchlistAndSourceBytes(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	watchlistWriteForTest(t, svc, 0, watchAAPL)
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "v20.db")
	manifestPath := backup + ".manifest.json"
	if _, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	dropPaperPromotionSchemaForLegacy(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	manifest := readJSONMap(t, manifestPath)
	stripPaperPromotionProofForLegacy(manifest)
	manifest["format_version"], manifest["schema_version"] = "omni-folio-backup.v20", "omni-folio.sqlite.v26"
	sha, size, err := hashFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	manifest["db_sha256"], manifest["size_bytes"], manifest["verification_receipt"].(map[string]any)["candidate_db_sha256"] = sha, size, sha
	writeJSONFile(t, manifestPath, manifest)
	before, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal("v20 lost watchlist or nonarchival recovery", err)
	}
	after, err := os.ReadFile(backup)
	if err != nil || string(before) != string(after) {
		t.Fatal("legacy source changed during verification", err)
	}
	for key, value := range map[string]any{"watchlist_state_sha256": strings.Repeat("0", 64), "paper_promotion_claim_count": 0} {
		forged := readJSONMap(t, manifestPath)
		forged[key] = value
		altered := filepath.Join(t.TempDir(), "forged-v20.json")
		writeJSONFile(t, altered, forged)
		if err := verifyManifest(backup, golden, altered); err == nil {
			t.Fatal("legacy v20 accepted unproved state", key)
		}
	}
}
