package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const ownerPriceSeriesSchema = "owner-price-series.v1"
const ownerPriceSource = "owner_declared"
const ownerPriceAdjustment = "unadjusted"

var errOwnerPriceInput = errors.New("owner price series contract is invalid")

type OwnerPricePoint struct {
	ObservedAt string `json:"observed_at"`
	Price      string `json:"price"`
}

type OwnerPriceSeriesInput struct {
	SchemaVersion   string            `json:"schema_version"`
	SourceVersion   string            `json:"source_version"`
	InstrumentID    string            `json:"instrument_id"`
	Symbol          string            `json:"symbol"`
	Venue           string            `json:"venue"`
	PriceAdjustment string            `json:"price_adjustment"`
	Currency        string            `json:"currency"`
	Sample          bool              `json:"sample"`
	FetchedAt       string            `json:"fetched_at"`
	Points          []OwnerPricePoint `json:"points"`
}

type OwnerPriceSeriesReceipt struct {
	StorageCheck    string  `json:"storage_check"`
	Authority       string  `json:"authority"`
	SourceVersion   string  `json:"source_version"`
	InstrumentID    string  `json:"instrument_id"`
	Symbol          string  `json:"symbol"`
	Venue           string  `json:"venue"`
	PriceAdjustment string  `json:"price_adjustment"`
	Currency        string  `json:"currency"`
	Sample          bool    `json:"sample"`
	DataSHA256      string  `json:"data_sha256"`
	PointCount      int     `json:"point_count"`
	FirstObservedAt *string `json:"first_observed_at"`
	LastObservedAt  *string `json:"last_observed_at"`
	FetchedAt       string  `json:"fetched_at"`
	RecordedAt      string  `json:"recorded_at"`
}

type ownerPriceSeriesRecord struct {
	Sequence   int64
	Input      OwnerPriceSeriesInput
	DataSHA256 string
	RecordedAt string
}

type ownerPriceSeriesRecoveryProof struct {
	SHA256   string
	Versions int
	Points   int
}

func decodeOwnerPriceSeries(ctx context.Context, raw []byte) (*OwnerPriceSeriesInput, []byte, string, error) {
	if ctx == nil || len(raw) == 0 || len(raw) > maxBodyBytes || !utf8.Valid(raw) || !validJSONSurrogates(raw) {
		return nil, nil, "", errOwnerPriceInput
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, "", err
	}
	fields, err := benchmarkObject(raw, "schema_version", "source_version", "instrument_id", "symbol", "venue", "currency", "price_adjustment", "sample", "fetched_at", "points")
	if err != nil {
		return nil, nil, "", errOwnerPriceInput
	}
	points := fields["points"]
	delete(fields, "points")
	scalars, err := json.Marshal(fields)
	var input OwnerPriceSeriesInput
	if err != nil || json.Unmarshal(scalars, &input) != nil || input.SchemaVersion != ownerPriceSeriesSchema || !safeOrderID(input.SourceVersion) ||
		!safeOrderID(input.InstrumentID) || !safeOrderID(input.Symbol) || !safeOrderID(input.Venue) || input.Symbol != strings.ToUpper(input.Symbol) || input.Venue != strings.ToUpper(input.Venue) || instrumentIDForSymbol(input.Symbol) != input.InstrumentID || !currencyCodePattern.MatchString(input.Currency) || input.PriceAdjustment != ownerPriceAdjustment || len(input.FetchedAt) > 30 || !canonicalUTCString(input.FetchedAt) {
		return nil, nil, "", errOwnerPriceInput
	}
	d := json.NewDecoder(bytes.NewReader(points))
	if token, err := d.Token(); err != nil || token != json.Delim('[') {
		return nil, nil, "", errOwnerPriceInput
	}
	input.Points = []OwnerPricePoint{}
	fetched, _ := time.Parse(time.RFC3339Nano, input.FetchedAt)
	var previous time.Time
	for d.More() {
		if err := ctx.Err(); err != nil {
			return nil, nil, "", err
		}
		if len(input.Points) >= 10000 {
			return nil, nil, "", errOwnerPriceInput
		}
		var rawPoint json.RawMessage
		if d.Decode(&rawPoint) != nil {
			return nil, nil, "", errOwnerPriceInput
		}
		if _, err := benchmarkObject(rawPoint, "observed_at", "price"); err != nil {
			return nil, nil, "", errOwnerPriceInput
		}
		var point OwnerPricePoint
		if json.Unmarshal(rawPoint, &point) != nil || len(point.ObservedAt) > 30 || !canonicalUTCString(point.ObservedAt) || !canonicalDecimal(point.Price, true) || point.Price == "0" {
			return nil, nil, "", errOwnerPriceInput
		}
		at, _ := time.Parse(time.RFC3339Nano, point.ObservedAt)
		if at.After(fetched) || (len(input.Points) > 0 && !at.After(previous)) {
			return nil, nil, "", errOwnerPriceInput
		}
		input.Points = append(input.Points, point)
		previous = at
	}
	if token, err := d.Token(); err != nil || token != json.Delim(']') || ensureJSONEOF(d) != nil {
		return nil, nil, "", errOwnerPriceInput
	}
	canonical, sha, err := orderJSONHash(input)
	if err != nil || len(canonical) > maxBodyBytes {
		return nil, nil, "", errOwnerPriceInput
	}
	return &input, canonical, sha, nil
}

func ownerPriceReceipt(r *ownerPriceSeriesRecord) *OwnerPriceSeriesReceipt {
	i := r.Input
	receipt := &OwnerPriceSeriesReceipt{StorageCheck: "verified", Authority: ownerPriceSource, SourceVersion: i.SourceVersion, InstrumentID: i.InstrumentID, Symbol: i.Symbol, Venue: i.Venue, PriceAdjustment: i.PriceAdjustment, Currency: i.Currency, Sample: i.Sample, DataSHA256: r.DataSHA256, PointCount: len(i.Points), FetchedAt: i.FetchedAt, RecordedAt: r.RecordedAt}
	if len(i.Points) > 0 {
		receipt.FirstObservedAt = stringPointer(i.Points[0].ObservedAt)
		receipt.LastObservedAt = stringPointer(i.Points[len(i.Points)-1].ObservedAt)
	}
	return receipt
}

func ownerPriceRecordSHA(r ownerPriceSeriesRecord) (string, error) {
	_, sha, err := orderJSONHash([]any{r.Sequence, r.Input.InstrumentID, r.Input.Currency, r.Input.SourceVersion, r.DataSHA256, r.RecordedAt})
	return sha, err
}

func requireOwnerPriceSeriesSchema(ctx context.Context, q queryer) error {
	ownerSQL, err := migrationFiles.ReadFile("migrations/025_owner_price_series_versions.sql")
	if err != nil {
		return err
	}
	fixtureSQL, err := migrationFiles.ReadFile("migrations/012_kiwoom_security_price_observations.sql")
	if err != nil {
		return err
	}
	for _, object := range []struct{ name, kind, table, script string }{
		{"owner_price_series_versions", "TABLE", "owner_price_series_versions", string(ownerSQL)},
		{"owner_price_series_latest_idx", "INDEX", "owner_price_series_versions", string(ownerSQL)},
		{"owner_price_series_versions_no_update", "TRIGGER", "owner_price_series_versions", string(ownerSQL)},
		{"owner_price_series_versions_no_delete", "TRIGGER", "owner_price_series_versions", string(ownerSQL)},
		{"owner_price_series_versions_insert_guard", "TRIGGER", "owner_price_series_versions", string(ownerSQL)},
		{"security_price_observations_owner_guard", "TRIGGER", "security_price_observations", string(ownerSQL)},
		{"security_price_observations", "TABLE", "security_price_observations", string(fixtureSQL)},
		{"security_price_observations_latest_idx", "INDEX", "security_price_observations", string(fixtureSQL)},
		{"security_price_observations_no_update", "TRIGGER", "security_price_observations", string(fixtureSQL)},
		{"security_price_observations_no_delete", "TRIGGER", "security_price_observations", string(fixtureSQL)},
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
			return errors.New("owner price physical schema changed")
		}
	}
	for table, expected := range map[string]int{"owner_price_series_versions": 5, "security_price_observations": 7} {
		var count int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
            SELECT name FROM sqlite_master WHERE tbl_name=? AND type IN ('index','trigger')
            UNION ALL SELECT name FROM sqlite_temp_master WHERE tbl_name=? AND type IN ('index','trigger')
        )`, table, table).Scan(&count); err != nil || count != expected {
			return errors.New("owner price schema inventory changed")
		}
	}
	var mixed bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM owner_price_series_versions owner JOIN security_price_observations fixture ON owner.instrument_id=fixture.instrument_id AND owner.currency=fixture.currency)`).Scan(&mixed); err != nil || mixed {
		return errors.New("price sources are mixed")
	}
	return nil
}

func replayOwnerPriceSeries(ctx context.Context, q queryer, instrument, currency, version string) (*ownerPriceSeriesRecord, ownerPriceSeriesRecoveryProof, error) {
	var proof ownerPriceSeriesRecoveryProof
	if err := requireOwnerPriceSeriesSchema(ctx, q); err != nil {
		return nil, proof, err
	}
	var present bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM owner_price_series_versions)`).Scan(&present); err != nil {
		return nil, proof, err
	}
	var admitted map[string]struct{}
	if present {
		var err error
		admitted, err = admittedOwnerPriceIdentities(ctx, q)
		if err != nil {
			return nil, proof, err
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT sequence,instrument_id,currency,source_version,series_json,series_sha256,recorded_at,record_sha256 FROM owner_price_series_versions ORDER BY sequence`)
	if err != nil {
		return nil, proof, err
	}
	defer rows.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	var selected *ownerPriceSeriesRecord
	var previous time.Time
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, proof, err
		}
		var r ownerPriceSeriesRecord
		var storedBase, storedQuote, storedVersion, payload, storedSHA string
		if err := rows.Scan(&r.Sequence, &storedBase, &storedQuote, &storedVersion, &payload, &r.DataSHA256, &r.RecordedAt, &storedSHA); err != nil {
			return nil, proof, errors.New("stored owner price metadata is invalid")
		}
		input, canonical, sha, err := decodeOwnerPriceSeries(ctx, []byte(payload))
		if err != nil || r.Sequence != int64(proof.Versions+1) || string(canonical) != payload || sha != r.DataSHA256 || storedBase != input.InstrumentID || storedQuote != input.Currency || storedVersion != input.SourceVersion || !canonicalUTCString(r.RecordedAt) {
			return nil, proof, errors.New("stored owner price series is invalid")
		}
		if _, ok := admitted[ownerPriceKey(input.InstrumentID, input.Currency)]; !ok {
			return nil, proof, errors.New("owner price identity has no admitted ledger instrument")
		}
		r.Input = *input
		at, _ := time.Parse(time.RFC3339Nano, r.RecordedAt)
		fetched, _ := time.Parse(time.RFC3339Nano, input.FetchedAt)
		if at.Before(fetched) || (proof.Versions > 0 && at.Before(previous)) {
			return nil, proof, errors.New("owner price record time is invalid")
		}
		previous = at
		want, err := ownerPriceRecordSHA(r)
		if err != nil || want != storedSHA {
			return nil, proof, errors.New("owner price record hash mismatch")
		}
		if err := encoder.Encode([]any{r.Sequence, storedSHA}); err != nil {
			return nil, proof, err
		}
		proof.Versions++
		proof.Points += len(input.Points)
		if instrument == input.InstrumentID && currency == input.Currency && version == input.SourceVersion {
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

func proveOwnerPriceSeriesRecovery(ctx context.Context, q queryer) (ownerPriceSeriesRecoveryProof, error) {
	_, proof, err := replayOwnerPriceSeries(ctx, q, "", "", "")
	return proof, err
}

func (s *Service) importOwnerPriceSeries(ctx context.Context, raw []byte) (*OwnerPriceSeriesReceipt, error) {
	input, canonical, sha, err := decodeOwnerPriceSeries(ctx, raw)
	if err != nil {
		return nil, err
	}
	if s == nil || s.db == nil || s.now == nil {
		return nil, errors.New("owner price service is unavailable")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireSchemaContext(ctx, tx); err != nil {
		return nil, err
	}
	if _, _, err := replaySecurityPriceObservations(ctx, tx); err != nil {
		return nil, err
	}
	admitted, err := admittedOwnerPriceIdentities(ctx, tx)
	if err != nil {
		return nil, err
	}
	if _, ok := admitted[ownerPriceKey(input.InstrumentID, input.Currency)]; !ok {
		return nil, errors.New("owner price identity has no admitted ledger instrument")
	}
	prior, proof, err := replayOwnerPriceSeries(ctx, tx, input.InstrumentID, input.Currency, input.SourceVersion)
	if err != nil {
		return nil, err
	}
	if prior != nil {
		if prior.DataSHA256 != sha {
			return nil, errors.New("owner price version is already bound to different data")
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return ownerPriceReceipt(prior), nil
	}
	r := ownerPriceSeriesRecord{Sequence: int64(proof.Versions + 1), Input: *input, DataSHA256: sha, RecordedAt: s.now().UTC().Format(time.RFC3339Nano)}
	recorded, err := time.Parse(time.RFC3339Nano, r.RecordedAt)
	fetched, _ := time.Parse(time.RFC3339Nano, input.FetchedAt)
	if err != nil || !canonicalUTCString(r.RecordedAt) || recorded.Before(fetched) {
		return nil, errors.New("owner price capture is in the future")
	}
	recordSHA, err := ownerPriceRecordSHA(r)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO owner_price_series_versions(sequence,instrument_id,currency,source_version,series_json,series_sha256,recorded_at,record_sha256) VALUES(?,?,?,?,?,?,?,?)`, r.Sequence, input.InstrumentID, input.Currency, input.SourceVersion, string(canonical), sha, r.RecordedAt, recordSHA)
	if err != nil {
		return nil, errors.New("owner price series could not be stored")
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return nil, errors.New("owner price series was not stored exactly once")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ownerPriceReceipt(&r), nil
}
