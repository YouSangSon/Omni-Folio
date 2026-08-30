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

func TestG38C2RestoreRejectsPaperMarketSchemaOrProtectionDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *sql.DB)
	}{
		{"missing bar table", func(t *testing.T, db *sql.DB) {
			if _, err := db.Exec(`DROP TRIGGER paper_signal_events_no_update; DROP TRIGGER paper_signal_events_no_delete;
				DROP TRIGGER paper_signal_events_state_guard; DROP TABLE paper_signal_events;
				DROP TRIGGER paper_market_bar_observations_no_update; DROP TRIGGER paper_market_bar_observations_no_delete;
				DROP TABLE paper_market_bar_observations`); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing signal table", func(t *testing.T, db *sql.DB) {
			if _, err := db.Exec(`DROP TRIGGER paper_signal_events_no_update; DROP TRIGGER paper_signal_events_no_delete;
				DROP TRIGGER paper_signal_events_state_guard; DROP TABLE paper_signal_events`); err != nil {
				t.Fatal(err)
			}
		}},
		{"bar table drift", func(t *testing.T, db *sql.DB) {
			if _, err := db.Exec(`ALTER TABLE paper_market_bar_observations ADD COLUMN untrusted TEXT`); err != nil {
				t.Fatal(err)
			}
		}},
		{"signal table drift", func(t *testing.T, db *sql.DB) {
			if _, err := db.Exec(`ALTER TABLE paper_signal_events ADD COLUMN untrusted TEXT`); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing bar identity uniqueness", func(t *testing.T, db *sql.DB) {
			removeSQLiteIndexForTest(t, db, "paper_market_bar_observations", []string{"observation_id"})
		}},
		{"missing bar source uniqueness", func(t *testing.T, db *sql.DB) {
			removeSQLiteIndexForTest(t, db, "paper_market_bar_observations", []string{"source", "source_observation_id"})
		}},
		{"missing bar series uniqueness", func(t *testing.T, db *sql.DB) {
			removeSQLiteIndexForTest(t, db, "paper_market_bar_observations", []string{"source", "symbol", "venue", "interval", "timezone", "price_adjustment", "open_at"})
		}},
		{"missing signal account uniqueness", func(t *testing.T, db *sql.DB) {
			removeSQLiteIndexForTest(t, db, "paper_signal_events", []string{"account_ref", "signal_id"})
		}},
		{"missing signal identity uniqueness", func(t *testing.T, db *sql.DB) {
			removeSQLiteIndexForTest(t, db, "paper_signal_events", []string{"event_id"})
		}},
		{"missing signal session foreign key", func(t *testing.T, db *sql.DB) {
			driftSQLiteTableSQLForTest(t, db, "paper_signal_events", "paper_accounting_session_id TEXT NOT NULL REFERENCES paper_accounting_sessions(session_id)", "paper_accounting_session_id TEXT NOT NULL")
		}},
		{"missing signal result foreign key", func(t *testing.T, db *sql.DB) {
			driftSQLiteTableSQLForTest(t, db, "paper_signal_events", "strategy_result_sha256 TEXT NOT NULL REFERENCES strategy_research_evidence(result_sha256)", "strategy_result_sha256 TEXT NOT NULL")
		}},
		{"missing signal selection foreign key", func(t *testing.T, db *sql.DB) {
			driftSQLiteTableSQLForTest(t, db, "paper_signal_events", "strategy_selection_event_id TEXT NOT NULL REFERENCES strategy_selection_events(event_id)", "strategy_selection_event_id TEXT NOT NULL")
		}},
		{"missing signal bar foreign key", func(t *testing.T, db *sql.DB) {
			driftSQLiteTableSQLForTest(t, db, "paper_signal_events", "signal_bar_observation_id TEXT NOT NULL REFERENCES paper_market_bar_observations(observation_id)", "signal_bar_observation_id TEXT NOT NULL")
		}},
		{"missing signal state guard", func(t *testing.T, db *sql.DB) {
			if _, err := db.Exec(`DROP TRIGGER paper_signal_events_state_guard`); err != nil {
				t.Fatal(err)
			}
		}},
		{"altered signal state guard", func(t *testing.T, db *sql.DB) {
			if _, err := db.Exec(`DROP TRIGGER paper_signal_events_state_guard;
				CREATE TRIGGER paper_signal_events_state_guard BEFORE INSERT ON paper_signal_events BEGIN SELECT 1; END`); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing reverse legacy guard", func(t *testing.T, db *sql.DB) {
			if _, err := db.Exec(`DROP TRIGGER order_idempotency_legacy_paper_signal_guard`); err != nil {
				t.Fatal(err)
			}
		}},
		{"altered reverse legacy guard", func(t *testing.T, db *sql.DB) {
			if _, err := db.Exec(`DROP TRIGGER order_idempotency_legacy_paper_signal_guard;
				CREATE TRIGGER order_idempotency_legacy_paper_signal_guard BEFORE INSERT ON order_idempotency BEGIN SELECT 1; END`); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, triggerName := range []string{
		"paper_market_bar_observations_no_update", "paper_market_bar_observations_no_delete",
		"paper_signal_events_no_update", "paper_signal_events_no_delete",
	} {
		triggerName := triggerName
		tests = append(tests, struct {
			name   string
			mutate func(*testing.T, *sql.DB)
		}{"missing " + triggerName, func(t *testing.T, db *sql.DB) {
			if _, err := db.Exec(`DROP TRIGGER ` + triggerName); err != nil {
				t.Fatal(err)
			}
		}})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			golden := writeCurrentSnapshot(t, svc.db)
			path := filepath.Join(t.TempDir(), "paper-market-schema.db")
			if _, err := createBackup(svc.db, path, golden, path+".manifest.json", svc.now, svc.id); err != nil {
				t.Fatal(err)
			}
			candidate, err := openExistingDB(path)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(t, candidate)
			if err := candidate.Close(); err != nil {
				t.Fatal(err)
			}
			if err := verifyRestore(path, golden); err == nil {
				t.Fatal("restore accepted paper market schema drift")
			}
		})
	}
}

func removeSQLiteIndexForTest(t testing.TB, db *sql.DB, table string, columns []string) {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_index_list(?) WHERE "unique"=1`, table)
	if err != nil {
		t.Fatal(err)
	}
	var indexes []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		indexes = append(indexes, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	var target string
	for _, name := range indexes {
		columnRows, err := db.Query(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, name)
		if err != nil {
			t.Fatal(err)
		}
		var actual []string
		for columnRows.Next() {
			var column string
			if err := columnRows.Scan(&column); err != nil {
				columnRows.Close()
				t.Fatal(err)
			}
			actual = append(actual, column)
		}
		if err := columnRows.Close(); err != nil {
			t.Fatal(err)
		}
		if strings.Join(actual, "\x00") == strings.Join(columns, "\x00") {
			target = name
		}
	}
	if target == "" {
		t.Fatalf("unique index not found for %s(%s)", table, strings.Join(columns, ","))
	}
	if _, err := db.Exec(`PRAGMA writable_schema=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM sqlite_master WHERE type='index' AND name=?`, target); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA writable_schema=OFF`); err != nil {
		t.Fatal(err)
	}
}

func driftSQLiteTableSQLForTest(t testing.TB, db *sql.DB, table, old, replacement string) {
	t.Helper()
	var definition string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, old) {
		t.Fatalf("%s definition lacks drift target", table)
	}
	if _, err := db.Exec(`PRAGMA writable_schema=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sqlite_master SET sql=? WHERE type='table' AND name=?`, strings.Replace(definition, old, replacement, 1), table); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA writable_schema=OFF`); err != nil {
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
