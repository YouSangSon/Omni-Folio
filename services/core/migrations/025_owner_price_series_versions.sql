CREATE TABLE owner_price_series_versions (
    sequence INTEGER PRIMARY KEY,
    instrument_id TEXT NOT NULL CHECK (length(instrument_id) BETWEEN 1 AND 128),
    currency TEXT NOT NULL CHECK (length(currency) = 3 AND currency GLOB '[A-Z][A-Z][A-Z]'),
    source_version TEXT NOT NULL CHECK (length(source_version) BETWEEN 1 AND 128),
    series_json TEXT NOT NULL CHECK (
        length(CAST(series_json AS BLOB)) BETWEEN 1 AND 1048576
        AND json_valid(series_json)
        AND json_type(series_json) = 'object'
        AND json_type(series_json, '$.schema_version') IS 'text'
        AND json_extract(series_json, '$.schema_version') = 'owner-price-series.v1'
        AND json_extract(series_json, '$.instrument_id') IS instrument_id
        AND json_extract(series_json, '$.currency') IS currency
        AND json_extract(series_json, '$.source_version') IS source_version
        AND json_extract(series_json, '$.price_adjustment') IS 'unadjusted'
        AND json_type(series_json, '$.sample') IN ('true', 'false')
        AND json_type(series_json, '$.points') IS 'array'
    ),
    series_sha256 TEXT NOT NULL CHECK (length(series_sha256) = 64 AND series_sha256 NOT GLOB '*[^0-9a-f]*'),
    recorded_at TEXT NOT NULL CHECK (recorded_at GLOB '????-??-??T??:??:??Z' OR recorded_at GLOB '????-??-??T??:??:??.*Z'),
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    UNIQUE (instrument_id, currency, source_version)
) STRICT;

CREATE INDEX owner_price_series_latest_idx
ON owner_price_series_versions(instrument_id, currency, sequence DESC);

CREATE TRIGGER owner_price_series_versions_no_update
BEFORE UPDATE ON owner_price_series_versions
BEGIN SELECT RAISE(ABORT, 'owner price series versions are insert-only'); END;

CREATE TRIGGER owner_price_series_versions_no_delete
BEFORE DELETE ON owner_price_series_versions
BEGIN SELECT RAISE(ABORT, 'owner price series versions are insert-only'); END;

CREATE TRIGGER owner_price_series_versions_insert_guard
BEFORE INSERT ON owner_price_series_versions
BEGIN
    SELECT CASE WHEN NEW.sequence != COALESCE((SELECT MAX(sequence) FROM owner_price_series_versions), 0) + 1
        THEN RAISE(ABORT, 'owner price sequence must be contiguous') END;
    SELECT CASE WHEN EXISTS (SELECT 1 FROM owner_price_series_versions WHERE rtrim(recorded_at, 'Z') > rtrim(NEW.recorded_at, 'Z'))
        THEN RAISE(ABORT, 'owner price record time cannot move backwards') END;
    SELECT CASE WHEN EXISTS (SELECT 1 FROM security_price_observations WHERE instrument_id=NEW.instrument_id AND currency=NEW.currency)
        THEN RAISE(ABORT, 'price sources cannot be mixed') END;
END;

CREATE TRIGGER security_price_observations_owner_guard
BEFORE INSERT ON security_price_observations
WHEN EXISTS (SELECT 1 FROM owner_price_series_versions WHERE instrument_id=NEW.instrument_id AND currency=NEW.currency)
BEGIN SELECT RAISE(ABORT, 'price sources cannot be mixed'); END;
