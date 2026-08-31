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
	"strings"

	"omni-folio/services/core/internal/paperdomain"
)

const (
	paperPerformanceSchema        = "paper-performance-evaluation.v1"
	paperPerformancePolicy        = "paper-performance-account-v1"
	noPaperPerformancePredecessor = "no_performance"
)

type PaperPerformanceEvent struct {
	PerformanceID                   string `json:"performance_id"`
	SchemaVersion                   string `json:"schema_version"`
	PolicyVersion                   string `json:"policy_version"`
	AccountRef                      string `json:"account_ref"`
	PaperAccountingSessionID        string `json:"paper_accounting_session_id"`
	StrategySelectionEventID        string `json:"strategy_selection_event_id"`
	SelectedStrategyResultRef       string `json:"selected_strategy_result_ref"`
	ExpectedPreviousPerformanceID   string `json:"expected_previous_performance_id"`
	StrategySelectionSequenceCutoff int64  `json:"strategy_selection_sequence_cutoff"`
	OrderEventSequenceCutoff        int64  `json:"order_event_sequence_cutoff"`
	PaperMarketSequenceCutoff       int64  `json:"paper_market_sequence_cutoff"`
	AsOf                            string `json:"as_of"`
	PaperAccountStateSHA256         string `json:"paper_account_state_sha256"`
	MarksSHA256                     string `json:"marks_sha256"`
	MarksJSON                       string `json:"marks_json"`
	MarkCount                       int    `json:"mark_count"`
	Cash                            string `json:"cash"`
	OpenCost                        string `json:"open_cost"`
	MarketValue                     string `json:"market_value"`
	RealizedPnL                     string `json:"realized_pnl"`
	UnrealizedPnL                   string `json:"unrealized_pnl"`
	TotalPnL                        string `json:"total_pnl"`
	Equity                          string `json:"equity"`
	PeakEquity                      string `json:"peak_equity"`
	PeriodReturnState               string `json:"period_return_state"`
	PeriodReturn                    string `json:"period_return"`
	CumulativeReturn                string `json:"cumulative_return"`
	Drawdown                        string `json:"drawdown"`
	MaxDrawdown                     string `json:"max_drawdown"`
	RecordedAt                      string `json:"recorded_at"`
}

type paperPerformanceRecoveryProof struct {
	SHA256 string
	Events int
	Marks  int
}

type paperPerformanceMark struct {
	Symbol              string `json:"symbol"`
	Quantity            string `json:"quantity"`
	ObservationID       string `json:"observation_id"`
	ObservationSequence int64  `json:"observation_sequence"`
	Close               string `json:"close"`
	OpenCost            string `json:"open_cost"`
	MarketValue         string `json:"market_value"`
	UnrealizedPnL       string `json:"unrealized_pnl"`
}

func (s *Service) evaluatePaperPerformance(ctx context.Context, accountRef, asOf string) (*PaperPerformanceEvent, error) {
	return s.evaluatePaperPerformanceTx(ctx, accountRef, asOf, nil)
}

func (s *Service) evaluatePaperPerformanceWithClaim(ctx context.Context, accountRef, asOf string, claim *paperRunnerClaim) (*PaperPerformanceEvent, error) {
	if s == nil || s.db == nil || s.now == nil || claim == nil || claim.AccountRef != accountRef || !orderAlias(accountRef, "account") {
		return nil, errors.New("paper performance runner claim is missing")
	}
	if _, ok := canonicalPaperTime(asOf); !ok {
		return nil, errors.New("paper performance as_of is invalid")
	}
	stageCtx, cancel := context.WithTimeout(ctx, paperRunnerStageTimeout)
	defer cancel()
	renewed, err := s.heartbeatPaperRunnerLease(stageCtx, claim)
	if err != nil {
		return nil, err
	}
	*claim = *renewed
	return s.evaluatePaperPerformanceTx(stageCtx, accountRef, asOf, claim)
}

func (s *Service) evaluatePaperPerformanceTx(ctx context.Context, accountRef, asOf string, claim *paperRunnerClaim) (*PaperPerformanceEvent, error) {
	if s == nil || s.db == nil || s.now == nil || !orderAlias(accountRef, "account") {
		return nil, errors.New("paper performance evaluator is not configured")
	}
	canonicalAsOf, ok := canonicalPaperTime(asOf)
	if !ok {
		return nil, errors.New("paper performance as_of is invalid")
	}
	asOf = canonicalAsOf
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if claim != nil {
		if err := validatePaperRunnerLeaseTx(ctx, tx, claim, accountRef, s.paperRunnerOwner, s.now()); err != nil {
			return nil, err
		}
	}
	if _, err := provePaperPerformanceRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("paper performance recovery: %w", err)
	}
	existing, found, err := loadPaperPerformanceByKey(ctx, tx, accountRef, asOf)
	if err != nil {
		return nil, err
	}
	if found {
		if claim != nil {
			if existing.StrategySelectionEventID != claim.StrategySelectionEventID || existing.SelectedStrategyResultRef != claim.SelectedResultSHA256 {
				return nil, errPaperRunnerPriorSelection
			}
			renewed, err := renewPaperRunnerLeaseTx(ctx, tx, claim, s.paperRunnerOwner, s.now())
			if err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			*claim = *renewed
		}
		return existing, nil
	}

	session, found, err := loadPaperAccountingSession(ctx, tx, accountRef)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("paper performance accounting session is missing")
	}
	if err := rejectLegacyPaperPerformanceAccount(ctx, tx, accountRef); err != nil {
		return nil, err
	}
	recordedAt := s.now().UTC().Format(canonicalPaperTimeLayout)
	if err := validatePaperPerformanceWindow(*session, asOf, recordedAt); err != nil {
		return nil, err
	}
	selectionSequence, selectionEventID, selectedResult, err := currentPaperPerformanceSelection(ctx, tx)
	if err != nil {
		return nil, err
	}
	var orderCutoff, marketCutoff int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM order_events`).Scan(&orderCutoff); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM paper_market_bar_observations`).Scan(&marketCutoff); err != nil {
		return nil, err
	}
	states, err := replayPaperAccountingStateWithCutoff(ctx, tx, &paperAccountingCutoff{OrderSequence: orderCutoff, AsOf: asOf})
	if err != nil {
		return nil, err
	}
	state, exists := states[accountRef]
	if !exists || state.PaperAccountingSessionID != session.SessionID {
		return nil, errors.New("paper performance account state is missing")
	}
	marks, valuation, err := derivePaperPerformanceMarks(ctx, tx, state, session.StartingCash, asOf, recordedAt, marketCutoff)
	if err != nil {
		return nil, err
	}
	accountJSON, accountSHA, err := orderJSONHash(state)
	if err != nil || len(accountJSON) == 0 {
		return nil, errors.Join(errors.New("paper performance account state is invalid"), err)
	}
	marksJSON, marksSHA, err := orderJSONHash(marks)
	if err != nil {
		return nil, err
	}
	previousID, equities, err := priorPaperPerformanceSeries(ctx, tx, accountRef, session.SessionID, asOf)
	if err != nil {
		return nil, err
	}
	points, err := paperdomain.CalculatePerformance(session.StartingCash, append(equities, valuation.Equity))
	if err != nil || len(points) == 0 {
		return nil, errors.Join(errors.New("paper performance series is invalid"), err)
	}
	point := points[len(points)-1]
	event := PaperPerformanceEvent{
		SchemaVersion: paperPerformanceSchema, PolicyVersion: paperPerformancePolicy, AccountRef: accountRef,
		PaperAccountingSessionID: session.SessionID, StrategySelectionEventID: selectionEventID,
		SelectedStrategyResultRef: selectedResult, ExpectedPreviousPerformanceID: previousID,
		StrategySelectionSequenceCutoff: selectionSequence, OrderEventSequenceCutoff: orderCutoff,
		PaperMarketSequenceCutoff: marketCutoff, AsOf: asOf, PaperAccountStateSHA256: accountSHA,
		MarksSHA256: marksSHA, MarksJSON: string(marksJSON), MarkCount: len(marks), Cash: valuation.Cash,
		OpenCost: valuation.OpenCost, MarketValue: valuation.MarketValue, RealizedPnL: valuation.RealizedPnL,
		UnrealizedPnL: valuation.UnrealizedPnL, TotalPnL: valuation.TotalPnL, Equity: valuation.Equity,
		PeakEquity: point.PeakEquity, PeriodReturnState: point.PeriodReturnState, PeriodReturn: point.PeriodReturn,
		CumulativeReturn: point.CumulativeReturn, Drawdown: point.Drawdown, MaxDrawdown: point.MaxDrawdown,
		RecordedAt: recordedAt,
	}
	event.PerformanceID = paperPerformanceID(event.PolicyVersion, event.AccountRef, event.PaperAccountingSessionID, event.AsOf)
	if err := insertPaperPerformanceEvent(ctx, tx, event); err != nil {
		return nil, err
	}
	if _, err := provePaperPerformanceRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("proposed paper performance recovery: %w", err)
	}
	var renewed *paperRunnerClaim
	if claim != nil {
		renewed, err = renewPaperRunnerLeaseTx(ctx, tx, claim, s.paperRunnerOwner, s.now())
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if renewed != nil {
		*claim = *renewed
	}
	return &event, nil
}

func paperPerformanceID(policyVersion, accountRef, sessionID, asOf string) string {
	hash := sha256.Sum256([]byte(strings.Join([]string{policyVersion, accountRef, sessionID, asOf}, "\x00")))
	return "paper_performance_" + hex.EncodeToString(hash[:16])
}

func insertPaperPerformanceEvent(ctx context.Context, tx *sql.Tx, event PaperPerformanceEvent) error {
	if err := validatePaperPerformanceEventShape(event); err != nil {
		return err
	}
	recordJSON, recordSHA, err := orderJSONHash(event)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO paper_performance_events(
		performance_id,schema_version,policy_version,account_ref,paper_accounting_session_id,
		strategy_selection_event_id,selected_strategy_result_ref,expected_previous_performance_id,
		strategy_selection_sequence_cutoff,order_event_sequence_cutoff,paper_market_sequence_cutoff,as_of,
		paper_account_state_sha256,marks_sha256,marks_json,mark_count,cash,open_cost,market_value,realized_pnl,
		unrealized_pnl,total_pnl,equity,peak_equity,period_return_state,period_return,cumulative_return,drawdown,
		max_drawdown,record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		event.PerformanceID, event.SchemaVersion, event.PolicyVersion, event.AccountRef, event.PaperAccountingSessionID,
		event.StrategySelectionEventID, event.SelectedStrategyResultRef, event.ExpectedPreviousPerformanceID,
		event.StrategySelectionSequenceCutoff, event.OrderEventSequenceCutoff, event.PaperMarketSequenceCutoff, event.AsOf,
		event.PaperAccountStateSHA256, event.MarksSHA256, event.MarksJSON, event.MarkCount, event.Cash, event.OpenCost,
		event.MarketValue, event.RealizedPnL, event.UnrealizedPnL, event.TotalPnL, event.Equity, event.PeakEquity,
		event.PeriodReturnState, event.PeriodReturn, event.CumulativeReturn, event.Drawdown, event.MaxDrawdown,
		recordSHA, string(recordJSON), event.RecordedAt)
	return err
}

// ponytail: full proof is O(P*(F+B+P)); add a disposable projection only after measured event volume or latency makes replay too slow.
func provePaperPerformanceRecovery(ctx context.Context, q orderQuerier) (paperPerformanceRecoveryProof, error) {
	if _, err := provePaperAccountingRecovery(ctx, q); err != nil {
		return paperPerformanceRecoveryProof{}, fmt.Errorf("paper performance prerequisite recovery: %w", err)
	}
	rows, err := q.QueryContext(ctx, `SELECT sequence,performance_id,schema_version,policy_version,account_ref,
		paper_accounting_session_id,strategy_selection_event_id,selected_strategy_result_ref,expected_previous_performance_id,
		strategy_selection_sequence_cutoff,order_event_sequence_cutoff,paper_market_sequence_cutoff,as_of,
		paper_account_state_sha256,marks_sha256,marks_json,mark_count,cash,open_cost,market_value,realized_pnl,
		unrealized_pnl,total_pnl,equity,peak_equity,period_return_state,period_return,cumulative_return,drawdown,
		max_drawdown,record_sha256,record_json,recorded_at FROM paper_performance_events ORDER BY sequence`)
	if err != nil {
		return paperPerformanceRecoveryProof{}, err
	}
	type storedPerformance struct {
		sequence              int64
		event                 PaperPerformanceEvent
		recordSHA, recordJSON string
	}
	var stored []storedPerformance
	for rows.Next() {
		var item storedPerformance
		if err := scanPaperPerformance(rows.Scan, &item.event, &item.sequence, &item.recordSHA, &item.recordJSON); err != nil {
			rows.Close()
			return paperPerformanceRecoveryProof{}, err
		}
		stored = append(stored, item)
	}
	if err := closeRows(rows); err != nil {
		return paperPerformanceRecoveryProof{}, err
	}

	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	type accountSeries struct {
		sessionID, previousID, previousAsOf string
		equities                            []string
	}
	series := map[string]*accountSeries{}
	markCount := 0
	for index, item := range stored {
		if item.sequence != int64(index+1) {
			return paperPerformanceRecoveryProof{}, fmt.Errorf("paper performance sequence %d is invalid", item.sequence)
		}
		if err := validateStoredPaperPerformanceEvent(item.event, item.recordSHA, item.recordJSON); err != nil {
			return paperPerformanceRecoveryProof{}, fmt.Errorf("paper performance %q metadata or hash mismatch: %w", item.event.PerformanceID, err)
		}
		if err := rejectLegacyPaperPerformanceAccount(ctx, q, item.event.AccountRef); err != nil {
			return paperPerformanceRecoveryProof{}, err
		}
		session, found, err := loadPaperAccountingSession(ctx, q, item.event.AccountRef)
		if err != nil || !found {
			return paperPerformanceRecoveryProof{}, errors.Join(errors.New("paper performance session is missing"), err)
		}
		if session.SessionID != item.event.PaperAccountingSessionID {
			return paperPerformanceRecoveryProof{}, errors.New("paper performance session binding is invalid")
		}
		if err := validatePaperPerformanceWindow(*session, item.event.AsOf, item.event.RecordedAt); err != nil {
			return paperPerformanceRecoveryProof{}, err
		}
		if err := validatePaperPerformanceSelection(ctx, q, item.event); err != nil {
			return paperPerformanceRecoveryProof{}, err
		}
		states, err := replayPaperAccountingStateWithCutoff(ctx, q, &paperAccountingCutoff{
			OrderSequence: item.event.OrderEventSequenceCutoff, AsOf: item.event.AsOf,
		})
		if err != nil {
			return paperPerformanceRecoveryProof{}, err
		}
		state, exists := states[item.event.AccountRef]
		if !exists {
			return paperPerformanceRecoveryProof{}, errors.New("paper performance replay account is missing")
		}
		_, accountSHA, err := orderJSONHash(state)
		if err != nil || accountSHA != item.event.PaperAccountStateSHA256 {
			return paperPerformanceRecoveryProof{}, errors.New("paper performance account digest mismatch")
		}
		marks, valuation, err := derivePaperPerformanceMarks(ctx, q, state, session.StartingCash, item.event.AsOf,
			item.event.RecordedAt, item.event.PaperMarketSequenceCutoff)
		if err != nil {
			return paperPerformanceRecoveryProof{}, err
		}
		marksJSON, marksSHA, err := orderJSONHash(marks)
		if err != nil || string(marksJSON) != item.event.MarksJSON || marksSHA != item.event.MarksSHA256 || len(marks) != item.event.MarkCount {
			return paperPerformanceRecoveryProof{}, errors.New("paper performance marks mismatch")
		}
		if valuation.Cash != item.event.Cash || valuation.OpenCost != item.event.OpenCost || valuation.MarketValue != item.event.MarketValue ||
			valuation.RealizedPnL != item.event.RealizedPnL || valuation.UnrealizedPnL != item.event.UnrealizedPnL ||
			valuation.TotalPnL != item.event.TotalPnL || valuation.Equity != item.event.Equity {
			return paperPerformanceRecoveryProof{}, errors.New("paper performance valuation mismatch")
		}
		account := series[item.event.AccountRef]
		if account == nil {
			account = &accountSeries{sessionID: session.SessionID, previousID: noPaperPerformancePredecessor}
			series[item.event.AccountRef] = account
		}
		if account.sessionID != session.SessionID || item.event.ExpectedPreviousPerformanceID != account.previousID ||
			(account.previousAsOf != "" && item.event.AsOf <= account.previousAsOf) {
			return paperPerformanceRecoveryProof{}, errors.New("paper performance predecessor or as_of is invalid")
		}
		points, err := paperdomain.CalculatePerformance(session.StartingCash, append(account.equities, valuation.Equity))
		if err != nil || len(points) == 0 {
			return paperPerformanceRecoveryProof{}, errors.Join(errors.New("paper performance series replay failed"), err)
		}
		point := points[len(points)-1]
		if point.PeakEquity != item.event.PeakEquity || point.PeriodReturnState != item.event.PeriodReturnState ||
			point.PeriodReturn != item.event.PeriodReturn || point.CumulativeReturn != item.event.CumulativeReturn ||
			point.Drawdown != item.event.Drawdown || point.MaxDrawdown != item.event.MaxDrawdown {
			return paperPerformanceRecoveryProof{}, errors.New("paper performance ratio series mismatch")
		}
		if err := encoder.Encode([]any{"paper_performance_events", item.sequence, item.event, item.recordSHA, item.recordJSON}); err != nil {
			return paperPerformanceRecoveryProof{}, err
		}
		account.previousID, account.previousAsOf = item.event.PerformanceID, item.event.AsOf
		account.equities = append(account.equities, valuation.Equity)
		markCount += item.event.MarkCount
	}
	return paperPerformanceRecoveryProof{SHA256: hex.EncodeToString(hash.Sum(nil)), Events: len(stored), Marks: markCount}, nil
}

func scanPaperPerformance(scan func(...any) error, event *PaperPerformanceEvent, sequence *int64, recordSHA, recordJSON *string) error {
	return scan(sequence, &event.PerformanceID, &event.SchemaVersion, &event.PolicyVersion, &event.AccountRef,
		&event.PaperAccountingSessionID, &event.StrategySelectionEventID, &event.SelectedStrategyResultRef,
		&event.ExpectedPreviousPerformanceID, &event.StrategySelectionSequenceCutoff, &event.OrderEventSequenceCutoff,
		&event.PaperMarketSequenceCutoff, &event.AsOf, &event.PaperAccountStateSHA256, &event.MarksSHA256,
		&event.MarksJSON, &event.MarkCount, &event.Cash, &event.OpenCost, &event.MarketValue, &event.RealizedPnL,
		&event.UnrealizedPnL, &event.TotalPnL, &event.Equity, &event.PeakEquity, &event.PeriodReturnState,
		&event.PeriodReturn, &event.CumulativeReturn, &event.Drawdown, &event.MaxDrawdown, recordSHA, recordJSON,
		&event.RecordedAt)
}

func loadPaperPerformanceByKey(ctx context.Context, q orderQuerier, accountRef, asOf string) (*PaperPerformanceEvent, bool, error) {
	row := q.QueryRowContext(ctx, `SELECT 0,performance_id,schema_version,policy_version,account_ref,
		paper_accounting_session_id,strategy_selection_event_id,selected_strategy_result_ref,expected_previous_performance_id,
		strategy_selection_sequence_cutoff,order_event_sequence_cutoff,paper_market_sequence_cutoff,as_of,
		paper_account_state_sha256,marks_sha256,marks_json,mark_count,cash,open_cost,market_value,realized_pnl,
		unrealized_pnl,total_pnl,equity,peak_equity,period_return_state,period_return,cumulative_return,drawdown,
		max_drawdown,record_sha256,record_json,recorded_at FROM paper_performance_events
		WHERE policy_version=? AND account_ref=? AND as_of=?`, paperPerformancePolicy, accountRef, asOf)
	var event PaperPerformanceEvent
	var sequence int64
	var recordSHA, recordJSON string
	err := scanPaperPerformance(row.Scan, &event, &sequence, &recordSHA, &recordJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := validateStoredPaperPerformanceEvent(event, recordSHA, recordJSON); err != nil {
		return nil, false, err
	}
	return &event, true, nil
}

func currentPaperPerformanceSelection(ctx context.Context, q orderQuerier) (int64, string, string, error) {
	var sequence int64
	var eventID, selected string
	err := q.QueryRowContext(ctx, `SELECT sequence,event_id,selected_result_sha256
		FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1`).Scan(&sequence, &eventID, &selected)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", "", errors.New("paper performance current strategy selection is missing")
	}
	return sequence, eventID, selected, err
}

func validatePaperPerformanceSelection(ctx context.Context, q orderQuerier, event PaperPerformanceEvent) error {
	var eventID, selected string
	err := q.QueryRowContext(ctx, `SELECT event_id,selected_result_sha256 FROM strategy_selection_events WHERE sequence=?`,
		event.StrategySelectionSequenceCutoff).Scan(&eventID, &selected)
	if err != nil || eventID != event.StrategySelectionEventID || selected != event.SelectedStrategyResultRef {
		return errors.Join(errors.New("paper performance strategy selection cutoff mismatch"), err)
	}
	var orderMax, marketMax int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM order_events`).Scan(&orderMax); err != nil {
		return err
	}
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM paper_market_bar_observations`).Scan(&marketMax); err != nil {
		return err
	}
	if event.OrderEventSequenceCutoff < 0 || event.OrderEventSequenceCutoff > orderMax ||
		event.PaperMarketSequenceCutoff < 0 || event.PaperMarketSequenceCutoff > marketMax {
		return errors.New("paper performance evidence cutoff is invalid")
	}
	return nil
}

func rejectLegacyPaperPerformanceAccount(ctx context.Context, q orderQuerier, accountRef string) error {
	var legacy int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_idempotency WHERE mode='paper' AND account_ref=?
		AND json_extract(intent_json, '$.signal_schema_version') IN ('paper-signal.v1','paper-signal.v2')`, accountRef).Scan(&legacy); err != nil {
		return err
	}
	if legacy != 0 {
		return errors.New("paper performance cannot include a legacy paper account")
	}
	return nil
}

func validatePaperPerformanceWindow(session PaperAccountingSession, asOf, recordedAt string) error {
	if !canonicalPaperTimes(asOf, recordedAt) || !canonicalUTCString(session.RecordedAt) {
		return errors.New("paper performance time window is invalid")
	}
	sessionTime, _ := parsePaperTime(session.RecordedAt)
	asOfTime, _ := parsePaperTime(asOf)
	recordedTime, _ := parsePaperTime(recordedAt)
	if !sessionTime.Before(asOfTime) || asOfTime.After(recordedTime) {
		return errors.New("paper performance requires session.RecordedAt < as_of <= event.RecordedAt")
	}
	return nil
}

func priorPaperPerformanceSeries(ctx context.Context, q orderQuerier, accountRef, sessionID, asOf string) (string, []string, error) {
	rows, err := q.QueryContext(ctx, `SELECT performance_id,paper_accounting_session_id,as_of,equity
		FROM paper_performance_events WHERE account_ref=? ORDER BY sequence`, accountRef)
	if err != nil {
		return "", nil, err
	}
	previous := noPaperPerformancePredecessor
	var equities []string
	for rows.Next() {
		var performanceID, storedSession, storedAsOf, equity string
		if err := rows.Scan(&performanceID, &storedSession, &storedAsOf, &equity); err != nil {
			rows.Close()
			return "", nil, err
		}
		if storedSession != sessionID || storedAsOf >= asOf {
			rows.Close()
			return "", nil, errors.New("paper performance backfill or session change is invalid")
		}
		previous = performanceID
		equities = append(equities, equity)
	}
	if err := closeRows(rows); err != nil {
		return "", nil, err
	}
	return previous, equities, nil
}

func validateStoredPaperPerformanceEvent(event PaperPerformanceEvent, recordSHA, recordJSON string) error {
	if err := validatePaperPerformanceEventShape(event); err != nil {
		return err
	}
	canonical, actualSHA, err := orderJSONHash(event)
	if err != nil || actualSHA != recordSHA || string(canonical) != recordJSON {
		return errors.Join(errors.New("paper performance record JSON or hash mismatch"), err)
	}
	return nil
}

func validatePaperPerformanceEventShape(event PaperPerformanceEvent) error {
	if !safeOrderID(event.PerformanceID) || event.PerformanceID != paperPerformanceID(event.PolicyVersion, event.AccountRef, event.PaperAccountingSessionID, event.AsOf) ||
		event.SchemaVersion != paperPerformanceSchema || event.PolicyVersion != paperPerformancePolicy ||
		!orderAlias(event.AccountRef, "account") || !safeOrderID(event.PaperAccountingSessionID) ||
		!safeOrderID(event.StrategySelectionEventID) || !safeOrderID(event.ExpectedPreviousPerformanceID) ||
		event.StrategySelectionSequenceCutoff <= 0 || event.OrderEventSequenceCutoff < 0 || event.PaperMarketSequenceCutoff < 0 ||
		!canonicalPaperTimes(event.AsOf, event.RecordedAt) || !strategySHA256Pattern.MatchString(event.PaperAccountStateSHA256) ||
		!strategySHA256Pattern.MatchString(event.MarksSHA256) || event.MarkCount < 0 {
		return errors.New("paper performance event shape is invalid")
	}
	if event.SelectedStrategyResultRef != noStrategySelection && !strategySHA256Pattern.MatchString(event.SelectedStrategyResultRef) {
		return errors.New("paper performance selected strategy is invalid")
	}
	if !canonicalDecimal(event.Cash, true) || !canonicalDecimal(event.OpenCost, true) || !canonicalDecimal(event.MarketValue, true) ||
		!canonicalDecimal(event.RealizedPnL, false) || !canonicalDecimal(event.UnrealizedPnL, false) ||
		!canonicalDecimal(event.TotalPnL, false) || !canonicalDecimal(event.Equity, true) || !canonicalDecimal(event.PeakEquity, true) ||
		!canonicalDecimal(event.CumulativeReturn, false) || !canonicalDecimal(event.Drawdown, true) || !canonicalDecimal(event.MaxDrawdown, true) {
		return errors.New("paper performance values are invalid")
	}
	if (event.PeriodReturnState == "defined" && !canonicalDecimal(event.PeriodReturn, false)) ||
		(event.PeriodReturnState == "undefined_zero_denominator" && event.PeriodReturn != "") ||
		(event.PeriodReturnState != "defined" && event.PeriodReturnState != "undefined_zero_denominator") {
		return errors.New("paper performance period return is invalid")
	}
	var marks []paperPerformanceMark
	if err := json.Unmarshal([]byte(event.MarksJSON), &marks); err != nil || marks == nil || len(marks) != event.MarkCount {
		return errors.New("paper performance marks JSON is invalid")
	}
	canonical, marksSHA, err := orderJSONHash(marks)
	if err != nil || string(canonical) != event.MarksJSON || marksSHA != event.MarksSHA256 {
		return errors.New("paper performance marks JSON or hash mismatch")
	}
	for _, mark := range marks {
		if len(mark.Symbol) != 6 || !safeOrderID(mark.ObservationID) || mark.ObservationSequence <= 0 ||
			!canonicalDecimal(mark.Quantity, true) || !canonicalDecimal(mark.Close, true) || !canonicalDecimal(mark.OpenCost, true) ||
			!canonicalDecimal(mark.MarketValue, true) || !canonicalDecimal(mark.UnrealizedPnL, false) {
			return errors.New("paper performance mark is invalid")
		}
	}
	return nil
}

func derivePaperPerformanceMarks(ctx context.Context, q orderQuerier, state paperAccountState, startingCash, asOf, recordedAt string, marketCutoff int64) ([]paperPerformanceMark, paperdomain.Valuation, error) {
	if marketCutoff < 0 || !canonicalPaperTimes(asOf, recordedAt) {
		return nil, paperdomain.Valuation{}, errors.New("paper performance mark cutoff is invalid")
	}
	recorded, _ := parsePaperTime(recordedAt)
	positions := make(map[string]bool, len(state.Lots))
	for symbol, lots := range state.Lots {
		if len(lots) != 0 {
			positions[symbol] = true
		}
	}
	if len(positions) == 0 {
		var anchored int
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM paper_market_bar_observations
			WHERE sequence<=? AND source='paper_fixture' AND venue='KRX' AND currency='KRW' AND interval='1d'
			  AND timezone='Asia/Seoul' AND price_adjustment='unspecified' AND close_at=?
			  AND source_available_at<=? AND fetched_at<=? AND recorded_at<=?)`,
			marketCutoff, asOf, recordedAt, recordedAt, recordedAt).Scan(&anchored); err != nil {
			return nil, paperdomain.Valuation{}, err
		}
		if anchored == 0 {
			return nil, paperdomain.Valuation{}, errors.New("paper performance cash-only close anchor is missing")
		}
		valuation, err := paperdomain.ValueAccount(startingCash, state, map[string]string{})
		if err != nil {
			return nil, paperdomain.Valuation{}, err
		}
		return []paperPerformanceMark{}, valuation, nil
	}

	rows, err := q.QueryContext(ctx, `SELECT observation_id FROM paper_market_bar_observations
		WHERE sequence<=? AND close_at=? ORDER BY sequence`, marketCutoff, asOf)
	if err != nil {
		return nil, paperdomain.Valuation{}, err
	}
	var observationIDs []string
	for rows.Next() {
		var observationID string
		if err := rows.Scan(&observationID); err != nil {
			rows.Close()
			return nil, paperdomain.Valuation{}, err
		}
		observationIDs = append(observationIDs, observationID)
	}
	if err := closeRows(rows); err != nil {
		return nil, paperdomain.Valuation{}, err
	}

	type selectedMark struct {
		bar      *PaperMarketBarObservation
		sequence int64
	}
	selected := make(map[string]selectedMark, len(positions))
	for _, observationID := range observationIDs {
		bar, sequence, err := loadPaperMarketBarByID(ctx, q, observationID)
		if err != nil {
			return nil, paperdomain.Valuation{}, err
		}
		if !positions[bar.Symbol] {
			continue
		}
		if bar.Source != "paper_fixture" || bar.Venue != "KRX" || bar.Currency != "KRW" || bar.Interval != "1d" ||
			bar.Timezone != "Asia/Seoul" || bar.PriceAdjustment != "unspecified" || bar.CloseAt != asOf {
			continue
		}
		sourceAvailable, _ := parsePaperTime(bar.SourceAvailableAt)
		fetched, _ := parsePaperTime(bar.FetchedAt)
		barRecorded, _ := parsePaperTime(bar.RecordedAt)
		if sourceAvailable.After(recorded) || fetched.After(recorded) || barRecorded.After(recorded) {
			return nil, paperdomain.Valuation{}, errors.New("paper performance mark was unavailable at record time")
		}
		if _, exists := selected[bar.Symbol]; exists {
			return nil, paperdomain.Valuation{}, errors.New("paper performance mark is ambiguous")
		}
		selected[bar.Symbol] = selectedMark{bar: bar, sequence: sequence}
	}
	if len(selected) != len(positions) {
		return nil, paperdomain.Valuation{}, errPaperRunnerIncompleteMarks
	}

	closes := make(map[string]string, len(selected))
	for symbol, mark := range selected {
		closes[symbol] = mark.bar.Close
	}
	valuation, err := paperdomain.ValueAccount(startingCash, state, closes)
	if err != nil {
		return nil, paperdomain.Valuation{}, err
	}
	symbols := make([]string, 0, len(valuation.Positions))
	for symbol := range valuation.Positions {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	marks := make([]paperPerformanceMark, 0, len(symbols))
	for _, symbol := range symbols {
		mark, exists := selected[symbol]
		if !exists {
			return nil, paperdomain.Valuation{}, errors.New("paper performance marks do not match positions")
		}
		position := valuation.Positions[symbol]
		marks = append(marks, paperPerformanceMark{
			Symbol: symbol, Quantity: position.Quantity, ObservationID: mark.bar.ObservationID, ObservationSequence: mark.sequence,
			Close: mark.bar.Close, OpenCost: position.OpenCost, MarketValue: position.MarketValue, UnrealizedPnL: position.UnrealizedPnL,
		})
	}
	return marks, valuation, nil
}
