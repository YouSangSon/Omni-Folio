CREATE TABLE instrument_listing_events (
    sequence INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL CHECK (event_type IN ('DECLARE', 'REVOKE')),
    authority TEXT NOT NULL CHECK (authority = 'owner_declared'),
    instrument_id TEXT NOT NULL CHECK (
        length(instrument_id) BETWEEN 1 AND 128
        AND instrument_id = trim(instrument_id)
    ),
    venue TEXT NOT NULL CHECK (
        length(venue) BETWEEN 1 AND 128
        AND venue = upper(venue)
    ),
    symbol TEXT NOT NULL CHECK (
        length(symbol) BETWEEN 1 AND 128
        AND symbol = upper(symbol)
    ),
    currency TEXT NOT NULL CHECK (
        length(currency) = 3
        AND currency GLOB '[A-Z][A-Z][A-Z]'
    ),
    expected_previous_event_id TEXT NOT NULL CHECK (
        length(expected_previous_event_id) BETWEEN 1 AND 128
        AND expected_previous_event_id = trim(expected_previous_event_id)
    ),
    record_sha256 TEXT NOT NULL CHECK (
        length(record_sha256) = 64
        AND record_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    recorded_at TEXT NOT NULL CHECK (
        recorded_at GLOB '????-??-??T??:??:??Z'
        OR recorded_at GLOB '????-??-??T??:??:??.*Z'
    ),
    CHECK (length(event_id) BETWEEN 1 AND 128 AND event_id = trim(event_id))
) STRICT;

CREATE INDEX instrument_listing_events_current_idx
ON instrument_listing_events(venue, symbol, currency, sequence DESC);

CREATE TRIGGER instrument_listing_events_no_update
BEFORE UPDATE ON instrument_listing_events
BEGIN
    SELECT RAISE(ABORT, 'instrument_listing_events is insert-only');
END;
CREATE TRIGGER instrument_listing_events_no_delete
BEFORE DELETE ON instrument_listing_events
BEGIN
    SELECT RAISE(ABORT, 'instrument_listing_events is insert-only');
END;

CREATE TRIGGER instrument_listing_events_state_guard
BEFORE INSERT ON instrument_listing_events
BEGIN
    SELECT CASE WHEN NEW.expected_previous_event_id != COALESCE(
        (
            SELECT event_id
            FROM instrument_listing_events
            WHERE venue = NEW.venue
              AND symbol = NEW.symbol
              AND currency = NEW.currency
            ORDER BY sequence DESC
            LIMIT 1
        ),
        'no_event'
    ) THEN RAISE(ABORT, 'instrument listing expected previous event is stale') END;

    SELECT CASE WHEN NEW.event_type = 'DECLARE'
        AND COALESCE(
            (
                SELECT event_type
                FROM instrument_listing_events
                WHERE venue = NEW.venue
                  AND symbol = NEW.symbol
                  AND currency = NEW.currency
                ORDER BY sequence DESC
                LIMIT 1
            ),
            'REVOKE'
        ) != 'REVOKE'
    THEN RAISE(ABORT, 'instrument listing is already declared') END;

    SELECT CASE WHEN NEW.event_type = 'REVOKE'
        AND (
            COALESCE(
                (
                    SELECT event_type
                    FROM instrument_listing_events
                    WHERE venue = NEW.venue
                      AND symbol = NEW.symbol
                      AND currency = NEW.currency
                    ORDER BY sequence DESC
                    LIMIT 1
                ),
                'NO_EVENT'
            ) != 'DECLARE'
            OR NEW.instrument_id != COALESCE(
                (
                    SELECT instrument_id
                    FROM instrument_listing_events
                    WHERE venue = NEW.venue
                      AND symbol = NEW.symbol
                      AND currency = NEW.currency
                    ORDER BY sequence DESC
                    LIMIT 1
                ),
                ''
            )
        )
    THEN RAISE(ABORT, 'instrument listing revoke does not match active declaration') END;
END;
