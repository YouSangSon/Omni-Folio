package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestG38DStrategyWindow(t *testing.T) {
	svc, asOfs := g38c3HeldPerformanceFixture(t)
	ctx := context.Background()

	firstPoint, err := svc.evaluatePaperPerformance(ctx, k2aAccountRef, asOfs[0])
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.evaluatePaperStrategyPerformance(ctx, k2aAccountRef, firstPoint.StrategySelectionEventID, firstPoint.PerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	if first.SchemaVersion != "paper-strategy-window-performance.v1" ||
		first.PolicyVersion != "paper-strategy-window-performance-v1" || first.SampleCount != 1 ||
		first.BaselinePerformanceID != firstPoint.PerformanceID || first.LatestPerformanceID != firstPoint.PerformanceID ||
		first.BaselineEquity != "10038.8" || first.LatestEquity != "10038.8" || first.PeakEquity != "10038.8" ||
		first.PeriodReturnState != "defined" || first.PeriodReturn != "0" || first.CumulativeReturn != "0" ||
		first.Drawdown != "0" || first.MaxDrawdown != "0" {
		t.Fatalf("first strategy point=%+v", first)
	}

	secondPoint, err := svc.evaluatePaperPerformance(ctx, k2aAccountRef, asOfs[1])
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.evaluatePaperStrategyPerformance(ctx, k2aAccountRef, secondPoint.StrategySelectionEventID, secondPoint.PerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	if second.ExpectedPreviousStrategyPerformanceID != first.StrategyPerformanceID || second.SampleCount != 2 ||
		second.BaselinePerformanceID != firstPoint.PerformanceID || second.LatestPerformanceID != secondPoint.PerformanceID ||
		second.BaselineEquity != "10038.8" || second.LatestEquity != "9958.8" || second.PeakEquity != "10038.8" ||
		second.PeriodReturn != "-0.00796908" || second.CumulativeReturn != "-0.00796908" ||
		second.Drawdown != "0.00796908" || second.MaxDrawdown != "0.00796908" {
		t.Fatalf("second strategy point=%+v", second)
	}

	replayed, err := svc.evaluatePaperStrategyPerformance(ctx, k2aAccountRef, secondPoint.StrategySelectionEventID, secondPoint.PerformanceID)
	if err != nil || replayed.StrategyPerformanceID != second.StrategyPerformanceID || strategyPerformanceCount(t, svc) != 2 {
		t.Fatalf("retry=%+v count=%d err=%v", replayed, strategyPerformanceCount(t, svc), err)
	}
}

func TestG38DStrategyWindowResetsAfterSelection(t *testing.T) {
	svc, asOfs := g38c3HeldPerformanceFixture(t)
	ctx := context.Background()
	for _, asOf := range asOfs[:2] {
		point, err := svc.evaluatePaperPerformance(ctx, k2aAccountRef, asOf)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.evaluatePaperStrategyPerformance(ctx, k2aAccountRef, point.StrategySelectionEventID, point.PerformanceID); err != nil {
			t.Fatal(err)
		}
	}
	current, err := replayStrategyRegistry(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return mustTime("2026-01-14T00:00:00Z") }
	artifact := rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(result map[string]any) {
		result["experiment_id"] = "g38d-selection-reset"
	})
	evidence, err := svc.registerStrategyEvidence(ctx, artifact)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := svc.selectPaperCandidate(ctx, evidence.ResultSHA256, current.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	asOf := "2026-01-14T06:30:00.000000000Z"
	svc.now = func() time.Time { return mustTime("2026-01-14T07:00:00Z") }
	recordG38C3MarkBar(t, svc, "005930", "g38d-selection-reset", asOf, "70")
	point, err := svc.evaluatePaperPerformance(ctx, k2aAccountRef, asOf)
	if err != nil {
		t.Fatal(err)
	}
	window, err := svc.evaluatePaperStrategyPerformance(ctx, k2aAccountRef, selected.CurrentEventID, point.PerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	if point.MaxDrawdown == "0" || window.SampleCount != 1 || window.BaselineEquity != point.Equity ||
		window.PeakEquity != point.Equity || window.CumulativeReturn != "0" || window.Drawdown != "0" || window.MaxDrawdown != "0" {
		t.Fatalf("account point=%+v strategy window=%+v", point, window)
	}
}

func TestG38DNoStrategy(t *testing.T) {
	svc, asOf := g38c3CashPerformanceFixture(t)
	ctx := context.Background()
	first, err := svc.evaluatePaperPerformance(ctx, k2aAccountRef, asOf)
	if err != nil {
		t.Fatal(err)
	}
	rolled, err := svc.rollbackPaperCandidate(ctx, first.StrategySelectionEventID, first.StrategySelectionEventID)
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return mustTime("2026-01-12T07:00:00Z") }
	nextAsOf := "2026-01-12T06:30:00.000000000Z"
	recordG38C3MarkBar(t, svc, "005930", "g38d-no-strategy", nextAsOf, "100")
	point, err := svc.evaluatePaperPerformance(ctx, k2aAccountRef, nextAsOf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.evaluatePaperStrategyPerformance(ctx, k2aAccountRef, rolled.CurrentEventID, point.PerformanceID); err == nil ||
		!strings.Contains(err.Error(), "not attributable") || strategyPerformanceCount(t, svc) != 0 {
		t.Fatalf("no_strategy err=%v rows=%d", err, strategyPerformanceCount(t, svc))
	}
}

func TestG38DStale(t *testing.T) {
	svc, asOfs := g38c3HeldPerformanceFixture(t)
	ctx := context.Background()
	first, err := svc.evaluatePaperPerformance(ctx, k2aAccountRef, asOfs[0])
	if err != nil {
		t.Fatal(err)
	}
	for name, ids := range map[string][2]string{
		"selection":   {"strategy_selection_stale", first.PerformanceID},
		"performance": {first.StrategySelectionEventID, "paper_performance_stale"},
	} {
		selectionID, performanceID := ids[0], ids[1]
		t.Run(name, func(t *testing.T) {
			if _, err := svc.evaluatePaperStrategyPerformance(ctx, k2aAccountRef, selectionID, performanceID); err == nil ||
				strategyPerformanceCount(t, svc) != 0 {
				t.Fatalf("stale result err=%v rows=%d", err, strategyPerformanceCount(t, svc))
			}
		})
	}
	if _, err := svc.evaluatePaperStrategyPerformance(ctx, k2aAccountRef, first.StrategySelectionEventID, first.PerformanceID); err != nil {
		t.Fatal(err)
	}
	second, err := svc.evaluatePaperPerformance(ctx, k2aAccountRef, asOfs[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.evaluatePaperStrategyPerformance(ctx, k2aAccountRef, first.StrategySelectionEventID, first.PerformanceID); err == nil ||
		strategyPerformanceCount(t, svc) != 1 {
		t.Fatalf("old latest retry err=%v rows=%d new=%s", err, strategyPerformanceCount(t, svc), second.PerformanceID)
	}
}

func TestG38DRecovery(t *testing.T) {
	t.Run("replays exact evidence and rejects mutation", func(t *testing.T) {
		svc, asOf := g38c3CashPerformanceFixture(t)
		point, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.evaluatePaperStrategyPerformance(context.Background(), k2aAccountRef, point.StrategySelectionEventID, point.PerformanceID); err != nil {
			t.Fatal(err)
		}
		proof, err := provePaperStrategyPerformanceRecovery(context.Background(), svc.db)
		if err != nil || proof.Events != 1 || proof.Samples != 1 || proof.SHA256 == "" {
			t.Fatalf("proof=%+v err=%v", proof, err)
		}
		for _, statement := range []string{
			`UPDATE paper_strategy_performance_events SET sample_count=2`,
			`DELETE FROM paper_strategy_performance_events`,
		} {
			if _, err := svc.db.Exec(statement); err == nil {
				t.Fatalf("append-only mutation was accepted: %s", statement)
			}
		}
		if _, err := svc.db.Exec(`DROP TRIGGER paper_strategy_performance_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE paper_strategy_performance_events SET sample_count=2`); err != nil {
			t.Fatal(err)
		}
		if _, err := provePaperStrategyPerformanceRecovery(context.Background(), svc.db); err == nil {
			t.Fatal("recovery accepted mutated strategy performance evidence")
		}
	})

	t.Run("old retry cannot hide later prerequisite corruption", func(t *testing.T) {
		svc, asOfs := g38c3HeldPerformanceFixture(t)
		first, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOfs[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.evaluatePaperStrategyPerformance(context.Background(), k2aAccountRef, first.StrategySelectionEventID, first.PerformanceID); err != nil {
			t.Fatal(err)
		}
		second, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOfs[1])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`DROP TRIGGER paper_performance_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`UPDATE paper_performance_events SET record_sha256=? WHERE performance_id=?`, strings.Repeat("0", 64), second.PerformanceID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.evaluatePaperStrategyPerformance(context.Background(), k2aAccountRef, first.StrategySelectionEventID, first.PerformanceID); err == nil {
			t.Fatal("old retry hid later corrupt C3 evidence")
		}
	})
}

func TestG38DIdempotency(t *testing.T) {
	svc, asOf := g38c3CashPerformanceFixture(t)
	point, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf)
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.evaluatePaperStrategyPerformance(context.Background(), k2aAccountRef, point.StrategySelectionEventID, point.PerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.evaluatePaperStrategyPerformance(context.Background(), k2aAccountRef, point.StrategySelectionEventID, point.PerformanceID)
	if err != nil || second.StrategyPerformanceID != first.StrategyPerformanceID || strategyPerformanceCount(t, svc) != 1 {
		t.Fatalf("first=%+v second=%+v rows=%d err=%v", first, second, strategyPerformanceCount(t, svc), err)
	}
}

func TestG38DAtomicity(t *testing.T) {
	svc, asOf := g38c3CashPerformanceFixture(t)
	point, err := svc.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`CREATE TRIGGER g38d_forced_failure BEFORE INSERT ON paper_strategy_performance_events
		BEGIN SELECT RAISE(ABORT, 'forced G3.8D failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.evaluatePaperStrategyPerformance(context.Background(), k2aAccountRef, point.StrategySelectionEventID, point.PerformanceID); err == nil ||
		strategyPerformanceCount(t, svc) != 0 {
		t.Fatalf("forced failure err=%v rows=%d", err, strategyPerformanceCount(t, svc))
	}
}

func TestG38DConcurrent(t *testing.T) {
	primary, asOf := g38c3CashPerformanceFixture(t)
	point, err := primary.evaluatePaperPerformance(context.Background(), k2aAccountRef, asOf)
	if err != nil {
		t.Fatal(err)
	}
	secondary := secondG38C2Service(t, primary, mustTime("2026-01-11T07:00:00Z"))
	start := make(chan struct{})
	results := make(chan *PaperStrategyPerformanceEvent, 2)
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for _, svc := range []*Service{primary, secondary} {
		wg.Add(1)
		go func(current *Service) {
			defer wg.Done()
			<-start
			event, err := current.evaluatePaperStrategyPerformance(context.Background(), k2aAccountRef,
				point.StrategySelectionEventID, point.PerformanceID)
			results <- event
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
	for event := range results {
		if event == nil || (id != "" && id != event.StrategyPerformanceID) {
			t.Fatalf("concurrent results diverged: prior=%s event=%+v", id, event)
		}
		id = event.StrategyPerformanceID
	}
	if strategyPerformanceCount(t, primary) != 1 {
		t.Fatalf("concurrent rows=%d", strategyPerformanceCount(t, primary))
	}
}

func strategyPerformanceCount(t testing.TB, svc *Service) int {
	t.Helper()
	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_strategy_performance_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
