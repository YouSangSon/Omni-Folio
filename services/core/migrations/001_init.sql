CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
) STRICT;

CREATE TABLE ledger_meta (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    revision INTEGER NOT NULL CHECK (revision >= 0),
    recorded_at TEXT NOT NULL,
    last_verified_at TEXT
) STRICT;

INSERT INTO ledger_meta(singleton, revision, recorded_at, last_verified_at)
VALUES (1, 0, '1970-01-01T00:00:00Z', NULL);

CREATE TABLE events (
    sequence INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    source_event_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('DEPOSIT', 'BUY', 'SELL')),
    occurred_at TEXT NOT NULL,
    instrument_id TEXT,
    symbol TEXT,
    quantity TEXT,
    price TEXT,
    fee TEXT,
    currency TEXT NOT NULL,
    amount TEXT NOT NULL,
    receipt_id TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    UNIQUE(account_id, source_event_id)
) STRICT;

CREATE TABLE previews (
    preview_id TEXT PRIMARY KEY,
    file_sha256 TEXT NOT NULL,
    ledger_revision INTEGER NOT NULL,
    can_apply INTEGER NOT NULL CHECK (can_apply IN (0, 1)),
    preview_json TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
) STRICT;

CREATE TABLE receipts (
    idempotency_key TEXT PRIMARY KEY,
    request_sha256 TEXT NOT NULL,
    receipt_id TEXT NOT NULL UNIQUE,
    receipt_json TEXT NOT NULL
) STRICT;
