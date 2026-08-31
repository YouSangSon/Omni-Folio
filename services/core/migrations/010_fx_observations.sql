CREATE TABLE fx_observations (
    sequence INTEGER PRIMARY KEY,
    observation_id TEXT NOT NULL UNIQUE,
    source TEXT NOT NULL CHECK (source = 'local_fixture'),
    source_observation_id TEXT NOT NULL,
    base_currency TEXT NOT NULL CHECK (length(base_currency) = 3 AND base_currency GLOB '[A-Z][A-Z][A-Z]'),
    quote_currency TEXT NOT NULL CHECK (length(quote_currency) = 3 AND quote_currency GLOB '[A-Z][A-Z][A-Z]'),
    rate TEXT NOT NULL CHECK (
        length(rate) BETWEEN 1 AND 64
        AND rate NOT GLOB '*[^0-9.]*'
        AND length(rate) - length(replace(rate, '.', '')) <= 1
        AND (
            (instr(rate, '.') = 0 AND substr(rate, 1, 1) GLOB '[1-9]')
            OR
            (instr(rate, '.') > 1
                AND (substr(rate, 1, instr(rate, '.') - 1) = '0' OR substr(rate, 1, 1) GLOB '[1-9]')
                AND substr(rate, -1) GLOB '[1-9]')
        )
    ),
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
    UNIQUE (source, base_currency, quote_currency, observed_at),
    CHECK (length(observation_id) BETWEEN 1 AND 128),
    CHECK (length(source_observation_id) BETWEEN 1 AND 128),
    CHECK (base_currency <> quote_currency)
) STRICT;

CREATE INDEX fx_observations_latest_idx
ON fx_observations(source, base_currency, quote_currency, observed_at DESC, sequence DESC);

CREATE TRIGGER fx_observations_no_update
BEFORE UPDATE ON fx_observations
BEGIN
    SELECT RAISE(ABORT, 'fx_observations is insert-only');
END;

CREATE TRIGGER fx_observations_no_delete
BEFORE DELETE ON fx_observations
BEGIN
    SELECT RAISE(ABORT, 'fx_observations is insert-only');
END;
