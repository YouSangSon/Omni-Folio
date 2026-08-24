package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"time"
)

const paperSignalSchema = "paper-signal.v1"

type PaperSignal struct {
	SchemaVersion            string `json:"schema_version"`
	SignalID                 string `json:"signal_id"`
	StrategyResultSHA256     string `json:"strategy_result_sha256"`
	StrategySelectionEventID string `json:"strategy_selection_event_id"`
	DataSHA256               string `json:"data_sha256"`
	AccountRef               string `json:"account_ref"`
	Symbol                   string `json:"symbol"`
	Side                     string `json:"side"`
	Quantity                 string `json:"quantity"`
	LimitPrice               string `json:"limit_price"`
	DataAsOf                 string `json:"data_as_of"`
	GeneratedAt              string `json:"generated_at"`
	ExpiresAt                string `json:"expires_at"`
}

type PaperMarketObservation struct {
	Source            string `json:"source"`
	Symbol            string `json:"symbol"`
	ObservedAt        string `json:"observed_at"`
	AskPrice          string `json:"ask_price"`
	AvailableQuantity string `json:"available_quantity"`
}

// ponytail: this first paper adapter models one KRX BUY limit against one ask with finite displayed
// quantity; add fees, tax, slippage and a quote stream before performance promotion evidence.
func (s *Service) runPaperSignal(ctx context.Context, signal PaperSignal, observation PaperMarketObservation, fencingToken int64) (*OrderState, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("paper runner is not configured")
	}
	intent, generatedAt, expiresAt, err := validatePaperSignal(signal)
	if err != nil {
		return nil, err
	}
	observedAt, ask, available, err := validatePaperObservation(observation, signal, generatedAt, s.now().UTC())
	if err != nil {
		return nil, err
	}
	exists, err := s.paperOrderExists(ctx, intent)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	if !exists && (now.Before(generatedAt) || !now.Before(expiresAt)) {
		return nil, errors.New("paper signal is not active")
	}
	state, err := s.recordOrderIntent(ctx, intent)
	if err != nil {
		return nil, err
	}
	if state.Status == "RECORDED" {
		if now.Before(generatedAt) || !now.Before(expiresAt) {
			return state, errors.New("paper signal expired before dispatch")
		}
		state, err = s.authorizeSyntheticDispatch(ctx, state.OrderID, fencingToken)
		if err != nil {
			return state, err
		}
	}
	orderRef := paperProviderAlias("order", state.OrderID)
	if state.Status == "SUBMIT_UNKNOWN" && state.PendingAction == "SUBMIT" {
		state, err = s.appendOrderEvent(ctx, OrderEvent{
			EventID: paperEventID("ack", state.OrderID), OrderID: state.OrderID,
			Type: "SUBMIT_ACKNOWLEDGED", Source: "synthetic", ProviderOrderRef: orderRef,
		})
		if err != nil {
			return state, err
		}
	}
	if state.Status != "OPEN" && state.Status != "PARTIALLY_FILLED" {
		return state, nil
	}
	limit, _ := parseDecimal(intent.LimitPrice)
	if ask.Cmp(limit) > 0 {
		return state, nil
	}
	total, _ := parseDecimal(state.Quantity)
	filled, _ := parseDecimal(state.FilledQuantity)
	remaining := new(big.Rat).Sub(total, filled)
	fillQuantity := new(big.Rat).Set(available)
	if fillQuantity.Cmp(remaining) > 0 {
		fillQuantity.Set(remaining)
	}
	quantity, err := formatDecimal(fillQuantity)
	if err != nil {
		return state, err
	}
	return s.appendOrderEvent(ctx, OrderEvent{
		EventID: paperEventID("fill", state.OrderID, observation.ObservedAt, observation.AskPrice, observation.AvailableQuantity),
		OrderID: state.OrderID, Type: "FILL_RECORDED", Source: "synthetic", ProviderOrderRef: orderRef,
		ProviderExecutionRef: paperProviderAlias("execution", state.OrderID, observation.ObservedAt, observation.AskPrice, observation.AvailableQuantity),
		Quantity:             quantity, Price: observation.AskPrice, OccurredAt: observedAt.Format(time.RFC3339Nano),
	})
}

func validatePaperSignal(signal PaperSignal) (OrderIntent, time.Time, time.Time, error) {
	dataAsOf, dataOK := canonicalUTCTime(signal.DataAsOf)
	generatedAt, generatedOK := canonicalUTCTime(signal.GeneratedAt)
	expiresAt, expiresOK := canonicalUTCTime(signal.ExpiresAt)
	intent := OrderIntent{
		ClientOrderID: "paper_" + signal.SignalID, Provider: "kiwoom", Mode: "paper", AccountRef: signal.AccountRef,
		Symbol: signal.Symbol, Exchange: "KRX", Side: signal.Side, OrderType: "LIMIT", Quantity: signal.Quantity,
		LimitPrice: signal.LimitPrice, Currency: "KRW", StrategyResultSHA256: signal.StrategyResultSHA256,
		StrategySelectionEventID: signal.StrategySelectionEventID, SignalSchemaVersion: signal.SchemaVersion,
		SignalID: signal.SignalID, SignalDataSHA256: signal.DataSHA256, SignalDataAsOf: signal.DataAsOf,
		SignalGeneratedAt: signal.GeneratedAt, SignalExpiresAt: signal.ExpiresAt,
	}
	if signal.SchemaVersion != paperSignalSchema || !safeOrderID(signal.SignalID) || signal.Side != "BUY" ||
		!dataOK || !generatedOK || !expiresOK || dataAsOf.After(generatedAt) || !generatedAt.Before(expiresAt) ||
		validateOrderIntent(intent) != nil {
		return OrderIntent{}, time.Time{}, time.Time{}, errors.New("paper signal is invalid")
	}
	return intent, generatedAt, expiresAt, nil
}

func validatePaperObservation(observation PaperMarketObservation, signal PaperSignal, generatedAt, now time.Time) (time.Time, *big.Rat, *big.Rat, error) {
	observedAt, timeOK := canonicalUTCTime(observation.ObservedAt)
	ask, askErr := parseDecimal(observation.AskPrice)
	available, quantityErr := parseDecimal(observation.AvailableQuantity)
	if observation.Source != "local_fixture" || observation.Symbol != signal.Symbol || !timeOK ||
		!observedAt.After(generatedAt) || observedAt.After(now) || askErr != nil || ask.Sign() <= 0 ||
		quantityErr != nil || available.Sign() <= 0 || !validOrderInteger(observation.AvailableQuantity) {
		return time.Time{}, nil, nil, errors.New("paper market observation is invalid")
	}
	return observedAt, ask, available, nil
}

func (s *Service) paperOrderExists(ctx context.Context, intent OrderIntent) (bool, error) {
	var orderID string
	err := s.db.QueryRowContext(ctx, `SELECT order_id FROM order_idempotency WHERE provider=? AND mode=? AND account_ref=? AND client_order_id=?`,
		intent.Provider, intent.Mode, intent.AccountRef, intent.ClientOrderID).Scan(&orderID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func paperProviderAlias(kind string, parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "kiwoom_" + kind + "_" + base64.RawURLEncoding.EncodeToString(hash[:18])
}

func paperEventID(kind string, parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "paper_" + kind + "_" + hex.EncodeToString(hash[:16])
}
