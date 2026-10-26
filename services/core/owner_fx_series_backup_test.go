package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dropOwnerFXSchemaForLegacy(t testing.TB, db *sql.DB) {
	t.Helper()
	dropOwnerPriceSchemaForLegacy(t, db)
	var present int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='owner_fx_series_versions'`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present != 0 {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM owner_fx_series_versions`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("legacy fixture cannot discard declared FX data")
		}
		if _, err := db.Exec(`DROP TRIGGER fx_observations_owner_pair_guard; DROP TABLE owner_fx_series_versions`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version=24`); err != nil {
		t.Fatal(err)
	}
}

func stripOwnerFXProofForLegacy(manifest map[string]any) {
	stripOwnerPriceProofForLegacy(manifest)
	for _, key := range []string{"owner_fx_series_state_sha256", "owner_fx_series_version_count", "owner_fx_series_point_count"} {
		delete(manifest, key)
	}
	receipt := manifest["verification_receipt"].(map[string]any)
	for _, key := range []string{"owner_fx_series_check", "candidate_owner_fx_series_state_sha256", "candidate_owner_fx_series_version_count", "candidate_owner_fx_series_point_count"} {
		delete(receipt, key)
	}
}

func TestOwnerFXBackupRestoresWholeVersionsAndProvesEachField(t *testing.T) {
	svc := ownerFXServiceForTest(t)
	applyTestCSV(t, svc, "capital", testCSV("capital,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,USD,100"))
	first := importOwnerFXForTest(t, svc, []byte(ownerFXTestInput))
	importOwnerFXForTest(t, svc, ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.SourceVersion = "v2"; i.Points[1].Rate = "1200" }))
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "owner.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := proveOwnerFXSeriesRecovery(context.Background(), svc.db)
	if err != nil || proof.Versions != 2 || proof.Points != 4 || !ownerFXManifestMatches(*manifest, proof) {
		t.Fatalf("owner proof mismatch: %+v %v", proof, err)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err != nil {
		t.Fatal(err)
	}
	db, err := openReadOnlyExistingDB(context.Background(), backup)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stored, _, err := replayOwnerFXSeries(context.Background(), db, "USD", "KRW", "manual-v1")
	if err != nil || stored == nil || ownerFXReceipt(stored).DataSHA256 != first.DataSHA256 {
		t.Fatal("original version lost in restore")
	}
	copySvc := newService(db, svc.now, svc.id)
	value, appErr := copySvc.currentPortfolioValuation(context.Background())
	if appErr != nil || value.TotalValue == nil || value.TotalValue.Amount != "120000" || value.Sample || !usesOwnerFX(value.Issues) {
		t.Fatalf("latest restored FX changed value: %+v %v", value, appErr)
	}
	for _, key := range []string{"owner_fx_series_state_sha256", "owner_fx_series_version_count", "owner_fx_series_point_count", "owner_fx_series_check", "candidate_owner_fx_series_state_sha256", "candidate_owner_fx_series_version_count", "candidate_owner_fx_series_point_count"} {
		for _, kind := range []string{"missing", "null", "changed"} {
			fields := readJSONMap(t, manifestPath)
			target := fields
			if key == "owner_fx_series_check" || strings.HasPrefix(key, "candidate_") {
				target = fields["verification_receipt"].(map[string]any)
			}
			switch kind {
			case "missing":
				delete(target, key)
			case "null":
				target[key] = nil
			case "changed":
				if strings.HasSuffix(key, "sha256") {
					target[key] = strings.Repeat("0", 64)
				} else if key == "owner_fx_series_check" {
					target[key] = "failed"
				} else {
					target[key] = 99
				}
			}
			variant := filepath.Join(t.TempDir(), "manifest.json")
			writeJSONFile(t, variant, fields)
			if err := verifyManifest(backup, golden, variant); err == nil {
				t.Fatalf("%s %s proof accepted", key, kind)
			}
		}
	}
}

func TestOwnerFXLegacyV17RetainsBenchmarkAndReplayAuthority(t *testing.T) {
	svc := ownerFXServiceForTest(t)
	applyTestCSV(t, svc, "chronology", testCSV("b0,account-main,BUY,2025-01-01T00:00:00Z,AAPL,1,10,0,USD,-10", "b1,account-main,BUY,2025-01-01T00:00:00.1Z,AAPL,1,20,0,USD,-20", "s1,account-main,SELL,2025-01-01T00:00:00.2Z,AAPL,1,30,0,USD,30"))
	importBenchmarkForTest(t, svc, []byte(benchmarkSeriesTestInput))
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "v17.db")
	manifestPath := backup + ".manifest.json"
	if _, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	dropOwnerFXSchemaForLegacy(t, db)
	if _, err := provePaperRunnerLeaseRecovery(context.Background(), db); err == nil {
		t.Fatal("current runner accepted schema23")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fields := readJSONMap(t, manifestPath)
	stripOwnerFXProofForLegacy(fields)
	fields["format_version"], fields["schema_version"] = "omni-folio-backup.v17", "omni-folio.sqlite.v23"
	sha, size, err := hashFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	fields["db_sha256"], fields["size_bytes"], fields["verification_receipt"].(map[string]any)["candidate_db_sha256"] = sha, size, sha
	writeJSONFile(t, manifestPath, fields)
	before, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatalf("v17 lost original source authority: %v", err)
	}
	after, err := os.ReadFile(backup)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("legacy source changed")
	}
	for _, key := range []string{"benchmark_series_state_sha256", "paper_runner_lease_state_sha256"} {
		variant := readJSONMap(t, manifestPath)
		variant[key] = strings.Repeat("0", 64)
		path := filepath.Join(t.TempDir(), "bad.json")
		writeJSONFile(t, path, variant)
		if err := verifyManifest(backup, golden, path); err == nil {
			t.Fatalf("v17 omitted %s source proof", key)
		}
	}
	for _, key := range []string{"owner_fx_series_state_sha256", "owner_fx_series_version_count"} {
		variant := readJSONMap(t, manifestPath)
		if strings.HasSuffix(key, "sha256") {
			variant[key] = strings.Repeat("0", 64)
		} else {
			variant[key] = 0
		}
		path := filepath.Join(t.TempDir(), "claim.json")
		writeJSONFile(t, path, variant)
		if err := verifyManifest(backup, golden, path); err == nil {
			t.Fatal("legacy format claimed new owner authority")
		}
	}
	// Even an empty precreated journal cannot inherit a legacy manifest's authority.
	precreated := filepath.Join(t.TempDir(), "precreated.db")
	if err := os.WriteFile(precreated, before, 0600); err != nil {
		t.Fatal(err)
	}
	preDB, err := openExistingDB(precreated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preDB.Exec(`CREATE TABLE owner_fx_series_versions (sequence INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := preDB.Close(); err != nil {
		t.Fatal(err)
	}
	sha, size, err = hashFile(precreated)
	if err != nil {
		t.Fatal(err)
	}
	fields["db_sha256"], fields["size_bytes"], fields["verification_receipt"].(map[string]any)["candidate_db_sha256"] = sha, size, sha
	preManifest := precreated + ".manifest.json"
	writeJSONFile(t, preManifest, fields)
	if err := verifyManifest(precreated, golden, preManifest); err == nil {
		t.Fatal("legacy source smuggled a precreated owner journal")
	}
}

func TestOwnerFXLegacyV17CannotHideAnActiveRunnerLease(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	if _, err := svc.acquirePaperRunnerLease(context.Background(), k2aAccountRef); err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "active-v17.db")
	manifestPath := backup + ".manifest.json"
	if _, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	dropOwnerFXSchemaForLegacy(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fields := readJSONMap(t, manifestPath)
	stripOwnerFXProofForLegacy(fields)
	fields["format_version"], fields["schema_version"] = "omni-folio-backup.v17", "omni-folio.sqlite.v23"
	sha, size, err := hashFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	receipt := fields["verification_receipt"].(map[string]any)
	fields["db_sha256"], fields["size_bytes"], receipt["candidate_db_sha256"] = sha, size, sha
	receipt["status"], receipt["eligible_for_activation"], receipt["errors"] = "verified", true, []string{}
	writeJSONFile(t, manifestPath, fields)
	if err := verifyManifest(backup, golden, manifestPath); err == nil {
		t.Fatal("active v17 runner became eligible")
	}
}

func TestOwnerFXRestorePreservesWithdrawalAndPairBinding(t *testing.T) {
	svc := ownerFXServiceForTest(t)
	applyTestCSV(t, svc, "capital", testCSV("capital,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,USD,100"))
	importOwnerFXForTest(t, svc, []byte(ownerFXTestInput))
	importOwnerFXForTest(t, svc, ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.SourceVersion = "withdrawn"; i.Points = []OwnerFXPoint{} }))
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "withdrawn.db")
	manifest := backup + ".manifest.json"
	if _, err := createBackup(svc.db, backup, golden, manifest, svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(backup, golden, manifest); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	copySvc := newService(db, svc.now, svc.id)
	value, appErr := copySvc.currentPortfolioValuation(context.Background())
	if appErr != nil || value.TotalValue != nil || value.Status != "unavailable" || usesOwnerFX(value.Issues) {
		t.Fatalf("withdrawal fell back after restore: %+v %v", value, appErr)
	}
	if _, err := copySvc.recordFXObservation(context.Background(), FXObservationInput{Source: "local_fixture", SourceObservationID: "cannot-replace-owner", BaseCurrency: "USD", QuoteCurrency: "KRW", Rate: "900", ObservedAt: "2026-01-01T00:00:00Z", FetchedAt: "2026-01-01T00:00:00Z"}); err == nil {
		t.Fatal("restored withdrawal released source binding")
	}
}
