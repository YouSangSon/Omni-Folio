CREATE TABLE broker_snapshots (
    sequence INTEGER PRIMARY KEY,
    snapshot_id TEXT NOT NULL UNIQUE,
    provider TEXT NOT NULL CHECK (provider = 'kiwoom'),
    environment TEXT NOT NULL CHECK (environment IN ('production', 'mock')),
    exchange TEXT NOT NULL CHECK (exchange = 'KRX'),
    account_ref TEXT NOT NULL,
    ledger_account_id TEXT NOT NULL CHECK (ledger_account_id = 'account-main'),
    fetched_at TEXT NOT NULL,
    snapshot_sha256 TEXT NOT NULL,
    record_sha256 TEXT NOT NULL,
    record_json TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    UNIQUE (provider, environment, exchange, account_ref, fetched_at)
) STRICT;

CREATE INDEX broker_snapshots_latest_idx
ON broker_snapshots(provider, environment, exchange, account_ref, fetched_at DESC, sequence DESC);

CREATE TRIGGER broker_snapshots_no_update
BEFORE UPDATE ON broker_snapshots
BEGIN
    SELECT RAISE(ABORT, 'broker_snapshots is insert-only');
END;

CREATE TRIGGER broker_snapshots_no_delete
BEFORE DELETE ON broker_snapshots
BEGIN
    SELECT RAISE(ABORT, 'broker_snapshots is insert-only');
END;

CREATE TRIGGER events_no_update
BEFORE UPDATE ON events
BEGIN
    SELECT RAISE(ABORT, 'events is insert-only');
END;

CREATE TRIGGER events_no_delete
BEFORE DELETE ON events
BEGIN
    SELECT RAISE(ABORT, 'events is insert-only');
END;
