DROP TRIGGER events_no_update;
DROP TRIGGER events_no_delete;

CREATE TABLE events_v8 (
    sequence INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    source_event_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('DEPOSIT', 'WITHDRAWAL', 'BUY', 'SELL', 'DIVIDEND', 'FEE', 'TAX', 'SPLIT', 'CASH_VOID')),
    occurred_at TEXT NOT NULL,
    instrument_id TEXT,
    symbol TEXT,
    quantity TEXT,
    price TEXT,
    fee TEXT,
    currency TEXT NOT NULL,
    amount TEXT NOT NULL,
    corrects_source_event_id TEXT,
    receipt_id TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    UNIQUE(account_id, source_event_id),
    UNIQUE(account_id, corrects_source_event_id),
    FOREIGN KEY (account_id, corrects_source_event_id)
        REFERENCES events_v8(account_id, source_event_id),
    CHECK (
        (type = 'DEPOSIT'
            AND corrects_source_event_id IS NULL
            AND instrument_id IS NULL AND symbol IS NULL AND quantity IS NULL AND price IS NULL AND fee IS NULL
            AND amount <> '0' AND amount NOT GLOB '-*')
        OR (type IN ('WITHDRAWAL', 'FEE', 'TAX')
            AND corrects_source_event_id IS NULL
            AND instrument_id IS NULL AND symbol IS NULL AND quantity IS NULL AND price IS NULL AND fee IS NULL
            AND amount GLOB '-*')
        OR (type = 'DIVIDEND'
            AND corrects_source_event_id IS NULL
            AND instrument_id IS NOT NULL AND symbol IS NOT NULL AND quantity IS NULL AND price IS NULL AND fee IS NULL
            AND amount <> '0' AND amount NOT GLOB '-*')
        OR (type = 'SPLIT'
            AND corrects_source_event_id IS NULL
            AND instrument_id IS NOT NULL AND symbol IS NOT NULL AND quantity IS NOT NULL AND price IS NULL AND fee IS NULL
            AND quantity <> '0' AND quantity NOT GLOB '-*' AND amount = '0')
        OR (type IN ('BUY', 'SELL')
            AND corrects_source_event_id IS NULL
            AND instrument_id IS NOT NULL AND symbol IS NOT NULL AND quantity IS NOT NULL AND price IS NOT NULL AND fee IS NOT NULL)
        OR (type = 'CASH_VOID'
            AND corrects_source_event_id IS NOT NULL
            AND instrument_id IS NULL AND symbol IS NULL AND quantity IS NULL AND price IS NULL AND fee IS NULL
            AND amount <> '0')
    )
) STRICT;

INSERT INTO events_v8 (
    sequence, event_id, source_event_id, account_id, type, occurred_at,
    instrument_id, symbol, quantity, price, fee, currency, amount,
    corrects_source_event_id, receipt_id, recorded_at
)
SELECT
    sequence, event_id, source_event_id, account_id, type, occurred_at,
    instrument_id, symbol, quantity, price, fee, currency, amount,
    NULL, receipt_id, recorded_at
FROM events;

DROP TABLE events;
ALTER TABLE events_v8 RENAME TO events;

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

CREATE TRIGGER events_cash_void_guard
BEFORE INSERT ON events
WHEN NEW.type = 'CASH_VOID'
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1
        FROM events target
        WHERE target.account_id = NEW.account_id
          AND target.source_event_id = NEW.corrects_source_event_id
          AND target.type IN ('DEPOSIT', 'WITHDRAWAL', 'DIVIDEND', 'FEE', 'TAX')
          AND target.currency = NEW.currency
          AND target.occurred_at <= NEW.occurred_at
          AND NEW.amount = CASE
              WHEN target.amount GLOB '-*' THEN substr(target.amount, 2)
              ELSE '-' || target.amount
          END
    ) THEN RAISE(ABORT, 'cash void must exactly reverse an eligible event') END;
END;
