CREATE TABLE benchmark_series_versions (
    sequence INTEGER PRIMARY KEY,
    benchmark_id TEXT NOT NULL CHECK (length(benchmark_id) BETWEEN 1 AND 128),
    source_version TEXT NOT NULL CHECK (length(source_version) BETWEEN 1 AND 128),
    series_json TEXT NOT NULL CHECK (
        length(CAST(series_json AS BLOB)) BETWEEN 1 AND 1048576
        AND json_valid(series_json)
        AND json_type(series_json) = 'object'
        AND json_type(series_json, '$.schema_version') IS 'text'
        AND json_extract(series_json, '$.schema_version') = 'benchmark-index-series.v1'
        AND json_type(series_json, '$.benchmark_id') IS 'text'
        AND json_extract(series_json, '$.benchmark_id') = benchmark_id
        AND json_type(series_json, '$.source_version') IS 'text'
        AND json_extract(series_json, '$.source_version') = source_version
    ),
    series_sha256 TEXT NOT NULL CHECK (length(series_sha256) = 64 AND series_sha256 NOT GLOB '*[^0-9a-f]*'),
    recorded_at TEXT NOT NULL CHECK (
        recorded_at GLOB '????-??-??T??:??:??Z'
        OR recorded_at GLOB '????-??-??T??:??:??.*Z'
    ),
    record_sha256 TEXT NOT NULL CHECK (length(record_sha256) = 64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),
    UNIQUE (benchmark_id, source_version)
) STRICT;

CREATE INDEX benchmark_series_latest_idx
ON benchmark_series_versions(benchmark_id, sequence DESC);

CREATE TRIGGER benchmark_series_versions_no_update
BEFORE UPDATE ON benchmark_series_versions
BEGIN SELECT RAISE(ABORT, 'benchmark series versions are insert-only'); END;

CREATE TRIGGER benchmark_series_versions_no_delete
BEFORE DELETE ON benchmark_series_versions
BEGIN SELECT RAISE(ABORT, 'benchmark series versions are insert-only'); END;

CREATE TRIGGER benchmark_series_versions_insert_guard
BEFORE INSERT ON benchmark_series_versions
BEGIN
    SELECT CASE WHEN NEW.sequence != COALESCE((SELECT MAX(sequence) FROM benchmark_series_versions), 0) + 1
        THEN RAISE(ABORT, 'benchmark sequence must be contiguous') END;
    SELECT CASE WHEN EXISTS (SELECT 1 FROM benchmark_series_versions
        WHERE rtrim(recorded_at, 'Z') > rtrim(NEW.recorded_at, 'Z'))
        THEN RAISE(ABORT, 'benchmark record time cannot move backwards') END;
    SELECT CASE WHEN EXISTS (SELECT 1 FROM benchmark_series_versions prior
        WHERE prior.benchmark_id = NEW.benchmark_id AND (
            json_extract(prior.series_json, '$.currency') != json_extract(NEW.series_json, '$.currency')
            OR json_extract(prior.series_json, '$.return_basis') != json_extract(NEW.series_json, '$.return_basis')
            OR json_extract(prior.series_json, '$.source_reference') != json_extract(NEW.series_json, '$.source_reference')
            OR json_extract(prior.series_json, '$.sample') != json_extract(NEW.series_json, '$.sample')
        )) THEN RAISE(ABORT, 'benchmark identity cannot be redefined') END;
END;
