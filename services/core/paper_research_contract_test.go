package main

import (
	"context"
	"testing"
)

func TestPaperResearchV2DoesNotGrantRegistryAuthority(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	artifact := strategyArtifact(t, func(config map[string]any) {
		config["schema_version"] = "strategy-improvement.v2"
	})
	if _, err := svc.registerStrategyEvidence(context.Background(), artifact); err == nil {
		t.Fatal("offline v2 research was admitted to the v1 paper registry")
	}
	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM strategy_research_evidence`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected research left evidence: count=%d err=%v", count, err)
	}
}
