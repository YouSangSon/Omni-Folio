package main

import (
	"context"
	"errors"
	"time"
)

const (
	paperRunnerLoopHeartbeatInterval  = 10 * time.Second
	paperRunnerLoopDueEveryHeartbeats = 6
	paperRunnerLoopReleaseTimeout     = 5 * time.Second
)

func (s *Service) runPaperPerformanceLoop(ctx context.Context, accountRef string) error {
	return s.runPaperPerformanceLoopWithWait(ctx, accountRef, waitPaperRunnerLoop)
}

func (s *Service) runPaperPerformanceLoopWithWait(ctx context.Context, accountRef string, wait func(context.Context, time.Duration) error) (resultErr error) {
	if ctx == nil || wait == nil {
		return errors.New("paper runner loop is not configured")
	}
	claim, err := s.acquirePaperRunnerLease(ctx, accountRef)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), paperRunnerLoopReleaseTimeout)
		defer cancel()
		releaseErr := s.releasePaperRunnerLease(cleanupCtx, claim)
		if errors.Is(resultErr, context.Canceled) {
			resultErr = releaseErr
		} else if releaseErr != nil {
			resultErr = errors.Join(resultErr, releaseErr)
		}
	}()

	if stop, err := s.runPaperPerformanceLoopDue(ctx, accountRef, claim); err != nil {
		return err
	} else if stop {
		return nil
	}

	completedHeartbeats := 0
	for {
		if err := wait(ctx, paperRunnerLoopHeartbeatInterval); err != nil {
			return err
		}
		claim, err = s.heartbeatPaperRunnerLease(ctx, claim)
		if err != nil {
			return err
		}
		completedHeartbeats++
		if completedHeartbeats%paperRunnerLoopDueEveryHeartbeats != 0 {
			continue
		}
		if stop, err := s.runPaperPerformanceLoopDue(ctx, accountRef, claim); err != nil {
			return err
		} else if stop {
			return nil
		}
	}
}

func (s *Service) runPaperPerformanceLoopDue(ctx context.Context, accountRef string, claim *paperRunnerClaim) (bool, error) {
	result, err := s.runDuePaperPerformancePolicyWithClaim(ctx, accountRef, claim)
	if err != nil {
		if errors.Is(err, errPaperRunnerNoAvailableClose) || errors.Is(err, errPaperRunnerIncompleteMarks) || errors.Is(err, errPaperRunnerPriorSelection) {
			return false, nil
		}
		return false, err
	}
	return result != nil && result.Decision == "HALT_AND_ROLLBACK", nil
}

func waitPaperRunnerLoop(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
