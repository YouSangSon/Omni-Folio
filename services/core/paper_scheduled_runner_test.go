package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestG38FScheduledPaperRunAppliesLatestAvailableCloseOnce(t *testing.T) {
	svc, baseline := g38EPerformanceWindow(t, []string{"100"})
	ctx := context.Background()
	if _, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef, baseline.StrategySelectionEventID, baseline.StrategyPerformanceID); err != nil {
		t.Fatal(err)
	}
	asOf := "2026-01-12T06:30:00.000000000Z"
	recordG38C3MarkBar(t, svc, "005930", "g38f-latest-close", asOf, "10")

	result, err := svc.runDuePaperPerformancePolicy(ctx, k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	if result.AsOf != asOf || result.PerformanceID == "" || result.StrategyPerformanceID == "" ||
		result.PolicyEventID == "" || result.Decision != "HALT_AND_ROLLBACK" ||
		result.ReasonCode != "max_drawdown_limit_reached" {
		t.Fatalf("run result=%+v", result)
	}
	if scheduledPerformanceCount(t, svc) != 2 || strategyPerformanceCount(t, svc) != 2 || g38EJournalCounts(t, svc).Policy != 2 {
		t.Fatalf("unexpected journal counts performance=%d strategy=%d g38e=%+v",
			scheduledPerformanceCount(t, svc), strategyPerformanceCount(t, svc), g38EJournalCounts(t, svc))
	}

	retry, err := svc.runDuePaperPerformancePolicy(ctx, k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	if retry.PolicyEventID != result.PolicyEventID || scheduledPerformanceCount(t, svc) != 2 ||
		strategyPerformanceCount(t, svc) != 2 || g38EJournalCounts(t, svc).Policy != 2 {
		t.Fatalf("retry diverged first=%+v retry=%+v counts=(%d,%d,%+v)",
			result, retry, scheduledPerformanceCount(t, svc), strategyPerformanceCount(t, svc), g38EJournalCounts(t, svc))
	}
}

func TestG38FScheduledPaperRunResumesInterruptedLossBeforeNewClose(t *testing.T) {
	for _, phase := range []string{"C3", "C3+D", "C3+D loop"} {
		t.Run(phase, func(t *testing.T) {
			svc, baseline := g38EPerformanceWindow(t, []string{"100"})
			ctx := context.Background()
			if _, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef, baseline.StrategySelectionEventID, baseline.StrategyPerformanceID); err != nil {
				t.Fatal(err)
			}
			const lossClose = "2026-01-12T06:30:00.000000000Z"
			recordG38C3MarkBar(t, svc, "005930", "interrupted-loss", lossClose, "70")
			point, err := svc.evaluatePaperPerformance(ctx, k2aAccountRef, lossClose)
			if err != nil {
				t.Fatal(err)
			}
			if phase != "C3" {
				if _, err := svc.evaluatePaperStrategyPerformance(ctx, k2aAccountRef, point.StrategySelectionEventID, point.PerformanceID); err != nil {
					t.Fatal(err)
				}
			}
			recordG38C3MarkBar(t, svc, "005930", "after-interrupted-loss", "2026-01-13T06:30:00.000000000Z", "100")
			resumer := secondG38C2Service(t, svc, svc.now())
			before := g38c2PaperSignalSideEffects(t, resumer)
			var result *PaperScheduledRunResult
			if phase == "C3+D loop" {
				err = resumer.runPaperPerformanceLoopWithWait(ctx, k2aAccountRef, func(context.Context, time.Duration) error {
					t.Fatal("loop idled before completing the pending loss halt")
					return nil
				})
				if err == nil {
					result, _, err = loadScheduledPaperRunResult(ctx, resumer.db, k2aAccountRef, lossClose)
				}
			} else {
				result, err = resumer.runDuePaperPerformancePolicy(ctx, k2aAccountRef)
			}
			if err != nil {
				t.Fatal(err)
			}
			if result == nil || result.AsOf != lossClose || result.Decision != "HALT_AND_ROLLBACK" || result.ReasonCode != "cumulative_return_floor_reached" {
				t.Fatalf("new close bypassed incomplete loss policy: %+v", result)
			}
			if scheduledPerformanceCount(t, resumer) != 2 || strategyPerformanceCount(t, resumer) != 2 || g38EJournalCounts(t, resumer).Policy != 2 {
				t.Fatalf("resumption advanced beyond pending loss: C3=%d D=%d E=%d", scheduledPerformanceCount(t, resumer), strategyPerformanceCount(t, resumer), g38EJournalCounts(t, resumer).Policy)
			}
			assertG38C2PaperSignalSideEffects(t, before, g38c2PaperSignalSideEffects(t, resumer))
			retry, err := resumer.runDuePaperPerformancePolicy(ctx, k2aAccountRef)
			if err == nil || !strings.Contains(err.Error(), "current paper strategy is missing") || retry != nil {
				t.Fatalf("no-strategy retry advanced after pending halt: result=%+v err=%v", retry, err)
			}
			if scheduledPerformanceCount(t, resumer) != 2 || strategyPerformanceCount(t, resumer) != 2 || g38EJournalCounts(t, resumer).Policy != 2 {
				t.Fatal("no-strategy retry appended performance or policy")
			}
			assertG38C2PaperSignalSideEffects(t, before, g38c2PaperSignalSideEffects(t, resumer))
		})
	}
}

func TestG38FScheduledPaperRunCompletesPendingHoldThenAdvances(t *testing.T) {
	svc, baseline := g38EPerformanceWindow(t, []string{"100"})
	ctx := context.Background()
	if _, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef, baseline.StrategySelectionEventID, baseline.StrategyPerformanceID); err != nil {
		t.Fatal(err)
	}
	const pendingClose = "2026-01-12T06:30:00.000000000Z"
	const latestClose = "2026-01-13T06:30:00.000000000Z"
	recordG38C3MarkBar(t, svc, "005930", "pending-hold", pendingClose, "100")
	if _, err := svc.evaluatePaperPerformance(ctx, k2aAccountRef, pendingClose); err != nil {
		t.Fatal(err)
	}
	recordG38C3MarkBar(t, svc, "005930", "after-pending-hold", latestClose, "100")
	first, err := svc.runDuePaperPerformancePolicy(ctx, k2aAccountRef)
	if err != nil || first == nil || first.AsOf != pendingClose || first.Decision != "HOLD" || scheduledPerformanceCount(t, svc) != 2 {
		t.Fatalf("pending HOLD was bypassed: result=%+v err=%v", first, err)
	}
	second, err := svc.runDuePaperPerformancePolicy(ctx, k2aAccountRef)
	if err != nil || second == nil || second.AsOf != latestClose || second.Decision != "HOLD" {
		t.Fatalf("completed HOLD prevented fresh latest close: result=%+v err=%v", second, err)
	}
	retry, err := svc.runDuePaperPerformancePolicy(ctx, k2aAccountRef)
	if err != nil || retry == nil || retry.PolicyEventID != second.PolicyEventID || scheduledPerformanceCount(t, svc) != 3 || g38EJournalCounts(t, svc).Policy != 3 {
		t.Fatalf("completed retry duplicated journal: result=%+v err=%v", retry, err)
	}
}

func TestG38FScheduledPaperRunFailsClosedWithoutCurrentStrategyOrCompleteMark(t *testing.T) {
	ctx := context.Background()
	t.Run("no strategy does not write", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		svc.now = func() time.Time { return mustTime("2026-01-11T07:00:00Z") }
		asOf := "2026-01-11T06:30:00.000000000Z"
		recordG38C3MarkBar(t, svc, "005930", "g38f-no-strategy", asOf, "100")
		if _, err := svc.runDuePaperPerformancePolicy(ctx, k2aAccountRef); err == nil ||
			!strings.Contains(err.Error(), "current strategy selection is missing") || scheduledPerformanceCount(t, svc) != 0 {
			t.Fatalf("no-strategy run wrote rows or returned wrong error asOf=%s err=%v rows=%d",
				asOf, err, scheduledPerformanceCount(t, svc))
		}
	})

	t.Run("missing held-position mark does not write", func(t *testing.T) {
		svc, baseline := g38EPerformanceWindow(t, []string{"100"})
		if _, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef, baseline.StrategySelectionEventID, baseline.StrategyPerformanceID); err != nil {
			t.Fatal(err)
		}
		asOf := "2026-01-12T06:30:00.000000000Z"
		recordG38C3MarkBar(t, svc, "000660", "g38f-wrong-symbol", asOf, "80")
		before := scheduledPerformanceCount(t, svc)
		if _, err := svc.runDuePaperPerformancePolicy(ctx, k2aAccountRef); err == nil ||
			!strings.Contains(err.Error(), "marks are incomplete") || scheduledPerformanceCount(t, svc) != before {
			t.Fatalf("incomplete mark run err=%v before=%d after=%d", err, before, scheduledPerformanceCount(t, svc))
		}
	})
}

func TestG38FScheduledPaperRunConvergesAcrossTwoOwners(t *testing.T) {
	primary, baseline := g38EPerformanceWindow(t, []string{"100"})
	if _, err := primary.applyPaperPerformancePolicy(context.Background(), k2aAccountRef, baseline.StrategySelectionEventID, baseline.StrategyPerformanceID); err != nil {
		t.Fatal(err)
	}
	asOf := "2026-01-12T06:30:00.000000000Z"
	primary.now = func() time.Time { return mustTime("2026-01-12T07:00:00Z") }
	recordG38C3MarkBar(t, primary, "005930", "g38f-concurrent-close", asOf, "100")
	secondary := secondG38C2Service(t, primary, mustTime("2026-01-12T07:00:00Z"))

	start := make(chan struct{})
	results := make(chan *PaperScheduledRunResult, 2)
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for _, svc := range []*Service{primary, secondary} {
		wg.Add(1)
		go func(svc *Service) {
			defer wg.Done()
			<-start
			result, err := svc.runDuePaperPerformancePolicy(context.Background(), k2aAccountRef)
			results <- result
			errors <- err
		}(svc)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id string
	for result := range results {
		if result == nil || result.AsOf != asOf || (id != "" && result.PolicyEventID != id) {
			t.Fatalf("concurrent runs diverged prior=%s result=%+v", id, result)
		}
		id = result.PolicyEventID
	}
	if scheduledPerformanceCount(t, primary) != 2 || strategyPerformanceCount(t, primary) != 2 || g38EJournalCounts(t, primary).Policy != 2 {
		t.Fatalf("duplicate rows performance=%d strategy=%d g38e=%+v",
			scheduledPerformanceCount(t, primary), strategyPerformanceCount(t, primary), g38EJournalCounts(t, primary))
	}
}

func TestG38FPaperRunDueCLI(t *testing.T) {
	svc, baseline := g38EPerformanceWindow(t, []string{"100"})
	if _, err := svc.applyPaperPerformancePolicy(context.Background(), k2aAccountRef, baseline.StrategySelectionEventID, baseline.StrategyPerformanceID); err != nil {
		t.Fatal(err)
	}
	asOf := "2026-01-12T06:30:00.000000000Z"
	recordG38C3MarkBar(t, svc, "005930", "g38f-cli-close", asOf, "100")
	var dbPath string
	if err := svc.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&dbPath); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if err := run([]string{"paper-run-due", "-db", dbPath, "-account", k2aAccountRef}); err != nil {
			t.Fatal(err)
		}
	}
	if scheduledPerformanceCount(t, svc) != 2 || strategyPerformanceCount(t, svc) != 2 || g38EJournalCounts(t, svc).Policy != 2 {
		t.Fatalf("CLI rows performance=%d strategy=%d policy=%+v",
			scheduledPerformanceCount(t, svc), strategyPerformanceCount(t, svc), g38EJournalCounts(t, svc))
	}
}

func TestG38FScheduledPaperRunRetryRejectsPrerequisiteCorruption(t *testing.T) {
	svc, baseline := g38EPerformanceWindow(t, []string{"100"})
	if _, err := svc.applyPaperPerformancePolicy(context.Background(), k2aAccountRef, baseline.StrategySelectionEventID, baseline.StrategyPerformanceID); err != nil {
		t.Fatal(err)
	}
	asOf := "2026-01-12T06:30:00.000000000Z"
	recordG38C3MarkBar(t, svc, "005930", "g38f-corrupt-retry", asOf, "100")
	result, err := svc.runDuePaperPerformancePolicy(context.Background(), k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER paper_market_bar_observations_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE paper_market_bar_observations SET record_sha256=? WHERE sequence=1`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if retry, err := svc.runDuePaperPerformancePolicy(context.Background(), k2aAccountRef); err == nil || retry != nil {
		t.Fatalf("corrupt prerequisite returned cached result first=%+v retry=%+v err=%v", result, retry, err)
	}
}

func scheduledPerformanceCount(t testing.TB, svc *Service) int {
	t.Helper()
	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_performance_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
