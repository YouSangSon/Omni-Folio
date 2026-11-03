CREATE TABLE paper_evaluation_events (
    sequence INTEGER PRIMARY KEY,
    evaluation_id TEXT NOT NULL UNIQUE CHECK (
        length(evaluation_id) BETWEEN 1 AND 128
        AND evaluation_id = trim(evaluation_id)
    ),
    schema_version TEXT NOT NULL CHECK (schema_version = 'paper-operational-evaluation.v1'),
    policy_version TEXT NOT NULL CHECK (policy_version = 'paper-operational-safety.v1'),
    account_ref TEXT NOT NULL,
    strategy_result_sha256 TEXT NOT NULL REFERENCES strategy_research_evidence(result_sha256) CHECK (
        length(strategy_result_sha256) = 64
        AND strategy_result_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    strategy_selection_event_id TEXT NOT NULL REFERENCES strategy_selection_events(event_id),
    expected_previous_evaluation_id TEXT NOT NULL CHECK (
        length(expected_previous_evaluation_id) BETWEEN 1 AND 128
        AND expected_previous_evaluation_id = trim(expected_previous_evaluation_id)
    ),
    paper_order_state_sha256 TEXT NOT NULL CHECK (
        length(paper_order_state_sha256) = 64
        AND paper_order_state_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    order_count INTEGER NOT NULL CHECK (order_count >= 0),
    terminal_order_count INTEGER NOT NULL CHECK (terminal_order_count >= 0),
    active_order_count INTEGER NOT NULL CHECK (active_order_count >= 0),
    pending_action_count INTEGER NOT NULL CHECK (pending_action_count >= 0),
    decision TEXT NOT NULL CHECK (decision IN ('INSUFFICIENT', 'PASS', 'DEGRADED')),
    reason_code TEXT NOT NULL CHECK (reason_code IN ('no_terminal_sample', 'operationally_complete', 'unresolved_action')),
    record_sha256 TEXT NOT NULL CHECK (
        length(record_sha256) = 64
        AND record_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    record_json TEXT NOT NULL,
    recorded_at TEXT NOT NULL CHECK (
        recorded_at GLOB '????-??-??T??:??:??Z'
        OR recorded_at GLOB '????-??-??T??:??:??.*Z'
    ),
    CHECK (order_count = terminal_order_count + active_order_count + pending_action_count),
    CHECK (
        (decision = 'DEGRADED' AND reason_code = 'unresolved_action' AND pending_action_count > 0)
        OR
        (decision = 'INSUFFICIENT' AND reason_code = 'no_terminal_sample' AND pending_action_count = 0 AND terminal_order_count = 0)
        OR
        (decision = 'PASS' AND reason_code = 'operationally_complete' AND pending_action_count = 0 AND terminal_order_count > 0)
    )
) STRICT;

ALTER TABLE strategy_selection_events
ADD COLUMN paper_evaluation_sequence INTEGER NOT NULL DEFAULT 0
CHECK (paper_evaluation_sequence >= 0);

DROP TRIGGER strategy_selection_events_state_guard;

CREATE TRIGGER strategy_selection_events_state_guard
BEFORE INSERT ON strategy_selection_events
BEGIN
    SELECT CASE WHEN NEW.expected_current_event_id != COALESCE(
        (SELECT event_id FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1), 'no_event'
    ) THEN RAISE(ABORT, 'strategy selection expected current event is stale') END;
    SELECT CASE WHEN NEW.previous_selected_result_sha256 != COALESCE(
        (SELECT selected_result_sha256 FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1), 'no_strategy'
    ) THEN RAISE(ABORT, 'strategy selection previous result is stale') END;
    SELECT CASE WHEN NEW.event_type = 'SELECT' AND (
        NEW.selected_result_sha256 != NEW.candidate_result_sha256 OR NOT EXISTS (
            SELECT 1 FROM strategy_research_evidence
            WHERE result_sha256 = NEW.candidate_result_sha256 AND target = 'paper_candidate'
        )
    ) THEN RAISE(ABORT, 'strategy selection requires paper_candidate evidence') END;
    SELECT CASE WHEN NEW.event_type = 'ROLLBACK' AND NEW.source_event_id != COALESCE(
        (SELECT event_id FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1), 'no_event'
    ) THEN RAISE(ABORT, 'strategy rollback source is stale') END;
    SELECT CASE WHEN NEW.paper_evaluation_sequence != COALESCE(
        (SELECT MAX(sequence) FROM paper_evaluation_events), 0
    ) THEN RAISE(ABORT, 'strategy selection paper evaluation sequence is stale') END;
END;

CREATE INDEX paper_evaluation_events_current_idx
ON paper_evaluation_events(account_ref, strategy_selection_event_id, sequence DESC);

CREATE TRIGGER paper_evaluation_events_no_update
BEFORE UPDATE ON paper_evaluation_events
BEGIN
    SELECT RAISE(ABORT, 'paper_evaluation_events is insert-only');
END;

CREATE TRIGGER paper_evaluation_events_no_delete
BEFORE DELETE ON paper_evaluation_events
BEGIN
    SELECT RAISE(ABORT, 'paper_evaluation_events is insert-only');
END;

CREATE TRIGGER paper_evaluation_events_state_guard
BEFORE INSERT ON paper_evaluation_events
BEGIN
    SELECT CASE WHEN NEW.expected_previous_evaluation_id != COALESCE(
        (
            SELECT evaluation_id
            FROM paper_evaluation_events
            WHERE account_ref = NEW.account_ref
              AND strategy_selection_event_id = NEW.strategy_selection_event_id
            ORDER BY sequence DESC
            LIMIT 1
        ),
        'no_evaluation'
    ) THEN RAISE(ABORT, 'paper evaluation expected previous event is stale') END;

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
    ) THEN RAISE(ABORT, 'paper evaluation strategy binding is invalid') END;
END;
