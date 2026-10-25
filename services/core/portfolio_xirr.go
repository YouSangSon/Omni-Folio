package main

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"net/http"
	"time"
)

const portfolioXIRRPolicy = "restated_external_cash_utc_date_actual_365_v1"
const portfolioXIRROwnerFXPolicy = "restated_external_cash_utc_date_actual_365_v2"

type PortfolioXIRR struct {
	PolicyVersion      string                     `json:"policy_version"`
	SolverPolicy       string                     `json:"solver_policy"`
	Scope              string                     `json:"scope"`
	Valuation          *CurrentPortfolioValuation `json:"valuation"`
	Status             string                     `json:"status"`
	Sample             bool                       `json:"sample"`
	PeriodStart        *string                    `json:"period_start"`
	PeriodEnd          string                     `json:"period_end"`
	ExternalEventCount *int                       `json:"external_event_count"`
	SignChanges        *int                       `json:"sign_changes"`
	Uniqueness         string                     `json:"uniqueness"`
	AnnualRate         *string                    `json:"annual_rate"`
	CandidateRate      *string                    `json:"candidate_rate"`
	NetContributions   *Money                     `json:"net_contributions"`
	Issues             []APIError                 `json:"issues"`
}

func (s *Service) portfolioXIRR(ctx context.Context) (*PortfolioXIRR, *appError) {
	if s == nil || s.db == nil || s.now == nil {
		return nil, internalError(errors.New("XIRR service is unavailable"))
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, internalError(err)
	}
	defer tx.Rollback()
	asOf := s.now().UTC()
	if err := requireSchemaContext(ctx, tx); err != nil {
		return nil, internalError(err)
	}
	if err := proveLedgerReplayState(ctx, tx, false); err != nil {
		return nil, internalError(err)
	}
	inputs, err := readValuationFXInputs(ctx, tx, asOf)
	if err != nil {
		return nil, internalError(err)
	}
	valuation, appErr := s.currentPortfolioValuationTx(ctx, tx, asOf, inputs)
	if appErr != nil {
		return nil, appErr
	}
	result := &PortfolioXIRR{PolicyVersion: portfolioXIRRPolicy, SolverPolicy: xirrSolverPolicy, Scope: "whole_portfolio_since_inception", Valuation: valuation, Status: "unavailable", Sample: valuation.Sample, PeriodEnd: asOf.Format("2006-01-02"), Uniqueness: "not_applicable", Issues: []APIError{}}
	usedOwnerFX := usesOwnerFX(valuation.Issues)
	finish := func() (*PortfolioXIRR, *appError) {
		if result.Sample {
			result.Issues = appendCurrentValuationIssue(result.Issues, APIError{Code: "sample_data", Message: "return inputs include stored local sample observations"})
		}
		if usedOwnerFX {
			result.PolicyVersion = portfolioXIRROwnerFXPolicy
			result.Issues = appendCurrentValuationIssue(result.Issues, ownerFXIssue())
		}
		if err := tx.Commit(); err != nil {
			return nil, internalError(err)
		}
		return result, nil
	}
	if valuation.Status == "empty" {
		result.Status = "empty"
		result.ExternalEventCount = new(int)
		result.SignChanges = new(int)
		return finish()
	}
	if valuation.TotalValue == nil {
		result.Issues = append(result.Issues, APIError{Code: "valuation_unavailable", Message: "a complete current portfolio value is required"})
		return finish()
	}
	// A void restates its original external flow, rather than inventing a withdrawal.
	rows, err := tx.QueryContext(ctx, `SELECT e.occurred_at,e.currency,e.amount FROM events e
		WHERE e.type IN ('DEPOSIT','WITHDRAWAL') AND NOT EXISTS
		(SELECT 1 FROM events correction WHERE correction.account_id=e.account_id AND correction.corrects_source_event_id=e.source_event_id)
		ORDER BY rtrim(e.occurred_at,'Z'),e.sequence`)
	if err != nil {
		return nil, internalError(err)
	}
	type externalFlow struct {
		at               time.Time
		currency, amount string
	}
	var external []externalFlow
	for rows.Next() {
		var rawAt, currency, amount string
		if err := rows.Scan(&rawAt, &currency, &amount); err != nil {
			rows.Close()
			return nil, internalError(err)
		}
		at, err := time.Parse(time.RFC3339Nano, rawAt)
		if err != nil {
			rows.Close()
			return nil, internalError(err)
		}
		external = append(external, externalFlow{at, currency, amount})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, internalError(err)
	}
	if err := rows.Close(); err != nil {
		return nil, internalError(err)
	}
	eventCount := len(external)
	result.ExternalEventCount = &eventCount
	observations, fxTimes := inputs.observations, inputs.times
	flows := make([]xirrCashFlow, 0, len(external)+1)
	contributions := new(big.Rat)
	for _, flow := range external {
		if err := ctx.Err(); err != nil {
			return nil, internalError(err)
		}
		amount, err := parseDecimal(flow.amount)
		if err != nil {
			return nil, internalError(err)
		}
		if result.PeriodStart == nil {
			day := flow.at.Format("2006-01-02")
			result.PeriodStart = &day
		}
		if flow.currency != "KRW" {
			fx := historicalXIRRFX(observations, fxTimes, flow.currency, flow.at, asOf)
			if fx == nil {
				result.Issues = appendCurrentValuationIssue(result.Issues, APIError{Code: "missing_historical_fx", Message: "an eligible historical direct FX observation is required", Field: flow.currency})
				continue
			}
			result.Sample = result.Sample || fxIsSample(fx)
			usedOwnerFX = usedOwnerFX || fx.Source == ownerFXSource
			if flow.at.Sub(fxTimes[fxTimeKey(*fx)][0]) > cashFXMaxAge {
				result.Issues = appendCurrentValuationIssue(result.Issues, APIError{Code: "stale_historical_fx", Message: "historical FX exceeds the event-time age policy", Field: flow.currency})
				continue
			}
			rate, err := parseDecimal(fx.Rate)
			if err != nil {
				return nil, internalError(err)
			}
			amount.Mul(amount, rate)
		}
		contributions.Add(contributions, amount)
		flows = append(flows, xirrCashFlow{xirrUTCDay(flow.at), new(big.Rat).Neg(amount)})
	}
	if len(result.Issues) > 0 {
		return finish()
	}
	text, err := formatDecimal(contributions)
	if err != nil {
		return nil, internalError(err)
	}
	result.NetContributions = &Money{Currency: "KRW", Amount: text}
	terminal, err := parseDecimal(valuation.TotalValue.Amount)
	if err != nil {
		return nil, internalError(err)
	}
	flows = append(flows, xirrCashFlow{xirrUTCDay(asOf), terminal})
	solution, err := solvePortfolioXIRR(ctx, flows)
	if err != nil {
		return nil, internalError(err)
	}
	if solution.reason != "cash_flow_limit" {
		changes := solution.signChanges
		result.SignChanges = &changes
	}
	result.AnnualRate = solution.rate
	result.CandidateRate = solution.candidate
	if solution.rate != nil {
		result.Status = "calculated"
		result.Uniqueness = "unique"
	}
	if solution.candidate != nil {
		result.Status = "candidate"
		result.Uniqueness = "not_proven"
	}
	if solution.reason != "" {
		result.Issues = append(result.Issues, APIError{Code: solution.reason, Message: "the numerical result is limited by the declared XIRR policy"})
	}
	return finish()
}

func xirrUTCDay(at time.Time) int64 {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC).Unix() / 86400
}

func historicalXIRRFX(observations []FXObservation, times map[string][3]time.Time, currency string, occurredAt, knownAt time.Time) *FXObservation {
	var selected *FXObservation
	for i := range observations {
		fx := &observations[i]
		if !supportedValuationFX(*fx) || fx.BaseCurrency != currency || fx.QuoteCurrency != "KRW" {
			continue
		}
		clock := times[fxTimeKey(*fx)]
		if clock[0].After(occurredAt) || clock[1].After(knownAt) || clock[2].After(knownAt) {
			continue
		}
		// Equal economic timestamps retain the first verified append, matching valuation selection.
		if selected == nil || clock[0].After(times[fxTimeKey(*selected)][0]) {
			selected = fx
		}
	}
	return selected
}

func (s *Service) handlePortfolioXIRR(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "portfolio XIRR does not accept query parameters"}})
		return
	}
	report, appErr := s.portfolioXIRR(r.Context())
	if appErr != nil {
		writeError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
