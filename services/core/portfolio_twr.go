package main

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"net/http"
	"time"
)

const portfolioTWRPolicy = "restated_external_first_exact_timestamp_twr_v1"
const portfolioTWROwnerFXPolicy = "restated_external_first_exact_timestamp_twr_v2"
const portfolioTWROwnerPricePolicy = "restated_external_first_exact_timestamp_twr_v3"

type PortfolioTWR struct {
	PolicyVersion      string                     `json:"policy_version"`
	Scope              string                     `json:"scope"`
	Valuation          *CurrentPortfolioValuation `json:"valuation"`
	Status             string                     `json:"status"`
	Sample             bool                       `json:"sample"`
	PeriodStart        *string                    `json:"period_start"`
	PeriodEnd          string                     `json:"period_end"`
	ExternalEventCount *int                       `json:"external_event_count"`
	BoundaryCount      *int                       `json:"boundary_count"`
	CumulativeRate     *string                    `json:"cumulative_rate"`
	NetContributions   *Money                     `json:"net_contributions"`
	Issues             []APIError                 `json:"issues"`
}

func (s *Service) portfolioTWR(ctx context.Context) (*PortfolioTWR, *appError) {
	if s == nil || s.db == nil || s.now == nil {
		return nil, internalError(errors.New("TWR service unavailable"))
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
	result, appErr := s.portfolioTWRTx(ctx, tx, asOf, inputs, prices)
	if appErr != nil {
		return nil, appErr
	}
	if err := tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return result, nil
}

func (s *Service) portfolioTWRTx(ctx context.Context, tx *sql.Tx, asOf time.Time, inputs valuationFXInputs, prices valuationPriceInputs) (*PortfolioTWR, *appError) {
	if err := requireSchemaContext(ctx, tx); err != nil {
		return nil, internalError(err)
	}
	if err := proveLedgerReplayState(ctx, tx, false); err != nil {
		return nil, internalError(err)
	}
	valuation, appErr := s.currentPortfolioValuationTx(ctx, tx, asOf, inputs, prices)
	if appErr != nil {
		return nil, appErr
	}
	r := &PortfolioTWR{PolicyVersion: portfolioTWRPolicy, Scope: "whole_portfolio_since_inception", Valuation: valuation, Status: "unavailable", Sample: valuation.Sample, PeriodEnd: asOf.Format(time.RFC3339Nano), Issues: []APIError{}}
	marks := twrMarks{fx: inputs.observations, fxTimes: inputs.times, prices: prices.observations, priceTimes: prices.times}
	finish := func(issue string) (*PortfolioTWR, *appError) {
		if err := ctx.Err(); err != nil {
			return nil, internalError(err)
		}
		if issue != "" {
			r.Issues = append(r.Issues, APIError{Code: issue, Message: "a required input does not meet the declared TWR policy"})
		}
		if r.Sample {
			r.Issues = append(r.Issues, APIError{Code: "sample_data", Message: "return inputs include stored local sample observations"})
		}
		if usesOwnerFX(valuation.Issues) || marks.ownerUsed {
			r.PolicyVersion = portfolioTWROwnerFXPolicy
			r.Issues = append(r.Issues, ownerFXIssue())
		}
		if usesOwnerPrice(valuation.Issues) || marks.ownerPriceUsed {
			r.PolicyVersion = portfolioTWROwnerPricePolicy
			r.Issues = append(r.Issues, ownerPriceIssue())
		}
		return r, nil
	}
	if valuation.Status == "empty" {
		r.Status = "empty"
		r.ExternalEventCount = new(int)
		r.BoundaryCount = new(int)
		return finish("")
	}
	if valuation.TotalValue == nil {
		return finish("valuation_unavailable")
	}
	// Restate both external and internal cash corrections in every historical balance.
	rows, err := tx.QueryContext(ctx, `SELECT e.type,e.occurred_at,COALESCE(e.instrument_id,''),COALESCE(e.symbol,''),COALESCE(e.quantity,''),e.currency,e.amount,COALESCE(e.counter_currency,''),COALESCE(e.counter_amount,'') FROM events e
		WHERE e.type<>'CASH_VOID' AND NOT EXISTS (SELECT 1 FROM events c WHERE c.type='CASH_VOID' AND c.account_id=e.account_id AND c.corrects_source_event_id=e.source_event_id)
		ORDER BY rtrim(e.occurred_at,'Z'),e.sequence`)
	if err != nil {
		return nil, internalError(err)
	}
	events := []Transaction{}
	for rows.Next() {
		var e Transaction
		if err := rows.Scan(&e.Type, &e.OccurredAt, &e.InstrumentID, &e.Symbol, &e.Quantity, &e.Currency, &e.Amount, &e.CounterCurrency, &e.CounterAmount); err != nil {
			rows.Close()
			return nil, internalError(err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, internalError(err)
	}
	if err := rows.Close(); err != nil {
		return nil, internalError(err)
	}
	count, boundaries := 0, 0
	last := ""
	for _, e := range events {
		if e.Type != "DEPOSIT" && e.Type != "WITHDRAWAL" {
			continue
		}
		count++
		if r.PeriodStart == nil {
			r.PeriodStart = stringPointer(e.OccurredAt)
		}
		if e.OccurredAt != last {
			boundaries++
			last = e.OccurredAt
		}
	}
	r.ExternalEventCount, r.BoundaryCount = &count, &boundaries
	if count == 0 {
		return finish("no_external_cash_flows")
	}
	// This bounds return calculation, not the required full-ledger integrity proof.
	if boundaries > 1000 {
		return finish("cash_flow_limit")
	}
	b := newTWRBalances()
	factor := big.NewRat(1, 1)
	var capital *big.Rat
	net := new(big.Rat)
	for i := 0; i < len(events); {
		if err := ctx.Err(); err != nil {
			return nil, internalError(err)
		}
		j := i + 1
		for j < len(events) && events[j].OccurredAt == events[i].OccurredAt {
			j++
		}
		at, err := time.Parse(time.RFC3339Nano, events[i].OccurredAt)
		if err != nil {
			return nil, internalError(err)
		}
		hasFlow, hasSplit := false, false
		for _, e := range events[i:j] {
			hasFlow = hasFlow || e.Type == "DEPOSIT" || e.Type == "WITHDRAWAL"
			hasSplit = hasSplit || e.Type == "SPLIT"
		}
		if hasFlow {
			if hasSplit {
				return finish("ambiguous_split_boundary")
			}
			before, used, issue := marks.value(ctx, b, at, asOf)
			r.Sample = r.Sample || used
			if issue != "" {
				return finish(issue)
			}
			if capital == nil {
				if !b.zero() {
					return finish("capital_path_unavailable")
				}
			} else {
				if before.Sign() <= 0 {
					return finish("capital_path_unavailable")
				}
				factor = twrChain(factor, before, capital)
				if factor == nil {
					return finish("numerical_limit")
				}
			}
			flow := new(big.Rat)
			for _, e := range events[i:j] {
				if e.Type != "DEPOSIT" && e.Type != "WITHDRAWAL" {
					continue
				}
				amount, err := parseDecimal(e.Amount)
				if err != nil {
					return nil, internalError(err)
				}
				v, used, issue := marks.convert(amount, e.Currency, at, asOf)
				r.Sample = r.Sample || used
				if issue != "" {
					return finish(issue)
				}
				flow.Add(flow, v)
				if err := b.apply(e); err != nil {
					return nil, internalError(err)
				}
			}
			net.Add(net, flow)
			capital = new(big.Rat).Add(before, flow)
			if capital.Sign() <= 0 {
				return finish("capital_path_unavailable")
			}
		}
		// External capital precedes internal events at the same exact timestamp.
		for _, e := range events[i:j] {
			if e.Type == "DEPOSIT" || e.Type == "WITHDRAWAL" {
				continue
			}
			if err := b.apply(e); err != nil {
				return nil, internalError(err)
			}
		}
		i = j
	}
	snapshot, err := snapshotFrom(ctx, tx)
	if err != nil {
		return nil, internalError(err)
	}
	if !b.matches(snapshot) {
		return nil, internalError(errors.New("TWR projection differs from proved ledger balances"))
	}
	terminal, used, issue := marks.value(ctx, b, asOf, asOf)
	r.Sample = r.Sample || used
	if issue != "" {
		return finish(issue)
	}
	current, err := parseDecimal(valuation.TotalValue.Amount)
	if err != nil || terminal.Cmp(current) != 0 {
		return nil, internalError(errors.New("TWR terminal valuation mismatch"))
	}
	if terminal.Sign() < 0 {
		return finish("capital_path_unavailable")
	}
	factor = twrChain(factor, terminal, capital)
	if factor == nil {
		return finish("numerical_limit")
	}
	rate, err := formatDecimal(quantizeHalfEven(new(big.Rat).Sub(factor, big.NewRat(1, 1)), 12))
	if err != nil || len(rate) > 128 {
		return finish("numerical_limit")
	}
	netText, err := formatDecimal(net)
	if err != nil {
		return nil, internalError(err)
	}
	r.Status = "calculated"
	r.CumulativeRate = &rate
	r.NetContributions = &Money{Currency: "KRW", Amount: netText}
	return finish("")
}

func (s *Service) handlePortfolioTWR(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "portfolio TWR does not accept query parameters"}})
		return
	}
	report, appErr := s.portfolioTWR(r.Context())
	if appErr != nil {
		writeError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
