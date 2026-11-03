package main

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestInstrumentListingDeclareResolveRevokeAndCorrect(t *testing.T) {
	svc, _ := testService(t, []time.Time{
		mustTime("2026-08-30T03:00:00Z"),
		mustTime("2026-08-30T03:01:00Z"),
		mustTime("2026-08-30T03:02:00Z"),
	}, nil)
	samsung := InstrumentListingInput{
		InstrumentID: "instrument_005930", Venue: "XKRX", Symbol: "005930", Currency: "KRW",
	}

	declared, err := svc.declareInstrumentListing(context.Background(), samsung)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := svc.declareInstrumentListing(context.Background(), samsung)
	if err != nil || !reflect.DeepEqual(declared, replayed) {
		t.Fatalf("idempotent declaration drifted: declared=%+v replayed=%+v err=%v", declared, replayed, err)
	}
	conflicting := samsung
	conflicting.InstrumentID = "instrument_other"
	if _, err := svc.declareInstrumentListing(context.Background(), conflicting); err == nil {
		t.Fatal("active listing identity was rebound")
	}
	if _, err := svc.revokeInstrumentListing(context.Background(), samsung); err != nil {
		t.Fatal(err)
	}
	if listing, err := resolveInstrumentListing(context.Background(), svc.db, "XKRX", "005930", "KRW"); err == nil || listing != nil {
		t.Fatalf("revoked listing resolved: listing=%+v err=%v", listing, err)
	}
	corrected, err := svc.declareInstrumentListing(context.Background(), conflicting)
	if err != nil || corrected.InstrumentID != "instrument_other" {
		t.Fatalf("corrected listing=%+v err=%v", corrected, err)
	}

	active, proof, err := replayInstrumentListingRegistry(context.Background(), svc.db)
	if err != nil || len(active) != 1 || proof.Events != 3 || proof.Active != 1 {
		t.Fatalf("listing replay active=%+v proof=%+v err=%v", active, proof, err)
	}
}

func TestInstrumentListingRejectsStaleWritesAndBindsBackupProof(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	listing := InstrumentListingInput{
		InstrumentID: "instrument_005930", Venue: "XKRX", Symbol: "005930", Currency: "KRW",
	}
	declared, err := svc.declareInstrumentListing(context.Background(), listing)
	if err != nil {
		t.Fatal(err)
	}
	stale := InstrumentListingEvent{
		EventID: "instrument_listing_stale", EventType: instrumentListingDeclare, Authority: instrumentListingAuthority,
		InstrumentID: listing.InstrumentID, Venue: listing.Venue, Symbol: listing.Symbol, Currency: listing.Currency,
		ExpectedPreviousEventID: noInstrumentListingEvent, RecordedAt: "2026-08-30T03:05:00Z",
	}
	stale.recordSHA256, err = instrumentListingEventSHA(stale)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO instrument_listing_events(
		event_id,event_type,authority,instrument_id,venue,symbol,currency,expected_previous_event_id,record_sha256,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?)`, stale.EventID, stale.EventType, stale.Authority, stale.InstrumentID,
		stale.Venue, stale.Symbol, stale.Currency, stale.ExpectedPreviousEventID, stale.recordSHA256, stale.RecordedAt); err == nil {
		t.Fatal("stale listing transition was stored")
	}
	if _, err := svc.db.Exec(`UPDATE instrument_listing_events SET instrument_id='instrument_other' WHERE event_id=?`, declared.EventID); err == nil {
		t.Fatal("listing event was updated")
	}
	if _, err := svc.db.Exec(`DELETE FROM instrument_listing_events WHERE event_id=?`, declared.EventID); err == nil {
		t.Fatal("listing event was deleted")
	}

	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "listing.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.InstrumentListingEventCount != 1 || manifest.ActiveInstrumentListingCount != 1 ||
		len(manifest.InstrumentListingStateSHA256) != 64 || manifest.VerificationReceipt.InstrumentListingCheck != "ok" ||
		manifest.VerificationReceipt.CandidateInstrumentListingStateSHA256 != manifest.InstrumentListingStateSHA256 {
		t.Fatalf("backup omitted instrument listing proof: %+v", manifest)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}
	tampered := readJSONMap(t, manifestPath)
	tampered["active_instrument_listing_count"] = float64(2)
	tamperedPath := filepath.Join(t.TempDir(), "listing-tampered.manifest.json")
	writeJSONFile(t, tamperedPath, tampered)
	if err := verifyManifest(backup, golden, tamperedPath); err == nil {
		t.Fatal("backup with a mismatched instrument listing proof was accepted")
	}
	weakBackup := filepath.Join(t.TempDir(), "listing-missing-trigger.db")
	if _, err := createBackup(svc.db, weakBackup, golden, weakBackup+".manifest.json", svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	candidate, err := openExistingDB(weakBackup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`DROP TRIGGER instrument_listing_events_state_guard`); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(weakBackup, golden); err == nil {
		t.Fatal("restore accepted instrument listings without the exact state guard")
	}
}
