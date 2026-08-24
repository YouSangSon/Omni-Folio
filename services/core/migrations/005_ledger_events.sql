DROP TRIGGER events_no_update;
DROP TRIGGER events_no_delete;

CREATE TABLE events_v5 (
    sequence INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    source_event_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('DEPOSIT', 'WITHDRAWAL', 'BUY', 'SELL', 'DIVIDEND', 'FEE', 'TAX', 'SPLIT')),
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
    UNIQUE(account_id, source_event_id),
    CHECK (
        (type = 'DEPOSIT'
            AND instrument_id IS NULL AND symbol IS NULL AND quantity IS NULL AND price IS NULL AND fee IS NULL
            AND amount <> '0' AND amount NOT GLOB '-*')
        OR (type IN ('WITHDRAWAL', 'FEE', 'TAX')
            AND instrument_id IS NULL AND symbol IS NULL AND quantity IS NULL AND price IS NULL AND fee IS NULL
            AND amount GLOB '-*')
        OR (type = 'DIVIDEND'
            AND instrument_id IS NOT NULL AND symbol IS NOT NULL AND quantity IS NULL AND price IS NULL AND fee IS NULL
            AND amount <> '0' AND amount NOT GLOB '-*')
        OR (type = 'SPLIT'
            AND instrument_id IS NOT NULL AND symbol IS NOT NULL AND quantity IS NOT NULL AND price IS NULL AND fee IS NULL
            AND quantity <> '0' AND quantity NOT GLOB '-*' AND amount = '0')
        OR (type IN ('BUY', 'SELL')
            AND instrument_id IS NOT NULL AND symbol IS NOT NULL AND quantity IS NOT NULL AND price IS NOT NULL AND fee IS NOT NULL)
    )
) STRICT;

INSERT INTO events_v5 (
    sequence, event_id, source_event_id, account_id, type, occurred_at,
    instrument_id, symbol, quantity, price, fee, currency, amount, receipt_id, recorded_at
)
SELECT
    sequence, event_id, source_event_id, account_id, type, occurred_at,
    instrument_id, symbol, quantity, price, fee, currency, amount, receipt_id, recorded_at
FROM events;

DROP TABLE events;
ALTER TABLE events_v5 RENAME TO events;

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
