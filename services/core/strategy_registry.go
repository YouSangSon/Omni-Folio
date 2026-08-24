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
	"os"
	"regexp"
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
}

type StrategySelectionEvent struct {
	EventID                      string `json:"event_id"`
	EventType                    string `json:"event_type"`
	CandidateResultSHA256        string `json:"candidate_result_sha256,omitempty"`
	ExpectedCurrentEventID       string `json:"expected_current_event_id"`
	SourceEventID                string `json:"source_event_id,omitempty"`
	PreviousSelectedResultSHA256 string `json:"previous_selected_result_sha256"`
	SelectedResultSHA256         string `json:"selected_result_sha256"`
	ReasonCode                   string `json:"reason_code"`
	RecordedAt                   string `json:"recorded_at"`
}

type StrategySelectionState struct {
	CurrentEventID       string `json:"current_event_id"`
	SelectedResultSHA256 string `json:"selected_result_sha256"`
}

type strategyRegistryRecoveryProof struct {
	SHA256               string
	Evidence             int
	Events               int
	SelectedResultSHA256 string
	CurrentEventID       string
	stack                []string
}

func sameStrategyRegistryProof(left, right strategyRegistryRecoveryProof) bool {
	return left.SHA256 == right.SHA256 && left.Evidence == right.Evidence && left.Events == right.Events &&
		left.SelectedResultSHA256 == right.SelectedResultSHA256 && left.CurrentEventID == right.CurrentEventID
}

func readStrategyArtifact(path string) ([]byte, error) {
	file, err := os.Open(path)
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
		SelectedResultSHA256: resultSHA256, ReasonCode: "manual_selection", RecordedAt: s.now().UTC().Format(time.RFC3339Nano),
	}
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
	state, err := replayStrategyRegistry(ctx, tx)
	if err != nil {
		return nil, err
	}
	if state.CurrentEventID != expectedCurrentEventID || sourceEventID != state.CurrentEventID {
		return nil, errors.New("stale or mismatched strategy rollback source")
	}
	if len(state.stack) == 0 {
		return nil, errors.New("no selected paper candidate to roll back")
	}
	nextStack := append([]string(nil), state.stack[:len(state.stack)-1]...)
	selected := noStrategySelection
	if len(nextStack) != 0 {
		selected = nextStack[len(nextStack)-1]
	}
	event := StrategySelectionEvent{
		EventID: s.id("strategy_selection"), EventType: "ROLLBACK", ExpectedCurrentEventID: expectedCurrentEventID,
		SourceEventID: sourceEventID, PreviousSelectedResultSHA256: state.SelectedResultSHA256,
		SelectedResultSHA256: selected, ReasonCode: "manual_rollback", RecordedAt: s.now().UTC().Format(time.RFC3339Nano),
	}
	if err := insertStrategySelectionEvent(ctx, tx, event); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &StrategySelectionState{CurrentEventID: event.EventID, SelectedResultSHA256: selected}, nil
}

func insertStrategySelectionEvent(ctx context.Context, tx *sql.Tx, event StrategySelectionEvent) error {
	recordJSON, recordSHA, err := orderJSONHash(event)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO strategy_selection_events(
		event_id,event_type,candidate_result_sha256,expected_current_event_id,source_event_id,
		previous_selected_result_sha256,selected_result_sha256,reason_code,record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, event.EventID, event.EventType, nullable(event.CandidateResultSHA256),
		event.ExpectedCurrentEventID, nullable(event.SourceEventID), event.PreviousSelectedResultSHA256,
		event.SelectedResultSHA256, event.ReasonCode, recordSHA, string(recordJSON), event.RecordedAt)
	return err
}

// ponytail: replay is linear for one personal research registry; add a projection only after measured history makes it hot.
func proveStrategyRegistryRecovery(ctx context.Context, q orderQuerier) (strategyRegistryRecoveryProof, error) {
	return replayStrategyRegistry(ctx, q)
}

func replayStrategyRegistry(ctx context.Context, q orderQuerier) (strategyRegistryRecoveryProof, error) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
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
	rows, err = q.QueryContext(ctx, `SELECT sequence,event_id,event_type,candidate_result_sha256,expected_current_event_id,
		source_event_id,previous_selected_result_sha256,selected_result_sha256,reason_code,record_sha256,record_json,recorded_at
		FROM strategy_selection_events ORDER BY sequence`)
	if err != nil {
		return strategyRegistryRecoveryProof{}, err
	}
	for rows.Next() {
		var sequence int64
		var eventID, eventType, expectedCurrent, previousSelected, nextSelected, reason, recordSHA, recordJSON, recordedAt string
		var candidateSHA, sourceEvent sql.NullString
		if err := rows.Scan(&sequence, &eventID, &eventType, &candidateSHA, &expectedCurrent, &sourceEvent,
			&previousSelected, &nextSelected, &reason, &recordSHA, &recordJSON, &recordedAt); err != nil {
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
			event.SelectedResultSHA256 != nextSelected || event.ReasonCode != reason || event.RecordedAt != recordedAt ||
			!safeOrderID(eventID) || !canonicalUTCString(recordedAt) || expectedCurrent != currentEvent || previousSelected != selected {
			rows.Close()
			return strategyRegistryRecoveryProof{}, fmt.Errorf("strategy selection event %q metadata or hash mismatch", eventID)
		}
		switch eventType {
		case "SELECT":
			if evidenceTargets[candidateSHA.String] != "paper_candidate" || sourceEvent.Valid || reason != "manual_selection" || nextSelected != candidateSHA.String || nextSelected == selected {
				rows.Close()
				return strategyRegistryRecoveryProof{}, fmt.Errorf("strategy selection event %q is invalid", eventID)
			}
			stack = append(stack, nextSelected)
		case "ROLLBACK":
			if candidateSHA.Valid || !sourceEvent.Valid || sourceEvent.String != currentEvent || !knownEvents[sourceEvent.String] || reason != "manual_rollback" || len(stack) == 0 {
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
		default:
			rows.Close()
			return strategyRegistryRecoveryProof{}, fmt.Errorf("strategy selection event %q has unsupported type", eventID)
		}
		if err := encoder.Encode([]any{"strategy_selection_events", sequence, eventID, eventType, candidateSHA, expectedCurrent,
			sourceEvent, previousSelected, nextSelected, reason, recordSHA, recordJSON, recordedAt}); err != nil {
			rows.Close()
			return strategyRegistryRecoveryProof{}, err
		}
		knownEvents[eventID] = true
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
	return strategyRegistryRecoveryProof{
		SHA256: hex.EncodeToString(hash.Sum(nil)), Evidence: evidenceCount, Events: eventCount,
		SelectedResultSHA256: selected, CurrentEventID: currentEvent, stack: stack,
	}, nil
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
		ParameterSHA256: parameterSHA, Target: target, artifactJSON: string(canonicalArtifact),
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
