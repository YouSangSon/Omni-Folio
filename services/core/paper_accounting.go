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
	"strings"
	"time"

	"omni-folio/services/core/internal/paperdomain"
)

const paperAccountingSessionSchema = "paper-accounting-session.v1"

type PaperAccountingSession struct {
	SessionID                string `json:"session_id"`
	SchemaVersion            string `json:"schema_version"`
	AccountRef               string `json:"account_ref"`
	StrategyResultSHA256     string `json:"strategy_result_sha256"`
	StrategySelectionEventID string `json:"strategy_selection_event_id"`
	ExecutionPolicySHA256    string `json:"execution_policy_sha256"`
	ExecutionPolicyJSON      string `json:"execution_policy_json"`
	StartingCash             string `json:"starting_cash"`
	Currency                 string `json:"currency"`
	RecordedAt               string `json:"recorded_at"`
}

type paperAccountingRecoveryProof struct {
	SHA256                                                          string
	Sessions, MarketBars, Signals, Authorizations, CapitalizedFills int
}

type paperLotState = paperdomain.Lot
type paperAccountState = paperdomain.AccountState

type paperAccountingCutoff struct {
	OrderSequence int64
	AsOf          string
}

func (s *Service) openPaperAccountingSession(ctx context.Context, accountRef, resultSHA256, selectionEventID string) (*PaperAccountingSession, error) {
	if s == nil || s.db == nil || !orderAlias(accountRef, "account") ||
		!strategySHA256Pattern.MatchString(resultSHA256) || !safeOrderID(selectionEventID) {
		return nil, errors.New("paper accounting session identity is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := proveOrderRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("paper accounting order recovery: %w", err)
	}
	if _, err := replayStrategyRegistry(ctx, tx); err != nil {
		return nil, fmt.Errorf("paper accounting strategy recovery: %w", err)
	}
	existing, found, err := loadPaperAccountingSession(ctx, tx, accountRef)
	if err != nil {
		return nil, err
	}
	if found {
		if existing.StrategyResultSHA256 != resultSHA256 || existing.StrategySelectionEventID != selectionEventID {
			return nil, errors.New("paper accounting session is already bound to different initial evidence")
		}
		return existing, nil
	}
	policy, err := loadCurrentStrategyExecutionPolicy(ctx, tx, resultSHA256, selectionEventID)
	if err != nil {
		return nil, err
	}
	var priorPaperOrders int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_idempotency WHERE mode='paper' AND account_ref=?`, accountRef).Scan(&priorPaperOrders); err != nil {
		return nil, err
	}
	if priorPaperOrders != 0 {
		return nil, errors.New("paper accounting session cannot follow a paper order")
	}
	session := PaperAccountingSession{
		SchemaVersion: paperAccountingSessionSchema, AccountRef: accountRef,
		StrategyResultSHA256: resultSHA256, StrategySelectionEventID: selectionEventID,
		ExecutionPolicySHA256: policy.SHA256, ExecutionPolicyJSON: policy.canonicalJSON,
		StartingCash: policy.StartingCash, Currency: "KRW", RecordedAt: s.now().UTC().Format(time.RFC3339Nano),
	}
	session.SessionID = paperAccountingSessionID(session.AccountRef, session.StrategyResultSHA256, session.StrategySelectionEventID, session.ExecutionPolicySHA256)
	if err := insertPaperAccountingSession(ctx, tx, session); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &session, nil
}

func paperAccountingSessionID(accountRef, resultSHA256, selectionEventID, policySHA256 string) string {
	hash := sha256.Sum256([]byte(strings.Join([]string{accountRef, resultSHA256, selectionEventID, policySHA256}, "\x00")))
	return "paper_accounting_session_" + hex.EncodeToString(hash[:16])
}

func insertPaperAccountingSession(ctx context.Context, tx *sql.Tx, session PaperAccountingSession) error {
	if _, err := validatePaperAccountingSession(session); err != nil {
		return err
	}
	recordJSON, recordSHA, err := orderJSONHash(session)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO paper_accounting_sessions(
		session_id,schema_version,account_ref,strategy_result_sha256,strategy_selection_event_id,
		execution_policy_sha256,execution_policy_json,starting_cash,currency,record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, session.SessionID, session.SchemaVersion, session.AccountRef,
		session.StrategyResultSHA256, session.StrategySelectionEventID, session.ExecutionPolicySHA256,
		session.ExecutionPolicyJSON, session.StartingCash, session.Currency, recordSHA, string(recordJSON), session.RecordedAt)
	return err
}

func loadPaperAccountingSession(ctx context.Context, q orderQuerier, accountRef string) (*PaperAccountingSession, bool, error) {
	row := q.QueryRowContext(ctx, `SELECT session_id,schema_version,account_ref,strategy_result_sha256,strategy_selection_event_id,
		execution_policy_sha256,execution_policy_json,starting_cash,currency,record_sha256,record_json,recorded_at
		FROM paper_accounting_sessions WHERE account_ref=?`, accountRef)
	var session PaperAccountingSession
	var recordSHA, recordJSON string
	err := row.Scan(&session.SessionID, &session.SchemaVersion, &session.AccountRef, &session.StrategyResultSHA256,
		&session.StrategySelectionEventID, &session.ExecutionPolicySHA256, &session.ExecutionPolicyJSON, &session.StartingCash,
		&session.Currency, &recordSHA, &recordJSON, &session.RecordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := validateStoredPaperAccountingSession(ctx, q, session, recordSHA, recordJSON); err != nil {
		return nil, false, err
	}
	return &session, true, nil
}

func provePaperAccountingRecovery(ctx context.Context, q orderQuerier) (paperAccountingRecoveryProof, error) {
	return provePaperAccountingRecoveryVersion(ctx, q, true)
}

func proveLegacyPaperAccountingRecovery(ctx context.Context, q orderQuerier) (paperAccountingRecoveryProof, error) {
	return provePaperAccountingRecoveryVersion(ctx, q, false)
}

func provePaperAccountingRecoveryVersion(ctx context.Context, q orderQuerier, includeMarket bool) (paperAccountingRecoveryProof, error) {
	if _, err := proveOrderRecovery(ctx, q); err != nil {
		return paperAccountingRecoveryProof{}, fmt.Errorf("paper accounting order recovery: %w", err)
	}
	if _, err := replayStrategyRegistry(ctx, q); err != nil {
		return paperAccountingRecoveryProof{}, fmt.Errorf("paper accounting strategy recovery: %w", err)
	}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	rows, err := q.QueryContext(ctx, `SELECT sequence,session_id,schema_version,account_ref,strategy_result_sha256,strategy_selection_event_id,
		execution_policy_sha256,execution_policy_json,starting_cash,currency,record_sha256,record_json,recorded_at
		FROM paper_accounting_sessions ORDER BY sequence`)
	if err != nil {
		return paperAccountingRecoveryProof{}, err
	}
	type storedSession struct {
		sequence              int64
		session               PaperAccountingSession
		recordSHA, recordJSON string
	}
	var stored []storedSession
	for rows.Next() {
		var item storedSession
		if err := rows.Scan(&item.sequence, &item.session.SessionID, &item.session.SchemaVersion, &item.session.AccountRef,
			&item.session.StrategyResultSHA256, &item.session.StrategySelectionEventID, &item.session.ExecutionPolicySHA256,
			&item.session.ExecutionPolicyJSON, &item.session.StartingCash, &item.session.Currency, &item.recordSHA, &item.recordJSON, &item.session.RecordedAt); err != nil {
			rows.Close()
			return paperAccountingRecoveryProof{}, err
		}
		stored = append(stored, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return paperAccountingRecoveryProof{}, err
	}
	if err := rows.Close(); err != nil {
		return paperAccountingRecoveryProof{}, err
	}
	for index, item := range stored {
		if item.sequence != int64(index+1) {
			return paperAccountingRecoveryProof{}, fmt.Errorf("paper accounting session sequence %d is invalid", item.sequence)
		}
		if err := validateStoredPaperAccountingSession(ctx, q, item.session, item.recordSHA, item.recordJSON); err != nil {
			return paperAccountingRecoveryProof{}, fmt.Errorf("paper accounting session %q metadata or hash mismatch: %w", item.session.SessionID, err)
		}
		if err := encoder.Encode([]any{"paper_accounting_sessions", item.sequence, item.session.SessionID, item.session.SchemaVersion,
			item.session.AccountRef, item.session.StrategyResultSHA256, item.session.StrategySelectionEventID, item.session.ExecutionPolicySHA256,
			item.session.ExecutionPolicyJSON, item.session.StartingCash, item.session.Currency, item.recordSHA, item.recordJSON, item.session.RecordedAt}); err != nil {
			return paperAccountingRecoveryProof{}, err
		}
	}
	market := paperMarketRecoveryProof{}
	authorizations, capitalizedFills := 0, 0
	if includeMarket {
		market, err = replayPaperMarketRecovery(ctx, q)
		if err != nil {
			return paperAccountingRecoveryProof{}, fmt.Errorf("paper market recovery: %w", err)
		}
		if err := encoder.Encode([]any{"paper_market_recovery", market.SHA256, market.Bars, market.Signals}); err != nil {
			return paperAccountingRecoveryProof{}, err
		}
		authorizationSHA, count, err := provePaperExecutionAuthorizationRecovery(ctx, q)
		if err != nil {
			return paperAccountingRecoveryProof{}, fmt.Errorf("paper authorization recovery: %w", err)
		}
		authorizations = count
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_events events
			JOIN order_idempotency orders ON orders.order_id=events.order_id
			WHERE events.event_type='FILL_RECORDED' AND orders.mode='paper'
			AND json_extract(orders.intent_json, '$.signal_schema_version')='paper-signal.v3'`).Scan(&capitalizedFills); err != nil {
			return paperAccountingRecoveryProof{}, err
		}
		if err := encoder.Encode([]any{"paper_execution_authorizations", authorizationSHA, authorizations, "paper_capitalized_fills", capitalizedFills}); err != nil {
			return paperAccountingRecoveryProof{}, err
		}
		states, err := replayPaperAccountingState(ctx, q)
		if err != nil {
			return paperAccountingRecoveryProof{}, fmt.Errorf("paper capitalized accounting recovery: %w", err)
		}
		derivedFills := 0
		for _, state := range states {
			derivedFills += state.CapitalizedFills
		}
		if derivedFills != capitalizedFills {
			return paperAccountingRecoveryProof{}, errors.New("paper capitalized fill count does not match replay")
		}
		if err := encoder.Encode([]any{"paper_account_states", states}); err != nil {
			return paperAccountingRecoveryProof{}, err
		}
	}
	return paperAccountingRecoveryProof{
		SHA256: hex.EncodeToString(hash.Sum(nil)), Sessions: len(stored), MarketBars: market.Bars, Signals: market.Signals,
		Authorizations: authorizations, CapitalizedFills: capitalizedFills,
	}, nil
}

func replayPaperAccounting(ctx context.Context, q orderQuerier) (map[string]paperAccountState, error) {
	return replayPaperAccountingWithCutoff(ctx, q, nil)
}

func replayPaperAccountingAt(ctx context.Context, q orderQuerier, cutoff paperAccountingCutoff) (map[string]paperAccountState, error) {
	if cutoff.OrderSequence < 0 || !canonicalPaperTimes(cutoff.AsOf) {
		return nil, errors.New("paper accounting cutoff is invalid")
	}
	return replayPaperAccountingWithCutoff(ctx, q, &cutoff)
}

func replayPaperAccountingWithCutoff(ctx context.Context, q orderQuerier, cutoff *paperAccountingCutoff) (map[string]paperAccountState, error) {
	if _, err := proveOrderRecovery(ctx, q); err != nil {
		return nil, fmt.Errorf("paper accounting order recovery: %w", err)
	}
	if _, err := replayStrategyRegistry(ctx, q); err != nil {
		return nil, fmt.Errorf("paper accounting strategy recovery: %w", err)
	}
	if _, err := replayPaperMarketRecovery(ctx, q); err != nil {
		return nil, fmt.Errorf("paper accounting market recovery: %w", err)
	}
	if _, _, err := provePaperExecutionAuthorizationRecovery(ctx, q); err != nil {
		return nil, fmt.Errorf("paper accounting authorization recovery: %w", err)
	}
	if cutoff != nil {
		var legacyOrders int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_idempotency WHERE mode='paper'
			AND json_extract(intent_json, '$.signal_schema_version') IN ('paper-signal.v1','paper-signal.v2')`).Scan(&legacyOrders); err != nil {
			return nil, err
		}
		if legacyOrders != 0 {
			return nil, errors.New("bounded paper accounting cannot include a legacy paper account")
		}
	}
	return replayPaperAccountingStateWithCutoff(ctx, q, cutoff)
}

func replayPaperAccountingState(ctx context.Context, q orderQuerier) (map[string]paperAccountState, error) {
	return replayPaperAccountingStateWithCutoff(ctx, q, nil)
}

func replayPaperAccountingStateWithCutoff(ctx context.Context, q orderQuerier, cutoff *paperAccountingCutoff) (map[string]paperAccountState, error) {
	accounts := map[string]*paperdomain.Account{}
	boundedAccounts := map[string]*paperdomain.Account{}
	var cutoffAsOf time.Time
	if cutoff != nil {
		cutoffAsOf, _ = parsePaperTime(cutoff.AsOf)
	}
	rows, err := q.QueryContext(ctx, `SELECT account_ref FROM paper_accounting_sessions ORDER BY sequence`)
	if err != nil {
		return nil, err
	}
	var accountRefs []string
	for rows.Next() {
		var accountRef string
		if err := rows.Scan(&accountRef); err != nil {
			rows.Close()
			return nil, err
		}
		accountRefs = append(accountRefs, accountRef)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	for _, accountRef := range accountRefs {
		session, found, err := loadPaperAccountingSession(ctx, q, accountRef)
		if err != nil || !found {
			return nil, fmt.Errorf("load paper accounting session %q: %w", accountRef, err)
		}
		accounts[accountRef], err = paperdomain.NewAccount(accountRef, session.SessionID, session.StartingCash)
		if err != nil {
			return nil, err
		}
		if cutoff != nil {
			boundedAccounts[accountRef], err = paperdomain.NewAccount(accountRef, session.SessionID, session.StartingCash)
			if err != nil {
				return nil, err
			}
		}
	}
	if err := validatePaperIntentDeltas(ctx, q); err != nil {
		return nil, err
	}

	rows, err = q.QueryContext(ctx, `SELECT events.sequence,events.event_sha256,events.event_json,events.recorded_at,orders.order_id
		FROM order_events events JOIN order_idempotency orders ON orders.order_id=events.order_id
		WHERE events.event_type='FILL_RECORDED' AND orders.mode='paper'
		  AND json_extract(orders.intent_json, '$.signal_schema_version')='paper-signal.v3'
		ORDER BY events.sequence`)
	if err != nil {
		return nil, err
	}
	type storedFill struct {
		sequence              int64
		hash, raw, recordedAt string
		orderID               string
	}
	var fills []storedFill
	for rows.Next() {
		var fill storedFill
		if err := rows.Scan(&fill.sequence, &fill.hash, &fill.raw, &fill.recordedAt, &fill.orderID); err != nil {
			rows.Close()
			return nil, err
		}
		fills = append(fills, fill)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	orderFilled := map[string]*big.Int{}
	usedByOrder := map[string]map[string]bool{}
	consumedByBar := map[string]*big.Int{}
	for _, stored := range fills {
		var event OrderEvent
		if err := json.Unmarshal([]byte(stored.raw), &event); err != nil {
			return nil, err
		}
		canonical, hash, err := orderJSONHash(event)
		if err != nil || string(canonical) != stored.raw || hash != stored.hash || event.OrderID != stored.orderID {
			return nil, errors.New("capitalized paper fill hash or order binding is invalid")
		}
		if err := validateOrderEvent(event); err != nil {
			return nil, err
		}
		intent, err := loadOrderIntentFrom(ctx, q, stored.orderID)
		if err != nil {
			return nil, err
		}
		session, signal, err := validateCapitalizedPaperOrderBindings(ctx, q, intent)
		if err != nil {
			return nil, err
		}
		authorization, found, err := loadPaperExecutionAuthorizationByOrder(ctx, q, stored.orderID)
		if err != nil || !found {
			return nil, errors.New("capitalized paper fill authorization is missing")
		}
		if _, err := validatePaperExecutionAuthorization(ctx, q, authorization); err != nil {
			return nil, err
		}
		policy, err := validatePaperAccountingSession(*session)
		if err != nil {
			return nil, err
		}
		bar, _, err := loadPaperMarketBarByID(ctx, q, event.PaperBarObservationID)
		if err != nil {
			return nil, err
		}
		authority, err := validatePaperFillAuthority(ctx, q, intent.AccountRef, event, stored.recordedAt)
		if err != nil {
			return nil, err
		}
		if event.PaperAuthorizationID != authorization.AuthorizationID || event.PaperAccountingSessionID != session.SessionID ||
			event.PaperSignalEventID != signal.EventID || event.PaperFillPolicyVersion != paperFillPolicyVersion ||
			event.ExecutionAuthorityEventID != authority.EventID || event.FencingToken != authority.FencingToken ||
			event.OccurredAt != bar.OpenAt || event.ProviderOrderRef != paperProviderAlias("order", stored.orderID) ||
			event.ProviderExecutionRef != paperProviderAlias("execution", stored.orderID, bar.ObservationID, paperFillPolicyVersion) ||
			event.EventID != paperEventID("fill", stored.orderID, bar.ObservationID, paperFillPolicyVersion) {
			return nil, errors.New("capitalized paper fill provenance does not match its evidence")
		}
		account := accounts[intent.AccountRef]
		if account == nil {
			return nil, errors.New("capitalized paper fill account session is missing")
		}
		if account.SessionID() != session.SessionID {
			return nil, errors.New("capitalized paper fill account session is missing")
		}
		if orderFilled[stored.orderID] == nil {
			orderFilled[stored.orderID] = new(big.Int)
		}
		total, _ := new(big.Int).SetString(intent.Quantity, 10)
		remaining := new(big.Int).Sub(total, orderFilled[stored.orderID])
		position := account.PositionQuantity(intent.Symbol)
		candidates, err := paperFillBars(ctx, q, *signal, policy.DelayBars)
		if err != nil {
			return nil, err
		}
		if usedByOrder[stored.orderID] == nil {
			usedByOrder[stored.orderID] = map[string]bool{}
		}
		var calculated paperdomain.Fill
		var expectedBar *PaperMarketBarObservation
		for _, candidate := range candidates {
			if usedByOrder[stored.orderID][candidate.ObservationID] {
				continue
			}
			candidateKey := strings.Join([]string{intent.AccountRef, intent.Symbol, candidate.ObservationID}, "\x00")
			if consumedByBar[candidateKey] == nil {
				consumedByBar[candidateKey] = new(big.Int)
			}
			candidateFill, ok, err := paperdomain.CalculateFill(paperExecutionPolicy(policy), paperdomain.FillInput{
				Side: intent.Side, Open: candidate.Open, Volume: candidate.Volume, RemainingQuantity: remaining.String(),
				Cash: account.Cash(), PositionQuantity: position, ConsumedCapacity: consumedByBar[candidateKey].String(),
			})
			if err != nil {
				return nil, err
			}
			if ok {
				calculated, expectedBar = candidateFill, candidate
				break
			}
		}
		if expectedBar == nil || expectedBar.ObservationID != bar.ObservationID {
			return nil, errors.New("capitalized paper fill used an ineligible bar")
		}
		barKey := strings.Join([]string{intent.AccountRef, intent.Symbol, bar.ObservationID}, "\x00")
		if event.Quantity != calculated.Quantity || event.ReferencePrice != calculated.ReferencePrice || event.Price != calculated.Price ||
			event.Fee != calculated.Fee || event.Tax != calculated.Tax || event.Slippage != calculated.Slippage {
			return nil, errors.New("stored capitalized paper fill calculation mismatch")
		}
		if err := account.ApplyFill(intent.Symbol, intent.Side, calculated); err != nil {
			return nil, err
		}
		if cutoff != nil && stored.sequence <= cutoff.OrderSequence {
			barClose, _ := parsePaperTime(bar.CloseAt)
			if !barClose.After(cutoffAsOf) {
				bounded := boundedAccounts[intent.AccountRef]
				if bounded == nil || bounded.SessionID() != session.SessionID {
					return nil, errors.New("bounded paper fill account session is missing")
				}
				if err := bounded.ApplyFill(intent.Symbol, intent.Side, calculated); err != nil {
					return nil, err
				}
			}
		}
		quantity, _ := new(big.Int).SetString(calculated.Quantity, 10)
		orderFilled[stored.orderID].Add(orderFilled[stored.orderID], quantity)
		consumedByBar[barKey].Add(consumedByBar[barKey], quantity)
		usedByOrder[stored.orderID][bar.ObservationID] = true
	}
	result := make(map[string]paperAccountState, len(accounts))
	for accountRef, account := range accounts {
		if cutoff != nil {
			account = boundedAccounts[accountRef]
		}
		state, err := account.State()
		if err != nil {
			return nil, err
		}
		result[accountRef] = state
	}
	return result, nil
}

func validatePaperIntentDeltas(ctx context.Context, q orderQuerier) error {
	rows, err := q.QueryContext(ctx, `SELECT events.sequence,events.event_type,events.event_json,orders.order_id
		FROM order_events events JOIN order_idempotency orders ON orders.order_id=events.order_id
		WHERE orders.mode='paper' AND json_extract(orders.intent_json, '$.signal_schema_version')='paper-signal.v3'
		ORDER BY events.sequence`)
	if err != nil {
		return err
	}
	type row struct {
		sequence                int64
		eventType, raw, orderID string
	}
	var stored []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.sequence, &item.eventType, &item.raw, &item.orderID); err != nil {
			rows.Close()
			return err
		}
		stored = append(stored, item)
	}
	if err := closeRows(rows); err != nil {
		return err
	}
	positions, active := map[string]*big.Int{}, map[string]string{}
	filled := map[string]*big.Int{}
	intents := map[string]OrderIntent{}
	for _, item := range stored {
		intent, ok := intents[item.orderID]
		if !ok {
			intent, err = loadOrderIntentFrom(ctx, q, item.orderID)
			if err != nil {
				return err
			}
			intents[item.orderID] = intent
		}
		key := intent.AccountRef + "\x00" + intent.Symbol
		if positions[key] == nil {
			positions[key] = new(big.Int)
		}
		if item.eventType == "INTENT_RECORDED" {
			if active[key] != "" {
				return errors.New("capitalized paper intents cross an active order")
			}
			target, _ := new(big.Int).SetString(intent.SignalTargetQuantity, 10)
			delta := new(big.Int).Sub(target, positions[key])
			quantity, _ := new(big.Int).SetString(intent.Quantity, 10)
			if delta.Sign() == 0 || new(big.Int).Abs(delta).Cmp(quantity) != 0 || (delta.Sign() > 0) != (intent.Side == "BUY") {
				return errors.New("capitalized paper intent target delta is invalid")
			}
			active[key], filled[item.orderID] = item.orderID, new(big.Int)
			continue
		}
		if item.eventType != "FILL_RECORDED" {
			continue
		}
		var event OrderEvent
		if err := json.Unmarshal([]byte(item.raw), &event); err != nil {
			return err
		}
		quantity, _ := new(big.Int).SetString(event.Quantity, 10)
		if intent.Side == "BUY" {
			positions[key].Add(positions[key], quantity)
		} else {
			positions[key].Sub(positions[key], quantity)
			if positions[key].Sign() < 0 {
				return errors.New("capitalized paper SELL oversold holdings")
			}
		}
		filled[item.orderID].Add(filled[item.orderID], quantity)
		total, _ := new(big.Int).SetString(intent.Quantity, 10)
		if filled[item.orderID].Cmp(total) > 0 {
			return errors.New("capitalized paper fill exceeds its order")
		}
		if filled[item.orderID].Cmp(total) == 0 {
			delete(active, key)
		}
	}
	return nil
}

func paperFillBars(ctx context.Context, q orderQuerier, signal PaperSignalEvent, delay int64) ([]*PaperMarketBarObservation, error) {
	if delay <= 0 {
		return nil, errors.New("paper fill delay is invalid")
	}
	signalBar, _, err := loadPaperMarketBarByID(ctx, q, signal.SignalBarObservationID)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT observation_id FROM paper_market_bar_observations
		WHERE source=? AND symbol=? AND venue=? AND interval=? AND timezone=? AND price_adjustment=? AND sequence>?
		ORDER BY sequence LIMIT -1 OFFSET ?`, signalBar.Source, signalBar.Symbol, signalBar.Venue, signalBar.Interval,
		signalBar.Timezone, signalBar.PriceAdjustment, signal.MarketObservationSequenceCutoff, delay-1)
	if err != nil {
		return nil, err
	}
	var observationIDs []string
	for rows.Next() {
		var observationID string
		if err := rows.Scan(&observationID); err != nil {
			rows.Close()
			return nil, err
		}
		observationIDs = append(observationIDs, observationID)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	bars := make([]*PaperMarketBarObservation, 0, len(observationIDs))
	for _, observationID := range observationIDs {
		bar, _, err := loadPaperMarketBarByID(ctx, q, observationID)
		if err != nil {
			return nil, err
		}
		bars = append(bars, bar)
	}
	return bars, nil
}

func validatePaperFillAuthority(ctx context.Context, q orderQuerier, accountRef string, event OrderEvent, recordedAt string) (executionAuthoritySnapshot, error) {
	record, err := loadExecutionAuthorityRecordByID(ctx, q, event.ExecutionAuthorityEventID)
	if err != nil || record.AccountRef != accountRef || !record.Armed || record.LeaseOwner == "" ||
		record.ReasonCode != "lease_acquired" || record.FencingToken != event.FencingToken {
		return executionAuthoritySnapshot{}, errors.New("paper fill execution authority binding is invalid")
	}
	fillTime, fillOK := canonicalUTCTime(recordedAt)
	recorded, recordedOK := canonicalUTCTime(record.RecordedAt)
	expires, expiresOK := canonicalUTCTime(record.LeaseExpiresAt)
	if !fillOK || !recordedOK || !expiresOK || fillTime.Before(recorded) || !fillTime.Before(expires) {
		return executionAuthoritySnapshot{}, errors.New("paper fill is outside its execution lease")
	}
	return executionAuthoritySnapshot{ExecutionAuthorityState: *authorityState(record), EventID: record.EventID}, nil
}

func validateStoredPaperAccountingSession(ctx context.Context, q orderQuerier, session PaperAccountingSession, recordSHA, recordJSON string) error {
	policy, err := validatePaperAccountingSession(session)
	if err != nil {
		return err
	}
	var artifactJSON string
	if err := q.QueryRowContext(ctx, `SELECT artifact_json FROM strategy_research_evidence WHERE result_sha256=?`, session.StrategyResultSHA256).Scan(&artifactJSON); err != nil {
		return err
	}
	evidence, err := decodeStrategyArtifact([]byte(artifactJSON))
	if err != nil || evidence.ResultSHA256 != session.StrategyResultSHA256 || evidence.executionPolicy.SHA256 != policy.SHA256 ||
		evidence.executionPolicy.canonicalJSON != session.ExecutionPolicyJSON || evidence.executionPolicy.StartingCash != session.StartingCash {
		return errors.New("paper accounting session policy is not derived from strategy evidence")
	}
	var selectedResult string
	if err := q.QueryRowContext(ctx, `SELECT selected_result_sha256 FROM strategy_selection_events WHERE event_id=?`, session.StrategySelectionEventID).Scan(&selectedResult); err != nil {
		return err
	}
	if selectedResult != session.StrategyResultSHA256 {
		return errors.New("paper accounting session strategy binding is invalid")
	}
	canonical, actualSHA, err := orderJSONHash(session)
	if err != nil || string(canonical) != recordJSON || actualSHA != recordSHA {
		return errors.New("paper accounting session record hash mismatch")
	}
	return nil
}

func validatePaperAccountingSession(session PaperAccountingSession) (strategyExecutionPolicy, error) {
	if !safeOrderID(session.SessionID) || session.SchemaVersion != paperAccountingSessionSchema || !orderAlias(session.AccountRef, "account") ||
		!strategySHA256Pattern.MatchString(session.StrategyResultSHA256) || !safeOrderID(session.StrategySelectionEventID) ||
		!strategySHA256Pattern.MatchString(session.ExecutionPolicySHA256) || session.Currency != "KRW" || !canonicalUTCString(session.RecordedAt) {
		return strategyExecutionPolicy{}, errors.New("paper accounting session is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(session.ExecutionPolicyJSON))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return strategyExecutionPolicy{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return strategyExecutionPolicy{}, err
	}
	policy, err := decodeStrategyExecutionContract(raw)
	if err != nil || policy.SHA256 != session.ExecutionPolicySHA256 || policy.canonicalJSON != session.ExecutionPolicyJSON ||
		policy.StartingCash != session.StartingCash || paperAccountingSessionID(session.AccountRef, session.StrategyResultSHA256, session.StrategySelectionEventID, session.ExecutionPolicySHA256) != session.SessionID {
		return strategyExecutionPolicy{}, errors.New("paper accounting session policy or identity is invalid")
	}
	return policy, nil
}
