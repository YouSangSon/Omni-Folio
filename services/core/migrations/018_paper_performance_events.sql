CREATE TABLE paper_performance_events (
    sequence INTEGER PRIMARY KEY,
    performance_id TEXT NOT NULL UNIQUE CHECK (
        length(performance_id) BETWEEN 1 AND 128
        AND performance_id = trim(performance_id)
    ),
    schema_version TEXT NOT NULL CHECK (schema_version = 'paper-performance-evaluation.v1'),
    policy_version TEXT NOT NULL CHECK (policy_version = 'paper-performance-account-v1'),
    account_ref TEXT NOT NULL CHECK (
        length(account_ref) BETWEEN 1 AND 128
        AND account_ref = trim(account_ref)
        AND account_ref GLOB 'kiwoom_account_*'
    ),
    paper_accounting_session_id TEXT NOT NULL REFERENCES paper_accounting_sessions(session_id),
    strategy_selection_event_id TEXT NOT NULL REFERENCES strategy_selection_events(event_id),
    selected_strategy_result_ref TEXT NOT NULL CHECK (
        selected_strategy_result_ref = 'no_strategy'
        OR (length(selected_strategy_result_ref) = 64 AND selected_strategy_result_ref NOT GLOB '*[^0-9a-f]*')
    ),
    expected_previous_performance_id TEXT NOT NULL CHECK (
        length(expected_previous_performance_id) BETWEEN 1 AND 128
        AND expected_previous_performance_id = trim(expected_previous_performance_id)
    ),
    strategy_selection_sequence_cutoff INTEGER NOT NULL CHECK (strategy_selection_sequence_cutoff > 0),
    order_event_sequence_cutoff INTEGER NOT NULL CHECK (order_event_sequence_cutoff >= 0),
    paper_market_sequence_cutoff INTEGER NOT NULL CHECK (paper_market_sequence_cutoff >= 0),
    as_of TEXT NOT NULL CHECK (
        length(as_of) = 30
        AND as_of GLOB '????-??-??T??:??:??.?????????Z'
        AND as_of NOT GLOB '*[^0-9TZ:.-]*'
    ),
    paper_account_state_sha256 TEXT NOT NULL CHECK (
        length(paper_account_state_sha256) = 64
        AND paper_account_state_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    marks_sha256 TEXT NOT NULL CHECK (
        length(marks_sha256) = 64
        AND marks_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    marks_json TEXT NOT NULL CHECK (
        length(marks_json) BETWEEN 2 AND 1048576
        AND json_valid(marks_json)
        AND json_type(marks_json) = 'array'
    ),
    mark_count INTEGER NOT NULL CHECK (mark_count >= 0 AND mark_count = json_array_length(marks_json)),
    cash TEXT NOT NULL CHECK (length(cash) BETWEEN 1 AND 64),
    open_cost TEXT NOT NULL CHECK (length(open_cost) BETWEEN 1 AND 64),
    market_value TEXT NOT NULL CHECK (length(market_value) BETWEEN 1 AND 64),
    realized_pnl TEXT NOT NULL CHECK (length(realized_pnl) BETWEEN 1 AND 64),
    unrealized_pnl TEXT NOT NULL CHECK (length(unrealized_pnl) BETWEEN 1 AND 64),
    total_pnl TEXT NOT NULL CHECK (length(total_pnl) BETWEEN 1 AND 64),
    equity TEXT NOT NULL CHECK (length(equity) BETWEEN 1 AND 64),
    peak_equity TEXT NOT NULL CHECK (length(peak_equity) BETWEEN 1 AND 64),
    period_return_state TEXT NOT NULL CHECK (period_return_state IN ('defined', 'undefined_zero_denominator')),
    period_return TEXT NOT NULL CHECK (
        (period_return_state = 'defined' AND length(period_return) BETWEEN 1 AND 64)
        OR (period_return_state = 'undefined_zero_denominator' AND period_return = '')
    ),
    cumulative_return TEXT NOT NULL CHECK (length(cumulative_return) BETWEEN 1 AND 64),
    drawdown TEXT NOT NULL CHECK (length(drawdown) BETWEEN 1 AND 64),
    max_drawdown TEXT NOT NULL CHECK (length(max_drawdown) BETWEEN 1 AND 64),
    record_sha256 TEXT NOT NULL CHECK (
        length(record_sha256) = 64
        AND record_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    record_json TEXT NOT NULL CHECK (length(record_json) BETWEEN 1 AND 1048576),
    recorded_at TEXT NOT NULL CHECK (
        length(recorded_at) = 30
        AND recorded_at GLOB '????-??-??T??:??:??.?????????Z'
        AND recorded_at NOT GLOB '*[^0-9TZ:.-]*'
    ),
    UNIQUE (policy_version, account_ref, paper_accounting_session_id, as_of),
    CHECK (as_of <= recorded_at)
) STRICT;

CREATE INDEX paper_performance_events_account_idx
ON paper_performance_events(account_ref, sequence DESC);

CREATE TRIGGER paper_performance_events_no_update
BEFORE UPDATE ON paper_performance_events
BEGIN
    SELECT RAISE(ABORT, 'paper_performance_events is insert-only');
END;

CREATE TRIGGER paper_performance_events_no_delete
BEFORE DELETE ON paper_performance_events
BEGIN
    SELECT RAISE(ABORT, 'paper_performance_events is insert-only');
END;

CREATE TRIGGER paper_performance_events_state_guard
BEFORE INSERT ON paper_performance_events
BEGIN
    SELECT CASE WHEN NEW.strategy_selection_sequence_cutoff != COALESCE(
        (SELECT MAX(sequence) FROM strategy_selection_events), 0
    ) THEN RAISE(ABORT, 'paper performance strategy cutoff is stale') END;

    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM strategy_selection_events
        WHERE sequence = NEW.strategy_selection_sequence_cutoff
          AND event_id = NEW.strategy_selection_event_id
          AND selected_result_sha256 = NEW.selected_strategy_result_ref
    ) THEN RAISE(ABORT, 'paper performance selected strategy is not current') END;

    SELECT CASE WHEN NEW.order_event_sequence_cutoff != COALESCE(
        (SELECT MAX(sequence) FROM order_events), 0
    ) THEN RAISE(ABORT, 'paper performance order cutoff is stale') END;

    SELECT CASE WHEN NEW.paper_market_sequence_cutoff != COALESCE(
        (SELECT MAX(sequence) FROM paper_market_bar_observations), 0
    ) THEN RAISE(ABORT, 'paper performance market cutoff is stale') END;

    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM paper_accounting_sessions
        WHERE session_id = NEW.paper_accounting_session_id
          AND account_ref = NEW.account_ref
    ) THEN RAISE(ABORT, 'paper performance accounting session is invalid') END;

    SELECT CASE WHEN NEW.expected_previous_performance_id != COALESCE(
        (
            SELECT performance_id FROM paper_performance_events
            WHERE account_ref = NEW.account_ref
            ORDER BY sequence DESC LIMIT 1
        ),
        'no_performance'
    ) THEN RAISE(ABORT, 'paper performance predecessor is stale') END;

    SELECT CASE WHEN EXISTS (
        SELECT 1 FROM paper_performance_events
        WHERE account_ref = NEW.account_ref
          AND as_of >= NEW.as_of
    ) THEN RAISE(ABORT, 'paper performance as_of is not increasing') END;
END;
