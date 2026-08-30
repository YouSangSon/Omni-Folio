package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
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
	SHA256                        string
	Sessions, MarketBars, Signals int
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
	if includeMarket {
		market, err = replayPaperMarketRecovery(ctx, q)
		if err != nil {
			return paperAccountingRecoveryProof{}, fmt.Errorf("paper market recovery: %w", err)
		}
		if err := encoder.Encode([]any{"paper_market_recovery", market.SHA256, market.Bars, market.Signals}); err != nil {
			return paperAccountingRecoveryProof{}, err
		}
	}
	return paperAccountingRecoveryProof{
		SHA256: hex.EncodeToString(hash.Sum(nil)), Sessions: len(stored), MarketBars: market.Bars, Signals: market.Signals,
	}, nil
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
