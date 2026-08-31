package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"omni-folio/services/core/internal/riskdomain"
)

const (
	paperPerformancePolicySchema        = "paper-strategy-performance-policy.v1"
	noPaperPerformancePolicyPredecessor = "no_policy_event"
)

type PaperPerformancePolicyEvent struct {
	PolicyEventID                          string `json:"policy_event_id"`
	SchemaVersion                          string `json:"schema_version"`
	PolicyVersion                          string `json:"policy_version"`
	AccountRef                             string `json:"account_ref"`
	PaperAccountingSessionID               string `json:"paper_accounting_session_id"`
	StrategySelectionEventID               string `json:"strategy_selection_event_id"`
	SelectedStrategyResultRef              string `json:"selected_strategy_result_ref"`
	StrategyPerformanceID                  string `json:"strategy_performance_id"`
	BaselinePerformanceID                  string `json:"baseline_performance_id"`
	LatestPerformanceID                    string `json:"latest_performance_id"`
	SampleCount                            int    `json:"sample_count"`
	ExpectedPreviousPolicyEventID          string `json:"expected_previous_policy_event_id"`
	StrategySelectionSequenceCutoff        int64  `json:"strategy_selection_sequence_cutoff"`
	PaperStrategyPerformanceSequenceCutoff int64  `json:"paper_strategy_performance_sequence_cutoff"`
	ExecutionAuthoritySequenceCutoff       int64  `json:"execution_authority_sequence_cutoff"`
	Decision                               string `json:"decision"`
	ReasonCode                             string `json:"reason_code"`
	RollbackSelectionEventID               string `json:"rollback_selection_event_id,omitempty"`
	AutomaticHaltCount                     int    `json:"automatic_halt_count"`
	RecordedAt                             string `json:"recorded_at"`
}

type paperPerformancePolicyRecoveryProof struct {
	SHA256         string
	Events         int
	Actions        int
	AutomaticHalts int
}

func (s *Service) applyPaperPerformancePolicy(ctx context.Context, accountRef, expectedSelectionEventID, expectedStrategyPerformanceID string) (*PaperPerformancePolicyEvent, error) {
	if s == nil || s.db == nil || s.now == nil || !orderAlias(accountRef, "account") ||
		!safeOrderID(expectedSelectionEventID) || !safeOrderID(expectedStrategyPerformanceID) {
		return nil, errors.New("paper performance policy identifiers are invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := provePaperPerformancePolicyRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("paper performance policy recovery: %w", err)
	}
	if existing, found, err := loadPaperPerformancePolicyByKey(ctx, tx, accountRef, expectedSelectionEventID, expectedStrategyPerformanceID); err != nil {
		return nil, err
	} else if found {
		return existing, nil
	}
	strategy, err := replayStrategyRegistry(ctx, tx)
	if err != nil {
		return nil, err
	}
	if strategy.CurrentEventID != expectedSelectionEventID || strategy.SelectedResultSHA256 == noStrategySelection {
		return nil, errors.New("paper performance policy selection is stale or no_strategy")
	}
	performance, _, err := loadPaperStrategyPerformanceByID(ctx, tx, expectedStrategyPerformanceID)
	if err != nil {
		return nil, err
	}
	var latestID string
	if err := tx.QueryRowContext(ctx, `SELECT strategy_performance_id FROM paper_strategy_performance_events WHERE account_ref=? ORDER BY sequence DESC LIMIT 1`, accountRef).Scan(&latestID); err != nil {
		return nil, err
	}
	if latestID != expectedStrategyPerformanceID || performance.AccountRef != accountRef ||
		performance.StrategySelectionEventID != strategy.CurrentEventID || performance.SelectedStrategyResultRef != strategy.SelectedResultSHA256 {
		return nil, errors.New("paper performance policy strategy evidence is stale")
	}
	decision, err := riskdomain.EvaluatePaperPerformancePolicy(riskdomain.PaperPerformanceInput{
		SampleCount: performance.SampleCount, CumulativeReturn: performance.CumulativeReturn, MaxDrawdown: performance.MaxDrawdown,
	})
	if err != nil {
		return nil, fmt.Errorf("paper performance policy evidence: %w", err)
	}
	previous, err := latestPaperPerformancePolicyID(ctx, tx, accountRef, strategy.CurrentEventID)
	if err != nil {
		return nil, err
	}
	selectionCutoff, err := maxSequence(ctx, tx, "strategy_selection_events")
	if err != nil {
		return nil, err
	}
	authorityCutoff, err := maxSequence(ctx, tx, "execution_authority_events")
	if err != nil {
		return nil, err
	}
	performanceCutoff, err := maxSequence(ctx, tx, "paper_strategy_performance_events")
	if err != nil {
		return nil, err
	}
	armed, err := armedExecutionAccounts(ctx, tx)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	event := PaperPerformancePolicyEvent{
		SchemaVersion: paperPerformancePolicySchema, PolicyVersion: riskdomain.PaperPerformancePolicyVersion,
		AccountRef: accountRef, PaperAccountingSessionID: performance.PaperAccountingSessionID,
		StrategySelectionEventID: performance.StrategySelectionEventID, SelectedStrategyResultRef: performance.SelectedStrategyResultRef,
		StrategyPerformanceID: performance.StrategyPerformanceID, BaselinePerformanceID: performance.BaselinePerformanceID,
		LatestPerformanceID: performance.LatestPerformanceID, SampleCount: performance.SampleCount,
		ExpectedPreviousPolicyEventID: previous, StrategySelectionSequenceCutoff: selectionCutoff,
		PaperStrategyPerformanceSequenceCutoff: performanceCutoff, ExecutionAuthoritySequenceCutoff: authorityCutoff,
		Decision: decision.Decision, ReasonCode: decision.ReasonCode, RecordedAt: now.Format(canonicalPaperTimeLayout),
	}
	event.PolicyEventID = paperPerformancePolicyID(event)
	automaticHaltIDs := map[string]string(nil)
	if decision.Decision == "HALT_AND_ROLLBACK" {
		event.RollbackSelectionEventID = s.id("strategy_selection")
		event.AutomaticHaltCount = len(armed)
		automaticHaltIDs = make(map[string]string, len(armed))
		for _, armedAccountRef := range armed {
			automaticHaltIDs[armedAccountRef] = paperPerformanceAutomaticHaltID(event.PolicyEventID, armedAccountRef)
		}
	}
	if err := insertPaperPerformancePolicyEvent(ctx, tx, event); err != nil {
		return nil, err
	}
	if decision.Decision == "HALT_AND_ROLLBACK" {
		if _, err := s.haltSyntheticExecutionTx(ctx, tx, now, "automatic_performance_halt", event.PolicyEventID, armed, automaticHaltIDs); err != nil {
			return nil, err
		}
		if _, err := s.rollbackPaperCandidateTx(ctx, tx, strategy, expectedSelectionEventID, expectedSelectionEventID,
			"automatic_performance_rollback", event.PolicyEventID, event.RollbackSelectionEventID, now); err != nil {
			return nil, err
		}
	}
	if _, err := provePaperPerformancePolicyRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("proposed paper performance policy recovery: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &event, nil
}

func paperPerformancePolicyID(event PaperPerformancePolicyEvent) string {
	hash := sha256.Sum256([]byte(strings.Join([]string{
		event.PolicyVersion, event.AccountRef, event.StrategySelectionEventID, event.StrategyPerformanceID,
		strconv.FormatInt(event.StrategySelectionSequenceCutoff, 10),
		strconv.FormatInt(event.PaperStrategyPerformanceSequenceCutoff, 10),
		strconv.FormatInt(event.ExecutionAuthoritySequenceCutoff, 10),
	}, "\x00")))
	return "paper_performance_policy_" + hex.EncodeToString(hash[:16])
}

func paperPerformanceAutomaticHaltID(policyEventID, accountRef string) string {
	hash := sha256.Sum256([]byte(strings.Join([]string{policyEventID, accountRef}, "\x00")))
	return "execution_authority_" + hex.EncodeToString(hash[:16])
}

func insertPaperPerformancePolicyEvent(ctx context.Context, tx *sql.Tx, event PaperPerformancePolicyEvent) error {
	if err := validatePaperPerformancePolicyEvent(event); err != nil {
		return fmt.Errorf("paper performance policy event %q: %w", event.PolicyEventID, err)
	}
	recordJSON, recordSHA, err := orderJSONHash(event)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO paper_performance_policy_events(
		policy_event_id,schema_version,policy_version,account_ref,paper_accounting_session_id,strategy_selection_event_id,
		selected_strategy_result_ref,strategy_performance_id,baseline_performance_id,latest_performance_id,sample_count,
		expected_previous_policy_event_id,strategy_selection_sequence_cutoff,paper_strategy_performance_sequence_cutoff,
		execution_authority_sequence_cutoff,decision,reason_code,rollback_selection_event_id,automatic_halt_count,
		record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, event.PolicyEventID, event.SchemaVersion, event.PolicyVersion,
		event.AccountRef, event.PaperAccountingSessionID, event.StrategySelectionEventID, event.SelectedStrategyResultRef,
		event.StrategyPerformanceID, event.BaselinePerformanceID, event.LatestPerformanceID, event.SampleCount,
		event.ExpectedPreviousPolicyEventID, event.StrategySelectionSequenceCutoff, event.PaperStrategyPerformanceSequenceCutoff,
		event.ExecutionAuthoritySequenceCutoff, event.Decision, event.ReasonCode, nullable(event.RollbackSelectionEventID),
		event.AutomaticHaltCount, recordSHA, string(recordJSON), event.RecordedAt)
	return err
}

func validatePaperPerformancePolicyEvent(event PaperPerformancePolicyEvent) error {
	if !safeOrderID(event.PolicyEventID) || event.SchemaVersion != paperPerformancePolicySchema ||
		event.PolicyVersion != riskdomain.PaperPerformancePolicyVersion || !orderAlias(event.AccountRef, "account") ||
		!safeOrderID(event.PaperAccountingSessionID) || !safeOrderID(event.StrategySelectionEventID) ||
		!strategySHA256Pattern.MatchString(event.SelectedStrategyResultRef) || !safeOrderID(event.StrategyPerformanceID) ||
		!safeOrderID(event.BaselinePerformanceID) || !safeOrderID(event.LatestPerformanceID) || event.SampleCount <= 0 ||
		!safeOrderID(event.ExpectedPreviousPolicyEventID) || event.StrategySelectionSequenceCutoff < 0 ||
		event.PaperStrategyPerformanceSequenceCutoff <= 0 || event.ExecutionAuthoritySequenceCutoff < 0 ||
		!canonicalPaperTimes(event.RecordedAt) || event.PolicyEventID != paperPerformancePolicyID(event) {
		return errors.New("paper performance policy event is invalid")
	}
	switch event.Decision {
	case "INSUFFICIENT":
		if event.ReasonCode != "minimum_same_selection_samples_not_met" || event.RollbackSelectionEventID != "" || event.AutomaticHaltCount != 0 {
			return errors.New("paper performance insufficient policy event is invalid")
		}
	case "HOLD":
		if event.ReasonCode != "within_local_paper_safety_bounds" || event.RollbackSelectionEventID != "" || event.AutomaticHaltCount != 0 {
			return errors.New("paper performance hold policy event is invalid")
		}
	case "HALT_AND_ROLLBACK":
		if (event.ReasonCode != "max_drawdown_limit_reached" && event.ReasonCode != "cumulative_return_floor_reached") ||
			!safeOrderID(event.RollbackSelectionEventID) || event.AutomaticHaltCount < 0 {
			return errors.New("paper performance action policy event is invalid")
		}
	default:
		return errors.New("paper performance policy decision is invalid")
	}
	return nil
}

func loadPaperStrategyPerformanceByID(ctx context.Context, q orderQuerier, id string) (PaperStrategyPerformanceEvent, int64, error) {
	row := q.QueryRowContext(ctx, `SELECT sequence,strategy_performance_id,schema_version,policy_version,account_ref,
		paper_accounting_session_id,strategy_selection_event_id,selected_strategy_result_ref,
		expected_previous_strategy_performance_id,baseline_performance_id,latest_performance_id,baseline_as_of,
		latest_as_of,sample_count,baseline_equity,latest_equity,peak_equity,period_return_state,period_return,
		cumulative_return,drawdown,max_drawdown,record_sha256,record_json,recorded_at
		FROM paper_strategy_performance_events WHERE strategy_performance_id=?`, id)
	var event PaperStrategyPerformanceEvent
	var sequence int64
	var recordSHA, recordJSON string
	if err := scanPaperStrategyPerformance(row.Scan, &event, &sequence, &recordSHA, &recordJSON); err != nil {
		return PaperStrategyPerformanceEvent{}, 0, err
	}
	if err := validateStoredPaperStrategyPerformanceEvent(event, recordSHA, recordJSON); err != nil {
		return PaperStrategyPerformanceEvent{}, 0, err
	}
	return event, sequence, nil
}

func latestPaperPerformancePolicyID(ctx context.Context, q orderQuerier, accountRef, selectionEventID string) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT policy_event_id FROM paper_performance_policy_events
		WHERE account_ref=? AND strategy_selection_event_id=? ORDER BY sequence DESC LIMIT 1`, accountRef, selectionEventID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return noPaperPerformancePolicyPredecessor, nil
	}
	return id, err
}

func loadPaperPerformancePolicyByKey(ctx context.Context, q orderQuerier, accountRef, selectionEventID, strategyPerformanceID string) (*PaperPerformancePolicyEvent, bool, error) {
	row := q.QueryRowContext(ctx, `SELECT sequence,policy_event_id,schema_version,policy_version,account_ref,paper_accounting_session_id,
		strategy_selection_event_id,selected_strategy_result_ref,strategy_performance_id,baseline_performance_id,latest_performance_id,
		sample_count,expected_previous_policy_event_id,strategy_selection_sequence_cutoff,paper_strategy_performance_sequence_cutoff,
		execution_authority_sequence_cutoff,decision,reason_code,rollback_selection_event_id,automatic_halt_count,record_sha256,record_json,recorded_at
		FROM paper_performance_policy_events WHERE policy_version=? AND account_ref=? AND strategy_selection_event_id=? AND strategy_performance_id=?`,
		riskdomain.PaperPerformancePolicyVersion, accountRef, selectionEventID, strategyPerformanceID)
	event, err := scanPaperPerformancePolicyEvent(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := validateStoredPaperPerformancePolicyEvent(event.event, event.recordSHA, event.recordJSON); err != nil {
		return nil, false, err
	}
	return &event.event, true, nil
}

type storedPaperPerformancePolicyEvent struct {
	sequence              int64
	event                 PaperPerformancePolicyEvent
	recordSHA, recordJSON string
}

func scanPaperPerformancePolicyEvent(scan func(...any) error) (storedPaperPerformancePolicyEvent, error) {
	var item storedPaperPerformancePolicyEvent
	var rollback sql.NullString
	err := scan(&item.sequence, &item.event.PolicyEventID, &item.event.SchemaVersion, &item.event.PolicyVersion, &item.event.AccountRef,
		&item.event.PaperAccountingSessionID, &item.event.StrategySelectionEventID, &item.event.SelectedStrategyResultRef, &item.event.StrategyPerformanceID,
		&item.event.BaselinePerformanceID, &item.event.LatestPerformanceID, &item.event.SampleCount, &item.event.ExpectedPreviousPolicyEventID,
		&item.event.StrategySelectionSequenceCutoff, &item.event.PaperStrategyPerformanceSequenceCutoff, &item.event.ExecutionAuthoritySequenceCutoff,
		&item.event.Decision, &item.event.ReasonCode, &rollback, &item.event.AutomaticHaltCount, &item.recordSHA, &item.recordJSON, &item.event.RecordedAt)
	item.event.RollbackSelectionEventID = rollback.String
	return item, err
}

func loadPaperPerformancePolicyByID(ctx context.Context, q orderQuerier, policyID string) (PaperPerformancePolicyEvent, error) {
	row := q.QueryRowContext(ctx, `SELECT sequence,policy_event_id,schema_version,policy_version,account_ref,paper_accounting_session_id,
		strategy_selection_event_id,selected_strategy_result_ref,strategy_performance_id,baseline_performance_id,latest_performance_id,
		sample_count,expected_previous_policy_event_id,strategy_selection_sequence_cutoff,paper_strategy_performance_sequence_cutoff,
		execution_authority_sequence_cutoff,decision,reason_code,rollback_selection_event_id,automatic_halt_count,record_sha256,record_json,recorded_at
		FROM paper_performance_policy_events WHERE policy_event_id=?`, policyID)
	item, err := scanPaperPerformancePolicyEvent(row.Scan)
	if err != nil {
		return PaperPerformancePolicyEvent{}, err
	}
	if err := validateStoredPaperPerformancePolicyEvent(item.event, item.recordSHA, item.recordJSON); err != nil {
		return PaperPerformancePolicyEvent{}, err
	}
	return item.event, nil
}

func validateStoredPaperPerformancePolicyEvent(event PaperPerformancePolicyEvent, recordSHA, recordJSON string) error {
	if err := validatePaperPerformancePolicyEvent(event); err != nil {
		return err
	}
	var decoded PaperPerformancePolicyEvent
	if err := json.Unmarshal([]byte(recordJSON), &decoded); err != nil {
		return err
	}
	canonical, actualSHA, err := orderJSONHash(decoded)
	if err != nil || string(canonical) != recordJSON || actualSHA != recordSHA || decoded != event {
		return errors.New("paper performance policy metadata or hash mismatch")
	}
	return nil
}

// ponytail: the root replay is intentionally linear over local append-only journals; add no projection until measured history warrants it.
func provePaperPerformancePolicyRecovery(ctx context.Context, q orderQuerier) (paperPerformancePolicyRecoveryProof, error) {
	var schemaVersion int
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&schemaVersion); err != nil {
		return paperPerformancePolicyRecoveryProof{}, err
	}
	if schemaVersion < 20 {
		return paperPerformancePolicyRecoveryProof{SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}, nil
	}
	if err := provePaperPerformancePolicyPrerequisites(ctx, q); err != nil {
		return paperPerformancePolicyRecoveryProof{}, err
	}
	return replayPaperPerformancePolicyRecovery(ctx, q)
}

func proveLinkedPaperPerformancePolicyMetadata(ctx context.Context, q orderQuerier, policyID, actionKind, actionID, accountRef string) error {
	if policyID == "" || !safeOrderID(policyID) || !safeOrderID(actionID) {
		return errors.New("automatic action policy link is invalid")
	}
	event, err := loadPaperPerformancePolicyByID(ctx, q, policyID)
	if err != nil {
		return fmt.Errorf("linked paper performance policy: %w", err)
	}
	if event.Decision != "HALT_AND_ROLLBACK" {
		return errors.New("automatic action does not name a halt policy")
	}
	switch actionKind {
	case "halt":
		if event.AutomaticHaltCount <= 0 || !orderAlias(accountRef, "account") ||
			actionID != paperPerformanceAutomaticHaltID(event.PolicyEventID, accountRef) {
			return errors.New("automatic halt policy count is invalid")
		}
	case "rollback":
		if event.RollbackSelectionEventID != actionID {
			return errors.New("automatic rollback policy link is invalid")
		}
	default:
		return errors.New("automatic action kind is invalid")
	}
	return nil
}

func replayPaperPerformancePolicyRecovery(ctx context.Context, q orderQuerier) (paperPerformancePolicyRecoveryProof, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence,policy_event_id,schema_version,policy_version,account_ref,paper_accounting_session_id,
		strategy_selection_event_id,selected_strategy_result_ref,strategy_performance_id,baseline_performance_id,latest_performance_id,
		sample_count,expected_previous_policy_event_id,strategy_selection_sequence_cutoff,paper_strategy_performance_sequence_cutoff,
		execution_authority_sequence_cutoff,decision,reason_code,rollback_selection_event_id,automatic_halt_count,record_sha256,record_json,recorded_at
		FROM paper_performance_policy_events ORDER BY sequence`)
	if err != nil {
		return paperPerformancePolicyRecoveryProof{}, err
	}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	previous := map[string]string{}
	policies := map[string]PaperPerformancePolicyEvent{}
	var ordered []PaperPerformancePolicyEvent
	var stored []storedPaperPerformancePolicyEvent
	for rows.Next() {
		item, err := scanPaperPerformancePolicyEvent(rows.Scan)
		if err != nil {
			rows.Close()
			return paperPerformancePolicyRecoveryProof{}, err
		}
		stored = append(stored, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return paperPerformancePolicyRecoveryProof{}, err
	}
	if err := rows.Close(); err != nil {
		return paperPerformancePolicyRecoveryProof{}, err
	}
	for index, item := range stored {
		event := item.event
		key := event.AccountRef + "\x00" + event.StrategySelectionEventID
		expectedPrevious := noPaperPerformancePolicyPredecessor
		if previous[key] != "" {
			expectedPrevious = previous[key]
		}
		if item.sequence != int64(index+1) || event.ExpectedPreviousPolicyEventID != expectedPrevious {
			return paperPerformancePolicyRecoveryProof{}, errors.New("paper performance policy sequence, predecessor, or metadata is invalid")
		}
		if err := validateStoredPaperPerformancePolicyEvent(event, item.recordSHA, item.recordJSON); err != nil {
			return paperPerformancePolicyRecoveryProof{}, errors.New("paper performance policy sequence, predecessor, or metadata is invalid")
		}
		if err := validatePaperPerformancePolicyEvidence(ctx, q, event); err != nil {
			return paperPerformancePolicyRecoveryProof{}, err
		}
		previous[key] = event.PolicyEventID
		policies[event.PolicyEventID] = event
		ordered = append(ordered, event)
	}
	actions, halts := 0, 0
	claimedHalts := map[string]bool{}
	claimedRollbacks := map[string]bool{}
	for _, event := range ordered {
		haltIDs, rollbackID, err := validatePaperPerformancePolicyActions(ctx, q, event)
		if err != nil {
			return paperPerformancePolicyRecoveryProof{}, err
		}
		if event.Decision == "HALT_AND_ROLLBACK" {
			actions++
			halts += len(haltIDs)
			for _, id := range haltIDs {
				if claimedHalts[id] {
					return paperPerformancePolicyRecoveryProof{}, errors.New("automatic execution halt is linked more than once")
				}
				claimedHalts[id] = true
			}
			if claimedRollbacks[rollbackID] {
				return paperPerformancePolicyRecoveryProof{}, errors.New("automatic strategy rollback is linked more than once")
			}
			claimedRollbacks[rollbackID] = true
		}
		if err := encoder.Encode([]any{"paper_performance_policy_events", event, haltIDs, rollbackID}); err != nil {
			return paperPerformancePolicyRecoveryProof{}, err
		}
	}
	if err := validatePaperPerformancePolicyReverseCoverage(ctx, q, policies, claimedHalts, claimedRollbacks); err != nil {
		return paperPerformancePolicyRecoveryProof{}, err
	}
	return paperPerformancePolicyRecoveryProof{SHA256: hex.EncodeToString(hash.Sum(nil)), Events: len(ordered), Actions: actions, AutomaticHalts: halts}, nil
}

func validatePaperPerformancePolicyEvidence(ctx context.Context, q orderQuerier, event PaperPerformancePolicyEvent) error {
	performance, sequence, err := loadPaperStrategyPerformanceByID(ctx, q, event.StrategyPerformanceID)
	if err != nil || sequence > event.PaperStrategyPerformanceSequenceCutoff || performance.AccountRef != event.AccountRef ||
		performance.PaperAccountingSessionID != event.PaperAccountingSessionID || performance.StrategySelectionEventID != event.StrategySelectionEventID ||
		performance.SelectedStrategyResultRef != event.SelectedStrategyResultRef || performance.BaselinePerformanceID != event.BaselinePerformanceID ||
		performance.LatestPerformanceID != event.LatestPerformanceID || performance.SampleCount != event.SampleCount {
		return errors.New("paper performance policy strategy evidence is invalid")
	}
	var latestAtCutoff string
	if err := q.QueryRowContext(ctx, `SELECT strategy_performance_id FROM paper_strategy_performance_events
		WHERE account_ref=? AND sequence<=? ORDER BY sequence DESC LIMIT 1`, event.AccountRef, event.PaperStrategyPerformanceSequenceCutoff).
		Scan(&latestAtCutoff); err != nil || latestAtCutoff != event.StrategyPerformanceID {
		return errors.New("paper performance policy strategy evidence is stale")
	}
	var selectionSequence int64
	var eventType, selected string
	if err := q.QueryRowContext(ctx, `SELECT sequence,event_type,selected_result_sha256 FROM strategy_selection_events WHERE event_id=?`, event.StrategySelectionEventID).
		Scan(&selectionSequence, &eventType, &selected); err != nil || selectionSequence != event.StrategySelectionSequenceCutoff ||
		eventType != "SELECT" || selected != event.SelectedStrategyResultRef {
		return errors.New("paper performance policy selection cutoff is invalid")
	}
	decision, err := riskdomain.EvaluatePaperPerformancePolicy(riskdomain.PaperPerformanceInput{
		SampleCount: performance.SampleCount, CumulativeReturn: performance.CumulativeReturn, MaxDrawdown: performance.MaxDrawdown,
	})
	if err != nil || decision.Decision != event.Decision || decision.ReasonCode != event.ReasonCode {
		return errors.New("paper performance policy decision is invalid")
	}
	return nil
}

type paperPerformancePolicyAuthorityRow struct {
	sequence int64
	record   executionAuthorityRecord
}

func scanPaperPerformancePolicyAuthorityRow(scan func(...any) error) (paperPerformancePolicyAuthorityRow, error) {
	var row paperPerformancePolicyAuthorityRow
	var armed int
	var owner, expires, policyID sql.NullString
	var recordSHA, recordJSON string
	err := scan(&row.sequence, &row.record.AccountRef, &row.record.EventID, &armed, &owner, &row.record.FencingToken,
		&expires, &row.record.ReasonCode, &policyID, &recordSHA, &recordJSON, &row.record.RecordedAt)
	if err != nil {
		return paperPerformancePolicyAuthorityRow{}, err
	}
	if err := json.Unmarshal([]byte(recordJSON), &row.record); err != nil {
		return paperPerformancePolicyAuthorityRow{}, err
	}
	canonical, actualSHA, err := orderJSONHash(row.record)
	if err != nil || string(canonical) != recordJSON || actualSHA != recordSHA || boolInt(row.record.Armed) != armed ||
		row.record.LeaseOwner != owner.String || (row.record.LeaseOwner != "") != owner.Valid || row.record.LeaseExpiresAt != expires.String ||
		(row.record.LeaseExpiresAt != "") != expires.Valid || row.record.PaperPerformancePolicyEventID != policyID.String ||
		(row.record.PaperPerformancePolicyEventID != "") != policyID.Valid {
		return paperPerformancePolicyAuthorityRow{}, errors.New("policy-linked authority row metadata is invalid")
	}
	return row, nil
}

func authorityStatesAtPolicyCutoff(ctx context.Context, q orderQuerier, cutoff int64) (map[string]executionAuthorityRecord, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence,account_ref,event_id,armed,lease_owner,fencing_token,lease_expires_at,reason_code,paper_performance_policy_event_id,record_sha256,record_json,recorded_at
		FROM execution_authority_events WHERE sequence<=? ORDER BY sequence`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	previous := map[string]*executionAuthorityRecord{}
	states := map[string]executionAuthorityRecord{}
	for rows.Next() {
		row, err := scanPaperPerformancePolicyAuthorityRow(rows.Scan)
		if err != nil || validateExecutionAuthorityRecord(row.record, previous[row.record.AccountRef]) != nil {
			return nil, errors.New("authority history at paper performance policy cutoff is invalid")
		}
		copy := row.record
		previous[row.record.AccountRef] = &copy
		states[row.record.AccountRef] = row.record
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return states, nil
}

func validatePaperPerformancePolicyActions(ctx context.Context, q orderQuerier, event PaperPerformancePolicyEvent) ([]string, string, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence,account_ref,event_id,armed,lease_owner,fencing_token,lease_expires_at,reason_code,paper_performance_policy_event_id,record_sha256,record_json,recorded_at
		FROM execution_authority_events WHERE paper_performance_policy_event_id=? ORDER BY sequence`, event.PolicyEventID)
	if err != nil {
		return nil, "", err
	}
	var actionRows []paperPerformancePolicyAuthorityRow
	for rows.Next() {
		row, err := scanPaperPerformancePolicyAuthorityRow(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, "", err
		}
		actionRows = append(actionRows, row)
	}
	if err := rows.Close(); err != nil {
		return nil, "", err
	}
	if event.Decision != "HALT_AND_ROLLBACK" {
		if len(actionRows) != 0 || event.RollbackSelectionEventID != "" {
			return nil, "", errors.New("no-action policy has automatic rows")
		}
		return nil, "", nil
	}
	policyRecordedAt, policyTimeOK := parsePaperTime(event.RecordedAt)
	if !policyTimeOK {
		return nil, "", errors.New("paper performance policy recorded time is invalid")
	}
	states, err := authorityStatesAtPolicyCutoff(ctx, q, event.ExecutionAuthoritySequenceCutoff)
	if err != nil {
		return nil, "", err
	}
	var expectedAccounts []string
	for account, state := range states {
		if state.Armed {
			expectedAccounts = append(expectedAccounts, account)
		}
	}
	sort.Strings(expectedAccounts)
	if event.AutomaticHaltCount != len(expectedAccounts) || len(actionRows) != len(expectedAccounts) {
		return nil, "", errors.New("automatic halt coverage count is invalid")
	}
	haltIDs := make([]string, 0, len(actionRows))
	for index, row := range actionRows {
		previous := states[row.record.AccountRef]
		actionRecordedAt, actionTimeOK := canonicalUTCTime(row.record.RecordedAt)
		if row.sequence != event.ExecutionAuthoritySequenceCutoff+int64(index+1) || row.record.AccountRef != expectedAccounts[index] ||
			row.record.ReasonCode != "automatic_performance_halt" || row.record.PaperPerformancePolicyEventID != event.PolicyEventID ||
			row.record.Armed || row.record.LeaseOwner != "" || row.record.LeaseExpiresAt != "" || row.record.FencingToken != previous.FencingToken+1 {
			return nil, "", errors.New("automatic halt coverage or order is invalid")
		}
		if row.record.EventID != paperPerformanceAutomaticHaltID(event.PolicyEventID, row.record.AccountRef) ||
			!actionTimeOK || !actionRecordedAt.Equal(policyRecordedAt) {
			return nil, "", errors.New("automatic halt provenance is invalid")
		}
		haltIDs = append(haltIDs, row.record.EventID)
	}
	var sequence int64
	var eventType, sourceID, previousResult, selectedResult, reason, policyID, rollbackRecordedAt string
	err = q.QueryRowContext(ctx, `SELECT sequence,event_type,source_event_id,previous_selected_result_sha256,selected_result_sha256,reason_code,paper_performance_policy_event_id,recorded_at
		FROM strategy_selection_events WHERE event_id=?`, event.RollbackSelectionEventID).
		Scan(&sequence, &eventType, &sourceID, &previousResult, &selectedResult, &reason, &policyID, &rollbackRecordedAt)
	if err != nil || sequence != event.StrategySelectionSequenceCutoff+1 || eventType != "ROLLBACK" || sourceID != event.StrategySelectionEventID ||
		previousResult != event.SelectedStrategyResultRef || selectedResult == event.SelectedStrategyResultRef || reason != "automatic_performance_rollback" || policyID != event.PolicyEventID {
		return nil, "", errors.New("automatic strategy rollback coverage is invalid")
	}
	rollbackTime, rollbackTimeOK := canonicalUTCTime(rollbackRecordedAt)
	if !rollbackTimeOK || !rollbackTime.Equal(policyRecordedAt) {
		return nil, "", errors.New("automatic strategy rollback provenance is invalid")
	}
	var sourcePrevious string
	if err := q.QueryRowContext(ctx, `SELECT previous_selected_result_sha256 FROM strategy_selection_events WHERE event_id=?`, event.StrategySelectionEventID).Scan(&sourcePrevious); err != nil || selectedResult != sourcePrevious {
		return nil, "", errors.New("automatic strategy rollback did not pop one selection")
	}
	return haltIDs, event.RollbackSelectionEventID, nil
}

func validatePaperPerformancePolicyReverseCoverage(ctx context.Context, q orderQuerier, policies map[string]PaperPerformancePolicyEvent, claimedHalts, claimedRollbacks map[string]bool) error {
	rows, err := q.QueryContext(ctx, `SELECT event_id,paper_performance_policy_event_id FROM execution_authority_events WHERE reason_code='automatic_performance_halt' ORDER BY sequence`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var eventID, policyID string
		if err := rows.Scan(&eventID, &policyID); err != nil || policies[policyID].Decision != "HALT_AND_ROLLBACK" || !claimedHalts[eventID] {
			rows.Close()
			return errors.New("automatic execution halt reverse coverage is invalid")
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	rows, err = q.QueryContext(ctx, `SELECT event_id,paper_performance_policy_event_id FROM strategy_selection_events WHERE reason_code='automatic_performance_rollback' ORDER BY sequence`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var eventID, policyID string
		if err := rows.Scan(&eventID, &policyID); err != nil || policies[policyID].Decision != "HALT_AND_ROLLBACK" || !claimedRollbacks[eventID] {
			rows.Close()
			return errors.New("automatic strategy rollback reverse coverage is invalid")
		}
	}
	return rows.Close()
}

func maxSequence(ctx context.Context, q orderQuerier, table string) (int64, error) {
	if table != "strategy_selection_events" && table != "execution_authority_events" && table != "paper_strategy_performance_events" {
		return 0, errors.New("unsupported sequence table")
	}
	var value int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM `+table).Scan(&value)
	return value, err
}

func armedExecutionAccounts(ctx context.Context, q orderQuerier) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT account_ref FROM execution_authority_events ORDER BY account_ref`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []string
	for rows.Next() {
		var account string
		if err := rows.Scan(&account); err != nil {
			return nil, err
		}
		state, err := loadExecutionAuthoritySnapshot(ctx, q, account)
		if err != nil {
			return nil, err
		}
		if state.Armed {
			accounts = append(accounts, account)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return accounts, nil
}

func provePaperPerformancePolicyPrerequisites(ctx context.Context, q orderQuerier) error {
	if _, err := provePaperStrategyPerformanceRecovery(ctx, q); err != nil {
		return err
	}
	return nil
}
