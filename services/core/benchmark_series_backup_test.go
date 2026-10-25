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

// Historical fixtures must not silently discard an admitted benchmark dataset.
func dropBenchmarkSchemaForLegacy(t testing.TB, db *sql.DB) {
	t.Helper()
	var present int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='benchmark_series_versions'`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present != 0 {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM benchmark_series_versions`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("legacy fixture cannot discard benchmark data")
		}
		if _, err := db.Exec(`DROP TABLE benchmark_series_versions`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version=23`); err != nil {
		t.Fatal(err)
	}
}

func stripBenchmarkProofForLegacy(manifest map[string]any) {
	for _, key := range []string{"benchmark_series_state_sha256", "benchmark_series_version_count", "benchmark_series_point_count"} {
		delete(manifest, key)
	}
	receipt := manifest["verification_receipt"].(map[string]any)
	for _, key := range []string{"benchmark_series_check", "candidate_benchmark_series_state_sha256", "candidate_benchmark_series_version_count", "candidate_benchmark_series_point_count"} {
		delete(receipt, key)
	}
}

func TestBenchmarkSeriesBackupBindsBothCurrenciesAndVersions(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	first := importBenchmarkForTest(t, svc, []byte(benchmarkSeriesTestInput))
	importBenchmarkForTest(t, svc, benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.SourceVersion = "test-v2"; i.Points[1].Level = "120" }))
	usd := importBenchmarkForTest(t, svc, benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.BenchmarkID = "test_usd"; i.Currency = "USD" }))
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "benchmark.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := proveBenchmarkSeriesRecovery(context.Background(), svc.db)
	if err != nil || proof.Versions != 3 || proof.Points != 6 || !benchmarkManifestMatches(*manifest, proof) {
		t.Fatalf("missing version proof: %+v %v", proof, err)
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
	for _, want := range []*BenchmarkSeriesReceipt{first, usd} {
		got, err := readBenchmarkSeriesStatus(context.Background(), db, want.BenchmarkID, want.SourceVersion)
		if err != nil || got.DataSHA256 != want.DataSHA256 || got.RecordedAt != want.RecordedAt {
			t.Fatalf("restored version changed: %+v %v", got, err)
		}
	}
	for _, key := range []string{"benchmark_series_state_sha256", "benchmark_series_version_count", "benchmark_series_point_count", "benchmark_series_check", "candidate_benchmark_series_state_sha256", "candidate_benchmark_series_version_count", "candidate_benchmark_series_point_count"} {
		t.Run(key, func(t *testing.T) {
			for _, mutation := range []string{"missing", "null", "changed"} {
				fields := readJSONMap(t, manifestPath)
				target := fields
				if key == "benchmark_series_check" || strings.HasPrefix(key, "candidate_") {
					target = fields["verification_receipt"].(map[string]any)
				}
				switch mutation {
				case "missing":
					delete(target, key)
				case "null":
					target[key] = nil
				case "changed":
					if strings.HasSuffix(key, "sha256") {
						target[key] = strings.Repeat("0", 64)
					} else if key == "benchmark_series_check" {
						target[key] = "failed"
					} else {
						target[key] = 99
					}
				}
				variant := filepath.Join(t.TempDir(), "manifest.json")
				writeJSONFile(t, variant, fields)
				if err := verifyManifest(backup, golden, variant); err == nil {
					t.Fatalf("%s %s proof accepted", mutation, key)
				}
			}
		})
	}
}

func TestBenchmarkSeriesRecoveryRejectsPhysicalAndJournalTampering(t *testing.T) {
	for _, weakness := range []string{"strict", "unique"} {
		t.Run(weakness, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			script, err := migrationFiles.ReadFile("migrations/023_benchmark_series_versions.sql")
			if err != nil {
				t.Fatal(err)
			}
			altered := string(script)
			if weakness == "strict" {
				altered = strings.Replace(altered, ") STRICT;", ");", 1)
			} else {
				altered = strings.Replace(altered, ",\n    UNIQUE (benchmark_id, source_version)", "", 1)
			}
			if altered == string(script) {
				t.Fatal("tamper fixture did not change DDL")
			}
			if _, err := svc.db.Exec(`DROP TABLE benchmark_series_versions`); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.db.Exec(altered); err != nil {
				t.Fatal(err)
			}
			if _, err := proveBenchmarkSeriesRecovery(context.Background(), svc.db); err == nil {
				t.Fatal("weakened empty table accepted")
			}
		})
	}
	for _, populated := range []bool{false, true} {
		for _, object := range []string{"benchmark_series_latest_idx", "benchmark_series_versions_no_update", "benchmark_series_versions_no_delete", "benchmark_series_versions_insert_guard"} {
			t.Run(object+map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
				svc, _ := testService(t, nil, nil)
				if populated {
					importBenchmarkForTest(t, svc, []byte(benchmarkSeriesTestInput))
				}
				kind := "TRIGGER"
				if object == "benchmark_series_latest_idx" {
					kind = "INDEX"
				}
				if _, err := svc.db.Exec("DROP " + kind + " " + object); err != nil {
					t.Fatal(err)
				}
				if _, err := proveBenchmarkSeriesRecovery(context.Background(), svc.db); err == nil {
					t.Fatal("weakened physical schema accepted")
				}
				if err := requireServerStartupRecovery(svc.db); err == nil {
					t.Fatal("startup accepted weakened benchmark schema")
				}
			})
		}
	}
	for _, column := range []string{"series_sha256", "record_sha256", "series_json"} {
		t.Run(column, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			importBenchmarkForTest(t, svc, []byte(benchmarkSeriesTestInput))
			if _, err := svc.db.Exec(`DROP TRIGGER benchmark_series_versions_no_update`); err != nil {
				t.Fatal(err)
			}
			value := "'" + strings.Repeat("0", 64) + "'"
			if column == "series_json" {
				value = `json_set(series_json,'$.points[1].level','999')`
			}
			if _, err := svc.db.Exec("UPDATE benchmark_series_versions SET " + column + "=" + value); err != nil {
				t.Fatal(err)
			}
			script, err := migrationFiles.ReadFile("migrations/023_benchmark_series_versions.sql")
			if err != nil {
				t.Fatal(err)
			}
			for _, block := range strings.Split(string(script), "\n\n") {
				if strings.HasPrefix(block, "CREATE TRIGGER benchmark_series_versions_no_update\n") {
					if _, err := svc.db.Exec(block); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := requireBenchmarkSeriesSchema(context.Background(), svc.db); err != nil {
				t.Fatal(err)
			}
			if _, err := proveBenchmarkSeriesRecovery(context.Background(), svc.db); err == nil {
				t.Fatal("tampered journal accepted")
			}
			if _, err := readBenchmarkSeriesStatus(context.Background(), svc.db, "test_reference", ""); err == nil {
				t.Fatal("tampered status published")
			}
		})
	}
}

func TestBenchmarkSeriesLegacyV16PreservesReplayAuthorityAndSource(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	// Fractional-time chronology differs from v1's legacy sequence ordering.
	applyTestCSV(t, svc, "chronology", testCSV(
		"b0,account-main,BUY,2026-01-01T00:00:00Z,AAPL,1,10,0,USD,-10",
		"b1,account-main,BUY,2026-01-01T00:00:00.1Z,AAPL,1,20,0,USD,-20",
		"s1,account-main,SELL,2026-01-01T00:00:00.2Z,AAPL,1,30,0,USD,30",
	))
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "v16.db")
	manifestPath := backup + ".manifest.json"
	if _, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	dropBenchmarkSchemaForLegacy(t, db)
	if _, err := provePaperRunnerLeaseRecovery(context.Background(), db); err == nil {
		t.Fatal("current runner accepted a schema22 database")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fields := readJSONMap(t, manifestPath)
	stripBenchmarkProofForLegacy(fields)
	fields["format_version"], fields["schema_version"] = "omni-folio-backup.v16", "omni-folio.sqlite.v22"
	sha, size, err := hashFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	fields["db_sha256"], fields["size_bytes"], fields["verification_receipt"].(map[string]any)["candidate_db_sha256"] = sha, size, sha
	writeJSONFile(t, manifestPath, fields)
	beforeDB, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	beforeManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatalf("v16 lost v2 authority: %v", err)
	}
	afterDB, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	afterManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeDB, afterDB) || !bytes.Equal(beforeManifest, afterManifest) {
		t.Fatal("legacy verification changed source bytes")
	}
	wrongPolicy := readJSONMap(t, manifestPath)
	wrongPolicy["paper_performance_policy_state_sha256"] = strings.Repeat("0", 64)
	wrongPolicy["verification_receipt"].(map[string]any)["candidate_paper_performance_policy_state_sha256"] = strings.Repeat("0", 64)
	variant := filepath.Join(t.TempDir(), "wrong-policy.json")
	writeJSONFile(t, variant, wrongPolicy)
	if err := verifyManifest(backup, golden, variant); err == nil {
		t.Fatal("v16 skipped original source policy proof")
	}
	fields["benchmark_series_version_count"] = 0
	writeJSONFile(t, manifestPath, fields)
	if err := verifyManifest(backup, golden, manifestPath); err == nil {
		t.Fatal("v16 claimed future proof")
	}
}

func TestBenchmarkSeriesRejectsUnexpectedTriggerAndIndexBeforeWriting(t *testing.T) {
	for name, definition := range map[string]string{
		"ignore":      `CREATE TRIGGER unexpected_benchmark_trigger BEFORE INSERT ON benchmark_series_versions BEGIN SELECT RAISE(IGNORE); END`,
		"temp ignore": `CREATE TEMP TRIGGER unexpected_benchmark_trigger BEFORE INSERT ON main.benchmark_series_versions BEGIN SELECT RAISE(IGNORE); END`,
		"side effect": `CREATE TRIGGER unexpected_benchmark_trigger AFTER INSERT ON benchmark_series_versions BEGIN INSERT INTO benchmark_trigger_probe VALUES ('changed'); END`,
		"index":       `CREATE INDEX unexpected_benchmark_index ON benchmark_series_versions(recorded_at)`,
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			if _, err := svc.db.Exec(`CREATE TABLE benchmark_trigger_probe(value TEXT)`); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.db.Exec(definition); err != nil {
				t.Fatal(err)
			}
			receipt, err := svc.importBenchmarkSeries(context.Background(), []byte(benchmarkSeriesTestInput))
			if err == nil || receipt != nil {
				t.Fatalf("unexpected schema reported storage success: %+v %v", receipt, err)
			}
			var stored, sideEffects int
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM benchmark_series_versions`).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM benchmark_trigger_probe`).Scan(&sideEffects); err != nil {
				t.Fatal(err)
			}
			if stored != 0 || sideEffects != 0 {
				t.Fatalf("rejected schema wrote rows: benchmark=%d side_effects=%d", stored, sideEffects)
			}
			if _, err := proveBenchmarkSeriesRecovery(context.Background(), svc.db); err == nil {
				t.Fatal("recovery accepted extra schema object")
			}
		})
	}
}

func TestBenchmarkSeriesLegacyV16CannotActivateAnActiveRunnerLease(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	if _, err := svc.acquirePaperRunnerLease(context.Background(), k2aAccountRef); err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "active-v16.db")
	manifestPath := backup + ".manifest.json"
	if _, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	dropBenchmarkSchemaForLegacy(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fields := readJSONMap(t, manifestPath)
	stripBenchmarkProofForLegacy(fields)
	fields["format_version"], fields["schema_version"] = "omni-folio-backup.v16", "omni-folio.sqlite.v22"
	sha, size, err := hashFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	receipt := fields["verification_receipt"].(map[string]any)
	fields["db_sha256"], fields["size_bytes"], receipt["candidate_db_sha256"] = sha, size, sha
	// A forged activation claim must not hide the actually captured active lease.
	receipt["status"], receipt["eligible_for_activation"], receipt["errors"] = "verified", true, []string{}
	writeJSONFile(t, manifestPath, fields)
	if err := verifyManifest(backup, golden, manifestPath); err == nil {
		t.Fatal("active v16 runner became eligible")
	}
}
