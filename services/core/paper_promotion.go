package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"omni-folio/services/core/internal/researchdomain"
)

const paperPromotionClaimSchema = "paper-promotion-claim.v1"

type paperPromotionClaim struct {
	SchemaVersion             string `json:"schema_version"`
	AttemptID                 string `json:"attempt_id"`
	ExpectedPreviousAttemptID string `json:"expected_previous_attempt_id"`
	AccountRef                string `json:"account_ref"`
	CandidateResultSHA256     string `json:"candidate_result_sha256"`
	IncumbentResultSHA256     string `json:"incumbent_result_sha256"`
	ExpectedCurrentEventID    string `json:"expected_current_event_id"`
	InputDataSHA256           string `json:"input_data_sha256"`
	InputCSV                  string `json:"input_csv"`
	Symbol                    string `json:"symbol"`
	ComparisonStart           string `json:"comparison_start"`
	ComparisonEnd             string `json:"comparison_end"`
	OrderSequenceCutoff       int64  `json:"order_sequence_cutoff"`
	ResearchSequenceCutoff    int64  `json:"research_sequence_cutoff"`
	StartingCash              string `json:"starting_cash"`
	SessionID                 string `json:"session_id"`
	ExecutionPolicySHA256     string `json:"execution_policy_sha256"`
	StateSHA256               string `json:"state_sha256"`
	RecordedAt                string `json:"recorded_at"`
}

type paperPromotionDecision struct {
	AttemptID              string                    `json:"attempt_id"`
	ClaimSHA256            string                    `json:"claim_sha256"`
	CandidateResultSHA256  string                    `json:"candidate_result_sha256"`
	ExpectedCurrentEventID string                    `json:"expected_current_event_id"`
	Decision               string                    `json:"decision"`
	Reason                 string                    `json:"reason"`
	Comparison             *paperPromotionComparison `json:"comparison,omitempty"`
	SelectionEventID       string                    `json:"selection_event_id,omitempty"`
	RecordedAt             string                    `json:"recorded_at"`
}

type PaperPromotionReceipt struct {
	AttemptID        string                    `json:"attempt_id"`
	InputDataSHA256  string                    `json:"input_data_sha256"`
	ComparisonStart  string                    `json:"comparison_start"`
	ComparisonEnd    string                    `json:"comparison_end"`
	Decision         string                    `json:"decision"`
	Reason           string                    `json:"reason"`
	Comparison       *paperPromotionComparison `json:"comparison,omitempty"`
	SelectionEventID string                    `json:"selection_event_id,omitempty"`
}

type paperPromotionRecoveryProof struct {
	SHA256            string
	Claims, Decisions int
}

func promotionReceipt(claim paperPromotionClaim, decision *paperPromotionDecision) *PaperPromotionReceipt {
	r := &PaperPromotionReceipt{AttemptID: claim.AttemptID, InputDataSHA256: claim.InputDataSHA256,
		ComparisonStart: claim.ComparisonStart, ComparisonEnd: claim.ComparisonEnd, Decision: "PENDING", Reason: "comparison_claimed"}
	if decision != nil {
		r.Decision, r.Reason, r.Comparison, r.SelectionEventID = decision.Decision, decision.Reason, decision.Comparison, decision.SelectionEventID
	}
	return r
}

func (s *Service) promotePaperCandidate(ctx context.Context, account, candidateSHA, expectedSelection, attemptID string, raw []byte) (*PaperPromotionReceipt, error) {
	claim, prior, err := s.claimPaperPromotion(ctx, account, candidateSHA, expectedSelection, attemptID, raw)
	if err != nil || prior != nil {
		return prior, err
	}
	comparison, scoreErr := scorePaperPromotionClaim(ctx, s.db, *claim)
	if ctx.Err() != nil {
		scoreErr = ctx.Err()
	}
	// A cancelled scorer must still consume its interval. A process crash leaves PENDING.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	receipt, err := s.finishPaperPromotion(finishCtx, *claim, comparison, scoreErr, false)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return receipt, ctx.Err()
	}
	return receipt, nil
}

func (s *Service) claimPaperPromotion(ctx context.Context, account, candidateSHA, expectedSelection, attemptID string, raw []byte) (*paperPromotionClaim, *PaperPromotionReceipt, error) {
	if s == nil || s.db == nil || ctx == nil || !orderAlias(account, "account") || !strategySHA256Pattern.MatchString(candidateSHA) || !safeOrderID(expectedSelection) || !safeOrderID(attemptID) {
		return nil, nil, errors.New("paper promotion identifiers are invalid")
	}
	symbol, bars, err := parsePaperPromotionBars(ctx, raw)
	if err != nil {
		return nil, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	if err := requireSchemaContext(ctx, tx); err != nil {
		return nil, nil, err
	}
	if _, err := provePaperPromotionRecovery(ctx, tx); err != nil {
		return nil, nil, err
	}
	existing, decision, found, err := loadPaperPromotion(ctx, tx, attemptID)
	if err != nil {
		return nil, nil, err
	}
	if found {
		if existing.AccountRef != account || existing.CandidateResultSHA256 != candidateSHA || existing.ExpectedCurrentEventID != expectedSelection || existing.InputCSV != string(raw) {
			return nil, nil, errors.New("paper promotion attempt conflicts with its saved request")
		}
		return nil, promotionReceipt(*existing, decision), nil
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM paper_promotion_claims c WHERE NOT EXISTS(SELECT 1 FROM paper_promotion_decisions d WHERE d.attempt_id=c.attempt_id)`).Scan(&pending); err != nil {
		return nil, nil, err
	}
	if pending != 0 {
		return nil, nil, errors.New("a paper comparison is pending; inspect or explicitly abort it first")
	}
	now := s.now().UTC()
	stateSHA, session, cash, selection, err := capturePaperPromotionState(ctx, tx, account, now)
	if err != nil {
		return nil, nil, err
	}
	if selection.CurrentEventID != expectedSelection || selection.SelectedResultSHA256 == candidateSHA || selection.SelectedResultSHA256 == noStrategySelection {
		return nil, nil, errors.New("paper promotion requires a distinct candidate and the current incumbent")
	}
	candidate, incumbent, _, err := paperPromotionInputs(ctx, tx, candidateSHA, selection.SelectedResultSHA256, cash, session.ExecutionPolicySHA256)
	if err != nil {
		return nil, nil, err
	}
	warmup := int(max(candidate.SlowWindow, incumbent.SlowWindow))
	if len(bars) != warmup+90 {
		return nil, nil, errors.New("paper promotion requires common warmup and three fresh 30-bar windows")
	}
	end, _ := paperResearchWholeSecond(bars[len(bars)-1].At)
	if end.After(now) {
		return nil, nil, errors.New("paper promotion input contains future bars")
	}
	hash := sha256.Sum256(raw)
	claim := paperPromotionClaim{SchemaVersion: paperPromotionClaimSchema, AttemptID: attemptID, ExpectedPreviousAttemptID: "no_attempt",
		AccountRef: account, CandidateResultSHA256: candidateSHA, IncumbentResultSHA256: selection.SelectedResultSHA256, ExpectedCurrentEventID: expectedSelection,
		InputDataSHA256: hex.EncodeToString(hash[:]), InputCSV: string(raw), Symbol: symbol, ComparisonStart: bars[warmup].At, ComparisonEnd: bars[len(bars)-1].At,
		StartingCash: cash, SessionID: session.SessionID, ExecutionPolicySHA256: session.ExecutionPolicySHA256, StateSHA256: stateSHA, RecordedAt: now.Format(canonicalPaperTimeLayout)}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM order_events`).Scan(&claim.OrderSequenceCutoff); err != nil {
		return nil, nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM strategy_research_evidence`).Scan(&claim.ResearchSequenceCutoff); err != nil {
		return nil, nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT attempt_id FROM paper_promotion_claims ORDER BY sequence DESC LIMIT 1),'no_attempt')`).Scan(&claim.ExpectedPreviousAttemptID); err != nil {
		return nil, nil, err
	}
	if err := validateStoredPaperPromotionClaim(ctx, tx, claim, 0); err != nil {
		return nil, nil, err
	}
	canonical, recordSHA, err := orderJSONHash(claim)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO paper_promotion_claims(attempt_id,record_sha256,record_json,recorded_at) VALUES(?,?,?,?)`, attemptID, recordSHA, string(canonical), claim.RecordedAt); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return &claim, nil, nil
}

func paperPromotionInputs(ctx context.Context, q orderQuerier, candidateSHA, incumbentSHA, cash, policySHA string) (researchdomain.Strategy, researchdomain.Strategy, researchdomain.Execution, error) {
	var strategies [2]researchdomain.Strategy
	var policies [2]strategyExecutionPolicy
	for i, sha := range []string{candidateSHA, incumbentSHA} {
		var raw string
		if err := q.QueryRowContext(ctx, `SELECT artifact_json FROM strategy_research_evidence WHERE result_sha256=? AND target='paper_candidate'`, sha).Scan(&raw); err != nil {
			return strategies[0], strategies[1], researchdomain.Execution{}, err
		}
		evidence, err := decodeStrategyArtifact([]byte(raw))
		if err != nil {
			return strategies[0], strategies[1], researchdomain.Execution{}, err
		}
		strategies[i], err = paperPromotionStrategy(*evidence)
		if err != nil {
			return strategies[0], strategies[1], researchdomain.Execution{}, err
		}
		policies[i] = evidence.executionPolicy
	}
	if strategies[0].Quantity != strategies[1].Quantity || policies[0].SHA256 != policies[1].SHA256 || policies[0].SHA256 != policySHA {
		return strategies[0], strategies[1], researchdomain.Execution{}, errors.New("paper promotion requires identical quantity and execution policy")
	}
	return strategies[0], strategies[1], researchdomain.Execution{StartingCash: cash, Policy: paperExecutionPolicy(policies[0]), DelayBars: int(policies[0].DelayBars)}, nil
}

func scorePaperPromotionClaim(ctx context.Context, q orderQuerier, claim paperPromotionClaim) (paperPromotionComparison, error) {
	_, bars, err := parsePaperPromotionBars(ctx, []byte(claim.InputCSV))
	if err != nil {
		return paperPromotionComparison{}, err
	}
	candidate, incumbent, execution, err := paperPromotionInputs(ctx, q, claim.CandidateResultSHA256, claim.IncumbentResultSHA256, claim.StartingCash, claim.ExecutionPolicySHA256)
	if err != nil {
		return paperPromotionComparison{}, err
	}
	return comparePaperPromotion(ctx, bars, candidate, incumbent, execution)
}

func capturePaperPromotionState(ctx context.Context, q orderQuerier, account string, now time.Time) (string, *PaperAccountingSession, string, strategyRegistryRecoveryProof, error) {
	var empty strategyRegistryRecoveryProof
	policyProof, err := provePaperPerformancePolicyRecovery(ctx, q)
	if err != nil {
		return "", nil, "", empty, err
	}
	if err := rejectLivePaperRunnerLease(ctx, q, now); err != nil {
		return "", nil, "", empty, err
	}
	selection, err := replayStrategyRegistry(ctx, q)
	if err != nil {
		return "", nil, "", empty, err
	}
	if err := requireIsolatedPaperAccount(ctx, q, account); err != nil {
		return "", nil, "", empty, err
	}
	var count int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM paper_accounting_sessions`).Scan(&count); err != nil {
		return "", nil, "", empty, err
	}
	if count != 1 {
		return "", nil, "", empty, errors.New("paper promotion requires exactly one existing paper account session")
	}
	session, found, err := loadPaperAccountingSession(ctx, q, account)
	if err != nil || !found {
		return "", nil, "", empty, errors.New("paper promotion account session is missing")
	}
	if _, err := validatePaperAccountingSession(*session); err != nil {
		return "", nil, "", empty, err
	}
	accounts, err := replayPaperAccountingWithoutRegistry(ctx, q, nil)
	if err != nil {
		return "", nil, "", empty, err
	}
	state := accounts[account]
	for _, lots := range state.Lots {
		if len(lots) != 0 {
			return "", nil, "", empty, errors.New("paper promotion requires a flat paper account")
		}
	}
	armed, err := armedExecutionAccounts(ctx, q)
	if err != nil {
		return "", nil, "", empty, err
	}
	if len(armed) != 0 {
		return "", nil, "", empty, errors.New("paper promotion requires all execution authorities halted")
	}
	if err := rejectIncompletePaperRunForExpiredClaim(ctx, q, paperRunnerLeaseRecord{AccountRef: account, StrategySelectionEventID: selection.CurrentEventID}); err != nil {
		return "", nil, "", empty, err
	}
	rows, err := q.QueryContext(ctx, `SELECT order_id FROM order_idempotency WHERE account_ref=?`, account)
	if err != nil {
		return "", nil, "", empty, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", nil, "", empty, err
		}
		ids = append(ids, id)
	}
	if err := closeRows(rows); err != nil {
		return "", nil, "", empty, err
	}
	for _, id := range ids {
		order, err := loadOrderStateFrom(ctx, q, id)
		if err != nil {
			return "", nil, "", empty, err
		}
		if order.PendingAction != "" || (order.Status != "FILLED" && order.Status != "CANCELED" && order.Status != "REJECTED" && order.Status != "RISK_REJECTED") {
			return "", nil, "", empty, errors.New("paper promotion requires every account order terminal")
		}
	}
	orderProof, err := proveOrderRecovery(ctx, q)
	if err != nil {
		return "", nil, "", empty, err
	}
	authoritySHA, _, err := proveExecutionAuthorityRecovery(ctx, q)
	if err != nil {
		return "", nil, "", empty, err
	}
	runner, err := provePaperRunnerLeaseRecovery(ctx, q)
	if err != nil {
		return "", nil, "", empty, err
	}
	var researchSequence int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM strategy_research_evidence`).Scan(&researchSequence); err != nil {
		return "", nil, "", empty, err
	}
	_, stateSHA, err := orderJSONHash([]any{selection.CurrentEventID, selection.SelectedResultSHA256, session, state, orderProof, authoritySHA, policyProof, runner, researchSequence})
	return stateSHA, session, state.Cash, selection, err
}

func (s *Service) finishPaperPromotion(ctx context.Context, claim paperPromotionClaim, comparison paperPromotionComparison, scoreErr error, abort bool) (*PaperPromotionReceipt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := provePaperPromotionRecovery(ctx, tx); err != nil {
		return nil, err
	}
	saved, prior, found, err := loadPaperPromotion(ctx, tx, claim.AttemptID)
	if err != nil {
		return nil, err
	}
	if !found || !reflect.DeepEqual(*saved, claim) {
		return nil, errors.New("paper promotion claim changed")
	}
	if prior != nil {
		return promotionReceipt(claim, prior), nil
	}
	_, claimSHA, err := orderJSONHash(claim)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	claimedAt, _ := parsePaperTime(claim.RecordedAt)
	if now.Before(claimedAt) {
		return nil, errors.New("paper promotion clock regressed")
	}
	decision := paperPromotionDecision{AttemptID: claim.AttemptID, ClaimSHA256: claimSHA, CandidateResultSHA256: claim.CandidateResultSHA256, ExpectedCurrentEventID: claim.ExpectedCurrentEventID, RecordedAt: now.Format(canonicalPaperTimeLayout)}
	switch {
	case abort:
		decision.Decision, decision.Reason = "ABORTED", "explicit_abort"
	case scoreErr != nil:
		decision.Decision, decision.Reason = "ERROR", "comparison_failed"
	default:
		decision.Comparison = &comparison
		decision.Decision, decision.Reason = comparison.Decision, comparison.Reason
		stateSHA, _, _, selection, stateErr := capturePaperPromotionState(ctx, tx, claim.AccountRef, now)
		if stateErr != nil || stateSHA != claim.StateSHA256 || selection.CurrentEventID != claim.ExpectedCurrentEventID {
			decision.Decision, decision.Reason = "STALE", "account_or_selection_changed"
		}
	}
	var event StrategySelectionEvent
	if decision.Decision == "PROMOTE" {
		sequence, err := latestPaperEvaluationSequence(ctx, tx)
		if err != nil {
			return nil, err
		}
		decision.SelectionEventID = s.id("strategy_selection")
		event = StrategySelectionEvent{EventID: decision.SelectionEventID, EventType: "SELECT", CandidateResultSHA256: claim.CandidateResultSHA256,
			ExpectedCurrentEventID: claim.ExpectedCurrentEventID, PreviousSelectedResultSHA256: claim.IncumbentResultSHA256, SelectedResultSHA256: claim.CandidateResultSHA256,
			ReasonCode: "auto_comparison", PaperEvaluationSequence: sequence, PaperPromotionAttemptID: claim.AttemptID, RecordedAt: decision.RecordedAt}
	}
	canonical, sha, err := orderJSONHash(decision)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO paper_promotion_decisions(attempt_id,selection_event_id,record_sha256,record_json,recorded_at) VALUES(?,?,?,?,?)`, claim.AttemptID, nullable(decision.SelectionEventID), sha, string(canonical), decision.RecordedAt); err != nil {
		return nil, err
	}
	if decision.SelectionEventID != "" {
		if err := insertStrategySelectionEvent(ctx, tx, event); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return promotionReceipt(claim, &decision), nil
}

func (s *Service) paperPromotionStatus(ctx context.Context, attemptID string) (*PaperPromotionReceipt, error) {
	if !safeOrderID(attemptID) {
		return nil, errors.New("paper promotion attempt ID is invalid")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := requireSchemaContext(ctx, tx); err != nil {
		return nil, err
	}
	if _, err := replayStrategyRegistry(ctx, tx); err != nil {
		return nil, err
	}
	claim, decision, found, err := loadPaperPromotion(ctx, tx, attemptID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("paper promotion attempt does not exist")
	}
	return promotionReceipt(*claim, decision), nil
}

func (s *Service) abortPaperPromotion(ctx context.Context, attemptID string) (*PaperPromotionReceipt, error) {
	if !safeOrderID(attemptID) {
		return nil, errors.New("paper promotion attempt ID is invalid")
	}
	claim, _, found, err := loadPaperPromotion(ctx, s.db, attemptID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("paper promotion attempt does not exist")
	}
	return s.finishPaperPromotion(ctx, *claim, paperPromotionComparison{}, nil, true)
}

func loadPaperPromotion(ctx context.Context, q orderQuerier, id string) (*paperPromotionClaim, *paperPromotionDecision, bool, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT record_json FROM paper_promotion_claims WHERE attempt_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	var claim paperPromotionClaim
	if err := json.Unmarshal([]byte(raw), &claim); err != nil {
		return nil, nil, false, err
	}
	err = q.QueryRowContext(ctx, `SELECT record_json FROM paper_promotion_decisions WHERE attempt_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return &claim, nil, true, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	var decision paperPromotionDecision
	if err := json.Unmarshal([]byte(raw), &decision); err != nil {
		return nil, nil, false, err
	}
	return &claim, &decision, true, nil
}

func validatePaperPromotionFreshRange(ctx context.Context, q orderQuerier, claim paperPromotionClaim, sequence int64) error {
	start, ok := paperResearchWholeSecond(claim.ComparisonStart)
	end, endOK := paperResearchWholeSecond(claim.ComparisonEnd)
	if !ok || !endOK || end.Before(start) {
		return errors.New("paper promotion interval is invalid")
	}
	rows, err := q.QueryContext(ctx, `SELECT artifact_json FROM strategy_research_evidence WHERE sequence<=? ORDER BY sequence`, claim.ResearchSequenceCutoff)
	if err != nil {
		return err
	}
	var artifacts []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		artifacts = append(artifacts, raw)
	}
	if err := closeRows(rows); err != nil {
		return err
	}
	for _, raw := range artifacts {
		var artifact struct {
			Evaluation struct {
				Folds        []struct{ Train, Validation map[string]any }
				FinalHoldout map[string]any `json:"final_holdout"`
			}
		}
		if json.Unmarshal([]byte(raw), &artifact) != nil || len(artifact.Evaluation.Folds) < 2 {
			return errors.New("registered research interval cannot be verified")
		}
		periods := []map[string]any{artifact.Evaluation.FinalHoldout}
		for _, fold := range artifact.Evaluation.Folds {
			periods = append(periods, fold.Train, fold.Validation)
		}
		for _, period := range periods {
			first, firstOK := paperResearchWholeSecond(stringField(period, "first_bar_at"))
			last, lastOK := paperResearchWholeSecond(stringField(period, "last_bar_at"))
			if !firstOK || !lastOK || last.Before(first) {
				return errors.New("registered research interval cannot be verified")
			}
			if !start.After(last) && !end.Before(first) {
				return errors.New("paper comparison overlaps registered research data")
			}
		}
	}
	var overlap int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM paper_promotion_claims WHERE (?=0 OR sequence<?) AND json_extract(record_json,'$.comparison_start')<=? AND json_extract(record_json,'$.comparison_end')>=?`, sequence, sequence, claim.ComparisonEnd, claim.ComparisonStart).Scan(&overlap); err != nil {
		return err
	}
	if overlap != 0 {
		return errors.New("paper comparison interval has already been consumed")
	}
	return nil
}

func validatePaperPromotionSignalCutoff(ctx context.Context, q orderQuerier, result, selectionID, asOf string) error {
	var schema int
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&schema); err != nil {
		return err
	}
	if schema < 27 {
		return nil
	}
	var cutoff sql.NullString
	err := q.QueryRowContext(ctx, `SELECT MAX(json_extract(c.record_json,'$.comparison_end')) FROM paper_promotion_decisions d
		JOIN paper_promotion_claims c ON c.attempt_id=d.attempt_id JOIN strategy_selection_events s ON s.event_id=d.selection_event_id
		WHERE s.selected_result_sha256=? AND s.sequence<=(SELECT sequence FROM strategy_selection_events WHERE event_id=?)`, result, selectionID).Scan(&cutoff)
	if err != nil {
		return err
	}
	if cutoff.Valid {
		at, ok := parsePaperTime(asOf)
		end, endOK := paperResearchWholeSecond(cutoff.String)
		if !ok || !endOK || !at.After(end) {
			return fmt.Errorf("paper signal must follow the automatic comparison interval")
		}
	}
	return nil
}
