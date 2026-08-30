package main

import (
	"context"
	"strings"
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
}

func strategyPerformanceCount(t testing.TB, svc *Service) int {
	t.Helper()
	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_strategy_performance_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
