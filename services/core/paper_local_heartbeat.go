package main

import (
	"context"
	"errors"
	"math"
	"time"
)

// Renew only an uninterrupted, still-owned paper run. Never arm or reacquire an
// expired lease here. Publish both new tokens only after the shared commit.
func (s *Service) heartbeatLocalPaperExecution(ctx context.Context, claim *paperRunnerClaim, fence int64) (*paperRunnerClaim, *ExecutionAuthorityState, error) {
	if s == nil || s.db == nil || s.now == nil || claim == nil || s.executionOwner == "" || fence <= 0 || fence >= math.MaxInt64-1 {
		return nil, nil, errors.New("local paper heartbeat is not configured")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	if _, err := provePaperRunnerLeaseRecovery(ctx, tx); err != nil {
		return nil, nil, err
	}
	now := s.now().UTC()
	if err := validatePaperRunnerLeaseTx(ctx, tx, claim, claim.AccountRef, s.paperRunnerOwner, now); err != nil {
		return nil, nil, err
	}
	session, found, err := loadPaperAccountingSession(ctx, tx, claim.AccountRef)
	if err != nil {
		return nil, nil, err
	}
	policy, err := loadCurrentStrategyExecutionPolicy(ctx, tx, claim.SelectedResultSHA256, claim.StrategySelectionEventID)
	if err != nil || !found || session.ExecutionPolicySHA256 != policy.SHA256 {
		return nil, nil, errors.New("local paper heartbeat session changed")
	}
	if err := requireIsolatedPaperAccount(ctx, tx, claim.AccountRef); err != nil {
		return nil, nil, err
	}
	current, err := s.requireCurrentSyntheticExecutionLease(ctx, tx, claim.AccountRef, fence, now)
	if err != nil {
		return nil, nil, err
	}
	previous, err := loadExecutionAuthorityRecordByID(ctx, tx, current.EventID)
	if err != nil {
		return nil, nil, err
	}
	recordedAt, _ := canonicalUTCTime(previous.RecordedAt)
	if now.Before(recordedAt) {
		return nil, nil, errors.New("local paper heartbeat clock regressed")
	}
	next := executionAuthorityRecord{
		EventID: s.id("execution_authority"), AccountRef: claim.AccountRef, Armed: true, LeaseOwner: s.executionOwner,
		FencingToken: fence + 1, LeaseExpiresAt: now.Add(syntheticExecutionLeaseTTL).Format(time.RFC3339Nano),
		ReasonCode: "lease_acquired", RecordedAt: now.Format(time.RFC3339Nano),
	}
	if err := validateExecutionAuthorityRecord(next, &previous); err != nil {
		return nil, nil, err
	}
	// ponytail: immutable renewals grow replay cost; measure sustained history
	// before promoting an always-on consumer, not a second mutable authority.
	if err := insertExecutionAuthorityRecord(ctx, tx, next); err != nil {
		return nil, nil, err
	}
	nextClaim, err := renewPaperRunnerLeaseTx(ctx, tx, claim, s.paperRunnerOwner, now)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return nextClaim, authorityState(next), nil
}
