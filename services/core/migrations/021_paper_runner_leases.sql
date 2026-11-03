CREATE UNIQUE INDEX IF NOT EXISTS strategy_selection_events_runner_binding_idx
ON strategy_selection_events(event_id, selected_result_sha256);

CREATE TABLE paper_runner_leases (
    scope TEXT PRIMARY KEY CHECK (scope = 'paper_strategy_selection'),
    fencing_token INTEGER NOT NULL CHECK (fencing_token >= 0),
    owner_id TEXT,
    account_ref TEXT,
    heartbeat_at_ns INTEGER,
    expires_at_ns INTEGER,
    strategy_selection_event_id TEXT,
    selected_result_sha256 TEXT,
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    record_json TEXT NOT NULL CHECK (length(record_json) BETWEEN 1 AND 1048576),
    FOREIGN KEY (strategy_selection_event_id, selected_result_sha256)
        REFERENCES strategy_selection_events(event_id, selected_result_sha256),
    CHECK (
        (owner_id IS NULL AND account_ref IS NULL AND heartbeat_at_ns IS NULL AND expires_at_ns IS NULL
            AND strategy_selection_event_id IS NULL AND selected_result_sha256 IS NULL)
        OR
        (fencing_token > 0 AND owner_id IS NOT NULL AND account_ref IS NOT NULL
            AND heartbeat_at_ns IS NOT NULL AND expires_at_ns IS NOT NULL
            AND strategy_selection_event_id IS NOT NULL AND selected_result_sha256 IS NOT NULL
            AND heartbeat_at_ns > 0 AND expires_at_ns - heartbeat_at_ns = 30000000000)
    )
) STRICT;

INSERT INTO paper_runner_leases(
    scope,fencing_token,owner_id,account_ref,heartbeat_at_ns,expires_at_ns,
    strategy_selection_event_id,selected_result_sha256,record_sha256,record_json
) VALUES(
    'paper_strategy_selection',0,NULL,NULL,NULL,NULL,NULL,NULL,
    'fc7dd827c2f410ec2ebdb5a110caa5f1ee371a55549a28039eb67fde3ce1deb1',
    '{"scope":"paper_strategy_selection","fencing_token":0,"owner_id":"","account_ref":"","heartbeat_at_ns":0,"expires_at_ns":0,"strategy_selection_event_id":"","selected_result_sha256":""}'
);

CREATE TRIGGER paper_runner_leases_no_delete
BEFORE DELETE ON paper_runner_leases
BEGIN
    SELECT RAISE(ABORT, 'paper_runner_leases singleton cannot be deleted');
END;

CREATE TRIGGER paper_runner_leases_state_guard
BEFORE UPDATE ON paper_runner_leases
BEGIN
    SELECT CASE WHEN NEW.scope != OLD.scope THEN
        RAISE(ABORT, 'paper runner lease scope is immutable')
    END;
    SELECT CASE WHEN NOT (
        (
            OLD.owner_id IS NOT NULL
            AND NEW.fencing_token = OLD.fencing_token
            AND NEW.owner_id = OLD.owner_id
            AND NEW.account_ref = OLD.account_ref
            AND NEW.strategy_selection_event_id = OLD.strategy_selection_event_id
            AND NEW.selected_result_sha256 = OLD.selected_result_sha256
            AND NEW.heartbeat_at_ns >= OLD.heartbeat_at_ns
            AND NEW.heartbeat_at_ns < OLD.expires_at_ns
            AND NEW.expires_at_ns - NEW.heartbeat_at_ns = 30000000000
        )
        OR
        (
            OLD.owner_id IS NOT NULL
            AND NEW.fencing_token = OLD.fencing_token
            AND NEW.owner_id IS NULL AND NEW.account_ref IS NULL
            AND NEW.heartbeat_at_ns IS NULL AND NEW.expires_at_ns IS NULL
            AND NEW.strategy_selection_event_id IS NULL AND NEW.selected_result_sha256 IS NULL
        )
        OR
        (
            NEW.owner_id IS NOT NULL
            AND OLD.fencing_token < 9223372036854775807
            AND NEW.fencing_token = OLD.fencing_token + 1
            AND NEW.heartbeat_at_ns > 0
            AND NEW.expires_at_ns - NEW.heartbeat_at_ns = 30000000000
            AND (
                OLD.owner_id IS NULL
                OR (NEW.heartbeat_at_ns >= OLD.expires_at_ns AND NEW.heartbeat_at_ns >= OLD.heartbeat_at_ns)
            )
        )
    ) THEN RAISE(ABORT, 'paper runner lease transition is invalid') END;
END;
