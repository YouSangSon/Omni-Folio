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

func dropOwnerPriceSchemaForLegacy(t testing.TB, db *sql.DB) {
	dropWatchlistSchemaForLegacy(t, db)
	t.Helper()
	var present int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='owner_price_series_versions'`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present != 0 {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM owner_price_series_versions`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("legacy fixture cannot discard declared prices")
		}
		if _, err := db.Exec(`DROP TRIGGER security_price_observations_owner_guard; DROP TABLE owner_price_series_versions`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version=25`); err != nil {
		t.Fatal(err)
	}
}

func stripOwnerPriceProofForLegacy(manifest map[string]any) {
	stripWatchlistProofForLegacy(manifest)
	for _, key := range []string{"owner_price_series_state_sha256", "owner_price_series_version_count", "owner_price_series_point_count"} {
		delete(manifest, key)
	}
	r := manifest["verification_receipt"].(map[string]any)
	for _, key := range []string{"owner_price_series_check", "candidate_owner_price_series_state_sha256", "candidate_owner_price_series_version_count", "candidate_owner_price_series_point_count"} {
		delete(r, key)
	}
}

func TestOwnerPricePhysicalAndTemporalProofFailsClosed(t *testing.T) {
	svc := ownerPriceServiceForTest(t)
	svc.db.SetMaxOpenConns(1)
	if _, err := svc.db.Exec(`CREATE TEMP TRIGGER owner_price_ignore BEFORE INSERT ON owner_price_series_versions BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.importOwnerPriceSeries(context.Background(), []byte(ownerPriceTestInput)); err == nil {
		t.Fatal("extra ignore trigger accepted")
	}
}

func TestOwnerPriceBackupWholeVersionsProofAndWithdrawal(t *testing.T) {
	svc := ownerPriceServiceForTest(t)
	first := importOwnerPriceForTest(t, svc, []byte(ownerPriceTestInput))
	importOwnerPriceForTest(t, svc, ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) {
		i.SourceVersion = "v2"
		i.Points = i.Points[1:]
		i.Points[0].Price = "300"
	}))
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "owner-price.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := proveOwnerPriceSeriesRecovery(context.Background(), svc.db)
	if err != nil || proof.Versions != 2 || proof.Points != 3 || !ownerPriceManifestMatches(*manifest, proof) {
		t.Fatalf("owner price proof mismatch: %+v %v", proof, err)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
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
	stored, _, err := replayOwnerPriceSeries(context.Background(), db, "instrument_aapl", "USD", "price-v1")
	if err != nil || stored == nil || ownerPriceReceipt(stored).DataSHA256 != first.DataSHA256 {
		t.Fatal("original price version lost")
	}
	value, appErr := newService(db, svc.now, svc.id).holdingValuation(context.Background(), "2026-01-01T00:00:00Z")
	if appErr != nil || value.Status != "complete" || value.Lines[0].MarketValue == nil || *value.Lines[0].MarketValue != "300" || !usesOwnerPrice(value.Issues) {
		t.Fatalf("restored latest price drift: %+v %v", value, appErr)
	}
	for _, key := range []string{"owner_price_series_state_sha256", "owner_price_series_version_count", "owner_price_series_point_count", "owner_price_series_check", "candidate_owner_price_series_state_sha256", "candidate_owner_price_series_version_count", "candidate_owner_price_series_point_count"} {
		for _, kind := range []string{"missing", "null", "changed"} {
			fields := readJSONMap(t, manifestPath)
			target := fields
			if key == "owner_price_series_check" || strings.HasPrefix(key, "candidate_") {
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
				} else if key == "owner_price_series_check" {
					target[key] = "failed"
				} else {
					target[key] = 99
				}
			}
			path := filepath.Join(t.TempDir(), "manifest.json")
			writeJSONFile(t, path, fields)
			if err := verifyManifest(backup, golden, path); err == nil {
				t.Fatalf("unproved price field accepted: %s %s", key, kind)
			}
		}
	}
	importOwnerPriceForTest(t, svc, ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.SourceVersion = "withdrawn"; i.Points = []OwnerPricePoint{} }))
	withdrawn := filepath.Join(t.TempDir(), "withdrawn.db")
	wm := withdrawn + ".manifest.json"
	if _, err := createBackup(svc.db, withdrawn, golden, wm, svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(withdrawn, golden, wm); err != nil {
		t.Fatal(err)
	}
	wdb, err := openExistingDB(withdrawn)
	if err != nil {
		t.Fatal(err)
	}
	defer wdb.Close()
	copySvc := newService(wdb, svc.now, svc.id)
	marks, err := valuationPriceObservations(context.Background(), wdb, svc.now())
	if err != nil || len(marks) != 0 {
		t.Fatal("restored withdrawal borrowed older points")
	}
	if _, err := copySvc.recordSecurityPriceObservation(context.Background(), SecurityPriceObservationInput{Source: "local_fixture", SourceObservationID: "blocked", InstrumentID: "instrument_aapl", Symbol: "AAPL", Venue: "XNAS", Currency: "USD", Price: "1", PriceAdjustment: "unspecified", ObservedAt: "2026-01-01T00:00:00Z", FetchedAt: "2026-01-01T00:00:00Z"}); err == nil {
		t.Fatal("restored withdrawal released source binding")
	}
}

func TestOwnerPriceLegacyV18PreservesOwnerFXAndNonarchivalReplay(t *testing.T) {
	svc := ownerPriceServiceForTest(t)
	importOwnerFXForTest(t, svc, []byte(ownerFXTestInput))
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "v18.db")
	manifestPath := backup + ".manifest.json"
	if _, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	dropOwnerPriceSchemaForLegacy(t, db)
	if _, err := provePaperRunnerLeaseRecovery(context.Background(), db); err == nil {
		t.Fatal("current runner accepted schema24")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	fields := readJSONMap(t, manifestPath)
	stripOwnerPriceProofForLegacy(fields)
	fields["format_version"], fields["schema_version"] = "omni-folio-backup.v18", "omni-folio.sqlite.v24"
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
		t.Fatalf("v18 lost owner FX/source replay authority: %v", err)
	}
	after, err := os.ReadFile(backup)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("legacy source changed")
	}
	for _, key := range []string{"owner_fx_series_state_sha256", "paper_runner_lease_state_sha256"} {
		v := readJSONMap(t, manifestPath)
		v[key] = strings.Repeat("0", 64)
		path := filepath.Join(t.TempDir(), "proof.json")
		writeJSONFile(t, path, v)
		if err := verifyManifest(backup, golden, path); err == nil {
			t.Fatalf("v18 omitted %s source proof", key)
		}
	}
	for _, value := range []any{nil, 0} {
		v := readJSONMap(t, manifestPath)
		v["owner_price_series_version_count"] = value
		path := filepath.Join(t.TempDir(), "claim.json")
		writeJSONFile(t, path, v)
		if err := verifyManifest(backup, golden, path); err == nil {
			t.Fatal("v18 claimed new price authority")
		}
	}
	precreated := filepath.Join(t.TempDir(), "precreated.db")
	if err := os.WriteFile(precreated, before, 0600); err != nil {
		t.Fatal(err)
	}
	pdb, err := openExistingDB(precreated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pdb.Exec(`CREATE TABLE owner_price_series_versions (sequence INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := pdb.Close(); err != nil {
		t.Fatal(err)
	}
	sha, size, err = hashFile(precreated)
	if err != nil {
		t.Fatal(err)
	}
	fields["db_sha256"], fields["size_bytes"], fields["verification_receipt"].(map[string]any)["candidate_db_sha256"] = sha, size, sha
	path := precreated + ".manifest.json"
	writeJSONFile(t, path, fields)
	if err := verifyManifest(precreated, golden, path); err == nil {
		t.Fatal("legacy source smuggled a price journal")
	}
}

func TestOwnerPriceLegacyV18CannotHideActiveLease(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	if _, err := svc.acquirePaperRunnerLease(context.Background(), k2aAccountRef); err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "active-v18.db")
	mp := backup + ".manifest.json"
	if _, err := createBackup(svc.db, backup, golden, mp, svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	dropOwnerPriceSchemaForLegacy(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	f := readJSONMap(t, mp)
	stripOwnerPriceProofForLegacy(f)
	f["format_version"], f["schema_version"] = "omni-folio-backup.v18", "omni-folio.sqlite.v24"
	sha, size, err := hashFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	r := f["verification_receipt"].(map[string]any)
	f["db_sha256"], f["size_bytes"], r["candidate_db_sha256"] = sha, size, sha
	r["status"], r["eligible_for_activation"], r["errors"] = "verified", true, []string{}
	writeJSONFile(t, mp, f)
	if err := verifyManifest(backup, golden, mp); err == nil {
		t.Fatal("active v18 lease became eligible")
	}
}
