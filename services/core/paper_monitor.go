package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

type PaperMonitor struct {
	SchemaVersion      string              `json:"schema_version"`
	Mode               string              `json:"mode"`
	ObservedAt         string              `json:"observed_at"`
	StrategySelected   bool                `json:"strategy_selected"`
	SessionCount       int                 `json:"session_count"`
	PendingPolicyCount int                 `json:"pending_policy_count"`
	Runner             PaperMonitorRunner  `json:"runner"`
	LatestPolicy       *PaperMonitorPolicy `json:"latest_policy"`
}

type PaperMonitorRunner struct {
	State       string  `json:"state"`
	HeartbeatAt *string `json:"heartbeat_at"`
	ExpiresAt   *string `json:"expires_at"`
}

type PaperMonitorPolicy struct {
	AsOf                    string `json:"as_of"`
	RecordedAt              string `json:"recorded_at"`
	Decision                string `json:"decision"`
	ReasonCode              string `json:"reason_code"`
	MatchesCurrentSelection bool   `json:"matches_current_selection"`
}

func (s *Service) handlePaperMonitor(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "paper monitor accepts no query parameters"}})
		return
	}
	result, err := s.paperMonitor(r.Context())
	if err != nil {
		writeError(w, internalError(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) paperMonitor(ctx context.Context) (*PaperMonitor, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// openDB uses BEGIN IMMEDIATE: the snapshot is pinned before this clock read.
	// ReadOnly documents intent; sqlite3 ignores that flag and still takes the writer lock.
	now := s.now().UTC()
	// ponytail: prove local journals on demand; a verified projection needs measured read contention first.
	if _, err := provePaperRunnerLeaseRecovery(ctx, tx); err != nil {
		return nil, err
	}
	result := &PaperMonitor{SchemaVersion: "paper-monitor.v1", Mode: "paper_fixture_only", ObservedAt: now.Format(time.RFC3339Nano), Runner: PaperMonitorRunner{State: "unowned"}}
	strategy, err := replayStrategyRegistry(ctx, tx)
	if err != nil {
		return nil, err
	}
	result.StrategySelected = strategy.SelectedResultSHA256 != noStrategySelection
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM paper_accounting_sessions`).Scan(&result.SessionCount); err != nil {
		return nil, err
	}
	// Count durable unfinished phases, not missing input or executable jobs.
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM paper_performance_events p
		WHERE NOT EXISTS (
			SELECT 1 FROM paper_strategy_performance_events w
			JOIN paper_performance_policy_events e ON e.account_ref=w.account_ref
			AND e.strategy_selection_event_id=w.strategy_selection_event_id
			AND e.strategy_performance_id=w.strategy_performance_id
			WHERE w.account_ref=p.account_ref AND w.strategy_selection_event_id=p.strategy_selection_event_id
			AND w.latest_performance_id=p.performance_id
		)`).Scan(&result.PendingPolicyCount); err != nil {
		return nil, err
	}
	row, err := loadPaperRunnerLease(ctx, tx)
	if err != nil {
		return nil, err
	}
	lease := row.record
	if lease.OwnerID != "" {
		nowNS, err := paperRunnerTimeNS(now)
		if err != nil {
			return nil, err
		}
		heartbeat := time.Unix(0, lease.HeartbeatAtNS).UTC().Format(time.RFC3339Nano)
		expires := time.Unix(0, lease.ExpiresAtNS).UTC().Format(time.RFC3339Nano)
		result.Runner = PaperMonitorRunner{State: "lease_recorded", HeartbeatAt: &heartbeat, ExpiresAt: &expires}
		// Ordered observations, not an authorization or process-liveness test.
		switch {
		case nowNS < lease.HeartbeatAtNS:
			result.Runner.State = "clock_regressed"
		case nowNS >= lease.ExpiresAtNS:
			result.Runner.State = "expired"
		case lease.StrategySelectionEventID != strategy.CurrentEventID || lease.SelectedResultSHA256 != strategy.SelectedResultSHA256:
			result.Runner.State = "selection_changed"
		}
	}
	var policyID string
	err = tx.QueryRowContext(ctx, `SELECT policy_event_id FROM paper_performance_policy_events ORDER BY sequence DESC LIMIT 1`).Scan(&policyID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		policy, err := loadPaperPerformancePolicyByID(ctx, tx, policyID)
		if err != nil {
			return nil, err
		}
		window, _, err := loadPaperStrategyPerformanceByID(ctx, tx, policy.StrategyPerformanceID)
		if err != nil {
			return nil, err
		}
		result.LatestPolicy = &PaperMonitorPolicy{AsOf: window.LatestAsOf, RecordedAt: policy.RecordedAt, Decision: policy.Decision, ReasonCode: policy.ReasonCode,
			MatchesCurrentSelection: policy.StrategySelectionEventID == strategy.CurrentEventID && policy.SelectedStrategyResultRef == strategy.SelectedResultSHA256}
	}
	return result, tx.Commit()
}
