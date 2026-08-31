package main

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"time"
)

const (
	cashValuationPolicy = "direct_fx_cash_v1"
	cashFXMaxAge        = 24 * time.Hour
)

type CashValuationFX struct {
	ObservationID string `json:"observation_id"`
	Source        string `json:"source"`
	BaseCurrency  string `json:"base_currency"`
	QuoteCurrency string `json:"quote_currency"`
	Rate          string `json:"rate"`
	ObservedAt    string `json:"observed_at"`
	FetchedAt     string `json:"fetched_at"`
	RecordedAt    string `json:"recorded_at"`
	Sample        bool   `json:"sample"`
	State         string `json:"state"`
}

type CashValuationLine struct {
	Currency     string           `json:"currency"`
	Amount       string           `json:"amount"`
	Status       string           `json:"status"`
	ValuedAmount *string          `json:"valued_amount"`
	FX           *CashValuationFX `json:"fx"`
	Issue        *APIError        `json:"issue"`
}

type CashValuation struct {
	Scope                    string              `json:"scope"`
	PolicyVersion            string              `json:"policy_version"`
	MaxObservationAgeSeconds int64               `json:"max_observation_age_seconds"`
	BaseCurrency             string              `json:"base_currency"`
	ValuationAsOf            string              `json:"valuation_as_of"`
	LedgerRevision           string              `json:"ledger_revision"`
	LedgerAsOf               string              `json:"ledger_as_of"`
	LedgerRecordedAt         string              `json:"ledger_recorded_at"`
	Status                   string              `json:"status"`
	Sample                   bool                `json:"sample"`
	Total                    *Money              `json:"total"`
	Lines                    []CashValuationLine `json:"lines"`
	Issues                   []APIError          `json:"issues"`
}

func (s *Service) cashValuation(ctx context.Context, baseCurrency, asOf string) (*CashValuation, *appError) {
	if !currencyCodePattern.MatchString(baseCurrency) || !canonicalUTCString(asOf) {
		return nil, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "cash valuation query values are invalid"}}
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
	observations, _, err := replayFXObservations(ctx, tx)
	if err != nil {
		return nil, internalError(err)
	}
	ledgerAsOf, ledgerAsOfErr := time.Parse(time.RFC3339Nano, snapshot.AsOf)
	ledgerRecordedAt, ledgerRecordedAtErr := time.Parse(time.RFC3339Nano, snapshot.RecordedAt)
	if ledgerAsOfErr != nil || ledgerRecordedAtErr != nil || !canonicalUTCString(snapshot.AsOf) || !canonicalUTCString(snapshot.RecordedAt) {
		return nil, internalError(errors.New("ledger snapshot timestamps are invalid"))
	}
	if valuationTime.Before(ledgerAsOf) || valuationTime.Before(ledgerRecordedAt) {
		return nil, &appError{http.StatusConflict, APIError{Code: "valuation_as_of_precedes_ledger_revision", Message: "valuation as_of precedes the current ledger state", Field: "as_of"}}
	}
	times := make(map[string][3]time.Time, len(observations))
	for _, observation := range observations {
		observedAt, observedErr := time.Parse(time.RFC3339Nano, observation.ObservedAt)
		fetchedAt, fetchedErr := time.Parse(time.RFC3339Nano, observation.FetchedAt)
		recordedAt, recordedErr := time.Parse(time.RFC3339Nano, observation.RecordedAt)
		if observedErr != nil || fetchedErr != nil || recordedErr != nil || fetchedAt.Before(observedAt) || recordedAt.Before(fetchedAt) {
			return nil, internalError(errors.New("FX observation timestamps are inconsistent"))
		}
		times[observation.ObservationID] = [3]time.Time{observedAt, fetchedAt, recordedAt}
	}

	result := &CashValuation{
		Scope: "cash_only", PolicyVersion: cashValuationPolicy, MaxObservationAgeSeconds: int64(cashFXMaxAge / time.Second),
		BaseCurrency: baseCurrency, ValuationAsOf: asOf, LedgerRevision: snapshot.LedgerRevision,
		LedgerAsOf: snapshot.AsOf, LedgerRecordedAt: snapshot.RecordedAt, Status: "complete",
		Lines: []CashValuationLine{}, Issues: []APIError{},
	}
	if len(snapshot.Cash) == 0 {
		result.Status = "empty"
		if err := tx.Commit(); err != nil {
			return nil, internalError(err)
		}
		return result, nil
	}

	total := new(big.Rat)
	available, usedSample := true, false
	for _, cash := range snapshot.Cash {
		amount, err := parseDecimal(cash.Amount)
		if err != nil {
			return nil, internalError(err)
		}
		line := CashValuationLine{Currency: cash.Currency, Amount: cash.Amount}
		switch {
		case amount.Sign() == 0:
			line.Status = "zero"
			line.ValuedAmount = stringPointer("0")
		case cash.Currency == baseCurrency:
			line.Status = "identity"
			line.ValuedAmount = stringPointer(cash.Amount)
			total.Add(total, amount)
		default:
			observation := latestEligibleCashFX(observations, times, cash.Currency, baseCurrency, valuationTime)
			if observation == nil {
				line.Status = "missing"
				line.Issue = &APIError{Code: "missing_direct_fx", Message: "an eligible direct FX observation is unavailable", Field: cash.Currency}
				result.Issues = append(result.Issues, *line.Issue)
				available = false
				break
			}
			line.FX = publicCashValuationFX(observation)
			usedSample = true
			if valuationTime.Sub(times[observation.ObservationID][0]) > cashFXMaxAge {
				line.Status = "stale"
				line.Issue = &APIError{Code: "stale_direct_fx", Message: "the direct FX observation exceeds the policy age", Field: cash.Currency}
				result.Issues = append(result.Issues, *line.Issue)
				available = false
				break
			}
			rate, err := parseDecimal(observation.Rate)
			if err != nil {
				return nil, internalError(err)
			}
			valued := new(big.Rat).Mul(amount, rate)
			valuedText, err := formatDecimal(valued)
			if err != nil {
				return nil, internalError(err)
			}
			line.Status = "valued"
			line.ValuedAmount = stringPointer(valuedText)
			total.Add(total, valued)
		}
		result.Lines = append(result.Lines, line)
	}
	result.Sample = usedSample
	if !available {
		result.Status = "unavailable"
	} else {
		totalText, err := formatDecimal(total)
		if err != nil {
			return nil, internalError(err)
		}
		result.Total = &Money{Currency: baseCurrency, Amount: totalText}
		if usedSample {
			result.Status = "stale_sample"
			result.Issues = append(result.Issues, APIError{Code: "sample_data", Message: "cash valuation uses local sample FX data"})
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

// ponytail: linear selection is enough for a personal FX series; index-backed projection comes after measured volume.
func latestEligibleCashFX(observations []FXObservation, times map[string][3]time.Time, base, quote string, asOf time.Time) *FXObservation {
	var selected *FXObservation
	for index := range observations {
		observation := &observations[index]
		if observation.Source != "local_fixture" || observation.BaseCurrency != base || observation.QuoteCurrency != quote {
			continue
		}
		observationTimes := times[observation.ObservationID]
		if observationTimes[0].After(asOf) || observationTimes[1].After(asOf) || observationTimes[2].After(asOf) {
			continue
		}
		if selected == nil || observationTimes[0].After(times[selected.ObservationID][0]) {
			selected = observation
		}
	}
	return selected
}

func publicCashValuationFX(observation *FXObservation) *CashValuationFX {
	return &CashValuationFX{
		ObservationID: observation.ObservationID, Source: observation.Source, BaseCurrency: observation.BaseCurrency,
		QuoteCurrency: observation.QuoteCurrency, Rate: observation.Rate, ObservedAt: observation.ObservedAt,
		FetchedAt: observation.FetchedAt, RecordedAt: observation.RecordedAt, Sample: true, State: "stale",
	}
}

func stringPointer(value string) *string { return &value }

func (s *Service) handleCashValuation(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "query parameters are invalid"}})
		return
	}
	for key := range query {
		if key != "base_currency" && key != "as_of" {
			writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "unknown query parameter " + key, Field: key}})
			return
		}
	}
	for _, key := range []string{"base_currency", "as_of"} {
		if len(query[key]) != 1 || query[key][0] == "" {
			writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: key + " query parameter must appear exactly once", Field: key}})
			return
		}
	}
	valuation, appErr := s.cashValuation(r.Context(), query.Get("base_currency"), query.Get("as_of"))
	if appErr != nil {
		writeError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, valuation)
}
