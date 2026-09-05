package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"regexp"
	"syscall"
	"time"
)

const (
	noStrategySelection      = "no_strategy"
	noStrategySelectionEvent = "no_event"
	strategyResultSchema     = "strategy-improvement-result.v1"
	strategyEvaluationPolicy = "sma-expanding-walk-forward.v1"
)

var strategySHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type StrategyEvidence struct {
	ResultSHA256    string `json:"result_sha256"`
	ArtifactSHA256  string `json:"artifact_sha256"`
	StrategyName    string `json:"strategy_name"`
	StrategyVersion string `json:"strategy_version"`
	ParameterSHA256 string `json:"parameter_sha256"`
	Target          string `json:"target"`
	RecordedAt      string `json:"recorded_at"`
	artifactJSON    string
	executionPolicy strategyExecutionPolicy
}

type strategyExecutionPolicy struct {
	StartingCash, Fee, Tax, SlippageBPS, MaxParticipation string
	DelayBars                                             int64
	SignalPrice, FillPrice, SHA256, canonicalJSON         string
}

type StrategySelectionEvent struct {
	EventID                       string `json:"event_id"`
	EventType                     string `json:"event_type"`
	CandidateResultSHA256         string `json:"candidate_result_sha256,omitempty"`
	ExpectedCurrentEventID        string `json:"expected_current_event_id"`
	SourceEventID                 string `json:"source_event_id,omitempty"`
	PreviousSelectedResultSHA256  string `json:"previous_selected_result_sha256"`
	SelectedResultSHA256          string `json:"selected_result_sha256"`
	ReasonCode                    string `json:"reason_code"`
	PaperEvaluationSequence       int64  `json:"paper_evaluation_sequence,omitempty"`
	PaperPerformancePolicyEventID string `json:"paper_performance_policy_event_id,omitempty"`
	RecordedAt                    string `json:"recorded_at"`
}

type StrategySelectionState struct {
	CurrentEventID       string `json:"current_event_id"`
	SelectedResultSHA256 string `json:"selected_result_sha256"`
}

type strategyRegistryRecoveryProof struct {
	SHA256               string
	Evidence             int
	Events               int
	Evaluations          int
	SelectedResultSHA256 string
	CurrentEventID       string
	stack                []string
}

func validateStrategyOrderSelection(ctx context.Context, q orderQuerier, intent OrderIntent) error {
	if intent.StrategyResultSHA256 == "" && intent.StrategySelectionEventID == "" {
		return nil
	}
	state, err := replayStrategyRegistry(ctx, q)
	if err != nil {
		return err
	}
	if state.SelectedResultSHA256 != intent.StrategyResultSHA256 || state.CurrentEventID != intent.StrategySelectionEventID {
		return errors.New("strategy order is not bound to the current paper candidate")
	}
	return nil
}

func sameStrategyRegistryProof(left, right strategyRegistryRecoveryProof) bool {
	return left.SHA256 == right.SHA256 && left.Evidence == right.Evidence && left.Events == right.Events && left.Evaluations == right.Evaluations &&
		left.SelectedResultSHA256 == right.SelectedResultSHA256 && left.CurrentEventID == right.CurrentEventID
}

func readStrategyArtifact(path string) ([]byte, error) {
	// Open first without waiting for a FIFO writer, then validate the actual
	// descriptor. A path-only Stat check would race a regular-file replacement.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("strategy evidence path must be a regular file")
	}
	artifact, err := io.ReadAll(io.LimitReader(file, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(artifact) > maxBodyBytes {
		return nil, errors.New("strategy evidence exceeds the 1 MiB limit")
	}
	return artifact, nil
}

func (s *Service) registerStrategyEvidence(ctx context.Context, artifact []byte) (*StrategyEvidence, error) {
	evidence, err := decodeStrategyArtifact(artifact)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := provePaperPerformancePolicyRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("strategy evidence policy recovery: %w", err)
	}
	var storedArtifact, recordedAt string
	err = tx.QueryRowContext(ctx, `SELECT artifact_json,recorded_at FROM strategy_research_evidence WHERE result_sha256=?`, evidence.ResultSHA256).Scan(&storedArtifact, &recordedAt)
	if err == nil {
		if storedArtifact != evidence.artifactJSON {
			return nil, errors.New("strategy result hash is already bound to different evidence")
		}
		evidence.RecordedAt = recordedAt
		return evidence, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	evidence.RecordedAt = s.now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `INSERT INTO strategy_research_evidence(
		result_sha256,artifact_sha256,strategy_name,strategy_version,parameter_sha256,target,artifact_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?)`, evidence.ResultSHA256, evidence.ArtifactSHA256, evidence.StrategyName,
		evidence.StrategyVersion, evidence.ParameterSHA256, evidence.Target, evidence.artifactJSON, evidence.RecordedAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return evidence, nil
}

func (s *Service) selectPaperCandidate(ctx context.Context, resultSHA256, expectedCurrentEventID string) (*StrategySelectionState, error) {
	if !strategySHA256Pattern.MatchString(resultSHA256) || !safeOrderID(expectedCurrentEventID) {
		return nil, errors.New("strategy selection identifiers are invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := provePaperPerformancePolicyRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("strategy selection policy recovery: %w", err)
	}
	now := s.now().UTC()
	if err := rejectLivePaperRunnerLease(ctx, tx, now); err != nil {
		return nil, err
	}
	state, err := replayStrategyRegistry(ctx, tx)
	if err != nil {
		return nil, err
	}
	if state.CurrentEventID != expectedCurrentEventID {
		return nil, errors.New("stale strategy selection")
	}
	var target string
	if err := tx.QueryRowContext(ctx, `SELECT target FROM strategy_research_evidence WHERE result_sha256=?`, resultSHA256).Scan(&target); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("strategy evidence is not registered")
		}
		return nil, err
	}
	if target != "paper_candidate" {
		return nil, errors.New("only paper_candidate evidence can be selected")
	}
	if state.SelectedResultSHA256 == resultSHA256 {
		return nil, errors.New("strategy evidence is already selected")
	}
	event := StrategySelectionEvent{
		EventID: s.id("strategy_selection"), EventType: "SELECT", CandidateResultSHA256: resultSHA256,
		ExpectedCurrentEventID: expectedCurrentEventID, PreviousSelectedResultSHA256: state.SelectedResultSHA256,
		SelectedResultSHA256: resultSHA256, ReasonCode: "manual_selection", RecordedAt: now.Format(time.RFC3339Nano),
	}
	sequence, err := latestPaperEvaluationSequence(ctx, tx)
	if err != nil {
		return nil, err
	}
	event.PaperEvaluationSequence = sequence
	if err := insertStrategySelectionEvent(ctx, tx, event); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &StrategySelectionState{CurrentEventID: event.EventID, SelectedResultSHA256: resultSHA256}, nil
}

func (s *Service) rollbackPaperCandidate(ctx context.Context, expectedCurrentEventID, sourceEventID string) (*StrategySelectionState, error) {
	if !safeOrderID(expectedCurrentEventID) || !safeOrderID(sourceEventID) {
		return nil, errors.New("strategy rollback identifiers are invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := provePaperPerformancePolicyRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("strategy rollback policy recovery: %w", err)
	}
	now := s.now().UTC()
	if err := rejectLivePaperRunnerLease(ctx, tx, now); err != nil {
		return nil, err
	}
	state, err := replayStrategyRegistry(ctx, tx)
	if err != nil {
		return nil, err
	}
	if state.CurrentEventID != expectedCurrentEventID || sourceEventID != state.CurrentEventID {
		return nil, errors.New("stale or mismatched strategy rollback source")
	}
	if err := s.haltAllSyntheticExecutionTx(ctx, tx, now); err != nil {
		return nil, err
	}
	returned, err := s.rollbackPaperCandidateTx(ctx, tx, state, expectedCurrentEventID, sourceEventID, "manual_rollback", "", s.id("strategy_selection"), now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return returned, nil
}

func (s *Service) rollbackPaperCandidateTx(ctx context.Context, tx *sql.Tx, state strategyRegistryRecoveryProof, expectedCurrentEventID, sourceEventID, reasonCode, policyEventID, eventID string, now time.Time) (*StrategySelectionState, error) {
	if state.CurrentEventID != expectedCurrentEventID || sourceEventID != state.CurrentEventID {
		return nil, errors.New("stale or mismatched strategy rollback source")
	}
	if len(state.stack) == 0 || (reasonCode == "manual_rollback") != (policyEventID == "") ||
		(reasonCode != "manual_rollback" && reasonCode != "automatic_performance_rollback") || !safeOrderID(eventID) {
		return nil, errors.New("strategy rollback transition is invalid")
	}
	nextStack := append([]string(nil), state.stack[:len(state.stack)-1]...)
	selected := noStrategySelection
	if len(nextStack) != 0 {
		selected = nextStack[len(nextStack)-1]
	}
	event := StrategySelectionEvent{
		EventID: eventID, EventType: "ROLLBACK", ExpectedCurrentEventID: expectedCurrentEventID,
		SourceEventID: sourceEventID, PreviousSelectedResultSHA256: state.SelectedResultSHA256,
		SelectedResultSHA256: selected, ReasonCode: reasonCode, PaperPerformancePolicyEventID: policyEventID,
		RecordedAt: now.Format(time.RFC3339Nano),
	}
	sequence, err := latestPaperEvaluationSequence(ctx, tx)
	if err != nil {
		return nil, err
	}
	event.PaperEvaluationSequence = sequence
	if err := insertStrategySelectionEvent(ctx, tx, event); err != nil {
		return nil, err
	}
	return &StrategySelectionState{CurrentEventID: event.EventID, SelectedResultSHA256: selected}, nil
}

func insertStrategySelectionEvent(ctx context.Context, tx *sql.Tx, event StrategySelectionEvent) error {
	recordJSON, recordSHA, err := orderJSONHash(event)
	if err != nil {
		return err
	}
	hasPolicySchema, err := hasPaperPerformancePolicySchema(ctx, tx)
	if err != nil {
		return err
	}
	if !hasPolicySchema {
		_, err = tx.ExecContext(ctx, `INSERT INTO strategy_selection_events(
			event_id,event_type,candidate_result_sha256,expected_current_event_id,source_event_id,
			previous_selected_result_sha256,selected_result_sha256,reason_code,paper_evaluation_sequence,record_sha256,record_json,recorded_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, event.EventID, event.EventType, nullable(event.CandidateResultSHA256),
			event.ExpectedCurrentEventID, nullable(event.SourceEventID), event.PreviousSelectedResultSHA256,
			event.SelectedResultSHA256, event.ReasonCode, event.PaperEvaluationSequence, recordSHA, string(recordJSON), event.RecordedAt)
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO strategy_selection_events(
		event_id,event_type,candidate_result_sha256,expected_current_event_id,source_event_id,
		previous_selected_result_sha256,selected_result_sha256,reason_code,paper_evaluation_sequence,paper_performance_policy_event_id,record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, event.EventID, event.EventType, nullable(event.CandidateResultSHA256),
		event.ExpectedCurrentEventID, nullable(event.SourceEventID), event.PreviousSelectedResultSHA256,
		event.SelectedResultSHA256, event.ReasonCode, event.PaperEvaluationSequence, nullable(event.PaperPerformancePolicyEventID), recordSHA, string(recordJSON), event.RecordedAt)
	return err
}

// ponytail: replay is linear for one personal research registry; add a projection only after measured history makes it hot.
func proveStrategyRegistryRecovery(ctx context.Context, q orderQuerier) (strategyRegistryRecoveryProof, error) {
	return replayStrategyRegistry(ctx, q)
}

func replayStrategyRegistry(ctx context.Context, q orderQuerier) (strategyRegistryRecoveryProof, error) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	var schemaVersion int
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&schemaVersion); err != nil {
		return strategyRegistryRecoveryProof{}, err
	}
	evidenceTargets := map[string]string{}
	rows, err := q.QueryContext(ctx, `SELECT sequence,result_sha256,artifact_sha256,strategy_name,strategy_version,
		parameter_sha256,target,artifact_json,recorded_at FROM strategy_research_evidence ORDER BY sequence`)
	if err != nil {
		return strategyRegistryRecoveryProof{}, err
	}
	evidenceCount := 0
	for rows.Next() {
		var sequence int64
		var resultSHA, artifactSHA, name, version, parameterSHA, target, artifactJSON, recordedAt string
		if err := rows.Scan(&sequence, &resultSHA, &artifactSHA, &name, &version, &parameterSHA, &target, &artifactJSON, &recordedAt); err != nil {
			rows.Close()
			return strategyRegistryRecoveryProof{}, err
		}
		evidence, err := decodeStrategyArtifact([]byte(artifactJSON))
		if err != nil || evidence.ResultSHA256 != resultSHA || evidence.ArtifactSHA256 != artifactSHA ||
			evidence.StrategyName != name || evidence.StrategyVersion != version || evidence.ParameterSHA256 != parameterSHA ||
			evidence.Target != target || evidence.artifactJSON != artifactJSON || !canonicalUTCString(recordedAt) {
			rows.Close()
			return strategyRegistryRecoveryProof{}, fmt.Errorf("strategy evidence %q metadata or hash mismatch", resultSHA)
		}
		if err := encoder.Encode([]any{"strategy_research_evidence", sequence, resultSHA, artifactSHA, name, version, parameterSHA, target, artifactJSON, recordedAt}); err != nil {
			rows.Close()
			return strategyRegistryRecoveryProof{}, err
		}
		evidenceTargets[resultSHA] = target
		evidenceCount++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return strategyRegistryRecoveryProof{}, err
	}
	if err := rows.Close(); err != nil {
		return strategyRegistryRecoveryProof{}, err
	}

	currentEvent, selected := noStrategySelectionEvent, noStrategySelection
	var stack []string
	eventCount := 0
	knownEvents := map[string]bool{}
	selectionResults := map[string]string{}
	selectionRecordedAt := map[string]time.Time{}
	selectionNextAt := map[string]time.Time{}
	selectionOpenSequence := map[string]int64{}
	selectionCloseSequence := map[string]int64{}
	type policyLink struct{ policyID, eventID string }
	var linkedRollbacks []policyLink
	selectionQuery := `SELECT sequence,event_id,event_type,candidate_result_sha256,expected_current_event_id,
		 source_event_id,previous_selected_result_sha256,selected_result_sha256,reason_code,0,NULL,record_sha256,record_json,recorded_at
		FROM strategy_selection_events ORDER BY sequence`
	if schemaVersion >= 20 {
		selectionQuery = `SELECT sequence,event_id,event_type,candidate_result_sha256,expected_current_event_id,
		 source_event_id,previous_selected_result_sha256,selected_result_sha256,reason_code,paper_evaluation_sequence,paper_performance_policy_event_id,record_sha256,record_json,recorded_at
		FROM strategy_selection_events ORDER BY sequence`
	} else if schemaVersion >= 14 {
		selectionQuery = `SELECT sequence,event_id,event_type,candidate_result_sha256,expected_current_event_id,
		 source_event_id,previous_selected_result_sha256,selected_result_sha256,reason_code,paper_evaluation_sequence,NULL,record_sha256,record_json,recorded_at
		FROM strategy_selection_events ORDER BY sequence`
	}
	rows, err = q.QueryContext(ctx, selectionQuery)
	if err != nil {
		return strategyRegistryRecoveryProof{}, err
	}
	for rows.Next() {
		var sequence, paperEvaluationSequence int64
		var eventID, eventType, expectedCurrent, previousSelected, nextSelected, reason, recordSHA, recordJSON, recordedAt string
		var candidateSHA, sourceEvent, policyEventID sql.NullString
		if err := rows.Scan(&sequence, &eventID, &eventType, &candidateSHA, &expectedCurrent, &sourceEvent,
			&previousSelected, &nextSelected, &reason, &paperEvaluationSequence, &policyEventID, &recordSHA, &recordJSON, &recordedAt); err != nil {
			rows.Close()
			return strategyRegistryRecoveryProof{}, err
		}
		var event StrategySelectionEvent
		if err := json.Unmarshal([]byte(recordJSON), &event); err != nil {
			rows.Close()
			return strategyRegistryRecoveryProof{}, err
		}
		canonical, actualSHA, err := orderJSONHash(event)
		if err != nil || string(canonical) != recordJSON || actualSHA != recordSHA || event.EventID != eventID || event.EventType != eventType ||
			event.CandidateResultSHA256 != candidateSHA.String || event.ExpectedCurrentEventID != expectedCurrent ||
			event.SourceEventID != sourceEvent.String || event.PreviousSelectedResultSHA256 != previousSelected ||
			event.SelectedResultSHA256 != nextSelected || event.ReasonCode != reason ||
			event.PaperEvaluationSequence != paperEvaluationSequence || event.PaperPerformancePolicyEventID != policyEventID.String ||
			(event.PaperPerformancePolicyEventID != "") != policyEventID.Valid || event.RecordedAt != recordedAt ||
			!safeOrderID(eventID) || !canonicalUTCString(recordedAt) || expectedCurrent != currentEvent || previousSelected != selected {
			rows.Close()
			return strategyRegistryRecoveryProof{}, fmt.Errorf("strategy selection event %q metadata or hash mismatch", eventID)
		}
		switch eventType {
		case "SELECT":
			if evidenceTargets[candidateSHA.String] != "paper_candidate" || sourceEvent.Valid || reason != "manual_selection" || policyEventID.Valid || nextSelected != candidateSHA.String || nextSelected == selected {
				rows.Close()
				return strategyRegistryRecoveryProof{}, fmt.Errorf("strategy selection event %q is invalid", eventID)
			}
			stack = append(stack, nextSelected)
		case "ROLLBACK":
			if candidateSHA.Valid || !sourceEvent.Valid || sourceEvent.String != currentEvent || !knownEvents[sourceEvent.String] ||
				(reason != "manual_rollback" && reason != "automatic_performance_rollback") ||
				(reason == "automatic_performance_rollback") != policyEventID.Valid || len(stack) == 0 {
				rows.Close()
				return strategyRegistryRecoveryProof{}, fmt.Errorf("strategy rollback event %q is invalid", eventID)
			}
			stack = stack[:len(stack)-1]
			expectedSelected := noStrategySelection
			if len(stack) != 0 {
				expectedSelected = stack[len(stack)-1]
			}
			if nextSelected != expectedSelected {
				rows.Close()
				return strategyRegistryRecoveryProof{}, fmt.Errorf("strategy rollback event %q skipped selection history", eventID)
			}
			if policyEventID.Valid {
				linkedRollbacks = append(linkedRollbacks, policyLink{policyID: policyEventID.String, eventID: eventID})
			}
		default:
			rows.Close()
			return strategyRegistryRecoveryProof{}, fmt.Errorf("strategy selection event %q has unsupported type", eventID)
		}
		selectionHashFields := []any{"strategy_selection_events", sequence, eventID, eventType, candidateSHA, expectedCurrent,
			sourceEvent, previousSelected, nextSelected, reason}
		if schemaVersion >= 14 && paperEvaluationSequence != 0 {
			selectionHashFields = append(selectionHashFields, paperEvaluationSequence)
		}
		if policyEventID.Valid {
			selectionHashFields = append(selectionHashFields, policyEventID)
		}
		selectionHashFields = append(selectionHashFields, recordSHA, recordJSON, recordedAt)
		if err := encoder.Encode(selectionHashFields); err != nil {
			rows.Close()
			return strategyRegistryRecoveryProof{}, err
		}
		knownEvents[eventID] = true
		selectionResults[eventID] = nextSelected
		selectionOpenSequence[eventID] = paperEvaluationSequence
		selectionRecordedAt[eventID], _ = canonicalUTCTime(recordedAt)
		if currentEvent != noStrategySelectionEvent {
			selectionNextAt[currentEvent] = selectionRecordedAt[eventID]
			selectionCloseSequence[currentEvent] = paperEvaluationSequence
		}
		currentEvent, selected = eventID, nextSelected
		eventCount++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return strategyRegistryRecoveryProof{}, err
	}
	if err := rows.Close(); err != nil {
		return strategyRegistryRecoveryProof{}, err
	}
	for _, link := range linkedRollbacks {
		if err := proveLinkedPaperPerformancePolicyMetadata(ctx, q, link.policyID, "rollback", link.eventID, ""); err != nil {
			return strategyRegistryRecoveryProof{}, err
		}
	}

	evaluationCount := 0
	previousEvaluations := map[string]string{}
	var paperEvaluationTable int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='paper_evaluation_events'`).Scan(&paperEvaluationTable); err != nil {
		return strategyRegistryRecoveryProof{}, err
	}
	if paperEvaluationTable == 0 && schemaVersion >= 14 {
		return strategyRegistryRecoveryProof{}, errors.New("paper evaluation table is missing")
	}
	if paperEvaluationTable != 0 {
		rows, err = q.QueryContext(ctx, `SELECT sequence,evaluation_id,schema_version,policy_version,account_ref,
		strategy_result_sha256,strategy_selection_event_id,expected_previous_evaluation_id,paper_order_state_sha256,
		order_count,terminal_order_count,active_order_count,pending_action_count,decision,reason_code,record_sha256,record_json,recorded_at
		FROM paper_evaluation_events ORDER BY sequence`)
		if err != nil {
			return strategyRegistryRecoveryProof{}, err
		}
		for rows.Next() {
			var sequence int64
			var event PaperEvaluationEvent
			var recordSHA, recordJSON string
			if err := rows.Scan(&sequence, &event.EvaluationID, &event.SchemaVersion, &event.PolicyVersion, &event.AccountRef,
				&event.StrategyResultSHA256, &event.StrategySelectionEventID, &event.ExpectedPreviousEvaluationID,
				&event.PaperOrderStateSHA256, &event.OrderCount, &event.TerminalOrderCount, &event.ActiveOrderCount,
				&event.PendingActionCount, &event.Decision, &event.ReasonCode, &recordSHA, &recordJSON, &event.RecordedAt); err != nil {
				rows.Close()
				return strategyRegistryRecoveryProof{}, err
			}
			tuple := event.AccountRef + "\x00" + event.StrategySelectionEventID
			expectedPrevious := noPaperEvaluation
			if previousEvaluations[tuple] != "" {
				expectedPrevious = previousEvaluations[tuple]
			}
			evaluationAt, evaluationTimeOK := canonicalUTCTime(event.RecordedAt)
			selectedAt, selectionTimeOK := selectionRecordedAt[event.StrategySelectionEventID]
			nextAt, hasNextSelection := selectionNextAt[event.StrategySelectionEventID]
			openSequence, hasOpenSequence := selectionOpenSequence[event.StrategySelectionEventID]
			closeSequence, hasCloseSequence := selectionCloseSequence[event.StrategySelectionEventID]
			if err := validateStoredPaperEvaluation(event, recordSHA, recordJSON); err != nil ||
				sequence != int64(evaluationCount+1) || !hasOpenSequence || sequence <= openSequence ||
				(hasCloseSequence && sequence > closeSequence) ||
				event.EvaluationID != paperEvaluationID(event) || event.ExpectedPreviousEvaluationID != expectedPrevious ||
				evidenceTargets[event.StrategyResultSHA256] != "paper_candidate" ||
				selectionResults[event.StrategySelectionEventID] != event.StrategyResultSHA256 || !evaluationTimeOK || !selectionTimeOK ||
				evaluationAt.Before(selectedAt) || (hasNextSelection && evaluationAt.After(nextAt)) {
				rows.Close()
				return strategyRegistryRecoveryProof{}, fmt.Errorf("paper evaluation %q metadata or binding mismatch", event.EvaluationID)
			}
			if err := encoder.Encode([]any{"paper_evaluation_events", sequence, event.EvaluationID, event.SchemaVersion,
				event.PolicyVersion, event.AccountRef, event.StrategyResultSHA256, event.StrategySelectionEventID,
				event.ExpectedPreviousEvaluationID, event.PaperOrderStateSHA256, event.OrderCount, event.TerminalOrderCount,
				event.ActiveOrderCount, event.PendingActionCount, event.Decision, event.ReasonCode, recordSHA, recordJSON, event.RecordedAt}); err != nil {
				rows.Close()
				return strategyRegistryRecoveryProof{}, err
			}
			previousEvaluations[tuple] = event.EvaluationID
			evaluationCount++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return strategyRegistryRecoveryProof{}, err
		}
		if err := rows.Close(); err != nil {
			return strategyRegistryRecoveryProof{}, err
		}
	}
	for _, sequence := range selectionOpenSequence {
		if sequence > int64(evaluationCount) {
			return strategyRegistryRecoveryProof{}, errors.New("strategy selection references a future paper evaluation sequence")
		}
	}
	return strategyRegistryRecoveryProof{
		SHA256: hex.EncodeToString(hash.Sum(nil)), Evidence: evidenceCount, Events: eventCount, Evaluations: evaluationCount,
		SelectedResultSHA256: selected, CurrentEventID: currentEvent, stack: stack,
	}, nil
}

func loadCurrentStrategyExecutionPolicy(ctx context.Context, q orderQuerier, resultSHA256, eventID string) (strategyExecutionPolicy, error) {
	state, err := replayStrategyRegistry(ctx, q)
	if err != nil {
		return strategyExecutionPolicy{}, err
	}
	if state.SelectedResultSHA256 != resultSHA256 || state.CurrentEventID != eventID {
		return strategyExecutionPolicy{}, errors.New("strategy execution policy is not current")
	}
	var artifactJSON string
	if err := q.QueryRowContext(ctx, `SELECT artifact_json FROM strategy_research_evidence WHERE result_sha256=?`, resultSHA256).Scan(&artifactJSON); err != nil {
		return strategyExecutionPolicy{}, err
	}
	evidence, err := decodeStrategyArtifact([]byte(artifactJSON))
	if err != nil {
		return strategyExecutionPolicy{}, err
	}
	if evidence.ResultSHA256 != resultSHA256 {
		return strategyExecutionPolicy{}, errors.New("strategy execution policy result mismatch")
	}
	return evidence.executionPolicy, nil
}

func decodeStrategyArtifact(artifact []byte) (*StrategyEvidence, error) {
	if len(artifact) == 0 || len(artifact) > maxBodyBytes {
		return nil, errors.New("strategy evidence size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(artifact))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("strategy evidence JSON: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	if !exactKeys(result, "schema_version", "experiment_id", "input_sha256", "config_sha256", "manifest", "execution", "evaluation", "candidates", "challenger", "promotion", "disclaimer", "result_sha256") ||
		stringField(result, "schema_version") != strategyResultSchema || stringField(result, "experiment_id") == "" ||
		!strategySHA256Pattern.MatchString(stringField(result, "input_sha256")) || !strategySHA256Pattern.MatchString(stringField(result, "config_sha256")) ||
		stringField(result, "disclaimer") != "Research-only experiment; not investment advice or a production trading authorization." {
		return nil, errors.New("strategy evidence top-level contract is invalid")
	}
	executionPolicy, err := decodeStrategyExecutionContract(result["execution"])
	if err != nil {
		return nil, err
	}
	claimedResultSHA := stringField(result, "result_sha256")
	if !strategySHA256Pattern.MatchString(claimedResultSHA) {
		return nil, errors.New("strategy result hash is invalid")
	}
	manifest, ok := mapField(result, "manifest")
	if !ok || !exactKeys(manifest, "strategy", "data", "engine", "evaluation_policy") {
		return nil, errors.New("strategy evidence manifest is invalid")
	}
	strategy, strategyOK := mapField(manifest, "strategy")
	data, dataOK := mapField(manifest, "data")
	engine, engineOK := mapField(manifest, "engine")
	policy, policyOK := mapField(manifest, "evaluation_policy")
	parameters, parametersOK := mapField(strategy, "parameters")
	if !strategyOK || !dataOK || !engineOK || !policyOK || !parametersOK ||
		!exactKeys(strategy, "name", "version", "parameters", "parameter_hash") || stringField(strategy, "name") != "long_only_sma_crossover" || stringField(strategy, "version") == "" ||
		!exactKeys(data, "version", "input_sha256") || stringField(data, "version") == "" || stringField(data, "input_sha256") != stringField(result, "input_sha256") ||
		!exactKeys(engine, "name", "version") || stringField(engine, "name") != "omni-folio-reference" || stringField(engine, "version") != "0.1.0" ||
		!exactKeys(policy, "version") || stringField(policy, "version") != strategyEvaluationPolicy {
		return nil, errors.New("strategy evidence manifest contract is invalid")
	}
	parameterJSON, err := strategyCanonicalJSON(parameters)
	if err != nil {
		return nil, err
	}
	parameterHash := sha256.Sum256(parameterJSON)
	parameterSHA := hex.EncodeToString(parameterHash[:])
	if stringField(strategy, "parameter_hash") != parameterSHA {
		return nil, errors.New("strategy parameter hash mismatch")
	}
	evaluation, evaluationOK := mapField(result, "evaluation")
	candidates, candidatesOK := result["candidates"].([]any)
	challenger, challengerOK := mapField(result, "challenger")
	promotion, promotionOK := mapField(result, "promotion")
	folds, foldsOK := evaluation["folds"].([]any)
	challengerFolds, challengerFoldsOK := challenger["walk_forward_folds"].([]any)
	if !evaluationOK || !candidatesOK || !challengerOK || !promotionOK || !foldsOK || !challengerFoldsOK ||
		len(candidates) == 0 || len(folds) < 2 || len(challengerFolds) < 2 ||
		stringField(evaluation, "method") != "expanding_walk_forward_then_final_holdout" || evaluation["final_holdout_evaluated_after_selection"] != true ||
		!exactKeys(promotion, "target", "baseline", "walk_forward_gate_passed", "final_holdout_gate_passed", "baseline_gate_passed", "failed_gates") {
		return nil, errors.New("strategy evaluation contract is invalid")
	}
	target := stringField(promotion, "target")
	gateNames := []string{"walk_forward", "final_holdout", "buy_and_hold_baseline"}
	gateFields := []string{"walk_forward_gate_passed", "final_holdout_gate_passed", "baseline_gate_passed"}
	failed, failedOK := promotion["failed_gates"].([]any)
	var expectedFailed []string
	for index, field := range gateFields {
		passed, ok := promotion[field].(bool)
		if !ok {
			return nil, errors.New("strategy promotion gates are invalid")
		}
		if !passed {
			expectedFailed = append(expectedFailed, gateNames[index])
		}
	}
	if !failedOK || len(failed) != len(expectedFailed) {
		return nil, errors.New("strategy failed gates are invalid")
	}
	for index := range failed {
		if failed[index] != expectedFailed[index] {
			return nil, errors.New("strategy failed gates are invalid")
		}
	}
	if (target == "paper_candidate") != (len(expectedFailed) == 0) || (target != "paper_candidate" && target != "no_promotion") {
		return nil, errors.New("strategy promotion target is inconsistent with its gates")
	}
	delete(result, "result_sha256")
	bodyJSON, err := strategyCanonicalJSON(result)
	if err != nil {
		return nil, err
	}
	resultHash := sha256.Sum256(bodyJSON)
	if hex.EncodeToString(resultHash[:]) != claimedResultSHA {
		return nil, errors.New("strategy result hash mismatch")
	}
	result["result_sha256"] = claimedResultSHA
	canonicalArtifact, err := strategyCanonicalJSON(result)
	if err != nil {
		return nil, err
	}
	artifactHash := sha256.Sum256(canonicalArtifact)
	return &StrategyEvidence{
		ResultSHA256: claimedResultSHA, ArtifactSHA256: hex.EncodeToString(artifactHash[:]),
		StrategyName: stringField(strategy, "name"), StrategyVersion: stringField(strategy, "version"),
		ParameterSHA256: parameterSHA, Target: target, artifactJSON: string(canonicalArtifact), executionPolicy: executionPolicy,
	}, nil
}

func decodeStrategyExecutionContract(value any) (strategyExecutionPolicy, error) {
	execution, ok := value.(map[string]any)
	if !ok || !exactKeys(execution, "starting_cash", "fee", "tax", "slippage_bps", "delay_bars", "max_participation", "signal_price", "fill_price") ||
		stringField(execution, "signal_price") != "bar_close" || stringField(execution, "fill_price") != "next_eligible_bar_open" {
		return strategyExecutionPolicy{}, errors.New("strategy execution contract is invalid")
	}
	startingCash, err := parseDecimal(stringField(execution, "starting_cash"))
	if err != nil || startingCash.Sign() <= 0 {
		return strategyExecutionPolicy{}, errors.New("strategy execution contract is invalid")
	}
	fee, err := parseDecimal(stringField(execution, "fee"))
	if err != nil || fee.Sign() < 0 {
		return strategyExecutionPolicy{}, errors.New("strategy execution contract is invalid")
	}
	tax, err := parseDecimal(stringField(execution, "tax"))
	if err != nil || tax.Sign() < 0 || tax.Cmp(big.NewRat(1, 1)) > 0 {
		return strategyExecutionPolicy{}, errors.New("strategy execution contract is invalid")
	}
	slippage, err := parseDecimal(stringField(execution, "slippage_bps"))
	if err != nil || slippage.Sign() < 0 || slippage.Cmp(big.NewRat(10000, 1)) >= 0 {
		return strategyExecutionPolicy{}, errors.New("strategy execution contract is invalid")
	}
	delay, err := parseDecimal(stringField(execution, "delay_bars"))
	if err != nil || delay.Sign() <= 0 || !delay.IsInt() || !delay.Num().IsInt64() {
		return strategyExecutionPolicy{}, errors.New("strategy execution contract is invalid")
	}
	participation, err := parseDecimal(stringField(execution, "max_participation"))
	if err != nil || participation.Sign() <= 0 || participation.Cmp(big.NewRat(1, 1)) > 0 {
		return strategyExecutionPolicy{}, errors.New("strategy execution contract is invalid")
	}
	canonicalJSON, err := strategyCanonicalJSON(execution)
	if err != nil {
		return strategyExecutionPolicy{}, err
	}
	hash := sha256.Sum256(canonicalJSON)
	return strategyExecutionPolicy{
		StartingCash: stringField(execution, "starting_cash"), Fee: stringField(execution, "fee"), Tax: stringField(execution, "tax"),
		SlippageBPS: stringField(execution, "slippage_bps"), DelayBars: delay.Num().Int64(),
		MaxParticipation: stringField(execution, "max_participation"), SignalPrice: stringField(execution, "signal_price"),
		FillPrice: stringField(execution, "fill_price"), SHA256: hex.EncodeToString(hash[:]), canonicalJSON: string(canonicalJSON),
	}, nil
}

func strategyCanonicalJSON(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(output.Bytes(), []byte{'\n'}), nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("strategy evidence contains trailing JSON")
	}
	return nil
}

func exactKeys(value map[string]any, expected ...string) bool {
	if len(value) != len(expected) {
		return false
	}
	for _, key := range expected {
		if _, ok := value[key]; !ok {
			return false
		}
	}
	return true
}

func mapField(value map[string]any, key string) (map[string]any, bool) {
	result, ok := value[key].(map[string]any)
	return result, ok
}

func stringField(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}
