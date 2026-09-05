package main

import (
	"net/http"
	"net/url"
	"time"
)

func (s *Service) handleHoldingValuation(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) > 1 || (len(query) == 1 && len(query["as_of"]) != 1) {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "holding valuation query is invalid"}})
		return
	}
	asOf := s.now().UTC().Format(time.RFC3339Nano)
	if values, ok := query["as_of"]; ok {
		asOf = values[0]
	}
	valuation, appErr := s.holdingValuation(r.Context(), asOf)
	if appErr != nil {
		writeError(w, appErr)
		return
	}
	// Whitelist the public view; durable instrument and observation IDs stay internal.
	lines := make([]map[string]any, 0, len(valuation.Lines))
	for _, line := range valuation.Lines {
		var price any
		if p := line.Price; p != nil {
			price = map[string]any{
				"source": p.Source, "venue": p.Venue, "currency": p.Currency,
				"price": p.Price, "price_adjustment": p.PriceAdjustment,
				"observed_at": p.ObservedAt, "fetched_at": p.FetchedAt, "recorded_at": p.RecordedAt,
				"sample": p.Sample, "state": p.State,
			}
		}
		lines = append(lines, map[string]any{
			"symbol": line.Symbol, "quantity": line.Quantity, "cost_basis": line.CostBasis,
			"currency": line.Currency, "status": line.Status, "price": price,
			"market_value": line.MarketValue, "unrealized_pnl": line.UnrealizedPnL, "issue": line.Issue,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope": valuation.Scope, "policy_version": valuation.PolicyVersion,
		"max_observation_age_seconds": valuation.MaxObservationAgeSeconds,
		"valuation_as_of":             valuation.ValuationAsOf, "ledger_revision": valuation.LedgerRevision,
		"ledger_as_of": valuation.LedgerAsOf, "ledger_recorded_at": valuation.LedgerRecordedAt,
		"status": valuation.Status, "sample": valuation.Sample, "totals": valuation.Totals,
		"lines": lines, "issues": valuation.Issues,
	})
}
