package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func paperInputStatusFixture(t *testing.T) (*Service, string, string, []byte, []byte, []byte) {
	t.Helper()
	svc, path, research, evidence, selected, rows := localPaperRestartFixture(t)
	bars := paperSnapshotCSV(rows...)
	proposal := localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], bars, "golden_cross")
	return svc, path, selected.CurrentEventID, proposal, bars, research
}

func paperInputStatusBundle(t *testing.T, proposal, bars, research []byte) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"schema_version": "paper-input-bundle.v1", "mode": "paper_bundle_only",
		"proposal": json.RawMessage(proposal), "bars_csv": string(bars), "research_csv": string(research),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPaperInputStatusCommandExistsWithoutArm(t *testing.T) {
	_, dbPath, selection, proposal, bars, research := paperInputStatusFixture(t)
	path := filepath.Join(t.TempDir(), "captured.json")
	if err := os.WriteFile(path, paperInputStatusBundle(t, proposal, bars, research), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runContext(context.Background(), []string{
		"paper-input-status", "-db", dbPath, "-account", k2aAccountRef,
		"-selection-event", selection, "-bundle", path,
	}); err != nil {
		t.Fatalf("read-only saved-input inspection is unavailable: %v", err)
	}
}
