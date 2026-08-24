package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"
)

const (
	syntheticExecutionLeaseTTL  = 30 * time.Second
	syntheticRiskPolicyVersion  = "credential_free_buy_v1"
	syntheticMaxOrderQuantity   = int64(10)
	syntheticMaxOrderNotional   = int64(1_000_000)
	syntheticMaxAccountNotional = int64(1_000_000)
)

type ExecutionAuthorityState struct {
	AccountRef     string `json:"account_ref"`
	Armed          bool   `json:"armed"`
	LeaseOwner     string `json:"lease_owner,omitempty"`
	FencingToken   int64  `json:"fencing_token"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
}

type executionAuthorityRecord struct {
	EventID        string `json:"event_id"`
	AccountRef     string `json:"account_ref"`
	Armed          bool   `json:"armed"`
	LeaseOwner     string `json:"lease_owner,omitempty"`
	FencingToken   int64  `json:"fencing_token"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
	ReasonCode     string `json:"reason_code"`
	RecordedAt     string `json:"recorded_at"`
}

type executionAuthoritySnapshot struct {
	ExecutionAuthorityState
	EventID string
	count   int
}

type riskReservationRecord struct {
	ReservationID    string `json:"reservation_id"`
	OrderID          string `json:"order_id"`
	AccountRef       string `json:"account_ref"`
	PolicyVersion    string `json:"policy_version"`
	AuthorityEventID string `json:"authority_event_id"`
	FencingToken     int64  `json:"fencing_token"`
	Quantity         string `json:"quantity"`
	LimitPrice       string `json:"limit_price"`
	LimitNotional    string `json:"limit_notional"`
	RiskEventID      string `json:"risk_event_id"`
	DispatchEventID  string `json:"dispatch_event_id"`
	ReservedAt       string `json:"reserved_at"`
}

func (s *Service) setSyntheticExecutionArmed(ctx context.Context, accountRef string, armed bool) (*ExecutionAuthorityState, error) {
	if !orderAlias(accountRef, "account") {
		return nil, errors.New("execution account reference is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	current, err := loadExecutionAuthoritySnapshot(ctx, tx, accountRef)
	if err != nil {
		return nil, err
	}
	if current.Armed == armed {
		state := current.ExecutionAuthorityState
		return &state, nil
	}
	now := s.now().UTC()
	reason := "manual_halt"
	if armed {
		reason = "manual_arm"
	}
	record := executionAuthorityRecord{
		EventID: s.id("execution_authority"), AccountRef: accountRef, Armed: armed,
		FencingToken: current.FencingToken + 1, ReasonCode: reason, RecordedAt: now.Format(time.RFC3339Nano),
	}
	if err := insertExecutionAuthorityRecord(ctx, tx, record); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return authorityState(record), nil
}

func (s *Service) acquireSyntheticExecutionLease(ctx context.Context, accountRef string) (*ExecutionAuthorityState, error) {
	if !orderAlias(accountRef, "account") {
		return nil, errors.New("execution account reference is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	current, err := loadExecutionAuthoritySnapshot(ctx, tx, accountRef)
	if err != nil {
		return nil, err
	}
	if !current.Armed {
		return nil, errors.New("synthetic execution is halted")
	}
	now := s.now().UTC()
	if current.LeaseOwner != "" {
		expires, ok := canonicalUTCTime(current.LeaseExpiresAt)
		if !ok {
			return nil, errors.New("execution lease expiry is invalid")
		}
		if now.Before(expires) {
			if current.LeaseOwner != s.executionOwner {
				return nil, errors.New("synthetic execution lease is owned by another process")
			}
			state := current.ExecutionAuthorityState
			return &state, nil
		}
	}
	record := executionAuthorityRecord{
		EventID: s.id("execution_authority"), AccountRef: accountRef, Armed: true, LeaseOwner: s.executionOwner,
		FencingToken: current.FencingToken + 1, LeaseExpiresAt: now.Add(syntheticExecutionLeaseTTL).Format(time.RFC3339Nano),
		ReasonCode: "lease_acquired", RecordedAt: now.Format(time.RFC3339Nano),
	}
	if err := insertExecutionAuthorityRecord(ctx, tx, record); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return authorityState(record), nil
}

func (s *Service) authorizeSyntheticDispatch(ctx context.Context, orderID string, fencingToken int64) (*OrderState, error) {
	state, _, err := s.authorizeSyntheticDispatchOnce(ctx, orderID, fencingToken)
	return state, err
}

func (s *Service) authorizeSyntheticDispatchOnce(ctx context.Context, orderID string, fencingToken int64) (*OrderState, bool, error) {
	if !safeOrderID(orderID) || fencingToken <= 0 {
		return nil, false, errors.New("execution authorization identifiers are invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	reservation, found, err := loadRiskReservationByOrder(ctx, tx, orderID)
	if err != nil {
		return nil, false, err
	}
	if found {
		state, err := validateAuthorizedReservation(ctx, tx, reservation)
		return state, false, err
	}
	state, err := loadOrderStateFrom(ctx, tx, orderID)
	if err != nil {
		return nil, false, err
	}
	if state.Status != "RECORDED" || state.PendingAction != "" {
		return nil, false, errors.New("execution authorization requires a recorded order")
	}
	intent, err := loadOrderIntentFrom(ctx, tx, orderID)
	if err != nil {
		return nil, false, err
	}
	if err := validateStrategyOrderSelection(ctx, tx, intent); err != nil {
		return nil, false, err
	}
	notional, notionalValue, err := validateSyntheticBuyPolicy(intent)
	if err != nil {
		return nil, false, err
	}
	authority, err := loadExecutionAuthoritySnapshot(ctx, tx, intent.AccountRef)
	if err != nil {
		return nil, false, err
	}
	now := s.now().UTC()
	expires, expiryOK := canonicalUTCTime(authority.LeaseExpiresAt)
	if !authority.Armed || authority.LeaseOwner != s.executionOwner || authority.FencingToken != fencingToken ||
		!expiryOK || !now.Before(expires) {
		return nil, false, errors.New("execution authority is halted, stale, expired, or owned by another process")
	}
	blocked, err := accountHasUnresolvedOrder(ctx, tx, intent.AccountRef)
	if err != nil {
		return nil, false, err
	}
	if blocked {
		return nil, false, errors.New("account has an unresolved order command")
	}
	active, err := activeReservedNotional(ctx, tx, intent.AccountRef)
	if err != nil {
		return nil, false, err
	}
	if active.Add(active, notionalValue).Cmp(big.NewRat(syntheticMaxAccountNotional, 1)) > 0 {
		return nil, false, errors.New("active synthetic BUY reservations exceed the fixed account limit")
	}
	recordedAt := now.Format(time.RFC3339Nano)
	reservation = riskReservationRecord{
		ReservationID: s.id("risk_reservation"), OrderID: orderID, AccountRef: intent.AccountRef,
		PolicyVersion: syntheticRiskPolicyVersion, AuthorityEventID: authority.EventID, FencingToken: fencingToken,
		Quantity: intent.Quantity, LimitPrice: intent.LimitPrice, LimitNotional: notional,
		RiskEventID: s.id("order_event"), DispatchEventID: s.id("order_event"), ReservedAt: recordedAt,
	}
	if err := insertRiskReservation(ctx, tx, reservation); err != nil {
		return nil, false, err
	}
	risk := authorizedOrderEvent(reservation.RiskEventID, orderID, "RISK_APPROVED", reservation)
	dispatch := authorizedOrderEvent(reservation.DispatchEventID, orderID, "SUBMIT_DISPATCHED", reservation)
	if err := validateOrderEvent(risk); err != nil {
		return nil, false, err
	}
	state, err = appendOrderEventTx(ctx, tx, risk, recordedAt)
	if err != nil || state.Status != "READY" {
		return nil, false, fmt.Errorf("append risk approval: %w", err)
	}
	if err := validateOrderEvent(dispatch); err != nil {
		return nil, false, err
	}
	state, err = appendOrderEventTx(ctx, tx, dispatch, recordedAt)
	if err != nil || state.Status != "SUBMIT_UNKNOWN" || state.PendingAction != "SUBMIT" {
		return nil, false, fmt.Errorf("append submit dispatch: %w", err)
	}
	var riskSequence, dispatchSequence int64
	if err := tx.QueryRowContext(ctx, `SELECT sequence FROM order_events WHERE event_id=?`, reservation.RiskEventID).Scan(&riskSequence); err != nil {
		return nil, false, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT sequence FROM order_events WHERE event_id=?`, reservation.DispatchEventID).Scan(&dispatchSequence); err != nil {
		return nil, false, err
	}
	if dispatchSequence != riskSequence+1 {
		return nil, false, errors.New("authorized order events are not consecutive")
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return state, true, nil
}

func validateSyntheticBuyPolicy(intent OrderIntent) (string, *big.Rat, error) {
	if intent.Provider != "kiwoom" || (intent.Mode != "synthetic" && intent.Mode != "paper") || intent.Exchange != "KRX" || intent.Currency != "KRW" ||
		intent.OrderType != "LIMIT" || intent.Side != "BUY" || (intent.Symbol != "005930" && intent.Symbol != "000660") {
		return "", nil, errors.New("order is outside the fixed credential-free synthetic BUY policy")
	}
	quantity, ok := new(big.Int).SetString(intent.Quantity, 10)
	if !ok || quantity.Sign() <= 0 || quantity.Cmp(big.NewInt(syntheticMaxOrderQuantity)) > 0 {
		return "", nil, errors.New("order quantity exceeds the fixed synthetic BUY limit")
	}
	price, err := parseDecimal(intent.LimitPrice)
	if err != nil || price.Sign() <= 0 {
		return "", nil, errors.New("order price is invalid")
	}
	notional := new(big.Rat).Mul(new(big.Rat).SetInt(quantity), price)
	if notional.Cmp(big.NewRat(syntheticMaxOrderNotional, 1)) > 0 {
		return "", nil, errors.New("order notional exceeds the fixed synthetic BUY limit")
	}
	formatted, err := formatDecimal(notional)
	return formatted, notional, err
}

func activeReservedNotional(ctx context.Context, q orderQuerier, accountRef string) (*big.Rat, error) {
	rows, err := q.QueryContext(ctx, `SELECT order_id FROM risk_reservations WHERE account_ref=? ORDER BY sequence`, accountRef)
	if err != nil {
		return nil, err
	}
	var orderIDs []string
	for rows.Next() {
		var orderID string
		if err := rows.Scan(&orderID); err != nil {
			rows.Close()
			return nil, err
		}
		orderIDs = append(orderIDs, orderID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	total := new(big.Rat)
	for _, orderID := range orderIDs {
		reservation, found, err := loadRiskReservationByOrder(ctx, q, orderID)
		if err != nil || !found {
			return nil, fmt.Errorf("load risk reservation: %w", err)
		}
		state, err := validateAuthorizedReservation(ctx, q, reservation)
		if err != nil {
			return nil, err
		}
		if reservationIsActive(state) {
			value, _ := parseDecimal(reservation.LimitNotional)
			total.Add(total, value)
		}
	}
	return total, nil
}

func reservationIsActive(state *OrderState) bool {
	if state.PendingAction != "" {
		return true
	}
	switch state.Status {
	case "SUBMIT_UNKNOWN", "OPEN", "PARTIALLY_FILLED", "CANCEL_UNKNOWN":
		return true
	default:
		return false
	}
}

func validateAuthorizedReservation(ctx context.Context, q orderQuerier, reservation riskReservationRecord) (*OrderState, error) {
	intent, err := loadOrderIntentFrom(ctx, q, reservation.OrderID)
	if err != nil {
		return nil, err
	}
	notional, _, err := validateSyntheticBuyPolicy(intent)
	if err != nil || reservation.AccountRef != intent.AccountRef || reservation.Quantity != intent.Quantity ||
		reservation.LimitPrice != intent.LimitPrice || reservation.LimitNotional != notional {
		return nil, errors.New("risk reservation does not match its order intent")
	}
	authority, err := loadExecutionAuthorityRecordByID(ctx, q, reservation.AuthorityEventID)
	if err != nil || authority.AccountRef != reservation.AccountRef || authority.FencingToken != reservation.FencingToken ||
		authority.ReasonCode != "lease_acquired" || authority.LeaseOwner == "" {
		return nil, errors.New("risk reservation does not match its execution authority")
	}
	if _, err := loadExecutionAuthoritySnapshot(ctx, q, reservation.AccountRef); err != nil {
		return nil, err
	}
	riskSequence, risk, err := loadAuthorizedOrderEvent(ctx, q, reservation.RiskEventID)
	if err != nil {
		return nil, err
	}
	dispatchSequence, dispatch, err := loadAuthorizedOrderEvent(ctx, q, reservation.DispatchEventID)
	if err != nil {
		return nil, err
	}
	for _, event := range []OrderEvent{risk, dispatch} {
		if event.OrderID != reservation.OrderID || event.Source != "synthetic" || event.RiskReservationID != reservation.ReservationID ||
			event.RiskPolicyVersion != reservation.PolicyVersion || event.FencingToken != reservation.FencingToken {
			return nil, errors.New("authorized order event does not match its reservation")
		}
	}
	if risk.Type != "RISK_APPROVED" || dispatch.Type != "SUBMIT_DISPATCHED" || dispatchSequence != riskSequence+1 {
		return nil, errors.New("risk reservation is missing its consecutive authorized event pair")
	}
	state, err := loadOrderStateFrom(ctx, q, reservation.OrderID)
	if err != nil {
		return nil, err
	}
	switch state.Status {
	case "SUBMIT_UNKNOWN", "OPEN", "PARTIALLY_FILLED", "CANCEL_UNKNOWN", "REJECTED", "FILLED", "CANCELED":
		return state, nil
	default:
		return nil, errors.New("reserved order is not in an authorized post-dispatch state")
	}
}

func authorizedOrderEvent(eventID, orderID, eventType string, reservation riskReservationRecord) OrderEvent {
	return OrderEvent{
		EventID: eventID, OrderID: orderID, Type: eventType, Source: "synthetic",
		RiskReservationID: reservation.ReservationID, RiskPolicyVersion: reservation.PolicyVersion,
		FencingToken: reservation.FencingToken,
	}
}

func authorityState(record executionAuthorityRecord) *ExecutionAuthorityState {
	return &ExecutionAuthorityState{
		AccountRef: record.AccountRef, Armed: record.Armed, LeaseOwner: record.LeaseOwner,
		FencingToken: record.FencingToken, LeaseExpiresAt: record.LeaseExpiresAt,
	}
}

func insertExecutionAuthorityRecord(ctx context.Context, tx *sql.Tx, record executionAuthorityRecord) error {
	if err := validateExecutionAuthorityRecordBasic(record); err != nil {
		return err
	}
	raw, hash, err := orderJSONHash(record)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO execution_authority_events(event_id,account_ref,armed,lease_owner,fencing_token,lease_expires_at,reason_code,record_sha256,record_json,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		record.EventID, record.AccountRef, boolInt(record.Armed), nullable(record.LeaseOwner), record.FencingToken, nullable(record.LeaseExpiresAt),
		record.ReasonCode, hash, string(raw), record.RecordedAt)
	return err
}

func loadExecutionAuthoritySnapshot(ctx context.Context, q orderQuerier, accountRef string) (executionAuthoritySnapshot, error) {
	rows, err := q.QueryContext(ctx, `SELECT event_id,armed,lease_owner,fencing_token,lease_expires_at,reason_code,record_sha256,record_json,recorded_at
		FROM execution_authority_events WHERE account_ref=? ORDER BY sequence`, accountRef)
	if err != nil {
		return executionAuthoritySnapshot{}, err
	}
	defer rows.Close()
	var snapshot executionAuthoritySnapshot
	var previous *executionAuthorityRecord
	for rows.Next() {
		record, err := scanExecutionAuthorityRecord(rows, accountRef)
		if err != nil {
			return executionAuthoritySnapshot{}, err
		}
		if err := validateExecutionAuthorityRecord(record, previous); err != nil {
			return executionAuthoritySnapshot{}, err
		}
		copy := record
		previous = &copy
		snapshot.ExecutionAuthorityState = *authorityState(record)
		snapshot.EventID = record.EventID
		snapshot.count++
	}
	if err := rows.Err(); err != nil {
		return executionAuthoritySnapshot{}, err
	}
	if snapshot.count == 0 {
		snapshot.AccountRef = accountRef
	}
	return snapshot, nil
}

type rowScanner interface{ Scan(...any) error }

func scanExecutionAuthorityRecord(row rowScanner, accountRef string) (executionAuthorityRecord, error) {
	var eventID, reason, hash, raw, recordedAt string
	var armed int
	var owner, expires sql.NullString
	var token int64
	if err := row.Scan(&eventID, &armed, &owner, &token, &expires, &reason, &hash, &raw, &recordedAt); err != nil {
		return executionAuthorityRecord{}, err
	}
	var record executionAuthorityRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return executionAuthorityRecord{}, err
	}
	canonical, actualHash, err := orderJSONHash(record)
	if err != nil {
		return executionAuthorityRecord{}, err
	}
	if string(canonical) != raw || actualHash != hash || record.EventID != eventID || record.AccountRef != accountRef ||
		boolInt(record.Armed) != armed || record.LeaseOwner != owner.String || (record.LeaseOwner != "") != owner.Valid ||
		record.FencingToken != token || record.LeaseExpiresAt != expires.String || (record.LeaseExpiresAt != "") != expires.Valid ||
		record.ReasonCode != reason || record.RecordedAt != recordedAt {
		return executionAuthorityRecord{}, errors.New("execution authority record metadata or hash mismatch")
	}
	return record, nil
}

func validateExecutionAuthorityRecord(record executionAuthorityRecord, previous *executionAuthorityRecord) error {
	if err := validateExecutionAuthorityRecordBasic(record); err != nil {
		return err
	}
	recordedAt, _ := canonicalUTCTime(record.RecordedAt)
	if previous == nil {
		if record.FencingToken != 1 || record.ReasonCode != "manual_arm" || !record.Armed || record.LeaseOwner != "" {
			return errors.New("execution authority history does not start with an explicit arm event")
		}
		return nil
	}
	if record.FencingToken != previous.FencingToken+1 {
		return errors.New("execution authority fencing token is not monotonic")
	}
	switch record.ReasonCode {
	case "manual_arm":
		if previous.Armed || !record.Armed || record.LeaseOwner != "" {
			return errors.New("execution authority arm transition is invalid")
		}
	case "manual_halt":
		if !previous.Armed || record.Armed || record.LeaseOwner != "" {
			return errors.New("execution authority halt transition is invalid")
		}
	case "lease_acquired":
		if !previous.Armed || !record.Armed || record.LeaseOwner == "" {
			return errors.New("execution authority lease transition is invalid")
		}
		if previous.LeaseOwner != "" {
			previousExpiry, ok := canonicalUTCTime(previous.LeaseExpiresAt)
			if !ok || recordedAt.Before(previousExpiry) {
				return errors.New("execution authority replaced an unexpired lease")
			}
		}
	default:
		return errors.New("execution authority reason is invalid")
	}
	return nil
}

func validateExecutionAuthorityRecordBasic(record executionAuthorityRecord) error {
	recordedAt, ok := canonicalUTCTime(record.RecordedAt)
	if !safeOrderID(record.EventID) || !orderAlias(record.AccountRef, "account") || !ok || record.FencingToken <= 0 ||
		(record.LeaseOwner == "") != (record.LeaseExpiresAt == "") || (!record.Armed && record.LeaseOwner != "") {
		return errors.New("execution authority record is invalid")
	}
	if record.LeaseOwner != "" {
		expires, ok := canonicalUTCTime(record.LeaseExpiresAt)
		if !safeOrderID(record.LeaseOwner) || !ok || !recordedAt.Before(expires) {
			return errors.New("execution authority lease is invalid")
		}
	}
	switch record.ReasonCode {
	case "manual_arm", "manual_halt", "lease_acquired":
	default:
		return errors.New("execution authority reason is invalid")
	}
	return nil
}

func loadExecutionAuthorityRecordByID(ctx context.Context, q orderQuerier, eventID string) (executionAuthorityRecord, error) {
	var accountRef string
	if err := q.QueryRowContext(ctx, `SELECT account_ref FROM execution_authority_events WHERE event_id=?`, eventID).Scan(&accountRef); err != nil {
		return executionAuthorityRecord{}, err
	}
	row := q.QueryRowContext(ctx, `SELECT event_id,armed,lease_owner,fencing_token,lease_expires_at,reason_code,record_sha256,record_json,recorded_at
		FROM execution_authority_events WHERE event_id=?`, eventID)
	record, err := scanExecutionAuthorityRecord(row, accountRef)
	if err != nil {
		return executionAuthorityRecord{}, err
	}
	if err := validateExecutionAuthorityRecordStandalone(record); err != nil {
		return executionAuthorityRecord{}, err
	}
	return record, nil
}

func validateExecutionAuthorityRecordStandalone(record executionAuthorityRecord) error {
	if record.ReasonCode != "lease_acquired" {
		return errors.New("reservation authority is not a lease")
	}
	recordedAt, ok := canonicalUTCTime(record.RecordedAt)
	expires, expiryOK := canonicalUTCTime(record.LeaseExpiresAt)
	if !safeOrderID(record.EventID) || !orderAlias(record.AccountRef, "account") || !safeOrderID(record.LeaseOwner) ||
		!record.Armed || record.FencingToken <= 0 || !ok || !expiryOK || !recordedAt.Before(expires) {
		return errors.New("reservation authority record is invalid")
	}
	return nil
}

func insertRiskReservation(ctx context.Context, tx *sql.Tx, record riskReservationRecord) error {
	if err := validateRiskReservationRecord(record); err != nil {
		return err
	}
	raw, hash, err := orderJSONHash(record)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO risk_reservations(reservation_id,order_id,account_ref,policy_version,authority_event_id,fencing_token,quantity,limit_price,limit_notional,risk_event_id,dispatch_event_id,record_sha256,record_json,reserved_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.ReservationID, record.OrderID, record.AccountRef, record.PolicyVersion, record.AuthorityEventID, record.FencingToken,
		record.Quantity, record.LimitPrice, record.LimitNotional, record.RiskEventID, record.DispatchEventID, hash, string(raw), record.ReservedAt)
	return err
}

func loadRiskReservationByOrder(ctx context.Context, q orderQuerier, orderID string) (riskReservationRecord, bool, error) {
	row := q.QueryRowContext(ctx, `SELECT reservation_id,account_ref,policy_version,authority_event_id,fencing_token,quantity,limit_price,limit_notional,risk_event_id,dispatch_event_id,record_sha256,record_json,reserved_at
		FROM risk_reservations WHERE order_id=?`, orderID)
	var reservationID, accountRef, policy, authorityEventID, quantity, price, notional, riskID, dispatchID, hash, raw, reservedAt string
	var token int64
	if err := row.Scan(&reservationID, &accountRef, &policy, &authorityEventID, &token, &quantity, &price, &notional, &riskID, &dispatchID, &hash, &raw, &reservedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return riskReservationRecord{}, false, nil
		}
		return riskReservationRecord{}, false, err
	}
	var record riskReservationRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return riskReservationRecord{}, false, err
	}
	canonical, actualHash, err := orderJSONHash(record)
	if err != nil {
		return riskReservationRecord{}, false, err
	}
	if string(canonical) != raw || actualHash != hash || record.ReservationID != reservationID || record.OrderID != orderID ||
		record.AccountRef != accountRef || record.PolicyVersion != policy || record.AuthorityEventID != authorityEventID ||
		record.FencingToken != token || record.Quantity != quantity || record.LimitPrice != price || record.LimitNotional != notional ||
		record.RiskEventID != riskID || record.DispatchEventID != dispatchID || record.ReservedAt != reservedAt {
		return riskReservationRecord{}, false, errors.New("risk reservation metadata or hash mismatch")
	}
	if err := validateRiskReservationRecord(record); err != nil {
		return riskReservationRecord{}, false, err
	}
	return record, true, nil
}

func validateRiskReservationRecord(record riskReservationRecord) error {
	_, reservedOK := canonicalUTCTime(record.ReservedAt)
	if !safeOrderID(record.ReservationID) || !safeOrderID(record.OrderID) || !orderAlias(record.AccountRef, "account") ||
		record.PolicyVersion != syntheticRiskPolicyVersion || !safeOrderID(record.AuthorityEventID) || record.FencingToken <= 0 ||
		!validOrderInteger(record.Quantity) || !positiveCanonicalDecimal(record.LimitPrice) || !positiveCanonicalDecimal(record.LimitNotional) ||
		!safeOrderID(record.RiskEventID) || !safeOrderID(record.DispatchEventID) || record.RiskEventID == record.DispatchEventID || !reservedOK {
		return errors.New("risk reservation is invalid")
	}
	return nil
}

func loadAuthorizedOrderEvent(ctx context.Context, q orderQuerier, eventID string) (int64, OrderEvent, error) {
	var sequence int64
	var eventSHA, orderID, eventType, source, raw, reservationID string
	if err := q.QueryRowContext(ctx, `SELECT sequence,event_sha256,order_id,event_type,source,event_json,authority_reservation_id FROM order_events WHERE event_id=?`, eventID).
		Scan(&sequence, &eventSHA, &orderID, &eventType, &source, &raw, &reservationID); err != nil {
		return 0, OrderEvent{}, err
	}
	var event OrderEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return 0, OrderEvent{}, err
	}
	canonical, actualSHA, err := orderJSONHash(event)
	if err != nil || string(canonical) != raw || actualSHA != eventSHA || event.EventID != eventID || event.OrderID != orderID ||
		event.Type != eventType || event.Source != source || event.RiskReservationID != reservationID {
		return 0, OrderEvent{}, errors.New("authorized order event metadata mismatch")
	}
	if err := validateOrderEvent(event); err != nil {
		return 0, OrderEvent{}, err
	}
	return sequence, event, nil
}

func proveExecutionAuthorityRecovery(ctx context.Context, q orderQuerier) (string, int, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence,account_ref,event_id,armed,lease_owner,fencing_token,lease_expires_at,reason_code,record_sha256,record_json,recorded_at
		FROM execution_authority_events ORDER BY sequence`)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	previous := map[string]*executionAuthorityRecord{}
	count := 0
	for rows.Next() {
		var sequence, token int64
		var accountRef, eventID, reason, recordSHA, raw, recordedAt string
		var armed int
		var owner, expires sql.NullString
		if err := rows.Scan(&sequence, &accountRef, &eventID, &armed, &owner, &token, &expires, &reason, &recordSHA, &raw, &recordedAt); err != nil {
			return "", 0, err
		}
		var record executionAuthorityRecord
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return "", 0, err
		}
		canonical, actualSHA, err := orderJSONHash(record)
		if err != nil || string(canonical) != raw || actualSHA != recordSHA || record.EventID != eventID ||
			record.AccountRef != accountRef || boolInt(record.Armed) != armed || record.LeaseOwner != owner.String ||
			(record.LeaseOwner != "") != owner.Valid || record.FencingToken != token || record.LeaseExpiresAt != expires.String ||
			(record.LeaseExpiresAt != "") != expires.Valid || record.ReasonCode != reason || record.RecordedAt != recordedAt {
			return "", 0, errors.New("execution authority recovery metadata or hash mismatch")
		}
		if err := validateExecutionAuthorityRecord(record, previous[accountRef]); err != nil {
			return "", 0, err
		}
		copy := record
		previous[accountRef] = &copy
		if err := encoder.Encode([]any{"execution_authority_events", sequence, accountRef, eventID, armed, owner, token, expires, reason, recordSHA, raw, recordedAt}); err != nil {
			return "", 0, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), count, nil
}

func proveRiskReservationRecovery(ctx context.Context, q orderQuerier) (string, int, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence,order_id,reservation_id,account_ref,policy_version,authority_event_id,fencing_token,quantity,limit_price,limit_notional,risk_event_id,dispatch_event_id,record_sha256,record_json,reserved_at
		FROM risk_reservations ORDER BY sequence`)
	if err != nil {
		return "", 0, err
	}
	var orderIDs []string
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	count := 0
	for rows.Next() {
		var sequence, token int64
		var orderID, reservationID, accountRef, policy, authorityID, quantity, price, notional, riskID, dispatchID, recordSHA, raw, reservedAt string
		if err := rows.Scan(&sequence, &orderID, &reservationID, &accountRef, &policy, &authorityID, &token, &quantity, &price, &notional, &riskID, &dispatchID, &recordSHA, &raw, &reservedAt); err != nil {
			rows.Close()
			return "", 0, err
		}
		var record riskReservationRecord
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			rows.Close()
			return "", 0, err
		}
		canonical, actualSHA, err := orderJSONHash(record)
		if err != nil || string(canonical) != raw || actualSHA != recordSHA || record.OrderID != orderID ||
			record.ReservationID != reservationID || record.AccountRef != accountRef || record.PolicyVersion != policy ||
			record.AuthorityEventID != authorityID || record.FencingToken != token || record.Quantity != quantity ||
			record.LimitPrice != price || record.LimitNotional != notional || record.RiskEventID != riskID ||
			record.DispatchEventID != dispatchID || record.ReservedAt != reservedAt {
			rows.Close()
			return "", 0, errors.New("risk reservation recovery metadata or hash mismatch")
		}
		if err := validateRiskReservationRecord(record); err != nil {
			rows.Close()
			return "", 0, err
		}
		if err := encoder.Encode([]any{"risk_reservations", sequence, orderID, reservationID, accountRef, policy, authorityID, token,
			quantity, price, notional, riskID, dispatchID, recordSHA, raw, reservedAt}); err != nil {
			rows.Close()
			return "", 0, err
		}
		orderIDs = append(orderIDs, orderID)
		count++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", 0, err
	}
	if err := rows.Close(); err != nil {
		return "", 0, err
	}
	for _, orderID := range orderIDs {
		record, found, err := loadRiskReservationByOrder(ctx, q, orderID)
		if err != nil || !found {
			return "", 0, fmt.Errorf("load risk reservation recovery row: %w", err)
		}
		if _, err := validateAuthorizedReservation(ctx, q, record); err != nil {
			return "", 0, err
		}
	}
	var taggedEvents, taggedReservations int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(DISTINCT authority_reservation_id) FROM order_events WHERE authority_reservation_id IS NOT NULL`).Scan(&taggedEvents, &taggedReservations); err != nil {
		return "", 0, err
	}
	if taggedEvents != count*2 || taggedReservations != count {
		return "", 0, errors.New("authorized order events do not match durable risk reservations")
	}
	return hex.EncodeToString(hash.Sum(nil)), count, nil
}
