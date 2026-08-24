CREATE TABLE strategy_research_evidence (
    sequence INTEGER PRIMARY KEY,
    result_sha256 TEXT NOT NULL UNIQUE CHECK (length(result_sha256) = 64 AND result_sha256 NOT GLOB '*[^0-9a-f]*'),
    artifact_sha256 TEXT NOT NULL CHECK (length(artifact_sha256) = 64 AND artifact_sha256 NOT GLOB '*[^0-9a-f]*'),
    strategy_name TEXT NOT NULL,
    strategy_version TEXT NOT NULL,
    parameter_sha256 TEXT NOT NULL CHECK (length(parameter_sha256) = 64 AND parameter_sha256 NOT GLOB '*[^0-9a-f]*'),
    target TEXT NOT NULL CHECK (target IN ('paper_candidate', 'no_promotion')),
    artifact_json TEXT NOT NULL,
    recorded_at TEXT NOT NULL
) STRICT;

CREATE TABLE strategy_selection_events (
    sequence INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL CHECK (event_type IN ('SELECT', 'ROLLBACK')),
    candidate_result_sha256 TEXT REFERENCES strategy_research_evidence(result_sha256),
    expected_current_event_id TEXT NOT NULL,
    source_event_id TEXT REFERENCES strategy_selection_events(event_id),
    previous_selected_result_sha256 TEXT NOT NULL,
    selected_result_sha256 TEXT NOT NULL,
    reason_code TEXT NOT NULL CHECK (reason_code IN ('manual_selection', 'manual_rollback')),
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    record_json TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    CHECK (
        (event_type = 'SELECT' AND candidate_result_sha256 IS NOT NULL AND source_event_id IS NULL AND reason_code = 'manual_selection')
        OR
        (event_type = 'ROLLBACK' AND candidate_result_sha256 IS NULL AND source_event_id IS NOT NULL AND reason_code = 'manual_rollback')
    )
) STRICT;

CREATE TRIGGER strategy_research_evidence_no_update
BEFORE UPDATE ON strategy_research_evidence
BEGIN
    SELECT RAISE(ABORT, 'strategy_research_evidence is insert-only');
END;

CREATE TRIGGER strategy_research_evidence_no_delete
BEFORE DELETE ON strategy_research_evidence
BEGIN
    SELECT RAISE(ABORT, 'strategy_research_evidence is insert-only');
END;

CREATE TRIGGER strategy_selection_events_no_update
BEFORE UPDATE ON strategy_selection_events
BEGIN
    SELECT RAISE(ABORT, 'strategy_selection_events is insert-only');
END;

CREATE TRIGGER strategy_selection_events_no_delete
BEFORE DELETE ON strategy_selection_events
BEGIN
    SELECT RAISE(ABORT, 'strategy_selection_events is insert-only');
END;

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
END;
