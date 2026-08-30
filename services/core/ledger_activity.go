package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

const defaultActivityLimit = 50

var errInvalidLedgerActivityCursor = errors.New("invalid ledger activity cursor")

type LedgerActivityPage struct {
	Source          string           `json:"source"`
	BrokerFreshness string           `json:"broker_freshness"`
	LedgerRevision  string           `json:"ledger_revision"`
	RecordedAt      string           `json:"recorded_at"`
	Events          []LedgerActivity `json:"events"`
	NextCursor      *string          `json:"next_cursor"`
}

type LedgerActivity struct {
	Type            string  `json:"type"`
	OccurredAt      string  `json:"occurred_at"`
	RecordedAt      string  `json:"recorded_at"`
	Symbol          *string `json:"symbol"`
	Quantity        *string `json:"quantity"`
	Price           *string `json:"price"`
	Fee             *string `json:"fee"`
	Currency        string  `json:"currency"`
	Amount          string  `json:"amount"`
	CounterCurrency *string `json:"counter_currency"`
	CounterAmount   *string `json:"counter_amount"`
	IsCorrection    bool    `json:"is_correction"`
}

type ledgerActivityCursor struct {
	Version          int    `json:"v"`
	LedgerRevision   int64  `json:"ledger_revision"`
	LedgerRecordedAt string `json:"ledger_recorded_at"`
	OccurredAt       string `json:"occurred_at"`
	Sequence         int64  `json:"sequence"`
}

type storedLedgerEvent struct {
	sequence                                           int64
	eventID, sourceEventID, accountID, typ, occurredAt string
	instrumentID, symbol, quantity, price, fee         string
	currency, amount, counterCurrency, counterAmount   string
	correctsSourceEventID, receiptID, recordedAt       string
}

func (s *Service) handleLedgerActivities(w http.ResponseWriter, r *http.Request) {
	limit, cursor, appErr := s.parseLedgerActivityQuery(r)
	if appErr != nil {
		writeError(w, appErr)
		return
	}
	result, err := s.ledgerActivities(r.Context(), limit, cursor)
	if err != nil {
		if errors.Is(err, errInvalidLedgerActivityCursor) {
			writeError(w, invalidActivityQuery())
			return
		}
		writeError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) parseLedgerActivityQuery(r *http.Request) (int, *ledgerActivityCursor, *appError) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, nil, invalidActivityQuery()
	}
	for key := range query {
		if key != "limit" && key != "cursor" {
			return 0, nil, invalidActivityQuery()
		}
	}
	limit := defaultActivityLimit
	if values, ok := query["limit"]; ok {
		if len(values) != 1 {
			return 0, nil, invalidActivityQuery()
		}
		parsed, err := strconv.Atoi(values[0])
		if err != nil || parsed < 1 || parsed > 100 || strconv.Itoa(parsed) != values[0] {
			return 0, nil, invalidActivityQuery()
		}
		limit = parsed
	}
	var cursor *ledgerActivityCursor
	if values, ok := query["cursor"]; ok {
		if len(values) != 1 {
			return 0, nil, invalidActivityQuery()
		}
		decoded, err := s.decodeLedgerActivityCursor(values[0])
		if err != nil {
			return 0, nil, invalidActivityQuery()
		}
		cursor = decoded
	}
	return limit, cursor, nil
}

func invalidActivityQuery() *appError {
	return &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "ledger activity query must match the contract"}}
}

func (s *Service) encodeLedgerActivityCursor(cursor ledgerActivityCursor) string {
	plaintext, _ := json.Marshal(cursor)
	block, _ := aes.NewCipher(s.activityCursorKey[:])
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	sealed := aead.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(sealed)
}

func (s *Service) decodeLedgerActivityCursor(raw string) (*ledgerActivityCursor, error) {
	if raw == "" || len(raw) > 1024 {
		return nil, errors.New("invalid cursor length")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > 768 {
		return nil, errors.New("invalid cursor encoding")
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return nil, errors.New("cursor encoding is not canonical")
	}
	block, err := aes.NewCipher(s.activityCursorKey[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(decoded) < aead.NonceSize() {
		return nil, errors.New("invalid cursor ciphertext")
	}
	plaintext, err := aead.Open(nil, decoded[:aead.NonceSize()], decoded[aead.NonceSize():], nil)
	if err != nil || len(plaintext) > 512 {
		return nil, errors.New("invalid cursor authentication")
	}
	var cursor ledgerActivityCursor
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("cursor must contain one object")
	}
	if cursor.Version != 1 || cursor.LedgerRevision < 1 || cursor.Sequence < 1 ||
		cursor.Sequence > cursor.LedgerRevision || !canonicalUTCString(cursor.LedgerRecordedAt) || !canonicalUTCString(cursor.OccurredAt) {
		return nil, errors.New("invalid cursor fields")
	}
	return &cursor, nil
}

func (s *Service) ledgerActivities(ctx context.Context, limit int, cursor *ledgerActivityCursor) (*LedgerActivityPage, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := snapshotFrom(ctx, tx); err != nil {
		return nil, err
	}
	currentRevision, currentRecordedAt, err := s.proveLedgerEvents(ctx, tx)
	if err != nil {
		return nil, err
	}
	pageRevision, pageRecordedAt := currentRevision, currentRecordedAt
	if cursor != nil {
		if cursor.LedgerRevision > currentRevision {
			return nil, errInvalidLedgerActivityCursor
		}
		var revisionRecordedAt, sequenceOccurredAt string
		if err := tx.QueryRowContext(ctx, `SELECT recorded_at FROM events WHERE sequence=?`, cursor.LedgerRevision).Scan(&revisionRecordedAt); err != nil {
			return nil, errInvalidLedgerActivityCursor
		}
		if err := tx.QueryRowContext(ctx, `SELECT occurred_at FROM events WHERE sequence=?`, cursor.Sequence).Scan(&sequenceOccurredAt); err != nil {
			return nil, errInvalidLedgerActivityCursor
		}
		if cursor.LedgerRecordedAt != revisionRecordedAt || cursor.OccurredAt != sequenceOccurredAt {
			return nil, errInvalidLedgerActivityCursor
		}
		pageRevision, pageRecordedAt = cursor.LedgerRevision, revisionRecordedAt
	}

	query := `SELECT sequence,type,occurred_at,recorded_at,COALESCE(symbol,''),COALESCE(quantity,''),COALESCE(price,''),COALESCE(fee,''),currency,amount,COALESCE(counter_currency,''),COALESCE(counter_amount,'')
		FROM events WHERE sequence<=?`
	args := []any{pageRevision}
	if cursor != nil {
		query += ` AND (occurred_at<? OR (occurred_at=? AND sequence<?))`
		args = append(args, cursor.OccurredAt, cursor.OccurredAt, cursor.Sequence)
	}
	query += ` ORDER BY occurred_at DESC,sequence DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	type item struct {
		sequence int64
		view     LedgerActivity
	}
	items := make([]item, 0, limit+1)
	for rows.Next() {
		var entry item
		var symbol, quantity, price, fee, counterCurrency, counterAmount string
		if err := rows.Scan(&entry.sequence, &entry.view.Type, &entry.view.OccurredAt, &entry.view.RecordedAt, &symbol, &quantity, &price, &fee, &entry.view.Currency, &entry.view.Amount, &counterCurrency, &counterAmount); err != nil {
			rows.Close()
			return nil, err
		}
		entry.view.Symbol = optionalText(symbol)
		entry.view.Quantity = optionalText(quantity)
		entry.view.Price = optionalText(price)
		entry.view.Fee = optionalText(fee)
		entry.view.CounterCurrency = optionalText(counterCurrency)
		entry.view.CounterAmount = optionalText(counterAmount)
		entry.view.IsCorrection = entry.view.Type == "CASH_VOID"
		items = append(items, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := &LedgerActivityPage{
		Source: "local_ledger", BrokerFreshness: "unverified", LedgerRevision: revision(pageRevision),
		RecordedAt: pageRecordedAt, Events: []LedgerActivity{}, NextCursor: nil,
	}
	if len(items) > limit {
		last := items[limit-1]
		next := s.encodeLedgerActivityCursor(ledgerActivityCursor{
			Version: 1, LedgerRevision: pageRevision, LedgerRecordedAt: pageRecordedAt,
			OccurredAt: last.view.OccurredAt, Sequence: last.sequence,
		})
		result.NextCursor = &next
		items = items[:limit]
	}
	for _, entry := range items {
		result.Events = append(result.Events, entry.view)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) proveLedgerEvents(ctx context.Context, tx *sql.Tx) (int64, string, error) {
	var revisionValue int64
	var ledgerRecordedAt string
	if err := tx.QueryRowContext(ctx, `SELECT revision,recorded_at FROM ledger_meta WHERE singleton=1`).Scan(&revisionValue, &ledgerRecordedAt); err != nil {
		return 0, "", err
	}
	if !canonicalUTCString(ledgerRecordedAt) {
		return 0, "", errors.New("ledger recorded_at is not canonical UTC")
	}
	rows, err := tx.QueryContext(ctx, `SELECT sequence,event_id,source_event_id,account_id,type,occurred_at,COALESCE(instrument_id,''),COALESCE(symbol,''),COALESCE(quantity,''),COALESCE(price,''),COALESCE(fee,''),currency,amount,COALESCE(counter_currency,''),COALESCE(counter_amount,''),COALESCE(corrects_source_event_id,''),receipt_id,recorded_at FROM events ORDER BY sequence`)
	if err != nil {
		return 0, "", err
	}
	stored := make([]storedLedgerEvent, 0)
	for rows.Next() {
		var event storedLedgerEvent
		if err := rows.Scan(&event.sequence, &event.eventID, &event.sourceEventID, &event.accountID, &event.typ, &event.occurredAt, &event.instrumentID, &event.symbol, &event.quantity, &event.price, &event.fee, &event.currency, &event.amount, &event.counterCurrency, &event.counterAmount, &event.correctsSourceEventID, &event.receiptID, &event.recordedAt); err != nil {
			rows.Close()
			return 0, "", err
		}
		if event.sequence != int64(len(stored)+1) {
			rows.Close()
			return 0, "", errors.New("ledger event sequence is not contiguous")
		}
		stored = append(stored, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, "", err
	}
	if err := rows.Close(); err != nil {
		return 0, "", err
	}
	if int64(len(stored)) != revisionValue || (len(stored) > 0 && stored[len(stored)-1].sequence != revisionValue) {
		return 0, "", errors.New("ledger revision does not match append-only events")
	}
	for i := range stored {
		if err := s.validateStoredLedgerEvent(ctx, tx, &stored[i]); err != nil {
			return 0, "", err
		}
	}
	activityRecordedAt := zeroTime
	if len(stored) > 0 {
		activityRecordedAt = stored[len(stored)-1].recordedAt
	}
	return revisionValue, activityRecordedAt, nil
}

func (s *Service) validateStoredLedgerEvent(ctx context.Context, tx *sql.Tx, stored *storedLedgerEvent) error {
	if stored.eventID == "" || stored.receiptID == "" || !canonicalUTCString(stored.recordedAt) {
		return errors.New("stored ledger event metadata is invalid")
	}
	values := map[string]string{
		"source_event_id": stored.sourceEventID, "account_id": stored.accountID, "type": stored.typ,
		"occurred_at": stored.occurredAt, "symbol": stored.symbol, "quantity": stored.quantity,
		"price": stored.price, "fee": stored.fee, "currency": stored.currency, "amount": stored.amount,
		"counter_currency": stored.counterCurrency, "counter_amount": stored.counterAmount,
		"corrects_source_event_id": stored.correctsSourceEventID,
	}
	validator := &Service{id: func(string) string { return stored.eventID }}
	normalized, fieldErrors := validator.normalize(nil, func(_ []string, name string) string { return values[name] })
	want := &Transaction{
		EventID: stored.eventID, SourceEventID: stored.sourceEventID, AccountID: stored.accountID,
		Type: stored.typ, OccurredAt: stored.occurredAt, InstrumentID: stored.instrumentID, Symbol: stored.symbol,
		Quantity: stored.quantity, Price: stored.price, Fee: stored.fee, Currency: stored.currency, Amount: stored.amount,
		CounterCurrency: stored.counterCurrency, CounterAmount: stored.counterAmount, CorrectsSourceEventID: stored.correctsSourceEventID,
	}
	if len(fieldErrors) != 0 || !sameTransaction(normalized, want) {
		return errors.New("stored ledger event is not canonical")
	}
	if stored.typ == "CASH_VOID" {
		if _, validationErr, err := validateCashVoid(ctx, tx, want, false); err != nil {
			return err
		} else if validationErr != nil {
			return errors.New("stored cash void failed validation")
		}
	}
	return nil
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
