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

func (s *Service) runDuePaperPerformancePolicy(ctx context.Context, accountRef string) (*PaperScheduledRunResult, error) {
	if s == nil || s.db == nil || s.now == nil || !orderAlias(accountRef, "account") {
		return nil, errors.New("scheduled paper runner is not configured")
	}
	asOf, completed, err := s.prepareDuePaperPerformancePolicy(ctx, accountRef)
	if err != nil {
		return nil, err
	}
	if completed != nil {
		return completed, nil
	}
	point, err := s.evaluatePaperPerformance(ctx, accountRef, asOf)
	if err != nil {
		return nil, err
	}
	if result, found, err := loadScheduledPaperRunResult(ctx, s.db, accountRef, point.AsOf); err != nil {
		return nil, err
	} else if found {
		return result, nil
	}
	window, err := s.evaluatePaperStrategyPerformance(ctx, accountRef, point.StrategySelectionEventID, point.PerformanceID)
	if err != nil {
		if result, found, loadErr := loadScheduledPaperRunResult(ctx, s.db, accountRef, point.AsOf); loadErr != nil {
			return nil, loadErr
		} else if found {
			return result, nil
		}
		return nil, err
	}
	policy, err := s.applyPaperPerformancePolicy(ctx, accountRef, window.StrategySelectionEventID, window.StrategyPerformanceID)
	if err != nil {
		if result, found, loadErr := loadScheduledPaperRunResult(ctx, s.db, accountRef, point.AsOf); loadErr != nil {
			return nil, loadErr
		} else if found {
			return result, nil
		}
		return nil, err
	}
	return scheduledPaperRunResult(point.AsOf, *point, *window, *policy), nil
}

func (s *Service) prepareDuePaperPerformancePolicy(ctx context.Context, accountRef string) (string, *PaperScheduledRunResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()
	if _, err := provePaperPerformancePolicyRecovery(ctx, tx); err != nil {
		return "", nil, errors.Join(errors.New("scheduled paper runner recovery failed"), err)
	}
	asOf, err := latestAvailablePaperClose(ctx, tx, s.now().UTC())
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
	return asOf, nil, nil
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
		return "", errors.New("scheduled paper runner has no available local close")
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
