-- Forward-only: the original events and historical recovery evidence stay unchanged.
CREATE TABLE ledger_replay_state (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    policy TEXT NOT NULL CHECK (policy IN ('fifo_exact_else_half_even_residual_8_v1', 'fifo_utc_nanosecond_exact_else_half_even_residual_8_v2')),
    legacy_revision INTEGER NOT NULL CHECK (legacy_revision >= 0)
) STRICT;

INSERT INTO ledger_replay_state
SELECT 1, CASE WHEN revision = 0 AND NOT EXISTS (SELECT 1 FROM events) THEN 'fifo_utc_nanosecond_exact_else_half_even_residual_8_v2' ELSE 'fifo_exact_else_half_even_residual_8_v1' END, revision
FROM ledger_meta WHERE singleton = 1;

CREATE TABLE ledger_replay_transition (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    ledger_revision INTEGER NOT NULL CHECK (ledger_revision > 0),
    ledger_recorded_at TEXT NOT NULL,
    events_sha256 TEXT NOT NULL CHECK (length(events_sha256) = 64),
    legacy_snapshot_sha256 TEXT NOT NULL CHECK (length(legacy_snapshot_sha256) = 64),
    snapshot_sha256 TEXT NOT NULL CHECK (length(snapshot_sha256) = 64),
    activated_at TEXT NOT NULL
) STRICT;

CREATE TRIGGER ledger_replay_state_no_replace BEFORE INSERT ON ledger_replay_state
WHEN EXISTS (SELECT 1 FROM ledger_replay_state)
BEGIN SELECT RAISE(ABORT, 'ledger replay state cannot be replaced'); END;
CREATE TRIGGER ledger_replay_transition_insert_guard BEFORE INSERT ON ledger_replay_transition
WHEN EXISTS (SELECT 1 FROM ledger_replay_transition)
    OR NOT EXISTS (SELECT 1 FROM ledger_replay_state WHERE singleton = 1
        AND policy = 'fifo_exact_else_half_even_residual_8_v1' AND legacy_revision = NEW.ledger_revision)
BEGIN SELECT RAISE(ABORT, 'ledger replay transition requires the frozen legacy origin'); END;
CREATE TRIGGER ledger_replay_transition_no_update BEFORE UPDATE ON ledger_replay_transition
BEGIN SELECT RAISE(ABORT, 'ledger replay transition is insert-only'); END;
CREATE TRIGGER ledger_replay_transition_no_delete BEFORE DELETE ON ledger_replay_transition
BEGIN SELECT RAISE(ABORT, 'ledger replay transition is insert-only'); END;
CREATE TRIGGER ledger_replay_state_no_delete BEFORE DELETE ON ledger_replay_state
BEGIN SELECT RAISE(ABORT, 'ledger replay state cannot be deleted'); END;
CREATE TRIGGER ledger_replay_state_transition BEFORE UPDATE ON ledger_replay_state
WHEN OLD.singleton != NEW.singleton OR OLD.legacy_revision != NEW.legacy_revision
    OR OLD.policy != 'fifo_exact_else_half_even_residual_8_v1'
    OR NEW.policy != 'fifo_utc_nanosecond_exact_else_half_even_residual_8_v2'
    OR NOT EXISTS (SELECT 1 FROM ledger_replay_transition WHERE singleton = 1 AND ledger_revision = OLD.legacy_revision)
BEGIN SELECT RAISE(ABORT, 'ledger replay policy requires an explicit transition'); END;
CREATE TRIGGER events_replay_policy_guard BEFORE INSERT ON events
WHEN NOT EXISTS (SELECT 1 FROM ledger_replay_state WHERE singleton = 1 AND policy = 'fifo_utc_nanosecond_exact_else_half_even_residual_8_v2')
BEGIN SELECT RAISE(ABORT, 'ledger replay upgrade required'); END;

DROP TRIGGER events_cash_void_guard;
CREATE TRIGGER events_cash_void_guard BEFORE INSERT ON events WHEN NEW.type = 'CASH_VOID'
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM events target
        WHERE target.account_id = NEW.account_id
          AND target.source_event_id = NEW.corrects_source_event_id
          AND target.type IN ('DEPOSIT', 'WITHDRAWAL', 'DIVIDEND', 'FEE', 'TAX')
          AND target.currency = NEW.currency
          AND rtrim(target.occurred_at, 'Z') <= rtrim(NEW.occurred_at, 'Z')
          AND NEW.amount = CASE WHEN target.amount GLOB '-*' THEN substr(target.amount, 2) ELSE '-' || target.amount END
    ) THEN RAISE(ABORT, 'cash void must exactly reverse an eligible event') END;
END;
