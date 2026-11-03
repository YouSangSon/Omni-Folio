package main

import (
	"strings"
	"testing"
)

func TestArchitectureCurrentProviderIdentityKeepsLegacyPaperOnKiwoom(t *testing.T) {
	signal := PaperSignal{
		SchemaVersion: legacyPaperSignalSchema, SignalID: "architecture-legacy-paper",
		StrategyResultSHA256: strings.Repeat("a", 64), StrategySelectionEventID: "architecture-selection",
		DataSHA256: strings.Repeat("b", 64), Symbol: "005930",
		DataAsOf: "2026-01-10T14:59:00Z", GeneratedAt: "2026-01-10T14:59:01Z", ExpiresAt: "2026-01-10T15:01:00Z",
	}
	intent := paperOrderIntent(k2aAccountRef, signal, "1", "1000")
	if intent.Provider != "kiwoom" || intent.Mode != "paper" || intent.AccountRef != k2aAccountRef || intent.SignalSchemaVersion != legacyPaperSignalSchema {
		t.Fatalf("legacy paper identity=%+v", intent)
	}
	if err := validateOrderIntent(intent); err != nil {
		t.Fatalf("legacy Kiwoom paper compatibility was rejected: %v", err)
	}
	alias := paperProviderAlias("order", "architecture-legacy-paper")
	if !strings.HasPrefix(alias, "kiwoom_order_") || !orderAlias(alias, "order") {
		t.Fatalf("paper provider alias=%q", alias)
	}

	providerNeutralized := intent
	providerNeutralized.Provider = "paper"
	if err := validateOrderIntent(providerNeutralized); err == nil {
		t.Fatal("current contract unexpectedly accepted a provider-neutral paper identity")
	}
}
