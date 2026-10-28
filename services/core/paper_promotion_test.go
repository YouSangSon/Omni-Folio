package main

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPaperPromotionCLISelectsOnlyAfterFreshForwardComparison(t *testing.T) {
	svc, dbPath := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-10-27T15:00:00Z") }
	ctx := context.Background()
	artifact := func(fast, slow int) []byte {
		return strategyArtifact(t, func(config map[string]any) {
			strategy := config["strategy"].(map[string]any)
			strategy["fast_windows"], strategy["slow_windows"] = []int{fast}, []int{slow}
		})
	}
	incumbent, err := svc.registerStrategyEvidence(ctx, artifact(3, 6))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := svc.selectPaperCandidate(ctx, incumbent.ResultSHA256, noStrategySelectionEvent)
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, incumbent.ResultSHA256, selected.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	challenger, err := svc.registerStrategyEvidence(ctx, artifact(2, 5))
	if err != nil {
		t.Fatal(err)
	}
	barsPath := filepath.Join(t.TempDir(), "forward.csv")
	var input strings.Builder
	writer := csv.NewWriter(&input)
	if err := writer.Write([]string{"bar_at", "symbol", "open", "high", "low", "close", "volume"}); err != nil {
		t.Fatal(err)
	}
	closes := []int{20, 20, 20, 20, 20, 20}
	for fold := 0; fold < 3; fold++ {
		for price := 20; price >= 9; price-- {
			closes = append(closes, price)
		}
		for price := 10; price <= 27; price++ {
			closes = append(closes, price)
		}
	}
	for index, close := range closes {
		at := mustTime("2026-05-01T00:00:00Z").Add(time.Duration(index) * 24 * time.Hour).Format(time.RFC3339)
		if err := writer.Write([]string{at, "005930", strconv.Itoa(close), strconv.Itoa(close + 1), strconv.Itoa(close - 1), strconv.Itoa(close), "100"}); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(barsPath, []byte(input.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"paper-promote", "-db", dbPath, "-account", k2aAccountRef,
		"-candidate-result-sha", challenger.ResultSHA256, "-expected-selection-event", selected.CurrentEventID,
		"-attempt-id", "paper_comparison_test_001", "-bars", barsPath}); err != nil {
		t.Fatalf("automatic forward comparison: %v", err)
	}
	state, err := replayStrategyRegistry(ctx, svc.db)
	if err != nil || state.SelectedResultSHA256 != challenger.ResultSHA256 || state.CurrentEventID == selected.CurrentEventID {
		t.Fatalf("candidate was not automatically selected: %+v, %v", state, err)
	}
	after, found, err := loadPaperAccountingSession(ctx, svc.db, k2aAccountRef)
	if err != nil || !found || after.SessionID != session.SessionID || after.StartingCash != session.StartingCash {
		t.Fatalf("promotion changed initial capital/session: %+v, %v", after, err)
	}
}
