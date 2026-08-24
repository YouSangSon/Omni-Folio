CREATE TABLE execution_authority_events (
    sequence INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    account_ref TEXT NOT NULL,
    armed INTEGER NOT NULL CHECK (armed IN (0, 1)),
    lease_owner TEXT,
    fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
    lease_expires_at TEXT,
    reason_code TEXT NOT NULL CHECK (reason_code IN ('manual_arm', 'manual_halt', 'lease_acquired')),
    record_sha256 TEXT NOT NULL,
    record_json TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    UNIQUE (account_ref, fencing_token),
    CHECK (
        (lease_owner IS NULL AND lease_expires_at IS NULL) OR
        (armed = 1 AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)
    ),
    CHECK (armed = 1 OR lease_owner IS NULL)
) STRICT;

CREATE INDEX execution_authority_latest_idx
ON execution_authority_events(account_ref, sequence DESC);

CREATE TABLE risk_reservations (
    sequence INTEGER PRIMARY KEY,
    reservation_id TEXT NOT NULL UNIQUE,
    order_id TEXT NOT NULL UNIQUE,
    account_ref TEXT NOT NULL,
    policy_version TEXT NOT NULL CHECK (policy_version = 'credential_free_buy_v1'),
    authority_event_id TEXT NOT NULL,
    fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
    quantity TEXT NOT NULL,
    limit_price TEXT NOT NULL,
    limit_notional TEXT NOT NULL,
    risk_event_id TEXT NOT NULL UNIQUE,
    dispatch_event_id TEXT NOT NULL UNIQUE,
    record_sha256 TEXT NOT NULL,
    record_json TEXT NOT NULL,
    reserved_at TEXT NOT NULL,
    FOREIGN KEY (order_id) REFERENCES order_idempotency(order_id),
    FOREIGN KEY (authority_event_id) REFERENCES execution_authority_events(event_id)
) STRICT;

CREATE INDEX risk_reservations_account_idx
ON risk_reservations(account_ref, sequence);

ALTER TABLE order_events
ADD COLUMN authority_reservation_id TEXT REFERENCES risk_reservations(reservation_id);

CREATE TRIGGER execution_authority_events_no_update
BEFORE UPDATE ON execution_authority_events
BEGIN
    SELECT RAISE(ABORT, 'execution_authority_events is insert-only');
END;

CREATE TRIGGER execution_authority_events_no_delete
BEFORE DELETE ON execution_authority_events
BEGIN
    SELECT RAISE(ABORT, 'execution_authority_events is insert-only');
END;

CREATE TRIGGER risk_reservations_no_update
BEFORE UPDATE ON risk_reservations
BEGIN
    SELECT RAISE(ABORT, 'risk_reservations is insert-only');
END;

CREATE TRIGGER risk_reservations_no_delete
BEFORE DELETE ON risk_reservations
BEGIN
    SELECT RAISE(ABORT, 'risk_reservations is insert-only');
END;

CREATE TRIGGER order_events_risk_reservation_guard
BEFORE INSERT ON order_events
WHEN NEW.event_type = 'RISK_APPROVED'
BEGIN
    SELECT CASE WHEN NEW.authority_reservation_id IS NULL OR NOT EXISTS (
        SELECT 1 FROM risk_reservations
        WHERE reservation_id = NEW.authority_reservation_id
          AND order_id = NEW.order_id
          AND risk_event_id = NEW.event_id
          AND reservation_id = json_extract(NEW.event_json, '$.risk_reservation_id')
          AND policy_version = json_extract(NEW.event_json, '$.risk_policy_version')
          AND fencing_token = json_extract(NEW.event_json, '$.fencing_token')
    ) THEN RAISE(ABORT, 'risk approval requires an authority reservation') END;
END;

CREATE TRIGGER order_events_dispatch_reservation_guard
BEFORE INSERT ON order_events
WHEN NEW.event_type = 'SUBMIT_DISPATCHED'
BEGIN
    SELECT CASE WHEN NEW.authority_reservation_id IS NULL OR NOT EXISTS (
        SELECT 1 FROM risk_reservations
        WHERE reservation_id = NEW.authority_reservation_id
          AND order_id = NEW.order_id
          AND dispatch_event_id = NEW.event_id
          AND reservation_id = json_extract(NEW.event_json, '$.risk_reservation_id')
          AND policy_version = json_extract(NEW.event_json, '$.risk_policy_version')
          AND fencing_token = json_extract(NEW.event_json, '$.fencing_token')
    ) THEN RAISE(ABORT, 'submit dispatch requires an authority reservation') END;
END;

CREATE TRIGGER order_events_non_authority_reservation_guard
BEFORE INSERT ON order_events
WHEN NEW.event_type NOT IN ('RISK_APPROVED', 'SUBMIT_DISPATCHED')
     AND NEW.authority_reservation_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'authority reservation is invalid for this event');
END;
