package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const ownerFXSeriesSchema = "owner-fx-series.v1"
const ownerFXSource = "owner_declared"

var errOwnerFXInput = errors.New("owner FX series contract is invalid")

type OwnerFXPoint struct {
	ObservedAt string `json:"observed_at"`
	Rate       string `json:"rate"`
}

type OwnerFXSeriesInput struct {
	SchemaVersion string         `json:"schema_version"`
	SourceVersion string         `json:"source_version"`
	BaseCurrency  string         `json:"base_currency"`
	QuoteCurrency string         `json:"quote_currency"`
	Sample        bool           `json:"sample"`
	FetchedAt     string         `json:"fetched_at"`
	Points        []OwnerFXPoint `json:"points"`
}

type OwnerFXSeriesReceipt struct {
	StorageCheck    string  `json:"storage_check"`
	Authority       string  `json:"authority"`
	SourceVersion   string  `json:"source_version"`
	BaseCurrency    string  `json:"base_currency"`
	QuoteCurrency   string  `json:"quote_currency"`
	Sample          bool    `json:"sample"`
	DataSHA256      string  `json:"data_sha256"`
	PointCount      int     `json:"point_count"`
	FirstObservedAt *string `json:"first_observed_at"`
	LastObservedAt  *string `json:"last_observed_at"`
	FetchedAt       string  `json:"fetched_at"`
	RecordedAt      string  `json:"recorded_at"`
}

type ownerFXSeriesRecord struct {
	Sequence   int64
	Input      OwnerFXSeriesInput
	DataSHA256 string
	RecordedAt string
}

type ownerFXSeriesRecoveryProof struct {
	SHA256   string
	Versions int
	Points   int
}

type valuationFXInputs struct {
	tx           *sql.Tx
	knownAt      time.Time
	observations []FXObservation
	times        map[string][3]time.Time
}

func readValuationFXInputs(ctx context.Context, tx *sql.Tx, knownAt time.Time) (valuationFXInputs, error) {
	observations, err := valuationFXObservations(ctx, tx, knownAt)
	if err != nil {
		return valuationFXInputs{}, err
	}
	times, err := currentFXObservationTimes(observations)
	if err != nil {
		return valuationFXInputs{}, err
	}
	return valuationFXInputs{tx, knownAt, observations, times}, nil
}

func decodeOwnerFXSeries(ctx context.Context, raw []byte) (*OwnerFXSeriesInput, []byte, string, error) {
	if ctx == nil || len(raw) == 0 || len(raw) > maxBodyBytes || !utf8.Valid(raw) || !validJSONSurrogates(raw) {
		return nil, nil, "", errOwnerFXInput
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, "", err
	}
	fields, err := benchmarkObject(raw, "schema_version", "source_version", "base_currency", "quote_currency", "sample", "fetched_at", "points")
	if err != nil {
		return nil, nil, "", errOwnerFXInput
	}
	points := fields["points"]
	delete(fields, "points")
	scalars, err := json.Marshal(fields)
	var input OwnerFXSeriesInput
	if err != nil || json.Unmarshal(scalars, &input) != nil || input.SchemaVersion != ownerFXSeriesSchema || !safeOrderID(input.SourceVersion) ||
		!currencyCodePattern.MatchString(input.BaseCurrency) || !currencyCodePattern.MatchString(input.QuoteCurrency) || input.BaseCurrency == input.QuoteCurrency || len(input.FetchedAt) > 30 || !canonicalUTCString(input.FetchedAt) {
		return nil, nil, "", errOwnerFXInput
	}
	d := json.NewDecoder(bytes.NewReader(points))
	if token, err := d.Token(); err != nil || token != json.Delim('[') {
		return nil, nil, "", errOwnerFXInput
	}
	input.Points = []OwnerFXPoint{}
	fetched, _ := time.Parse(time.RFC3339Nano, input.FetchedAt)
	var previous time.Time
	for d.More() {
		if err := ctx.Err(); err != nil {
			return nil, nil, "", err
		}
		if len(input.Points) >= 10000 {
			return nil, nil, "", errOwnerFXInput
		}
		var rawPoint json.RawMessage
		if d.Decode(&rawPoint) != nil {
			return nil, nil, "", errOwnerFXInput
		}
		if _, err := benchmarkObject(rawPoint, "observed_at", "rate"); err != nil {
			return nil, nil, "", errOwnerFXInput
		}
		var point OwnerFXPoint
		if json.Unmarshal(rawPoint, &point) != nil || len(point.ObservedAt) > 30 || !canonicalUTCString(point.ObservedAt) || !canonicalDecimal(point.Rate, true) || point.Rate == "0" {
			return nil, nil, "", errOwnerFXInput
		}
		at, _ := time.Parse(time.RFC3339Nano, point.ObservedAt)
		if at.After(fetched) || (len(input.Points) > 0 && !at.After(previous)) {
			return nil, nil, "", errOwnerFXInput
		}
		input.Points = append(input.Points, point)
		previous = at
	}
	if token, err := d.Token(); err != nil || token != json.Delim(']') || ensureJSONEOF(d) != nil {
		return nil, nil, "", errOwnerFXInput
	}
	canonical, sha, err := orderJSONHash(input)
	if err != nil || len(canonical) > maxBodyBytes {
		return nil, nil, "", errOwnerFXInput
	}
	return &input, canonical, sha, nil
}

func ownerFXReceipt(r *ownerFXSeriesRecord) *OwnerFXSeriesReceipt {
	i := r.Input
	receipt := &OwnerFXSeriesReceipt{StorageCheck: "verified", Authority: ownerFXSource, SourceVersion: i.SourceVersion, BaseCurrency: i.BaseCurrency, QuoteCurrency: i.QuoteCurrency, Sample: i.Sample, DataSHA256: r.DataSHA256, PointCount: len(i.Points), FetchedAt: i.FetchedAt, RecordedAt: r.RecordedAt}
	if len(i.Points) > 0 {
		receipt.FirstObservedAt = stringPointer(i.Points[0].ObservedAt)
		receipt.LastObservedAt = stringPointer(i.Points[len(i.Points)-1].ObservedAt)
	}
	return receipt
}

func ownerFXRecordSHA(r ownerFXSeriesRecord) (string, error) {
	_, sha, err := orderJSONHash([]any{r.Sequence, r.Input.BaseCurrency, r.Input.QuoteCurrency, r.Input.SourceVersion, r.DataSHA256, r.RecordedAt})
	return sha, err
}

func requireOwnerFXSeriesSchema(ctx context.Context, q queryer) error {
	ownerSQL, err := migrationFiles.ReadFile("migrations/024_owner_fx_series_versions.sql")
	if err != nil {
		return err
	}
	fixtureSQL, err := migrationFiles.ReadFile("migrations/010_fx_observations.sql")
	if err != nil {
		return err
	}
	for _, object := range []struct{ name, kind, table, script string }{
		{"owner_fx_series_versions", "TABLE", "owner_fx_series_versions", string(ownerSQL)},
		{"owner_fx_series_latest_idx", "INDEX", "owner_fx_series_versions", string(ownerSQL)},
		{"owner_fx_series_versions_no_update", "TRIGGER", "owner_fx_series_versions", string(ownerSQL)},
		{"owner_fx_series_versions_no_delete", "TRIGGER", "owner_fx_series_versions", string(ownerSQL)},
		{"owner_fx_series_versions_insert_guard", "TRIGGER", "owner_fx_series_versions", string(ownerSQL)},
		{"fx_observations_owner_pair_guard", "TRIGGER", "fx_observations", string(ownerSQL)},
		{"fx_observations", "TABLE", "fx_observations", string(fixtureSQL)},
		{"fx_observations_latest_idx", "INDEX", "fx_observations", string(fixtureSQL)},
		{"fx_observations_no_update", "TRIGGER", "fx_observations", string(fixtureSQL)},
		{"fx_observations_no_delete", "TRIGGER", "fx_observations", string(fixtureSQL)},
	} {
		expected := ""
		// shortcut: embedded migration objects use blank-line-separated blocks; use a SQL parser if this controlled format changes.
		for _, block := range strings.Split(object.script, "\n\n") {
			definition := strings.Join(strings.Fields(block), " ")
			if strings.HasPrefix(definition, "CREATE "+object.kind+" "+object.name+" ") && strings.HasSuffix(definition, ";") {
				expected = strings.TrimSuffix(definition, ";")
				break
			}
		}
		var actual string
		if expected == "" || q.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name=? AND type=? AND tbl_name=?`, object.name, strings.ToLower(object.kind), object.table).Scan(&actual) != nil || strings.Join(strings.Fields(actual), " ") != expected {
			return errors.New("owner FX physical schema changed")
		}
	}
	for table, expected := range map[string]int{"owner_fx_series_versions": 5, "fx_observations": 7} {
		var count int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
            SELECT name FROM sqlite_master WHERE tbl_name=? AND type IN ('index','trigger')
            UNION ALL SELECT name FROM sqlite_temp_master WHERE tbl_name=? AND type IN ('index','trigger')
        )`, table, table).Scan(&count); err != nil || count != expected {
			return errors.New("owner FX schema inventory changed")
		}
	}
	var mixed bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM owner_fx_series_versions owner JOIN fx_observations fixture ON owner.base_currency=fixture.base_currency AND owner.quote_currency=fixture.quote_currency)`).Scan(&mixed); err != nil || mixed {
		return errors.New("FX pair sources are mixed")
	}
	return nil
}

func replayOwnerFXSeries(ctx context.Context, q queryer, base, quote, version string) (*ownerFXSeriesRecord, ownerFXSeriesRecoveryProof, error) {
	var proof ownerFXSeriesRecoveryProof
	if err := requireOwnerFXSeriesSchema(ctx, q); err != nil {
		return nil, proof, err
	}
	rows, err := q.QueryContext(ctx, `SELECT sequence,base_currency,quote_currency,source_version,series_json,series_sha256,recorded_at,record_sha256 FROM owner_fx_series_versions ORDER BY sequence`)
	if err != nil {
		return nil, proof, err
	}
	defer rows.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	var selected *ownerFXSeriesRecord
	var previous time.Time
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, proof, err
		}
		var r ownerFXSeriesRecord
		var storedBase, storedQuote, storedVersion, payload, storedSHA string
		if err := rows.Scan(&r.Sequence, &storedBase, &storedQuote, &storedVersion, &payload, &r.DataSHA256, &r.RecordedAt, &storedSHA); err != nil {
			return nil, proof, errors.New("stored owner FX metadata is invalid")
		}
		input, canonical, sha, err := decodeOwnerFXSeries(ctx, []byte(payload))
		if err != nil || r.Sequence != int64(proof.Versions+1) || string(canonical) != payload || sha != r.DataSHA256 || storedBase != input.BaseCurrency || storedQuote != input.QuoteCurrency || storedVersion != input.SourceVersion || !canonicalUTCString(r.RecordedAt) {
			return nil, proof, errors.New("stored owner FX series is invalid")
		}
		r.Input = *input
		at, _ := time.Parse(time.RFC3339Nano, r.RecordedAt)
		fetched, _ := time.Parse(time.RFC3339Nano, input.FetchedAt)
		if at.Before(fetched) || (proof.Versions > 0 && at.Before(previous)) {
			return nil, proof, errors.New("owner FX record time is invalid")
		}
		previous = at
		want, err := ownerFXRecordSHA(r)
		if err != nil || want != storedSHA {
			return nil, proof, errors.New("owner FX record hash mismatch")
		}
		if err := encoder.Encode([]any{r.Sequence, storedSHA}); err != nil {
			return nil, proof, err
		}
		proof.Versions++
		proof.Points += len(input.Points)
		if base == input.BaseCurrency && quote == input.QuoteCurrency && version == input.SourceVersion {
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

func proveOwnerFXSeriesRecovery(ctx context.Context, q queryer) (ownerFXSeriesRecoveryProof, error) {
	_, proof, err := replayOwnerFXSeries(ctx, q, "", "", "")
	return proof, err
}

func (s *Service) importOwnerFXSeries(ctx context.Context, raw []byte) (*OwnerFXSeriesReceipt, error) {
	input, canonical, sha, err := decodeOwnerFXSeries(ctx, raw)
	if err != nil {
		return nil, err
	}
	if s == nil || s.db == nil || s.now == nil {
		return nil, errors.New("owner FX service is unavailable")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireSchemaContext(ctx, tx); err != nil {
		return nil, err
	}
	if _, _, err := replayFXObservations(ctx, tx); err != nil {
		return nil, err
	}
	prior, proof, err := replayOwnerFXSeries(ctx, tx, input.BaseCurrency, input.QuoteCurrency, input.SourceVersion)
	if err != nil {
		return nil, err
	}
	if prior != nil {
		if prior.DataSHA256 != sha {
			return nil, errors.New("owner FX version is already bound to different data")
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return ownerFXReceipt(prior), nil
	}
	r := ownerFXSeriesRecord{Sequence: int64(proof.Versions + 1), Input: *input, DataSHA256: sha, RecordedAt: s.now().UTC().Format(time.RFC3339Nano)}
	recorded, err := time.Parse(time.RFC3339Nano, r.RecordedAt)
	fetched, _ := time.Parse(time.RFC3339Nano, input.FetchedAt)
	if err != nil || !canonicalUTCString(r.RecordedAt) || recorded.Before(fetched) {
		return nil, errors.New("owner FX capture is in the future")
	}
	recordSHA, err := ownerFXRecordSHA(r)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO owner_fx_series_versions(sequence,base_currency,quote_currency,source_version,series_json,series_sha256,recorded_at,record_sha256) VALUES(?,?,?,?,?,?,?,?)`, r.Sequence, input.BaseCurrency, input.QuoteCurrency, input.SourceVersion, string(canonical), sha, r.RecordedAt, recordSHA)
	if err != nil {
		return nil, errors.New("owner FX series could not be stored")
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return nil, errors.New("owner FX series was not stored exactly once")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ownerFXReceipt(&r), nil
}

func valuationFXObservations(ctx context.Context, q queryer, knownAt time.Time) ([]FXObservation, error) {
	observations, _, err := replayFXObservations(ctx, q)
	if err != nil {
		return nil, err
	}
	if _, err := proveOwnerFXSeriesRecovery(ctx, q); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT o.sequence,o.series_json,o.recorded_at FROM owner_fx_series_versions o
        JOIN (SELECT base_currency,quote_currency,MAX(sequence) AS sequence FROM owner_fx_series_versions
            WHERE rtrim(recorded_at,'Z')<=rtrim(?,'Z') GROUP BY base_currency,quote_currency) latest ON latest.sequence=o.sequence
        ORDER BY o.sequence`, knownAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int64
		var payload, recorded string
		if err := rows.Scan(&sequence, &payload, &recorded); err != nil {
			return nil, err
		}
		input, _, _, err := decodeOwnerFXSeries(ctx, []byte(payload))
		if err != nil {
			return nil, err
		}
		for i, point := range input.Points {
			id := fmt.Sprintf("owner_fx_%d_%d", sequence, i)
			observations = append(observations, FXObservation{ObservationID: id, Source: ownerFXSource, SourceObservationID: id, BaseCurrency: input.BaseCurrency, QuoteCurrency: input.QuoteCurrency, Rate: point.Rate, ObservedAt: point.ObservedAt, FetchedAt: input.FetchedAt, RecordedAt: recorded, ownerVersion: input.SourceVersion, ownerSample: input.Sample})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return observations, nil
}

func fxTimeKey(fx FXObservation) string {
	if fx.Source == ownerFXSource {
		// Verified fixture identifiers cannot contain NUL; public IDs remain unchanged.
		return "\x00" + fx.ObservationID
	}
	return fx.ObservationID
}

func fxIsSample(fx *FXObservation) bool {
	return fx != nil && (fx.Source == "local_fixture" || fx.ownerSample)
}

func supportedValuationFX(fx FXObservation) bool {
	return fx.Source == "local_fixture" || fx.Source == ownerFXSource
}

func ownerFXIssue() APIError {
	return APIError{Code: "owner_declared_fx", Message: "selected FX inputs were declared by the owner, not verified by a market provider"}
}

func usesOwnerFX(issues []APIError) bool {
	for _, issue := range issues {
		if issue.Code == "owner_declared_fx" {
			return true
		}
	}
	return false
}
