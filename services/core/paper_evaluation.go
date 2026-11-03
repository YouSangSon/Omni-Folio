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

const (
	noPaperEvaluation           = "no_evaluation"
	paperEvaluationSchema       = "paper-operational-evaluation.v1"
	paperEvaluationPolicy       = "paper-operational-safety.v1"
	paperEvaluationInsufficient = "INSUFFICIENT"
	paperEvaluationPass         = "PASS"
	paperEvaluationDegraded     = "DEGRADED"
)

type PaperEvaluationEvent struct {
	EvaluationID                 string `json:"evaluation_id"`
	SchemaVersion                string `json:"schema_version"`
	PolicyVersion                string `json:"policy_version"`
	AccountRef                   string `json:"account_ref"`
	StrategyResultSHA256         string `json:"strategy_result_sha256"`
	StrategySelectionEventID     string `json:"strategy_selection_event_id"`
	ExpectedPreviousEvaluationID string `json:"expected_previous_evaluation_id"`
	PaperOrderStateSHA256        string `json:"paper_order_state_sha256"`
	OrderCount                   int    `json:"order_count"`
	TerminalOrderCount           int    `json:"terminal_order_count"`
	ActiveOrderCount             int    `json:"active_order_count"`
	PendingActionCount           int    `json:"pending_action_count"`
	Decision                     string `json:"decision"`
	ReasonCode                   string `json:"reason_code"`
	RecordedAt                   string `json:"recorded_at"`
}

type paperOperationalSnapshot struct {
	SHA256   string
	Orders   int
	Terminal int
	Active   int
	Pending  int
}

func (s *Service) evaluatePaperOperations(ctx context.Context, accountRef, resultSHA256, selectionEventID string) (*PaperEvaluationEvent, error) {
	if s == nil || s.db == nil || !orderAlias(accountRef, "account") ||
		!strategySHA256Pattern.MatchString(resultSHA256) || !safeOrderID(selectionEventID) {
		return nil, errors.New("paper evaluation identity is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := proveOrderRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("paper evaluation order recovery: %w", err)
	}
	registry, err := replayStrategyRegistry(ctx, tx)
	if err != nil {
		return nil, err
	}
	if registry.SelectedResultSHA256 != resultSHA256 || registry.CurrentEventID != selectionEventID {
		return nil, errors.New("paper evaluation is not bound to the current strategy selection")
	}
	snapshot, err := derivePaperOperationalSnapshot(ctx, tx, accountRef, resultSHA256, selectionEventID)
	if err != nil {
		return nil, err
	}
	event := PaperEvaluationEvent{
		SchemaVersion: paperEvaluationSchema, PolicyVersion: paperEvaluationPolicy, AccountRef: accountRef,
		StrategyResultSHA256: resultSHA256, StrategySelectionEventID: selectionEventID,
		PaperOrderStateSHA256: snapshot.SHA256, OrderCount: snapshot.Orders,
		TerminalOrderCount: snapshot.Terminal, ActiveOrderCount: snapshot.Active, PendingActionCount: snapshot.Pending,
	}
	event.Decision, event.ReasonCode = paperOperationalDecision(snapshot)
	event.EvaluationID = paperEvaluationID(event)
	stored, found, err := loadPaperEvaluationByID(ctx, tx, event.EvaluationID)
	if err != nil {
		return nil, err
	}
	if found {
		if !samePaperEvaluationSnapshot(stored, event) {
			return nil, errors.New("paper evaluation identity is already bound to different evidence")
		}
		return stored, nil
	}
	event.ExpectedPreviousEvaluationID, err = latestPaperEvaluationID(ctx, tx, accountRef, selectionEventID)
	if err != nil {
		return nil, err
	}
	event.RecordedAt = s.now().UTC().Format(time.RFC3339Nano)
	if err := insertPaperEvaluationEvent(ctx, tx, event); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &event, nil
}

func derivePaperOperationalSnapshot(ctx context.Context, q orderQuerier, accountRef, resultSHA256, selectionEventID string) (paperOperationalSnapshot, error) {
	rows, err := q.QueryContext(ctx, `SELECT order_id FROM order_idempotency WHERE mode='paper' AND account_ref=? ORDER BY order_id`, accountRef)
	if err != nil {
		return paperOperationalSnapshot{}, err
	}
	var orderIDs []string
	for rows.Next() {
		var orderID string
		if err := rows.Scan(&orderID); err != nil {
			rows.Close()
			return paperOperationalSnapshot{}, err
		}
		orderIDs = append(orderIDs, orderID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return paperOperationalSnapshot{}, err
	}
	if err := rows.Close(); err != nil {
		return paperOperationalSnapshot{}, err
	}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	result := paperOperationalSnapshot{}
	for _, orderID := range orderIDs {
		intent, err := loadOrderIntentFrom(ctx, q, orderID)
		if err != nil {
			return paperOperationalSnapshot{}, err
		}
		if intent.StrategyResultSHA256 != resultSHA256 || intent.StrategySelectionEventID != selectionEventID {
			continue
		}
		state, err := loadOrderStateFrom(ctx, q, orderID)
		if err != nil {
			return paperOperationalSnapshot{}, err
		}
		if intent.Mode != "paper" || intent.AccountRef != accountRef || state.AccountRef != accountRef {
			return paperOperationalSnapshot{}, errors.New("paper evaluation order scope is invalid")
		}
		intentJSON, _, err := orderJSONHash(intent)
		if err != nil {
			return paperOperationalSnapshot{}, err
		}
		stateJSON, _, err := orderJSONHash(state)
		if err != nil {
			return paperOperationalSnapshot{}, err
		}
		if err := encoder.Encode([]any{"paper_order", orderID, string(intentJSON), string(stateJSON)}); err != nil {
			return paperOperationalSnapshot{}, err
		}
		result.Orders++
		switch {
		case state.PendingAction != "":
			result.Pending++
		case state.Status == "FILLED" || state.Status == "CANCELED" || state.Status == "REJECTED" || state.Status == "RISK_REJECTED":
			result.Terminal++
		default:
			result.Active++
		}
	}
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

func paperOperationalDecision(snapshot paperOperationalSnapshot) (string, string) {
	if snapshot.Pending > 0 {
		return paperEvaluationDegraded, "unresolved_action"
	}
	if snapshot.Terminal == 0 {
		return paperEvaluationInsufficient, "no_terminal_sample"
	}
	return paperEvaluationPass, "operationally_complete"
}

func paperEvaluationID(event PaperEvaluationEvent) string {
	hash := sha256.Sum256([]byte(strings.Join([]string{
		event.PolicyVersion, event.AccountRef, event.StrategyResultSHA256,
		event.StrategySelectionEventID, event.PaperOrderStateSHA256,
	}, "\x00")))
	return "paper_evaluation_" + hex.EncodeToString(hash[:16])
}

func samePaperEvaluationSnapshot(left *PaperEvaluationEvent, right PaperEvaluationEvent) bool {
	if left == nil {
		return false
	}
	copy := *left
	copy.ExpectedPreviousEvaluationID = ""
	copy.RecordedAt = ""
	right.ExpectedPreviousEvaluationID = ""
	right.RecordedAt = ""
	return copy == right
}

func latestPaperEvaluationID(ctx context.Context, q orderQuerier, accountRef, selectionEventID string) (string, error) {
	var evaluationID string
	err := q.QueryRowContext(ctx, `SELECT evaluation_id FROM paper_evaluation_events WHERE account_ref=? AND strategy_selection_event_id=? ORDER BY sequence DESC LIMIT 1`, accountRef, selectionEventID).Scan(&evaluationID)
	if errors.Is(err, sql.ErrNoRows) {
		return noPaperEvaluation, nil
	}
	return evaluationID, err
}

func latestPaperEvaluationSequence(ctx context.Context, q orderQuerier) (int64, error) {
	var sequence int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) FROM paper_evaluation_events`).Scan(&sequence)
	return sequence, err
}

func insertPaperEvaluationEvent(ctx context.Context, tx *sql.Tx, event PaperEvaluationEvent) error {
	if err := validatePaperEvaluationEvent(event); err != nil {
		return err
	}
	recordJSON, recordSHA, err := orderJSONHash(event)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO paper_evaluation_events(
		evaluation_id,schema_version,policy_version,account_ref,strategy_result_sha256,strategy_selection_event_id,
		expected_previous_evaluation_id,paper_order_state_sha256,order_count,terminal_order_count,active_order_count,
		pending_action_count,decision,reason_code,record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, event.EvaluationID, event.SchemaVersion, event.PolicyVersion,
		event.AccountRef, event.StrategyResultSHA256, event.StrategySelectionEventID, event.ExpectedPreviousEvaluationID,
		event.PaperOrderStateSHA256, event.OrderCount, event.TerminalOrderCount, event.ActiveOrderCount,
		event.PendingActionCount, event.Decision, event.ReasonCode, recordSHA, string(recordJSON), event.RecordedAt)
	return err
}

func loadPaperEvaluationByID(ctx context.Context, q orderQuerier, evaluationID string) (*PaperEvaluationEvent, bool, error) {
	row := q.QueryRowContext(ctx, `SELECT schema_version,policy_version,account_ref,strategy_result_sha256,strategy_selection_event_id,
		expected_previous_evaluation_id,paper_order_state_sha256,order_count,terminal_order_count,active_order_count,
		pending_action_count,decision,reason_code,record_sha256,record_json,recorded_at
		FROM paper_evaluation_events WHERE evaluation_id=?`, evaluationID)
	var event PaperEvaluationEvent
	var recordSHA, recordJSON string
	event.EvaluationID = evaluationID
	err := row.Scan(&event.SchemaVersion, &event.PolicyVersion, &event.AccountRef, &event.StrategyResultSHA256,
		&event.StrategySelectionEventID, &event.ExpectedPreviousEvaluationID, &event.PaperOrderStateSHA256,
		&event.OrderCount, &event.TerminalOrderCount, &event.ActiveOrderCount, &event.PendingActionCount,
		&event.Decision, &event.ReasonCode, &recordSHA, &recordJSON, &event.RecordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := validateStoredPaperEvaluation(event, recordSHA, recordJSON); err != nil {
		return nil, false, err
	}
	return &event, true, nil
}

func validateStoredPaperEvaluation(event PaperEvaluationEvent, recordSHA, recordJSON string) error {
	if err := validatePaperEvaluationEvent(event); err != nil {
		return err
	}
	canonical, actualSHA, err := orderJSONHash(event)
	if err != nil || string(canonical) != recordJSON || actualSHA != recordSHA {
		return errors.New("paper evaluation metadata or hash mismatch")
	}
	return nil
}

func validatePaperEvaluationEvent(event PaperEvaluationEvent) error {
	if !safeOrderID(event.EvaluationID) || event.SchemaVersion != paperEvaluationSchema || event.PolicyVersion != paperEvaluationPolicy ||
		!orderAlias(event.AccountRef, "account") || !strategySHA256Pattern.MatchString(event.StrategyResultSHA256) ||
		!safeOrderID(event.StrategySelectionEventID) || !safeOrderID(event.ExpectedPreviousEvaluationID) ||
		!strategySHA256Pattern.MatchString(event.PaperOrderStateSHA256) || !canonicalUTCString(event.RecordedAt) ||
		event.OrderCount < 0 || event.TerminalOrderCount < 0 || event.ActiveOrderCount < 0 || event.PendingActionCount < 0 ||
		event.OrderCount != event.TerminalOrderCount+event.ActiveOrderCount+event.PendingActionCount {
		return errors.New("paper evaluation event is invalid")
	}
	if paperEvaluationID(event) != event.EvaluationID {
		return errors.New("paper evaluation identity is invalid")
	}
	expectedDecision, expectedReason := paperOperationalDecision(paperOperationalSnapshot{
		Orders: event.OrderCount, Terminal: event.TerminalOrderCount, Active: event.ActiveOrderCount, Pending: event.PendingActionCount,
	})
	if event.Decision != expectedDecision || event.ReasonCode != expectedReason {
		return errors.New("paper evaluation decision is invalid")
	}
	return nil
}
