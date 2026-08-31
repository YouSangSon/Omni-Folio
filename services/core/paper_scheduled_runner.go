package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type PaperScheduledRunResult struct {
	AccountRef               string `json:"account_ref"`
	AsOf                     string `json:"as_of"`
	PerformanceID            string `json:"performance_id"`
	StrategyPerformanceID    string `json:"strategy_performance_id"`
	PolicyEventID            string `json:"policy_event_id"`
	Decision                 string `json:"decision"`
	ReasonCode               string `json:"reason_code"`
	AutomaticHaltCount       int    `json:"automatic_halt_count"`
	RollbackSelectionEventID string `json:"rollback_selection_event_id,omitempty"`
}

var (
	errPaperRunnerNoAvailableClose = errors.New("scheduled paper runner has no available local close")
	errPaperRunnerIncompleteMarks  = errors.New("paper performance marks are incomplete")
	errPaperRunnerPriorSelection   = errors.New("scheduled paper runner close belongs to a prior strategy selection")
)

const (
	paperScheduledRunnerContentionTimeout = 5 * time.Second
	paperScheduledRunnerContentionPoll    = 5 * time.Millisecond
	paperScheduledRunnerReleaseTimeout    = 5 * time.Second
	paperRunnerStageTimeout               = 20 * time.Second
)

func (s *Service) runDuePaperPerformancePolicy(ctx context.Context, accountRef string) (*PaperScheduledRunResult, error) {
	if s == nil || s.db == nil || s.now == nil || !orderAlias(accountRef, "account") {
		return nil, errors.New("scheduled paper runner is not configured")
	}
	_, completed, err := s.prepareDuePaperPerformancePolicy(ctx, accountRef)
	if err != nil {
		return nil, err
	}
	if completed != nil {
		return completed, nil
	}
	claim, err := s.acquirePaperRunnerLease(ctx, accountRef)
	if errors.Is(err, errPaperRunnerLeaseHeld) {
		claim, completed, err = s.waitForPaperRunnerLeaseOrCompletion(ctx, accountRef)
	}
	if err != nil {
		_, completed, prepareErr := s.prepareDuePaperPerformancePolicy(ctx, accountRef)
		if prepareErr != nil {
			return nil, errors.Join(err, prepareErr)
		}
		if completed != nil {
			return completed, nil
		}
		return nil, err
	}
	if completed != nil {
		return completed, nil
	}
	result, runErr := s.runDuePaperPerformancePolicyWithClaim(ctx, accountRef, claim)
	releaseCtx, cancel := context.WithTimeout(context.Background(), paperScheduledRunnerReleaseTimeout)
	defer cancel()
	releaseErr := s.releasePaperRunnerLease(releaseCtx, claim)
	if runErr != nil || releaseErr != nil {
		return nil, errors.Join(runErr, releaseErr)
	}
	return result, nil
}

func (s *Service) prepareDuePaperPerformancePolicy(ctx context.Context, accountRef string) (string, *PaperScheduledRunResult, error) {
	return s.prepareDuePaperPerformancePolicyWithClaim(ctx, accountRef, nil)
}

func (s *Service) prepareDuePaperPerformancePolicyWithClaim(ctx context.Context, accountRef string, claim *paperRunnerClaim) (string, *PaperScheduledRunResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()
	if claim != nil {
		if err := validatePaperRunnerLeaseTx(ctx, tx, claim, accountRef, s.paperRunnerOwner, s.now()); err != nil {
			return "", nil, err
		}
	}
	if _, err := provePaperRunnerLeaseRecovery(ctx, tx); err != nil {
		return "", nil, errors.Join(errors.New("scheduled paper runner lease recovery failed"), err)
	}
	now := s.now().UTC()
	asOf, err := latestAvailablePaperClose(ctx, tx, now)
	if err != nil {
		return "", nil, err
	}
	if result, found, err := loadScheduledPaperRunResult(ctx, tx, accountRef, asOf); err != nil {
		return "", nil, err
	} else if found {
		return asOf, result, nil
	}
	if _, _, selectedResult, err := currentPaperPerformanceSelection(ctx, tx); err != nil {
		return "", nil, err
	} else if selectedResult == noStrategySelection {
		return "", nil, errors.New("current paper strategy is missing")
	}
	session, found, err := loadPaperAccountingSession(ctx, tx, accountRef)
	if err != nil {
		return "", nil, err
	}
	if !found {
		return "", nil, errors.New("paper performance accounting session is missing")
	}
	if err := validatePaperPerformanceWindow(*session, asOf, now.Format(canonicalPaperTimeLayout)); err != nil {
		return "", nil, errors.Join(errPaperRunnerNoAvailableClose, err)
	}
	return asOf, nil, nil
}

func (s *Service) runDuePaperPerformancePolicyWithClaim(ctx context.Context, accountRef string, claim *paperRunnerClaim) (*PaperScheduledRunResult, error) {
	if s == nil || s.db == nil || s.now == nil || !orderAlias(accountRef, "account") || claim == nil {
		return nil, errors.New("scheduled paper runner claim is not configured")
	}
	asOf, completed, err := s.prepareDuePaperPerformancePolicyWithClaim(ctx, accountRef, claim)
	if err != nil || completed != nil {
		return completed, err
	}
	point, err := s.evaluatePaperPerformanceWithClaim(ctx, accountRef, asOf, claim)
	if err != nil {
		return nil, err
	}
	window, err := s.evaluatePaperStrategyPerformanceWithClaim(ctx, accountRef, point.StrategySelectionEventID, point.PerformanceID, claim)
	if err != nil {
		return nil, err
	}
	policy, err := s.applyPaperPerformancePolicyWithClaim(ctx, accountRef, window.StrategySelectionEventID, window.StrategyPerformanceID, claim)
	if err != nil {
		return nil, err
	}
	return scheduledPaperRunResult(point.AsOf, *point, *window, *policy), nil
}

func (s *Service) waitForPaperRunnerLeaseOrCompletion(ctx context.Context, accountRef string) (*paperRunnerClaim, *PaperScheduledRunResult, error) {
	waitCtx, cancel := context.WithTimeout(ctx, paperScheduledRunnerContentionTimeout)
	defer cancel()
	for {
		_, completed, err := s.prepareDuePaperPerformancePolicy(waitCtx, accountRef)
		if err != nil {
			return nil, nil, err
		}
		if completed != nil {
			return nil, completed, nil
		}
		timer := time.NewTimer(paperScheduledRunnerContentionPoll)
		select {
		case <-waitCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, nil, waitCtx.Err()
		case <-timer.C:
		}
		claim, err := s.acquirePaperRunnerLease(waitCtx, accountRef)
		if err == nil {
			return claim, nil, nil
		}
		if !errors.Is(err, errPaperRunnerLeaseHeld) {
			return nil, nil, err
		}
	}
}

func latestAvailablePaperClose(ctx context.Context, q orderQuerier, now time.Time) (string, error) {
	recordedAt := now.UTC().Format(canonicalPaperTimeLayout)
	var asOf string
	err := q.QueryRowContext(ctx, `SELECT close_at FROM paper_market_bar_observations
		WHERE source='paper_fixture' AND venue='KRX' AND currency='KRW' AND interval='1d'
		  AND timezone='Asia/Seoul' AND price_adjustment='unspecified'
		  AND source_available_at<=? AND fetched_at<=? AND recorded_at<=?
		GROUP BY close_at ORDER BY close_at DESC LIMIT 1`, recordedAt, recordedAt, recordedAt).Scan(&asOf)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errPaperRunnerNoAvailableClose
	}
	if err != nil {
		return "", err
	}
	if !canonicalPaperTimes(asOf) {
		return "", errors.New("scheduled paper runner close time is invalid")
	}
	return asOf, nil
}

func loadScheduledPaperRunResult(ctx context.Context, q orderQuerier, accountRef, asOf string) (*PaperScheduledRunResult, bool, error) {
	point, found, err := loadPaperPerformanceByKey(ctx, q, accountRef, asOf)
	if err != nil || !found {
		return nil, false, err
	}
	window, found, err := loadPaperStrategyPerformanceByKey(ctx, q, accountRef, point.StrategySelectionEventID, point.PerformanceID)
	if err != nil || !found {
		return nil, false, err
	}
	policy, found, err := loadPaperPerformancePolicyByKey(ctx, q, accountRef, window.StrategySelectionEventID, window.StrategyPerformanceID)
	if err != nil || !found {
		return nil, false, err
	}
	return scheduledPaperRunResult(asOf, *point, *window, *policy), true, nil
}

func scheduledPaperRunResult(asOf string, point PaperPerformanceEvent, window PaperStrategyPerformanceEvent, policy PaperPerformancePolicyEvent) *PaperScheduledRunResult {
	return &PaperScheduledRunResult{
		AccountRef:               policy.AccountRef,
		AsOf:                     asOf,
		PerformanceID:            point.PerformanceID,
		StrategyPerformanceID:    window.StrategyPerformanceID,
		PolicyEventID:            policy.PolicyEventID,
		Decision:                 policy.Decision,
		ReasonCode:               policy.ReasonCode,
		AutomaticHaltCount:       policy.AutomaticHaltCount,
		RollbackSelectionEventID: policy.RollbackSelectionEventID,
	}
}
