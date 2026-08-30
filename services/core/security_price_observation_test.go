package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func securityPriceInput() SecurityPriceObservationInput {
	return SecurityPriceObservationInput{
		Source: "local_fixture", SourceObservationID: "aapl_close_20260110", InstrumentID: "US0378331005",
		Symbol: "AAPL", Venue: "XNAS", Currency: "USD", Price: "250.25",
		PriceAdjustment: marketDataAdjustmentUnspecified, ObservedAt: "2026-01-10T15:00:00Z", FetchedAt: "2026-01-10T15:00:01Z",
	}
}

func TestSecurityPriceObservationAcceptsCanonicalInternalInstrumentID(t *testing.T) {
	svc, _ := testService(t, []time.Time{mustTime("2026-01-10T15:02:00Z")}, nil)
	input := securityPriceInput()
	input.InstrumentID = "instrument_aapl"

	stored, err := svc.recordSecurityPriceObservation(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if stored.InstrumentID != "instrument_aapl" || stored.Symbol != "AAPL" || stored.Venue != "XNAS" {
		t.Fatalf("stored identity drifted: %+v", stored)
	}
	replayed, _, err := replaySecurityPriceObservations(context.Background(), svc.db)
	if err != nil || len(replayed) != 1 || replayed[0].InstrumentID != "instrument_aapl" || replayed[0].Symbol != "AAPL" || replayed[0].Venue != "XNAS" {
		t.Fatalf("replayed identity drifted: observations=%+v err=%v", replayed, err)
	}
	latest, err := latestSecurityPriceObservation(context.Background(), svc.db, input.Source, input.InstrumentID, input.Symbol, input.Venue, input.Currency, input.PriceAdjustment, "2026-01-10T15:03:00Z")
	if err != nil || latest.SourceObservationID != input.SourceObservationID || latest.InstrumentID != "instrument_aapl" || latest.Symbol != "AAPL" || latest.Venue != "XNAS" {
		t.Fatalf("exact latest lookup drifted: observation=%+v err=%v", latest, err)
	}
}

func TestSecurityPriceObservationRecordReplayConflictAndSnapshotBoundary(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	ctx := context.Background()
	first := securityPriceInput()
	stored, err := svc.recordSecurityPriceObservation(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if !canonicalUTCString(stored.ObservedAt) || !canonicalUTCString(stored.FetchedAt) || !canonicalUTCString(stored.RecordedAt) {
		t.Fatalf("stored timestamps are not canonical UTC: %+v", stored)
	}
	replayed, err := svc.recordSecurityPriceObservation(ctx, first)
	if err != nil || !reflect.DeepEqual(stored, replayed) {
		t.Fatalf("exact replay drifted: stored=%+v replayed=%+v err=%v", stored, replayed, err)
	}
	changed := first
	changed.Price = "251"
	if _, err := svc.recordSecurityPriceObservation(ctx, changed); err == nil {
		t.Fatal("source observation identity was rebound to a different price")
	}
	sameSlot := first
	sameSlot.SourceObservationID = "aapl_close_other"
	if _, err := svc.recordSecurityPriceObservation(ctx, sameSlot); err == nil {
		t.Fatal("source/instrument/venue/currency/time/adjustment slot accepted two identities")
	}
	second := first
	second.SourceObservationID = "aapl_close_20260111"
	second.Price = "252"
	second.ObservedAt = "2026-01-10T15:00:02Z"
	second.FetchedAt = "2026-01-10T15:00:03Z"
	if _, err := svc.recordSecurityPriceObservation(ctx, second); err != nil {
		t.Fatal(err)
	}
	observations, proof, err := replaySecurityPriceObservations(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 2 || observations[0].SourceObservationID != first.SourceObservationID ||
		observations[1].SourceObservationID != second.SourceObservationID || observations[0].Price != first.Price ||
		observations[1].Price != second.Price || proof.Observations != 2 || len(proof.SHA256) != 64 {
		t.Fatalf("security price series/proof drifted: observations=%+v proof=%+v", observations, proof)
	}
	snapshot, err := snapshotFrom(ctx, svc.db)
	if err != nil || snapshot.ValuationStatus != "unavailable" {
		t.Fatalf("price storage changed valuation authority: snapshot=%+v err=%v", snapshot, err)
	}
}

func TestSecurityPriceObservationRejectsInvalidBoundaryAndDirectMutation(t *testing.T) {
	valid := securityPriceInput()
	tests := map[string]func(*SecurityPriceObservationInput){
		"empty source id":         func(v *SecurityPriceObservationInput) { v.SourceObservationID = "" },
		"unknown source":          func(v *SecurityPriceObservationInput) { v.Source = "kiwoom" },
		"empty instrument":        func(v *SecurityPriceObservationInput) { v.InstrumentID = "" },
		"lower symbol":            func(v *SecurityPriceObservationInput) { v.Symbol = "aapl" },
		"empty venue":             func(v *SecurityPriceObservationInput) { v.Venue = "" },
		"lower currency":          func(v *SecurityPriceObservationInput) { v.Currency = "usd" },
		"zero price":              func(v *SecurityPriceObservationInput) { v.Price = "0" },
		"negative price":          func(v *SecurityPriceObservationInput) { v.Price = "-1" },
		"exponent price":          func(v *SecurityPriceObservationInput) { v.Price = "1e3" },
		"trailing zero price":     func(v *SecurityPriceObservationInput) { v.Price = "250.250" },
		"unknown adjustment":      func(v *SecurityPriceObservationInput) { v.PriceAdjustment = "split_adjusted" },
		"provider adjustment":     func(v *SecurityPriceObservationInput) { v.PriceAdjustment = marketDataAdjustmentProviderAdjusted },
		"offset observed":         func(v *SecurityPriceObservationInput) { v.ObservedAt = "2026-01-11T00:00:00+09:00" },
		"offset fetched":          func(v *SecurityPriceObservationInput) { v.FetchedAt = "2026-01-11T00:00:01+09:00" },
		"fetched before observed": func(v *SecurityPriceObservationInput) { v.FetchedAt = "2026-01-10T14:59:59Z" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			observation := valid
			mutate(&observation)
			if _, err := svc.recordSecurityPriceObservation(context.Background(), observation); err == nil {
				t.Fatalf("invalid security price observation was accepted: %+v", observation)
			}
		})
	}
	recordedBeforeFetched, _ := testService(t, []time.Time{mustTime("2026-01-10T15:00:00Z")}, nil)
	if _, err := recordedBeforeFetched.recordSecurityPriceObservation(context.Background(), valid); err == nil {
		t.Fatal("security price observation was recorded before its fetched_at timestamp")
	}

	svc, _ := testService(t, nil, nil)
	var strict int
	if err := svc.db.QueryRow(`SELECT strict FROM pragma_table_list WHERE schema='main' AND type='table' AND name='security_price_observations'`).Scan(&strict); err != nil || strict != 1 {
		t.Fatalf("security price table is not STRICT: strict=%d err=%v", strict, err)
	}
	if _, err := svc.db.Exec(`INSERT INTO security_price_observations(observation_id,source,source_observation_id,instrument_id,symbol,venue,currency,price,price_adjustment,observed_at,fetched_at,record_sha256,recorded_at) VALUES('bad-price','local_fixture','bad-price','US0378331005','AAPL','XNAS','USD','250.250','unspecified','2026-01-10T15:00:00Z','2026-01-10T15:00:01Z','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','2026-01-10T15:00:02Z')`); err == nil {
		t.Fatal("STRICT storage accepted a non-canonical direct price")
	}
	if _, err := svc.recordSecurityPriceObservation(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE security_price_observations SET price=price`,
		`DELETE FROM security_price_observations`,
	} {
		if _, err := svc.db.Exec(statement); err == nil {
			t.Fatalf("insert-only security price storage accepted mutation: %s", statement)
		}
	}
}

func TestSecurityPriceObservationLatestIsExactAndExcludesFutureKnowledge(t *testing.T) {
	svc, _ := testService(t, []time.Time{
		mustTime("2026-01-10T15:02:00Z"), mustTime("2026-01-10T15:03:00Z"), mustTime("2026-01-10T15:05:00Z"), mustTime("2026-01-10T15:07:00Z"),
	}, nil)
	current := securityPriceInput()
	futureRecorded := current
	futureRecorded.SourceObservationID = "aapl_future_recorded"
	futureRecorded.Price = "250.5"
	futureRecorded.ObservedAt = "2026-01-10T14:59:00Z"
	futureRecorded.FetchedAt = "2026-01-10T14:59:01Z"
	futureFetched := current
	futureFetched.SourceObservationID = "aapl_future_fetch"
	futureFetched.Price = "251"
	futureFetched.ObservedAt = "2026-01-10T15:01:00Z"
	futureFetched.FetchedAt = "2026-01-10T15:05:00Z"
	futureObserved := current
	futureObserved.SourceObservationID = "aapl_future_observed"
	futureObserved.Price = "252"
	futureObserved.ObservedAt = "2026-01-10T15:05:00Z"
	futureObserved.FetchedAt = "2026-01-10T15:05:01Z"
	for _, input := range []SecurityPriceObservationInput{current, futureRecorded, futureFetched, futureObserved} {
		if _, err := svc.recordSecurityPriceObservation(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	lookup := func(source, instrumentID, symbol, venue, currency, adjustment string) (*SecurityPriceObservation, error) {
		return latestSecurityPriceObservation(context.Background(), svc.db, source, instrumentID, symbol, venue, currency, adjustment, "2026-01-10T15:02:00Z")
	}
	got, err := lookup(current.Source, current.InstrumentID, current.Symbol, current.Venue, current.Currency, current.PriceAdjustment)
	if err != nil || got.SourceObservationID != current.SourceObservationID || got.Price != current.Price {
		t.Fatalf("exact as-of lookup drifted: observation=%+v err=%v", got, err)
	}
	for _, mismatch := range [][6]string{
		{"kiwoom", current.InstrumentID, current.Symbol, current.Venue, current.Currency, current.PriceAdjustment},
		{current.Source, "US5949181045", current.Symbol, current.Venue, current.Currency, current.PriceAdjustment},
		{current.Source, current.InstrumentID, "MSFT", current.Venue, current.Currency, current.PriceAdjustment},
		{current.Source, current.InstrumentID, current.Symbol, "XNYS", current.Currency, current.PriceAdjustment},
		{current.Source, current.InstrumentID, current.Symbol, current.Venue, "KRW", current.PriceAdjustment},
		{current.Source, current.InstrumentID, current.Symbol, current.Venue, current.Currency, marketDataAdjustmentProviderAdjusted},
	} {
		if observation, err := lookup(mismatch[0], mismatch[1], mismatch[2], mismatch[3], mismatch[4], mismatch[5]); err == nil || observation != nil {
			t.Fatalf("exact lookup accepted mismatched identity: observation=%+v err=%v", observation, err)
		}
	}
}

func TestSecurityPriceObservationReplayFailsClosedOnCorruption(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	if _, err := svc.recordSecurityPriceObservation(context.Background(), securityPriceInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER security_price_observations_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE security_price_observations SET price='251'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := replaySecurityPriceObservations(context.Background(), svc.db); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("corrupt security price observation was certified: %v", err)
	}
}

func TestSecurityPriceObservationRestoreRejectsWeakAdjustmentConstraint(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "security-prices-weak-adjustment.db")
	if _, err := createBackup(svc.db, backup, golden, backup+".manifest.json", svc.now, svc.id); err != nil {
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
	if _, err := candidate.Exec(`UPDATE sqlite_master SET sql=replace(sql, "price_adjustment = 'unspecified'", "price_adjustment <> ''") WHERE type='table' AND name='security_price_observations'`); err != nil {
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
		t.Fatal("restore accepted security prices without the pinned price_adjustment constraint")
	}
}

func TestSecurityPriceObservationBackupProofAndLegacyV10CopyMigration(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	if _, err := svc.recordSecurityPriceObservation(context.Background(), securityPriceInput()); err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "security-prices.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v7" || manifest.SchemaVersion != "omni-folio.sqlite.v11" ||
		manifest.SecurityPriceObservationCount != 1 || len(manifest.SecurityPriceObservationStateSHA256) != 64 ||
		manifest.VerificationReceipt.SecurityPriceObservationCheck != "ok" ||
		manifest.VerificationReceipt.CandidateSecurityPriceObservationStateSHA256 != manifest.SecurityPriceObservationStateSHA256 {
		t.Fatalf("backup omitted security price proof: %+v", manifest)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err != nil {
		t.Fatal(err)
	}
	candidate, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`DROP TRIGGER security_price_observations_no_delete`); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err == nil {
		t.Fatal("restore accepted a security price table without its insert-only trigger")
	}
	indexBackup := filepath.Join(t.TempDir(), "security-prices-missing-index.db")
	if _, err := createBackup(svc.db, indexBackup, golden, indexBackup+".manifest.json", svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	candidate, err = openExistingDB(indexBackup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`DROP INDEX security_price_observations_latest_idx`); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(indexBackup, golden); err == nil {
		t.Fatal("restore accepted security prices without the exact latest index")
	}

	if _, err := svc.db.Exec(`DROP TRIGGER security_price_observations_no_update; DROP TRIGGER security_price_observations_no_delete; DROP TABLE security_price_observations; DELETE FROM schema_migrations WHERE version=11`); err != nil {
		t.Fatal(err)
	}
	legacyBackup := filepath.Join(t.TempDir(), "legacy-v10.db")
	if _, err := svc.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(legacyBackup, "'", "''") + `'`); err != nil {
		t.Fatal(err)
	}
	legacyManifest := readJSONMap(t, manifestPath)
	legacyManifest["format_version"] = "omni-folio-backup.v6"
	legacyManifest["schema_version"] = "omni-folio.sqlite.v10"
	delete(legacyManifest, "security_price_observation_state_sha256")
	delete(legacyManifest, "security_price_observation_count")
	receipt := legacyManifest["verification_receipt"].(map[string]any)
	delete(receipt, "security_price_observation_check")
	delete(receipt, "candidate_security_price_observation_state_sha256")
	sha, size, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	legacyManifest["db_sha256"] = sha
	legacyManifest["size_bytes"] = size
	legacyManifest["verification_receipt"].(map[string]any)["candidate_db_sha256"] = sha
	legacyManifestPath := filepath.Join(t.TempDir(), "legacy-v10.manifest.json")
	writeJSONFile(t, legacyManifestPath, legacyManifest)
	beforeSHA, beforeSize, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(legacyBackup, golden, legacyManifestPath); err != nil {
		t.Fatal(err)
	}
	mixed := readJSONMap(t, legacyManifestPath)
	mixed["security_price_observation_state_sha256"] = strings.Repeat("0", 64)
	mixed["security_price_observation_count"] = float64(0)
	mixedReceipt := mixed["verification_receipt"].(map[string]any)
	mixedReceipt["security_price_observation_check"] = "ok"
	mixedReceipt["candidate_security_price_observation_state_sha256"] = strings.Repeat("0", 64)
	mixedPath := filepath.Join(t.TempDir(), "mixed-v6-v7.manifest.json")
	writeJSONFile(t, mixedPath, mixed)
	if err := verifyManifest(legacyBackup, golden, mixedPath); err == nil {
		t.Fatal("legacy v6 manifest with v7-only security price fields was accepted")
	}
	afterSHA, afterSize, err := hashFile(legacyBackup)
	if err != nil || beforeSHA != afterSHA || beforeSize != afterSize {
		t.Fatalf("legacy v10 source was changed during copy migration: before=(%s,%d) after=(%s,%d) err=%v", beforeSHA, beforeSize, afterSHA, afterSize, err)
	}
	var version int
	if err := svc.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 10 {
		t.Fatalf("legacy v10 source was migrated in place: version=%d err=%v", version, err)
	}
}
