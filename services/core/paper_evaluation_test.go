package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestG38PaperEvaluationDerivesInsufficientForLocallyAcknowledgedPaperOrder(t *testing.T) {
	ctx := context.Background()

	t.Run("empty then active", func(t *testing.T) {
		svc, signal, _ := g38c2PaperSignalFixture(t, true)

		insufficient, err := svc.evaluatePaperOperations(ctx, k2aAccountRef, signal.StrategyResultSHA256, signal.StrategySelectionEventID)
		if err != nil {
			t.Fatal(err)
		}
		if insufficient.Decision != "INSUFFICIENT" || insufficient.ReasonCode != "no_terminal_sample" ||
			insufficient.OrderCount != 0 || insufficient.TerminalOrderCount != 0 ||
			insufficient.ActiveOrderCount != 0 || insufficient.PendingActionCount != 0 {
			t.Fatalf("unexpected empty evaluation: %+v", insufficient)
		}

		signal.SignalID = "paper-evaluation-open"
		signal.TargetQuantity = "1"
		lease := mustK2CLease(t, svc, k2aAccountRef)
		_, state, err := svc.admitPaperSignal(ctx, k2aAccountRef, signal, lease.FencingToken)
		if err != nil || state == nil || state.Status != "OPEN" {
			t.Fatalf("paper authorization state=%+v err=%v", state, err)
		}

		active, err := svc.evaluatePaperOperations(ctx, k2aAccountRef, signal.StrategyResultSHA256, signal.StrategySelectionEventID)
		if err != nil {
			t.Fatal(err)
		}
		if active.Decision != "INSUFFICIENT" || active.ReasonCode != "no_terminal_sample" ||
			active.OrderCount != 1 || active.TerminalOrderCount != 0 ||
			active.ActiveOrderCount != 1 || active.PendingActionCount != 0 ||
			active.ExpectedPreviousEvaluationID != insufficient.EvaluationID || active.PaperOrderStateSHA256 == "" {
			t.Fatalf("unexpected active evaluation: %+v", active)
		}
	})

	t.Run("unresolved policy remains degraded", func(t *testing.T) {
		decision, reason := paperOperationalDecision(paperOperationalSnapshot{Orders: 1, Pending: 1})
		if decision != "DEGRADED" || reason != "unresolved_action" {
			t.Fatalf("unexpected unresolved decision=%s reason=%s", decision, reason)
		}
	})
}

func TestG38PaperEvaluationIsIdempotentAndSelectionBound(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-10T15:00:00Z") }
	ctx := context.Background()
	firstEvidence, firstSelection := selectedPaperStrategy(t, svc)

	first, err := svc.evaluatePaperOperations(ctx, k2aAccountRef, firstEvidence.ResultSHA256, firstSelection.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := svc.evaluatePaperOperations(ctx, k2aAccountRef, firstEvidence.ResultSHA256, firstSelection.CurrentEventID)
	if err != nil || replayed.EvaluationID != first.EvaluationID {
		t.Fatalf("idempotent evaluation=%+v err=%v", replayed, err)
	}
	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_evaluation_events`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("evaluation count=%d, want 1: %v", count, err)
	}

	secondEvidence, err := svc.registerStrategyEvidence(ctx, strategyArtifact(t, func(config map[string]any) {
		config["experiment_id"] = "sma-grid-paper-evaluation-002"
		config["strategy"].(map[string]any)["version"] = "1.0.2"
	}))
	if err != nil {
		t.Fatal(err)
	}
	secondSelection, err := svc.selectPaperCandidate(ctx, secondEvidence.ResultSHA256, firstSelection.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	var sealedEvaluationSequence int64
	if err := svc.db.QueryRow(`SELECT paper_evaluation_sequence FROM strategy_selection_events WHERE event_id=?`, secondSelection.CurrentEventID).
		Scan(&sealedEvaluationSequence); err != nil || sealedEvaluationSequence != 1 {
		t.Fatalf("selection sealed evaluation sequence=%d, want 1: %v", sealedEvaluationSequence, err)
	}
	if _, err := svc.evaluatePaperOperations(ctx, k2aAccountRef, firstEvidence.ResultSHA256, firstSelection.CurrentEventID); err == nil {
		t.Fatal("stale paper selection was evaluated")
	}
	stale := *first
	stale.PaperOrderStateSHA256 = strings.Repeat("f", 64)
	stale.EvaluationID = paperEvaluationID(stale)
	stale.ExpectedPreviousEvaluationID = first.EvaluationID
	stale.RecordedAt = "2026-01-10T15:00:00Z"
	if err := insertPaperEvaluationDirect(svc, stale); err == nil {
		t.Fatal("SQLite accepted an evaluation for a superseded strategy selection")
	}
	if _, err := svc.db.Exec(`DROP TRIGGER paper_evaluation_events_state_guard`); err != nil {
		t.Fatal(err)
	}
	if err := insertPaperEvaluationDirect(svc, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := proveStrategyRegistryRecovery(ctx, svc.db); err == nil {
		t.Fatal("strategy recovery certified a superseded paper evaluation")
	}
	if _, err := svc.evaluatePaperOperations(ctx, "not-an-account", secondEvidence.ResultSHA256, secondSelection.CurrentEventID); err == nil {
		t.Fatal("invalid paper account was evaluated")
	}
}

func TestG38PaperEvaluationDoesNotMutateStrategyOrExecutionAuthority(t *testing.T) {
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	ctx := context.Background()
	lease := mustK2CLease(t, svc, k2aAccountRef)
	signal.SignalID, signal.TargetQuantity = "paper-evaluation-no-mutation", "1"
	if _, _, err := svc.admitPaperSignal(ctx, k2aAccountRef, signal, lease.FencingToken); err != nil {
		t.Fatal(err)
	}

	evaluation, err := svc.evaluatePaperOperations(ctx, k2aAccountRef, signal.StrategyResultSHA256, signal.StrategySelectionEventID)
	if err != nil || evaluation.Decision != "INSUFFICIENT" || evaluation.ActiveOrderCount != 1 {
		t.Fatalf("evaluation=%+v err=%v", evaluation, err)
	}
	authority, err := loadExecutionAuthoritySnapshot(ctx, svc.db, k2aAccountRef)
	if err != nil || !authority.Armed || authority.FencingToken != lease.FencingToken || authority.LeaseOwner != lease.LeaseOwner {
		t.Fatalf("evaluation changed authority: %+v err=%v", authority, err)
	}
	registry, err := replayStrategyRegistry(ctx, svc.db)
	if err != nil || registry.CurrentEventID != signal.StrategySelectionEventID || registry.SelectedResultSHA256 != signal.StrategyResultSHA256 || registry.Events != 1 {
		t.Fatalf("evaluation changed strategy selection: %+v err=%v", registry, err)
	}
}

func TestG38PaperEvaluationIsInsertOnlyAndRecoveryVerified(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-10T15:00:00Z") }
	evidence, selected := selectedPaperStrategy(t, svc)
	evaluation, err := svc.evaluatePaperOperations(context.Background(), k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE paper_evaluation_events SET decision='PASS'`,
		`DELETE FROM paper_evaluation_events`,
	} {
		if _, err := svc.db.Exec(statement); err == nil {
			t.Fatalf("insert-only evaluation log accepted mutation: %s", statement)
		}
	}

	stale := *evaluation
	stale.PaperOrderStateSHA256 = strings.Repeat("f", 64)
	stale.EvaluationID = paperEvaluationID(stale)
	stale.ExpectedPreviousEvaluationID = noPaperEvaluation
	stale.RecordedAt = "2026-01-10T15:00:01Z"
	if err := insertPaperEvaluationDirect(svc, stale); err == nil {
		t.Fatal("SQLite accepted a stale paper evaluation predecessor")
	}

	if _, err := svc.db.Exec(`DROP TRIGGER paper_evaluation_events_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE paper_evaluation_events SET record_sha256=? WHERE evaluation_id=?`, strings.Repeat("0", 64), evaluation.EvaluationID); err != nil {
		t.Fatal(err)
	}
	if _, err := proveStrategyRegistryRecovery(context.Background(), svc.db); err == nil {
		t.Fatal("strategy recovery certified a tampered paper evaluation")
	}
}

func insertPaperEvaluationDirect(svc *Service, event PaperEvaluationEvent) error {
	recordJSON, recordSHA, err := orderJSONHash(event)
	if err != nil {
		return err
	}
	_, err = svc.db.Exec(`INSERT INTO paper_evaluation_events(
		evaluation_id,schema_version,policy_version,account_ref,strategy_result_sha256,strategy_selection_event_id,
		expected_previous_evaluation_id,paper_order_state_sha256,order_count,terminal_order_count,active_order_count,
		pending_action_count,decision,reason_code,record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, event.EvaluationID, event.SchemaVersion, event.PolicyVersion,
		event.AccountRef, event.StrategyResultSHA256, event.StrategySelectionEventID, event.ExpectedPreviousEvaluationID,
		event.PaperOrderStateSHA256, event.OrderCount, event.TerminalOrderCount, event.ActiveOrderCount,
		event.PendingActionCount, event.Decision, event.ReasonCode, recordSHA, string(recordJSON), event.RecordedAt)
	return err
}

func TestG38PaperEvaluationFailsClosedOnOrderCorruption(t *testing.T) {
	svc, signal, _ := g38c2PaperSignalFixture(t, true)
	lease := mustK2CLease(t, svc, k2aAccountRef)
	signal.SignalID, signal.TargetQuantity = "paper-evaluation-corrupt-order", "1"
	if _, _, err := svc.admitPaperSignal(context.Background(), k2aAccountRef, signal, lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER order_events_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE order_events SET event_sha256=? WHERE event_type='SUBMIT_ACKNOWLEDGED'`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.evaluatePaperOperations(context.Background(), k2aAccountRef, signal.StrategyResultSHA256, signal.StrategySelectionEventID); err == nil {
		t.Fatal("paper evaluation accepted a corrupt order log")
	}
}

func TestG38PaperEvaluationBackupAndLegacySchema13Migration(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	evidence, selected := selectedPaperStrategy(t, svc)
	if _, err := svc.evaluatePaperOperations(context.Background(), k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID); err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "paper-evaluation.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v11" || manifest.SchemaVersion != "omni-folio.sqlite.v17" ||
		manifest.PaperEvaluationEventCount != 1 || manifest.StrategyRegistrySHA256 == "" {
		t.Fatalf("backup omitted paper evaluation proof: %+v", manifest)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}
	missingCount := readJSONMap(t, manifestPath)
	delete(missingCount, "paper_evaluation_event_count")
	missingCountPath := filepath.Join(t.TempDir(), "missing-paper-count.manifest.json")
	writeJSONFile(t, missingCountPath, missingCount)
	if err := verifyManifest(backup, golden, missingCountPath); err == nil {
		t.Fatal("current manifest without paper evaluation count was accepted")
	}
	candidate, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`DROP TRIGGER paper_evaluation_events_state_guard`); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err == nil {
		t.Fatal("restore accepted a paper evaluation log without its state guard")
	}

	legacySvc, _ := testService(t, nil, nil)
	selectedPaperStrategy(t, legacySvc)
	legacyGolden := writeCurrentSnapshot(t, legacySvc.db)
	currentBackup := filepath.Join(t.TempDir(), "current-empty.db")
	currentManifestPath := currentBackup + ".manifest.json"
	if _, err := createBackup(legacySvc.db, currentBackup, legacyGolden, currentManifestPath, legacySvc.now, legacySvc.id); err != nil {
		t.Fatal(err)
	}
	downgradePaperMarketSignalsForTest(t, legacySvc.db)
	downgradePaperEvaluationForTest(t, legacySvc.db)
	var legacySelectionCount int
	if err := legacySvc.db.QueryRow(`SELECT COUNT(*) FROM strategy_selection_events`).Scan(&legacySelectionCount); err != nil || legacySelectionCount != 1 {
		t.Fatalf("legacy schema13 strategy selection count=%d, want 1: %v", legacySelectionCount, err)
	}
	legacyBackup := filepath.Join(t.TempDir(), "legacy-schema13.db")
	if _, err := legacySvc.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(legacyBackup, "'", "''") + `'`); err != nil {
		t.Fatal(err)
	}
	legacyManifest := readJSONMap(t, currentManifestPath)
	legacyManifest["format_version"] = "omni-folio-backup.v8"
	legacyManifest["schema_version"] = "omni-folio.sqlite.v13"
	delete(legacyManifest, "paper_evaluation_event_count")
	delete(legacyManifest, "paper_accounting_state_sha256")
	delete(legacyManifest, "paper_accounting_session_count")
	delete(legacyManifest, "paper_market_bar_observation_count")
	delete(legacyManifest, "paper_signal_event_count")
	delete(legacyManifest, "paper_execution_authorization_count")
	delete(legacyManifest, "paper_capitalized_fill_count")
	delete(legacyManifest["verification_receipt"].(map[string]any), "candidate_paper_accounting_state_sha256")
	legacySHA, legacySize, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	legacyManifest["db_sha256"] = legacySHA
	legacyManifest["size_bytes"] = legacySize
	legacyManifest["verification_receipt"].(map[string]any)["candidate_db_sha256"] = legacySHA
	legacyManifestPath := filepath.Join(t.TempDir(), "legacy-schema13.manifest.json")
	writeJSONFile(t, legacyManifestPath, legacyManifest)
	beforeSHA, beforeSize, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(legacyBackup, legacyGolden, legacyManifestPath); err != nil {
		t.Fatal(err)
	}
	afterSHA, afterSize, err := hashFile(legacyBackup)
	if err != nil || beforeSHA != afterSHA || beforeSize != afterSize {
		t.Fatalf("legacy schema13 source changed: before=(%s,%d) after=(%s,%d) err=%v", beforeSHA, beforeSize, afterSHA, afterSize, err)
	}
}

func downgradePaperEvaluationForTest(t testing.TB, db *sql.DB) {
	t.Helper()
	downgradePaperAccountingForTest(t, db)
	legacyMigration, err := migrationFiles.ReadFile("migrations/006_strategy_registry.sql")
	if err != nil {
		t.Fatal(err)
	}
	const triggerPrefix = "CREATE TRIGGER strategy_selection_events_state_guard"
	triggerStart := strings.Index(string(legacyMigration), triggerPrefix)
	if triggerStart < 0 {
		t.Fatal("legacy strategy selection state guard is missing")
	}
	if _, err := db.Exec(`DROP TRIGGER paper_evaluation_events_no_update;
		DROP TRIGGER paper_evaluation_events_no_delete;
		DROP TRIGGER paper_evaluation_events_state_guard;
		DROP TABLE paper_evaluation_events;
		DROP TRIGGER strategy_selection_events_state_guard;
		ALTER TABLE strategy_selection_events DROP COLUMN paper_evaluation_sequence`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(legacyMigration[triggerStart:])); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version=14`); err != nil {
		t.Fatal(err)
	}
}

func selectedPaperStrategy(t *testing.T, svc *Service) (*StrategyEvidence, *StrategySelectionState) {
	t.Helper()
	evidence, err := svc.registerStrategyEvidence(context.Background(), strategyArtifact(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, noStrategySelectionEvent)
	if err != nil {
		t.Fatal(err)
	}
	return evidence, selected
}

func paperEvaluationSignal(resultSHA, selectionEventID, signalID string) PaperSignal {
	return PaperSignal{
		SchemaVersion: paperSignalSchema, SignalID: signalID,
		StrategyResultSHA256: resultSHA, StrategySelectionEventID: selectionEventID,
		DataSHA256: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Symbol:     "005930", TargetQuantity: "1", DataAsOf: "2026-01-10T14:59:00Z",
		GeneratedAt: "2026-01-10T14:59:01Z", ExpiresAt: "2026-01-10T15:01:00Z",
	}
}
