package main

import (
	"database/sql"
	"path/filepath"
	"strings"
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

func TestG38C1RestoreRejectsPaperAccountingSchemaOrProtectionDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *sql.DB)
	}{
		{
			name: "missing table",
			mutate: func(t *testing.T, db *sql.DB) {
				t.Helper()
				if _, err := db.Exec(`DROP TRIGGER paper_accounting_sessions_no_update;
					DROP TRIGGER paper_accounting_sessions_no_delete;
					DROP TRIGGER paper_accounting_sessions_state_guard;
					DROP TABLE paper_accounting_sessions`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "altered table",
			mutate: func(t *testing.T, db *sql.DB) {
				t.Helper()
				if _, err := db.Exec(`ALTER TABLE paper_accounting_sessions ADD COLUMN untrusted TEXT`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing account uniqueness",
			mutate: func(t *testing.T, db *sql.DB) {
				t.Helper()
				rebuildPaperAccountingWithoutAccountUnique(t, db)
			},
		},
		{
			name: "missing state guard",
			mutate: func(t *testing.T, db *sql.DB) {
				t.Helper()
				if _, err := db.Exec(`DROP TRIGGER paper_accounting_sessions_state_guard`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "altered state guard",
			mutate: func(t *testing.T, db *sql.DB) {
				t.Helper()
				if _, err := db.Exec(`DROP TRIGGER paper_accounting_sessions_state_guard;
					CREATE TRIGGER paper_accounting_sessions_state_guard
					BEFORE INSERT ON paper_accounting_sessions
					BEGIN
						SELECT 1;
					END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing update guard",
			mutate: func(t *testing.T, db *sql.DB) {
				t.Helper()
				if _, err := db.Exec(`DROP TRIGGER paper_accounting_sessions_no_update`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing delete guard",
			mutate: func(t *testing.T, db *sql.DB) {
				t.Helper()
				if _, err := db.Exec(`DROP TRIGGER paper_accounting_sessions_no_delete`); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			golden := writeCurrentSnapshot(t, svc.db)
			candidatePath := filepath.Join(t.TempDir(), "paper-accounting-schema.db")
			if _, err := createBackup(svc.db, candidatePath, golden, candidatePath+".manifest.json", svc.now, svc.id); err != nil {
				t.Fatal(err)
			}
			candidate, err := openExistingDB(candidatePath)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, candidate)
			if err := candidate.Close(); err != nil {
				t.Fatal(err)
			}
			if err := verifyRestore(candidatePath, golden); err == nil {
				t.Fatal("restore accepted paper accounting schema drift")
			}
		})
	}
}

func rebuildPaperAccountingWithoutAccountUnique(t testing.TB, db *sql.DB) {
	t.Helper()
	migration, err := migrationFiles.ReadFile("migrations/015_paper_accounting_sessions.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(migration)
	const tablePrefix = "CREATE TABLE paper_accounting_sessions"
	tableStart := strings.Index(source, tablePrefix)
	triggerStart := strings.Index(source, "CREATE TRIGGER paper_accounting_sessions_no_update")
	if tableStart < 0 || triggerStart <= tableStart {
		t.Fatal("paper accounting migration does not contain the expected table and triggers")
	}
	weakTable := strings.TrimSuffix(strings.TrimSpace(source[tableStart:triggerStart]), ";")
	weakTable = strings.Replace(weakTable, "account_ref TEXT NOT NULL UNIQUE", "account_ref TEXT NOT NULL", 1)
	if _, err := db.Exec(`DROP TRIGGER paper_accounting_sessions_no_update;
		DROP TRIGGER paper_accounting_sessions_no_delete;
		DROP TRIGGER paper_accounting_sessions_state_guard;
		DROP TABLE paper_accounting_sessions`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(weakTable + ";\n" + source[triggerStart:]); err != nil {
		t.Fatal(err)
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
