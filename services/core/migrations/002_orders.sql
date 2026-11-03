CREATE TABLE order_idempotency (
    provider TEXT NOT NULL CHECK (provider = 'kiwoom'),
    mode TEXT NOT NULL CHECK (mode = 'synthetic'),
    account_ref TEXT NOT NULL,
    client_order_id TEXT NOT NULL,
    request_sha256 TEXT NOT NULL,
    order_id TEXT NOT NULL UNIQUE,
    intent_json TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    PRIMARY KEY (provider, mode, account_ref, client_order_id)
) STRICT;

CREATE TABLE order_events (
    sequence INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    event_sha256 TEXT NOT NULL,
    order_id TEXT NOT NULL,
    event_type TEXT NOT NULL CHECK (event_type IN (
        'INTENT_RECORDED',
        'RISK_APPROVED',
        'RISK_REJECTED',
        'SUBMIT_DISPATCHED',
        'SUBMIT_ACKNOWLEDGED',
        'SUBMIT_REJECTED',
        'FILL_RECORDED',
        'CANCEL_DISPATCHED',
        'CANCEL_ACKNOWLEDGED',
        'CANCEL_REJECTED'
    )),
    source TEXT NOT NULL CHECK (source IN ('synthetic', 'reconciliation')),
    provider_order_ref TEXT,
    provider_execution_ref TEXT UNIQUE,
    event_json TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    FOREIGN KEY (order_id) REFERENCES order_idempotency(order_id)
) STRICT;

CREATE INDEX order_events_order_sequence_idx ON order_events(order_id, sequence);
CREATE INDEX order_events_provider_order_ref_idx ON order_events(provider_order_ref);

CREATE TRIGGER order_idempotency_no_update
BEFORE UPDATE ON order_idempotency
BEGIN
    SELECT RAISE(ABORT, 'order_idempotency is insert-only');
END;

CREATE TRIGGER order_idempotency_no_delete
BEFORE DELETE ON order_idempotency
BEGIN
    SELECT RAISE(ABORT, 'order_idempotency is insert-only');
END;

CREATE TRIGGER order_events_no_update
BEFORE UPDATE ON order_events
BEGIN
    SELECT RAISE(ABORT, 'order_events is insert-only');
END;

CREATE TRIGGER order_events_no_delete
BEFORE DELETE ON order_events
BEGIN
    SELECT RAISE(ABORT, 'order_events is insert-only');
END;
