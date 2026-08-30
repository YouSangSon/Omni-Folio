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

const (
	legacyPaperSignalSchema = "paper-signal.v1"
	paperSignalSchema       = "paper-signal.v2"
)

type PaperSignal struct {
	SchemaVersion            string `json:"schema_version"`
	SignalID                 string `json:"signal_id"`
	SignalBarObservationID   string `json:"signal_bar_observation_id,omitempty"`
	StrategyResultSHA256     string `json:"strategy_result_sha256"`
	StrategySelectionEventID string `json:"strategy_selection_event_id"`
	DataSHA256               string `json:"data_sha256"`
	Symbol                   string `json:"symbol"`
	TargetQuantity           string `json:"target_quantity"`
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

// ponytail: this first paper adapter nets one KRX long-only target against local paper orders and
// one finite ask; add holdings, cash allocation, fees, tax, slippage and quotes before promotion.
func (s *Service) runPaperSignal(ctx context.Context, accountRef string, signal PaperSignal, observation PaperMarketObservation, fencingToken int64) (*OrderState, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("paper runner is not configured")
	}
	target, generatedAt, expiresAt, err := validatePaperSignal(signal)
	if err != nil {
		return nil, err
	}
	observedAt, ask, available, err := validatePaperObservation(observation, signal, generatedAt, s.now().UTC())
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	state, err := s.recordPaperTarget(ctx, accountRef, signal, observation.AskPrice, target, generatedAt, expiresAt, now, fencingToken)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, nil
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
	limit, _ := parseDecimal(state.LimitPrice)
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

func validatePaperSignal(signal PaperSignal) (*big.Int, time.Time, time.Time, error) {
	dataAsOf, dataOK := canonicalUTCTime(signal.DataAsOf)
	generatedAt, generatedOK := canonicalUTCTime(signal.GeneratedAt)
	expiresAt, expiresOK := canonicalUTCTime(signal.ExpiresAt)
	if len(signal.TargetQuantity) == 0 || len(signal.TargetQuantity) > 64 {
		return nil, time.Time{}, time.Time{}, errors.New("paper signal is invalid")
	}
	target, targetOK := new(big.Int).SetString(signal.TargetQuantity, 10)
	if signal.SchemaVersion != paperSignalSchema || !safeOrderID(signal.SignalID) || !kiwoomStockPattern.MatchString(signal.Symbol) ||
		!strategySHA256Pattern.MatchString(signal.StrategyResultSHA256) || !safeOrderID(signal.StrategySelectionEventID) ||
		!strategySHA256Pattern.MatchString(signal.DataSHA256) || !targetOK || target.Sign() <= 0 || !validOrderInteger(signal.TargetQuantity) ||
		!dataOK || !generatedOK || !expiresOK || dataAsOf.After(generatedAt) || !generatedAt.Before(expiresAt) ||
		!safeOrderID("paper_"+signal.SignalID) {
		return nil, time.Time{}, time.Time{}, errors.New("paper signal is invalid")
	}
	return target, generatedAt, expiresAt, nil
}

func paperOrderIntent(accountRef string, signal PaperSignal, quantity, limitPrice string) OrderIntent {
	return OrderIntent{
		ClientOrderID: "paper_" + signal.SignalID, Provider: "kiwoom", Mode: "paper", AccountRef: accountRef,
		Symbol: signal.Symbol, Exchange: "KRX", Side: "BUY", OrderType: "LIMIT", Quantity: quantity,
		LimitPrice: limitPrice, Currency: "KRW", StrategyResultSHA256: signal.StrategyResultSHA256,
		StrategySelectionEventID: signal.StrategySelectionEventID, SignalSchemaVersion: signal.SchemaVersion,
		SignalID: signal.SignalID, SignalDataSHA256: signal.DataSHA256, SignalDataAsOf: signal.DataAsOf,
		SignalGeneratedAt: signal.GeneratedAt, SignalExpiresAt: signal.ExpiresAt, SignalTargetQuantity: signal.TargetQuantity,
	}
}

func capitalizedPaperOrderIntent(signal PaperSignalEvent, side, quantity string) OrderIntent {
	return OrderIntent{
		ClientOrderID: "paper_" + signal.SignalID, Provider: "kiwoom", Mode: "paper", AccountRef: signal.AccountRef,
		Symbol: signal.Symbol, Exchange: "KRX", Side: side, OrderType: "PAPER_MARKET", Quantity: quantity,
		Currency: "KRW", StrategyResultSHA256: signal.StrategyResultSHA256,
		StrategySelectionEventID: signal.StrategySelectionEventID, SignalSchemaVersion: signal.SchemaVersion,
		SignalID: signal.SignalID, SignalDataSHA256: signal.DataSHA256, SignalDataAsOf: signal.DataAsOf,
		SignalGeneratedAt: signal.GeneratedAt, SignalExpiresAt: signal.ExpiresAt, SignalTargetQuantity: signal.TargetQuantity,
		PaperAccountingSessionID: signal.PaperAccountingSessionID, PaperAccountingPolicyVersion: paperAccountingPolicyVersion,
		PaperSignalEventID: signal.EventID, ExecutionPolicySHA256: signal.ExecutionPolicySHA256,
	}
}

func (s *Service) admitPaperSignal(ctx context.Context, accountRef string, signal PaperSignal, fencingToken int64) (*PaperSignalEvent, *OrderState, error) {
	if s == nil || s.db == nil || s.now == nil || !orderAlias(accountRef, "account") || fencingToken <= 0 {
		return nil, nil, errors.New("paper signal admission is not configured")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	event, err := s.recordPaperSignalEventTx(ctx, tx, accountRef, signal)
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.requireCurrentSyntheticExecutionLease(ctx, tx, accountRef, fencingToken, s.now().UTC()); err != nil {
		return nil, nil, err
	}
	orderID, exists, err := paperOrderBySignalFrom(ctx, tx, accountRef, signal.SignalID)
	if err != nil {
		return nil, nil, err
	}
	if exists {
		intent, err := loadOrderIntentFrom(ctx, tx, orderID)
		if err != nil {
			return nil, nil, err
		}
		if _, boundSignal, err := validateCapitalizedPaperOrderBindings(ctx, tx, intent); err != nil || boundSignal.EventID != event.EventID {
			return nil, nil, errors.New("paper signal conflicts with its recorded order")
		}
		state, err := loadOrderStateFrom(ctx, tx, orderID)
		if err != nil {
			return nil, nil, err
		}
		return event, state, nil
	}
	target, ok := new(big.Int).SetString(event.TargetQuantity, 10)
	if !ok || target.Sign() < 0 {
		return nil, nil, errors.New("paper target quantity is invalid")
	}
	projection, err := replayPaperProjectionFrom(ctx, tx, accountRef, event.Symbol)
	if err != nil {
		return nil, nil, err
	}
	if projection.active {
		if projection.quantity.Cmp(target) == 0 {
			if err := tx.Commit(); err != nil {
				return nil, nil, err
			}
			return event, nil, nil
		}
		return nil, nil, errors.New("active paper order target cannot be replaced")
	}
	delta := new(big.Int).Sub(target, projection.quantity)
	if delta.Sign() == 0 {
		if err := tx.Commit(); err != nil {
			return nil, nil, err
		}
		return event, nil, nil
	}
	side := "BUY"
	quantity := new(big.Int).Set(delta)
	if delta.Sign() < 0 {
		side = "SELL"
		quantity.Neg(quantity)
		if quantity.Cmp(projection.quantity) > 0 {
			return nil, nil, errors.New("paper SELL exceeds filled holdings")
		}
	}
	intent := capitalizedPaperOrderIntent(*event, side, quantity.String())
	state, err := s.recordOrderIntentTx(ctx, tx, intent)
	if err != nil {
		return nil, nil, err
	}
	state, _, err = s.authorizePaperDispatchOnceTx(ctx, tx, state.OrderID, fencingToken)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return event, state, nil
}

func (s *Service) recordPaperTarget(ctx context.Context, accountRef string, signal PaperSignal, limitPrice string, target *big.Int, generatedAt, expiresAt, now time.Time, fencingToken int64) (*OrderState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	orderID, exists, err := paperOrderBySignalFrom(ctx, tx, accountRef, signal.SignalID)
	if err != nil {
		return nil, err
	}
	if exists {
		intent, err := loadOrderIntentFrom(ctx, tx, orderID)
		if err != nil || intent != paperOrderIntent(accountRef, signal, intent.Quantity, intent.LimitPrice) {
			return nil, errors.New("paper signal conflicts with its recorded order")
		}
		return loadOrderStateFrom(ctx, tx, orderID)
	}
	if now.Before(generatedAt) || !now.Before(expiresAt) {
		return nil, errors.New("paper signal is not active")
	}
	projected, err := paperProjectedQuantityFrom(ctx, tx, accountRef, signal.Symbol)
	if err != nil {
		return nil, err
	}
	delta := new(big.Int).Sub(target, projected)
	if delta.Sign() <= 0 {
		return nil, nil
	}
	intent := paperOrderIntent(accountRef, signal, delta.String(), limitPrice)
	if _, _, err := validateSyntheticBuyPolicy(intent); err != nil {
		return nil, err
	}
	state, err := s.recordOrderIntentTx(ctx, tx, intent)
	if err != nil {
		return nil, err
	}
	state, _, err = s.authorizeSyntheticDispatchOnceTx(ctx, tx, state.OrderID, fencingToken)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return state, nil
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

func paperOrderBySignalFrom(ctx context.Context, q orderQuerier, accountRef, signalID string) (string, bool, error) {
	if !orderAlias(accountRef, "account") || !safeOrderID(signalID) {
		return "", false, errors.New("paper execution identity is invalid")
	}
	var orderID string
	err := q.QueryRowContext(ctx, `SELECT order_id FROM order_idempotency WHERE provider=? AND mode=? AND account_ref=? AND client_order_id=?`,
		"kiwoom", "paper", accountRef, "paper_"+signalID).Scan(&orderID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return orderID, err == nil, err
}

func paperProjectedQuantityFrom(ctx context.Context, q orderQuerier, accountRef, symbol string) (*big.Int, error) {
	projection, err := replayPaperProjectionFrom(ctx, q, accountRef, symbol)
	if err != nil {
		return nil, err
	}
	return projection.quantity, nil
}

type paperProjection struct {
	quantity *big.Int
	active   bool
}

func replayPaperProjectionFrom(ctx context.Context, q orderQuerier, accountRef, symbol string) (paperProjection, error) {
	rows, err := q.QueryContext(ctx, `SELECT order_id FROM order_idempotency WHERE mode='paper' AND account_ref=? ORDER BY rowid`, accountRef)
	if err != nil {
		return paperProjection{}, err
	}
	var orderIDs []string
	for rows.Next() {
		var orderID string
		if err := rows.Scan(&orderID); err != nil {
			rows.Close()
			return paperProjection{}, err
		}
		orderIDs = append(orderIDs, orderID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return paperProjection{}, err
	}
	if err := rows.Close(); err != nil {
		return paperProjection{}, err
	}
	projected := new(big.Int)
	activeCount := 0
	for _, orderID := range orderIDs {
		intent, err := loadOrderIntentFrom(ctx, q, orderID)
		if err != nil {
			return paperProjection{}, err
		}
		if intent.SignalSchemaVersion != capitalizedPaperSignalSchema {
			return paperProjection{}, errors.New("paper projection contains an uncapitalized legacy order")
		}
		if intent.Symbol != symbol {
			continue
		}
		state, err := loadOrderStateFrom(ctx, q, orderID)
		if err != nil {
			return paperProjection{}, err
		}
		total, totalOK := new(big.Int).SetString(state.Quantity, 10)
		filled, filledOK := new(big.Int).SetString(state.FilledQuantity, 10)
		if !totalOK || !filledOK || filled.Sign() < 0 || filled.Cmp(total) > 0 {
			return paperProjection{}, errors.New("paper projected quantity is invalid")
		}
		sign := int64(1)
		if intent.Side == "SELL" {
			sign = -1
		}
		projected.Add(projected, new(big.Int).Mul(filled, big.NewInt(sign)))
		active := state.Status == "RECORDED" || state.Status == "READY" || reservationIsActive(state)
		if active {
			activeCount++
			remaining := new(big.Int).Sub(total, filled)
			projected.Add(projected, remaining.Mul(remaining, big.NewInt(sign)))
		}
	}
	if activeCount > 1 || projected.Sign() < 0 {
		return paperProjection{}, errors.New("paper projection violates the one-active-order or long-only boundary")
	}
	return paperProjection{quantity: projected, active: activeCount == 1}, nil
}

func paperProviderAlias(kind string, parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "kiwoom_" + kind + "_" + base64.RawURLEncoding.EncodeToString(hash[:18])
}

func paperEventID(kind string, parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "paper_" + kind + "_" + hex.EncodeToString(hash[:16])
}
