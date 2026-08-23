package main

import (
	"path/filepath"
	"testing"
)

func TestK2ARestoreRejectsOrderTablesWithoutDurabilityConstraints(t *testing.T) {
	path, golden := weakOrderRestoreCandidate(t, `
CREATE TABLE order_events (
    sequence INTEGER PRIMARY KEY, event_id TEXT NOT NULL UNIQUE, event_sha256 TEXT NOT NULL,
    order_id TEXT NOT NULL, event_type TEXT NOT NULL, source TEXT NOT NULL,
    provider_order_ref TEXT, provider_execution_ref TEXT,
    event_json TEXT NOT NULL, recorded_at TEXT NOT NULL
) STRICT;`)
	if err := verifyRestore(path, golden); err == nil {
		t.Fatal("restore accepted order tables without provider-execution uniqueness and the order foreign key")
	}
}

func TestK2ARestoreRejectsCompositeEventSequencePrimaryKey(t *testing.T) {
	path, golden := weakOrderRestoreCandidate(t, `
CREATE TABLE order_events (
    sequence INTEGER NOT NULL, event_id TEXT NOT NULL UNIQUE, event_sha256 TEXT NOT NULL,
    order_id TEXT NOT NULL, event_type TEXT NOT NULL, source TEXT NOT NULL,
    provider_order_ref TEXT, provider_execution_ref TEXT UNIQUE,
    event_json TEXT NOT NULL, recorded_at TEXT NOT NULL,
    PRIMARY KEY (sequence, event_id),
    FOREIGN KEY (order_id) REFERENCES order_idempotency(order_id)
) STRICT;`)
	if err := verifyRestore(path, golden); err == nil {
		t.Fatal("restore accepted a composite event sequence primary key that cannot auto-assign sequence")
	}
}

func TestK2ARestoreRejectsDescendingEventSequencePrimaryKey(t *testing.T) {
	path, golden := weakOrderRestoreCandidate(t, `
CREATE TABLE order_events (
    sequence INTEGER PRIMARY KEY DESC, event_id TEXT NOT NULL UNIQUE, event_sha256 TEXT NOT NULL,
    order_id TEXT NOT NULL, event_type TEXT NOT NULL, source TEXT NOT NULL,
    provider_order_ref TEXT, provider_execution_ref TEXT UNIQUE,
    event_json TEXT NOT NULL, recorded_at TEXT NOT NULL,
    FOREIGN KEY (order_id) REFERENCES order_idempotency(order_id)
) STRICT;`)
	if err := verifyRestore(path, golden); err == nil {
		t.Fatal("restore accepted an event sequence primary key that is not a rowid alias")
	}
}

func weakOrderRestoreCandidate(t *testing.T, orderEventsDDL string) (string, string) {
	t.Helper()
	svc, _ := testService(t, nil, nil)
	golden := writeCurrentSnapshot(t, svc.db)
	path := filepath.Join(t.TempDir(), "weak-order-schema.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := migrationFiles.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(v1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE order_idempotency (
    provider TEXT NOT NULL CHECK (provider = 'kiwoom'),
    mode TEXT NOT NULL CHECK (mode = 'synthetic'), account_ref TEXT NOT NULL,
    client_order_id TEXT NOT NULL, request_sha256 TEXT NOT NULL,
    order_id TEXT NOT NULL UNIQUE, intent_json TEXT NOT NULL, recorded_at TEXT NOT NULL,
    PRIMARY KEY (provider, mode, account_ref, client_order_id)
) STRICT;
` + orderEventsDDL + `
CREATE TRIGGER order_idempotency_no_update BEFORE UPDATE ON order_idempotency
BEGIN SELECT RAISE(ABORT, 'order_idempotency is insert-only'); END;
CREATE TRIGGER order_idempotency_no_delete BEFORE DELETE ON order_idempotency
BEGIN SELECT RAISE(ABORT, 'order_idempotency is insert-only'); END;
CREATE TRIGGER order_events_no_update BEFORE UPDATE ON order_events
BEGIN SELECT RAISE(ABORT, 'order_events is insert-only'); END;
CREATE TRIGGER order_events_no_delete BEFORE DELETE ON order_events
BEGIN SELECT RAISE(ABORT, 'order_events is insert-only'); END;
INSERT INTO schema_migrations(version, applied_at) VALUES
    (1, '2026-01-10T15:00:00Z'),
    (2, '2026-01-10T15:00:01Z');
`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path, golden
}
