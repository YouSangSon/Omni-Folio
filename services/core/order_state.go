package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	orderIntegerPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	orderAliasPattern   = regexp.MustCompile(`^kiwoom_(account|order|execution)_[A-Za-z0-9_-]{24}$`)
)

type OrderIntent struct {
	ClientOrderID string `json:"client_order_id"`
	Provider      string `json:"provider"`
	Mode          string `json:"mode"`
	AccountRef    string `json:"account_ref"`
	Symbol        string `json:"symbol"`
	Exchange      string `json:"exchange"`
	Side          string `json:"side"`
	OrderType     string `json:"order_type"`
	Quantity      string `json:"quantity"`
	LimitPrice    string `json:"limit_price"`
	Currency      string `json:"currency"`
}

type OrderEvent struct {
	EventID              string `json:"event_id"`
	OrderID              string `json:"order_id"`
	Type                 string `json:"type"`
	Source               string `json:"source"`
	ProviderOrderRef     string `json:"provider_order_ref,omitempty"`
	ProviderExecutionRef string `json:"provider_execution_ref,omitempty"`
	Quantity             string `json:"quantity,omitempty"`
	Price                string `json:"price,omitempty"`
	OccurredAt           string `json:"occurred_at,omitempty"`
}

type OrderState struct {
	OrderID           string   `json:"order_id"`
	ClientOrderID     string   `json:"client_order_id"`
	AccountRef        string   `json:"account_ref"`
	Status            string   `json:"status"`
	Quantity          string   `json:"quantity"`
	LimitPrice        string   `json:"limit_price"`
	FilledQuantity    string   `json:"filled_quantity"`
	ProviderOrderRefs []string `json:"provider_order_refs"`
	PendingAction     string   `json:"pending_action,omitempty"`
}

type orderQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type orderRecoveryProof struct {
	SHA256 string
	Orders int
	Events int
}

func proveOrderRecovery(ctx context.Context, q orderQuerier) (orderRecoveryProof, error) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	orderRows, err := q.QueryContext(ctx, `SELECT provider,mode,account_ref,client_order_id,request_sha256,order_id,intent_json,recorded_at
		FROM order_idempotency ORDER BY provider,mode,account_ref,client_order_id`)
	if err != nil {
		return orderRecoveryProof{}, err
	}
	var orderIDs []string
	knownOrders := map[string]bool{}
	for orderRows.Next() {
		var provider, mode, accountRef, clientOrderID, requestSHA, orderID, intentJSON, recordedAt string
		if err := orderRows.Scan(&provider, &mode, &accountRef, &clientOrderID, &requestSHA, &orderID, &intentJSON, &recordedAt); err != nil {
			orderRows.Close()
			return orderRecoveryProof{}, err
		}
		var intent OrderIntent
		if err := json.Unmarshal([]byte(intentJSON), &intent); err != nil {
			orderRows.Close()
			return orderRecoveryProof{}, fmt.Errorf("decode order %q intent: %w", orderID, err)
		}
		canonical, actualSHA, err := orderJSONHash(intent)
		if err != nil {
			orderRows.Close()
			return orderRecoveryProof{}, err
		}
		if string(canonical) != intentJSON || actualSHA != requestSHA || intent.Provider != provider || intent.Mode != mode ||
			intent.AccountRef != accountRef || intent.ClientOrderID != clientOrderID || !safeOrderID(orderID) {
			orderRows.Close()
			return orderRecoveryProof{}, fmt.Errorf("order %q intent metadata or hash mismatch", orderID)
		}
		if err := encoder.Encode([]any{"order_idempotency", provider, mode, accountRef, clientOrderID, requestSHA, orderID, intentJSON, recordedAt}); err != nil {
			orderRows.Close()
			return orderRecoveryProof{}, err
		}
		orderIDs = append(orderIDs, orderID)
		knownOrders[orderID] = true
	}
	if err := orderRows.Err(); err != nil {
		orderRows.Close()
		return orderRecoveryProof{}, err
	}
	if err := orderRows.Close(); err != nil {
		return orderRecoveryProof{}, err
	}

	eventRows, err := q.QueryContext(ctx, `SELECT sequence,event_id,event_sha256,order_id,event_type,source,provider_order_ref,provider_execution_ref,event_json,recorded_at
		FROM order_events ORDER BY sequence`)
	if err != nil {
		return orderRecoveryProof{}, err
	}
	eventCount := 0
	for eventRows.Next() {
		var sequence int64
		var eventID, eventSHA, orderID, eventType, source, eventJSON, recordedAt string
		var providerOrderRef, providerExecutionRef sql.NullString
		if err := eventRows.Scan(&sequence, &eventID, &eventSHA, &orderID, &eventType, &source, &providerOrderRef, &providerExecutionRef, &eventJSON, &recordedAt); err != nil {
			eventRows.Close()
			return orderRecoveryProof{}, err
		}
		if !knownOrders[orderID] {
			eventRows.Close()
			return orderRecoveryProof{}, fmt.Errorf("order event %q references unknown order %q", eventID, orderID)
		}
		var event OrderEvent
		if err := json.Unmarshal([]byte(eventJSON), &event); err != nil {
			eventRows.Close()
			return orderRecoveryProof{}, fmt.Errorf("decode order event %q: %w", eventID, err)
		}
		canonical, actualSHA, err := orderJSONHash(event)
		if err != nil {
			eventRows.Close()
			return orderRecoveryProof{}, err
		}
		nullableMatches := func(column sql.NullString, value string) bool {
			return column.Valid == (value != "") && (!column.Valid || column.String == value)
		}
		if string(canonical) != eventJSON || actualSHA != eventSHA || event.EventID != eventID || event.OrderID != orderID ||
			event.Type != eventType || event.Source != source || !nullableMatches(providerOrderRef, event.ProviderOrderRef) ||
			!nullableMatches(providerExecutionRef, event.ProviderExecutionRef) {
			eventRows.Close()
			return orderRecoveryProof{}, fmt.Errorf("order event %q metadata or hash mismatch", eventID)
		}
		if err := encoder.Encode([]any{"order_events", sequence, eventID, eventSHA, orderID, eventType, source,
			providerOrderRef, providerExecutionRef, eventJSON, recordedAt}); err != nil {
			eventRows.Close()
			return orderRecoveryProof{}, err
		}
		eventCount++
	}
	if err := eventRows.Err(); err != nil {
		eventRows.Close()
		return orderRecoveryProof{}, err
	}
	if err := eventRows.Close(); err != nil {
		return orderRecoveryProof{}, err
	}
	for _, orderID := range orderIDs {
		if _, err := loadOrderStateFrom(ctx, q, orderID); err != nil {
			return orderRecoveryProof{}, fmt.Errorf("replay order %q: %w", orderID, err)
		}
	}
	return orderRecoveryProof{hex.EncodeToString(hash.Sum(nil)), len(orderIDs), eventCount}, nil
}

func (s *Service) recordOrderIntent(ctx context.Context, intent OrderIntent) (*OrderState, error) {
	if err := validateOrderIntent(intent); err != nil {
		return nil, err
	}
	intentJSON, requestSHA, err := orderJSONHash(intent)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var priorSHA, priorOrderID string
	err = tx.QueryRowContext(ctx, `SELECT request_sha256, order_id FROM order_idempotency WHERE provider=? AND mode=? AND account_ref=? AND client_order_id=?`,
		intent.Provider, intent.Mode, intent.AccountRef, intent.ClientOrderID).Scan(&priorSHA, &priorOrderID)
	if err == nil {
		if priorSHA != requestSHA {
			return nil, errors.New("client_order_id was already used with a different intent")
		}
		return loadOrderStateFrom(ctx, tx, priorOrderID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	orderID := s.id("order")
	recordedAt := s.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO order_idempotency(provider,mode,account_ref,client_order_id,request_sha256,order_id,intent_json,recorded_at) VALUES(?,?,?,?,?,?,?,?)`,
		intent.Provider, intent.Mode, intent.AccountRef, intent.ClientOrderID, requestSHA, orderID, string(intentJSON), recordedAt); err != nil {
		return nil, err
	}
	event := OrderEvent{EventID: s.id("order_event"), OrderID: orderID, Type: "INTENT_RECORDED", Source: "synthetic"}
	if err := insertOrderEvent(ctx, tx, event, recordedAt); err != nil {
		return nil, err
	}
	state := newOrderState(orderID, intent)
	state, err = applyOrderEvent(state, event)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return state, nil
}

func (s *Service) appendOrderEvent(ctx context.Context, event OrderEvent) (*OrderState, error) {
	if err := validateOrderEvent(event); err != nil {
		return nil, err
	}
	eventJSON, eventSHA, err := orderJSONHash(event)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var priorSHA string
	err = tx.QueryRowContext(ctx, `SELECT event_sha256 FROM order_events WHERE event_id=?`, event.EventID).Scan(&priorSHA)
	if err == nil {
		if priorSHA != eventSHA {
			return nil, errors.New("event_id was already used with a different event")
		}
		return loadOrderStateFrom(ctx, tx, event.OrderID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	if event.ProviderExecutionRef != "" {
		var priorJSON string
		err := tx.QueryRowContext(ctx, `SELECT event_json FROM order_events WHERE provider_execution_ref=?`, event.ProviderExecutionRef).Scan(&priorJSON)
		if err == nil {
			var prior OrderEvent
			if err := json.Unmarshal([]byte(priorJSON), &prior); err != nil {
				return nil, err
			}
			if !sameProviderExecution(prior, event) {
				return nil, errors.New("provider_execution_ref was already used with a different fill")
			}
			return loadOrderStateFrom(ctx, tx, prior.OrderID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}

	state, err := loadOrderStateFrom(ctx, tx, event.OrderID)
	if err != nil {
		return nil, err
	}
	if event.Type == "SUBMIT_DISPATCHED" {
		blocked, err := accountHasUnresolvedOrder(ctx, tx, state.AccountRef)
		if err != nil {
			return nil, err
		}
		if blocked {
			return nil, errors.New("account has an unresolved order command")
		}
	}
	if event.ProviderOrderRef != "" {
		var boundOrderID string
		err := tx.QueryRowContext(ctx, `SELECT order_id FROM order_events WHERE provider_order_ref=? LIMIT 1`, event.ProviderOrderRef).Scan(&boundOrderID)
		if err == nil && boundOrderID != event.OrderID {
			return nil, errors.New("provider_order_ref is already bound to another order")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	next, err := applyOrderEvent(state, event)
	if err != nil {
		return nil, err
	}
	recordedAt := s.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO order_events(event_id,event_sha256,order_id,event_type,source,provider_order_ref,provider_execution_ref,event_json,recorded_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		event.EventID, eventSHA, event.OrderID, event.Type, event.Source, nullable(event.ProviderOrderRef), nullable(event.ProviderExecutionRef), string(eventJSON), recordedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return next, nil
}

func (s *Service) loadOrderState(ctx context.Context, orderID string) (*OrderState, error) {
	return loadOrderStateFrom(ctx, s.db, orderID)
}

func loadOrderStateFrom(ctx context.Context, q orderQuerier, orderID string) (*OrderState, error) {
	var intentJSON string
	if err := q.QueryRowContext(ctx, `SELECT intent_json FROM order_idempotency WHERE order_id=?`, orderID).Scan(&intentJSON); err != nil {
		return nil, err
	}
	var intent OrderIntent
	if err := json.Unmarshal([]byte(intentJSON), &intent); err != nil {
		return nil, err
	}
	if err := validateOrderIntent(intent); err != nil {
		return nil, fmt.Errorf("stored order intent is invalid: %w", err)
	}
	state := newOrderState(orderID, intent)
	rows, err := q.QueryContext(ctx, `SELECT event_json FROM order_events WHERE order_id=? ORDER BY sequence`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var event OrderEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, err
		}
		if event.OrderID != orderID {
			return nil, errors.New("stored order event belongs to a different order")
		}
		if err := validateOrderEvent(event); err != nil {
			return nil, fmt.Errorf("stored order event is invalid: %w", err)
		}
		state, err = applyOrderEvent(state, event)
		if err != nil {
			return nil, fmt.Errorf("invalid stored order event sequence: %w", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if count == 0 || state.Status == "" {
		return nil, errors.New("order has no recorded intent event")
	}
	return state, nil
}

func newOrderState(orderID string, intent OrderIntent) *OrderState {
	return &OrderState{
		OrderID: orderID, ClientOrderID: intent.ClientOrderID, AccountRef: intent.AccountRef,
		Quantity: intent.Quantity, LimitPrice: intent.LimitPrice, FilledQuantity: "0", ProviderOrderRefs: []string{},
	}
}

func applyOrderEvent(current *OrderState, event OrderEvent) (*OrderState, error) {
	next := *current
	next.ProviderOrderRefs = append([]string(nil), current.ProviderOrderRefs...)
	switch event.Type {
	case "INTENT_RECORDED":
		if next.Status != "" {
			return nil, errors.New("intent was already recorded")
		}
		next.Status = "RECORDED"
	case "RISK_APPROVED":
		if next.Status != "RECORDED" {
			return nil, errors.New("risk approval requires a recorded order")
		}
		next.Status = "READY"
	case "RISK_REJECTED":
		if next.Status != "RECORDED" {
			return nil, errors.New("risk rejection requires a recorded order")
		}
		next.Status = "RISK_REJECTED"
	case "SUBMIT_DISPATCHED":
		if next.Status != "READY" || next.PendingAction != "" {
			return nil, errors.New("submit dispatch requires a ready order")
		}
		next.Status, next.PendingAction = "SUBMIT_UNKNOWN", "SUBMIT"
	case "SUBMIT_ACKNOWLEDGED":
		if next.Status != "SUBMIT_UNKNOWN" || next.PendingAction != "SUBMIT" {
			return nil, errors.New("submit acknowledgement requires an unknown submit")
		}
		if err := bindProviderOrderRef(&next, event.ProviderOrderRef); err != nil {
			return nil, err
		}
		next.Status, next.PendingAction = "OPEN", ""
	case "SUBMIT_REJECTED":
		if next.Status != "SUBMIT_UNKNOWN" || next.PendingAction != "SUBMIT" {
			return nil, errors.New("submit rejection requires an unknown submit")
		}
		next.Status, next.PendingAction = "REJECTED", ""
	case "FILL_RECORDED":
		if next.Status != "SUBMIT_UNKNOWN" && next.Status != "OPEN" && next.Status != "PARTIALLY_FILLED" &&
			next.Status != "CANCEL_UNKNOWN" && next.Status != "CANCELED" {
			return nil, errors.New("fill is not valid for the current order state")
		}
		if err := bindProviderOrderRef(&next, event.ProviderOrderRef); err != nil {
			return nil, err
		}
		filled, _ := parseDecimal(next.FilledQuantity)
		quantity, _ := parseDecimal(event.Quantity)
		total, _ := parseDecimal(next.Quantity)
		filled.Add(filled, quantity)
		if filled.Cmp(total) > 0 {
			return nil, errors.New("fill exceeds order quantity")
		}
		formatted, err := formatDecimal(filled)
		if err != nil {
			return nil, err
		}
		next.FilledQuantity = formatted
		priorStatus := next.Status
		if next.PendingAction == "SUBMIT" {
			next.PendingAction = ""
		}
		switch {
		case filled.Cmp(total) == 0:
			next.Status = "FILLED"
		case priorStatus == "CANCEL_UNKNOWN":
			next.Status = "CANCEL_UNKNOWN"
		case priorStatus == "CANCELED":
			next.Status = "CANCELED"
		default:
			next.Status = "PARTIALLY_FILLED"
		}
	case "CANCEL_DISPATCHED":
		if (next.Status != "OPEN" && next.Status != "PARTIALLY_FILLED") || next.PendingAction != "" {
			return nil, errors.New("cancel dispatch requires an open order")
		}
		next.Status, next.PendingAction = "CANCEL_UNKNOWN", "CANCEL"
	case "CANCEL_ACKNOWLEDGED":
		if next.PendingAction != "CANCEL" || (next.Status != "CANCEL_UNKNOWN" && next.Status != "FILLED") {
			return nil, errors.New("cancel acknowledgement requires an unknown cancel")
		}
		next.PendingAction = ""
		if next.FilledQuantity == next.Quantity {
			next.Status = "FILLED"
		} else {
			next.Status = "CANCELED"
		}
	case "CANCEL_REJECTED":
		if next.PendingAction != "CANCEL" || (next.Status != "CANCEL_UNKNOWN" && next.Status != "FILLED") {
			return nil, errors.New("cancel rejection requires an unknown cancel")
		}
		next.PendingAction = ""
		if next.FilledQuantity == next.Quantity {
			next.Status = "FILLED"
		} else if next.FilledQuantity == "0" {
			next.Status = "OPEN"
		} else {
			next.Status = "PARTIALLY_FILLED"
		}
	default:
		return nil, fmt.Errorf("unsupported order event %q", event.Type)
	}
	return &next, nil
}

func validateOrderIntent(intent OrderIntent) error {
	if !safeOrderID(intent.ClientOrderID) {
		return errors.New("client_order_id is invalid")
	}
	if intent.Provider != "kiwoom" || intent.Mode != "synthetic" || intent.Exchange != "KRX" ||
		(intent.Side != "BUY" && intent.Side != "SELL") || intent.OrderType != "LIMIT" || intent.Currency != "KRW" {
		return errors.New("order intent is outside the synthetic Kiwoom LIMIT/KRW/KRX boundary")
	}
	if !orderAlias(intent.AccountRef, "account") {
		return errors.New("account_ref must be an opaque Kiwoom account alias")
	}
	if !kiwoomStockPattern.MatchString(intent.Symbol) {
		return errors.New("symbol must be a six-digit KRX code")
	}
	if !validOrderInteger(intent.Quantity) {
		return errors.New("quantity must be a positive canonical integer")
	}
	if len(intent.LimitPrice) == 0 || len(intent.LimitPrice) > 64 {
		return errors.New("limit_price is invalid")
	}
	price, err := parseDecimal(intent.LimitPrice)
	if err != nil || price.Sign() <= 0 {
		return errors.New("limit_price must be a positive canonical decimal")
	}
	return nil
}

func validateOrderEvent(event OrderEvent) error {
	if !safeOrderID(event.EventID) || !safeOrderID(event.OrderID) {
		return errors.New("order event identifiers are invalid")
	}
	if event.Source != "synthetic" && event.Source != "reconciliation" {
		return errors.New("order event source is invalid")
	}
	allowed := map[string]bool{
		"INTENT_RECORDED": true, "RISK_APPROVED": true, "RISK_REJECTED": true,
		"SUBMIT_DISPATCHED": true, "SUBMIT_ACKNOWLEDGED": true, "SUBMIT_REJECTED": true,
		"FILL_RECORDED": true, "CANCEL_DISPATCHED": true, "CANCEL_ACKNOWLEDGED": true, "CANCEL_REJECTED": true,
	}
	if !allowed[event.Type] {
		return fmt.Errorf("unsupported order event %q", event.Type)
	}
	if event.Type == "SUBMIT_ACKNOWLEDGED" {
		if !orderAlias(event.ProviderOrderRef, "order") || event.ProviderExecutionRef != "" || event.Quantity != "" || event.Price != "" || event.OccurredAt != "" {
			return errors.New("submit acknowledgement payload is invalid")
		}
		return nil
	}
	if event.Type == "FILL_RECORDED" {
		if !orderAlias(event.ProviderOrderRef, "order") || !orderAlias(event.ProviderExecutionRef, "execution") ||
			!validOrderInteger(event.Quantity) || len(event.Price) == 0 || len(event.Price) > 64 {
			return errors.New("fill payload is invalid")
		}
		price, err := parseDecimal(event.Price)
		if err != nil || price.Sign() <= 0 {
			return errors.New("fill price must be a positive canonical decimal")
		}
		if _, err := time.Parse(time.RFC3339Nano, event.OccurredAt); err != nil {
			return errors.New("fill occurred_at is invalid")
		}
		return nil
	}
	if event.ProviderOrderRef != "" || event.ProviderExecutionRef != "" || event.Quantity != "" || event.Price != "" || event.OccurredAt != "" {
		return errors.New("order event has fields that are not valid for its type")
	}
	return nil
}

func validOrderInteger(raw string) bool {
	return len(raw) <= 32 && orderIntegerPattern.MatchString(raw)
}

func orderAlias(raw, kind string) bool {
	return orderAliasPattern.MatchString(raw) && strings.HasPrefix(raw, "kiwoom_"+kind+"_")
}

func safeOrderID(raw string) bool {
	if raw == "" || len(raw) > 128 || !utf8.ValidString(raw) {
		return false
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func bindProviderOrderRef(state *OrderState, ref string) error {
	if len(state.ProviderOrderRefs) == 0 {
		state.ProviderOrderRefs = append(state.ProviderOrderRefs, ref)
		return nil
	}
	if state.ProviderOrderRefs[0] != ref {
		return errors.New("provider order reference changed")
	}
	return nil
}

// ponytail: scan and replay one local account; add a validated projection only when measured order volume makes this hot.
func accountHasUnresolvedOrder(ctx context.Context, q orderQuerier, accountRef string) (bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT order_id FROM order_idempotency WHERE account_ref=? ORDER BY order_id`, accountRef)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var orderIDs []string
	for rows.Next() {
		var orderID string
		if err := rows.Scan(&orderID); err != nil {
			return false, err
		}
		orderIDs = append(orderIDs, orderID)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	for _, orderID := range orderIDs {
		state, err := loadOrderStateFrom(ctx, q, orderID)
		if err != nil {
			return false, err
		}
		if state.PendingAction != "" {
			return true, nil
		}
	}
	return false, nil
}

func sameProviderExecution(left, right OrderEvent) bool {
	left.EventID, right.EventID = "", ""
	leftJSON, _, leftErr := orderJSONHash(left)
	rightJSON, _, rightErr := orderJSONHash(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func orderJSONHash(value any) ([]byte, string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(raw)
	return raw, hex.EncodeToString(hash[:]), nil
}

func insertOrderEvent(ctx context.Context, tx *sql.Tx, event OrderEvent, recordedAt string) error {
	eventJSON, eventSHA, err := orderJSONHash(event)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO order_events(event_id,event_sha256,order_id,event_type,source,provider_order_ref,provider_execution_ref,event_json,recorded_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		event.EventID, eventSHA, event.OrderID, event.Type, event.Source, nil, nil, string(eventJSON), recordedAt)
	return err
}
