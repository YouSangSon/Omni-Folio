CREATE TABLE security_price_observations (
    sequence INTEGER PRIMARY KEY,
    observation_id TEXT NOT NULL UNIQUE,
    source TEXT NOT NULL CHECK (source = 'local_fixture'),
    source_observation_id TEXT NOT NULL,
    instrument_id TEXT NOT NULL CHECK (length(instrument_id) BETWEEN 1 AND 128),
    symbol TEXT NOT NULL CHECK (length(symbol) BETWEEN 1 AND 128 AND symbol = upper(symbol)),
    venue TEXT NOT NULL CHECK (length(venue) BETWEEN 1 AND 128 AND venue = upper(venue)),
    currency TEXT NOT NULL CHECK (length(currency) = 3 AND currency GLOB '[A-Z][A-Z][A-Z]'),
    price TEXT NOT NULL CHECK (
        length(price) BETWEEN 1 AND 64
        AND price NOT GLOB '*[^0-9.]*'
        AND length(price) - length(replace(price, '.', '')) <= 1
        AND (
            (instr(price, '.') = 0 AND substr(price, 1, 1) GLOB '[1-9]')
            OR
            (instr(price, '.') > 1
                AND (substr(price, 1, instr(price, '.') - 1) = '0' OR substr(price, 1, 1) GLOB '[1-9]')
                AND substr(price, -1) GLOB '[1-9]')
        )
    ),
    price_adjustment TEXT NOT NULL CHECK (price_adjustment = 'unspecified'),
    observed_at TEXT NOT NULL CHECK (
        observed_at GLOB '????-??-??T??:??:??Z'
        OR observed_at GLOB '????-??-??T??:??:??.*Z'
    ),
    fetched_at TEXT NOT NULL CHECK (
        fetched_at GLOB '????-??-??T??:??:??Z'
        OR fetched_at GLOB '????-??-??T??:??:??.*Z'
    ),
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    recorded_at TEXT NOT NULL CHECK (
        recorded_at GLOB '????-??-??T??:??:??Z'
        OR recorded_at GLOB '????-??-??T??:??:??.*Z'
    ),
    UNIQUE (source, source_observation_id),
    UNIQUE (source, instrument_id, symbol, venue, currency, observed_at, price_adjustment),
    CHECK (length(observation_id) BETWEEN 1 AND 128),
    CHECK (length(source_observation_id) BETWEEN 1 AND 128)
) STRICT;

CREATE INDEX security_price_observations_latest_idx
ON security_price_observations(source, instrument_id, symbol, venue, currency, price_adjustment, observed_at DESC, sequence DESC);

CREATE TRIGGER security_price_observations_no_update
BEFORE UPDATE ON security_price_observations
BEGIN
    SELECT RAISE(ABORT, 'security_price_observations is insert-only');
END;

CREATE TRIGGER security_price_observations_no_delete
BEFORE DELETE ON security_price_observations
BEGIN
    SELECT RAISE(ABORT, 'security_price_observations is insert-only');
END;
