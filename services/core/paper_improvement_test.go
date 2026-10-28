package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPaperPromotionImprovementCLIRegistersAndComparesResearch(t *testing.T) {
	f := newPaperPromotionFixture(t)
	artifact := strategyArtifact(t, func(config map[string]any) {
		config["experiment_id"] = "paper-improvement-handoff-fixture"
		strategy := config["strategy"].(map[string]any)
		strategy["fast_windows"], strategy["slow_windows"] = []int{2}, []int{5}
	})
	evidence, err := decodeStrategyArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.svc.db.QueryRow(`SELECT COUNT(*) FROM strategy_research_evidence WHERE result_sha256=?`, evidence.ResultSHA256).Scan(&count); err != nil || count != 0 {
		t.Fatalf("research must start unregistered: %d, %v", count, err)
	}
	path := filepath.Join(t.TempDir(), "research.json")
	if err := os.WriteFile(path, artifact, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runContext(context.Background(), []string{"paper-improve", "-db", f.dbPath, "-account", k2aAccountRef,
		"-artifact", path, "-bars", f.barsPath, "-attempt-id", "paper-improvement-primary"}); err != nil {
		t.Fatalf("research-to-paper handoff: %v", err)
	}
	if err := f.svc.db.QueryRow(`SELECT COUNT(*) FROM strategy_research_evidence WHERE result_sha256=?`, evidence.ResultSHA256).Scan(&count); err != nil || count != 1 {
		t.Fatalf("research registration: %d, %v", count, err)
	}
	receipt, err := f.svc.paperPromotionStatus(context.Background(), "paper-improvement-primary")
	if err != nil || receipt == nil || receipt.Decision != "PROMOTE" || receipt.SelectionEventID == "" {
		t.Fatalf("durable comparison and selection: %+v, %v", receipt, err)
	}
}
