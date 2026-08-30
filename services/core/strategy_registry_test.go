package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestG38BRegistryRejectsUnsafeExecutionContract(t *testing.T) {
	valid := strategyArtifact(t, nil)
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing field", func(v map[string]any) { delete(v, "fee") }},
		{"extra field", func(v map[string]any) { v["commission_currency"] = "KRW" }},
		{"zero starting cash", func(v map[string]any) { v["starting_cash"] = "0" }},
		{"negative fee", func(v map[string]any) { v["fee"] = "-1" }},
		{"negative tax", func(v map[string]any) { v["tax"] = "-0.001" }},
		{"negative slippage", func(v map[string]any) { v["slippage_bps"] = "-1" }},
		{"zero delay", func(v map[string]any) { v["delay_bars"] = "0" }},
		{"fractional delay", func(v map[string]any) { v["delay_bars"] = "1.5" }},
		{"zero participation", func(v map[string]any) { v["max_participation"] = "0" }},
		{"excess participation", func(v map[string]any) { v["max_participation"] = "1.1" }},
		{"noncanonical money", func(v map[string]any) { v["starting_cash"] = "01" }},
		{"negative zero fee", func(v map[string]any) { v["fee"] = "-0" }},
		{"numeric money", func(v map[string]any) { v["starting_cash"] = json.Number("10000") }},
		{"wrong signal price", func(v map[string]any) { v["signal_price"] = "same_bar_close" }},
		{"wrong fill price", func(v map[string]any) { v["fill_price"] = "same_bar_close" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			artifact := rehashedStrategyArtifact(t, valid, func(result map[string]any) {
				test.mutate(result["execution"].(map[string]any))
			})
			if _, err := svc.registerStrategyEvidence(context.Background(), artifact); err == nil {
				t.Fatal("unsafe execution contract was admitted")
			}
			var count int
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM strategy_research_evidence`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rejected execution contract left evidence: count=%d err=%v", count, err)
			}
		})
	}
}

func TestG38C1LoadsOnlyCurrentSelectedExecutionPolicy(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	ctx := context.Background()
	evidence, err := svc.registerStrategyEvidence(ctx, strategyArtifact(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := svc.selectPaperCandidate(ctx, evidence.ResultSHA256, noStrategySelectionEvent)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := loadCurrentStrategyExecutionPolicy(ctx, svc.db, evidence.ResultSHA256, selected.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	if policy.StartingCash != "10000" || policy.Fee != "1" || policy.Tax != "0.001" || policy.SlippageBPS != "10" ||
		policy.DelayBars != 1 || policy.MaxParticipation != "0.5" || policy.SignalPrice != "bar_close" ||
		policy.FillPrice != "next_eligible_bar_open" || !strategySHA256Pattern.MatchString(policy.SHA256) {
		t.Fatalf("execution policy=%+v", policy)
	}
	if _, err := svc.rollbackPaperCandidate(ctx, selected.CurrentEventID, selected.CurrentEventID); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCurrentStrategyExecutionPolicy(ctx, svc.db, evidence.ResultSHA256, selected.CurrentEventID); err == nil {
		t.Fatal("superseded selection loaded an execution policy")
	}
}

func rehashedStrategyArtifact(t testing.TB, artifact []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(artifact))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	mutate(result)
	delete(result, "result_sha256")
	body, err := strategyCanonicalJSON(result)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	result["result_sha256"] = hex.EncodeToString(hash[:])
	canonical, err := strategyCanonicalJSON(result)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestG3RegistryRegistersPythonEvidenceAndSelectsFailClosed(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	ctx := context.Background()
	candidate := strategyArtifact(t, nil)

	evidence, err := svc.registerStrategyEvidence(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Target != "paper_candidate" || evidence.ResultSHA256 == "" {
		t.Fatalf("valid Python evidence was not admitted: %+v", evidence)
	}
	duplicate, err := svc.registerStrategyEvidence(ctx, candidate)
	if err != nil || duplicate.ResultSHA256 != evidence.ResultSHA256 {
		t.Fatalf("identical evidence was not idempotent: duplicate=%+v err=%v", duplicate, err)
	}
	var evidenceCount int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM strategy_research_evidence`).Scan(&evidenceCount); err != nil || evidenceCount != 1 {
		t.Fatalf("evidence count=%d, want 1: %v", evidenceCount, err)
	}
	tampered := bytes.Replace(candidate, []byte(`"paper_candidate"`), []byte(`"live"`), 1)
	if _, err := svc.registerStrategyEvidence(ctx, tampered); err == nil {
		t.Fatal("tampered promotion evidence was admitted")
	}

	blocked := strategyArtifact(t, func(config map[string]any) {
		config["experiment_id"] = "sma-grid-blocked-001"
		config["promotion"].(map[string]any)["minimum_holdout_after_cost_return"] = "999"
	})
	rejected, err := svc.registerStrategyEvidence(ctx, blocked)
	if err != nil || rejected.Target != "no_promotion" {
		t.Fatalf("rejected evidence was not preserved: evidence=%+v err=%v", rejected, err)
	}
	if _, err := svc.selectPaperCandidate(ctx, rejected.ResultSHA256, noStrategySelectionEvent); err == nil {
		t.Fatal("no_promotion evidence became the selected paper candidate")
	}
	direct := StrategySelectionEvent{
		EventID: "strategy_selection_direct_rejected", EventType: "SELECT", CandidateResultSHA256: rejected.ResultSHA256,
		ExpectedCurrentEventID: noStrategySelectionEvent, PreviousSelectedResultSHA256: noStrategySelection,
		SelectedResultSHA256: rejected.ResultSHA256, ReasonCode: "manual_selection", RecordedAt: "2026-01-10T15:02:00Z",
	}
	directJSON, directSHA, err := orderJSONHash(direct)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO strategy_selection_events(
		event_id,event_type,candidate_result_sha256,expected_current_event_id,source_event_id,
		previous_selected_result_sha256,selected_result_sha256,reason_code,record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, direct.EventID, direct.EventType, direct.CandidateResultSHA256,
		direct.ExpectedCurrentEventID, nil, direct.PreviousSelectedResultSHA256, direct.SelectedResultSHA256,
		direct.ReasonCode, directSHA, string(directJSON), direct.RecordedAt); err == nil {
		t.Fatal("SQLite guard allowed direct selection of no_promotion evidence")
	}

	selected, err := svc.selectPaperCandidate(ctx, evidence.ResultSHA256, noStrategySelectionEvent)
	if err != nil {
		t.Fatal(err)
	}
	if selected.SelectedResultSHA256 != evidence.ResultSHA256 || selected.CurrentEventID == noStrategySelectionEvent {
		t.Fatalf("paper candidate was not selected: %+v", selected)
	}
	if _, err := svc.selectPaperCandidate(ctx, evidence.ResultSHA256, noStrategySelectionEvent); err == nil {
		t.Fatal("stale expected_current_event_id appended a selection")
	}
	var eventCount int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM strategy_selection_events`).Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("selection event count=%d, want 1: %v", eventCount, err)
	}
}

func TestG3RegistryMatchesPythonCanonicalJSONForHTMLCharacters(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	artifact := strategyArtifact(t, func(config map[string]any) {
		config["experiment_id"] = "sma-grid-<&>-canonical"
		config["strategy"].(map[string]any)["version"] = "1.0.0<&>"
	})
	if _, err := svc.registerStrategyEvidence(context.Background(), artifact); err != nil {
		t.Fatal(err)
	}
}

func TestG3RegistryRollsBackOnlyToThePreviousSelection(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	ctx := context.Background()
	first, err := svc.registerStrategyEvidence(ctx, strategyArtifact(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.registerStrategyEvidence(ctx, strategyArtifact(t, func(config map[string]any) {
		config["experiment_id"] = "sma-grid-fixture-002"
		config["strategy"].(map[string]any)["version"] = "1.0.1"
	}))
	if err != nil {
		t.Fatal(err)
	}
	firstState, err := svc.selectPaperCandidate(ctx, first.ResultSHA256, noStrategySelectionEvent)
	if err != nil {
		t.Fatal(err)
	}
	secondState, err := svc.selectPaperCandidate(ctx, second.ResultSHA256, firstState.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.rollbackPaperCandidate(ctx, secondState.CurrentEventID, firstState.CurrentEventID); err == nil {
		t.Fatal("rollback accepted a source event other than the current selection event")
	}
	rolledBack, err := svc.rollbackPaperCandidate(ctx, secondState.CurrentEventID, secondState.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.SelectedResultSHA256 != first.ResultSHA256 {
		t.Fatalf("rollback selected %q, want previous %q", rolledBack.SelectedResultSHA256, first.ResultSHA256)
	}
	noStrategy, err := svc.rollbackPaperCandidate(ctx, rolledBack.CurrentEventID, rolledBack.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	if noStrategy.SelectedResultSHA256 != noStrategySelection {
		t.Fatalf("second rollback selected %q, want %q", noStrategy.SelectedResultSHA256, noStrategySelection)
	}

	for _, statement := range []string{
		`UPDATE strategy_research_evidence SET target='no_promotion'`,
		`DELETE FROM strategy_research_evidence`,
		`UPDATE strategy_selection_events SET reason_code='manual_selection'`,
		`DELETE FROM strategy_selection_events`,
	} {
		if _, err := svc.db.Exec(statement); err == nil {
			t.Fatalf("insert-only registry accepted mutation: %s", statement)
		}
	}
	proof, err := proveStrategyRegistryRecovery(ctx, svc.db)
	if err != nil || proof.Evidence != 2 || proof.Events != 4 || proof.SelectedResultSHA256 != noStrategySelection {
		t.Fatalf("registry recovery proof=%+v err=%v", proof, err)
	}
}

func TestG3RegistryRecoveryRejectsTamperedEvidence(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	evidence, err := svc.registerStrategyEvidence(context.Background(), strategyArtifact(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, noStrategySelectionEvent); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER strategy_research_evidence_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE strategy_research_evidence SET artifact_sha256=?`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := proveStrategyRegistryRecovery(context.Background(), svc.db); err == nil {
		t.Fatal("tampered strategy evidence was certified")
	}
}

func TestG3RegistryBackupRestoresSelectionProof(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	evidence, err := svc.registerStrategyEvidence(context.Background(), strategyArtifact(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, noStrategySelectionEvent)
	if err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "backup.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v10" || manifest.SchemaVersion != "omni-folio.sqlite.v15" ||
		manifest.StrategyRegistrySHA256 == "" || manifest.StrategyEvidenceCount != 1 || manifest.StrategySelectionEventCount != 1 ||
		manifest.SelectedStrategyResultSHA256 != evidence.ResultSHA256 {
		t.Fatalf("backup omitted strategy registry proof: %+v", manifest)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}
	restoredDB, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close()
	restored, err := proveStrategyRegistryRecovery(context.Background(), restoredDB)
	if err != nil || restored.CurrentEventID != selected.CurrentEventID || restored.SelectedResultSHA256 != evidence.ResultSHA256 {
		t.Fatalf("restored registry proof=%+v err=%v", restored, err)
	}

	tampered := readJSONMap(t, manifestPath)
	tampered["strategy_registry_sha256"] = strings.Repeat("0", 64)
	tamperedPath := filepath.Join(t.TempDir(), "tampered-manifest.json")
	writeJSONFile(t, tamperedPath, tampered)
	if err := verifyManifest(backup, golden, tamperedPath); err == nil {
		t.Fatal("manifest with a different strategy registry digest was accepted")
	}
	if _, err := restoredDB.Exec(`DROP TRIGGER strategy_selection_events_state_guard`); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err == nil {
		t.Fatal("restore accepted a strategy registry without its state guard")
	}
}

func TestG3RegistryCLIImportsSelectsAndRollsBackLocalEvidence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "core.db")
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(t.TempDir(), "strategy-result.json")
	if err := os.WriteFile(artifactPath, strategyArtifact(t, nil), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"strategy-register", "-db", dbPath, "-artifact", artifactPath}); err != nil {
		t.Fatal(err)
	}
	var resultSHA string
	if err := db.QueryRow(`SELECT result_sha256 FROM strategy_research_evidence`).Scan(&resultSHA); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"strategy-select", "-db", dbPath, "-result-sha256", resultSHA, "-expected-current-event", noStrategySelectionEvent}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"strategy-status", "-db", dbPath}); err != nil {
		t.Fatal(err)
	}
	var eventID string
	if err := db.QueryRow(`SELECT event_id FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1`).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"strategy-rollback", "-db", dbPath, "-expected-current-event", eventID, "-source-event", eventID}); err != nil {
		t.Fatal(err)
	}
	proof, err := proveStrategyRegistryRecovery(context.Background(), db)
	if err != nil || proof.SelectedResultSHA256 != noStrategySelection || proof.Events != 2 {
		t.Fatalf("CLI registry proof=%+v err=%v", proof, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func strategyArtifact(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	configBytes, err := os.ReadFile(filepath.Join(root, "contracts", "fixtures", "strategy-improvement-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(configBytes, &config); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(config)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	configBytes, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", "-m", "omni_research.improve_cli",
		"--bars", filepath.Join(root, "contracts", "fixtures", "strategy-market-bars.csv"),
		"--config", configPath,
	)
	command.Env = append(os.Environ(), "PYTHONPATH="+filepath.Join(root, "services", "research"))
	artifact, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generate Python strategy artifact: %v\n%s", err, artifact)
	}
	return artifact
}
