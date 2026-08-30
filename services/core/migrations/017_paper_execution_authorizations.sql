ALTER TABLE order_events
ADD COLUMN paper_authorization_id TEXT REFERENCES paper_execution_authorizations(authorization_id);

CREATE TABLE paper_execution_authorizations (
    sequence INTEGER PRIMARY KEY,
    authorization_id TEXT NOT NULL UNIQUE,
    schema_version TEXT NOT NULL CHECK (schema_version = 'paper-execution-authorization.v1'),
    order_id TEXT NOT NULL UNIQUE REFERENCES order_idempotency(order_id),
    account_ref TEXT NOT NULL,
    paper_accounting_session_id TEXT NOT NULL REFERENCES paper_accounting_sessions(session_id),
    execution_policy_sha256 TEXT NOT NULL CHECK (length(execution_policy_sha256) = 64 AND execution_policy_sha256 NOT GLOB '*[^0-9a-f]*'),
    policy_version TEXT NOT NULL CHECK (policy_version = 'paper_accounting_v1'),
    side TEXT NOT NULL CHECK (side IN ('BUY', 'SELL')),
    quantity TEXT NOT NULL CHECK (length(quantity) BETWEEN 1 AND 32 AND substr(quantity, 1, 1) GLOB '[1-9]' AND quantity NOT GLOB '*[^0-9]*'),
    authority_event_id TEXT NOT NULL REFERENCES execution_authority_events(event_id),
    fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
    risk_event_id TEXT NOT NULL UNIQUE,
    dispatch_event_id TEXT NOT NULL UNIQUE,
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    record_json TEXT NOT NULL CHECK (length(record_json) BETWEEN 1 AND 1048576),
    authorized_at TEXT NOT NULL
) STRICT;

CREATE INDEX paper_execution_authorizations_account_idx
ON paper_execution_authorizations(account_ref, sequence);

CREATE TRIGGER paper_execution_authorizations_no_update
BEFORE UPDATE ON paper_execution_authorizations
BEGIN
    SELECT RAISE(ABORT, 'paper_execution_authorizations is insert-only');
END;

CREATE TRIGGER paper_execution_authorizations_no_delete
BEFORE DELETE ON paper_execution_authorizations
BEGIN
    SELECT RAISE(ABORT, 'paper_execution_authorizations is insert-only');
END;

CREATE TRIGGER paper_execution_authorizations_state_guard
BEFORE INSERT ON paper_execution_authorizations
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1
        FROM order_idempotency orders
        JOIN paper_accounting_sessions session ON session.session_id=json_extract(orders.intent_json, '$.paper_accounting_session_id')
        JOIN execution_authority_events authority ON authority.event_id=NEW.authority_event_id
        WHERE orders.order_id=NEW.order_id AND orders.mode='paper' AND orders.account_ref=NEW.account_ref
          AND json_extract(orders.intent_json, '$.signal_schema_version')='paper-signal.v3'
          AND json_extract(orders.intent_json, '$.paper_accounting_policy_version')=NEW.policy_version
          AND json_extract(orders.intent_json, '$.paper_accounting_session_id')=NEW.paper_accounting_session_id
          AND json_extract(orders.intent_json, '$.execution_policy_sha256')=NEW.execution_policy_sha256
          AND json_extract(orders.intent_json, '$.side')=NEW.side
          AND json_extract(orders.intent_json, '$.quantity')=NEW.quantity
          AND session.account_ref=NEW.account_ref
          AND session.execution_policy_sha256=NEW.execution_policy_sha256
          AND authority.account_ref=NEW.account_ref AND authority.armed=1
          AND authority.fencing_token=NEW.fencing_token AND authority.lease_owner IS NOT NULL
          AND authority.reason_code='lease_acquired'
          AND julianday(NEW.authorized_at) >= julianday(authority.recorded_at)
          AND julianday(NEW.authorized_at) < julianday(authority.lease_expires_at)
          AND authority.event_id=(SELECT event_id FROM execution_authority_events WHERE account_ref=NEW.account_ref ORDER BY sequence DESC LIMIT 1)
    ) THEN RAISE(ABORT, 'paper execution authorization binding is invalid') END;
END;

DROP TRIGGER order_idempotency_legacy_paper_signal_guard;

CREATE TRIGGER order_idempotency_legacy_paper_signal_guard
BEFORE INSERT ON order_idempotency
WHEN NEW.mode = 'paper'
  AND COALESCE(json_extract(NEW.intent_json, '$.signal_schema_version'), '') != 'paper-signal.v3'
BEGIN
    SELECT RAISE(ABORT, 'new non-v3 paper orders are recovery-only');
END;

CREATE TRIGGER order_idempotency_capitalized_paper_guard
BEFORE INSERT ON order_idempotency
WHEN NEW.mode = 'paper'
  AND json_extract(NEW.intent_json, '$.signal_schema_version') = 'paper-signal.v3'
BEGIN
    SELECT CASE WHEN json_extract(NEW.intent_json, '$.signal_schema_version') IS NOT 'paper-signal.v3'
        OR json_extract(NEW.intent_json, '$.provider') IS NOT NEW.provider
        OR json_extract(NEW.intent_json, '$.mode') IS NOT NEW.mode
        OR json_extract(NEW.intent_json, '$.account_ref') IS NOT NEW.account_ref
        OR json_extract(NEW.intent_json, '$.client_order_id') IS NOT NEW.client_order_id
        OR json_extract(NEW.intent_json, '$.order_type') IS NOT 'PAPER_MARKET'
        OR COALESCE(json_extract(NEW.intent_json, '$.limit_price'), '') != ''
        OR json_extract(NEW.intent_json, '$.paper_accounting_policy_version') IS NOT 'paper_accounting_v1'
        OR COALESCE(json_type(NEW.intent_json, '$.quantity'), '') != 'text'
        OR length(json_extract(NEW.intent_json, '$.quantity')) NOT BETWEEN 1 AND 32
        OR substr(json_extract(NEW.intent_json, '$.quantity'), 1, 1) NOT GLOB '[1-9]'
        OR json_extract(NEW.intent_json, '$.quantity') GLOB '*[^0-9]*'
        OR COALESCE(json_type(NEW.intent_json, '$.signal_target_quantity'), '') != 'text'
        OR NOT EXISTS (
            SELECT 1
            FROM paper_signal_events signal
            JOIN paper_accounting_sessions session ON session.session_id = signal.paper_accounting_session_id
            JOIN strategy_selection_events selection ON selection.event_id = signal.strategy_selection_event_id
            WHERE signal.event_id = json_extract(NEW.intent_json, '$.paper_signal_event_id')
              AND signal.account_ref = NEW.account_ref
              AND signal.paper_accounting_session_id = json_extract(NEW.intent_json, '$.paper_accounting_session_id')
              AND signal.execution_policy_sha256 = json_extract(NEW.intent_json, '$.execution_policy_sha256')
              AND signal.strategy_result_sha256 = json_extract(NEW.intent_json, '$.strategy_result_sha256')
              AND signal.strategy_selection_event_id = json_extract(NEW.intent_json, '$.strategy_selection_event_id')
              AND signal.signal_id = json_extract(NEW.intent_json, '$.signal_id')
              AND signal.data_sha256 = json_extract(NEW.intent_json, '$.signal_data_sha256')
              AND signal.data_as_of = json_extract(NEW.intent_json, '$.signal_data_as_of')
              AND signal.generated_at = json_extract(NEW.intent_json, '$.signal_generated_at')
              AND signal.expires_at = json_extract(NEW.intent_json, '$.signal_expires_at')
              AND signal.target_quantity = json_extract(NEW.intent_json, '$.signal_target_quantity')
              AND signal.symbol = json_extract(NEW.intent_json, '$.symbol')
              AND session.account_ref = NEW.account_ref
              AND session.execution_policy_sha256 = signal.execution_policy_sha256
              AND selection.selected_result_sha256 = signal.strategy_result_sha256
        )
        OR json_extract(NEW.intent_json, '$.side') IS NOT 'BUY'
        OR json_extract(NEW.intent_json, '$.signal_target_quantity') IS NOT json_extract(NEW.intent_json, '$.quantity') COLLATE BINARY
        OR EXISTS (
            SELECT 1 FROM order_idempotency prior
            WHERE prior.mode='paper' AND prior.account_ref=NEW.account_ref
              AND json_extract(prior.intent_json, '$.signal_schema_version')='paper-signal.v3'
              AND json_extract(prior.intent_json, '$.symbol')=json_extract(NEW.intent_json, '$.symbol')
        )
    THEN RAISE(ABORT, 'capitalized paper intent binding is invalid') END;
END;

DROP TRIGGER order_events_risk_reservation_guard;
DROP TRIGGER order_events_dispatch_reservation_guard;
DROP TRIGGER order_events_non_authority_reservation_guard;

CREATE TRIGGER order_events_risk_reservation_guard
BEFORE INSERT ON order_events
WHEN NEW.event_type = 'RISK_APPROVED'
BEGIN
    SELECT CASE WHEN NOT (
        (EXISTS (SELECT 1 FROM order_idempotency WHERE order_id=NEW.order_id AND mode='synthetic')
          AND NEW.authority_reservation_id IS NOT NULL AND NEW.paper_authorization_id IS NULL
          AND EXISTS (SELECT 1 FROM risk_reservations WHERE reservation_id=NEW.authority_reservation_id
            AND order_id=NEW.order_id AND risk_event_id=NEW.event_id
            AND reservation_id=json_extract(NEW.event_json, '$.risk_reservation_id')
            AND policy_version=json_extract(NEW.event_json, '$.risk_policy_version')
            AND fencing_token=json_extract(NEW.event_json, '$.fencing_token')))
        OR
        (EXISTS (SELECT 1 FROM order_idempotency WHERE order_id=NEW.order_id AND mode='paper'
          AND json_extract(intent_json, '$.signal_schema_version')='paper-signal.v3')
          AND NEW.authority_reservation_id IS NULL AND NEW.paper_authorization_id IS NOT NULL
          AND EXISTS (SELECT 1 FROM paper_execution_authorizations WHERE authorization_id=NEW.paper_authorization_id
            AND order_id=NEW.order_id AND risk_event_id=NEW.event_id
            AND authorization_id=json_extract(NEW.event_json, '$.paper_authorization_id')
            AND policy_version=json_extract(NEW.event_json, '$.risk_policy_version')
            AND fencing_token=json_extract(NEW.event_json, '$.fencing_token')))
    ) THEN RAISE(ABORT, 'risk approval requires its mode authority') END;
END;

CREATE TRIGGER order_events_dispatch_reservation_guard
BEFORE INSERT ON order_events
WHEN NEW.event_type = 'SUBMIT_DISPATCHED'
BEGIN
    SELECT CASE WHEN NOT (
        (EXISTS (SELECT 1 FROM order_idempotency WHERE order_id=NEW.order_id AND mode='synthetic')
          AND NEW.authority_reservation_id IS NOT NULL AND NEW.paper_authorization_id IS NULL
          AND EXISTS (SELECT 1 FROM risk_reservations WHERE reservation_id=NEW.authority_reservation_id
            AND order_id=NEW.order_id AND dispatch_event_id=NEW.event_id
            AND reservation_id=json_extract(NEW.event_json, '$.risk_reservation_id')
            AND policy_version=json_extract(NEW.event_json, '$.risk_policy_version')
            AND fencing_token=json_extract(NEW.event_json, '$.fencing_token')))
        OR
        (EXISTS (SELECT 1 FROM order_idempotency WHERE order_id=NEW.order_id AND mode='paper'
          AND json_extract(intent_json, '$.signal_schema_version')='paper-signal.v3')
          AND NEW.authority_reservation_id IS NULL AND NEW.paper_authorization_id IS NOT NULL
          AND EXISTS (SELECT 1 FROM paper_execution_authorizations WHERE authorization_id=NEW.paper_authorization_id
            AND order_id=NEW.order_id AND dispatch_event_id=NEW.event_id
            AND authorization_id=json_extract(NEW.event_json, '$.paper_authorization_id')
            AND policy_version=json_extract(NEW.event_json, '$.risk_policy_version')
            AND fencing_token=json_extract(NEW.event_json, '$.fencing_token')))
    ) THEN RAISE(ABORT, 'submit dispatch requires its mode authority') END;
END;

CREATE TRIGGER order_events_non_authority_reservation_guard
BEFORE INSERT ON order_events
WHEN NEW.event_type NOT IN ('RISK_APPROVED', 'SUBMIT_DISPATCHED')
  AND (NEW.authority_reservation_id IS NOT NULL OR NEW.paper_authorization_id IS NOT NULL)
BEGIN
    SELECT RAISE(ABORT, 'authority reference is invalid for this event');
END;
