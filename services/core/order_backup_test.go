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
	if manifest.FormatVersion != "omni-folio-backup.v6" || manifest.SchemaVersion != "omni-folio.sqlite.v10" {
		t.Fatalf("backup did not declare the order-aware schema: %+v", manifest)
	}
	if manifest.OrderStateSHA256 == "" || manifest.OrderCount != 1 || manifest.OrderEventCount != 3 ||
		manifest.ExecutionAuthorityEventCount != 2 || manifest.RiskReservationCount != 1 {
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

func TestK2ABackupRejectsCorruptOrderRows(t *testing.T) {
	t.Run("missing order storage", func(t *testing.T) {
		db, err := openDB(filepath.Join(t.TempDir(), "unmigrated.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := proveOrderRecovery(context.Background(), db); err == nil {
			t.Fatal("database without order storage was certified")
		}
	})

	t.Run("malformed intent", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		if _, err := svc.db.Exec(`INSERT INTO order_idempotency(provider,mode,account_ref,client_order_id,request_sha256,order_id,intent_json,recorded_at) VALUES(?,?,?,?,?,?,?,?)`,
			"kiwoom", "synthetic", k2aAccountRef, "client-malformed-intent", strings.Repeat("0", 64), "order_malformed_intent", "{", "2026-01-10T15:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("malformed durable order intent was certified")
		}
	})

	t.Run("intent hash mismatch", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		intent := k2aIntent("client-corrupt-intent")
		intentJSON, _, err := orderJSONHash(intent)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`INSERT INTO order_idempotency(provider,mode,account_ref,client_order_id,request_sha256,order_id,intent_json,recorded_at) VALUES(?,?,?,?,?,?,?,?)`,
			intent.Provider, intent.Mode, intent.AccountRef, intent.ClientOrderID, strings.Repeat("0", 64), "order_corrupt_intent", string(intentJSON), "2026-01-10T15:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("order intent with a mismatched durable hash was certified")
		}
	})

	t.Run("orphan event", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		event := k2aEvent("orphan-event", "order_orphan", "INTENT_RECORDED")
		eventJSON, eventSHA, err := orderJSONHash(event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`INSERT INTO order_events(event_id,event_sha256,order_id,event_type,source,event_json,recorded_at) VALUES(?,?,?,?,?,?,?)`,
			event.EventID, eventSHA, event.OrderID, event.Type, event.Source, string(eventJSON), "2026-01-10T15:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("order event without a durable intent was certified")
		}
	})

	t.Run("event hash mismatch", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		state := mustRecordK2AOrder(t, svc, "client-corrupt-event")
		if _, err := svc.db.Exec(`DROP TRIGGER order_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE order_events SET event_sha256=? WHERE order_id=?`, strings.Repeat("0", 64), state.OrderID); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("order event with a mismatched durable hash was certified")
		}
	})

	t.Run("malformed event", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		state := mustRecordK2AOrder(t, svc, "client-malformed-event")
		if _, err := svc.db.Exec(`DROP TRIGGER order_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE order_events SET event_json='{' WHERE order_id=?`, state.OrderID); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("malformed durable order event was certified")
		}
	})

	t.Run("missing event storage", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		for _, name := range []string{"order_events_no_update", "order_events_no_delete"} {
			if _, err := svc.db.Exec(`DROP TRIGGER ` + name); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := svc.db.Exec(`DROP TABLE order_events`); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("database without order event storage was certified")
		}
	})

	t.Run("invalid event replay", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		state := mustRecordK2AOrder(t, svc, "client-invalid-replay")
		event := k2aEvent("duplicate-intent-event", state.OrderID, "INTENT_RECORDED")
		eventJSON, eventSHA, err := orderJSONHash(event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`INSERT INTO order_events(event_id,event_sha256,order_id,event_type,source,event_json,recorded_at) VALUES(?,?,?,?,?,?,?)`,
			event.EventID, eventSHA, event.OrderID, event.Type, event.Source, string(eventJSON), "2026-01-10T15:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := proveOrderRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("non-replayable order event sequence was certified")
		}
	})
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
