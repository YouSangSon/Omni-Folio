package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"time"
)

type LocalPaperStepResult struct {
	Mode     string                   `json:"mode"`
	Snapshot *PaperSnapshotImport     `json:"snapshot"`
	Order    *OrderState              `json:"order,omitempty"`
	Policy   *PaperScheduledRunResult `json:"policy"`
}

// Explicit one-shot fixture execution; never call this from a scheduler because
// it includes the caller's explicit arm. Each journaled phase is retryable.
func (s *Service) executeLocalPaper(ctx context.Context, account, selection string, proposalRaw, barsRaw, researchRaw []byte) (result *LocalPaperStepResult, resultErr error) {
	run := &localPaperRun{service: s, account: account, selection: selection}
	defer func() { resultErr = errors.Join(resultErr, ctx.Err(), run.close()) }()
	return run.step(ctx, proposalRaw, barsRaw, researchRaw)
}

// A single invocation owns this state. Only the first validated step may arm;
// both one-shot and stream consumers use the same preparation/execution path.
type localPaperRun struct {
	service            *Service
	account, selection string
	claim              *paperRunnerClaim
	lease              *ExecutionAuthorityState
}

func (r *localPaperRun) close() error {
	var err error
	if r.lease != nil {
		ctx, cancel := context.WithTimeout(context.Background(), paperRunnerLoopReleaseTimeout)
		err = r.service.haltOwnedSyntheticExecutionLease(ctx, r.account, r.lease.FencingToken)
		cancel()
	}
	if r.claim != nil {
		ctx, cancel := context.WithTimeout(context.Background(), paperRunnerLoopReleaseTimeout)
		err = errors.Join(err, r.service.releasePaperRunnerLease(ctx, r.claim))
		cancel()
	}
	return err
}

func (r *localPaperRun) refresh(ctx context.Context) error {
	if r.lease == nil {
		return nil
	}
	// Policy stages can refresh the global claim independently. Schedule
	// against the execution expiry, never that mutable global heartbeat.
	expires, ok := canonicalUTCTime(r.lease.LeaseExpiresAt)
	if !ok {
		return errors.New("local paper execution expiry is invalid")
	}
	if r.service.now().Before(expires.Add(-syntheticExecutionLeaseTTL + paperRunnerLoopHeartbeatInterval)) {
		return nil
	}
	claim, lease, err := r.service.heartbeatLocalPaperExecution(ctx, r.claim, r.lease.FencingToken)
	if err == nil {
		r.claim, r.lease = claim, lease
	}
	return err
}

func (r *localPaperRun) step(ctx context.Context, proposalRaw, barsRaw, researchRaw []byte) (*LocalPaperStepResult, error) {
	s, account, selection := r.service, r.account, r.selection
	proposal, err := decodePaperProposal(proposalRaw)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(barsRaw)
	if hex.EncodeToString(hash[:]) != stringField(proposal, "input_sha256") {
		return nil, errors.New("paper proposal CSV hash differs")
	}
	if r.claim == nil {
		claim, err := s.acquirePaperRunnerLease(ctx, account)
		if err != nil {
			return nil, err
		}
		r.claim = claim
	}
	if r.lease != nil {
		if err := r.refresh(ctx); err != nil {
			return nil, err
		}
		if _, err := s.requireCurrentSyntheticExecutionLease(ctx, s.db, account, r.lease.FencingToken, s.now()); err != nil {
			return nil, err
		}
	}
	if selection != r.claim.StrategySelectionEventID || stringField(proposal, "strategy_result_sha256") != r.claim.SelectedResultSHA256 {
		return nil, errors.New("local paper proposal selection changed")
	}
	var slow int
	var researchSHA string
	if err := s.db.QueryRowContext(ctx, `SELECT json_extract(artifact_json,'$.manifest.strategy.parameters.slow_window'),json_extract(artifact_json,'$.input_sha256') FROM strategy_research_evidence WHERE result_sha256=?`, r.claim.SelectedResultSHA256).Scan(&slow, &researchSHA); err != nil {
		return nil, err
	}
	if err := validatePaperResearchInput(researchRaw, researchSHA, stringField(proposal, "symbol"), stringField(proposal, "data_as_of")); err != nil {
		return nil, err
	}
	snapshot, err := s.importPaperSnapshot(ctx, barsRaw)
	if err != nil {
		return nil, err
	}
	if slow < 2 || snapshot.Bars <= slow {
		return nil, errors.New("paper snapshot lacks the complete strategy warmup window")
	}
	if _, _, err := s.processPaperProposal(ctx, account, selection, proposalRaw, snapshot.SignalBarObservationID, 0, false, r.claim); err != nil {
		return nil, err
	}
	// Preparation can age the global claim before execution acquires its own
	// lease. Align their lifetimes, retaining the old tuple if renewal fails.
	if r.lease == nil {
		preparedClaim, err := s.heartbeatPaperRunnerLease(ctx, r.claim)
		if err != nil {
			return nil, err
		}
		r.claim = preparedClaim
		lease, err := s.startLocalPaperExecutionWithClaim(ctx, account, selection, r.claim)
		if err != nil {
			return nil, err
		}
		r.lease = lease
	}
	ids, err := s.openLocalPaperOrderIDs(ctx, account)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		state, err := s.loadOrderState(ctx, id)
		if err != nil {
			return nil, err
		}
		// Each committed fill consumes a distinct bar and increases the finite
		// order quantity. No progress ends catch-up; every write rechecks leases.
		// ponytail: replay per fill is quadratic in history; batch only after
		// profiling, preserving per-fill accounting and authority validation.
		for state.Status == "OPEN" || state.Status == "PARTIALLY_FILLED" {
			if err := r.refresh(ctx); err != nil {
				return nil, err
			}
			filled := state.FilledQuantity
			state, err = s.runPaperOrderWithClaim(ctx, id, r.lease.FencingToken, r.claim)
			if err != nil {
				return nil, err
			}
			if state.FilledQuantity == filled {
				break
			}
		}
	}
	if err := r.refresh(ctx); err != nil {
		return nil, err
	}
	policy, err := s.runDuePaperPerformancePolicyWithClaim(ctx, account, r.claim)
	if err != nil {
		return nil, err
	}
	result := &LocalPaperStepResult{Mode: "paper_fixture_only", Snapshot: snapshot, Policy: policy}
	if policy.Decision == "HALT_AND_ROLLBACK" {
		return result, nil
	}
	if err := r.refresh(ctx); err != nil {
		return nil, err
	}
	_, order, err := s.processPaperProposal(ctx, account, selection, proposalRaw, snapshot.SignalBarObservationID, r.lease.FencingToken, true, r.claim)
	if err != nil {
		return nil, err
	}
	result.Order = order
	return result, nil
}

func (s *Service) openLocalPaperOrderIDs(ctx context.Context, account string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT order_id FROM order_idempotency WHERE account_ref=? AND mode='paper' ORDER BY recorded_at,order_id`, account)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var active []string
	for _, id := range ids {
		intent, err := loadOrderIntentFrom(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		if intent.SignalSchemaVersion != capitalizedPaperSignalSchema || intent.OrderType != "PAPER_MARKET" {
			return nil, errors.New("local paper cannot execute legacy orders")
		}
		state, err := s.loadOrderState(ctx, id)
		if err != nil {
			return nil, err
		}
		if state.PendingAction != "" {
			return nil, errors.New("local paper has an unresolved order action")
		}
		switch state.Status {
		case "OPEN", "PARTIALLY_FILLED":
			active = append(active, id)
		case "FILLED", "CANCELED", "REJECTED":
		default:
			return nil, errors.New("local paper has an unexecutable order")
		}
	}
	return active, nil
}

// Only the explicit local paper command calls this; a scheduler must not rearm.
func (s *Service) startLocalPaperExecution(ctx context.Context, accountRef, selectionEventID string) (*ExecutionAuthorityState, error) {
	return s.startLocalPaperExecutionWithClaim(ctx, accountRef, selectionEventID, nil)
}

func (s *Service) startLocalPaperExecutionWithClaim(ctx context.Context, accountRef, selectionEventID string, claim *paperRunnerClaim) (*ExecutionAuthorityState, error) {
	if s == nil || s.db == nil || s.now == nil || s.executionOwner == "" || !orderAlias(accountRef, "account") || !safeOrderID(selectionEventID) {
		return nil, errors.New("local paper execution is not configured")
	}
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
	if _, err := provePaperPerformancePolicyRecovery(ctx, tx); err != nil {
		return nil, err
	}
	_, currentEvent, result, err := currentPaperPerformanceSelection(ctx, tx)
	if err != nil || currentEvent != selectionEventID || result == noStrategySelection {
		return nil, errors.New("local paper selection changed")
	}
	session, found, err := loadPaperAccountingSession(ctx, tx, accountRef)
	if err != nil {
		return nil, err
	}
	policy, err := loadCurrentStrategyExecutionPolicy(ctx, tx, result, selectionEventID)
	if err != nil || !found || session.ExecutionPolicySHA256 != policy.SHA256 {
		return nil, errors.New("local paper requires a matching initialized session")
	}
	if err := requireIsolatedPaperAccount(ctx, tx, accountRef); err != nil {
		return nil, err
	}
	current, err := loadExecutionAuthoritySnapshot(ctx, tx, accountRef)
	if err != nil {
		return nil, err
	}
	if current.FencingToken >= math.MaxInt64-1 {
		return nil, errors.New("local paper execution fence overflow")
	}
	if !current.Armed {
		if err := insertExecutionAuthorityRecord(ctx, tx, executionAuthorityRecord{
			EventID: s.id("execution_authority"), AccountRef: accountRef, Armed: true,
			FencingToken: current.FencingToken + 1, ReasonCode: "manual_arm", RecordedAt: s.now().UTC().Format(time.RFC3339Nano),
		}); err != nil {
			return nil, err
		}
	}
	lease, err := s.acquireSyntheticExecutionLeaseTx(ctx, tx, accountRef)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return lease, nil
}

// Revocation may clean an expired lease, but never a newer or foreign owner.
func (s *Service) haltOwnedSyntheticExecutionLease(ctx context.Context, accountRef string, fence int64) error {
	if s == nil || s.db == nil || s.now == nil || s.executionOwner == "" || !orderAlias(accountRef, "account") || fence <= 0 {
		return errors.New("local paper cleanup is not configured")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := provePaperPerformancePolicyRecovery(ctx, tx); err != nil {
		return err
	}
	current, err := loadExecutionAuthoritySnapshot(ctx, tx, accountRef)
	if err != nil {
		return err
	}
	if !current.Armed {
		return nil
	}
	if current.LeaseOwner != s.executionOwner || current.FencingToken != fence || fence == math.MaxInt64 {
		return errors.New("local paper cleanup does not own the current lease")
	}
	if _, err := s.haltSyntheticExecutionTx(ctx, tx, s.now().UTC(), "manual_halt", "", []string{accountRef}, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func requireIsolatedPaperAccount(ctx context.Context, q orderQuerier, account string) error {
	var mixed bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM order_idempotency WHERE account_ref=? AND mode<>'paper')`, account).Scan(&mixed); err != nil {
		return err
	}
	if mixed {
		return errors.New("local paper requires an isolated paper account")
	}
	return nil
}
