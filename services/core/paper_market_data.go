package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	paperMarketBarSchema         = "paper-market-bar.v1"
	capitalizedPaperSignalSchema = "paper-signal.v3"
	canonicalPaperTimeLayout     = "2006-01-02T15:04:05.000000000Z"
)

type PaperMarketBarObservation struct {
	ObservationID       string `json:"observation_id"`
	SchemaVersion       string `json:"schema_version"`
	Source              string `json:"source"`
	SourceObservationID string `json:"source_observation_id"`
	InputDataSHA256     string `json:"input_data_sha256"`
	Symbol              string `json:"symbol"`
	Venue               string `json:"venue"`
	Currency            string `json:"currency"`
	Interval            string `json:"interval"`
	Timezone            string `json:"timezone"`
	PriceAdjustment     string `json:"price_adjustment"`
	Open                string `json:"open"`
	High                string `json:"high"`
	Low                 string `json:"low"`
	Close               string `json:"close"`
	Volume              string `json:"volume"`
	OpenAt              string `json:"open_at"`
	CloseAt             string `json:"close_at"`
	SourceAvailableAt   string `json:"source_available_at"`
	FetchedAt           string `json:"fetched_at"`
	RecordedAt          string `json:"recorded_at"`
}

type PaperSignalEvent struct {
	EventID                         string `json:"event_id"`
	SchemaVersion                   string `json:"schema_version"`
	AccountRef                      string `json:"account_ref"`
	PaperAccountingSessionID        string `json:"paper_accounting_session_id"`
	StrategyResultSHA256            string `json:"strategy_result_sha256"`
	StrategySelectionEventID        string `json:"strategy_selection_event_id"`
	ExecutionPolicySHA256           string `json:"execution_policy_sha256"`
	SignalID                        string `json:"signal_id"`
	SignalBarObservationID          string `json:"signal_bar_observation_id"`
	DataSHA256                      string `json:"data_sha256"`
	Symbol                          string `json:"symbol"`
	TargetQuantity                  string `json:"target_quantity"`
	DataAsOf                        string `json:"data_as_of"`
	GeneratedAt                     string `json:"generated_at"`
	ExpiresAt                       string `json:"expires_at"`
	MarketObservationSequenceCutoff int64  `json:"market_observation_sequence_cutoff"`
	RecordedAt                      string `json:"recorded_at"`
}

type paperMarketRecoveryProof struct {
	SHA256        string
	Bars, Signals int
}

func (s *Service) recordPaperMarketBar(ctx context.Context, input PaperMarketBarObservation) (*PaperMarketBarObservation, error) {
	if s == nil || s.db == nil || s.now == nil {
		return nil, errors.New("paper market data recorder is not configured")
	}
	now := s.now().UTC()
	bar := input
	bar.ObservationID = paperMarketBarObservationID(input.Source, input.SourceObservationID)
	bar.SchemaVersion = paperMarketBarSchema
	bar.RecordedAt = now.Format(canonicalPaperTimeLayout)
	var ok bool
	if bar.OpenAt, ok = canonicalPaperTime(bar.OpenAt); !ok {
		return nil, errors.New("paper market bar open time is invalid")
	}
	if bar.CloseAt, ok = canonicalPaperTime(bar.CloseAt); !ok {
		return nil, errors.New("paper market bar close time is invalid")
	}
	if bar.SourceAvailableAt, ok = canonicalPaperTime(bar.SourceAvailableAt); !ok {
		return nil, errors.New("paper market bar availability time is invalid")
	}
	if bar.FetchedAt, ok = canonicalPaperTime(bar.FetchedAt); !ok {
		return nil, errors.New("paper market bar fetch time is invalid")
	}
	if err := validatePaperMarketBar(bar); err != nil {
		return nil, err
	}
	if fetched, _ := parsePaperTime(bar.FetchedAt); fetched.After(now) {
		return nil, errors.New("paper market bar was fetched in the future")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := replayPaperMarketRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("paper market recovery: %w", err)
	}
	existing, found, err := loadPaperMarketBarBySource(ctx, tx, bar.Source, bar.SourceObservationID)
	if err != nil {
		return nil, err
	}
	if found {
		if !samePaperMarketBarInput(*existing, bar) {
			return nil, errors.New("paper market source observation conflicts with its recorded bar")
		}
		return existing, nil
	}
	recordJSON, recordSHA, err := orderJSONHash(bar)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO paper_market_bar_observations(
		observation_id,schema_version,source,source_observation_id,input_data_sha256,symbol,venue,currency,interval,timezone,
		price_adjustment,open,high,low,close,volume,open_at,close_at,source_available_at,fetched_at,record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		bar.ObservationID, bar.SchemaVersion, bar.Source, bar.SourceObservationID, bar.InputDataSHA256, bar.Symbol, bar.Venue, bar.Currency,
		bar.Interval, bar.Timezone, bar.PriceAdjustment, bar.Open, bar.High, bar.Low, bar.Close, bar.Volume, bar.OpenAt, bar.CloseAt,
		bar.SourceAvailableAt, bar.FetchedAt, recordSHA, string(recordJSON), bar.RecordedAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &bar, nil
}

func (s *Service) recordPaperSignalEventTx(ctx context.Context, tx *sql.Tx, accountRef string, signal PaperSignal) (*PaperSignalEvent, error) {
	if s == nil || s.db == nil || s.now == nil || tx == nil || !orderAlias(accountRef, "account") {
		return nil, errors.New("paper signal recorder is not configured")
	}
	now := s.now().UTC()
	var ok bool
	if signal.DataAsOf, ok = canonicalPaperTime(signal.DataAsOf); !ok {
		return nil, errors.New("paper signal data time is invalid")
	}
	if signal.GeneratedAt, ok = canonicalPaperTime(signal.GeneratedAt); !ok {
		return nil, errors.New("paper signal generation time is invalid")
	}
	if signal.ExpiresAt, ok = canonicalPaperTime(signal.ExpiresAt); !ok {
		return nil, errors.New("paper signal expiry time is invalid")
	}
	if err := validateCapitalizedPaperSignalShape(signal); err != nil {
		return nil, err
	}
	if _, err := provePaperAccountingRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("paper signal accounting recovery: %w", err)
	}
	existing, found, err := loadPaperSignalEvent(ctx, tx, accountRef, signal.SignalID)
	if err != nil {
		return nil, err
	}
	if found {
		if !samePaperSignalInput(*existing, signal) {
			return nil, errors.New("paper signal identity conflicts with its recorded event")
		}
		return existing, nil
	}
	if err := validateCapitalizedPaperSignal(signal, now); err != nil {
		return nil, err
	}
	policy, err := loadCurrentStrategyExecutionPolicy(ctx, tx, signal.StrategyResultSHA256, signal.StrategySelectionEventID)
	if err != nil {
		return nil, err
	}
	session, found, err := loadPaperAccountingSession(ctx, tx, accountRef)
	if err != nil {
		return nil, err
	}
	if !found || session.ExecutionPolicySHA256 != policy.SHA256 {
		return nil, errors.New("paper signal accounting policy does not match the account session")
	}
	bar, barSequence, err := loadPaperMarketBarByID(ctx, tx, signal.SignalBarObservationID)
	if err != nil {
		return nil, err
	}
	if bar.Symbol != signal.Symbol || bar.InputDataSHA256 != signal.DataSHA256 || bar.CloseAt != signal.DataAsOf ||
		bar.Source != "paper_fixture" || bar.Venue != "KRX" || bar.Currency != "KRW" || bar.Interval != "1d" ||
		bar.Timezone != "Asia/Seoul" || bar.PriceAdjustment != "unspecified" {
		return nil, errors.New("paper signal bar metadata is invalid")
	}
	availableAt, _ := parsePaperTime(bar.SourceAvailableAt)
	generatedAt, _ := parsePaperTime(signal.GeneratedAt)
	if generatedAt.Before(availableAt) {
		return nil, errors.New("paper signal predates source availability")
	}
	var cutoff, sameSeriesLatest, legacyOrders int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM paper_market_bar_observations`).Scan(&cutoff); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM paper_market_bar_observations
		WHERE source=? AND symbol=? AND venue=? AND interval=? AND timezone=? AND price_adjustment=? AND sequence<=?`,
		bar.Source, bar.Symbol, bar.Venue, bar.Interval, bar.Timezone, bar.PriceAdjustment, cutoff).Scan(&sameSeriesLatest); err != nil {
		return nil, err
	}
	if barSequence != sameSeriesLatest {
		return nil, errors.New("paper signal bar is not latest at the transaction cutoff")
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_idempotency WHERE mode='paper' AND account_ref=?
		AND json_extract(intent_json, '$.signal_schema_version') IN ('paper-signal.v1','paper-signal.v2')`, accountRef).Scan(&legacyOrders); err != nil {
		return nil, err
	}
	if legacyOrders != 0 {
		return nil, errors.New("paper signal cannot follow a legacy paper order")
	}
	event := PaperSignalEvent{
		EventID: paperSignalEventID(accountRef, signal.SignalID), SchemaVersion: capitalizedPaperSignalSchema,
		AccountRef: accountRef, PaperAccountingSessionID: session.SessionID,
		StrategyResultSHA256: signal.StrategyResultSHA256, StrategySelectionEventID: signal.StrategySelectionEventID,
		ExecutionPolicySHA256: policy.SHA256, SignalID: signal.SignalID, SignalBarObservationID: signal.SignalBarObservationID,
		DataSHA256: signal.DataSHA256, Symbol: signal.Symbol, TargetQuantity: signal.TargetQuantity,
		DataAsOf: signal.DataAsOf, GeneratedAt: signal.GeneratedAt, ExpiresAt: signal.ExpiresAt,
		MarketObservationSequenceCutoff: cutoff, RecordedAt: now.Format(canonicalPaperTimeLayout),
	}
	if err := validatePaperSignalEvent(event); err != nil {
		return nil, err
	}
	recordJSON, recordSHA, err := orderJSONHash(event)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO paper_signal_events(
		event_id,schema_version,account_ref,paper_accounting_session_id,strategy_result_sha256,strategy_selection_event_id,
		execution_policy_sha256,signal_id,signal_bar_observation_id,data_sha256,symbol,target_quantity,data_as_of,generated_at,
		expires_at,market_observation_sequence_cutoff,record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		event.EventID, event.SchemaVersion, event.AccountRef, event.PaperAccountingSessionID, event.StrategyResultSHA256,
		event.StrategySelectionEventID, event.ExecutionPolicySHA256, event.SignalID, event.SignalBarObservationID, event.DataSHA256,
		event.Symbol, event.TargetQuantity, event.DataAsOf, event.GeneratedAt, event.ExpiresAt, event.MarketObservationSequenceCutoff,
		recordSHA, string(recordJSON), event.RecordedAt)
	if err != nil {
		return nil, err
	}
	return &event, nil
}

func provePaperMarketRecovery(ctx context.Context, q orderQuerier) (paperMarketRecoveryProof, error) {
	if _, err := proveOrderRecovery(ctx, q); err != nil {
		return paperMarketRecoveryProof{}, fmt.Errorf("paper market order recovery: %w", err)
	}
	if _, err := replayStrategyRegistry(ctx, q); err != nil {
		return paperMarketRecoveryProof{}, fmt.Errorf("paper market strategy recovery: %w", err)
	}
	return replayPaperMarketRecovery(ctx, q)
}

func replayPaperMarketRecovery(ctx context.Context, q orderQuerier) (paperMarketRecoveryProof, error) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	rows, err := q.QueryContext(ctx, `SELECT sequence,observation_id,schema_version,source,source_observation_id,input_data_sha256,
		symbol,venue,currency,interval,timezone,price_adjustment,open,high,low,close,volume,open_at,close_at,source_available_at,
		fetched_at,record_sha256,record_json,recorded_at FROM paper_market_bar_observations ORDER BY sequence`)
	if err != nil {
		return paperMarketRecoveryProof{}, err
	}
	type storedBar struct {
		sequence              int64
		bar                   PaperMarketBarObservation
		recordSHA, recordJSON string
	}
	var bars []storedBar
	for rows.Next() {
		var item storedBar
		if err := rows.Scan(&item.sequence, &item.bar.ObservationID, &item.bar.SchemaVersion, &item.bar.Source,
			&item.bar.SourceObservationID, &item.bar.InputDataSHA256, &item.bar.Symbol, &item.bar.Venue, &item.bar.Currency,
			&item.bar.Interval, &item.bar.Timezone, &item.bar.PriceAdjustment, &item.bar.Open, &item.bar.High, &item.bar.Low,
			&item.bar.Close, &item.bar.Volume, &item.bar.OpenAt, &item.bar.CloseAt, &item.bar.SourceAvailableAt, &item.bar.FetchedAt,
			&item.recordSHA, &item.recordJSON, &item.bar.RecordedAt); err != nil {
			rows.Close()
			return paperMarketRecoveryProof{}, err
		}
		bars = append(bars, item)
	}
	if err := closeRows(rows); err != nil {
		return paperMarketRecoveryProof{}, err
	}
	for index, item := range bars {
		if item.sequence != int64(index+1) {
			return paperMarketRecoveryProof{}, fmt.Errorf("paper market bar sequence %d is invalid", item.sequence)
		}
		if err := validateStoredPaperMarketBar(item.bar, item.recordSHA, item.recordJSON); err != nil {
			return paperMarketRecoveryProof{}, fmt.Errorf("paper market bar %q is invalid: %w", item.bar.ObservationID, err)
		}
		if err := encoder.Encode([]any{"paper_market_bar_observations", item.sequence, item.bar, item.recordSHA, item.recordJSON}); err != nil {
			return paperMarketRecoveryProof{}, err
		}
	}

	rows, err = q.QueryContext(ctx, `SELECT sequence,event_id,schema_version,account_ref,paper_accounting_session_id,
		strategy_result_sha256,strategy_selection_event_id,execution_policy_sha256,signal_id,signal_bar_observation_id,data_sha256,
		symbol,target_quantity,data_as_of,generated_at,expires_at,market_observation_sequence_cutoff,record_sha256,record_json,recorded_at
		FROM paper_signal_events ORDER BY sequence`)
	if err != nil {
		return paperMarketRecoveryProof{}, err
	}
	type storedSignal struct {
		sequence              int64
		event                 PaperSignalEvent
		recordSHA, recordJSON string
	}
	var signals []storedSignal
	for rows.Next() {
		var item storedSignal
		if err := rows.Scan(&item.sequence, &item.event.EventID, &item.event.SchemaVersion, &item.event.AccountRef,
			&item.event.PaperAccountingSessionID, &item.event.StrategyResultSHA256, &item.event.StrategySelectionEventID,
			&item.event.ExecutionPolicySHA256, &item.event.SignalID, &item.event.SignalBarObservationID, &item.event.DataSHA256,
			&item.event.Symbol, &item.event.TargetQuantity, &item.event.DataAsOf, &item.event.GeneratedAt, &item.event.ExpiresAt,
			&item.event.MarketObservationSequenceCutoff, &item.recordSHA, &item.recordJSON, &item.event.RecordedAt); err != nil {
			rows.Close()
			return paperMarketRecoveryProof{}, err
		}
		signals = append(signals, item)
	}
	if err := closeRows(rows); err != nil {
		return paperMarketRecoveryProof{}, err
	}
	for index, item := range signals {
		if item.sequence != int64(index+1) {
			return paperMarketRecoveryProof{}, fmt.Errorf("paper signal sequence %d is invalid", item.sequence)
		}
		if err := validateStoredPaperSignalEvent(ctx, q, item.event, item.recordSHA, item.recordJSON); err != nil {
			return paperMarketRecoveryProof{}, fmt.Errorf("paper signal %q is invalid: %w", item.event.EventID, err)
		}
		if err := encoder.Encode([]any{"paper_signal_events", item.sequence, item.event, item.recordSHA, item.recordJSON}); err != nil {
			return paperMarketRecoveryProof{}, err
		}
	}
	return paperMarketRecoveryProof{SHA256: hex.EncodeToString(hash.Sum(nil)), Bars: len(bars), Signals: len(signals)}, nil
}

func validatePaperMarketBar(bar PaperMarketBarObservation) error {
	openAt, openOK := parsePaperTime(bar.OpenAt)
	closeAt, closeOK := parsePaperTime(bar.CloseAt)
	availableAt, availableOK := parsePaperTime(bar.SourceAvailableAt)
	fetchedAt, fetchedOK := parsePaperTime(bar.FetchedAt)
	recordedAt, recordedOK := parsePaperTime(bar.RecordedAt)
	if !safeOrderID(bar.ObservationID) || bar.SchemaVersion != paperMarketBarSchema || bar.Source != "paper_fixture" ||
		!safeOrderID(bar.SourceObservationID) || !strategySHA256Pattern.MatchString(bar.InputDataSHA256) ||
		!kiwoomStockPattern.MatchString(bar.Symbol) || bar.Venue != "KRX" || bar.Currency != "KRW" || bar.Interval != "1d" ||
		bar.Timezone != "Asia/Seoul" || bar.PriceAdjustment != "unspecified" || !openOK || !closeOK || !availableOK ||
		!fetchedOK || !recordedOK || !openAt.Before(closeAt) || closeAt.After(availableAt) || availableAt.After(fetchedAt) ||
		fetchedAt.After(recordedAt) || !canonicalPaperTimes(bar.OpenAt, bar.CloseAt, bar.SourceAvailableAt, bar.FetchedAt, bar.RecordedAt) ||
		paperMarketBarObservationID(bar.Source, bar.SourceObservationID) != bar.ObservationID {
		return errors.New("paper market bar identity or time contract is invalid")
	}
	prices := make([]*big.Rat, 4)
	for index, raw := range []string{bar.Open, bar.High, bar.Low, bar.Close} {
		value, err := parseDecimal(raw)
		if err != nil || value.Sign() <= 0 {
			return errors.New("paper market bar price is invalid")
		}
		prices[index] = value
	}
	volume, err := parseDecimal(bar.Volume)
	if err != nil || volume.Sign() < 0 {
		return errors.New("paper market bar volume is invalid")
	}
	if prices[1].Cmp(prices[0]) < 0 || prices[1].Cmp(prices[3]) < 0 || prices[2].Cmp(prices[0]) > 0 || prices[2].Cmp(prices[3]) > 0 {
		return errors.New("paper market bar OHLC range is invalid")
	}
	return nil
}

func validateCapitalizedPaperSignal(signal PaperSignal, now time.Time) error {
	if err := validateCapitalizedPaperSignalShape(signal); err != nil {
		return err
	}
	generatedAt, _ := parsePaperTime(signal.GeneratedAt)
	expiresAt, _ := parsePaperTime(signal.ExpiresAt)
	if generatedAt.After(now) || !now.Before(expiresAt) {
		return errors.New("capitalized paper signal is not active")
	}
	return nil
}

func validateCapitalizedPaperSignalShape(signal PaperSignal) error {
	dataAsOf, dataOK := parsePaperTime(signal.DataAsOf)
	generatedAt, generatedOK := parsePaperTime(signal.GeneratedAt)
	expiresAt, expiresOK := parsePaperTime(signal.ExpiresAt)
	if signal.SchemaVersion != capitalizedPaperSignalSchema || !safeOrderID(signal.SignalID) || !safeOrderID(signal.SignalBarObservationID) ||
		!strategySHA256Pattern.MatchString(signal.StrategyResultSHA256) || !safeOrderID(signal.StrategySelectionEventID) ||
		!strategySHA256Pattern.MatchString(signal.DataSHA256) || !kiwoomStockPattern.MatchString(signal.Symbol) ||
		!validPaperTargetQuantity(signal.TargetQuantity) || !dataOK || !generatedOK || !expiresOK || dataAsOf.After(generatedAt) ||
		!generatedAt.Before(expiresAt) {
		return errors.New("capitalized paper signal is invalid")
	}
	return nil
}

func validatePaperSignalEvent(event PaperSignalEvent) error {
	dataAsOf, dataOK := parsePaperTime(event.DataAsOf)
	generatedAt, generatedOK := parsePaperTime(event.GeneratedAt)
	expiresAt, expiresOK := parsePaperTime(event.ExpiresAt)
	recordedAt, recordedOK := parsePaperTime(event.RecordedAt)
	if !safeOrderID(event.EventID) || event.SchemaVersion != capitalizedPaperSignalSchema || !orderAlias(event.AccountRef, "account") ||
		!safeOrderID(event.PaperAccountingSessionID) || !strategySHA256Pattern.MatchString(event.StrategyResultSHA256) ||
		!safeOrderID(event.StrategySelectionEventID) || !strategySHA256Pattern.MatchString(event.ExecutionPolicySHA256) ||
		!safeOrderID(event.SignalID) || !safeOrderID(event.SignalBarObservationID) || !strategySHA256Pattern.MatchString(event.DataSHA256) ||
		!kiwoomStockPattern.MatchString(event.Symbol) || !validPaperTargetQuantity(event.TargetQuantity) || event.MarketObservationSequenceCutoff <= 0 ||
		!dataOK || !generatedOK || !expiresOK || !recordedOK || !canonicalPaperTimes(event.DataAsOf, event.GeneratedAt, event.ExpiresAt, event.RecordedAt) ||
		dataAsOf.After(generatedAt) || generatedAt.After(recordedAt) ||
		!generatedAt.Before(expiresAt) || !recordedAt.Before(expiresAt) || paperSignalEventID(event.AccountRef, event.SignalID) != event.EventID {
		return errors.New("paper signal event is invalid")
	}
	return nil
}

func validateStoredPaperMarketBar(bar PaperMarketBarObservation, recordSHA, recordJSON string) error {
	if err := validatePaperMarketBar(bar); err != nil {
		return err
	}
	canonical, actualSHA, err := orderJSONHash(bar)
	if err != nil || string(canonical) != recordJSON || actualSHA != recordSHA {
		return errors.New("paper market bar record hash mismatch")
	}
	return nil
}

func validateStoredPaperSignalEvent(ctx context.Context, q orderQuerier, event PaperSignalEvent, recordSHA, recordJSON string) error {
	if err := validatePaperSignalEvent(event); err != nil {
		return err
	}
	session, found, err := loadPaperAccountingSession(ctx, q, event.AccountRef)
	if err != nil || !found || session.SessionID != event.PaperAccountingSessionID || session.ExecutionPolicySHA256 != event.ExecutionPolicySHA256 {
		return errors.New("paper signal session binding is invalid")
	}
	var artifactJSON, selectedResult string
	if err := q.QueryRowContext(ctx, `SELECT artifact_json FROM strategy_research_evidence WHERE result_sha256=?`, event.StrategyResultSHA256).Scan(&artifactJSON); err != nil {
		return err
	}
	evidence, err := decodeStrategyArtifact([]byte(artifactJSON))
	if err != nil || evidence.ResultSHA256 != event.StrategyResultSHA256 || evidence.executionPolicy.SHA256 != event.ExecutionPolicySHA256 {
		return errors.New("paper signal execution policy binding is invalid")
	}
	if err := q.QueryRowContext(ctx, `SELECT selected_result_sha256 FROM strategy_selection_events WHERE event_id=?`, event.StrategySelectionEventID).Scan(&selectedResult); err != nil || selectedResult != event.StrategyResultSHA256 {
		return errors.New("paper signal selection binding is invalid")
	}
	bar, barSequence, err := loadPaperMarketBarByID(ctx, q, event.SignalBarObservationID)
	if err != nil {
		return err
	}
	generatedAt, _ := parsePaperTime(event.GeneratedAt)
	availableAt, _ := parsePaperTime(bar.SourceAvailableAt)
	if bar.InputDataSHA256 != event.DataSHA256 || bar.Symbol != event.Symbol || bar.CloseAt != event.DataAsOf || generatedAt.Before(availableAt) ||
		barSequence > event.MarketObservationSequenceCutoff {
		return errors.New("paper signal bar binding is invalid")
	}
	var latest, cutoffExists int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM paper_market_bar_observations
		WHERE source=? AND symbol=? AND venue=? AND interval=? AND timezone=? AND price_adjustment=? AND sequence<=?`,
		bar.Source, bar.Symbol, bar.Venue, bar.Interval, bar.Timezone, bar.PriceAdjustment, event.MarketObservationSequenceCutoff).Scan(&latest); err != nil {
		return err
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM paper_market_bar_observations WHERE sequence=?`, event.MarketObservationSequenceCutoff).Scan(&cutoffExists); err != nil {
		return err
	}
	if latest != barSequence || cutoffExists != 1 {
		return errors.New("paper signal cutoff binding is invalid")
	}
	var legacyOrders int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_idempotency WHERE mode='paper' AND account_ref=?
		AND json_extract(intent_json, '$.signal_schema_version') IN ('paper-signal.v1','paper-signal.v2')`, event.AccountRef).Scan(&legacyOrders); err != nil {
		return err
	}
	if legacyOrders != 0 {
		return errors.New("paper signal account contains a legacy paper order")
	}
	canonical, actualSHA, err := orderJSONHash(event)
	if err != nil || string(canonical) != recordJSON || actualSHA != recordSHA {
		return errors.New("paper signal record hash mismatch")
	}
	return nil
}

func loadPaperMarketBarBySource(ctx context.Context, q orderQuerier, source, sourceID string) (*PaperMarketBarObservation, bool, error) {
	row := q.QueryRowContext(ctx, `SELECT observation_id,schema_version,source,source_observation_id,input_data_sha256,symbol,venue,currency,
		interval,timezone,price_adjustment,open,high,low,close,volume,open_at,close_at,source_available_at,fetched_at,record_sha256,record_json,recorded_at
		FROM paper_market_bar_observations WHERE source=? AND source_observation_id=?`, source, sourceID)
	bar, _, found, err := scanPaperMarketBar(row, false)
	return bar, found, err
}

func loadPaperMarketBarByID(ctx context.Context, q orderQuerier, observationID string) (*PaperMarketBarObservation, int64, error) {
	row := q.QueryRowContext(ctx, `SELECT sequence,observation_id,schema_version,source,source_observation_id,input_data_sha256,symbol,venue,currency,
		interval,timezone,price_adjustment,open,high,low,close,volume,open_at,close_at,source_available_at,fetched_at,record_sha256,record_json,recorded_at
		FROM paper_market_bar_observations WHERE observation_id=?`, observationID)
	bar, sequence, found, err := scanPaperMarketBar(row, true)
	if err != nil {
		return nil, 0, err
	}
	if !found {
		return nil, 0, errors.New("paper signal bar does not exist")
	}
	return bar, sequence, nil
}

func scanPaperMarketBar(row rowScanner, withSequence bool) (*PaperMarketBarObservation, int64, bool, error) {
	var bar PaperMarketBarObservation
	var sequence int64
	var recordSHA, recordJSON string
	values := []any{&bar.ObservationID, &bar.SchemaVersion, &bar.Source, &bar.SourceObservationID, &bar.InputDataSHA256,
		&bar.Symbol, &bar.Venue, &bar.Currency, &bar.Interval, &bar.Timezone, &bar.PriceAdjustment, &bar.Open, &bar.High, &bar.Low,
		&bar.Close, &bar.Volume, &bar.OpenAt, &bar.CloseAt, &bar.SourceAvailableAt, &bar.FetchedAt, &recordSHA, &recordJSON, &bar.RecordedAt}
	if withSequence {
		values = append([]any{&sequence}, values...)
	}
	err := row.Scan(values...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if err := validateStoredPaperMarketBar(bar, recordSHA, recordJSON); err != nil {
		return nil, 0, false, err
	}
	return &bar, sequence, true, nil
}

func loadPaperSignalEvent(ctx context.Context, q orderQuerier, accountRef, signalID string) (*PaperSignalEvent, bool, error) {
	row := q.QueryRowContext(ctx, `SELECT event_id,schema_version,account_ref,paper_accounting_session_id,strategy_result_sha256,
		strategy_selection_event_id,execution_policy_sha256,signal_id,signal_bar_observation_id,data_sha256,symbol,target_quantity,
		data_as_of,generated_at,expires_at,market_observation_sequence_cutoff,record_sha256,record_json,recorded_at
		FROM paper_signal_events WHERE account_ref=? AND signal_id=?`, accountRef, signalID)
	var event PaperSignalEvent
	var recordSHA, recordJSON string
	err := row.Scan(&event.EventID, &event.SchemaVersion, &event.AccountRef, &event.PaperAccountingSessionID,
		&event.StrategyResultSHA256, &event.StrategySelectionEventID, &event.ExecutionPolicySHA256, &event.SignalID,
		&event.SignalBarObservationID, &event.DataSHA256, &event.Symbol, &event.TargetQuantity, &event.DataAsOf, &event.GeneratedAt,
		&event.ExpiresAt, &event.MarketObservationSequenceCutoff, &recordSHA, &recordJSON, &event.RecordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := validateStoredPaperSignalEvent(ctx, q, event, recordSHA, recordJSON); err != nil {
		return nil, false, err
	}
	return &event, true, nil
}

func samePaperMarketBarInput(left, right PaperMarketBarObservation) bool {
	left.ObservationID, left.SchemaVersion, left.RecordedAt = "", "", ""
	right.ObservationID, right.SchemaVersion, right.RecordedAt = "", "", ""
	return left == right
}

func samePaperSignalInput(event PaperSignalEvent, signal PaperSignal) bool {
	return event.SchemaVersion == signal.SchemaVersion && event.StrategyResultSHA256 == signal.StrategyResultSHA256 &&
		event.StrategySelectionEventID == signal.StrategySelectionEventID && event.SignalID == signal.SignalID &&
		event.SignalBarObservationID == signal.SignalBarObservationID && event.DataSHA256 == signal.DataSHA256 &&
		event.Symbol == signal.Symbol && event.TargetQuantity == signal.TargetQuantity && event.DataAsOf == signal.DataAsOf &&
		event.GeneratedAt == signal.GeneratedAt && event.ExpiresAt == signal.ExpiresAt
}

func validPaperTargetQuantity(raw string) bool { return raw == "0" || validOrderInteger(raw) }

func canonicalPaperTime(raw string) (string, bool) {
	value, ok := parsePaperTime(raw)
	if !ok {
		return "", false
	}
	return value.Format(canonicalPaperTimeLayout), true
}

func parsePaperTime(raw string) (time.Time, bool) {
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || value.Location() != time.UTC {
		return time.Time{}, false
	}
	return value, value.Format(time.RFC3339Nano) == raw || value.Format(canonicalPaperTimeLayout) == raw
}

func canonicalPaperTimes(values ...string) bool {
	for _, raw := range values {
		canonical, ok := canonicalPaperTime(raw)
		if !ok || canonical != raw {
			return false
		}
	}
	return true
}

func paperMarketBarObservationID(source, sourceID string) string {
	hash := sha256.Sum256([]byte(strings.Join([]string{source, sourceID}, "\x00")))
	return "paper_market_bar_" + hex.EncodeToString(hash[:16])
}

func paperSignalEventID(accountRef, signalID string) string {
	hash := sha256.Sum256([]byte(strings.Join([]string{accountRef, signalID}, "\x00")))
	return "paper_signal_event_" + hex.EncodeToString(hash[:16])
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	return rows.Close()
}
