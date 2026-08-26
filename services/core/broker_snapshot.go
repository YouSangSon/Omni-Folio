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
	"regexp"
	"sort"
	"time"
)

var (
	maskedKiwoomAccountPattern = regexp.MustCompile(`^\*{6}[0-9]{4}$`)
	ledgerRevisionPattern      = regexp.MustCompile(`^rev_[0-9]{10}$`)
)

type BrokerPositionDifference struct {
	Symbol         string `json:"symbol"`
	BrokerQuantity string `json:"broker_quantity"`
	LedgerQuantity string `json:"ledger_quantity"`
	Difference     string `json:"difference"`
	Match          bool   `json:"match"`
}

type BrokerKnownGoodSnapshot struct {
	SnapshotID          string                     `json:"snapshot_id"`
	ReconciliationID    string                     `json:"reconciliation_id"`
	Provider            string                     `json:"provider"`
	Environment         KiwoomEnvironment          `json:"environment"`
	Exchange            KiwoomExchange             `json:"exchange"`
	AccountRef          string                     `json:"account_ref"`
	LedgerAccountID     string                     `json:"ledger_account_id"`
	FetchedAt           string                     `json:"fetched_at"`
	RecordedAt          string                     `json:"recorded_at"`
	SnapshotSHA256      string                     `json:"snapshot_sha256"`
	LedgerRevision      string                     `json:"ledger_revision"`
	AllPositionsMatch   bool                       `json:"all_positions_match"`
	PositionDifferences []BrokerPositionDifference `json:"position_differences"`
	Snapshot            KiwoomSnapshot             `json:"snapshot"`
}

type BrokerReconciliationView struct {
	Provider            string                     `json:"provider"`
	Environment         KiwoomEnvironment          `json:"environment"`
	Exchange            KiwoomExchange             `json:"exchange"`
	Freshness           string                     `json:"freshness"`
	FetchedAt           string                     `json:"fetched_at"`
	RecordedAt          string                     `json:"recorded_at"`
	LedgerRevision      string                     `json:"ledger_revision"`
	AllPositionsMatch   bool                       `json:"all_positions_match"`
	PositionDifferences []BrokerPositionDifference `json:"position_differences"`
}

type brokerReconciliationRecord struct {
	ReconciliationID    string                     `json:"reconciliation_id"`
	SnapshotID          string                     `json:"snapshot_id"`
	LedgerAccountID     string                     `json:"ledger_account_id"`
	LedgerRevision      string                     `json:"ledger_revision"`
	RecordedAt          string                     `json:"recorded_at"`
	AllPositionsMatch   bool                       `json:"all_positions_match"`
	PositionDifferences []BrokerPositionDifference `json:"position_differences"`
}

type brokerRecoveryProof struct {
	SHA256          string
	Snapshots       int
	Reconciliations int
}

func (s *Service) recordKiwoomSnapshot(ctx context.Context, ledgerAccountID string, snapshot *KiwoomSnapshot) (*BrokerKnownGoodSnapshot, error) {
	if ledgerAccountID != "account-main" {
		return nil, errors.New("ledger account is outside the current account-main boundary")
	}
	if err := validateKiwoomSnapshot(snapshot); err != nil {
		return nil, err
	}
	snapshotJSON, snapshotSHA, err := orderJSONHash(snapshot)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	snapshotID, storedSnapshot, err := s.ensureBrokerSnapshot(ctx, tx, snapshot, string(snapshotJSON), snapshotSHA)
	if err != nil {
		return nil, err
	}
	ledgerRevision, ledgerQuantities, err := ledgerKRXQuantities(ctx, tx, ledgerAccountID)
	if err != nil {
		return nil, err
	}

	var recordSHA, recordJSON, recordRecordedAt string
	err = tx.QueryRowContext(ctx, `SELECT record_sha256,record_json,recorded_at FROM broker_snapshot_reconciliations
		WHERE snapshot_id=? AND ledger_account_id=? AND ledger_revision=?`, snapshotID, ledgerAccountID, ledgerRevision).Scan(&recordSHA, &recordJSON, &recordRecordedAt)
	if err == nil {
		record, err := decodeBrokerReconciliation(recordJSON, recordSHA, storedSnapshot)
		if err != nil {
			return nil, err
		}
		if err := requireReconciliationIdentity(record, snapshotID, ledgerAccountID, ledgerRevision, recordRecordedAt); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return knownGoodSnapshot(record, storedSnapshot, snapshotSHA), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	differences, allMatch, err := positionDifferences(storedSnapshot.Positions, ledgerQuantities)
	if err != nil {
		return nil, err
	}
	record := &brokerReconciliationRecord{
		ReconciliationID: s.id("broker_reconciliation"), SnapshotID: snapshotID, LedgerAccountID: ledgerAccountID,
		LedgerRevision: revision(ledgerRevision), RecordedAt: s.now().UTC().Format(time.RFC3339Nano),
		AllPositionsMatch: allMatch, PositionDifferences: differences,
	}
	if err := validateBrokerReconciliation(record, storedSnapshot); err != nil {
		return nil, err
	}
	recordBytes, recordSHA, err := orderJSONHash(record)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO broker_snapshot_reconciliations(
		reconciliation_id,snapshot_id,ledger_account_id,ledger_revision,record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?)`, record.ReconciliationID, snapshotID, ledgerAccountID, ledgerRevision, recordSHA, string(recordBytes), record.RecordedAt)
	if err != nil {
		return nil, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if inserted == 0 {
		if err := tx.QueryRowContext(ctx, `SELECT record_sha256,record_json,recorded_at FROM broker_snapshot_reconciliations
			WHERE snapshot_id=? AND ledger_account_id=? AND ledger_revision=?`, snapshotID, ledgerAccountID, ledgerRevision).Scan(&recordSHA, &recordJSON, &recordRecordedAt); err != nil {
			return nil, errors.New("broker reconciliation identifier collided with another record")
		}
		record, err = decodeBrokerReconciliation(recordJSON, recordSHA, storedSnapshot)
		if err != nil {
			return nil, err
		}
		if err := requireReconciliationIdentity(record, snapshotID, ledgerAccountID, ledgerRevision, recordRecordedAt); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return knownGoodSnapshot(record, storedSnapshot, snapshotSHA), nil
}

func (s *Service) ensureBrokerSnapshot(ctx context.Context, tx *sql.Tx, snapshot *KiwoomSnapshot, snapshotJSON, snapshotSHA string) (string, *KiwoomSnapshot, error) {
	var snapshotID, priorSHA, priorJSON string
	err := tx.QueryRowContext(ctx, `SELECT snapshot_id,snapshot_sha256,snapshot_json FROM broker_snapshots
		WHERE provider='kiwoom' AND environment=? AND exchange=? AND account_ref=? AND fetched_at=?`,
		snapshot.Environment, snapshot.Exchange, snapshot.AccountRef, snapshot.FetchedAt).Scan(&snapshotID, &priorSHA, &priorJSON)
	if err == nil {
		if priorSHA != snapshotSHA {
			return "", nil, errors.New("fetched_at was already used with a different broker snapshot")
		}
		stored, err := decodeStoredBrokerSnapshot(priorJSON, priorSHA)
		if err != nil {
			return "", nil, err
		}
		if !safeOrderID(snapshotID) || !sameKiwoomSnapshotIdentity(stored, snapshot) {
			return "", nil, errors.New("stored broker snapshot metadata mismatch")
		}
		return snapshotID, stored, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", nil, err
	}

	snapshotID = s.id("broker_snapshot")
	if !safeOrderID(snapshotID) {
		return "", nil, errors.New("broker snapshot identifier is invalid")
	}
	recordedAt := s.now().UTC().Format(time.RFC3339Nano)
	if !canonicalUTCString(recordedAt) {
		return "", nil, errors.New("broker snapshot recorded_at is invalid")
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO broker_snapshots(
		snapshot_id,provider,environment,exchange,account_ref,fetched_at,snapshot_sha256,snapshot_json,recorded_at
	) VALUES(?,'kiwoom',?,?,?,?,?,?,?)`, snapshotID, snapshot.Environment, snapshot.Exchange, snapshot.AccountRef,
		snapshot.FetchedAt, snapshotSHA, snapshotJSON, recordedAt)
	if err != nil {
		return "", nil, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return "", nil, err
	}
	if inserted == 1 {
		stored, err := decodeStoredBrokerSnapshot(snapshotJSON, snapshotSHA)
		return snapshotID, stored, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT snapshot_id,snapshot_sha256,snapshot_json FROM broker_snapshots
		WHERE provider='kiwoom' AND environment=? AND exchange=? AND account_ref=? AND fetched_at=?`,
		snapshot.Environment, snapshot.Exchange, snapshot.AccountRef, snapshot.FetchedAt).Scan(&snapshotID, &priorSHA, &priorJSON); err != nil {
		return "", nil, errors.New("broker snapshot identifier collided with another record")
	}
	if priorSHA != snapshotSHA {
		return "", nil, errors.New("fetched_at was already used with a different broker snapshot")
	}
	stored, err := decodeStoredBrokerSnapshot(priorJSON, priorSHA)
	if err == nil && (!safeOrderID(snapshotID) || !sameKiwoomSnapshotIdentity(stored, snapshot)) {
		return "", nil, errors.New("stored broker snapshot metadata mismatch")
	}
	return snapshotID, stored, err
}

func (s *Service) latestKiwoomSnapshot(ctx context.Context, environment KiwoomEnvironment, exchange KiwoomExchange, accountRef string) (*BrokerKnownGoodSnapshot, error) {
	if (environment != KiwoomProduction && environment != KiwoomMock) || exchange != KiwoomKRX || !orderAlias(accountRef, "account") {
		return nil, errors.New("invalid Kiwoom snapshot identity")
	}
	var snapshotID, ledgerAccountID, fetchedAt, snapshotSHA, snapshotJSON, recordSHA, recordJSON, recordRecordedAt string
	var ledgerRevision int64
	err := s.db.QueryRowContext(ctx, `SELECT s.snapshot_id,r.ledger_account_id,r.ledger_revision,s.fetched_at,
		s.snapshot_sha256,s.snapshot_json,r.record_sha256,r.record_json,r.recorded_at
		FROM broker_snapshots s JOIN broker_snapshot_reconciliations r ON r.snapshot_id=s.snapshot_id
		WHERE s.provider='kiwoom' AND s.environment=? AND s.exchange=? AND s.account_ref=?
		ORDER BY s.fetched_at DESC,s.sequence DESC,r.ledger_revision DESC,r.sequence DESC LIMIT 1`,
		environment, exchange, accountRef).Scan(&snapshotID, &ledgerAccountID, &ledgerRevision, &fetchedAt, &snapshotSHA, &snapshotJSON, &recordSHA, &recordJSON, &recordRecordedAt)
	if err != nil {
		return nil, err
	}
	snapshot, err := decodeStoredBrokerSnapshot(snapshotJSON, snapshotSHA)
	if err != nil {
		return nil, err
	}
	record, err := decodeBrokerReconciliation(recordJSON, recordSHA, snapshot)
	if err != nil {
		return nil, err
	}
	if !safeOrderID(snapshotID) || snapshot.Environment != environment || snapshot.Exchange != exchange || snapshot.AccountRef != accountRef || snapshot.FetchedAt != fetchedAt {
		return nil, errors.New("stored broker snapshot metadata mismatch")
	}
	if err := requireReconciliationIdentity(record, snapshotID, ledgerAccountID, ledgerRevision, recordRecordedAt); err != nil {
		return nil, err
	}
	return knownGoodSnapshot(record, snapshot, snapshotSHA), nil
}

func (s *Service) latestBrokerReconciliation(ctx context.Context) (*BrokerReconciliationView, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var snapshotID, accountRef, fetchedAt, snapshotSHA, snapshotJSON string
	var environment KiwoomEnvironment
	var exchange KiwoomExchange
	if err := tx.QueryRowContext(ctx, `SELECT snapshot_id,environment,exchange,account_ref,fetched_at,snapshot_sha256,snapshot_json
		FROM broker_snapshots WHERE provider='kiwoom' AND exchange='KRX'
		ORDER BY fetched_at DESC,sequence DESC LIMIT 1`).Scan(
		&snapshotID, &environment, &exchange, &accountRef, &fetchedAt, &snapshotSHA, &snapshotJSON,
	); err != nil {
		return nil, err
	}
	snapshot, err := decodeStoredBrokerSnapshot(snapshotJSON, snapshotSHA)
	if err != nil {
		return nil, err
	}
	if !safeOrderID(snapshotID) || snapshot.Environment != environment || snapshot.Exchange != exchange ||
		snapshot.AccountRef != accountRef || snapshot.FetchedAt != fetchedAt {
		return nil, errors.New("stored broker snapshot metadata mismatch")
	}
	var ledgerAccountID, recordSHA, recordJSON, recordRecordedAt string
	var ledgerRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT ledger_account_id,ledger_revision,record_sha256,record_json,recorded_at
		FROM broker_snapshot_reconciliations WHERE snapshot_id=?
		ORDER BY ledger_revision DESC,sequence DESC LIMIT 1`, snapshotID).Scan(
		&ledgerAccountID, &ledgerRevision, &recordSHA, &recordJSON, &recordRecordedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("latest broker snapshot has no reconciliation")
		}
		return nil, err
	}
	record, err := decodeBrokerReconciliation(recordJSON, recordSHA, snapshot)
	if err != nil {
		return nil, err
	}
	if err := requireReconciliationIdentity(record, snapshotID, ledgerAccountID, ledgerRevision, recordRecordedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &BrokerReconciliationView{
		Provider: "kiwoom", Environment: environment, Exchange: exchange, Freshness: "unverified",
		FetchedAt: fetchedAt, RecordedAt: record.RecordedAt, LedgerRevision: record.LedgerRevision,
		AllPositionsMatch: record.AllPositionsMatch, PositionDifferences: record.PositionDifferences,
	}, nil
}

func sameKiwoomSnapshotIdentity(left, right *KiwoomSnapshot) bool {
	return left != nil && right != nil && left.Source == right.Source && left.Environment == right.Environment &&
		left.Exchange == right.Exchange && left.AccountRef == right.AccountRef && left.FetchedAt == right.FetchedAt
}

func requireReconciliationIdentity(record *brokerReconciliationRecord, snapshotID, ledgerAccountID string, ledgerRevision int64, recordedAt string) error {
	if record.SnapshotID != snapshotID || record.LedgerAccountID != ledgerAccountID || record.LedgerRevision != revision(ledgerRevision) || record.RecordedAt != recordedAt {
		return errors.New("stored broker reconciliation metadata mismatch")
	}
	return nil
}

func ledgerKRXQuantities(ctx context.Context, q orderQuerier, accountID string) (int64, map[string]*big.Rat, error) {
	var ledgerRevision int64
	if err := q.QueryRowContext(ctx, `SELECT revision FROM ledger_meta WHERE singleton=1`).Scan(&ledgerRevision); err != nil {
		return 0, nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT type,symbol,quantity FROM events
		WHERE account_id=? AND currency='KRW' AND type IN ('BUY','SELL') ORDER BY occurred_at,sequence`, accountID)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	quantities := map[string]*big.Rat{}
	for rows.Next() {
		var typ, symbol, raw string
		if err := rows.Scan(&typ, &symbol, &raw); err != nil {
			return 0, nil, err
		}
		if !kiwoomStockPattern.MatchString(symbol) {
			continue
		}
		quantity, err := parseDecimal(raw)
		if err != nil || quantity.Sign() <= 0 {
			return 0, nil, fmt.Errorf("ledger quantity for %s is invalid", symbol)
		}
		if quantities[symbol] == nil {
			quantities[symbol] = new(big.Rat)
		}
		if typ == "BUY" {
			quantities[symbol].Add(quantities[symbol], quantity)
		} else {
			quantities[symbol].Sub(quantities[symbol], quantity)
			if quantities[symbol].Sign() < 0 {
				return 0, nil, fmt.Errorf("ledger position for %s is negative", symbol)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	return ledgerRevision, quantities, nil
}

func positionDifferences(positions []KiwoomPosition, ledger map[string]*big.Rat) ([]BrokerPositionDifference, bool, error) {
	broker := make(map[string]*big.Rat, len(positions))
	for _, position := range positions {
		quantity, err := parseDecimal(position.Quantity)
		if err != nil {
			return nil, false, err
		}
		broker[position.Symbol] = quantity
	}
	symbols := make(map[string]struct{}, len(broker)+len(ledger))
	for symbol := range broker {
		symbols[symbol] = struct{}{}
	}
	for symbol := range ledger {
		symbols[symbol] = struct{}{}
	}
	ordered := make([]string, 0, len(symbols))
	for symbol := range symbols {
		ordered = append(ordered, symbol)
	}
	sort.Strings(ordered)
	result := make([]BrokerPositionDifference, 0, len(ordered))
	allMatch := true
	for _, symbol := range ordered {
		brokerQuantity, ledgerQuantity := broker[symbol], ledger[symbol]
		if brokerQuantity == nil {
			brokerQuantity = new(big.Rat)
		}
		if ledgerQuantity == nil {
			ledgerQuantity = new(big.Rat)
		}
		difference := new(big.Rat).Sub(new(big.Rat).Set(brokerQuantity), ledgerQuantity)
		brokerRaw, err := formatDecimal(brokerQuantity)
		if err != nil {
			return nil, false, err
		}
		ledgerRaw, err := formatDecimal(ledgerQuantity)
		if err != nil {
			return nil, false, err
		}
		differenceRaw, err := formatDecimal(difference)
		if err != nil {
			return nil, false, err
		}
		match := difference.Sign() == 0
		allMatch = allMatch && match
		result = append(result, BrokerPositionDifference{symbol, brokerRaw, ledgerRaw, differenceRaw, match})
	}
	return result, allMatch, nil
}

func validateKiwoomSnapshot(snapshot *KiwoomSnapshot) error {
	if snapshot == nil || !snapshot.Complete || snapshot.Source != "kiwoom" ||
		(snapshot.Environment != KiwoomProduction && snapshot.Environment != KiwoomMock) || snapshot.Exchange != KiwoomKRX ||
		!orderAlias(snapshot.AccountRef, "account") || !maskedKiwoomAccountPattern.MatchString(snapshot.MaskedAccount) ||
		snapshot.Currency != "KRW" || snapshot.Positions == nil || snapshot.OpenOrders == nil {
		return errors.New("broker snapshot identity or completeness is invalid")
	}
	if !canonicalUTCString(snapshot.FetchedAt) {
		return errors.New("broker snapshot fetched_at must be canonical UTC")
	}
	for _, value := range []string{snapshot.Totals.UnrealizedPnL, snapshot.Totals.ReturnRatePercent, snapshot.Totals.EstimatedAssets} {
		if !canonicalDecimal(value, false) {
			return errors.New("broker snapshot totals contain an invalid decimal")
		}
	}
	for _, value := range []string{snapshot.Totals.PurchaseAmount, snapshot.Totals.EvaluationAmount, snapshot.Totals.LoanAmount, snapshot.Totals.CreditLoanAmount, snapshot.Totals.CreditLendingAmount} {
		if !canonicalDecimal(value, true) {
			return errors.New("broker snapshot totals contain an invalid non-negative decimal")
		}
	}
	previousSymbol := ""
	for _, position := range snapshot.Positions {
		if !kiwoomStockPattern.MatchString(position.Symbol) || position.Symbol <= previousSymbol {
			return errors.New("broker positions must have unique ascending KRX symbols")
		}
		if text, ok := kiwoomText(position.Name); !ok || text != position.Name {
			return errors.New("broker position name is invalid")
		}
		for _, value := range []string{position.Quantity, position.TradableQuantity, position.AveragePurchasePrice, position.CurrentPrice, position.PurchaseAmount, position.EvaluationAmount, position.WeightPercent} {
			if !canonicalDecimal(value, true) {
				return errors.New("broker position contains an invalid non-negative decimal")
			}
		}
		for _, value := range []string{position.UnrealizedPnL, position.ReturnRatePercent} {
			if !canonicalDecimal(value, false) {
				return errors.New("broker position contains an invalid signed decimal")
			}
		}
		quantity, _ := parseDecimal(position.Quantity)
		tradable, _ := parseDecimal(position.TradableQuantity)
		if tradable.Cmp(quantity) > 0 {
			return errors.New("broker tradable quantity exceeds position quantity")
		}
		previousSymbol = position.Symbol
	}
	previousOrder := ""
	seenOrder := map[string]bool{}
	for _, order := range snapshot.OpenOrders {
		key := order.Symbol + "\x00" + order.Time + "\x00" + order.OrderRef
		if !orderAlias(order.OrderRef, "order") || seenOrder[order.OrderRef] || key <= previousOrder ||
			!kiwoomStockPattern.MatchString(order.Symbol) || order.Status != "OPEN" ||
			(order.Side != "BUY" && order.Side != "SELL") || order.Exchange != string(KiwoomKRX) || !kiwoomValidTime(order.Time) {
			return errors.New("broker open order is invalid or unsorted")
		}
		if text, ok := kiwoomText(order.Name); !ok || text != order.Name {
			return errors.New("broker open order name is invalid")
		}
		for _, value := range []string{order.Quantity, order.Price, order.RemainingQuantity} {
			if !canonicalDecimal(value, true) {
				return errors.New("broker open order contains an invalid decimal")
			}
		}
		quantity, _ := parseDecimal(order.Quantity)
		remaining, _ := parseDecimal(order.RemainingQuantity)
		if remaining.Cmp(quantity) > 0 {
			return errors.New("broker remaining quantity exceeds order quantity")
		}
		seenOrder[order.OrderRef], previousOrder = true, key
	}
	return nil
}

func canonicalDecimal(raw string, nonNegative bool) bool {
	if len(raw) == 0 || len(raw) > 64 {
		return false
	}
	value, err := parseDecimal(raw)
	if err != nil || (nonNegative && value.Sign() < 0) {
		return false
	}
	canonical, err := formatDecimal(value)
	return err == nil && canonical == raw
}

func canonicalUTCString(raw string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && parsed.UTC().Format(time.RFC3339Nano) == raw
}

func decodeStoredBrokerSnapshot(raw, expectedSHA string) (*KiwoomSnapshot, error) {
	var snapshot KiwoomSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return nil, err
	}
	canonical, actualSHA, err := orderJSONHash(&snapshot)
	if err != nil || string(canonical) != raw {
		return nil, errors.New("stored broker snapshot is not canonical")
	}
	if actualSHA != expectedSHA {
		return nil, errors.New("stored broker snapshot hash mismatch")
	}
	if err := validateKiwoomSnapshot(&snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func decodeBrokerReconciliation(raw, expectedSHA string, snapshot *KiwoomSnapshot) (*brokerReconciliationRecord, error) {
	var record brokerReconciliationRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return nil, err
	}
	canonical, actualSHA, err := orderJSONHash(&record)
	if err != nil || string(canonical) != raw {
		return nil, errors.New("stored broker reconciliation is not canonical")
	}
	if actualSHA != expectedSHA {
		return nil, errors.New("stored broker reconciliation hash mismatch")
	}
	if err := validateBrokerReconciliation(&record, snapshot); err != nil {
		return nil, err
	}
	return &record, nil
}

func validateBrokerReconciliation(record *brokerReconciliationRecord, snapshot *KiwoomSnapshot) error {
	if record == nil || snapshot == nil || !safeOrderID(record.ReconciliationID) || !safeOrderID(record.SnapshotID) ||
		record.LedgerAccountID != "account-main" || !ledgerRevisionPattern.MatchString(record.LedgerRevision) || !canonicalUTCString(record.RecordedAt) {
		return errors.New("broker reconciliation metadata is invalid")
	}
	if len(record.PositionDifferences) == 0 && len(snapshot.Positions) != 0 {
		return errors.New("broker reconciliation is missing positions")
	}
	allMatch, previous := true, ""
	brokerQuantities := map[string]string{}
	for _, position := range snapshot.Positions {
		brokerQuantities[position.Symbol] = position.Quantity
	}
	for _, difference := range record.PositionDifferences {
		if !kiwoomStockPattern.MatchString(difference.Symbol) || difference.Symbol <= previous ||
			!canonicalDecimal(difference.BrokerQuantity, true) || !canonicalDecimal(difference.LedgerQuantity, true) || !canonicalDecimal(difference.Difference, false) {
			return errors.New("broker reconciliation row is invalid")
		}
		brokerQuantity, _ := parseDecimal(difference.BrokerQuantity)
		ledgerQuantity, _ := parseDecimal(difference.LedgerQuantity)
		expected := new(big.Rat).Sub(brokerQuantity, ledgerQuantity)
		actual, _ := parseDecimal(difference.Difference)
		if expected.Cmp(actual) != 0 || difference.Match != (actual.Sign() == 0) {
			return errors.New("broker reconciliation arithmetic is invalid")
		}
		if raw, ok := brokerQuantities[difference.Symbol]; ok && raw != difference.BrokerQuantity {
			return errors.New("broker reconciliation quantity differs from snapshot")
		}
		if _, ok := brokerQuantities[difference.Symbol]; !ok && difference.BrokerQuantity != "0" {
			return errors.New("broker reconciliation invented a broker position")
		}
		delete(brokerQuantities, difference.Symbol)
		allMatch, previous = allMatch && difference.Match, difference.Symbol
	}
	if len(brokerQuantities) != 0 || allMatch != record.AllPositionsMatch {
		return errors.New("broker reconciliation projection is incomplete")
	}
	return nil
}

func knownGoodSnapshot(record *brokerReconciliationRecord, snapshot *KiwoomSnapshot, snapshotSHA string) *BrokerKnownGoodSnapshot {
	return &BrokerKnownGoodSnapshot{
		SnapshotID: record.SnapshotID, ReconciliationID: record.ReconciliationID, Provider: "kiwoom",
		Environment: snapshot.Environment, Exchange: snapshot.Exchange, AccountRef: snapshot.AccountRef,
		LedgerAccountID: record.LedgerAccountID, FetchedAt: snapshot.FetchedAt, RecordedAt: record.RecordedAt,
		SnapshotSHA256: snapshotSHA, LedgerRevision: record.LedgerRevision, AllPositionsMatch: record.AllPositionsMatch,
		PositionDifferences: record.PositionDifferences, Snapshot: *snapshot,
	}
}

func proveBrokerRecovery(ctx context.Context, q orderQuerier) (brokerRecoveryProof, error) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	snapshots := map[string]struct {
		snapshot *KiwoomSnapshot
		sha      string
	}{}
	rows, err := q.QueryContext(ctx, `SELECT sequence,snapshot_id,provider,environment,exchange,account_ref,fetched_at,
		snapshot_sha256,snapshot_json,recorded_at FROM broker_snapshots ORDER BY sequence`)
	if err != nil {
		return brokerRecoveryProof{}, err
	}
	for rows.Next() {
		var sequence int64
		var snapshotID, provider, environment, exchange, accountRef, fetchedAt, snapshotSHA, snapshotJSON, recordedAt string
		if err := rows.Scan(&sequence, &snapshotID, &provider, &environment, &exchange, &accountRef, &fetchedAt,
			&snapshotSHA, &snapshotJSON, &recordedAt); err != nil {
			rows.Close()
			return brokerRecoveryProof{}, err
		}
		snapshot, err := decodeStoredBrokerSnapshot(snapshotJSON, snapshotSHA)
		if err != nil {
			rows.Close()
			return brokerRecoveryProof{}, fmt.Errorf("decode broker snapshot %q: %w", snapshotID, err)
		}
		if !safeOrderID(snapshotID) || provider != "kiwoom" || string(snapshot.Environment) != environment || string(snapshot.Exchange) != exchange ||
			snapshot.AccountRef != accountRef || snapshot.FetchedAt != fetchedAt || !canonicalUTCString(recordedAt) {
			rows.Close()
			return brokerRecoveryProof{}, fmt.Errorf("broker snapshot %q metadata mismatch", snapshotID)
		}
		if err := encoder.Encode([]any{"broker_snapshots", sequence, snapshotID, provider, environment, exchange, accountRef,
			fetchedAt, snapshotSHA, snapshotJSON, recordedAt}); err != nil {
			rows.Close()
			return brokerRecoveryProof{}, err
		}
		snapshots[snapshotID] = struct {
			snapshot *KiwoomSnapshot
			sha      string
		}{snapshot, snapshotSHA}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return brokerRecoveryProof{}, err
	}
	if err := rows.Close(); err != nil {
		return brokerRecoveryProof{}, err
	}

	reconciliations := 0
	reconciledSnapshots := map[string]bool{}
	rows, err = q.QueryContext(ctx, `SELECT sequence,reconciliation_id,snapshot_id,ledger_account_id,ledger_revision,
		record_sha256,record_json,recorded_at FROM broker_snapshot_reconciliations ORDER BY sequence`)
	if err != nil {
		return brokerRecoveryProof{}, err
	}
	for rows.Next() {
		var sequence, ledgerRevision int64
		var reconciliationID, snapshotID, ledgerAccountID, recordSHA, recordJSON, recordedAt string
		if err := rows.Scan(&sequence, &reconciliationID, &snapshotID, &ledgerAccountID, &ledgerRevision, &recordSHA, &recordJSON, &recordedAt); err != nil {
			rows.Close()
			return brokerRecoveryProof{}, err
		}
		stored, ok := snapshots[snapshotID]
		if !ok {
			rows.Close()
			return brokerRecoveryProof{}, fmt.Errorf("broker reconciliation %q references unknown snapshot", reconciliationID)
		}
		record, err := decodeBrokerReconciliation(recordJSON, recordSHA, stored.snapshot)
		if err != nil {
			rows.Close()
			return brokerRecoveryProof{}, fmt.Errorf("decode broker reconciliation %q: %w", reconciliationID, err)
		}
		if record.ReconciliationID != reconciliationID || record.SnapshotID != snapshotID || record.LedgerAccountID != ledgerAccountID ||
			record.LedgerRevision != revision(ledgerRevision) || record.RecordedAt != recordedAt {
			rows.Close()
			return brokerRecoveryProof{}, fmt.Errorf("broker reconciliation %q metadata mismatch", reconciliationID)
		}
		if err := encoder.Encode([]any{"broker_snapshot_reconciliations", sequence, reconciliationID, snapshotID,
			ledgerAccountID, ledgerRevision, recordSHA, recordJSON, recordedAt}); err != nil {
			rows.Close()
			return brokerRecoveryProof{}, err
		}
		reconciliations++
		reconciledSnapshots[snapshotID] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return brokerRecoveryProof{}, err
	}
	if err := rows.Close(); err != nil {
		return brokerRecoveryProof{}, err
	}
	for snapshotID := range snapshots {
		if !reconciledSnapshots[snapshotID] {
			return brokerRecoveryProof{}, fmt.Errorf("broker snapshot %q has no reconciliation", snapshotID)
		}
	}
	return brokerRecoveryProof{SHA256: hex.EncodeToString(hash.Sum(nil)), Snapshots: len(snapshots), Reconciliations: reconciliations}, nil
}
