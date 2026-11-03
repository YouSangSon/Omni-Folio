CREATE TABLE paper_strategy_performance_events (
    sequence INTEGER PRIMARY KEY,
    strategy_performance_id TEXT NOT NULL UNIQUE CHECK (
        length(strategy_performance_id) BETWEEN 1 AND 128
        AND strategy_performance_id = trim(strategy_performance_id)
    ),
    schema_version TEXT NOT NULL CHECK (schema_version = 'paper-strategy-window-performance.v1'),
    policy_version TEXT NOT NULL CHECK (policy_version = 'paper-strategy-window-performance-v1'),
    account_ref TEXT NOT NULL CHECK (
        length(account_ref) BETWEEN 1 AND 128
        AND account_ref = trim(account_ref)
        AND account_ref GLOB 'kiwoom_account_*'
    ),
    paper_accounting_session_id TEXT NOT NULL REFERENCES paper_accounting_sessions(session_id),
    strategy_selection_event_id TEXT NOT NULL REFERENCES strategy_selection_events(event_id),
    selected_strategy_result_ref TEXT NOT NULL REFERENCES strategy_research_evidence(result_sha256),
    expected_previous_strategy_performance_id TEXT NOT NULL CHECK (
        length(expected_previous_strategy_performance_id) BETWEEN 1 AND 128
        AND expected_previous_strategy_performance_id = trim(expected_previous_strategy_performance_id)
    ),
    baseline_performance_id TEXT NOT NULL REFERENCES paper_performance_events(performance_id),
    latest_performance_id TEXT NOT NULL REFERENCES paper_performance_events(performance_id),
    baseline_as_of TEXT NOT NULL CHECK (
        length(baseline_as_of) = 30
        AND baseline_as_of GLOB '????-??-??T??:??:??.?????????Z'
        AND baseline_as_of NOT GLOB '*[^0-9TZ:.-]*'
    ),
    latest_as_of TEXT NOT NULL CHECK (
        length(latest_as_of) = 30
        AND latest_as_of GLOB '????-??-??T??:??:??.?????????Z'
        AND latest_as_of NOT GLOB '*[^0-9TZ:.-]*'
    ),
    sample_count INTEGER NOT NULL CHECK (sample_count > 0),
    baseline_equity TEXT NOT NULL CHECK (length(baseline_equity) BETWEEN 1 AND 64),
    latest_equity TEXT NOT NULL CHECK (length(latest_equity) BETWEEN 1 AND 64),
    peak_equity TEXT NOT NULL CHECK (length(peak_equity) BETWEEN 1 AND 64),
    period_return_state TEXT NOT NULL CHECK (period_return_state IN ('defined', 'undefined_zero_denominator')),
    period_return TEXT NOT NULL CHECK (
        (period_return_state = 'defined' AND length(period_return) BETWEEN 1 AND 64)
        OR (period_return_state = 'undefined_zero_denominator' AND period_return = '')
    ),
    cumulative_return TEXT NOT NULL CHECK (length(cumulative_return) BETWEEN 1 AND 64),
    drawdown TEXT NOT NULL CHECK (length(drawdown) BETWEEN 1 AND 64),
    max_drawdown TEXT NOT NULL CHECK (length(max_drawdown) BETWEEN 1 AND 64),
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    record_json TEXT NOT NULL CHECK (length(record_json) BETWEEN 1 AND 1048576),
    recorded_at TEXT NOT NULL CHECK (
        length(recorded_at) = 30
        AND recorded_at GLOB '????-??-??T??:??:??.?????????Z'
        AND recorded_at NOT GLOB '*[^0-9TZ:.-]*'
    ),
    UNIQUE (policy_version, account_ref, strategy_selection_event_id, latest_performance_id),
    CHECK (baseline_as_of <= latest_as_of AND latest_as_of <= recorded_at)
) STRICT;

CREATE INDEX paper_strategy_performance_events_window_idx
ON paper_strategy_performance_events(account_ref, strategy_selection_event_id, sequence DESC);

CREATE TRIGGER paper_strategy_performance_events_no_update
BEFORE UPDATE ON paper_strategy_performance_events
BEGIN
    SELECT RAISE(ABORT, 'paper_strategy_performance_events is insert-only');
END;

CREATE TRIGGER paper_strategy_performance_events_no_delete
BEFORE DELETE ON paper_strategy_performance_events
BEGIN
    SELECT RAISE(ABORT, 'paper_strategy_performance_events is insert-only');
END;

CREATE TRIGGER paper_strategy_performance_events_state_guard
BEFORE INSERT ON paper_strategy_performance_events
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM strategy_selection_events
        WHERE event_id = NEW.strategy_selection_event_id
          AND selected_result_sha256 = NEW.selected_strategy_result_ref
          AND sequence = (SELECT MAX(sequence) FROM strategy_selection_events)
    ) THEN RAISE(ABORT, 'paper strategy performance selection is stale') END;

    SELECT CASE WHEN NEW.latest_performance_id != COALESCE(
        (
            SELECT performance_id FROM paper_performance_events
            WHERE account_ref = NEW.account_ref
              AND paper_accounting_session_id = NEW.paper_accounting_session_id
            ORDER BY sequence DESC LIMIT 1
        ),
        'no_performance'
    ) THEN RAISE(ABORT, 'paper strategy performance latest point is stale') END;

    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM paper_performance_events
        WHERE performance_id = NEW.baseline_performance_id
          AND account_ref = NEW.account_ref
          AND paper_accounting_session_id = NEW.paper_accounting_session_id
          AND strategy_selection_event_id = NEW.strategy_selection_event_id
          AND selected_strategy_result_ref = NEW.selected_strategy_result_ref
    ) THEN RAISE(ABORT, 'paper strategy performance baseline is invalid') END;

    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM paper_performance_events
        WHERE performance_id = NEW.latest_performance_id
          AND account_ref = NEW.account_ref
          AND paper_accounting_session_id = NEW.paper_accounting_session_id
          AND strategy_selection_event_id = NEW.strategy_selection_event_id
          AND selected_strategy_result_ref = NEW.selected_strategy_result_ref
    ) THEN RAISE(ABORT, 'paper strategy performance latest point is invalid') END;

    SELECT CASE WHEN NEW.expected_previous_strategy_performance_id != COALESCE(
        (
            SELECT strategy_performance_id FROM paper_strategy_performance_events
            WHERE account_ref = NEW.account_ref
              AND paper_accounting_session_id = NEW.paper_accounting_session_id
              AND strategy_selection_event_id = NEW.strategy_selection_event_id
            ORDER BY sequence DESC LIMIT 1
        ),
        'no_strategy_performance'
    ) THEN RAISE(ABORT, 'paper strategy performance predecessor is stale') END;
END;
