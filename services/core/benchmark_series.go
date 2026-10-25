package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const benchmarkSeriesSchema = "benchmark-index-series.v1"

var errBenchmarkSeriesInput = errors.New("benchmark index series contract is invalid")
var errBenchmarkSeriesNotFound = errors.New("benchmark index series was not found")

type BenchmarkIndexPoint struct {
	ObservedAt string `json:"observed_at"`
	Level      string `json:"level"`
}

type BenchmarkSeriesInput struct {
	SchemaVersion   string                `json:"schema_version"`
	BenchmarkID     string                `json:"benchmark_id"`
	Label           string                `json:"label"`
	Currency        string                `json:"currency"`
	ReturnBasis     string                `json:"return_basis"`
	SourceReference string                `json:"source_reference"`
	SourceVersion   string                `json:"source_version"`
	Sample          bool                  `json:"sample"`
	FetchedAt       string                `json:"fetched_at"`
	Points          []BenchmarkIndexPoint `json:"points"`
}

type BenchmarkSeriesReceipt struct {
	StorageCheck    string `json:"storage_check"`
	Authority       string `json:"authority"`
	BenchmarkID     string `json:"benchmark_id"`
	Label           string `json:"label"`
	Currency        string `json:"currency"`
	ReturnBasis     string `json:"return_basis"`
	SourceReference string `json:"source_reference"`
	SourceVersion   string `json:"source_version"`
	Sample          bool   `json:"sample"`
	DataSHA256      string `json:"data_sha256"`
	PointCount      int    `json:"point_count"`
	FirstObservedAt string `json:"first_observed_at"`
	LastObservedAt  string `json:"last_observed_at"`
	FetchedAt       string `json:"fetched_at"`
	RecordedAt      string `json:"recorded_at"`
}

type benchmarkSeriesRecord struct {
	Sequence   int64
	Input      BenchmarkSeriesInput
	DataSHA256 string
	RecordedAt string
}

type benchmarkSeriesRecoveryProof struct {
	SHA256   string
	Versions int
	Points   int
}

// This small closed-object reader is shared by the envelope and its point rows.
func benchmarkObject(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, errBenchmarkSeriesInput
	}
	fields := map[string]json.RawMessage{}
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || !allowed[key] || fields[key] != nil {
			return nil, errBenchmarkSeriesInput
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errBenchmarkSeriesInput
		}
		fields[key] = value
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || len(fields) != len(keys) || ensureJSONEOF(d) != nil {
		return nil, errBenchmarkSeriesInput
	}
	return fields, nil
}

func decodeBenchmarkSeries(ctx context.Context, raw []byte) (*BenchmarkSeriesInput, []byte, string, error) {
	if len(raw) == 0 || len(raw) > maxBodyBytes || !utf8.Valid(raw) || !validJSONSurrogates(raw) {
		return nil, nil, "", errBenchmarkSeriesInput
	}
	fields, err := benchmarkObject(raw, "schema_version", "benchmark_id", "label", "currency", "return_basis", "source_reference", "source_version", "sample", "fetched_at", "points")
	if err != nil {
		return nil, nil, "", err
	}
	var input BenchmarkSeriesInput
	// Decode scalar fields separately so the point count is bounded during parsing.
	points := fields["points"]
	delete(fields, "points")
	scalars, err := json.Marshal(fields)
	if err != nil || json.Unmarshal(scalars, &input) != nil {
		return nil, nil, "", errBenchmarkSeriesInput
	}
	if input.SchemaVersion != benchmarkSeriesSchema || !safeOrderID(input.BenchmarkID) || !safeOrderID(input.SourceVersion) ||
		input.Label == "" || len(input.Label) > 160 || strings.TrimSpace(input.Label) != input.Label || strings.IndexFunc(input.Label, unicode.IsControl) >= 0 ||
		(input.Currency != "KRW" && input.Currency != "USD") || len(input.FetchedAt) > 30 || !canonicalUTCString(input.FetchedAt) {
		return nil, nil, "", errBenchmarkSeriesInput
	}
	switch input.ReturnBasis {
	case "price_index", "gross_total_return", "net_total_return":
	default:
		return nil, nil, "", errBenchmarkSeriesInput
	}
	ref, err := url.Parse(input.SourceReference)
	if err != nil || len(input.SourceReference) > 512 || ref.Scheme != "https" || ref.Hostname() == "" || ref.User != nil || ref.RawQuery != "" || ref.ForceQuery || ref.Fragment != "" || ref.Opaque != "" || ref.String() != input.SourceReference {
		return nil, nil, "", errBenchmarkSeriesInput
	}
	d := json.NewDecoder(bytes.NewReader(points))
	if token, err := d.Token(); err != nil || token != json.Delim('[') {
		return nil, nil, "", errBenchmarkSeriesInput
	}
	fetched, _ := time.Parse(time.RFC3339Nano, input.FetchedAt)
	var previous time.Time
	for d.More() {
		if err := ctx.Err(); err != nil {
			return nil, nil, "", err
		}
		if len(input.Points) >= 10000 {
			return nil, nil, "", errBenchmarkSeriesInput
		}
		var row json.RawMessage
		if d.Decode(&row) != nil {
			return nil, nil, "", errBenchmarkSeriesInput
		}
		if _, err := benchmarkObject(row, "observed_at", "level"); err != nil {
			return nil, nil, "", err
		}
		var point BenchmarkIndexPoint
		if json.Unmarshal(row, &point) != nil || len(point.ObservedAt) > 30 || !canonicalUTCString(point.ObservedAt) || !canonicalDecimal(point.Level, true) {
			return nil, nil, "", errBenchmarkSeriesInput
		}
		at, _ := time.Parse(time.RFC3339Nano, point.ObservedAt)
		if len(input.Points) > 0 && !at.After(previous) || at.After(fetched) {
			return nil, nil, "", errBenchmarkSeriesInput
		}
		previous = at
		input.Points = append(input.Points, point)
	}
	if token, err := d.Token(); err != nil || token != json.Delim(']') || ensureJSONEOF(d) != nil || len(input.Points) == 0 {
		return nil, nil, "", errBenchmarkSeriesInput
	}
	canonical, sha, err := orderJSONHash(input)
	if err != nil || len(canonical) > maxBodyBytes {
		return nil, nil, "", errBenchmarkSeriesInput
	}
	return &input, canonical, sha, nil
}

func sameBenchmarkDefinition(a, b BenchmarkSeriesInput) bool {
	return a.Currency == b.Currency && a.ReturnBasis == b.ReturnBasis && a.SourceReference == b.SourceReference && a.Sample == b.Sample
}

func benchmarkRecordSHA(record benchmarkSeriesRecord) (string, error) {
	_, sha, err := orderJSONHash(struct {
		Sequence                                           int64
		BenchmarkID, SourceVersion, DataSHA256, RecordedAt string
	}{record.Sequence, record.Input.BenchmarkID, record.Input.SourceVersion, record.DataSHA256, record.RecordedAt})
	return sha, err
}

func benchmarkReceipt(r *benchmarkSeriesRecord) *BenchmarkSeriesReceipt {
	i := r.Input
	return &BenchmarkSeriesReceipt{StorageCheck: "verified", Authority: "owner_declared", BenchmarkID: i.BenchmarkID, Label: i.Label, Currency: i.Currency, ReturnBasis: i.ReturnBasis, SourceReference: i.SourceReference, SourceVersion: i.SourceVersion, Sample: i.Sample, DataSHA256: r.DataSHA256, PointCount: len(i.Points), FirstObservedAt: i.Points[0].ObservedAt, LastObservedAt: i.Points[len(i.Points)-1].ObservedAt, FetchedAt: i.FetchedAt, RecordedAt: r.RecordedAt}
}

func requireBenchmarkSeriesSchema(ctx context.Context, q queryer) error {
	script, err := migrationFiles.ReadFile("migrations/023_benchmark_series_versions.sql")
	if err != nil {
		return err
	}
	objects := map[string]string{"benchmark_series_versions": "TABLE", "benchmark_series_latest_idx": "INDEX", "benchmark_series_versions_no_update": "TRIGGER", "benchmark_series_versions_no_delete": "TRIGGER", "benchmark_series_versions_insert_guard": "TRIGGER"}
	for name, kind := range objects {
		prefix := "CREATE " + kind + " " + name + " "
		expected := ""
		// shortcut: controlled SQL objects use blank-line-separated blocks; use a parser if that format becomes variable.
		for _, block := range strings.Split(string(script), "\n\n") {
			definition := strings.Join(strings.Fields(block), " ")
			if strings.HasPrefix(definition, prefix) && strings.HasSuffix(definition, ";") {
				expected = strings.TrimSuffix(definition, ";")
				break
			}
		}
		if expected == "" {
			return errors.New("benchmark schema definition is incomplete")
		}
		var actual string
		if err := q.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name=? AND type=?`, name, strings.ToLower(kind)).Scan(&actual); err != nil {
			return errors.New("benchmark schema object is missing")
		}
		// Preserve literals as well as constraints; SQL keyword case is pinned too.
		if strings.Join(strings.Fields(actual), " ") != strings.Join(strings.Fields(expected), " ") {
			return errors.New("benchmark schema object changed")
		}
	}
	return nil
}

func replayBenchmarkSeries(ctx context.Context, q queryer, id, version string) (*benchmarkSeriesRecord, benchmarkSeriesRecoveryProof, error) {
	var proof benchmarkSeriesRecoveryProof
	if err := requireBenchmarkSeriesSchema(ctx, q); err != nil {
		return nil, proof, err
	}
	rows, err := q.QueryContext(ctx, `SELECT sequence,benchmark_id,source_version,series_json,series_sha256,recorded_at,record_sha256 FROM benchmark_series_versions ORDER BY sequence`)
	if err != nil {
		return nil, proof, err
	}
	defer rows.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	definitions := map[string]BenchmarkSeriesInput{}
	var selected *benchmarkSeriesRecord
	previousRecorded := time.Time{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, proof, err
		}
		var r benchmarkSeriesRecord
		var benchmarkID, sourceVersion, payload, storedSHA string
		if err := rows.Scan(&r.Sequence, &benchmarkID, &sourceVersion, &payload, &r.DataSHA256, &r.RecordedAt, &storedSHA); err != nil {
			return nil, proof, err
		}
		input, canonical, sha, err := decodeBenchmarkSeries(ctx, []byte(payload))
		if err != nil || r.Sequence != int64(proof.Versions+1) || string(canonical) != payload || sha != r.DataSHA256 || benchmarkID != input.BenchmarkID || sourceVersion != input.SourceVersion || !canonicalUTCString(r.RecordedAt) {
			return nil, proof, errors.New("stored benchmark series is invalid")
		}
		r.Input = *input
		at, _ := time.Parse(time.RFC3339Nano, r.RecordedAt)
		fetched, _ := time.Parse(time.RFC3339Nano, input.FetchedAt)
		if at.Before(fetched) || (proof.Versions > 0 && at.Before(previousRecorded)) {
			return nil, proof, errors.New("benchmark record time is invalid")
		}
		previousRecorded = at
		if prior, ok := definitions[benchmarkID]; ok && !sameBenchmarkDefinition(prior, *input) {
			return nil, proof, errors.New("benchmark identity changed")
		}
		// Retain identity metadata only, not every historical point array.
		definition := *input
		definition.Points = nil
		definitions[benchmarkID] = definition
		want, err := benchmarkRecordSHA(r)
		if err != nil || want != storedSHA {
			return nil, proof, errors.New("benchmark record hash mismatch")
		}
		if err := encoder.Encode([]any{r.Sequence, storedSHA}); err != nil {
			return nil, proof, err
		}
		proof.Versions++
		proof.Points += len(input.Points)
		if benchmarkID == id && (version == "" || sourceVersion == version) {
			copy := r
			selected = &copy
		}
	}
	if err := rows.Err(); err != nil {
		return nil, proof, err
	}
	proof.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return selected, proof, nil
}

func proveBenchmarkSeriesRecovery(ctx context.Context, q queryer) (benchmarkSeriesRecoveryProof, error) {
	_, proof, err := replayBenchmarkSeries(ctx, q, "", "")
	return proof, err
}

func (s *Service) importBenchmarkSeries(ctx context.Context, raw []byte) (*BenchmarkSeriesReceipt, error) {
	input, canonical, sha, err := decodeBenchmarkSeries(ctx, raw)
	if err != nil {
		return nil, err
	}
	if s == nil || s.db == nil || s.now == nil {
		return nil, errors.New("benchmark service is unavailable")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireSchemaContext(ctx, tx); err != nil {
		return nil, err
	}
	prior, proof, err := replayBenchmarkSeries(ctx, tx, input.BenchmarkID, input.SourceVersion)
	if err != nil {
		return nil, err
	}
	if prior != nil {
		if prior.DataSHA256 != sha {
			return nil, errors.New("benchmark version is already bound to different data")
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return benchmarkReceipt(prior), nil
	}
	// The full journal is already proved in this transaction; read only its latest identity fields.
	var currency, basis, reference string
	var sample bool
	err = tx.QueryRowContext(ctx, `SELECT json_extract(series_json,'$.currency'),json_extract(series_json,'$.return_basis'),json_extract(series_json,'$.source_reference'),json_extract(series_json,'$.sample') FROM benchmark_series_versions WHERE benchmark_id=? ORDER BY sequence DESC LIMIT 1`, input.BenchmarkID).Scan(&currency, &basis, &reference, &sample)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil && (currency != input.Currency || basis != input.ReturnBasis || reference != input.SourceReference || sample != input.Sample) {
		return nil, errors.New("benchmark identity cannot be redefined")
	}
	r := benchmarkSeriesRecord{Sequence: int64(proof.Versions + 1), Input: *input, DataSHA256: sha, RecordedAt: s.now().UTC().Format(time.RFC3339Nano)}
	if !canonicalUTCString(r.RecordedAt) {
		return nil, errors.New("benchmark record time is invalid")
	}
	recorded, _ := time.Parse(time.RFC3339Nano, r.RecordedAt)
	fetched, _ := time.Parse(time.RFC3339Nano, input.FetchedAt)
	if recorded.Before(fetched) {
		return nil, errors.New("benchmark capture is in the future")
	}
	recordSHA, err := benchmarkRecordSHA(r)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO benchmark_series_versions(sequence,benchmark_id,source_version,series_json,series_sha256,recorded_at,record_sha256) VALUES(?,?,?,?,?,?,?)`, r.Sequence, input.BenchmarkID, input.SourceVersion, string(canonical), sha, r.RecordedAt, recordSHA); err != nil {
		return nil, errors.New("benchmark series could not be stored")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return benchmarkReceipt(&r), nil
}

func readBenchmarkSeriesStatus(ctx context.Context, db *sql.DB, id, version string) (*BenchmarkSeriesReceipt, error) {
	if !safeOrderID(id) || (version != "" && !safeOrderID(version)) {
		return nil, errors.New("benchmark query is invalid")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireSchemaContext(ctx, tx); err != nil {
		return nil, err
	}
	r, _, err := replayBenchmarkSeries(ctx, tx, id, version)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errBenchmarkSeriesNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return benchmarkReceipt(r), nil
}
