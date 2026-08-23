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

type brokerRecoveryProof struct {
	SHA256    string
	Snapshots int
}

func (s *Service) recordKiwoomSnapshot(ctx context.Context, ledgerAccountID string, snapshot *KiwoomSnapshot) (*BrokerKnownGoodSnapshot, error) {
	if ledgerAccountID != "account-main" {
		return nil, errors.New("ledger account is outside the current account-main boundary")
	}
	if err := validateKiwoomSnapshot(snapshot); err != nil {
		return nil, err
	}
	_, snapshotSHA, err := orderJSONHash(snapshot)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var priorSHA, priorRecordSHA, priorJSON string
	err = tx.QueryRowContext(ctx, `SELECT snapshot_sha256,record_sha256,record_json FROM broker_snapshots
		WHERE provider='kiwoom' AND environment=? AND exchange=? AND account_ref=? AND fetched_at=?`,
		snapshot.Environment, snapshot.Exchange, snapshot.AccountRef, snapshot.FetchedAt).Scan(&priorSHA, &priorRecordSHA, &priorJSON)
	if err == nil {
		if priorSHA != snapshotSHA {
			return nil, errors.New("fetched_at was already used with a different broker snapshot")
		}
		return decodeBrokerRecordWithSHA(priorJSON, priorRecordSHA)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	ledgerRevision, ledgerQuantities, err := ledgerKRXQuantities(ctx, tx, ledgerAccountID)
	if err != nil {
		return nil, err
	}
	differences, allMatch, err := positionDifferences(snapshot.Positions, ledgerQuantities)
	if err != nil {
		return nil, err
	}
	record := &BrokerKnownGoodSnapshot{
		SnapshotID: s.id("broker_snapshot"), Provider: "kiwoom", Environment: snapshot.Environment,
		Exchange: snapshot.Exchange, AccountRef: snapshot.AccountRef, LedgerAccountID: ledgerAccountID,
		FetchedAt: snapshot.FetchedAt, RecordedAt: s.now().UTC().Format(time.RFC3339Nano), SnapshotSHA256: snapshotSHA,
		LedgerRevision: revision(ledgerRevision), AllPositionsMatch: allMatch, PositionDifferences: differences, Snapshot: *snapshot,
	}
	if err := validateBrokerRecord(record); err != nil {
		return nil, err
	}
	recordJSON, recordSHA, err := orderJSONHash(record)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO broker_snapshots(
		snapshot_id,provider,environment,exchange,account_ref,ledger_account_id,fetched_at,snapshot_sha256,record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, record.SnapshotID, record.Provider, record.Environment, record.Exchange, record.AccountRef,
		record.LedgerAccountID, record.FetchedAt, snapshotSHA, recordSHA, string(recordJSON), record.RecordedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return decodeBrokerRecordWithSHA(string(recordJSON), recordSHA)
}

func (s *Service) latestKiwoomSnapshot(ctx context.Context, environment KiwoomEnvironment, exchange KiwoomExchange, accountRef string) (*BrokerKnownGoodSnapshot, error) {
	if (environment != KiwoomProduction && environment != KiwoomMock) || exchange != KiwoomKRX || !orderAlias(accountRef, "account") {
		return nil, errors.New("invalid Kiwoom snapshot identity")
	}
	var recordSHA, raw string
	err := s.db.QueryRowContext(ctx, `SELECT record_sha256,record_json FROM broker_snapshots
		WHERE provider='kiwoom' AND environment=? AND exchange=? AND account_ref=?
		ORDER BY fetched_at DESC,sequence DESC LIMIT 1`, environment, exchange, accountRef).Scan(&recordSHA, &raw)
	if err != nil {
		return nil, err
	}
	return decodeBrokerRecordWithSHA(raw, recordSHA)
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
	fetchedAt, err := time.Parse(time.RFC3339Nano, snapshot.FetchedAt)
	if err != nil || fetchedAt.UTC().Format(time.RFC3339Nano) != snapshot.FetchedAt {
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

func decodeBrokerRecord(raw string) (*BrokerKnownGoodSnapshot, error) {
	var record BrokerKnownGoodSnapshot
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return nil, err
	}
	canonical, _, err := orderJSONHash(&record)
	if err != nil || string(canonical) != raw {
		return nil, errors.New("stored broker snapshot record is not canonical")
	}
	if err := validateBrokerRecord(&record); err != nil {
		return nil, err
	}
	return &record, nil
}

func decodeBrokerRecordWithSHA(raw, expectedSHA string) (*BrokerKnownGoodSnapshot, error) {
	record, err := decodeBrokerRecord(raw)
	if err != nil {
		return nil, err
	}
	_, actualSHA, err := orderJSONHash(record)
	if err != nil || actualSHA != expectedSHA {
		return nil, errors.New("stored broker snapshot record hash mismatch")
	}
	return record, nil
}

func validateBrokerRecord(record *BrokerKnownGoodSnapshot) error {
	if record == nil || !safeOrderID(record.SnapshotID) || record.Provider != "kiwoom" || record.LedgerAccountID != "account-main" ||
		record.Environment != record.Snapshot.Environment || record.Exchange != record.Snapshot.Exchange || record.AccountRef != record.Snapshot.AccountRef ||
		record.FetchedAt != record.Snapshot.FetchedAt || !ledgerRevisionPattern.MatchString(record.LedgerRevision) {
		return errors.New("broker snapshot record metadata is invalid")
	}
	recordedAt, err := time.Parse(time.RFC3339Nano, record.RecordedAt)
	if err != nil || recordedAt.UTC().Format(time.RFC3339Nano) != record.RecordedAt {
		return errors.New("broker snapshot record time is invalid")
	}
	if err := validateKiwoomSnapshot(&record.Snapshot); err != nil {
		return err
	}
	_, snapshotSHA, err := orderJSONHash(&record.Snapshot)
	if err != nil || snapshotSHA != record.SnapshotSHA256 {
		return errors.New("broker snapshot hash mismatch")
	}
	if len(record.PositionDifferences) == 0 && len(record.Snapshot.Positions) != 0 {
		return errors.New("broker reconciliation is missing positions")
	}
	allMatch, previous := true, ""
	brokerQuantities := map[string]string{}
	for _, position := range record.Snapshot.Positions {
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

func proveBrokerRecovery(ctx context.Context, q orderQuerier) (brokerRecoveryProof, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence,snapshot_id,provider,environment,exchange,account_ref,ledger_account_id,fetched_at,
		snapshot_sha256,record_sha256,record_json,recorded_at FROM broker_snapshots ORDER BY sequence`)
	if err != nil {
		return brokerRecoveryProof{}, err
	}
	defer rows.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	count := 0
	for rows.Next() {
		var sequence int64
		var snapshotID, provider, environment, exchange, accountRef, ledgerAccountID, fetchedAt, snapshotSHA, recordSHA, recordJSON, recordedAt string
		if err := rows.Scan(&sequence, &snapshotID, &provider, &environment, &exchange, &accountRef, &ledgerAccountID, &fetchedAt,
			&snapshotSHA, &recordSHA, &recordJSON, &recordedAt); err != nil {
			return brokerRecoveryProof{}, err
		}
		record, err := decodeBrokerRecordWithSHA(recordJSON, recordSHA)
		if err != nil {
			return brokerRecoveryProof{}, fmt.Errorf("decode broker snapshot %q: %w", snapshotID, err)
		}
		_, actualRecordSHA, err := orderJSONHash(record)
		if err != nil || actualRecordSHA != recordSHA || record.SnapshotID != snapshotID || record.Provider != provider ||
			string(record.Environment) != environment || string(record.Exchange) != exchange || record.AccountRef != accountRef ||
			record.LedgerAccountID != ledgerAccountID || record.FetchedAt != fetchedAt || record.SnapshotSHA256 != snapshotSHA || record.RecordedAt != recordedAt {
			return brokerRecoveryProof{}, fmt.Errorf("broker snapshot %q metadata or hash mismatch", snapshotID)
		}
		if err := encoder.Encode([]any{"broker_snapshots", sequence, snapshotID, provider, environment, exchange, accountRef,
			ledgerAccountID, fetchedAt, snapshotSHA, recordSHA, recordJSON, recordedAt}); err != nil {
			return brokerRecoveryProof{}, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return brokerRecoveryProof{}, err
	}
	return brokerRecoveryProof{SHA256: hex.EncodeToString(hash.Sum(nil)), Snapshots: count}, nil
}
