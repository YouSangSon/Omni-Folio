CREATE TABLE paper_accounting_sessions (
    sequence INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL UNIQUE CHECK (
        length(session_id) BETWEEN 1 AND 128
        AND session_id = trim(session_id)
    ),
    schema_version TEXT NOT NULL CHECK (schema_version = 'paper-accounting-session.v1'),
    account_ref TEXT NOT NULL UNIQUE CHECK (
        length(account_ref) BETWEEN 1 AND 128
        AND account_ref = trim(account_ref)
        AND account_ref GLOB 'kiwoom_account_*'
    ),
    strategy_result_sha256 TEXT NOT NULL REFERENCES strategy_research_evidence(result_sha256) CHECK (
        length(strategy_result_sha256) = 64
        AND strategy_result_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    strategy_selection_event_id TEXT NOT NULL REFERENCES strategy_selection_events(event_id) CHECK (
        length(strategy_selection_event_id) BETWEEN 1 AND 128
        AND strategy_selection_event_id = trim(strategy_selection_event_id)
    ),
    execution_policy_sha256 TEXT NOT NULL CHECK (
        length(execution_policy_sha256) = 64
        AND execution_policy_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    execution_policy_json TEXT NOT NULL CHECK (
        length(execution_policy_json) BETWEEN 1 AND 1048576
    ),
    starting_cash TEXT NOT NULL CHECK (
        length(starting_cash) BETWEEN 1 AND 64
        AND starting_cash NOT GLOB '*[^0-9.]*'
        AND length(starting_cash) - length(replace(starting_cash, '.', '')) <= 1
        AND (
            (instr(starting_cash, '.') = 0 AND substr(starting_cash, 1, 1) GLOB '[1-9]')
            OR
            (instr(starting_cash, '.') > 1
                AND (substr(starting_cash, 1, instr(starting_cash, '.') - 1) = '0' OR substr(starting_cash, 1, 1) GLOB '[1-9]')
                AND substr(starting_cash, -1) GLOB '[1-9]')
        )
    ),
    currency TEXT NOT NULL CHECK (currency = 'KRW'),
    record_sha256 TEXT NOT NULL CHECK (
        length(record_sha256) = 64
        AND record_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    record_json TEXT NOT NULL CHECK (
        length(record_json) BETWEEN 1 AND 1048576
    ),
    recorded_at TEXT NOT NULL CHECK (
        recorded_at GLOB '????-??-??T??:??:??Z'
        OR recorded_at GLOB '????-??-??T??:??:??.*Z'
    )
) STRICT;

CREATE TRIGGER paper_accounting_sessions_no_update
BEFORE UPDATE ON paper_accounting_sessions
BEGIN
    SELECT RAISE(ABORT, 'paper_accounting_sessions is insert-only');
END;

CREATE TRIGGER paper_accounting_sessions_no_delete
BEFORE DELETE ON paper_accounting_sessions
BEGIN
    SELECT RAISE(ABORT, 'paper_accounting_sessions is insert-only');
END;

CREATE TRIGGER paper_accounting_sessions_state_guard
BEFORE INSERT ON paper_accounting_sessions
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1
        FROM strategy_selection_events
        WHERE event_id = NEW.strategy_selection_event_id
          AND selected_result_sha256 = NEW.strategy_result_sha256
          AND event_id = (
              SELECT event_id
              FROM strategy_selection_events
              ORDER BY sequence DESC
              LIMIT 1
          )
    ) THEN RAISE(ABORT, 'paper accounting session strategy binding is not current') END;
    SELECT CASE WHEN EXISTS (
        SELECT 1
        FROM order_idempotency
        WHERE mode = 'paper'
          AND account_ref = NEW.account_ref
    ) THEN RAISE(ABORT, 'paper accounting session cannot follow a paper order') END;
END;
