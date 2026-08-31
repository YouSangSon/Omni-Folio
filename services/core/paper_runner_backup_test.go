package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestG38F2BackupV15BindsReleasedPaperRunnerLeaseProof(t *testing.T) {
	ctx := context.Background()
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	proof, err := provePaperRunnerLeaseRecovery(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if proof.Leases != 1 || proof.Active != 0 || len(proof.SHA256) != 64 {
		t.Fatalf("released runner lease proof=%+v", proof)
	}

	backup := filepath.Join(t.TempDir(), "g38f2-released-v15.db")
	golden := writeCurrentSnapshot(t, svc.db)
	manifest, err := createBackup(svc.db, backup, golden, backup+".manifest.json", svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v15" || manifest.SchemaVersion != "omni-folio.sqlite.v21" ||
		manifest.PaperRunnerLeaseStateSHA256 != proof.SHA256 || manifest.PaperRunnerLeaseCount != proof.Leases ||
		manifest.ActivePaperRunnerLeaseCount != proof.Active || manifest.VerificationReceipt.PaperRunnerLeaseCheck != "ok" ||
		manifest.VerificationReceipt.CandidatePaperRunnerLeaseStateSHA256 != proof.SHA256 ||
		!manifest.VerificationReceipt.EligibleForActivation {
		t.Fatalf("v15 manifest omitted quiesced runner lease proof: %+v", manifest)
	}
	if err := verifyManifest(backup, golden, backup+".manifest.json"); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{
		"paper_runner_lease_state_sha256", "paper_runner_lease_count", "active_paper_runner_lease_count",
	} {
		tampered := readJSONMap(t, backup+".manifest.json")
		delete(tampered, field)
		path := filepath.Join(t.TempDir(), "missing-"+field+".json")
		writeJSONFile(t, path, tampered)
		if err := verifyManifest(backup, golden, path); err == nil {
			t.Fatalf("v15 manifest without %s was accepted", field)
		}
	}
	for _, field := range []string{"paper_runner_lease_check", "candidate_paper_runner_lease_state_sha256"} {
		tampered := readJSONMap(t, backup+".manifest.json")
		delete(tampered["verification_receipt"].(map[string]any), field)
		path := filepath.Join(t.TempDir(), "missing-receipt-"+field+".json")
		writeJSONFile(t, path, tampered)
		if err := verifyManifest(backup, golden, path); err == nil {
			t.Fatalf("v15 receipt without %s was accepted", field)
		}
	}
	for name, mutate := range map[string]func(map[string]any){
		"digest": func(value map[string]any) { value["paper_runner_lease_state_sha256"] = strings.Repeat("0", 64) },
		"count":  func(value map[string]any) { value["paper_runner_lease_count"] = float64(0) },
		"active": func(value map[string]any) { value["active_paper_runner_lease_count"] = float64(1) },
		"receipt": func(value map[string]any) {
			value["verification_receipt"].(map[string]any)["paper_runner_lease_check"] = "failed"
		},
	} {
		t.Run(name, func(t *testing.T) {
			tampered := readJSONMap(t, backup+".manifest.json")
			mutate(tampered)
			path := filepath.Join(t.TempDir(), "forged-"+name+".json")
			writeJSONFile(t, path, tampered)
			if err := verifyManifest(backup, golden, path); err == nil {
				t.Fatal("forged runner lease proof was eligible for activation")
			}
		})
	}
}

func TestG38F2Migration21CreatesStrictSingletonWithoutChangingPolicyProof(t *testing.T) {
	ctx := context.Background()
	svc, performance := g38EPerformanceWindow(t, []string{"100", "100"})
	if _, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID); err != nil {
		t.Fatal(err)
	}
	before, err := provePaperPerformancePolicyRecovery(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	g38F2DowngradePaperRunnerLeaseForTest(t, svc.db)
	if err := migrate(svc.db); err != nil {
		t.Fatal(err)
	}
	after, err := provePaperPerformancePolicyRecovery(ctx, svc.db)
	if err != nil || after != before {
		t.Fatalf("migration 21 changed prior policy proof: before=%+v after=%+v err=%v", before, after, err)
	}
	var strict, rows int
	if err := svc.db.QueryRow(`SELECT strict FROM pragma_table_list WHERE schema='main' AND type='table' AND name='paper_runner_leases'`).Scan(&strict); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_runner_leases WHERE scope='paper_strategy_selection'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	proof, err := provePaperRunnerLeaseRecovery(ctx, svc.db)
	if err != nil || strict != 1 || rows != 1 || proof.Leases != 1 || proof.Active != 0 {
		t.Fatalf("migration 21 did not create the strict released singleton: strict=%d rows=%d proof=%+v err=%v", strict, rows, proof, err)
	}
}

func TestG38F2BackupV15PreservesActiveLeaseButRejectsActivation(t *testing.T) {
	ctx := context.Background()
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	claim, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	source, err := provePaperRunnerLeaseRecovery(ctx, svc.db)
	if err != nil || source.Leases != 1 || source.Active != 1 {
		t.Fatalf("active source runner lease proof=%+v err=%v", source, err)
	}

	backup := filepath.Join(t.TempDir(), "g38f2-active-v15.db")
	golden := writeCurrentSnapshot(t, svc.db)
	manifest, err := createBackup(svc.db, backup, golden, backup+".manifest.json", svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.PaperRunnerLeaseStateSHA256 != source.SHA256 || manifest.PaperRunnerLeaseCount != 1 ||
		manifest.ActivePaperRunnerLeaseCount != 1 || manifest.VerificationReceipt.CandidatePaperRunnerLeaseStateSHA256 != source.SHA256 ||
		manifest.VerificationReceipt.EligibleForActivation {
		t.Fatalf("active runner lease was not preserved and activation-blocked: %+v", manifest)
	}
	candidate, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	candidateProof, proofErr := provePaperRunnerLeaseRecovery(ctx, candidate)
	fresh := newService(candidate, svc.now, func(prefix string) string { return "fresh_" + prefix })
	_, freshClaimErr := fresh.acquirePaperRunnerLease(ctx, k2aAccountRef)
	_, freshHeartbeatErr := fresh.heartbeatPaperRunnerLease(ctx, claim)
	freshReleaseErr := fresh.releasePaperRunnerLease(ctx, claim)
	closeErr := candidate.Close()
	if proofErr != nil || freshClaimErr == nil || freshHeartbeatErr == nil || freshReleaseErr == nil || closeErr != nil || candidateProof != source {
		t.Fatalf("active runner lease changed or captured owner was reusable: source=%+v candidate=%+v errors=(%v,%v,%v,%v,%v)", source, candidateProof, proofErr, freshClaimErr, freshHeartbeatErr, freshReleaseErr, closeErr)
	}
	if err := verifyManifest(backup, golden, backup+".manifest.json"); err == nil {
		t.Fatal("active runner lease backup was eligible for activation")
	}
	if err := verifyRestore(backup, golden); err == nil {
		t.Fatal("active runner lease candidate passed restore activation")
	}
}

func TestG38F2BackupLegacyV14UsesOwnedV21MigrationWithEmptyLease(t *testing.T) {
	ctx := context.Background()
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	current := filepath.Join(t.TempDir(), "g38f2-v15-current.db")
	golden := writeCurrentSnapshot(t, svc.db)
	if _, err := createBackup(svc.db, current, golden, current+".manifest.json", svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	g38F2DowngradePaperRunnerLeaseForTest(t, svc.db)
	legacy := filepath.Join(t.TempDir(), "g38f2-v14-source.db")
	if _, err := svc.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(legacy, "'", "''") + `'`); err != nil {
		t.Fatal(err)
	}
	manifest := readJSONMap(t, current+".manifest.json")
	manifest["format_version"], manifest["schema_version"] = "omni-folio-backup.v14", "omni-folio.sqlite.v20"
	for _, field := range []string{"paper_runner_lease_state_sha256", "paper_runner_lease_count", "active_paper_runner_lease_count"} {
		delete(manifest, field)
	}
	receipt := manifest["verification_receipt"].(map[string]any)
	delete(receipt, "paper_runner_lease_check")
	delete(receipt, "candidate_paper_runner_lease_state_sha256")
	sha, size, err := hashFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	manifest["db_sha256"], manifest["size_bytes"], receipt["candidate_db_sha256"] = sha, size, sha
	legacyManifest := filepath.Join(t.TempDir(), "g38f2-v14-source.manifest.json")
	writeJSONFile(t, legacyManifest, manifest)

	beforeSHA, beforeSize, err := hashFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(legacy, golden, legacyManifest); err != nil {
		t.Fatal(err)
	}
	afterSHA, afterSize, err := hashFile(legacy)
	if err != nil || beforeSHA != afterSHA || beforeSize != afterSize {
		t.Fatalf("v14 source changed during verification: before=(%s,%d) after=(%s,%d) err=%v", beforeSHA, beforeSize, afterSHA, afterSize, err)
	}
	owned := filepath.Join(t.TempDir(), "g38f2-v14-owned.db")
	if err := copyFile(legacy, owned); err != nil {
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
	proof, proofErr := provePaperRunnerLeaseRecovery(ctx, db)
	closeErr := db.Close()
	if proofErr != nil || closeErr != nil || proof.Leases != 1 || proof.Active != 0 || len(proof.SHA256) != 64 {
		t.Fatalf("owned v14 migration runner lease proof=%+v errors=(%v,%v)", proof, proofErr, closeErr)
	}
}

func TestG38F2BackupRejectsRunnerLeaseSchemaAndRecordCorruption(t *testing.T) {
	ctx := context.Background()
	for name, corrupt := range map[string]func(t *testing.T, svc *Service){
		"missing table": func(t *testing.T, svc *Service) {
			t.Helper()
			if _, err := svc.db.Exec(`DROP TABLE paper_runner_leases`); err != nil {
				t.Fatal(err)
			}
		},
		"record hash": func(t *testing.T, svc *Service) {
			t.Helper()
			rows, err := svc.db.Query(`SELECT name FROM sqlite_master WHERE type='trigger' AND tbl_name='paper_runner_leases'`)
			if err != nil {
				t.Fatal(err)
			}
			var triggers []string
			for rows.Next() {
				var trigger string
				if err := rows.Scan(&trigger); err != nil {
					_ = rows.Close()
					t.Fatal(err)
				}
				triggers = append(triggers, trigger)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			for _, trigger := range triggers {
				if _, err := svc.db.Exec(`DROP TRIGGER ` + quoteSQLiteIdentifier(trigger)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := svc.db.Exec(`UPDATE paper_runner_leases SET record_sha256=?`, strings.Repeat("0", 64)); err != nil {
				t.Fatal(err)
			}
		},
		"missing transition trigger": func(t *testing.T, svc *Service) {
			t.Helper()
			if _, err := svc.db.Exec(`DROP TRIGGER paper_runner_leases_state_guard`); err != nil {
				t.Fatal(err)
			}
		},
		"missing selection binding index": func(t *testing.T, svc *Service) {
			t.Helper()
			if _, err := svc.db.Exec(`DROP INDEX strategy_selection_events_runner_binding_idx`); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := g38EPerformanceWindow(t, []string{"100"})
			corrupt(t, svc)
			if _, err := provePaperRunnerLeaseRecovery(ctx, svc.db); err == nil {
				t.Fatal("runner lease corruption passed recovery proof")
			}
			backup := filepath.Join(t.TempDir(), "corrupt.db")
			if _, err := createBackup(svc.db, backup, writeCurrentSnapshot(t, svc.db), backup+".manifest.json", svc.now, svc.id); err == nil {
				t.Fatal("runner lease corruption created an activation candidate")
			}
		})
	}
}

func g38F2DowngradePaperRunnerLeaseForTest(t testing.TB, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`DROP TABLE paper_runner_leases; DELETE FROM schema_migrations WHERE version=21`); err != nil {
		t.Fatal(err)
	}
}
