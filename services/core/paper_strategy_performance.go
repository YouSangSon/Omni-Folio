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

	"omni-folio/services/core/internal/paperdomain"
)

const (
	paperStrategyPerformanceSchema        = "paper-strategy-window-performance.v1"
	paperStrategyPerformancePolicy        = "paper-strategy-window-performance-v1"
	noPaperStrategyPerformancePredecessor = "no_strategy_performance"
)

type PaperStrategyPerformanceEvent struct {
	StrategyPerformanceID                 string `json:"strategy_performance_id"`
	SchemaVersion                         string `json:"schema_version"`
	PolicyVersion                         string `json:"policy_version"`
	AccountRef                            string `json:"account_ref"`
	PaperAccountingSessionID              string `json:"paper_accounting_session_id"`
	StrategySelectionEventID              string `json:"strategy_selection_event_id"`
	SelectedStrategyResultRef             string `json:"selected_strategy_result_ref"`
	ExpectedPreviousStrategyPerformanceID string `json:"expected_previous_strategy_performance_id"`
	BaselinePerformanceID                 string `json:"baseline_performance_id"`
	LatestPerformanceID                   string `json:"latest_performance_id"`
	BaselineAsOf                          string `json:"baseline_as_of"`
	LatestAsOf                            string `json:"latest_as_of"`
	SampleCount                           int    `json:"sample_count"`
	BaselineEquity                        string `json:"baseline_equity"`
	LatestEquity                          string `json:"latest_equity"`
	PeakEquity                            string `json:"peak_equity"`
	PeriodReturnState                     string `json:"period_return_state"`
	PeriodReturn                          string `json:"period_return"`
	CumulativeReturn                      string `json:"cumulative_return"`
	Drawdown                              string `json:"drawdown"`
	MaxDrawdown                           string `json:"max_drawdown"`
	RecordedAt                            string `json:"recorded_at"`
}

type paperStrategyPerformanceRecoveryProof struct {
	SHA256  string
	Events  int
	Samples int
}

type paperStrategyPerformanceWindow struct {
	baseline PaperPerformanceEvent
	latest   PaperPerformanceEvent
	equities []string
}

func (s *Service) evaluatePaperStrategyPerformance(
	ctx context.Context,
	accountRef string,
	expectedSelectionEventID string,
	expectedLatestPerformanceID string,
) (*PaperStrategyPerformanceEvent, error) {
	if s == nil || s.db == nil || s.now == nil || !orderAlias(accountRef, "account") ||
		!safeOrderID(expectedSelectionEventID) || !safeOrderID(expectedLatestPerformanceID) {
		return nil, errors.New("paper strategy performance identifiers are invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := provePaperStrategyPerformanceRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("paper strategy performance recovery: %w", err)
	}
	_, selectionEventID, selectedResult, err := currentPaperPerformanceSelection(ctx, tx)
	if err != nil {
		return nil, err
	}
	if selectionEventID != expectedSelectionEventID {
		return nil, errors.New("paper strategy performance selection is stale")
	}
	if selectedResult == noStrategySelection {
		return nil, errors.New("paper strategy performance is not attributable to no_strategy")
	}
	latest, latestSequence, found, err := loadLatestPaperPerformance(ctx, tx, accountRef)
	if err != nil {
		return nil, err
	}
	if !found || latest.PerformanceID != expectedLatestPerformanceID {
		return nil, errors.New("paper strategy performance latest point is stale")
	}
	if latest.StrategySelectionEventID != selectionEventID || latest.SelectedStrategyResultRef != selectedResult {
		return nil, errors.New("paper strategy performance requires a current-selection point")
	}
	if existing, found, err := loadPaperStrategyPerformanceByKey(ctx, tx, accountRef, selectionEventID, latest.PerformanceID); err != nil {
		return nil, err
	} else if found {
		return existing, nil
	}
	window, err := loadPaperStrategyPerformanceWindow(ctx, tx, accountRef, latest.PaperAccountingSessionID,
		selectionEventID, selectedResult, latest.PerformanceID, latestSequence)
	if err != nil {
		return nil, err
	}
	points, err := paperdomain.CalculatePerformance(window.baseline.Equity, window.equities)
	if err != nil || len(points) == 0 {
		return nil, errors.Join(errors.New("paper strategy performance window is invalid"), err)
	}
	point := points[len(points)-1]
	previousID, err := latestPaperStrategyPerformanceID(ctx, tx, accountRef, latest.PaperAccountingSessionID, selectionEventID)
	if err != nil {
		return nil, err
	}
	event := PaperStrategyPerformanceEvent{
		SchemaVersion: paperStrategyPerformanceSchema, PolicyVersion: paperStrategyPerformancePolicy,
		AccountRef: accountRef, PaperAccountingSessionID: latest.PaperAccountingSessionID,
		StrategySelectionEventID: selectionEventID, SelectedStrategyResultRef: selectedResult,
		ExpectedPreviousStrategyPerformanceID: previousID, BaselinePerformanceID: window.baseline.PerformanceID,
		LatestPerformanceID: latest.PerformanceID, BaselineAsOf: window.baseline.AsOf, LatestAsOf: latest.AsOf,
		SampleCount: len(window.equities), BaselineEquity: window.baseline.Equity, LatestEquity: latest.Equity,
		PeakEquity: point.PeakEquity, PeriodReturnState: point.PeriodReturnState, PeriodReturn: point.PeriodReturn,
		CumulativeReturn: point.CumulativeReturn, Drawdown: point.Drawdown, MaxDrawdown: point.MaxDrawdown,
		RecordedAt: s.now().UTC().Format(canonicalPaperTimeLayout),
	}
	event.StrategyPerformanceID = paperStrategyPerformanceID(event)
	if err := insertPaperStrategyPerformanceEvent(ctx, tx, event); err != nil {
		return nil, err
	}
	if _, err := provePaperStrategyPerformanceRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("proposed paper strategy performance recovery: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &event, nil
}

func paperStrategyPerformanceID(event PaperStrategyPerformanceEvent) string {
	hash := sha256.Sum256([]byte(strings.Join([]string{
		event.PolicyVersion, event.AccountRef, event.PaperAccountingSessionID, event.StrategySelectionEventID,
		event.BaselinePerformanceID, event.LatestPerformanceID,
	}, "\x00")))
	return "paper_strategy_performance_" + hex.EncodeToString(hash[:16])
}

func insertPaperStrategyPerformanceEvent(ctx context.Context, tx *sql.Tx, event PaperStrategyPerformanceEvent) error {
	if err := validatePaperStrategyPerformanceEventShape(event); err != nil {
		return err
	}
	recordJSON, recordSHA, err := orderJSONHash(event)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO paper_strategy_performance_events(
		strategy_performance_id,schema_version,policy_version,account_ref,paper_accounting_session_id,
		strategy_selection_event_id,selected_strategy_result_ref,expected_previous_strategy_performance_id,
		baseline_performance_id,latest_performance_id,baseline_as_of,latest_as_of,sample_count,baseline_equity,
		latest_equity,peak_equity,period_return_state,period_return,cumulative_return,drawdown,max_drawdown,
		record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, event.StrategyPerformanceID, event.SchemaVersion,
		event.PolicyVersion, event.AccountRef, event.PaperAccountingSessionID, event.StrategySelectionEventID,
		event.SelectedStrategyResultRef, event.ExpectedPreviousStrategyPerformanceID, event.BaselinePerformanceID,
		event.LatestPerformanceID, event.BaselineAsOf, event.LatestAsOf, event.SampleCount, event.BaselineEquity,
		event.LatestEquity, event.PeakEquity, event.PeriodReturnState, event.PeriodReturn, event.CumulativeReturn,
		event.Drawdown, event.MaxDrawdown, recordSHA, string(recordJSON), event.RecordedAt)
	return err
}

// ponytail: full replay is linear in strategy evidence and source performance points; add a projection only after measured volume makes it hot.
func provePaperStrategyPerformanceRecovery(ctx context.Context, q orderQuerier) (paperStrategyPerformanceRecoveryProof, error) {
	if _, err := provePaperPerformanceRecovery(ctx, q); err != nil {
		return paperStrategyPerformanceRecoveryProof{}, fmt.Errorf("paper strategy performance prerequisite recovery: %w", err)
	}
	rows, err := q.QueryContext(ctx, `SELECT sequence,strategy_performance_id,schema_version,policy_version,account_ref,
		paper_accounting_session_id,strategy_selection_event_id,selected_strategy_result_ref,
		expected_previous_strategy_performance_id,baseline_performance_id,latest_performance_id,baseline_as_of,
		latest_as_of,sample_count,baseline_equity,latest_equity,peak_equity,period_return_state,period_return,
		cumulative_return,drawdown,max_drawdown,record_sha256,record_json,recorded_at
		FROM paper_strategy_performance_events ORDER BY sequence`)
	if err != nil {
		return paperStrategyPerformanceRecoveryProof{}, err
	}
	type stored struct {
		sequence              int64
		event                 PaperStrategyPerformanceEvent
		recordSHA, recordJSON string
	}
	var events []stored
	for rows.Next() {
		var item stored
		if err := scanPaperStrategyPerformance(rows.Scan, &item.event, &item.sequence, &item.recordSHA, &item.recordJSON); err != nil {
			rows.Close()
			return paperStrategyPerformanceRecoveryProof{}, err
		}
		events = append(events, item)
	}
	if err := closeRows(rows); err != nil {
		return paperStrategyPerformanceRecoveryProof{}, err
	}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	previous := map[string]string{}
	samples := 0
	for index, item := range events {
		if item.sequence != int64(index+1) {
			return paperStrategyPerformanceRecoveryProof{}, errors.New("paper strategy performance sequence is invalid")
		}
		if err := validateStoredPaperStrategyPerformanceEvent(item.event, item.recordSHA, item.recordJSON); err != nil {
			return paperStrategyPerformanceRecoveryProof{}, fmt.Errorf("paper strategy performance %q metadata or hash mismatch: %w", item.event.StrategyPerformanceID, err)
		}
		var latestSequence int64
		if err := q.QueryRowContext(ctx, `SELECT sequence FROM paper_performance_events WHERE performance_id=?`, item.event.LatestPerformanceID).Scan(&latestSequence); err != nil {
			return paperStrategyPerformanceRecoveryProof{}, err
		}
		window, err := loadPaperStrategyPerformanceWindow(ctx, q, item.event.AccountRef, item.event.PaperAccountingSessionID,
			item.event.StrategySelectionEventID, item.event.SelectedStrategyResultRef, item.event.LatestPerformanceID, latestSequence)
		if err != nil {
			return paperStrategyPerformanceRecoveryProof{}, err
		}
		points, err := paperdomain.CalculatePerformance(window.baseline.Equity, window.equities)
		if err != nil || len(points) == 0 {
			return paperStrategyPerformanceRecoveryProof{}, errors.Join(errors.New("paper strategy performance recovery calculation failed"), err)
		}
		point := points[len(points)-1]
		key := strings.Join([]string{item.event.AccountRef, item.event.PaperAccountingSessionID, item.event.StrategySelectionEventID}, "\x00")
		expectedPrevious := noPaperStrategyPerformancePredecessor
		if previous[key] != "" {
			expectedPrevious = previous[key]
		}
		if item.event.ExpectedPreviousStrategyPerformanceID != expectedPrevious ||
			item.event.BaselinePerformanceID != window.baseline.PerformanceID || item.event.LatestPerformanceID != window.latest.PerformanceID ||
			item.event.BaselineAsOf != window.baseline.AsOf || item.event.LatestAsOf != window.latest.AsOf ||
			item.event.SampleCount != len(window.equities) || item.event.BaselineEquity != window.baseline.Equity ||
			item.event.LatestEquity != window.latest.Equity || item.event.PeakEquity != point.PeakEquity ||
			item.event.PeriodReturnState != point.PeriodReturnState || item.event.PeriodReturn != point.PeriodReturn ||
			item.event.CumulativeReturn != point.CumulativeReturn || item.event.Drawdown != point.Drawdown ||
			item.event.MaxDrawdown != point.MaxDrawdown {
			return paperStrategyPerformanceRecoveryProof{}, errors.New("paper strategy performance window mismatch")
		}
		if err := encoder.Encode([]any{"paper_strategy_performance_events", item.sequence, item.event, item.recordSHA, item.recordJSON}); err != nil {
			return paperStrategyPerformanceRecoveryProof{}, err
		}
		previous[key] = item.event.StrategyPerformanceID
		samples += item.event.SampleCount
	}
	return paperStrategyPerformanceRecoveryProof{SHA256: hex.EncodeToString(hash.Sum(nil)), Events: len(events), Samples: samples}, nil
}

func loadPaperStrategyPerformanceWindow(ctx context.Context, q orderQuerier, accountRef, sessionID, selectionEventID,
	selectedResult, latestPerformanceID string, latestSequence int64,
) (paperStrategyPerformanceWindow, error) {
	var selectionRecordedAt string
	if err := q.QueryRowContext(ctx, `SELECT recorded_at FROM strategy_selection_events WHERE event_id=? AND selected_result_sha256=?`,
		selectionEventID, selectedResult).Scan(&selectionRecordedAt); err != nil {
		return paperStrategyPerformanceWindow{}, err
	}
	selectionTime, ok := canonicalUTCTime(selectionRecordedAt)
	if !ok {
		return paperStrategyPerformanceWindow{}, errors.New("paper strategy performance selection time is invalid")
	}
	rows, err := q.QueryContext(ctx, `SELECT 0,performance_id,schema_version,policy_version,account_ref,
		paper_accounting_session_id,strategy_selection_event_id,selected_strategy_result_ref,expected_previous_performance_id,
		strategy_selection_sequence_cutoff,order_event_sequence_cutoff,paper_market_sequence_cutoff,as_of,
		paper_account_state_sha256,marks_sha256,marks_json,mark_count,cash,open_cost,market_value,realized_pnl,
		unrealized_pnl,total_pnl,equity,peak_equity,period_return_state,period_return,cumulative_return,drawdown,
		max_drawdown,record_sha256,record_json,recorded_at FROM paper_performance_events
		WHERE account_ref=? AND paper_accounting_session_id=? AND strategy_selection_event_id=? AND sequence<=?
		ORDER BY sequence`, accountRef, sessionID, selectionEventID, latestSequence)
	if err != nil {
		return paperStrategyPerformanceWindow{}, err
	}
	var window paperStrategyPerformanceWindow
	foundLatest := false
	for rows.Next() {
		var event PaperPerformanceEvent
		var sequence int64
		var recordSHA, recordJSON string
		if err := scanPaperPerformance(rows.Scan, &event, &sequence, &recordSHA, &recordJSON); err != nil {
			rows.Close()
			return paperStrategyPerformanceWindow{}, err
		}
		asOf, ok := parsePaperTime(event.AsOf)
		if !ok || asOf.Before(selectionTime) {
			continue
		}
		if event.SelectedStrategyResultRef != selectedResult {
			rows.Close()
			return paperStrategyPerformanceWindow{}, errors.New("paper strategy performance result binding is invalid")
		}
		if len(window.equities) == 0 {
			window.baseline = event
		}
		window.latest = event
		window.equities = append(window.equities, event.Equity)
		if event.PerformanceID == latestPerformanceID {
			foundLatest = true
		}
	}
	if err := closeRows(rows); err != nil {
		return paperStrategyPerformanceWindow{}, err
	}
	if !foundLatest || len(window.equities) == 0 || window.latest.PerformanceID != latestPerformanceID {
		return paperStrategyPerformanceWindow{}, errors.New("paper strategy performance current-selection window is missing")
	}
	return window, nil
}

func loadLatestPaperPerformance(ctx context.Context, q orderQuerier, accountRef string) (PaperPerformanceEvent, int64, bool, error) {
	row := q.QueryRowContext(ctx, `SELECT sequence,performance_id,schema_version,policy_version,account_ref,
		paper_accounting_session_id,strategy_selection_event_id,selected_strategy_result_ref,expected_previous_performance_id,
		strategy_selection_sequence_cutoff,order_event_sequence_cutoff,paper_market_sequence_cutoff,as_of,
		paper_account_state_sha256,marks_sha256,marks_json,mark_count,cash,open_cost,market_value,realized_pnl,
		unrealized_pnl,total_pnl,equity,peak_equity,period_return_state,period_return,cumulative_return,drawdown,
		max_drawdown,record_sha256,record_json,recorded_at FROM paper_performance_events
		WHERE account_ref=? ORDER BY sequence DESC LIMIT 1`, accountRef)
	var event PaperPerformanceEvent
	var sequence int64
	var recordSHA, recordJSON string
	err := scanPaperPerformance(row.Scan, &event, &sequence, &recordSHA, &recordJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return PaperPerformanceEvent{}, 0, false, nil
	}
	if err != nil {
		return PaperPerformanceEvent{}, 0, false, err
	}
	if err := validateStoredPaperPerformanceEvent(event, recordSHA, recordJSON); err != nil {
		return PaperPerformanceEvent{}, 0, false, err
	}
	return event, sequence, true, nil
}

func loadPaperStrategyPerformanceByKey(ctx context.Context, q orderQuerier, accountRef, selectionEventID,
	latestPerformanceID string,
) (*PaperStrategyPerformanceEvent, bool, error) {
	row := q.QueryRowContext(ctx, `SELECT 0,strategy_performance_id,schema_version,policy_version,account_ref,
		paper_accounting_session_id,strategy_selection_event_id,selected_strategy_result_ref,
		expected_previous_strategy_performance_id,baseline_performance_id,latest_performance_id,baseline_as_of,
		latest_as_of,sample_count,baseline_equity,latest_equity,peak_equity,period_return_state,period_return,
		cumulative_return,drawdown,max_drawdown,record_sha256,record_json,recorded_at
		FROM paper_strategy_performance_events WHERE policy_version=? AND account_ref=?
		AND strategy_selection_event_id=? AND latest_performance_id=?`, paperStrategyPerformancePolicy, accountRef,
		selectionEventID, latestPerformanceID)
	var event PaperStrategyPerformanceEvent
	var sequence int64
	var recordSHA, recordJSON string
	err := scanPaperStrategyPerformance(row.Scan, &event, &sequence, &recordSHA, &recordJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := validateStoredPaperStrategyPerformanceEvent(event, recordSHA, recordJSON); err != nil {
		return nil, false, err
	}
	return &event, true, nil
}

func latestPaperStrategyPerformanceID(ctx context.Context, q orderQuerier, accountRef, sessionID, selectionEventID string) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT strategy_performance_id FROM paper_strategy_performance_events
		WHERE account_ref=? AND paper_accounting_session_id=? AND strategy_selection_event_id=?
		ORDER BY sequence DESC LIMIT 1`, accountRef, sessionID, selectionEventID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return noPaperStrategyPerformancePredecessor, nil
	}
	return id, err
}

func scanPaperStrategyPerformance(scan func(...any) error, event *PaperStrategyPerformanceEvent, sequence *int64,
	recordSHA, recordJSON *string,
) error {
	return scan(sequence, &event.StrategyPerformanceID, &event.SchemaVersion, &event.PolicyVersion, &event.AccountRef,
		&event.PaperAccountingSessionID, &event.StrategySelectionEventID, &event.SelectedStrategyResultRef,
		&event.ExpectedPreviousStrategyPerformanceID, &event.BaselinePerformanceID, &event.LatestPerformanceID,
		&event.BaselineAsOf, &event.LatestAsOf, &event.SampleCount, &event.BaselineEquity, &event.LatestEquity,
		&event.PeakEquity, &event.PeriodReturnState, &event.PeriodReturn, &event.CumulativeReturn, &event.Drawdown,
		&event.MaxDrawdown, recordSHA, recordJSON, &event.RecordedAt)
}

func validateStoredPaperStrategyPerformanceEvent(event PaperStrategyPerformanceEvent, recordSHA, recordJSON string) error {
	if err := validatePaperStrategyPerformanceEventShape(event); err != nil {
		return err
	}
	canonical, actualSHA, err := orderJSONHash(event)
	if err != nil || actualSHA != recordSHA || string(canonical) != recordJSON {
		return errors.Join(errors.New("paper strategy performance record JSON or hash mismatch"), err)
	}
	return nil
}

func validatePaperStrategyPerformanceEventShape(event PaperStrategyPerformanceEvent) error {
	if event.StrategyPerformanceID != paperStrategyPerformanceID(event) || !safeOrderID(event.StrategyPerformanceID) ||
		event.SchemaVersion != paperStrategyPerformanceSchema || event.PolicyVersion != paperStrategyPerformancePolicy ||
		!orderAlias(event.AccountRef, "account") || !safeOrderID(event.PaperAccountingSessionID) ||
		!safeOrderID(event.StrategySelectionEventID) || !strategySHA256Pattern.MatchString(event.SelectedStrategyResultRef) ||
		!safeOrderID(event.ExpectedPreviousStrategyPerformanceID) || !safeOrderID(event.BaselinePerformanceID) ||
		!safeOrderID(event.LatestPerformanceID) || event.SampleCount <= 0 ||
		!canonicalPaperTimes(event.BaselineAsOf, event.LatestAsOf, event.RecordedAt) || event.BaselineAsOf > event.LatestAsOf {
		return errors.New("paper strategy performance event shape is invalid")
	}
	for _, raw := range []string{event.BaselineEquity, event.LatestEquity, event.PeakEquity, event.CumulativeReturn, event.Drawdown, event.MaxDrawdown} {
		if _, err := parseDecimal(raw); err != nil {
			return errors.New("paper strategy performance decimal is invalid")
		}
	}
	if event.PeriodReturnState == "defined" {
		if _, err := parseDecimal(event.PeriodReturn); err != nil {
			return errors.New("paper strategy performance period return is invalid")
		}
	} else if event.PeriodReturnState != "undefined_zero_denominator" || event.PeriodReturn != "" {
		return errors.New("paper strategy performance period return state is invalid")
	}
	return nil
}
