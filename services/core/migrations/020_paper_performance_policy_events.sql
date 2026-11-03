CREATE TABLE execution_authority_events_new (
    sequence INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    account_ref TEXT NOT NULL,
    armed INTEGER NOT NULL CHECK (armed IN (0, 1)),
    lease_owner TEXT,
    fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
    lease_expires_at TEXT,
    reason_code TEXT NOT NULL CHECK (reason_code IN ('manual_arm', 'manual_halt', 'lease_acquired', 'automatic_performance_halt')),
    paper_performance_policy_event_id TEXT REFERENCES paper_performance_policy_events(policy_event_id),
    record_sha256 TEXT NOT NULL,
    record_json TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    UNIQUE (account_ref, fencing_token),
    CHECK (
        (lease_owner IS NULL AND lease_expires_at IS NULL) OR
        (armed = 1 AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)
    ),
    CHECK (armed = 1 OR lease_owner IS NULL),
    CHECK (
        (reason_code IN ('manual_arm', 'manual_halt', 'lease_acquired') AND paper_performance_policy_event_id IS NULL)
        OR (reason_code = 'automatic_performance_halt' AND paper_performance_policy_event_id IS NOT NULL)
    )
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
    reason_code TEXT NOT NULL CHECK (reason_code IN ('manual_selection', 'manual_rollback', 'automatic_performance_rollback')),
    paper_evaluation_sequence INTEGER NOT NULL DEFAULT 0 CHECK (paper_evaluation_sequence >= 0),
    paper_performance_policy_event_id TEXT REFERENCES paper_performance_policy_events(policy_event_id),
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    record_json TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    CHECK (
        (event_type = 'SELECT' AND candidate_result_sha256 IS NOT NULL AND source_event_id IS NULL AND reason_code = 'manual_selection' AND paper_performance_policy_event_id IS NULL)
        OR
        (event_type = 'ROLLBACK' AND candidate_result_sha256 IS NULL AND source_event_id IS NOT NULL AND reason_code = 'manual_rollback' AND paper_performance_policy_event_id IS NULL)
        OR
        (event_type = 'ROLLBACK' AND candidate_result_sha256 IS NULL AND source_event_id IS NOT NULL AND reason_code = 'automatic_performance_rollback' AND paper_performance_policy_event_id IS NOT NULL)
    )
) STRICT;

INSERT INTO execution_authority_events_new(sequence,event_id,account_ref,armed,lease_owner,fencing_token,lease_expires_at,reason_code,paper_performance_policy_event_id,record_sha256,record_json,recorded_at)
SELECT sequence,event_id,account_ref,armed,lease_owner,fencing_token,lease_expires_at,reason_code,NULL,record_sha256,record_json,recorded_at
FROM execution_authority_events ORDER BY sequence;

INSERT INTO strategy_selection_events_new(sequence,event_id,event_type,candidate_result_sha256,expected_current_event_id,source_event_id,previous_selected_result_sha256,selected_result_sha256,reason_code,paper_evaluation_sequence,paper_performance_policy_event_id,record_sha256,record_json,recorded_at)
SELECT sequence,event_id,event_type,candidate_result_sha256,expected_current_event_id,source_event_id,previous_selected_result_sha256,selected_result_sha256,reason_code,paper_evaluation_sequence,NULL,record_sha256,record_json,recorded_at
FROM strategy_selection_events ORDER BY sequence;

DROP TRIGGER execution_authority_events_no_update;
DROP TRIGGER execution_authority_events_no_delete;
DROP TRIGGER strategy_selection_events_no_update;
DROP TRIGGER strategy_selection_events_no_delete;
DROP TRIGGER strategy_selection_events_state_guard;
DROP INDEX execution_authority_latest_idx;
DROP TABLE execution_authority_events;
DROP TABLE strategy_selection_events;
ALTER TABLE execution_authority_events_new RENAME TO execution_authority_events;
ALTER TABLE strategy_selection_events_new RENAME TO strategy_selection_events;

CREATE TABLE paper_performance_policy_events (
    sequence INTEGER PRIMARY KEY,
    policy_event_id TEXT NOT NULL UNIQUE CHECK (length(policy_event_id) BETWEEN 1 AND 128 AND policy_event_id = trim(policy_event_id)),
    schema_version TEXT NOT NULL CHECK (schema_version = 'paper-strategy-performance-policy.v1'),
    policy_version TEXT NOT NULL CHECK (policy_version = 'paper-strategy-performance-safety.v1'),
    account_ref TEXT NOT NULL CHECK (length(account_ref) BETWEEN 1 AND 128 AND account_ref = trim(account_ref) AND account_ref GLOB 'kiwoom_account_*'),
    paper_accounting_session_id TEXT NOT NULL REFERENCES paper_accounting_sessions(session_id),
    strategy_selection_event_id TEXT NOT NULL REFERENCES strategy_selection_events(event_id),
    selected_strategy_result_ref TEXT NOT NULL REFERENCES strategy_research_evidence(result_sha256),
    strategy_performance_id TEXT NOT NULL REFERENCES paper_strategy_performance_events(strategy_performance_id),
    baseline_performance_id TEXT NOT NULL REFERENCES paper_performance_events(performance_id),
    latest_performance_id TEXT NOT NULL REFERENCES paper_performance_events(performance_id),
    sample_count INTEGER NOT NULL CHECK (sample_count > 0),
    expected_previous_policy_event_id TEXT NOT NULL CHECK (length(expected_previous_policy_event_id) BETWEEN 1 AND 128 AND expected_previous_policy_event_id = trim(expected_previous_policy_event_id)),
    strategy_selection_sequence_cutoff INTEGER NOT NULL CHECK (strategy_selection_sequence_cutoff >= 0),
    paper_strategy_performance_sequence_cutoff INTEGER NOT NULL CHECK (paper_strategy_performance_sequence_cutoff >= 0),
    execution_authority_sequence_cutoff INTEGER NOT NULL CHECK (execution_authority_sequence_cutoff >= 0),
    decision TEXT NOT NULL CHECK (decision IN ('INSUFFICIENT', 'HOLD', 'HALT_AND_ROLLBACK')),
    reason_code TEXT NOT NULL CHECK (reason_code IN ('minimum_same_selection_samples_not_met', 'within_local_paper_safety_bounds', 'max_drawdown_limit_reached', 'cumulative_return_floor_reached')),
    rollback_selection_event_id TEXT REFERENCES strategy_selection_events(event_id) DEFERRABLE INITIALLY DEFERRED,
    automatic_halt_count INTEGER NOT NULL CHECK (automatic_halt_count >= 0),
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    record_json TEXT NOT NULL CHECK (length(record_json) BETWEEN 1 AND 1048576),
    recorded_at TEXT NOT NULL CHECK (length(recorded_at) = 30 AND recorded_at GLOB '????-??-??T??:??:??.?????????Z' AND recorded_at NOT GLOB '*[^0-9TZ:.-]*'),
    UNIQUE (policy_version, account_ref, strategy_selection_event_id, strategy_performance_id),
    CHECK (
        (decision = 'INSUFFICIENT' AND reason_code = 'minimum_same_selection_samples_not_met' AND rollback_selection_event_id IS NULL AND automatic_halt_count = 0)
        OR (decision = 'HOLD' AND reason_code = 'within_local_paper_safety_bounds' AND rollback_selection_event_id IS NULL AND automatic_halt_count = 0)
        OR (decision = 'HALT_AND_ROLLBACK' AND reason_code IN ('max_drawdown_limit_reached', 'cumulative_return_floor_reached') AND rollback_selection_event_id IS NOT NULL)
    )
) STRICT;

CREATE INDEX execution_authority_latest_idx ON execution_authority_events(account_ref, sequence DESC);
CREATE INDEX paper_performance_policy_events_account_selection_idx ON paper_performance_policy_events(account_ref, strategy_selection_event_id, sequence DESC);

CREATE TRIGGER execution_authority_events_no_update BEFORE UPDATE ON execution_authority_events
BEGIN SELECT RAISE(ABORT, 'execution_authority_events is insert-only'); END;
CREATE TRIGGER execution_authority_events_no_delete BEFORE DELETE ON execution_authority_events
BEGIN SELECT RAISE(ABORT, 'execution_authority_events is insert-only'); END;
CREATE TRIGGER strategy_selection_events_no_update BEFORE UPDATE ON strategy_selection_events
BEGIN SELECT RAISE(ABORT, 'strategy_selection_events is insert-only'); END;
CREATE TRIGGER strategy_selection_events_no_delete BEFORE DELETE ON strategy_selection_events
BEGIN SELECT RAISE(ABORT, 'strategy_selection_events is insert-only'); END;
CREATE TRIGGER paper_performance_policy_events_no_update BEFORE UPDATE ON paper_performance_policy_events
BEGIN SELECT RAISE(ABORT, 'paper_performance_policy_events is insert-only'); END;
CREATE TRIGGER paper_performance_policy_events_no_delete BEFORE DELETE ON paper_performance_policy_events
BEGIN SELECT RAISE(ABORT, 'paper_performance_policy_events is insert-only'); END;

CREATE TRIGGER strategy_selection_events_state_guard BEFORE INSERT ON strategy_selection_events
BEGIN
    SELECT CASE WHEN NEW.expected_current_event_id != COALESCE((SELECT event_id FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1), 'no_event') THEN RAISE(ABORT, 'strategy selection expected current event is stale') END;
    SELECT CASE WHEN NEW.previous_selected_result_sha256 != COALESCE((SELECT selected_result_sha256 FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1), 'no_strategy') THEN RAISE(ABORT, 'strategy selection previous result is stale') END;
    SELECT CASE WHEN NEW.event_type = 'SELECT' AND (NEW.selected_result_sha256 != NEW.candidate_result_sha256 OR NOT EXISTS (SELECT 1 FROM strategy_research_evidence WHERE result_sha256 = NEW.candidate_result_sha256 AND target = 'paper_candidate')) THEN RAISE(ABORT, 'strategy selection requires paper_candidate evidence') END;
    SELECT CASE WHEN NEW.event_type = 'ROLLBACK' AND NEW.source_event_id != COALESCE((SELECT event_id FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1), 'no_event') THEN RAISE(ABORT, 'strategy rollback source is stale') END;
    SELECT CASE WHEN NEW.paper_evaluation_sequence != COALESCE((SELECT MAX(sequence) FROM paper_evaluation_events), 0) THEN RAISE(ABORT, 'strategy selection paper evaluation sequence is stale') END;
    SELECT CASE WHEN NEW.reason_code = 'automatic_performance_rollback' AND NOT EXISTS (SELECT 1 FROM paper_performance_policy_events WHERE policy_event_id = NEW.paper_performance_policy_event_id AND decision = 'HALT_AND_ROLLBACK' AND rollback_selection_event_id = NEW.event_id AND strategy_selection_event_id = NEW.source_event_id) THEN RAISE(ABORT, 'automatic strategy rollback policy link is invalid') END;
END;

CREATE TRIGGER execution_authority_events_state_guard BEFORE INSERT ON execution_authority_events
WHEN NEW.reason_code = 'automatic_performance_halt'
BEGIN
    SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM paper_performance_policy_events WHERE policy_event_id = NEW.paper_performance_policy_event_id AND decision = 'HALT_AND_ROLLBACK') THEN RAISE(ABORT, 'automatic execution halt policy link is invalid') END;
END;

CREATE TRIGGER paper_performance_policy_events_state_guard BEFORE INSERT ON paper_performance_policy_events
BEGIN
    SELECT CASE WHEN NEW.expected_previous_policy_event_id != COALESCE((SELECT policy_event_id FROM paper_performance_policy_events WHERE account_ref = NEW.account_ref AND strategy_selection_event_id = NEW.strategy_selection_event_id ORDER BY sequence DESC LIMIT 1), 'no_policy_event') THEN RAISE(ABORT, 'paper performance policy predecessor is stale') END;
    SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM strategy_selection_events WHERE event_id = NEW.strategy_selection_event_id AND selected_result_sha256 = NEW.selected_strategy_result_ref AND sequence = NEW.strategy_selection_sequence_cutoff) THEN RAISE(ABORT, 'paper performance policy selection is stale') END;
    SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM paper_strategy_performance_events candidate WHERE candidate.strategy_performance_id = NEW.strategy_performance_id AND candidate.account_ref = NEW.account_ref AND candidate.paper_accounting_session_id = NEW.paper_accounting_session_id AND candidate.strategy_selection_event_id = NEW.strategy_selection_event_id AND candidate.selected_strategy_result_ref = NEW.selected_strategy_result_ref AND candidate.baseline_performance_id = NEW.baseline_performance_id AND candidate.latest_performance_id = NEW.latest_performance_id AND candidate.sample_count = NEW.sample_count AND candidate.sequence <= NEW.paper_strategy_performance_sequence_cutoff AND NOT EXISTS (SELECT 1 FROM paper_strategy_performance_events later WHERE later.account_ref = NEW.account_ref AND later.sequence <= NEW.paper_strategy_performance_sequence_cutoff AND later.sequence > candidate.sequence)) THEN RAISE(ABORT, 'paper performance policy strategy evidence is stale') END;
END;
