package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestG38FScheduledPaperRunAppliesLatestAvailableCloseOnce(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	ctx := context.Background()
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
	if scheduledPerformanceCount(t, svc) != 2 || strategyPerformanceCount(t, svc) != 2 || g38EJournalCounts(t, svc).Policy != 1 {
		t.Fatalf("unexpected journal counts performance=%d strategy=%d g38e=%+v",
			scheduledPerformanceCount(t, svc), strategyPerformanceCount(t, svc), g38EJournalCounts(t, svc))
	}

	retry, err := svc.runDuePaperPerformancePolicy(ctx, k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	if retry.PolicyEventID != result.PolicyEventID || scheduledPerformanceCount(t, svc) != 2 ||
		strategyPerformanceCount(t, svc) != 2 || g38EJournalCounts(t, svc).Policy != 1 {
		t.Fatalf("retry diverged first=%+v retry=%+v counts=(%d,%d,%+v)",
			result, retry, scheduledPerformanceCount(t, svc), strategyPerformanceCount(t, svc), g38EJournalCounts(t, svc))
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
		svc, _ := g38EPerformanceWindow(t, []string{"100"})
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
	primary, _ := g38EPerformanceWindow(t, []string{"100"})
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
	if scheduledPerformanceCount(t, primary) != 2 || strategyPerformanceCount(t, primary) != 2 || g38EJournalCounts(t, primary).Policy != 1 {
		t.Fatalf("duplicate rows performance=%d strategy=%d g38e=%+v",
			scheduledPerformanceCount(t, primary), strategyPerformanceCount(t, primary), g38EJournalCounts(t, primary))
	}
}

func TestG38FPaperRunDueCLI(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
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
	if scheduledPerformanceCount(t, svc) != 2 || strategyPerformanceCount(t, svc) != 2 || g38EJournalCounts(t, svc).Policy != 1 {
		t.Fatalf("CLI rows performance=%d strategy=%d policy=%+v",
			scheduledPerformanceCount(t, svc), strategyPerformanceCount(t, svc), g38EJournalCounts(t, svc))
	}
}

func TestG38FScheduledPaperRunRetryRejectsPrerequisiteCorruption(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
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
