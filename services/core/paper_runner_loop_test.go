package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestG38F2PaperRunnerLoopRunsDueBeforeItsFirstIdleHeartbeat(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waits := 0
	err := svc.runPaperPerformanceLoopWithWait(ctx, k2aAccountRef, func(waitCtx context.Context, delay time.Duration) error {
		waits++
		if delay != 10*time.Second {
			t.Fatalf("idle delay=%s, want heartbeat interval", delay)
		}
		if got := g38EJournalCounts(t, svc).Policy; got != 1 {
			t.Fatalf("idle heartbeat started before immediate C3/D/E completion: policy rows=%d", got)
		}
		cancel()
		<-waitCtx.Done()
		return waitCtx.Err()
	})
	if !paperRunnerLoopCanceled(err) || waits != 1 {
		t.Fatalf("loop err=%v waits=%d", err, waits)
	}
}

func TestG38F2PaperRunnerLoopPollsNotDueWorkAfterSerialHeartbeats(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	clock := mustTime("2026-01-12T06:32:00Z")
	svc.now = func() time.Time { return clock }
	recordG38C3MarkBar(t, svc, "005930", "g38f2-completion-cadence", "2026-01-12T06:30:00.000000000Z", "100")
	clock = mustTime("2026-01-12T06:31:00Z")
	svc.now = func() time.Time { return clock }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waits := 0
	err := svc.runPaperPerformanceLoopWithWait(ctx, k2aAccountRef, func(waitCtx context.Context, delay time.Duration) error {
		waits++
		if delay != 10*time.Second {
			t.Fatalf("wait %d delay=%s, want only serial heartbeat waits", waits, delay)
		}
		if waits <= 6 && g38EJournalCounts(t, svc).Policy != 0 {
			t.Fatalf("due work overlapped heartbeat %d before the completion-based 60s poll", waits)
		}
		clock = clock.Add(delay)
		if waits == 7 {
			if got := g38EJournalCounts(t, svc).Policy; got != 1 {
				t.Fatalf("due poll did not complete once after six heartbeats: policy rows=%d", got)
			}
			cancel()
			<-waitCtx.Done()
			return waitCtx.Err()
		}
		return nil
	})
	if !paperRunnerLoopCanceled(err) || waits != 7 {
		t.Fatalf("loop err=%v waits=%d", err, waits)
	}
}

func TestG38F2PaperRunnerLoopWaitsForNoCloseOrIncompleteMarks(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, svc *Service)
	}{
		{
			name: "no close",
			setup: func(t *testing.T, svc *Service) {
				svc.now = func() time.Time { return mustTime("2026-01-11T06:30:00Z") }
			},
		},
		{
			name: "incomplete marks",
			setup: func(t *testing.T, svc *Service) {
				svc.now = func() time.Time { return mustTime("2026-01-12T07:00:00Z") }
				recordG38C3MarkBar(t, svc, "000660", "g38f2-loop-wrong-symbol", "2026-01-12T06:30:00.000000000Z", "100")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := g38EPerformanceWindow(t, []string{"100"})
			test.setup(t, svc)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			waits := 0
			err := svc.runPaperPerformanceLoopWithWait(ctx, k2aAccountRef, func(waitCtx context.Context, delay time.Duration) error {
				waits++
				if delay != 10*time.Second || g38EJournalCounts(t, svc).Policy != 0 {
					t.Fatalf("not-due state was not idle: delay=%s policy=%d", delay, g38EJournalCounts(t, svc).Policy)
				}
				cancel()
				<-waitCtx.Done()
				return waitCtx.Err()
			})
			if !paperRunnerLoopCanceled(err) || waits != 1 {
				t.Fatalf("loop err=%v waits=%d", err, waits)
			}
		})
	}
}

func TestG38F2PaperRunnerLoopStopsBeforeAnyIdleRetryOnFatalCorruption(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	if _, err := svc.db.Exec(`DROP TRIGGER paper_market_bar_observations_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE paper_market_bar_observations SET record_sha256=? WHERE sequence=1`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	waits := 0
	err := svc.runPaperPerformanceLoopWithWait(context.Background(), k2aAccountRef, func(context.Context, time.Duration) error {
		waits++
		return nil
	})
	if err == nil || waits != 0 {
		t.Fatalf("fatal corruption retried as idle: err=%v waits=%d", err, waits)
	}
}

func TestG38F2PaperRunnerLoopCancellationReleasesOnlyItsExactClaim(t *testing.T) {
	primary, _ := g38EPerformanceWindow(t, []string{"100"})
	primaryClock := mustTime("2026-01-11T06:30:00Z")
	primary.now = func() time.Time { return primaryClock }
	secondary := secondG38C2Service(t, primary, primaryClock.Add(31*time.Second))
	primaryCtx, cancelPrimary := context.WithCancel(context.Background())
	defer cancelPrimary()
	secondaryCtx, cancelSecondary := context.WithCancel(context.Background())
	defer cancelSecondary()

	primaryWaiting := make(chan struct{})
	primaryDone := make(chan error, 1)
	go func() {
		primaryDone <- primary.runPaperPerformanceLoopWithWait(primaryCtx, k2aAccountRef, func(waitCtx context.Context, delay time.Duration) error {
			if delay != 10*time.Second {
				return errors.New("unexpected primary wait delay")
			}
			close(primaryWaiting)
			<-waitCtx.Done()
			return waitCtx.Err()
		})
	}()
	select {
	case <-primaryWaiting:
	case <-time.After(time.Second):
		t.Fatal("primary runner did not reach its idle heartbeat")
	}
	oldOwner, oldFence := paperRunnerLoopActiveLease(t, primary)
	if oldOwner == "" || oldFence <= 0 {
		t.Fatalf("primary active lease owner=%q fence=%d", oldOwner, oldFence)
	}

	secondaryWaiting := make(chan struct{})
	secondaryDone := make(chan error, 1)
	go func() {
		secondaryDone <- secondary.runPaperPerformanceLoopWithWait(secondaryCtx, k2aAccountRef, func(waitCtx context.Context, delay time.Duration) error {
			if delay != 10*time.Second {
				return errors.New("unexpected secondary wait delay")
			}
			close(secondaryWaiting)
			<-waitCtx.Done()
			return waitCtx.Err()
		})
	}()
	select {
	case <-secondaryWaiting:
	case <-time.After(time.Second):
		t.Fatal("stale takeover did not reach its idle heartbeat")
	}
	newOwner, newFence := paperRunnerLoopActiveLease(t, primary)
	if newOwner == oldOwner || newFence <= oldFence {
		t.Fatalf("takeover did not fence primary old=(%q,%d) new=(%q,%d)", oldOwner, oldFence, newOwner, newFence)
	}

	cancelPrimary()
	select {
	case err := <-primaryDone:
		if err == nil || !strings.Contains(err.Error(), "paper runner lease was lost") {
			t.Fatalf("stale primary did not report its lost claim: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("primary cancellation did not finish")
	}
	ownerAfterLoserRelease, fenceAfterLoserRelease := paperRunnerLoopActiveLease(t, primary)
	if ownerAfterLoserRelease != newOwner || fenceAfterLoserRelease != newFence {
		t.Fatalf("loser released or changed winner lease before=(%q,%d) after=(%q,%d)",
			newOwner, newFence, ownerAfterLoserRelease, fenceAfterLoserRelease)
	}

	cancelSecondary()
	select {
	case err := <-secondaryDone:
		if !paperRunnerLoopCanceled(err) {
			t.Fatalf("secondary cancellation err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("secondary cancellation did not finish")
	}
	if owner, fence := paperRunnerLoopActiveLease(t, primary); owner != "" || fence != newFence {
		t.Fatalf("winner did not conditionally release its exact claim: owner=%q fence=%d", owner, fence)
	}
}

func TestG38F2PaperRunnerLoopCancellationWaitsForWriterThenReleasesLease(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	svc.now = func() time.Time { return mustTime("2026-01-11T06:30:00Z") }
	var dbPath string
	if err := svc.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&dbPath); err != nil {
		t.Fatal(err)
	}
	writer, err := openExistingDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	locked := make(chan *sql.Tx, 1)
	done := make(chan error, 1)
	go func() {
		done <- svc.runPaperPerformanceLoopWithWait(ctx, k2aAccountRef, func(waitCtx context.Context, _ time.Duration) error {
			tx, err := writer.BeginTx(context.Background(), nil)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE paper_runner_leases SET owner_id=owner_id WHERE scope=?`, paperRunnerLeaseScope); err != nil {
				_ = tx.Rollback()
				return err
			}
			locked <- tx
			cancel()
			<-waitCtx.Done()
			return waitCtx.Err()
		})
	}()

	var tx *sql.Tx
	select {
	case tx = <-locked:
	case err := <-done:
		t.Fatalf("runner stopped before writer contention: %v", err)
	case <-time.After(time.Second):
		t.Fatal("writer lock was not acquired")
	}
	select {
	case err := <-done:
		t.Fatalf("runner bypassed the contended release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !paperRunnerLoopCanceled(err) {
			t.Fatalf("cancellation err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not release after writer contention cleared")
	}
	if owner, fence := paperRunnerLoopActiveLease(t, svc); owner != "" || fence <= 0 {
		t.Fatalf("writer-contention cancellation left lease owner=%q fence=%d", owner, fence)
	}
}

func TestG38F2PaperRunnerLoopCancellationDoesNotHideReleaseFailure(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	svc.now = func() time.Time { return mustTime("2026-01-11T06:30:00Z") }
	ctx, cancel := context.WithCancel(context.Background())
	err := svc.runPaperPerformanceLoopWithWait(ctx, k2aAccountRef, func(waitCtx context.Context, _ time.Duration) error {
		if _, err := svc.db.Exec(`DROP TRIGGER paper_runner_leases_no_delete; DROP TABLE paper_runner_leases`); err != nil {
			return err
		}
		cancel()
		<-waitCtx.Done()
		return waitCtx.Err()
	})
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation hid release failure: %v", err)
	}
}

func TestG38F2PaperRunnerLoopStopsAfterAutomaticRollback(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100", "10"})
	waits := 0
	err := svc.runPaperPerformanceLoopWithWait(context.Background(), k2aAccountRef, func(context.Context, time.Duration) error {
		waits++
		return nil
	})
	if err != nil || waits != 0 {
		t.Fatalf("automatic rollback should stop the loop without idle retry: err=%v waits=%d", err, waits)
	}
	state, err := replayStrategyRegistry(context.Background(), svc.db)
	if err != nil || state.SelectedResultSHA256 != noStrategySelection {
		t.Fatalf("automatic rollback selection=%+v err=%v", state, err)
	}
	if owner, _ := paperRunnerLoopActiveLease(t, svc); owner != "" {
		t.Fatalf("automatic rollback left an active runner lease owner=%q", owner)
	}
}

func paperRunnerLoopActiveLease(t testing.TB, svc *Service) (string, int64) {
	t.Helper()
	var owner string
	var fence int64
	if err := svc.db.QueryRow(`SELECT COALESCE(owner_id,''),fencing_token
		FROM paper_runner_leases WHERE scope='paper_strategy_selection'`).Scan(&owner, &fence); err != nil {
		t.Fatal(err)
	}
	return owner, fence
}

func paperRunnerLoopCanceled(err error) bool {
	return err == nil || errors.Is(err, context.Canceled)
}
