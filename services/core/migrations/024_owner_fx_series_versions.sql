CREATE TABLE owner_fx_series_versions (
    sequence INTEGER PRIMARY KEY,
    base_currency TEXT NOT NULL CHECK (length(base_currency) = 3 AND base_currency GLOB '[A-Z][A-Z][A-Z]'),
    quote_currency TEXT NOT NULL CHECK (length(quote_currency) = 3 AND quote_currency GLOB '[A-Z][A-Z][A-Z]'),
    source_version TEXT NOT NULL CHECK (length(source_version) BETWEEN 1 AND 128),
    series_json TEXT NOT NULL CHECK (
        length(CAST(series_json AS BLOB)) BETWEEN 1 AND 1048576
        AND json_valid(series_json)
        AND json_type(series_json) = 'object'
        AND json_type(series_json, '$.schema_version') IS 'text'
        AND json_extract(series_json, '$.schema_version') = 'owner-fx-series.v1'
        AND json_extract(series_json, '$.base_currency') IS base_currency
        AND json_extract(series_json, '$.quote_currency') IS quote_currency
        AND json_extract(series_json, '$.source_version') IS source_version
        AND json_type(series_json, '$.sample') IN ('true', 'false')
        AND json_type(series_json, '$.points') IS 'array'
    ),
    series_sha256 TEXT NOT NULL CHECK (length(series_sha256) = 64 AND series_sha256 NOT GLOB '*[^0-9a-f]*'),
    recorded_at TEXT NOT NULL CHECK (recorded_at GLOB '????-??-??T??:??:??Z' OR recorded_at GLOB '????-??-??T??:??:??.*Z'),
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    CHECK (base_currency <> quote_currency),
    UNIQUE (base_currency, quote_currency, source_version)
) STRICT;

CREATE INDEX owner_fx_series_latest_idx
ON owner_fx_series_versions(base_currency, quote_currency, sequence DESC);

CREATE TRIGGER owner_fx_series_versions_no_update
BEFORE UPDATE ON owner_fx_series_versions
BEGIN SELECT RAISE(ABORT, 'owner FX series versions are insert-only'); END;

CREATE TRIGGER owner_fx_series_versions_no_delete
BEFORE DELETE ON owner_fx_series_versions
BEGIN SELECT RAISE(ABORT, 'owner FX series versions are insert-only'); END;

CREATE TRIGGER owner_fx_series_versions_insert_guard
BEFORE INSERT ON owner_fx_series_versions
BEGIN
    SELECT CASE WHEN NEW.sequence != COALESCE((SELECT MAX(sequence) FROM owner_fx_series_versions), 0) + 1
        THEN RAISE(ABORT, 'owner FX sequence must be contiguous') END;
    SELECT CASE WHEN EXISTS (SELECT 1 FROM owner_fx_series_versions WHERE rtrim(recorded_at, 'Z') > rtrim(NEW.recorded_at, 'Z'))
        THEN RAISE(ABORT, 'owner FX record time cannot move backwards') END;
    SELECT CASE WHEN EXISTS (SELECT 1 FROM fx_observations WHERE base_currency=NEW.base_currency AND quote_currency=NEW.quote_currency)
        THEN RAISE(ABORT, 'FX pair source cannot be mixed') END;
END;

CREATE TRIGGER fx_observations_owner_pair_guard
BEFORE INSERT ON fx_observations
WHEN EXISTS (SELECT 1 FROM owner_fx_series_versions WHERE base_currency=NEW.base_currency AND quote_currency=NEW.quote_currency)
BEGIN SELECT RAISE(ABORT, 'FX pair source cannot be mixed'); END;
