package main

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"unicode/utf8"

	"omni-folio/services/core/internal/exact"
)

const instrumentAverageCostPolicy = "remaining_fifo_cost_per_unit_half_even_8_v1"

type InstrumentLedger struct {
	InstrumentID      string           `json:"instrument_id"`
	Symbol            string           `json:"symbol"`
	Currency          string           `json:"currency"`
	Source            string           `json:"source"`
	BrokerFreshness   string           `json:"broker_freshness"`
	LedgerRevision    string           `json:"ledger_revision"`
	LedgerAsOf        string           `json:"ledger_as_of"`
	LedgerRecordedAt  string           `json:"ledger_recorded_at"`
	CostBasisPolicy   string           `json:"cost_basis_policy"`
	AverageCostPolicy string           `json:"average_cost_policy"`
	Quantity          string           `json:"quantity"`
	CostBasis         string           `json:"cost_basis"`
	AverageCost       *string          `json:"average_cost"`
	TradeLimit        int              `json:"trade_limit"`
	HasMore           bool             `json:"has_more"`
	Trades            []LedgerActivity `json:"trades"`
}

func (s *Service) handleInstrumentLedger(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 2 || len(query["instrument_id"]) != 1 || len(query["currency"]) != 1 ||
		query.Get("instrument_id") == "" || !utf8.ValidString(query.Get("instrument_id")) || !currencyCodePattern.MatchString(query.Get("currency")) {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "instrument ledger query is invalid"}})
		return
	}
	result, appErr := s.instrumentLedger(r.Context(), query.Get("instrument_id"), query.Get("currency"))
	if appErr != nil {
		writeError(w, appErr)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) instrumentLedger(ctx context.Context, instrumentID, currency string) (*InstrumentLedger, *appError) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, internalError(err)
	}
	defer tx.Rollback()
	snapshot, err := snapshotFrom(ctx, tx)
	if err != nil {
		return nil, internalError(err)
	}
	ledgerRevision, recordedAt, err := s.proveLedgerEvents(ctx, tx)
	if err != nil {
		return nil, internalError(err)
	}
	if revision(ledgerRevision) != snapshot.LedgerRevision {
		return nil, internalError(errors.New("instrument ledger snapshot proof mismatch"))
	}
	result := &InstrumentLedger{
		InstrumentID: instrumentID, Currency: currency, Source: "local_ledger", BrokerFreshness: "unverified",
		LedgerRevision: snapshot.LedgerRevision, LedgerAsOf: snapshot.AsOf, LedgerRecordedAt: recordedAt,
		CostBasisPolicy: snapshot.CostBasisPolicy, AverageCostPolicy: instrumentAverageCostPolicy,
		Quantity: "0", CostBasis: "0", TradeLimit: defaultActivityLimit, Trades: []LedgerActivity{},
	}
	for _, holding := range snapshot.Holdings {
		if holding.InstrumentID != instrumentID || holding.Currency != currency {
			continue
		}
		quantity, quantityErr := parseDecimal(holding.Quantity)
		cost, costErr := parseDecimal(holding.CostBasis)
		if quantityErr != nil || costErr != nil || quantity.Sign() <= 0 || cost.Sign() < 0 {
			return nil, internalError(errors.New("instrument ledger holding is invalid"))
		}
		// Round only the display ratio; the exact remaining FIFO cost stays unchanged.
		average, err := formatDecimal(exact.QuantizeHalfEven(new(big.Rat).Quo(cost, quantity), 8))
		if err != nil {
			return nil, internalError(err)
		}
		result.Quantity, result.CostBasis, result.AverageCost = holding.Quantity, holding.CostBasis, &average
		result.Symbol = holding.Symbol
	}
	rows, err := tx.QueryContext(ctx, `SELECT type,occurred_at,recorded_at,symbol,quantity,price,fee,amount
		FROM events WHERE instrument_id=? AND currency=? AND type IN ('BUY','SELL') AND sequence<=?
		ORDER BY rtrim(occurred_at,'Z') DESC,sequence DESC LIMIT ?`, instrumentID, currency, ledgerRevision, defaultActivityLimit+1)
	if err != nil {
		return nil, internalError(err)
	}
	for rows.Next() {
		entry := LedgerActivity{Currency: currency}
		var symbol, quantity, price, fee string
		if err := rows.Scan(&entry.Type, &entry.OccurredAt, &entry.RecordedAt, &symbol, &quantity, &price, &fee, &entry.Amount); err != nil {
			rows.Close()
			return nil, internalError(err)
		}
		entry.Symbol, entry.Quantity, entry.Price, entry.Fee = &symbol, &quantity, &price, &fee
		if result.Symbol == "" {
			result.Symbol = symbol
		}
		result.Trades = append(result.Trades, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, internalError(err)
	}
	if err := rows.Close(); err != nil {
		return nil, internalError(err)
	}
	if len(result.Trades) == 0 {
		return nil, &appError{http.StatusNotFound, APIError{Code: "instrument_ledger_not_found", Message: "no trades for this instrument and currency"}}
	}
	result.HasMore = len(result.Trades) > defaultActivityLimit
	if result.HasMore {
		result.Trades = result.Trades[:defaultActivityLimit]
	}
	if err := tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return result, nil
}
