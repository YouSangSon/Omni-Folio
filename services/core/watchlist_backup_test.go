package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func dropWatchlistSchemaForLegacy(t testing.TB, db *sql.DB) {
	t.Helper()
	var present int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='watchlist_state'`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present != 0 {
		state, _, err := readWatchlistState(context.Background(), db)
		if err != nil || state.Revision != 0 || len(state.Entries) != 0 {
			t.Fatal("legacy fixture cannot discard watchlist state", err)
		}
		if _, err := db.Exec(`DROP TABLE watchlist_state`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version=26`); err != nil {
		t.Fatal(err)
	}
}

func stripWatchlistProofForLegacy(manifest map[string]any) {
	for _, key := range []string{"watchlist_state_sha256", "watchlist_revision", "watchlist_entry_count"} {
		delete(manifest, key)
	}
	receipt := manifest["verification_receipt"].(map[string]any)
	for _, key := range []string{"watchlist_check", "candidate_watchlist_state_sha256", "candidate_watchlist_revision", "candidate_watchlist_entry_count"} {
		delete(receipt, key)
	}
}

func TestWatchlistBackupRestoresCurrentStateAndRejectsUnprovedFields(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	watchlistWriteForTest(t, svc, 0, watchAAPL, watchKR)
	saved := watchlistWriteForTest(t, svc, 1, watchAAPL)
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "watchlist.db")
	mp := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, mp, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v20" || manifest.SchemaVersion != "omni-folio.sqlite.v26" || manifest.WatchlistRevision != 2 || manifest.WatchlistEntryCount != 1 {
		t.Fatal("watchlist manifest missing")
	}
	if err := verifyManifest(backup, golden, mp); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	restored, proof, err := readWatchlistState(context.Background(), db)
	if err != nil || !reflect.DeepEqual(restored, saved) || !watchlistManifestMatches(*manifest, proof) {
		t.Fatal("saved state changed during restore", err)
	}
	for _, key := range []string{"watchlist_state_sha256", "watchlist_revision", "watchlist_entry_count", "watchlist_check", "candidate_watchlist_state_sha256", "candidate_watchlist_revision", "candidate_watchlist_entry_count"} {
		for _, kind := range []string{"missing", "null", "changed"} {
			f := readJSONMap(t, mp)
			target := f
			if key == "watchlist_check" || strings.HasPrefix(key, "candidate_") {
				target = f["verification_receipt"].(map[string]any)
			}
			switch kind {
			case "missing":
				delete(target, key)
			case "null":
				target[key] = nil
			default:
				if strings.HasSuffix(key, "sha256") {
					target[key] = strings.Repeat("0", 64)
				} else if key == "watchlist_check" {
					target[key] = "failed"
				} else {
					target[key] = 99
				}
			}
			altered := filepath.Join(t.TempDir(), "altered.json")
			writeJSONFile(t, altered, f)
			if err := verifyManifest(backup, golden, altered); err == nil {
				t.Fatal("unproved watchlist field accepted", key, kind)
			}
		}
	}
}

func TestWatchlistLegacyV19PreservesPricesAndReplayAuthority(t *testing.T) {
	svc := ownerPriceServiceForTest(t)
	importOwnerPriceForTest(t, svc, []byte(ownerPriceTestInput))
	importOwnerFXForTest(t, svc, []byte(ownerFXTestInput))
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "v19.db")
	mp := backup + ".manifest.json"
	if _, err := createBackup(svc.db, backup, golden, mp, svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	dropWatchlistSchemaForLegacy(t, db)
	if _, err := provePaperRunnerLeaseRecovery(context.Background(), db); err == nil {
		t.Fatal("current schema accepted legacy25")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	f := readJSONMap(t, mp)
	stripWatchlistProofForLegacy(f)
	f["format_version"], f["schema_version"] = "omni-folio-backup.v19", "omni-folio.sqlite.v25"
	sha, size, err := hashFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	f["db_sha256"], f["size_bytes"], f["verification_receipt"].(map[string]any)["candidate_db_sha256"] = sha, size, sha
	writeJSONFile(t, mp, f)
	before, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(backup, golden, mp); err != nil {
		t.Fatal("v19 source lost owner-price/FX/nonarchival authority", err)
	}
	after, err := os.ReadFile(backup)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("legacy source modified")
	}
	for _, key := range []string{"owner_price_series_state_sha256", "owner_fx_series_state_sha256", "paper_runner_lease_state_sha256"} {
		changed := readJSONMap(t, mp)
		changed[key] = strings.Repeat("0", 64)
		path := filepath.Join(t.TempDir(), "bad-proof.json")
		writeJSONFile(t, path, changed)
		if err := verifyManifest(backup, golden, path); err == nil {
			t.Fatal("legacy source proof ignored", key)
		}
	}
	for _, value := range []any{nil, 0} {
		changed := readJSONMap(t, mp)
		changed["watchlist_revision"] = value
		path := filepath.Join(t.TempDir(), "new-field.json")
		writeJSONFile(t, path, changed)
		if err := verifyManifest(backup, golden, path); err == nil {
			t.Fatal("legacy source claimed new state")
		}
	}
}

func TestWatchlistLegacyV19CannotHideActiveLease(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	if _, err := svc.acquirePaperRunnerLease(context.Background(), k2aAccountRef); err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "active-v19.db")
	mp := backup + ".manifest.json"
	if _, err := createBackup(svc.db, backup, golden, mp, svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	dropWatchlistSchemaForLegacy(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	f := readJSONMap(t, mp)
	stripWatchlistProofForLegacy(f)
	f["format_version"], f["schema_version"] = "omni-folio-backup.v19", "omni-folio.sqlite.v25"
	sha, size, err := hashFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	r := f["verification_receipt"].(map[string]any)
	f["db_sha256"], f["size_bytes"], r["candidate_db_sha256"] = sha, size, sha
	r["status"], r["eligible_for_activation"], r["errors"] = "verified", true, []string{}
	writeJSONFile(t, mp, f)
	if err := verifyManifest(backup, golden, mp); err == nil {
		t.Fatal("active legacy runner became eligible")
	}
}
