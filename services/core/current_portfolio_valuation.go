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
	currentPortfolioValuationPolicy           = "current_base_portfolio_valuation_v1"
	currentPortfolioValuationOwnerFXPolicy    = "current_base_portfolio_valuation_v2"
	currentPortfolioValuationOwnerPricePolicy = "current_base_portfolio_valuation_v3"
	currentPortfolioValuationBaseCurrency     = "KRW"
)

type CurrentPortfolioValuation struct {
	Scope                    string     `json:"scope"`
	PolicyVersion            string     `json:"policy_version"`
	MaxObservationAgeSeconds int64      `json:"max_observation_age_seconds"`
	BaseCurrency             string     `json:"base_currency"`
	ValuationAsOf            string     `json:"valuation_as_of"`
	LedgerRevision           string     `json:"ledger_revision"`
	LedgerAsOf               string     `json:"ledger_as_of"`
	LedgerRecordedAt         string     `json:"ledger_recorded_at"`
	Status                   string     `json:"status"`
	Sample                   bool       `json:"sample"`
	CashValue                *Money     `json:"cash_value"`
	HoldingsValue            *Money     `json:"holdings_value"`
	TotalValue               *Money     `json:"total_value"`
	Issues                   []APIError `json:"issues"`
}

func (s *Service) currentPortfolioValuation(ctx context.Context) (*CurrentPortfolioValuation, *appError) {
	if s == nil || s.db == nil || s.now == nil {
		return nil, internalError(errors.New("current portfolio valuation service is unavailable"))
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, internalError(err)
	}
	defer tx.Rollback()

	asOf := s.now().UTC()
	inputs, err := readValuationFXInputs(ctx, tx, asOf)
	if err != nil {
		return nil, internalError(err)
	}
	prices, err := readValuationPriceInputs(ctx, tx, asOf)
	if err != nil {
		return nil, internalError(err)
	}
	result, appErr := s.currentPortfolioValuationTx(ctx, tx, asOf, inputs, prices)
	if appErr != nil {
		return nil, appErr
	}
	if err := tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

func (s *Service) currentPortfolioValuationTx(ctx context.Context, tx *sql.Tx, valuationTime time.Time, inputs valuationFXInputs, prices valuationPriceInputs) (*CurrentPortfolioValuation, *appError) {
	if inputs.tx != tx || !inputs.knownAt.Equal(valuationTime) {
		return nil, internalError(errors.New("FX inputs do not belong to the valuation observation"))
	}
	if prices.tx != tx || !prices.knownAt.Equal(valuationTime) {
		return nil, internalError(errors.New("price inputs do not belong to the valuation observation"))
	}
	// The server owns one cutoff inside the same read transaction as every required input.
	valuationAsOf := valuationTime.Format(time.RFC3339Nano)
	snapshot, err := snapshotFrom(ctx, tx)
	if err != nil {
		return nil, internalError(err)
	}
	ledgerRevision, _, err := s.proveLedgerEvents(ctx, tx)
	if err != nil {
		return nil, internalError(err)
	}
	if revision(ledgerRevision) != snapshot.LedgerRevision {
		return nil, internalError(errors.New("ledger snapshot proof mismatch"))
	}
	// Keep legacy snapshot/backup replay unchanged; this read contract needs the exact event cutoff.
	ledgerAsOfRaw := zeroTime
	err = tx.QueryRowContext(ctx, `SELECT occurred_at FROM events ORDER BY rtrim(occurred_at,'Z') DESC,sequence DESC LIMIT 1`).Scan(&ledgerAsOfRaw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, internalError(err)
	}
	ledgerAsOf, ledgerAsOfErr := time.Parse(time.RFC3339Nano, ledgerAsOfRaw)
	ledgerRecordedAt, ledgerRecordedAtErr := time.Parse(time.RFC3339Nano, snapshot.RecordedAt)
	if ledgerAsOfErr != nil || ledgerRecordedAtErr != nil || !canonicalUTCString(ledgerAsOfRaw) || !canonicalUTCString(snapshot.RecordedAt) {
		return nil, internalError(errors.New("ledger snapshot timestamps are invalid"))
	}
	// Import metadata and append order need not follow event recording clocks.
	var futureRecord bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE sequence<=? AND rtrim(recorded_at,'Z')>rtrim(?,'Z'))`, ledgerRevision, valuationAsOf).Scan(&futureRecord); err != nil {
		return nil, internalError(err)
	}
	if valuationTime.Before(ledgerAsOf) || valuationTime.Before(ledgerRecordedAt) || futureRecord {
		return nil, internalError(errors.New("server valuation cutoff precedes ledger state"))
	}

	fxObservations, fxTimes := inputs.observations, inputs.times
	priceObservations, priceTimes := prices.observations, prices.times

	result := &CurrentPortfolioValuation{
		Scope: "whole_portfolio", PolicyVersion: currentPortfolioValuationPolicy,
		MaxObservationAgeSeconds: int64(cashFXMaxAge / time.Second),
		BaseCurrency:             currentPortfolioValuationBaseCurrency, ValuationAsOf: valuationAsOf,
		LedgerRevision: snapshot.LedgerRevision, LedgerAsOf: ledgerAsOfRaw, LedgerRecordedAt: snapshot.RecordedAt,
		Status: "complete", Issues: []APIError{},
	}
	if len(snapshot.Cash) == 0 && len(snapshot.Holdings) == 0 {
		result.Status = "empty"
		return result, nil
	}

	cashTotal := new(big.Rat)
	holdingsTotal := new(big.Rat)
	available := true
	usedSample := false
	usedOwnerFX := false
	usedOwnerPrice := false
	for _, cash := range snapshot.Cash {
		amount, err := parseDecimal(cash.Amount)
		if err != nil {
			return nil, internalError(errors.New("cash amount is invalid"))
		}
		if amount.Sign() == 0 || cash.Currency == currentPortfolioValuationBaseCurrency {
			cashTotal.Add(cashTotal, amount)
			continue
		}
		observation := latestEligibleCashFX(fxObservations, fxTimes, cash.Currency, currentPortfolioValuationBaseCurrency, valuationTime)
		if observation == nil {
			result.Issues = appendCurrentValuationIssue(result.Issues, APIError{
				Code: "missing_direct_fx", Message: "an eligible direct FX observation is unavailable", Field: cash.Currency,
			})
			available = false
			continue
		}
		usedSample = usedSample || fxIsSample(observation)
		usedOwnerFX = usedOwnerFX || observation.Source == ownerFXSource
		if valuationTime.Sub(fxTimes[fxTimeKey(*observation)][0]) > cashFXMaxAge {
			result.Issues = appendCurrentValuationIssue(result.Issues, APIError{
				Code: "stale_direct_fx", Message: "the direct FX observation exceeds the policy age", Field: cash.Currency,
			})
			available = false
			continue
		}
		rate, err := parseDecimal(observation.Rate)
		if err != nil {
			return nil, internalError(errors.New("FX rate is invalid"))
		}
		cashTotal.Add(cashTotal, new(big.Rat).Mul(amount, rate))
	}

	for _, holding := range snapshot.Holdings {
		observation, selection := latestEligibleHoldingPrice(priceObservations, priceTimes, holding, valuationTime)
		if observation == nil {
			result.Issues = appendCurrentValuationIssue(result.Issues, *holdingPriceIssue(selection, holding.Symbol))
			available = false
			continue
		}
		usedSample = usedSample || priceIsSample(observation)
		usedOwnerPrice = usedOwnerPrice || observation.Source == ownerPriceSource
		if !prices.afterLastSplit(observation) {
			result.Issues = appendCurrentValuationIssue(result.Issues, *holdingPriceIssue("split_basis", holding.Symbol))
			available = false
			continue
		}
		if valuationTime.Sub(priceTimes[priceTimeKey(*observation)][0]) > holdingPriceMaxAge {
			result.Issues = appendCurrentValuationIssue(result.Issues, *holdingPriceIssue("stale", holding.Symbol))
			available = false
			continue
		}
		quantity, quantityErr := parseDecimal(holding.Quantity)
		price, priceErr := parseDecimal(observation.Price)
		if quantityErr != nil || priceErr != nil {
			return nil, internalError(errors.New("holding valuation decimal is invalid"))
		}
		marketValue := new(big.Rat).Mul(quantity, price)
		if holding.Currency != currentPortfolioValuationBaseCurrency {
			fx := latestEligibleCashFX(fxObservations, fxTimes, holding.Currency, currentPortfolioValuationBaseCurrency, valuationTime)
			if fx == nil {
				result.Issues = appendCurrentValuationIssue(result.Issues, APIError{
					Code: "missing_direct_fx", Message: "an eligible direct FX observation is unavailable", Field: holding.Currency,
				})
				available = false
				continue
			}
			usedSample = usedSample || fxIsSample(fx)
			usedOwnerFX = usedOwnerFX || fx.Source == ownerFXSource
			if valuationTime.Sub(fxTimes[fxTimeKey(*fx)][0]) > cashFXMaxAge {
				result.Issues = appendCurrentValuationIssue(result.Issues, APIError{
					Code: "stale_direct_fx", Message: "the direct FX observation exceeds the policy age", Field: holding.Currency,
				})
				available = false
				continue
			}
			rate, err := parseDecimal(fx.Rate)
			if err != nil {
				return nil, internalError(errors.New("FX rate is invalid"))
			}
			marketValue.Mul(marketValue, rate)
		}
		holdingsTotal.Add(holdingsTotal, marketValue)
	}

	result.Sample = usedSample
	if usedOwnerFX {
		result.PolicyVersion = currentPortfolioValuationOwnerFXPolicy
		result.Issues = append(result.Issues, ownerFXIssue())
	}
	if usedOwnerPrice {
		result.PolicyVersion = currentPortfolioValuationOwnerPricePolicy
		result.Issues = append(result.Issues, ownerPriceIssue())
	}
	if !available {
		result.Status = "unavailable"
	} else {
		cashText, cashErr := formatDecimal(cashTotal)
		holdingsText, holdingsErr := formatDecimal(holdingsTotal)
		totalText, totalErr := formatDecimal(new(big.Rat).Add(cashTotal, holdingsTotal))
		if cashErr != nil || holdingsErr != nil || totalErr != nil {
			return nil, internalError(errors.New("current portfolio valuation is not an exact finite decimal"))
		}
		result.CashValue = &Money{Currency: currentPortfolioValuationBaseCurrency, Amount: cashText}
		result.HoldingsValue = &Money{Currency: currentPortfolioValuationBaseCurrency, Amount: holdingsText}
		result.TotalValue = &Money{Currency: currentPortfolioValuationBaseCurrency, Amount: totalText}
		if usedSample {
			result.Status = "stale_sample"
			result.Issues = append(result.Issues, APIError{
				Code: "sample_data", Message: "portfolio valuation uses stored local sample prices or FX data",
			})
		}
	}
	return result, nil
}

func currentFXObservationTimes(observations []FXObservation) (map[string][3]time.Time, error) {
	times := make(map[string][3]time.Time, len(observations))
	for _, observation := range observations {
		parsed, err := currentObservationTimes(observation.ObservedAt, observation.FetchedAt, observation.RecordedAt)
		if err != nil {
			return nil, errors.New("FX observation timestamps are inconsistent")
		}
		times[fxTimeKey(observation)] = parsed
	}
	return times, nil
}

func currentPriceObservationTimes(observations []SecurityPriceObservation) (map[string][3]time.Time, error) {
	times := make(map[string][3]time.Time, len(observations))
	for _, observation := range observations {
		parsed, err := currentObservationTimes(observation.ObservedAt, observation.FetchedAt, observation.RecordedAt)
		if err != nil {
			return nil, errors.New("security price observation timestamps are inconsistent")
		}
		times[priceTimeKey(observation)] = parsed
	}
	return times, nil
}

func currentObservationTimes(observedRaw, fetchedRaw, recordedRaw string) ([3]time.Time, error) {
	observed, observedErr := time.Parse(time.RFC3339Nano, observedRaw)
	fetched, fetchedErr := time.Parse(time.RFC3339Nano, fetchedRaw)
	recorded, recordedErr := time.Parse(time.RFC3339Nano, recordedRaw)
	if observedErr != nil || fetchedErr != nil || recordedErr != nil || fetched.Before(observed) || recorded.Before(fetched) {
		return [3]time.Time{}, errors.New("observation timestamps are inconsistent")
	}
	return [3]time.Time{observed, fetched, recorded}, nil
}

func appendCurrentValuationIssue(issues []APIError, candidate APIError) []APIError {
	for _, issue := range issues {
		if issue.Code == candidate.Code && issue.Field == candidate.Field {
			return issues
		}
	}
	return append(issues, candidate)
}

func (s *Service) handleCurrentPortfolioValuation(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, &appError{http.StatusBadRequest, APIError{
			Code: "invalid_query", Message: "current portfolio valuation does not accept query parameters",
		}})
		return
	}
	valuation, appErr := s.currentPortfolioValuation(r.Context())
	if appErr != nil {
		writeError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, valuation)
}
