CREATE TABLE paper_promotion_claims (
    sequence INTEGER PRIMARY KEY,
    attempt_id TEXT NOT NULL UNIQUE CHECK (length(CAST(attempt_id AS BLOB)) BETWEEN 1 AND 128 AND attempt_id = trim(attempt_id)),
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    record_json TEXT NOT NULL CHECK (length(CAST(record_json AS BLOB)) BETWEEN 1 AND 2097152),
    recorded_at TEXT NOT NULL CHECK (length(recorded_at) = 30 AND recorded_at GLOB '????-??-??T??:??:??.?????????Z' AND recorded_at NOT GLOB '*[^0-9TZ:.-]*')
) STRICT;

CREATE TABLE paper_promotion_decisions (
    sequence INTEGER PRIMARY KEY,
    attempt_id TEXT NOT NULL UNIQUE REFERENCES paper_promotion_claims(attempt_id) CHECK (length(CAST(attempt_id AS BLOB)) BETWEEN 1 AND 128 AND attempt_id = trim(attempt_id)),
    selection_event_id TEXT REFERENCES strategy_selection_events(event_id) DEFERRABLE INITIALLY DEFERRED CHECK (selection_event_id IS NULL OR (length(CAST(selection_event_id AS BLOB)) BETWEEN 1 AND 128 AND selection_event_id = trim(selection_event_id))),
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    record_json TEXT NOT NULL CHECK (length(CAST(record_json AS BLOB)) BETWEEN 1 AND 1048576),
    recorded_at TEXT NOT NULL CHECK (length(recorded_at) = 30 AND recorded_at GLOB '????-??-??T??:??:??.?????????Z' AND recorded_at NOT GLOB '*[^0-9TZ:.-]*')
) STRICT;

CREATE TABLE strategy_selection_events_new (
    sequence INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL CHECK (event_type IN ('SELECT', 'ROLLBACK')),
    candidate_result_sha256 TEXT REFERENCES strategy_research_evidence(result_sha256),
    expected_current_event_id TEXT NOT NULL,
    source_event_id TEXT REFERENCES strategy_selection_events(event_id),
    previous_selected_result_sha256 TEXT NOT NULL,
    selected_result_sha256 TEXT NOT NULL,
    reason_code TEXT NOT NULL CHECK (reason_code IN ('manual_selection', 'manual_rollback', 'automatic_performance_rollback', 'auto_comparison')),
    paper_evaluation_sequence INTEGER NOT NULL DEFAULT 0 CHECK (paper_evaluation_sequence >= 0),
    paper_performance_policy_event_id TEXT REFERENCES paper_performance_policy_events(policy_event_id),
    paper_promotion_attempt_id TEXT REFERENCES paper_promotion_decisions(attempt_id),
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    record_json TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    CHECK (
        (event_type = 'SELECT' AND candidate_result_sha256 IS NOT NULL AND source_event_id IS NULL AND reason_code = 'manual_selection' AND paper_performance_policy_event_id IS NULL AND paper_promotion_attempt_id IS NULL)
        OR
        (event_type = 'SELECT' AND candidate_result_sha256 IS NOT NULL AND source_event_id IS NULL AND reason_code = 'auto_comparison' AND paper_performance_policy_event_id IS NULL AND paper_promotion_attempt_id IS NOT NULL)
        OR
        (event_type = 'ROLLBACK' AND candidate_result_sha256 IS NULL AND source_event_id IS NOT NULL AND reason_code = 'manual_rollback' AND paper_performance_policy_event_id IS NULL AND paper_promotion_attempt_id IS NULL)
        OR
        (event_type = 'ROLLBACK' AND candidate_result_sha256 IS NULL AND source_event_id IS NOT NULL AND reason_code = 'automatic_performance_rollback' AND paper_performance_policy_event_id IS NOT NULL AND paper_promotion_attempt_id IS NULL)
    )
) STRICT;

INSERT INTO strategy_selection_events_new(sequence,event_id,event_type,candidate_result_sha256,expected_current_event_id,source_event_id,previous_selected_result_sha256,selected_result_sha256,reason_code,paper_evaluation_sequence,paper_performance_policy_event_id,paper_promotion_attempt_id,record_sha256,record_json,recorded_at)
SELECT sequence,event_id,event_type,candidate_result_sha256,expected_current_event_id,source_event_id,previous_selected_result_sha256,selected_result_sha256,reason_code,paper_evaluation_sequence,paper_performance_policy_event_id,NULL,record_sha256,record_json,recorded_at
FROM strategy_selection_events ORDER BY sequence;

DROP TRIGGER strategy_selection_events_no_update;
DROP TRIGGER strategy_selection_events_no_delete;
DROP TRIGGER strategy_selection_events_state_guard;
DROP TABLE strategy_selection_events;
ALTER TABLE strategy_selection_events_new RENAME TO strategy_selection_events;

CREATE TRIGGER strategy_selection_events_no_update BEFORE UPDATE ON strategy_selection_events
BEGIN SELECT RAISE(ABORT, 'strategy_selection_events is insert-only'); END;

CREATE TRIGGER strategy_selection_events_no_delete BEFORE DELETE ON strategy_selection_events
BEGIN SELECT RAISE(ABORT, 'strategy_selection_events is insert-only'); END;

CREATE TRIGGER paper_promotion_claims_no_update BEFORE UPDATE ON paper_promotion_claims
BEGIN SELECT RAISE(ABORT, 'paper_promotion_claims is insert-only'); END;

CREATE TRIGGER paper_promotion_claims_no_delete BEFORE DELETE ON paper_promotion_claims
BEGIN SELECT RAISE(ABORT, 'paper_promotion_claims is insert-only'); END;

CREATE TRIGGER paper_promotion_decisions_no_update BEFORE UPDATE ON paper_promotion_decisions
BEGIN SELECT RAISE(ABORT, 'paper_promotion_decisions is insert-only'); END;

CREATE TRIGGER paper_promotion_decisions_no_delete BEFORE DELETE ON paper_promotion_decisions
BEGIN SELECT RAISE(ABORT, 'paper_promotion_decisions is insert-only'); END;

CREATE TRIGGER strategy_selection_events_state_guard BEFORE INSERT ON strategy_selection_events
BEGIN
    SELECT CASE WHEN NEW.expected_current_event_id != COALESCE((SELECT event_id FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1), 'no_event') THEN RAISE(ABORT, 'strategy selection expected current event is stale') END;
    SELECT CASE WHEN NEW.previous_selected_result_sha256 != COALESCE((SELECT selected_result_sha256 FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1), 'no_strategy') THEN RAISE(ABORT, 'strategy selection previous result is stale') END;
    SELECT CASE WHEN NEW.event_type = 'SELECT' AND (NEW.selected_result_sha256 != NEW.candidate_result_sha256 OR NOT EXISTS (SELECT 1 FROM strategy_research_evidence WHERE result_sha256 = NEW.candidate_result_sha256 AND target = 'paper_candidate')) THEN RAISE(ABORT, 'strategy selection requires paper_candidate evidence') END;
    SELECT CASE WHEN NEW.event_type = 'ROLLBACK' AND NEW.source_event_id != COALESCE((SELECT event_id FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1), 'no_event') THEN RAISE(ABORT, 'strategy rollback source is stale') END;
    SELECT CASE WHEN NEW.paper_evaluation_sequence != COALESCE((SELECT MAX(sequence) FROM paper_evaluation_events), 0) THEN RAISE(ABORT, 'strategy selection paper evaluation sequence is stale') END;
    SELECT CASE WHEN NEW.reason_code = 'automatic_performance_rollback' AND NOT EXISTS (SELECT 1 FROM paper_performance_policy_events WHERE policy_event_id = NEW.paper_performance_policy_event_id AND decision = 'HALT_AND_ROLLBACK' AND rollback_selection_event_id = NEW.event_id AND strategy_selection_event_id = NEW.source_event_id) THEN RAISE(ABORT, 'automatic strategy rollback policy link is invalid') END;
    SELECT CASE WHEN NEW.reason_code = 'auto_comparison' AND NOT EXISTS (SELECT 1 FROM paper_promotion_decisions WHERE attempt_id = NEW.paper_promotion_attempt_id AND selection_event_id = NEW.event_id AND json_extract(record_json, '$.decision') = 'PROMOTE' AND json_extract(record_json, '$.candidate_result_sha256') = NEW.candidate_result_sha256 AND json_extract(record_json, '$.expected_current_event_id') = NEW.expected_current_event_id) THEN RAISE(ABORT, 'automatic strategy promotion decision link is invalid') END;
END;
