package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func downgradeLedgerReplayForTest(t testing.TB, db *sql.DB) {
	t.Helper()
	var present int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='ledger_replay_state'`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present == 0 {
		return
	}
	if _, err := db.Exec(`DROP TRIGGER events_replay_policy_guard; DROP TABLE ledger_replay_state; DROP TABLE ledger_replay_transition;
        DROP TRIGGER events_cash_void_guard; DELETE FROM schema_migrations WHERE version=22`); err != nil {
		t.Fatal(err)
	}
	script, err := migrationFiles.ReadFile("migrations/009_fx_exchange.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, body, found := strings.Cut(string(script), "CREATE TRIGGER events_cash_void_guard")
	if !found {
		t.Fatal("legacy cash-void guard fixture is missing")
	}
	if _, err := db.Exec("CREATE TRIGGER events_cash_void_guard" + body); err != nil {
		t.Fatal(err)
	}
}

func legacyGoldenForTest(t *testing.T, currentPath string, manifest map[string]any) string {
	t.Helper()
	raw, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot PortfolioSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.CostBasisPolicy = legacyFIFOCostBasisPolicy
	path := filepath.Join(t.TempDir(), "legacy-policy-golden.json")
	writeJSONFile(t, path, snapshot)
	sha, _, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest["expected_snapshot_sha256"] = sha
	manifest["verification_receipt"].(map[string]any)["candidate_snapshot_sha256"] = sha
	return path
}

func legacyChronologyService(t *testing.T) *Service {
	t.Helper()
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "legacy-chronology", testCSV(
		"b0,account-main,BUY,2026-01-01T00:00:00Z,AAPL,1,10,0,USD,-10",
		"b1,account-main,BUY,2026-01-01T00:00:00.1Z,AAPL,1,20,0,USD,-20",
		"s1,account-main,SELL,2026-01-01T00:00:00.2Z,AAPL,1,30,0,USD,30",
	))
	downgradeLedgerReplayForTest(t, svc.db)
	return svc
}

func TestLedgerReplayTransitionIsExplicitAtomicAndReplayable(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	body := testCSV(
		"b0,account-main,BUY,2026-01-01T00:00:00Z,AAPL,1,10,0,USD,-10",
		"b1,account-main,BUY,2026-01-01T00:00:00.1Z,AAPL,1,20,0,USD,-20",
		"s1,account-main,SELL,2026-01-01T00:00:00.2Z,AAPL,1,30,0,USD,30",
	)
	applyTestCSV(t, svc, "before-duplicate", body)
	svc.now = func() time.Time { return mustTime("2026-01-10T16:00:00Z") }
	applyTestCSV(t, svc, "duplicate-only", body)
	downgradeLedgerReplayForTest(t, svc.db)
	ctx := context.Background()
	if err := migrate(svc.db); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotFrom(ctx, svc.db); err == nil {
		t.Fatal("unreviewed legacy replay served a current snapshot")
	}
	if err := requireServerStartupRecovery(svc.db); err == nil {
		t.Fatal("legacy replay started normal server authority")
	}
	if _, err := svc.db.Exec(`INSERT OR REPLACE INTO ledger_replay_state VALUES(1,?,0)`, fifoCostBasisPolicy); err == nil {
		t.Fatal("SQL REPLACE bypassed the unreviewed legacy state")
	}
	if _, err := svc.db.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,currency,amount,receipt_id,recorded_at) VALUES('blocked','blocked','account-main','DEPOSIT','2026-01-02T00:00:00Z','USD','1','blocked','2026-01-10T15:01:00Z')`); err == nil {
		t.Fatal("legacy preparation allowed an unreviewed direct write")
	}
	preview, err := previewLedgerReplay(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if preview.LegacySnapshot.RealizedPnL[0].Amount != "10" || preview.Snapshot.RealizedPnL[0].Amount != "20" || preview.LegacySnapshotSHA256 == preview.SnapshotSHA256 {
		t.Fatalf("preview hid the economic correction: %+v", preview)
	}
	if preview.LegacySnapshot.RecordedAt != "2026-01-10T16:00:00Z" || preview.Snapshot.RecordedAt != "2026-01-10T16:00:00Z" {
		t.Fatal("preview lost duplicate-only import metadata")
	}
	for _, expectation := range []struct {
		revision         int64
		events, snapshot string
	}{
		{preview.LedgerRevision + 1, preview.EventsSHA256, preview.SnapshotSHA256},
		{preview.LedgerRevision, strings.Repeat("0", 64), preview.SnapshotSHA256},
		{preview.LedgerRevision, preview.EventsSHA256, strings.Repeat("0", 64)},
	} {
		if _, err := activateLedgerReplay(ctx, svc.db, expectation.revision, expectation.events, expectation.snapshot, svc.now()); err == nil {
			t.Fatal("activation ignored changed preview expectations")
		}
		var count int
		if err := svc.db.QueryRow(`SELECT COUNT(*) FROM ledger_replay_transition`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed activation left a receipt: %d %v", count, err)
		}
	}
	if _, err := activateLedgerReplay(ctx, svc.db, preview.LedgerRevision, preview.EventsSHA256, preview.SnapshotSHA256, mustTime("2025-01-01T00:00:00Z")); err == nil {
		t.Fatal("backdated transition activated")
	}
	if _, err := svc.db.Exec(`UPDATE ledger_meta SET recorded_at='2026-01-10T16:00:30Z' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := activateLedgerReplay(ctx, svc.db, preview.LedgerRevision, preview.EventsSHA256, preview.SnapshotSHA256, mustTime("2026-01-10T16:01:00Z")); err == nil {
		t.Fatal("metadata-only change bypassed reviewed snapshot expectations")
	}
	preview, err = previewLedgerReplay(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := activateLedgerReplay(ctx, svc.db, preview.LedgerRevision, preview.EventsSHA256, preview.SnapshotSHA256, mustTime("2026-01-10T16:01:00Z")); err != nil {
		t.Fatal(err)
	}
	current, err := snapshotFrom(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	_, currentSHA, err := orderJSONHash(current)
	if err != nil || currentSHA != preview.SnapshotSHA256 {
		t.Fatalf("activated snapshot differs from the reviewed result: %s %v", currentSHA, err)
	}
	if _, err := svc.db.Exec(`INSERT OR REPLACE INTO ledger_replay_transition SELECT * FROM ledger_replay_transition`); err == nil {
		t.Fatal("SQL REPLACE bypassed immutable transition evidence")
	}
	if _, err := svc.db.Exec(`INSERT OR REPLACE INTO ledger_replay_state VALUES(1,?,0)`, fifoCostBasisPolicy); err == nil {
		t.Fatal("SQL REPLACE erased the activated legacy origin")
	}
	svc.now = func() time.Time { return mustTime("2026-01-10T16:02:00Z") }
	applyTestCSV(t, svc, "after-upgrade", testCSV("s2,account-main,SELL,2026-01-02T00:00:00Z,AAPL,1,40,0,USD,40"))
	if err := proveLedgerReplayState(ctx, svc.db, false); err != nil {
		t.Fatal("later append changed historical transition proof:", err)
	}
	backup := filepath.Join(t.TempDir(), "upgraded.db")
	golden := writeCurrentSnapshot(t, svc.db)
	if _, err := createBackup(svc.db, backup, golden, backup+".manifest.json", svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(backup, golden, backup+".manifest.json"); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerReplayRejectsInvalidHistoryAndTamperedTransition(t *testing.T) {
	for _, mutation := range []string{"missing_receipt", "changed_hash", "invalid_chronology"} {
		t.Run(mutation, func(t *testing.T) {
			svc := legacyChronologyService(t)
			if mutation == "invalid_chronology" {
				if _, err := svc.db.Exec(`DROP TRIGGER events_no_update; UPDATE events SET occurred_at=CASE WHEN source_event_id='b0' THEN '2026-01-01T00:00:00.2Z' ELSE '2026-01-01T00:00:00Z' END WHERE source_event_id IN ('b0','s1')`); err != nil {
					t.Fatal(err)
				}
			}
			if err := migrate(svc.db); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			preview, err := previewLedgerReplay(ctx, svc.db)
			if mutation == "invalid_chronology" {
				if err == nil {
					t.Fatal("invalid chronological history was eligible for transition")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := activateLedgerReplay(ctx, svc.db, preview.LedgerRevision, preview.EventsSHA256, preview.SnapshotSHA256, svc.now()); err != nil {
				t.Fatal(err)
			}
			var sqlMutation string
			if mutation == "missing_receipt" {
				sqlMutation = `DROP TRIGGER ledger_replay_transition_no_delete; DELETE FROM ledger_replay_transition`
			} else {
				sqlMutation = `DROP TRIGGER ledger_replay_transition_no_update; UPDATE ledger_replay_transition SET snapshot_sha256='0000000000000000000000000000000000000000000000000000000000000000'`
			}
			if _, err := svc.db.Exec(sqlMutation); err != nil {
				t.Fatal(err)
			}
			guard := "ledger_replay_transition_no_update"
			if mutation == "missing_receipt" {
				guard = "ledger_replay_transition_no_delete"
			}
			script, err := migrationFiles.ReadFile("migrations/022_ledger_replay_policy.sql")
			if err != nil {
				t.Fatal(err)
			}
			prefix := "CREATE TRIGGER " + guard + " "
			_, after, found := strings.Cut(string(script), prefix)
			body, _, ended := strings.Cut(after, "END;")
			if !found || !ended {
				t.Fatal("missing original receipt guard")
			}
			if _, err := svc.db.Exec(prefix + body + "END;"); err != nil {
				t.Fatal(err)
			}
			if err := proveLedgerReplayState(ctx, svc.db, false); err == nil {
				t.Fatal("tampered replay transition passed current recovery")
			}
		})
	}
}

func TestLedgerReplayLegacyV15ArchivePreservesSourceAndGolden(t *testing.T) {
	svc, path := testService(t, nil, nil)
	applyTestCSV(t, svc, "legacy-v15-fifo", testCSV(
		"b0,account-main,BUY,2026-01-01T00:00:00Z,AAPL,1,10,0,USD,-10",
		"b1,account-main,BUY,2026-01-01T00:00:00.1Z,AAPL,1,20,0,USD,-20",
		"s1,account-main,SELL,2026-01-01T00:00:00.2Z,AAPL,1,30,0,USD,30",
	))
	backup := filepath.Join(t.TempDir(), "legacy-v15.db")
	manifest, err := createBackup(svc.db, backup, writeCurrentSnapshot(t, svc.db), backup+".manifest.json", svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	downgradeLedgerReplayForTest(t, db)
	legacy, err := snapshotFromPolicy(context.Background(), db, legacyFIFOCostBasisPolicy, -1)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join(t.TempDir(), "legacy-golden.json")
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(golden, raw, 0600); err != nil {
		t.Fatal(err)
	}
	manifest.FormatVersion, manifest.SchemaVersion = "omni-folio-backup.v15", "omni-folio.sqlite.v21"
	manifest.DBSHA256, manifest.SizeBytes, err = hashFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	manifest.VerificationReceipt.CandidateDBSHA256 = manifest.DBSHA256
	manifest.ExpectedSnapshotSHA256, _, err = hashFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	manifest.VerificationReceipt.CandidateSnapshotSHA256 = manifest.ExpectedSnapshotSHA256
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := backup + ".manifest.json"
	if err := os.WriteFile(manifestPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	before, _, err := hashFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal("legacy archive verification failed:", err)
	}
	for _, policy := range []string{"", "unsupported-policy"} {
		var historical map[string]any
		if err := json.Unmarshal(raw, &historical); err != nil {
			t.Fatal(err)
		}
		if policy == "" {
			delete(historical, "cost_basis_policy")
		} else {
			historical["cost_basis_policy"] = policy
		}
		variantGolden := filepath.Join(t.TempDir(), "historical-golden.json")
		writeJSONFile(t, variantGolden, historical)
		variant := *manifest
		variant.ExpectedSnapshotSHA256, _, err = hashFile(variantGolden)
		if err != nil {
			t.Fatal(err)
		}
		variant.VerificationReceipt.CandidateSnapshotSHA256 = variant.ExpectedSnapshotSHA256
		variantManifest := filepath.Join(t.TempDir(), "historical-manifest.json")
		writeJSONFile(t, variantManifest, variant)
		err := verifyManifest(backup, variantGolden, variantManifest)
		if (policy == "" && err != nil) || (policy != "" && err == nil) {
			t.Fatalf("historical policy boundary %q: %v", policy, err)
		}
	}
	after, _, err := hashFile(backup)
	if err != nil || before != after {
		t.Fatalf("archival verification mutated source: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerReplayCLIUsesReviewedExpectations(t *testing.T) {
	svc := legacyChronologyService(t)
	var sequence int
	var name, path string
	if err := svc.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"migrate", "-db", path}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"ledger-replay-preview", "-db", path}); err != nil {
		t.Fatal(err)
	}
	preview, err := previewLedgerReplay(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"ledger-replay-activate", "-db", path}); err == nil {
		t.Fatal("CLI activated without reviewed expectations")
	}
	if err := run([]string{"ledger-replay-activate", "-db", path,
		"-expected-revision", strconv.FormatInt(preview.LedgerRevision, 10),
		"-expected-events-sha", preview.EventsSHA256, "-expected-snapshot-sha", preview.SnapshotSHA256}); err != nil {
		t.Fatal(err)
	}
	if err := requireServerStartupRecovery(svc.db); err != nil {
		t.Fatal("activated CLI candidate failed startup recovery:", err)
	}
	absent := filepath.Join(t.TempDir(), "absent.db")
	if err := run([]string{"ledger-replay-preview", "-db", absent}); err == nil {
		t.Fatal("preview accepted a missing database")
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("failed preview created a database")
	}
}

func TestLedgerReplayGenesisRejectsUncountedEvents(t *testing.T) {
	for _, legacyMigration := range []bool{false, true} {
		t.Run(strconv.FormatBool(legacyMigration), func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			if _, err := svc.db.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,currency,amount,receipt_id,recorded_at) VALUES('uncounted','uncounted','account-main','DEPOSIT','2026-01-01T00:00:00Z','USD','1','uncounted','2026-01-10T15:01:00Z')`); err != nil {
				t.Fatal(err)
			}
			if legacyMigration {
				downgradeLedgerReplayForTest(t, svc.db)
				if err := migrate(svc.db); err != nil {
					t.Fatal(err)
				}
			}
			if err := proveLedgerReplayState(context.Background(), svc.db, false); err == nil {
				t.Fatal("genesis recovery concealed existing events behind revision zero")
			}
			if _, err := snapshotFrom(context.Background(), svc.db); err == nil {
				t.Fatal("current snapshot concealed existing events behind revision zero")
			}
		})
	}
}
