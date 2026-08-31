package main

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"net/http"
	"time"
)

const (
	holdingValuationPolicy = "native_holding_valuation_v1"
	holdingPriceMaxAge     = 24 * time.Hour
)

type HoldingValuationPrice struct {
	ObservationID   string `json:"observation_id"`
	Source          string `json:"source"`
	Venue           string `json:"venue"`
	Currency        string `json:"currency"`
	Price           string `json:"price"`
	PriceAdjustment string `json:"price_adjustment"`
	ObservedAt      string `json:"observed_at"`
	FetchedAt       string `json:"fetched_at"`
	RecordedAt      string `json:"recorded_at"`
	Sample          bool   `json:"sample"`
	State           string `json:"state"`
}

type HoldingValuationLine struct {
	InstrumentID  string                 `json:"instrument_id"`
	Symbol        string                 `json:"symbol"`
	Quantity      string                 `json:"quantity"`
	CostBasis     string                 `json:"cost_basis"`
	Currency      string                 `json:"currency"`
	Status        string                 `json:"status"`
	Price         *HoldingValuationPrice `json:"price"`
	MarketValue   *string                `json:"market_value"`
	UnrealizedPnL *string                `json:"unrealized_pnl"`
	Issue         *APIError              `json:"issue"`
}

type HoldingValuationTotal struct {
	Currency      string `json:"currency"`
	CostBasis     string `json:"cost_basis"`
	MarketValue   string `json:"market_value"`
	UnrealizedPnL string `json:"unrealized_pnl"`
}

type HoldingValuation struct {
	Scope                    string                  `json:"scope"`
	PolicyVersion            string                  `json:"policy_version"`
	MaxObservationAgeSeconds int64                   `json:"max_observation_age_seconds"`
	ValuationAsOf            string                  `json:"valuation_as_of"`
	LedgerRevision           string                  `json:"ledger_revision"`
	LedgerAsOf               string                  `json:"ledger_as_of"`
	LedgerRecordedAt         string                  `json:"ledger_recorded_at"`
	Status                   string                  `json:"status"`
	Sample                   bool                    `json:"sample"`
	Totals                   []HoldingValuationTotal `json:"totals"`
	Lines                    []HoldingValuationLine  `json:"lines"`
	Issues                   []APIError              `json:"issues"`
}

func (s *Service) holdingValuation(ctx context.Context, asOf string) (*HoldingValuation, *appError) {
	if !canonicalUTCString(asOf) {
		return nil, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "holding valuation as_of is invalid", Field: "as_of"}}
	}
	valuationTime, _ := time.Parse(time.RFC3339Nano, asOf)
	if valuationTime.After(s.now().UTC()) {
		return nil, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "valuation as_of cannot be in the future", Field: "as_of"}}
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, internalError(err)
	}
	defer tx.Rollback()
	snapshot, err := snapshotFrom(ctx, tx)
	if err != nil {
		return nil, internalError(err)
	}
	ledgerRevision, proofRecordedAt, err := s.proveLedgerEvents(ctx, tx)
	if err != nil {
		return nil, internalError(err)
	}
	if revision(ledgerRevision) != snapshot.LedgerRevision || proofRecordedAt != snapshot.RecordedAt {
		return nil, internalError(errors.New("ledger snapshot proof mismatch"))
	}
	ledgerAsOf, ledgerAsOfErr := time.Parse(time.RFC3339Nano, snapshot.AsOf)
	ledgerRecordedAt, ledgerRecordedAtErr := time.Parse(time.RFC3339Nano, snapshot.RecordedAt)
	if ledgerAsOfErr != nil || ledgerRecordedAtErr != nil || !canonicalUTCString(snapshot.AsOf) || !canonicalUTCString(snapshot.RecordedAt) {
		return nil, internalError(errors.New("ledger snapshot timestamps are invalid"))
	}
	if valuationTime.Before(ledgerAsOf) || valuationTime.Before(ledgerRecordedAt) {
		return nil, &appError{http.StatusConflict, APIError{Code: "valuation_as_of_precedes_ledger_revision", Message: "valuation as_of precedes the current ledger state", Field: "as_of"}}
	}

	observations, _, err := replaySecurityPriceObservations(ctx, tx)
	if err != nil {
		return nil, internalError(err)
	}
	times := make(map[string][3]time.Time, len(observations))
	for _, observation := range observations {
		observedAt, observedErr := time.Parse(time.RFC3339Nano, observation.ObservedAt)
		fetchedAt, fetchedErr := time.Parse(time.RFC3339Nano, observation.FetchedAt)
		recordedAt, recordedErr := time.Parse(time.RFC3339Nano, observation.RecordedAt)
		if observedErr != nil || fetchedErr != nil || recordedErr != nil || fetchedAt.Before(observedAt) || recordedAt.Before(fetchedAt) {
			return nil, internalError(errors.New("security price observation timestamps are inconsistent"))
		}
		times[observation.ObservationID] = [3]time.Time{observedAt, fetchedAt, recordedAt}
	}

	result := &HoldingValuation{
		Scope: "holdings_only", PolicyVersion: holdingValuationPolicy,
		MaxObservationAgeSeconds: int64(holdingPriceMaxAge / time.Second), ValuationAsOf: asOf,
		LedgerRevision: snapshot.LedgerRevision, LedgerAsOf: snapshot.AsOf, LedgerRecordedAt: snapshot.RecordedAt,
		Status: "complete", Lines: []HoldingValuationLine{}, Issues: []APIError{},
	}
	if len(snapshot.Holdings) == 0 {
		result.Status = "empty"
		if err := tx.Commit(); err != nil {
			return nil, internalError(err)
		}
		return result, nil
	}

	costTotals := map[string]*big.Rat{}
	marketTotals := map[string]*big.Rat{}
	pnlTotals := map[string]*big.Rat{}
	available := true
	for _, holding := range snapshot.Holdings {
		line := HoldingValuationLine{
			InstrumentID: holding.InstrumentID, Symbol: holding.Symbol, Quantity: holding.Quantity,
			CostBasis: holding.CostBasis, Currency: holding.Currency,
		}
		observation, selection := latestEligibleHoldingPrice(observations, times, holding, valuationTime)
		if observation == nil {
			line.Status = selection
			line.Issue = holdingPriceIssue(selection, holding.Symbol)
			result.Issues = append(result.Issues, *line.Issue)
			result.Lines = append(result.Lines, line)
			available = false
			continue
		}
		line.Price = publicHoldingValuationPrice(observation)
		result.Sample = true
		if valuationTime.Sub(times[observation.ObservationID][0]) > holdingPriceMaxAge {
			line.Status = "stale"
			line.Issue = holdingPriceIssue("stale", holding.Symbol)
			result.Issues = append(result.Issues, *line.Issue)
			result.Lines = append(result.Lines, line)
			available = false
			continue
		}

		quantity, quantityErr := parseDecimal(holding.Quantity)
		cost, costErr := parseDecimal(holding.CostBasis)
		price, priceErr := parseDecimal(observation.Price)
		if quantityErr != nil || costErr != nil || priceErr != nil {
			return nil, internalError(errors.New("holding valuation decimal is invalid"))
		}
		market := new(big.Rat).Mul(quantity, price)
		pnl := new(big.Rat).Sub(new(big.Rat).Set(market), cost)
		marketText, marketErr := formatDecimal(market)
		pnlText, pnlErr := formatDecimal(pnl)
		if marketErr != nil || pnlErr != nil {
			return nil, internalError(errors.New("holding valuation is not an exact finite decimal"))
		}
		line.Status = "valued"
		line.MarketValue = stringPointer(marketText)
		line.UnrealizedPnL = stringPointer(pnlText)
		addRat(costTotals, holding.Currency, cost)
		addRat(marketTotals, holding.Currency, market)
		addRat(pnlTotals, holding.Currency, pnl)
		result.Lines = append(result.Lines, line)
	}

	if !available {
		result.Status = "unavailable"
	} else {
		for _, currency := range sortedKeys(marketTotals) {
			cost, costErr := formatDecimal(costTotals[currency])
			market, marketErr := formatDecimal(marketTotals[currency])
			pnl, pnlErr := formatDecimal(pnlTotals[currency])
			if costErr != nil || marketErr != nil || pnlErr != nil {
				return nil, internalError(errors.New("holding valuation total is not an exact finite decimal"))
			}
			result.Totals = append(result.Totals, HoldingValuationTotal{currency, cost, market, pnl})
		}
		if result.Sample {
			result.Status = "stale_sample"
			result.Issues = append(result.Issues, APIError{Code: "sample_data", Message: "holding valuation uses local sample security prices"})
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

// ponytail: linear selection is enough for a personal price series; add a projection only after measured volume makes it hot.
func latestEligibleHoldingPrice(observations []SecurityPriceObservation, times map[string][3]time.Time, holding Holding, asOf time.Time) (*SecurityPriceObservation, string) {
	venues := map[string]struct{}{}
	var selected *SecurityPriceObservation
	for index := range observations {
		observation := &observations[index]
		if observation.Source != "local_fixture" || observation.InstrumentID != holding.InstrumentID || observation.Symbol != holding.Symbol ||
			observation.Currency != holding.Currency || observation.PriceAdjustment != marketDataAdjustmentUnspecified {
			continue
		}
		observationTimes := times[observation.ObservationID]
		if observationTimes[0].After(asOf) || observationTimes[1].After(asOf) || observationTimes[2].After(asOf) {
			continue
		}
		venues[observation.Venue] = struct{}{}
		if selected == nil || observationTimes[0].After(times[selected.ObservationID][0]) {
			selected = observation
		}
	}
	if selected == nil {
		return nil, "missing"
	}
	if len(venues) != 1 {
		return nil, "ambiguous"
	}
	return selected, "valued"
}

func holdingPriceIssue(status, symbol string) *APIError {
	switch status {
	case "ambiguous":
		return &APIError{Code: "ambiguous_security_price_venue", Message: "security price venue identity is ambiguous", Field: symbol}
	case "stale":
		return &APIError{Code: "stale_security_price", Message: "the security price observation exceeds the policy age", Field: symbol}
	default:
		return &APIError{Code: "missing_security_price", Message: "an eligible security price observation is unavailable", Field: symbol}
	}
}

func publicHoldingValuationPrice(observation *SecurityPriceObservation) *HoldingValuationPrice {
	return &HoldingValuationPrice{
		ObservationID: observation.ObservationID, Source: observation.Source, Venue: observation.Venue,
		Currency: observation.Currency, Price: observation.Price, PriceAdjustment: observation.PriceAdjustment,
		ObservedAt: observation.ObservedAt, FetchedAt: observation.FetchedAt, RecordedAt: observation.RecordedAt,
		Sample: true, State: "stale",
	}
}
