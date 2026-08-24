package main

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"sort"
	"time"
)

const (
	kiwoomLookupMatched      = "MATCHED"
	kiwoomLookupNotFound     = "NOT_FOUND"
	kiwoomLookupIncomplete   = "INCOMPLETE"
	kiwoomLookupUncorrelated = "UNCORRELATED"
)

type KiwoomOrderLookup struct {
	Provider   string
	Mode       string
	AccountRef string
	ObservedAt string
	Complete   bool
	Orders     []KiwoomOrderObservation
}

type KiwoomOrderObservation struct {
	ProviderOrderRef   string
	Symbol             string
	Exchange           string
	Side               string
	OrderType          string
	Currency           string
	Quantity           string
	LimitPrice         string
	RemainingQuantity  string
	SubmittedAt        string
	ExecutionsComplete bool
	Executions         []KiwoomExecutionObservation
}

type KiwoomExecutionObservation struct {
	ProviderExecutionRef string
	Quantity             string
	Price                string
	OccurredAt           string
}

type KiwoomOrderReconciliation struct {
	Outcome string
	State   *OrderState
}

type validatedKiwoomObservation struct {
	order      KiwoomOrderObservation
	submitted  time.Time
	incomplete bool
}

func (s *Service) reconcileKiwoomOrderLookup(ctx context.Context, orderID string, lookup KiwoomOrderLookup) (*KiwoomOrderReconciliation, error) {
	if s == nil || s.db == nil || !safeOrderID(orderID) {
		return nil, errors.New("invalid reconciliation target")
	}
	observedAt, observations, err := validateKiwoomOrderLookup(lookup)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	intent, err := loadOrderIntentFrom(ctx, tx, orderID)
	if err != nil {
		return nil, err
	}
	state, err := loadOrderStateFrom(ctx, tx, orderID)
	if err != nil {
		return nil, err
	}
	if lookup.Provider != intent.Provider || lookup.Mode != intent.Mode || lookup.AccountRef != intent.AccountRef {
		return nil, errors.New("lookup authority does not match the order")
	}
	dispatchedAt, err := orderDispatchTime(ctx, tx, orderID)
	if err != nil {
		return nil, err
	}
	if observedAt.Before(dispatchedAt) {
		return nil, errors.New("lookup observation predates dispatch")
	}

	result := &KiwoomOrderReconciliation{State: state}
	if !lookup.Complete {
		result.Outcome = kiwoomLookupIncomplete
		return result, nil
	}
	if state.Status == "SUBMIT_UNKNOWN" && state.PendingAction == "SUBMIT" {
		result.Outcome = kiwoomLookupUncorrelated
		return result, nil
	}
	if len(state.ProviderOrderRefs) != 1 ||
		(state.Status != "OPEN" && state.Status != "PARTIALLY_FILLED" && state.Status != "FILLED" &&
			state.Status != "CANCEL_UNKNOWN" && state.Status != "CANCELED") {
		return nil, errors.New("order state cannot accept lookup reconciliation")
	}
	var match *validatedKiwoomObservation
	for i := range observations {
		if observations[i].order.ProviderOrderRef == state.ProviderOrderRefs[0] {
			match = &observations[i]
			break
		}
	}
	if match == nil {
		result.Outcome = kiwoomLookupNotFound
		return result, nil
	}
	order := match.order
	if match.submitted.Before(dispatchedAt) || match.submitted.After(observedAt) || order.Symbol != intent.Symbol ||
		order.Exchange != intent.Exchange || order.Side != intent.Side || order.OrderType != intent.OrderType ||
		order.Currency != intent.Currency || order.Quantity != intent.Quantity || order.LimitPrice != intent.LimitPrice {
		return nil, errors.New("provider order observation conflicts with the local order")
	}
	if match.incomplete {
		result.Outcome = kiwoomLookupIncomplete
		return result, nil
	}

	events, err := reconciliationEvents(orderID, state, order)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		if err := validateOrderEvent(event); err != nil {
			return nil, err
		}
	}
	recordedAt := s.now().UTC().Format(time.RFC3339Nano)
	for _, event := range events {
		state, err = appendOrderEventTx(ctx, tx, event, recordedAt)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &KiwoomOrderReconciliation{Outcome: kiwoomLookupMatched, State: state}, nil
}

func validateKiwoomOrderLookup(lookup KiwoomOrderLookup) (time.Time, []validatedKiwoomObservation, error) {
	if lookup.Provider != "kiwoom" || lookup.Mode != "synthetic" || !orderAlias(lookup.AccountRef, "account") {
		return time.Time{}, nil, errors.New("invalid Kiwoom lookup envelope")
	}
	observedAt, ok := canonicalUTCTime(lookup.ObservedAt)
	if !ok {
		return time.Time{}, nil, errors.New("invalid lookup observation time")
	}
	orders := make([]validatedKiwoomObservation, 0, len(lookup.Orders))
	seenOrders := make(map[string]struct{}, len(lookup.Orders))
	seenExecutions := make(map[string]struct{})
	for _, order := range lookup.Orders {
		if !orderAlias(order.ProviderOrderRef, "order") {
			return time.Time{}, nil, errors.New("invalid provider order reference")
		}
		if _, duplicate := seenOrders[order.ProviderOrderRef]; duplicate {
			return time.Time{}, nil, errors.New("duplicate provider order observation")
		}
		seenOrders[order.ProviderOrderRef] = struct{}{}
		if !kiwoomStockPattern.MatchString(order.Symbol) || order.Exchange != "KRX" ||
			(order.Side != "BUY" && order.Side != "SELL") || order.OrderType != "LIMIT" || order.Currency != "KRW" ||
			!validOrderInteger(order.Quantity) || !validOrderNonNegativeInteger(order.RemainingQuantity) || !positiveCanonicalDecimal(order.LimitPrice) {
			return time.Time{}, nil, errors.New("invalid provider order observation")
		}
		submittedAt, ok := canonicalUTCTime(order.SubmittedAt)
		if !ok || submittedAt.After(observedAt) {
			return time.Time{}, nil, errors.New("invalid provider order time")
		}
		fillTotal, _ := parseDecimal("0")
		for _, execution := range order.Executions {
			if !orderAlias(execution.ProviderExecutionRef, "execution") {
				return time.Time{}, nil, errors.New("invalid provider execution reference")
			}
			if _, duplicate := seenExecutions[execution.ProviderExecutionRef]; duplicate {
				return time.Time{}, nil, errors.New("duplicate provider execution observation")
			}
			seenExecutions[execution.ProviderExecutionRef] = struct{}{}
			occurredAt, ok := canonicalUTCTime(execution.OccurredAt)
			if !ok || occurredAt.Before(submittedAt) || occurredAt.After(observedAt) ||
				!validOrderInteger(execution.Quantity) || !positiveCanonicalDecimal(execution.Price) {
				return time.Time{}, nil, errors.New("invalid provider execution observation")
			}
			quantity, _ := parseDecimal(execution.Quantity)
			fillTotal.Add(fillTotal, quantity)
		}
		remaining, _ := parseDecimal(order.RemainingQuantity)
		total, _ := parseDecimal(order.Quantity)
		accounted := new(big.Rat).Add(fillTotal, remaining)
		if accounted.Cmp(total) > 0 || (order.ExecutionsComplete && accounted.Cmp(total) != 0) {
			return time.Time{}, nil, errors.New("provider execution quantities conflict")
		}
		incomplete := !order.ExecutionsComplete
		orders = append(orders, validatedKiwoomObservation{order: order, submitted: submittedAt, incomplete: incomplete})
	}
	return observedAt, orders, nil
}

func reconciliationEvents(orderID string, state *OrderState, order KiwoomOrderObservation) ([]OrderEvent, error) {
	if len(state.ProviderOrderRefs) != 1 || state.ProviderOrderRefs[0] != order.ProviderOrderRef {
		return nil, errors.New("order state cannot accept lookup reconciliation")
	}
	events := make([]OrderEvent, 0, len(order.Executions))
	executions := append([]KiwoomExecutionObservation(nil), order.Executions...)
	sort.Slice(executions, func(i, j int) bool {
		left, _ := time.Parse(time.RFC3339Nano, executions[i].OccurredAt)
		right, _ := time.Parse(time.RFC3339Nano, executions[j].OccurredAt)
		if left.Equal(right) {
			return executions[i].ProviderExecutionRef < executions[j].ProviderExecutionRef
		}
		return left.Before(right)
	})
	for _, execution := range executions {
		events = append(events, OrderEvent{
			EventID: reconciliationEventID("fill", orderID, execution.ProviderExecutionRef), OrderID: orderID,
			Type: "FILL_RECORDED", Source: "reconciliation", ProviderOrderRef: order.ProviderOrderRef,
			ProviderExecutionRef: execution.ProviderExecutionRef, Quantity: execution.Quantity, Price: execution.Price,
			OccurredAt: execution.OccurredAt,
		})
	}
	return events, nil
}

func orderDispatchTime(ctx context.Context, q orderQuerier, orderID string) (time.Time, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT recorded_at FROM order_events WHERE order_id=? AND event_type='SUBMIT_DISPATCHED' ORDER BY sequence LIMIT 1`, orderID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, errors.New("order has no submit dispatch")
	}
	if err != nil {
		return time.Time{}, err
	}
	value, ok := canonicalUTCTime(raw)
	if !ok {
		return time.Time{}, errors.New("stored submit dispatch time is invalid")
	}
	return value, nil
}

func positiveCanonicalDecimal(raw string) bool {
	value, err := parseDecimal(raw)
	return err == nil && value.Sign() > 0
}

func validOrderNonNegativeInteger(raw string) bool {
	return raw == "0" || validOrderInteger(raw)
}

func canonicalUTCTime(raw string) (time.Time, bool) {
	value, err := time.Parse(time.RFC3339Nano, raw)
	return value, err == nil && value.Location() == time.UTC && value.Format(time.RFC3339Nano) == raw
}

func reconciliationEventID(kind, orderID, ref string) string {
	_, hash, _ := orderJSONHash([]string{kind, orderID, ref})
	return "reconciliation_" + hash[:32]
}
