package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

var currencyCodePattern = regexp.MustCompile(`^[A-Z]{3}$`)

type FXObservationInput struct {
	Source              string `json:"source"`
	SourceObservationID string `json:"source_observation_id"`
	BaseCurrency        string `json:"base_currency"`
	QuoteCurrency       string `json:"quote_currency"`
	Rate                string `json:"rate"`
	ObservedAt          string `json:"observed_at"`
	FetchedAt           string `json:"fetched_at"`
}

type FXObservation struct {
	ObservationID       string `json:"observation_id"`
	Source              string `json:"source"`
	SourceObservationID string `json:"source_observation_id"`
	BaseCurrency        string `json:"base_currency"`
	QuoteCurrency       string `json:"quote_currency"`
	Rate                string `json:"rate"`
	ObservedAt          string `json:"observed_at"`
	FetchedAt           string `json:"fetched_at"`
	RecordedAt          string `json:"recorded_at"`
	recordSHA256        string
}

type fxObservationRecoveryProof struct {
	SHA256       string
	Observations int
}

var errFXObservationNotFound = errors.New("FX observation not found")

func validateFXObservationInput(input FXObservationInput) error {
	if input.Source != "local_fixture" {
		return errors.New("FX observation source is outside the local fixture boundary")
	}
	if !safeOrderID(input.SourceObservationID) {
		return errors.New("FX source observation identifier is invalid")
	}
	if !currencyCodePattern.MatchString(input.BaseCurrency) || !currencyCodePattern.MatchString(input.QuoteCurrency) || input.BaseCurrency == input.QuoteCurrency {
		return errors.New("FX observation currencies must be distinct uppercase three-letter codes")
	}
	if !canonicalDecimal(input.Rate, true) || input.Rate == "0" {
		return errors.New("FX observation rate must be a positive canonical decimal")
	}
	if !canonicalUTCString(input.ObservedAt) || !canonicalUTCString(input.FetchedAt) {
		return errors.New("FX observation timestamps must be canonical UTC")
	}
	return nil
}

func validateFXObservation(observation FXObservation) error {
	if !safeOrderID(observation.ObservationID) || !canonicalUTCString(observation.RecordedAt) {
		return errors.New("FX observation durable metadata is invalid")
	}
	return validateFXObservationInput(FXObservationInput{
		Source: observation.Source, SourceObservationID: observation.SourceObservationID,
		BaseCurrency: observation.BaseCurrency, QuoteCurrency: observation.QuoteCurrency, Rate: observation.Rate,
		ObservedAt: observation.ObservedAt, FetchedAt: observation.FetchedAt,
	})
}

func fxObservationSHA(observation FXObservation) (string, error) {
	observation.recordSHA256 = ""
	_, hash, err := orderJSONHash(observation)
	return hash, err
}

func (s *Service) recordFXObservation(ctx context.Context, input FXObservationInput) (*FXObservation, error) {
	if err := validateFXObservationInput(input); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	stored, err := loadFXObservationBySourceID(ctx, tx, input.Source, input.SourceObservationID)
	if err == nil {
		if stored.Source != input.Source || stored.SourceObservationID != input.SourceObservationID ||
			stored.BaseCurrency != input.BaseCurrency || stored.QuoteCurrency != input.QuoteCurrency || stored.Rate != input.Rate ||
			stored.ObservedAt != input.ObservedAt || stored.FetchedAt != input.FetchedAt {
			return nil, errors.New("FX source observation identity is already bound to different data")
		}
		return stored, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	observation := &FXObservation{
		ObservationID: s.id("fx_observation"), Source: input.Source, SourceObservationID: input.SourceObservationID,
		BaseCurrency: input.BaseCurrency, QuoteCurrency: input.QuoteCurrency, Rate: input.Rate,
		ObservedAt: input.ObservedAt, FetchedAt: input.FetchedAt, RecordedAt: s.now().UTC().Format(time.RFC3339Nano),
	}
	if err := validateFXObservation(*observation); err != nil {
		return nil, err
	}
	observation.recordSHA256, err = fxObservationSHA(*observation)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO fx_observations(
		observation_id,source,source_observation_id,base_currency,quote_currency,rate,observed_at,fetched_at,record_sha256,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?)`, observation.ObservationID, observation.Source, observation.SourceObservationID,
		observation.BaseCurrency, observation.QuoteCurrency, observation.Rate, observation.ObservedAt, observation.FetchedAt, observation.recordSHA256, observation.RecordedAt)
	if err != nil {
		return nil, fmt.Errorf("store FX observation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return observation, nil
}

func loadFXObservationBySourceID(ctx context.Context, q orderQuerier, source, sourceID string) (*FXObservation, error) {
	var observation FXObservation
	err := q.QueryRowContext(ctx, `SELECT observation_id,source,source_observation_id,base_currency,quote_currency,rate,observed_at,fetched_at,record_sha256,recorded_at
		FROM fx_observations WHERE source=? AND source_observation_id=?`, source, sourceID).Scan(
		&observation.ObservationID, &observation.Source, &observation.SourceObservationID, &observation.BaseCurrency,
		&observation.QuoteCurrency, &observation.Rate, &observation.ObservedAt, &observation.FetchedAt, &observation.recordSHA256, &observation.RecordedAt)
	if err != nil {
		return nil, err
	}
	if err := validateFXObservation(observation); err != nil {
		return nil, err
	}
	wantSHA, err := fxObservationSHA(observation)
	if err != nil || wantSHA != observation.recordSHA256 {
		return nil, errors.New("FX observation metadata or hash mismatch")
	}
	return &observation, nil
}

func latestDirectFXObservation(ctx context.Context, q orderQuerier, source, base, quote, asOf string) (*FXObservation, error) {
	if err := validateFXObservationQuery(source, base, quote, asOf); err != nil {
		return nil, errors.New("invalid direct FX observation query")
	}
	var observation FXObservation
	err := q.QueryRowContext(ctx, `SELECT observation_id,source,source_observation_id,base_currency,quote_currency,rate,observed_at,fetched_at,record_sha256,recorded_at
		FROM fx_observations WHERE source=? AND base_currency=? AND quote_currency=? AND observed_at<=? AND fetched_at<=?
		ORDER BY observed_at DESC,sequence DESC LIMIT 1`, source, base, quote, asOf, asOf).Scan(
		&observation.ObservationID, &observation.Source, &observation.SourceObservationID, &observation.BaseCurrency,
		&observation.QuoteCurrency, &observation.Rate, &observation.ObservedAt, &observation.FetchedAt, &observation.recordSHA256, &observation.RecordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errFXObservationNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := validateFXObservation(observation); err != nil {
		return nil, err
	}
	wantSHA, err := fxObservationSHA(observation)
	if err != nil || wantSHA != observation.recordSHA256 {
		return nil, errors.New("FX observation metadata or hash mismatch")
	}
	return &observation, nil
}

func validateFXObservationQuery(source, base, quote, asOf string) error {
	if source != "local_fixture" || !currencyCodePattern.MatchString(base) || !currencyCodePattern.MatchString(quote) || base == quote || !canonicalUTCString(asOf) {
		return errors.New("invalid direct FX observation query")
	}
	return nil
}

// ponytail: linear replay is sufficient for a personal FX series; add a projection only after measured volume makes it hot.
func replayFXObservations(ctx context.Context, q orderQuerier) ([]FXObservation, fxObservationRecoveryProof, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence,observation_id,source,source_observation_id,base_currency,quote_currency,rate,observed_at,fetched_at,record_sha256,recorded_at
		FROM fx_observations ORDER BY sequence`)
	if err != nil {
		return nil, fxObservationRecoveryProof{}, err
	}
	defer rows.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	observations := []FXObservation{}
	for rows.Next() {
		var sequence int64
		var observation FXObservation
		if err := rows.Scan(&sequence, &observation.ObservationID, &observation.Source, &observation.SourceObservationID,
			&observation.BaseCurrency, &observation.QuoteCurrency, &observation.Rate, &observation.ObservedAt,
			&observation.FetchedAt, &observation.recordSHA256, &observation.RecordedAt); err != nil {
			return nil, fxObservationRecoveryProof{}, err
		}
		if err := validateFXObservation(observation); err != nil {
			return nil, fxObservationRecoveryProof{}, fmt.Errorf("FX observation %q metadata mismatch: %w", observation.ObservationID, err)
		}
		wantSHA, err := fxObservationSHA(observation)
		if err != nil || wantSHA != observation.recordSHA256 {
			return nil, fxObservationRecoveryProof{}, fmt.Errorf("FX observation %q metadata or hash mismatch", observation.ObservationID)
		}
		if err := encoder.Encode([]any{sequence, observation.ObservationID, observation.Source, observation.SourceObservationID,
			observation.BaseCurrency, observation.QuoteCurrency, observation.Rate, observation.ObservedAt,
			observation.FetchedAt, observation.recordSHA256, observation.RecordedAt}); err != nil {
			return nil, fxObservationRecoveryProof{}, err
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, fxObservationRecoveryProof{}, err
	}
	return observations, fxObservationRecoveryProof{SHA256: hex.EncodeToString(hash.Sum(nil)), Observations: len(observations)}, nil
}

func proveFXObservationRecovery(ctx context.Context, q orderQuerier) (fxObservationRecoveryProof, error) {
	_, proof, err := replayFXObservations(ctx, q)
	return proof, err
}

func (s *Service) handleLatestFXObservation(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "query parameters are invalid"}})
		return
	}
	for key := range query {
		if key != "source" && key != "base_currency" && key != "quote_currency" && key != "as_of" {
			writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "unknown query parameter " + key, Field: key}})
			return
		}
	}
	for _, key := range []string{"source", "base_currency", "quote_currency", "as_of"} {
		if len(query[key]) != 1 || query[key][0] == "" {
			writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: key + " query parameter must appear exactly once", Field: key}})
			return
		}
	}
	if err := validateFXObservationQuery(query.Get("source"), query.Get("base_currency"), query.Get("quote_currency"), query.Get("as_of")); err != nil {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "direct FX observation query values are invalid"}})
		return
	}
	observation, err := latestDirectFXObservation(r.Context(), s.db, query.Get("source"), query.Get("base_currency"), query.Get("quote_currency"), query.Get("as_of"))
	if errors.Is(err, errFXObservationNotFound) {
		writeError(w, &appError{http.StatusNotFound, APIError{Code: "fx_observation_not_found", Message: "direct FX observation was not found"}})
		return
	}
	if err != nil {
		writeError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ObservationID string     `json:"observation_id"`
		Source        string     `json:"source"`
		BaseCurrency  string     `json:"base_currency"`
		QuoteCurrency string     `json:"quote_currency"`
		Rate          string     `json:"rate"`
		ObservedAt    string     `json:"observed_at"`
		FetchedAt     string     `json:"fetched_at"`
		RecordedAt    string     `json:"recorded_at"`
		Sample        bool       `json:"sample"`
		State         string     `json:"state"`
		Issues        []APIError `json:"issues"`
	}{observation.ObservationID, observation.Source, observation.BaseCurrency, observation.QuoteCurrency,
		observation.Rate, observation.ObservedAt, observation.FetchedAt, observation.RecordedAt, true, "stale",
		[]APIError{{Code: "sample_data", Message: "FX rate is a local sample and not live"}}})
}
