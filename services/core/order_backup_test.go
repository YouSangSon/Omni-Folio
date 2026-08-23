package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	if manifest.FormatVersion != "omni-folio-backup.v2" || manifest.SchemaVersion != "omni-folio.sqlite.v2" {
		t.Fatalf("backup did not declare the order-aware schema: %+v", manifest)
	}
	if manifest.OrderStateSHA256 == "" || manifest.OrderCount != 1 || manifest.OrderEventCount != 3 {
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
