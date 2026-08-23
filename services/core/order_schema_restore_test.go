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
    event_json TEXT NOT NULL, recorded_at TEXT NOT NULL, authority_reservation_id TEXT
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
    event_json TEXT NOT NULL, recorded_at TEXT NOT NULL, authority_reservation_id TEXT REFERENCES risk_reservations(reservation_id),
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
    event_json TEXT NOT NULL, recorded_at TEXT NOT NULL, authority_reservation_id TEXT REFERENCES risk_reservations(reservation_id),
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
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
	DROP TRIGGER order_events_no_update;
	DROP TRIGGER order_events_no_delete;
	DROP TABLE order_events;
` + orderEventsDDL + `
	CREATE TRIGGER order_events_no_update BEFORE UPDATE ON order_events
	BEGIN SELECT RAISE(ABORT, 'order_events is insert-only'); END;
	CREATE TRIGGER order_events_no_delete BEFORE DELETE ON order_events
	BEGIN SELECT RAISE(ABORT, 'order_events is insert-only'); END;
	CREATE TRIGGER order_events_risk_reservation_guard BEFORE INSERT ON order_events
	WHEN NEW.event_type = 'RISK_APPROVED'
	BEGIN
		SELECT CASE WHEN NEW.authority_reservation_id IS NULL OR NOT EXISTS (
			SELECT 1 FROM risk_reservations
			WHERE reservation_id = NEW.authority_reservation_id
			  AND order_id = NEW.order_id
			  AND risk_event_id = NEW.event_id
			  AND reservation_id = json_extract(NEW.event_json, '$.risk_reservation_id')
			  AND policy_version = json_extract(NEW.event_json, '$.risk_policy_version')
			  AND fencing_token = json_extract(NEW.event_json, '$.fencing_token')
		) THEN RAISE(ABORT, 'risk approval requires an authority reservation') END;
	END;
	CREATE TRIGGER order_events_dispatch_reservation_guard BEFORE INSERT ON order_events
	WHEN NEW.event_type = 'SUBMIT_DISPATCHED'
	BEGIN
		SELECT CASE WHEN NEW.authority_reservation_id IS NULL OR NOT EXISTS (
			SELECT 1 FROM risk_reservations
			WHERE reservation_id = NEW.authority_reservation_id
			  AND order_id = NEW.order_id
			  AND dispatch_event_id = NEW.event_id
			  AND reservation_id = json_extract(NEW.event_json, '$.risk_reservation_id')
			  AND policy_version = json_extract(NEW.event_json, '$.risk_policy_version')
			  AND fencing_token = json_extract(NEW.event_json, '$.fencing_token')
		) THEN RAISE(ABORT, 'submit dispatch requires an authority reservation') END;
	END;
	CREATE TRIGGER order_events_non_authority_reservation_guard BEFORE INSERT ON order_events
	WHEN NEW.event_type NOT IN ('RISK_APPROVED', 'SUBMIT_DISPATCHED')
		 AND NEW.authority_reservation_id IS NOT NULL
	BEGIN
		SELECT RAISE(ABORT, 'authority reservation is invalid for this event');
	END;
`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path, golden
}
