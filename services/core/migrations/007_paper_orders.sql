DROP TRIGGER order_idempotency_no_update;
DROP TRIGGER order_idempotency_no_delete;

CREATE TABLE order_idempotency_v7 (
    provider TEXT NOT NULL CHECK (provider = 'kiwoom'),
    mode TEXT NOT NULL CHECK (mode IN ('synthetic', 'paper')),
    account_ref TEXT NOT NULL,
    client_order_id TEXT NOT NULL,
    request_sha256 TEXT NOT NULL,
    order_id TEXT NOT NULL UNIQUE,
    intent_json TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    PRIMARY KEY (provider, mode, account_ref, client_order_id)
) STRICT;

INSERT INTO order_idempotency_v7 (
    provider, mode, account_ref, client_order_id, request_sha256, order_id, intent_json, recorded_at
)
SELECT provider, mode, account_ref, client_order_id, request_sha256, order_id, intent_json, recorded_at
FROM order_idempotency;

DROP TABLE order_idempotency;
ALTER TABLE order_idempotency_v7 RENAME TO order_idempotency;

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
