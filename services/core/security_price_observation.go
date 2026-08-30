package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type SecurityPriceObservationInput struct {
	Source              string `json:"source"`
	SourceObservationID string `json:"source_observation_id"`
	InstrumentID        string `json:"instrument_id"`
	Symbol              string `json:"symbol"`
	Venue               string `json:"venue"`
	Currency            string `json:"currency"`
	Price               string `json:"price"`
	PriceAdjustment     string `json:"price_adjustment"`
	ObservedAt          string `json:"observed_at"`
	FetchedAt           string `json:"fetched_at"`
}

type SecurityPriceObservation struct {
	ObservationID       string `json:"observation_id"`
	Source              string `json:"source"`
	SourceObservationID string `json:"source_observation_id"`
	InstrumentID        string `json:"instrument_id"`
	Symbol              string `json:"symbol"`
	Venue               string `json:"venue"`
	Currency            string `json:"currency"`
	Price               string `json:"price"`
	PriceAdjustment     string `json:"price_adjustment"`
	ObservedAt          string `json:"observed_at"`
	FetchedAt           string `json:"fetched_at"`
	RecordedAt          string `json:"recorded_at"`
	recordSHA256        string
}

type securityPriceObservationRecoveryProof struct {
	SHA256       string
	Observations int
}

var errSecurityPriceObservationNotFound = errors.New("security price observation not found")

func validSecurityPriceObservationSource(source string) bool {
	return source == "local_fixture" || source == "kiwoom_mock" || source == "kiwoom_production"
}

func kiwoomSecurityPriceObservationSource(source string) bool {
	return source == "kiwoom_mock" || source == "kiwoom_production"
}

func validateSecurityPriceObservationInput(input SecurityPriceObservationInput) error {
	if !validSecurityPriceObservationSource(input.Source) {
		return errors.New("security price observation source is outside the supported boundary")
	}
	if !safeOrderID(input.SourceObservationID) || !safeOrderID(input.InstrumentID) || !safeOrderID(input.Symbol) || !safeOrderID(input.Venue) ||
		input.Symbol != strings.ToUpper(input.Symbol) || input.Venue != strings.ToUpper(input.Venue) {
		return errors.New("security price observation identifier is invalid")
	}
	if kiwoomSecurityPriceObservationSource(input.Source) {
		if !kiwoomStockPattern.MatchString(input.Symbol) || input.InstrumentID != instrumentIDForSymbol(input.Symbol) || input.Venue != "XKRX" || input.Currency != "KRW" {
			return errors.New("Kiwoom price observation market identity is invalid")
		}
		wantID, err := kiwoomLatestTradeObservationID(input.Source, input.InstrumentID, input.Symbol, input.Venue, input.Currency, input.PriceAdjustment, input.ObservedAt)
		if err != nil || input.SourceObservationID != wantID {
			return errors.New("Kiwoom price observation identifier is invalid")
		}
	}
	if !currencyCodePattern.MatchString(input.Currency) || !canonicalDecimal(input.Price, true) || input.Price == "0" {
		return errors.New("security price observation price or currency is invalid")
	}
	if input.PriceAdjustment != marketDataAdjustmentUnspecified {
		return errors.New("security price observation adjustment is outside the local fixture boundary")
	}
	if !canonicalUTCString(input.ObservedAt) || !canonicalUTCString(input.FetchedAt) {
		return errors.New("security price observation timestamps must be canonical UTC")
	}
	observed, _ := time.Parse(time.RFC3339Nano, input.ObservedAt)
	fetched, _ := time.Parse(time.RFC3339Nano, input.FetchedAt)
	if fetched.Before(observed) {
		return errors.New("security price observation fetched_at precedes observed_at")
	}
	return nil
}

func (s *Service) recordKiwoomLatestTradeObservation(ctx context.Context, trade KiwoomLatestTrade) (*SecurityPriceObservation, error) {
	source := ""
	switch trade.Environment {
	case KiwoomMock:
		source = "kiwoom_mock"
	case KiwoomProduction:
		source = "kiwoom_production"
	}
	if trade.Source != "kiwoom" || source == "" || trade.Exchange != KiwoomKRX || trade.Currency != "KRW" ||
		!kiwoomStockPattern.MatchString(trade.Symbol) {
		return nil, errors.New("Kiwoom latest trade identity is invalid")
	}
	instrumentID := instrumentIDForSymbol(trade.Symbol)
	sourceID, err := kiwoomLatestTradeObservationID(source, instrumentID, trade.Symbol, "XKRX", trade.Currency, marketDataAdjustmentUnspecified, trade.ObservedAt)
	if err != nil {
		return nil, err
	}
	input := SecurityPriceObservationInput{
		Source: source, SourceObservationID: sourceID, InstrumentID: instrumentID,
		Symbol: trade.Symbol, Venue: "XKRX", Currency: trade.Currency, Price: trade.Price,
		PriceAdjustment: marketDataAdjustmentUnspecified, ObservedAt: trade.ObservedAt, FetchedAt: trade.FetchedAt,
	}
	return s.recordSecurityPriceObservation(ctx, input)
}

func (s *Service) captureKiwoomLatestTradeObservation(ctx context.Context, client *KiwoomClient, symbol string) (*SecurityPriceObservation, error) {
	if s == nil || s.db == nil || client == nil || !kiwoomStockPattern.MatchString(symbol) {
		return nil, errors.New("Kiwoom latest trade capture identity is invalid")
	}
	trade, err := client.LatestTrade(ctx, symbol)
	if err != nil {
		return nil, err
	}
	return s.recordKiwoomLatestTradeObservation(ctx, *trade)
}

func kiwoomLatestTradeObservationID(source, instrumentID, symbol, venue, currency, adjustment, observedAt string) (string, error) {
	_, hash, err := orderJSONHash([]any{
		"kiwoom_latest_trade_observation.v1", source, "ka10079", instrumentID,
		venue, symbol, currency, adjustment, observedAt,
	})
	return hash, err
}

func validateSecurityPriceObservation(observation SecurityPriceObservation) error {
	if !safeOrderID(observation.ObservationID) || !canonicalUTCString(observation.RecordedAt) {
		return errors.New("security price observation durable metadata is invalid")
	}
	if err := validateSecurityPriceObservationInput(SecurityPriceObservationInput{
		Source: observation.Source, SourceObservationID: observation.SourceObservationID, InstrumentID: observation.InstrumentID,
		Symbol: observation.Symbol, Venue: observation.Venue, Currency: observation.Currency, Price: observation.Price,
		PriceAdjustment: observation.PriceAdjustment, ObservedAt: observation.ObservedAt, FetchedAt: observation.FetchedAt,
	}); err != nil {
		return err
	}
	fetched, _ := time.Parse(time.RFC3339Nano, observation.FetchedAt)
	recorded, _ := time.Parse(time.RFC3339Nano, observation.RecordedAt)
	if recorded.Before(fetched) {
		return errors.New("security price observation recorded_at precedes fetched_at")
	}
	return nil
}

func securityPriceObservationSHA(observation SecurityPriceObservation) (string, error) {
	observation.recordSHA256 = ""
	_, hash, err := orderJSONHash(observation)
	return hash, err
}

func (s *Service) recordSecurityPriceObservation(ctx context.Context, input SecurityPriceObservationInput) (*SecurityPriceObservation, error) {
	if err := validateSecurityPriceObservationInput(input); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	stored, err := loadSecurityPriceObservationBySourceID(ctx, tx, input.Source, input.SourceObservationID)
	if err == nil {
		fetchedAtMatches := stored.FetchedAt == input.FetchedAt || kiwoomSecurityPriceObservationSource(input.Source)
		if stored.Source != input.Source || stored.SourceObservationID != input.SourceObservationID || stored.InstrumentID != input.InstrumentID ||
			stored.Symbol != input.Symbol || stored.Venue != input.Venue || stored.Currency != input.Currency || stored.Price != input.Price ||
			stored.PriceAdjustment != input.PriceAdjustment || stored.ObservedAt != input.ObservedAt || !fetchedAtMatches {
			return nil, errors.New("security price source observation identity is already bound to different data")
		}
		return stored, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	observation := &SecurityPriceObservation{
		ObservationID: s.id("security_price_observation"), Source: input.Source, SourceObservationID: input.SourceObservationID,
		InstrumentID: input.InstrumentID, Symbol: input.Symbol, Venue: input.Venue, Currency: input.Currency, Price: input.Price,
		PriceAdjustment: input.PriceAdjustment, ObservedAt: input.ObservedAt, FetchedAt: input.FetchedAt,
		RecordedAt: s.now().UTC().Format(time.RFC3339Nano),
	}
	if err := validateSecurityPriceObservation(*observation); err != nil {
		return nil, err
	}
	observation.recordSHA256, err = securityPriceObservationSHA(*observation)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO security_price_observations(
		observation_id,source,source_observation_id,instrument_id,symbol,venue,currency,price,price_adjustment,observed_at,fetched_at,record_sha256,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, observation.ObservationID, observation.Source, observation.SourceObservationID,
		observation.InstrumentID, observation.Symbol, observation.Venue, observation.Currency, observation.Price, observation.PriceAdjustment,
		observation.ObservedAt, observation.FetchedAt, observation.recordSHA256, observation.RecordedAt)
	if err != nil {
		return nil, fmt.Errorf("store security price observation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return observation, nil
}

func loadSecurityPriceObservationBySourceID(ctx context.Context, q orderQuerier, source, sourceID string) (*SecurityPriceObservation, error) {
	var observation SecurityPriceObservation
	err := q.QueryRowContext(ctx, `SELECT observation_id,source,source_observation_id,instrument_id,symbol,venue,currency,price,price_adjustment,observed_at,fetched_at,record_sha256,recorded_at
		FROM security_price_observations WHERE source=? AND source_observation_id=?`, source, sourceID).Scan(
		&observation.ObservationID, &observation.Source, &observation.SourceObservationID, &observation.InstrumentID, &observation.Symbol,
		&observation.Venue, &observation.Currency, &observation.Price, &observation.PriceAdjustment, &observation.ObservedAt,
		&observation.FetchedAt, &observation.recordSHA256, &observation.RecordedAt)
	if err != nil {
		return nil, err
	}
	if err := validateSecurityPriceObservation(observation); err != nil {
		return nil, err
	}
	wantSHA, err := securityPriceObservationSHA(observation)
	if err != nil || wantSHA != observation.recordSHA256 {
		return nil, errors.New("security price observation metadata or hash mismatch")
	}
	return &observation, nil
}

func validateSecurityPriceObservationQuery(source, instrumentID, symbol, venue, currency, adjustment, asOf string) error {
	if source != "local_fixture" || !safeOrderID(instrumentID) || !safeOrderID(symbol) || !safeOrderID(venue) ||
		symbol != strings.ToUpper(symbol) || venue != strings.ToUpper(venue) ||
		!currencyCodePattern.MatchString(currency) || adjustment != marketDataAdjustmentUnspecified || !canonicalUTCString(asOf) {
		return errors.New("invalid security price observation query")
	}
	return nil
}

// ponytail: linear replay is sufficient for personal local-fixture prices; add a projection only after measured volume makes it hot.
func replaySecurityPriceObservations(ctx context.Context, q orderQuerier) ([]SecurityPriceObservation, securityPriceObservationRecoveryProof, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence,observation_id,source,source_observation_id,instrument_id,symbol,venue,currency,price,price_adjustment,observed_at,fetched_at,record_sha256,recorded_at
		FROM security_price_observations ORDER BY sequence`)
	if err != nil {
		return nil, securityPriceObservationRecoveryProof{}, err
	}
	defer rows.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	observations := []SecurityPriceObservation{}
	for rows.Next() {
		var sequence int64
		var observation SecurityPriceObservation
		if err := rows.Scan(&sequence, &observation.ObservationID, &observation.Source, &observation.SourceObservationID,
			&observation.InstrumentID, &observation.Symbol, &observation.Venue, &observation.Currency, &observation.Price,
			&observation.PriceAdjustment, &observation.ObservedAt, &observation.FetchedAt, &observation.recordSHA256, &observation.RecordedAt); err != nil {
			return nil, securityPriceObservationRecoveryProof{}, err
		}
		if err := validateSecurityPriceObservation(observation); err != nil {
			return nil, securityPriceObservationRecoveryProof{}, fmt.Errorf("security price observation %q metadata mismatch: %w", observation.ObservationID, err)
		}
		wantSHA, err := securityPriceObservationSHA(observation)
		if err != nil || wantSHA != observation.recordSHA256 {
			return nil, securityPriceObservationRecoveryProof{}, fmt.Errorf("security price observation %q metadata or hash mismatch", observation.ObservationID)
		}
		if err := encoder.Encode([]any{sequence, observation.ObservationID, observation.Source, observation.SourceObservationID,
			observation.InstrumentID, observation.Symbol, observation.Venue, observation.Currency, observation.Price, observation.PriceAdjustment,
			observation.ObservedAt, observation.FetchedAt, observation.recordSHA256, observation.RecordedAt}); err != nil {
			return nil, securityPriceObservationRecoveryProof{}, err
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, securityPriceObservationRecoveryProof{}, err
	}
	return observations, securityPriceObservationRecoveryProof{SHA256: hex.EncodeToString(hash.Sum(nil)), Observations: len(observations)}, nil
}

func proveSecurityPriceObservationRecovery(ctx context.Context, q orderQuerier) (securityPriceObservationRecoveryProof, error) {
	_, proof, err := replaySecurityPriceObservations(ctx, q)
	return proof, err
}

func latestSecurityPriceObservation(ctx context.Context, q orderQuerier, source, instrumentID, symbol, venue, currency, adjustment, asOf string) (*SecurityPriceObservation, error) {
	if err := validateSecurityPriceObservationQuery(source, instrumentID, symbol, venue, currency, adjustment, asOf); err != nil {
		return nil, errors.New("invalid security price observation query")
	}
	cutoff, _ := time.Parse(time.RFC3339Nano, asOf)
	observations, _, err := replaySecurityPriceObservations(ctx, q)
	if err != nil {
		return nil, err
	}
	var selected *SecurityPriceObservation
	for i := range observations {
		observation := &observations[i]
		if observation.Source != source || observation.InstrumentID != instrumentID || observation.Symbol != symbol || observation.Venue != venue ||
			observation.Currency != currency || observation.PriceAdjustment != adjustment {
			continue
		}
		observed, _ := time.Parse(time.RFC3339Nano, observation.ObservedAt)
		fetched, _ := time.Parse(time.RFC3339Nano, observation.FetchedAt)
		recorded, _ := time.Parse(time.RFC3339Nano, observation.RecordedAt)
		if observed.After(cutoff) || fetched.After(cutoff) || recorded.After(cutoff) {
			continue
		}
		if selected == nil {
			selected = observation
			continue
		}
		selectedObserved, _ := time.Parse(time.RFC3339Nano, selected.ObservedAt)
		if observed.After(selectedObserved) || (observed.Equal(selectedObserved) && observation.ObservationID > selected.ObservationID) {
			selected = observation
		}
	}
	if selected == nil {
		return nil, errSecurityPriceObservationNotFound
	}
	return selected, nil
}
