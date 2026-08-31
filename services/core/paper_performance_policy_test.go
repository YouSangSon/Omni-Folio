package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestG38ELegacySchema19BaseReplaysUseOldColumnShapeAndDigest(t *testing.T) {
	ctx := context.Background()
	db := g38ELegacySchema19BaseReplayDB(t)
	defer db.Close()

	record := executionAuthorityRecord{
		EventID: "execution_authority_v19", AccountRef: k2aAccountRef, Armed: true,
		FencingToken: 1, ReasonCode: "manual_arm", RecordedAt: "2026-01-10T00:00:00Z",
	}
	raw, recordSHA, err := orderJSONHash(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO execution_authority_events(
		sequence,event_id,account_ref,armed,lease_owner,fencing_token,lease_expires_at,reason_code,record_sha256,record_json,recorded_at
	) VALUES(1,?,?,?,?,?,?,?,?,?,?)`, record.EventID, record.AccountRef, 1, nil, record.FencingToken, nil,
		record.ReasonCode, recordSHA, string(raw), record.RecordedAt); err != nil {
		t.Fatal(err)
	}

	strategy, err := proveStrategyRegistryRecovery(ctx, db)
	if err != nil || strategy.SHA256 != emptySHA256 || strategy.Events != 0 || strategy.CurrentEventID != noStrategySelectionEvent {
		t.Fatalf("schema19 strategy proof=%+v err=%v", strategy, err)
	}
	authoritySHA, authorityCount, err := proveExecutionAuthorityRecovery(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if err := json.NewEncoder(hash).Encode([]any{"execution_authority_events", int64(1), record.AccountRef, record.EventID, 1,
		sql.NullString{}, record.FencingToken, sql.NullString{}, record.ReasonCode, recordSHA, string(raw), record.RecordedAt}); err != nil {
		t.Fatal(err)
	}
	if authorityCount != 1 || authoritySHA != hex.EncodeToString(hash.Sum(nil)) {
		t.Fatalf("schema19 authority proof=(%s,%d), want historical digest", authoritySHA, authorityCount)
	}
}

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func g38ELegacySchema19BaseReplayDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "g38e-schema19-base.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE schema_migrations(version INTEGER NOT NULL, applied_at TEXT NOT NULL);
		INSERT INTO schema_migrations(version,applied_at) VALUES(19,'2026-01-10T00:00:00Z');
		CREATE TABLE execution_authority_events(
			sequence INTEGER PRIMARY KEY,event_id TEXT NOT NULL,account_ref TEXT NOT NULL,armed INTEGER NOT NULL,
			lease_owner TEXT,fencing_token INTEGER NOT NULL,lease_expires_at TEXT,reason_code TEXT NOT NULL,
			record_sha256 TEXT NOT NULL,record_json TEXT NOT NULL,recorded_at TEXT NOT NULL
		);
		CREATE TABLE strategy_research_evidence(
			sequence INTEGER PRIMARY KEY,result_sha256 TEXT NOT NULL,artifact_sha256 TEXT NOT NULL,strategy_name TEXT NOT NULL,
			strategy_version TEXT NOT NULL,parameter_sha256 TEXT NOT NULL,target TEXT NOT NULL,artifact_json TEXT NOT NULL,recorded_at TEXT NOT NULL
		);
		CREATE TABLE strategy_selection_events(
			sequence INTEGER PRIMARY KEY,event_id TEXT NOT NULL,event_type TEXT NOT NULL,candidate_result_sha256 TEXT,
			expected_current_event_id TEXT NOT NULL,source_event_id TEXT,previous_selected_result_sha256 TEXT NOT NULL,
			selected_result_sha256 TEXT NOT NULL,reason_code TEXT NOT NULL,paper_evaluation_sequence INTEGER NOT NULL,
			record_sha256 TEXT NOT NULL,record_json TEXT NOT NULL,recorded_at TEXT NOT NULL
		);
		CREATE TABLE paper_evaluation_events(
			sequence INTEGER PRIMARY KEY,evaluation_id TEXT NOT NULL,schema_version TEXT NOT NULL,policy_version TEXT NOT NULL,
			account_ref TEXT NOT NULL,strategy_result_sha256 TEXT NOT NULL,strategy_selection_event_id TEXT NOT NULL,
			expected_previous_evaluation_id TEXT NOT NULL,paper_order_state_sha256 TEXT NOT NULL,order_count INTEGER NOT NULL,
			terminal_order_count INTEGER NOT NULL,active_order_count INTEGER NOT NULL,pending_action_count INTEGER NOT NULL,
			decision TEXT NOT NULL,reason_code TEXT NOT NULL,record_sha256 TEXT NOT NULL,record_json TEXT NOT NULL,recorded_at TEXT NOT NULL
		);`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

func TestG38EPolicyRecordsNoActionDecisionsAndRejectsStaleEvidence(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name          string
		closes        []string
		wantDecision  string
		wantReason    string
		makeSelection bool
	}{
		{"insufficient", []string{"100"}, "INSUFFICIENT", "minimum_same_selection_samples_not_met", false},
		{"hold", []string{"100", "100"}, "HOLD", "within_local_paper_safety_bounds", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, performance := g38EPerformanceWindow(t, test.closes)
			before := g38EJournalCounts(t, svc)
			event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
				performance.StrategySelectionEventID, performance.StrategyPerformanceID)
			if err != nil || event.Decision != test.wantDecision || event.ReasonCode != test.wantReason ||
				event.AutomaticHaltCount != 0 || event.RollbackSelectionEventID != "" {
				t.Fatalf("event=%+v err=%v", event, err)
			}
			after := g38EJournalCounts(t, svc)
			if after.Policy != before.Policy+1 || after.Authority != before.Authority || after.Selection != before.Selection {
				t.Fatalf("no-action counts before=%+v after=%+v", before, after)
			}
		})
	}

	t.Run("stale and cross-selection evidence leave no policy event", func(t *testing.T) {
		svc, performance := g38EPerformanceWindow(t, []string{"100", "100"})
		before := g38EJournalCounts(t, svc)
		if _, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef, "strategy_selection_stale", performance.StrategyPerformanceID); err == nil {
			t.Fatal("stale selection was accepted")
		}
		artifact := rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(result map[string]any) {
			result["experiment_id"] = "g38e-cross-selection"
		})
		evidence, err := svc.registerStrategyEvidence(ctx, artifact)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.selectPaperCandidate(ctx, evidence.ResultSHA256, performance.StrategySelectionEventID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
			performance.StrategySelectionEventID, performance.StrategyPerformanceID); err == nil {
			t.Fatal("superseded selection was accepted")
		}
		after := g38EJournalCounts(t, svc)
		if after.Policy != before.Policy || after.Authority != before.Authority || after.Selection != before.Selection+1 {
			t.Fatalf("stale side effects before=%+v after=%+v", before, after)
		}
	})
}

func TestG38EPolicyAtomicallyHaltsAllArmedAccountsAndRollsBack(t *testing.T) {
	ctx := context.Background()
	svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
	secondAccount := "kiwoom_account_BBBBBBBBBBBBBBBBBBBBBBBB"
	if _, err := svc.setSyntheticExecutionArmed(ctx, secondAccount, true); err != nil {
		t.Fatal(err)
	}
	before := g38EJournalCounts(t, svc)
	event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID)
	if err != nil || event.Decision != "HALT_AND_ROLLBACK" || event.ReasonCode != "max_drawdown_limit_reached" ||
		event.AutomaticHaltCount != 2 || event.RollbackSelectionEventID == "" {
		t.Fatalf("event=%+v err=%v", event, err)
	}
	after := g38EJournalCounts(t, svc)
	if after.Policy != before.Policy+1 || after.Authority != before.Authority+2 || after.Selection != before.Selection+1 {
		t.Fatalf("action counts before=%+v after=%+v", before, after)
	}
	rows, err := svc.db.Query(`SELECT account_ref,reason_code,paper_performance_policy_event_id
		FROM execution_authority_events WHERE paper_performance_policy_event_id=? ORDER BY sequence`, event.PolicyEventID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var accounts []string
	for rows.Next() {
		var account, reason, policyID string
		if err := rows.Scan(&account, &reason, &policyID); err != nil {
			t.Fatal(err)
		}
		if reason != "automatic_performance_halt" || policyID != event.PolicyEventID {
			t.Fatalf("automatic halt provenance account=%s reason=%s policy=%s", account, reason, policyID)
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil || len(accounts) != 2 || accounts[0] >= accounts[1] {
		t.Fatalf("automatic halt lexical order=%v err=%v", accounts, err)
	}
	var rollbackReason, rollbackPolicyID string
	if err := svc.db.QueryRow(`SELECT reason_code,paper_performance_policy_event_id FROM strategy_selection_events WHERE event_id=?`,
		event.RollbackSelectionEventID).Scan(&rollbackReason, &rollbackPolicyID); err != nil ||
		rollbackReason != "automatic_performance_rollback" || rollbackPolicyID != event.PolicyEventID {
		t.Fatalf("rollback provenance reason=%s policy=%s err=%v", rollbackReason, rollbackPolicyID, err)
	}
	for _, account := range accounts {
		state, err := loadExecutionAuthoritySnapshot(ctx, svc.db, account)
		if err != nil || state.Armed || state.LeaseOwner != "" || state.LeaseExpiresAt != "" {
			t.Fatalf("halted authority account=%s state=%+v err=%v", account, state, err)
		}
	}
	state, err := replayStrategyRegistry(ctx, svc.db)
	if err != nil || state.CurrentEventID != event.RollbackSelectionEventID || state.SelectedResultSHA256 != noStrategySelection {
		t.Fatalf("rollback state=%+v err=%v", state, err)
	}
	retry, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID)
	if err != nil || retry.PolicyEventID != event.PolicyEventID || g38EJournalCounts(t, svc) != after {
		t.Fatalf("retry=%+v err=%v counts=%+v want=%+v", retry, err, g38EJournalCounts(t, svc), after)
	}
}

func TestG38EPolicyActionUsesOneProvenanceTime(t *testing.T) {
	ctx := context.Background()
	svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
	base := mustTime("2026-01-16T07:00:00Z")
	calls := 0
	svc.now = func() time.Time {
		value := base.Add(time.Duration(calls) * time.Second)
		calls++
		return value
	}
	event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID)
	if err != nil || event.Decision != "HALT_AND_ROLLBACK" {
		t.Fatalf("event=%+v err=%v", event, err)
	}
	wantRecordedAt := base.Format(canonicalPaperTimeLayout)
	if calls != 1 || event.RecordedAt != wantRecordedAt {
		t.Fatalf("policy clock calls=%d recorded_at=%s want=%s", calls, event.RecordedAt, wantRecordedAt)
	}
	assertActionTime := func(name, recordedAt string, err error) {
		t.Helper()
		value, parseErr := time.Parse(time.RFC3339Nano, recordedAt)
		if err != nil || parseErr != nil || !value.Equal(base) {
			t.Fatalf("%s recorded_at=%s want instant=%s errors=(%v,%v)", name, recordedAt, wantRecordedAt, err, parseErr)
		}
	}
	rows, err := svc.db.Query(`SELECT recorded_at FROM execution_authority_events WHERE paper_performance_policy_event_id=?`, event.PolicyEventID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var recordedAt string
		if err := rows.Scan(&recordedAt); err != nil {
			t.Fatal(err)
		}
		assertActionTime("automatic halt", recordedAt, nil)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var rollbackRecordedAt string
	if err := svc.db.QueryRow(`SELECT recorded_at FROM strategy_selection_events WHERE event_id=?`, event.RollbackSelectionEventID).Scan(&rollbackRecordedAt); err != nil {
		t.Fatal(err)
	}
	assertActionTime("rollback", rollbackRecordedAt, nil)
}

func TestG38EPolicyRollsBackWithoutArmedAuthority(t *testing.T) {
	ctx := context.Background()
	svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
	if _, err := svc.setSyntheticExecutionArmed(ctx, k2aAccountRef, false); err != nil {
		t.Fatal(err)
	}
	before := g38EJournalCounts(t, svc)
	event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID)
	if err != nil || event.Decision != "HALT_AND_ROLLBACK" || event.AutomaticHaltCount != 0 || event.RollbackSelectionEventID == "" {
		t.Fatalf("event=%+v err=%v", event, err)
	}
	after := g38EJournalCounts(t, svc)
	if after.Policy != before.Policy+1 || after.Authority != before.Authority || after.Selection != before.Selection+1 {
		t.Fatalf("zero-armed action before=%+v after=%+v", before, after)
	}
}

func TestG38EPolicyActionFailureRollsBackEveryJournal(t *testing.T) {
	for _, target := range []string{"paper_performance_policy_events", "execution_authority_events", "strategy_selection_events"} {
		t.Run(target, func(t *testing.T) {
			svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
			before := g38EJournalCounts(t, svc)
			if _, err := svc.db.Exec(`CREATE TRIGGER g38e_forced_failure BEFORE INSERT ON ` + target +
				` BEGIN SELECT RAISE(ABORT, 'forced G3.8E failure'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.applyPaperPerformancePolicy(context.Background(), k2aAccountRef,
				performance.StrategySelectionEventID, performance.StrategyPerformanceID); err == nil {
				t.Fatal("forced action failure was accepted")
			}
			if after := g38EJournalCounts(t, svc); after != before {
				t.Fatalf("partial action write before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestG38EPolicyConcurrentIdenticalCallsConverge(t *testing.T) {
	primary, performance := g38EPerformanceWindow(t, []string{"100", "100"})
	secondary := secondG38C2Service(t, primary, mustTime("2026-01-15T07:00:00Z"))
	start := make(chan struct{})
	results := make(chan *PaperPerformancePolicyEvent, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, svc := range []*Service{primary, secondary} {
		wg.Add(1)
		go func(svc *Service) {
			defer wg.Done()
			<-start
			event, err := svc.applyPaperPerformancePolicy(context.Background(), k2aAccountRef,
				performance.StrategySelectionEventID, performance.StrategyPerformanceID)
			results <- event
			errs <- err
		}(svc)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	var policyID string
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for event := range results {
		if event == nil || (policyID != "" && policyID != event.PolicyEventID) {
			t.Fatalf("concurrent policy results diverged: prior=%s event=%+v", policyID, event)
		}
		policyID = event.PolicyEventID
	}
	if g38EJournalCounts(t, primary).Policy != 1 {
		t.Fatalf("concurrent policy rows=%+v", g38EJournalCounts(t, primary))
	}
}

func TestG38EPolicyRootRecoveryRejectsTamperedPolicyAndNeverReturnsCorruptRetry(t *testing.T) {
	ctx := context.Background()
	svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
	event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := provePaperPerformancePolicyRecovery(ctx, svc.db)
	if err != nil || proof.Events != 1 || proof.Actions != 1 || proof.AutomaticHalts != event.AutomaticHaltCount || proof.SHA256 == "" {
		t.Fatalf("policy proof=%+v err=%v", proof, err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER paper_performance_policy_events_no_update;
		UPDATE paper_performance_policy_events SET record_sha256=? WHERE policy_event_id=?`, emptySHA256, event.PolicyEventID); err != nil {
		t.Fatal(err)
	}
	if _, err := provePaperPerformancePolicyRecovery(ctx, svc.db); err == nil {
		t.Fatal("policy recovery certified a tampered policy row")
	}
	before := g38EJournalCounts(t, svc)
	if _, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID); err == nil {
		t.Fatal("corrupt policy retry was returned")
	}
	if after := g38EJournalCounts(t, svc); after != before {
		t.Fatalf("corrupt retry appended journal rows before=%+v after=%+v", before, after)
	}
}

func TestG38EStartupRejectsTamperedPolicyRecovery(t *testing.T) {
	ctx := context.Background()
	svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
	event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER paper_performance_policy_events_no_update;
		UPDATE paper_performance_policy_events SET record_sha256=? WHERE policy_event_id=?`, emptySHA256, event.PolicyEventID); err != nil {
		t.Fatal(err)
	}
	if err := requireServerStartupRecovery(svc.db); err == nil {
		t.Fatal("pre-listen startup acceptance certified a corrupt G3.8E policy row")
	}
}

func TestG38ESchema20PublicLinkedJournalWritersRejectTamperedPolicy(t *testing.T) {
	ctx := context.Background()
	svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
	event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER paper_performance_policy_events_no_update;
		UPDATE paper_performance_policy_events SET record_sha256=? WHERE policy_event_id=?`, emptySHA256, event.PolicyEventID); err != nil {
		t.Fatal(err)
	}
	before := g38EJournalCounts(t, svc)
	var evidenceBefore int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM strategy_research_evidence`).Scan(&evidenceBefore); err != nil {
		t.Fatal(err)
	}
	artifact := rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(result map[string]any) { result["experiment_id"] = "g38e-root-writer" })
	if _, err := svc.registerStrategyEvidence(ctx, artifact); err == nil {
		t.Fatal("schema20 strategy evidence writer accepted a corrupt linked policy")
	}
	if _, err := svc.setSyntheticExecutionArmed(ctx, k2aAccountRef, true); err == nil {
		t.Fatal("schema20 authority arm accepted a corrupt linked policy")
	}
	if _, err := svc.acquireSyntheticExecutionLease(ctx, k2aAccountRef); err == nil {
		t.Fatal("schema20 authority lease accepted a corrupt linked policy")
	}
	var evidenceAfter int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM strategy_research_evidence`).Scan(&evidenceAfter); err != nil {
		t.Fatal(err)
	}
	if after := g38EJournalCounts(t, svc); after != before || evidenceAfter != evidenceBefore {
		t.Fatalf("corrupt public writer appended rows journal=%+v/%+v evidence=%d/%d", before, after, evidenceBefore, evidenceAfter)
	}
}

func TestG38ERestoreSchemaPinsPolicyTableDefinitionAndSequence(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *sql.DB){
		"weakened policy check": func(t *testing.T, db *sql.DB) {
			driftSQLiteTableSQLForTest(t, db, "paper_performance_policy_events",
				"sample_count INTEGER NOT NULL CHECK (sample_count > 0)", "sample_count INTEGER NOT NULL")
		},
		"policy sequence is not primary key": func(t *testing.T, db *sql.DB) {
			driftSQLiteTableSQLForTest(t, db, "paper_performance_policy_events",
				"sequence INTEGER PRIMARY KEY", "sequence INTEGER NOT NULL")
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			if err := requireOrderRestoreSchema(svc.db); err != nil {
				t.Fatalf("baseline schema rejected: %v", err)
			}
			mutate(t, svc.db)
			if err := requireOrderRestoreSchema(svc.db); err == nil {
				t.Fatal("restore schema accepted weakened policy table definition")
			}
		})
	}
}

func TestG38EPolicyUsesGlobalStrategyPerformanceCutoffAcrossAccounts(t *testing.T) {
	ctx := context.Background()
	svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
	other := g38ELaterOtherAccountStrategyPerformance(t, svc, performance)
	var globalCutoff int64
	if err := svc.db.QueryRow(`SELECT MAX(sequence) FROM paper_strategy_performance_events`).Scan(&globalCutoff); err != nil {
		t.Fatal(err)
	}
	if globalCutoff <= 0 || other.StrategyPerformanceID == performance.StrategyPerformanceID {
		t.Fatalf("invalid cross-account G3.8D fixture cutoff=%d target=%s other=%s", globalCutoff, performance.StrategyPerformanceID, other.StrategyPerformanceID)
	}
	event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID)
	if err != nil || event.PaperStrategyPerformanceSequenceCutoff != globalCutoff {
		t.Fatalf("policy=%+v want global strategy-performance cutoff=%d err=%v", event, globalCutoff, err)
	}
	if _, err := provePaperPerformancePolicyRecovery(ctx, svc.db); err != nil {
		t.Fatalf("cross-account global cutoff policy replay failed: %v", err)
	}
}

func TestG38EPolicyRootRecoveryRejectsCanonicalRecordedCutoffDowngrade(t *testing.T) {
	ctx := context.Background()
	svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
	_ = g38ELaterOtherAccountStrategyPerformance(t, svc, performance)
	var globalCutoff, targetCutoff int64
	if err := svc.db.QueryRow(`SELECT MAX(sequence) FROM paper_strategy_performance_events`).Scan(&globalCutoff); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT sequence FROM paper_strategy_performance_events WHERE strategy_performance_id=?`, performance.StrategyPerformanceID).Scan(&targetCutoff); err != nil {
		t.Fatal(err)
	}
	if targetCutoff >= globalCutoff {
		t.Fatalf("fixture cutoff target=%d global=%d", targetCutoff, globalCutoff)
	}
	event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID)
	if err != nil || event.PaperStrategyPerformanceSequenceCutoff != globalCutoff {
		t.Fatalf("policy=%+v global cutoff=%d err=%v", event, globalCutoff, err)
	}
	mutated := *event
	mutated.PaperStrategyPerformanceSequenceCutoff = targetCutoff
	recordJSON, recordSHA, err := orderJSONHash(mutated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER paper_performance_policy_events_no_update;
		UPDATE paper_performance_policy_events
		SET paper_strategy_performance_sequence_cutoff=?,record_json=?,record_sha256=?
		WHERE policy_event_id=?`, targetCutoff, string(recordJSON), recordSHA, event.PolicyEventID); err != nil {
		t.Fatal(err)
	}
	if _, err := provePaperPerformancePolicyRecovery(ctx, svc.db); err == nil {
		t.Fatal("policy root accepted a canonical rewrite that downgraded the global strategy-performance cutoff")
	}
}

func TestG38EPolicyRootRecoveryBindsAutomaticActionProvenanceTime(t *testing.T) {
	ctx := context.Background()
	for name, mutate := range map[string]func(*testing.T, *Service, *PaperPerformancePolicyEvent){
		"halt": func(t *testing.T, svc *Service, event *PaperPerformancePolicyEvent) {
			var record executionAuthorityRecord
			var raw string
			if err := svc.db.QueryRow(`SELECT record_json FROM execution_authority_events WHERE paper_performance_policy_event_id=? ORDER BY sequence LIMIT 1`, event.PolicyEventID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(raw), &record); err != nil {
				t.Fatal(err)
			}
			record.RecordedAt = "2026-01-16T07:00:01Z"
			canonical, hash, err := orderJSONHash(record)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.db.Exec(`DROP TRIGGER execution_authority_events_no_update;
				UPDATE execution_authority_events SET record_sha256=?,record_json=?,recorded_at=? WHERE event_id=?`, hash, string(canonical), record.RecordedAt, record.EventID); err != nil {
				t.Fatal(err)
			}
		},
		"rollback": func(t *testing.T, svc *Service, event *PaperPerformancePolicyEvent) {
			var record StrategySelectionEvent
			var raw string
			if err := svc.db.QueryRow(`SELECT record_json FROM strategy_selection_events WHERE event_id=?`, event.RollbackSelectionEventID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(raw), &record); err != nil {
				t.Fatal(err)
			}
			record.RecordedAt = "2026-01-16T07:00:01Z"
			canonical, hash, err := orderJSONHash(record)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.db.Exec(`DROP TRIGGER strategy_selection_events_no_update;
				UPDATE strategy_selection_events SET record_sha256=?,record_json=?,recorded_at=? WHERE event_id=?`, hash, string(canonical), record.RecordedAt, record.EventID); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
			event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
				performance.StrategySelectionEventID, performance.StrategyPerformanceID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provePaperPerformancePolicyRecovery(ctx, svc.db); err != nil {
				t.Fatalf("baseline policy proof failed: %v", err)
			}
			mutate(t, svc, event)
			if _, err := provePaperPerformancePolicyRecovery(ctx, svc.db); err == nil {
				t.Fatal("policy root accepted a canonically rehashed action with a different provenance instant")
			}
		})
	}
}

func TestG38EPolicyRootRejectsCanonicalMutationMatrix(t *testing.T) {
	ctx := context.Background()
	secondAccount := "kiwoom_account_BBBBBBBBBBBBBBBBBBBBBBBB"
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *Service, *PaperPerformancePolicyEvent)
	}{
		{
			name: "decision and reason",
			mutate: func(t *testing.T, svc *Service, event *PaperPerformancePolicyEvent) {
				mutated := *event
				mutated.Decision = "HOLD"
				mutated.ReasonCode = "within_local_paper_safety_bounds"
				mutated.RollbackSelectionEventID = ""
				mutated.AutomaticHaltCount = 0
				g38ERewritePolicyEvent(t, svc, mutated)
			},
		},
		{
			name: "latest G3.8D source link",
			mutate: func(t *testing.T, svc *Service, event *PaperPerformancePolicyEvent) {
				mutated := *event
				mutated.LatestPerformanceID = mutated.BaselinePerformanceID
				g38ERewritePolicyEvent(t, svc, mutated)
			},
		},
		{
			name: "sample count",
			mutate: func(t *testing.T, svc *Service, event *PaperPerformancePolicyEvent) {
				mutated := *event
				mutated.SampleCount++
				g38ERewritePolicyEvent(t, svc, mutated)
			},
		},
		{
			name: "authority fencing token",
			mutate: func(t *testing.T, svc *Service, event *PaperPerformancePolicyEvent) {
				var raw string
				if err := svc.db.QueryRow(`SELECT record_json FROM execution_authority_events
					WHERE paper_performance_policy_event_id=? ORDER BY sequence LIMIT 1`, event.PolicyEventID).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var record executionAuthorityRecord
				if err := json.Unmarshal([]byte(raw), &record); err != nil {
					t.Fatal(err)
				}
				record.FencingToken += 10
				canonical, hash, err := orderJSONHash(record)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := svc.db.Exec(`DROP TRIGGER execution_authority_events_no_update;
					UPDATE execution_authority_events SET fencing_token=?,record_sha256=?,record_json=? WHERE event_id=?`,
					record.FencingToken, hash, string(canonical), record.EventID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "automatic halt coverage",
			mutate: func(t *testing.T, svc *Service, event *PaperPerformancePolicyEvent) {
				mutated := *event
				mutated.AutomaticHaltCount--
				g38ERewritePolicyEvent(t, svc, mutated)
			},
		},
		{
			name: "automatic rollback source",
			mutate: func(t *testing.T, svc *Service, event *PaperPerformancePolicyEvent) {
				var raw string
				if err := svc.db.QueryRow(`SELECT record_json FROM strategy_selection_events WHERE event_id=?`, event.RollbackSelectionEventID).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var record StrategySelectionEvent
				if err := json.Unmarshal([]byte(raw), &record); err != nil {
					t.Fatal(err)
				}
				record.SourceEventID = record.EventID
				canonical, hash, err := orderJSONHash(record)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := svc.db.Exec(`DROP TRIGGER strategy_selection_events_no_update;
					UPDATE strategy_selection_events SET source_event_id=?,record_sha256=?,record_json=? WHERE event_id=?`,
					record.SourceEventID, hash, string(canonical), record.EventID); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
			if _, err := svc.setSyntheticExecutionArmed(ctx, secondAccount, true); err != nil {
				t.Fatal(err)
			}
			event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
				performance.StrategySelectionEventID, performance.StrategyPerformanceID)
			if err != nil || event.Decision != "HALT_AND_ROLLBACK" || event.AutomaticHaltCount != 2 {
				t.Fatalf("policy=%+v err=%v", event, err)
			}
			if _, err := provePaperPerformancePolicyRecovery(ctx, svc.db); err != nil {
				t.Fatalf("baseline policy recovery failed: %v", err)
			}
			test.mutate(t, svc, event)
			if _, err := provePaperPerformancePolicyRecovery(ctx, svc.db); err == nil {
				t.Fatal("policy root accepted a canonically rehashed mutation")
			}
		})
	}
}

func g38ERewritePolicyEvent(t testing.TB, svc *Service, event PaperPerformancePolicyEvent) {
	t.Helper()
	canonical, hash, err := orderJSONHash(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER paper_performance_policy_events_no_update;
		UPDATE paper_performance_policy_events SET
		schema_version=?,policy_version=?,account_ref=?,paper_accounting_session_id=?,strategy_selection_event_id=?,
		selected_strategy_result_ref=?,strategy_performance_id=?,baseline_performance_id=?,latest_performance_id=?,sample_count=?,
		expected_previous_policy_event_id=?,strategy_selection_sequence_cutoff=?,paper_strategy_performance_sequence_cutoff=?,
		execution_authority_sequence_cutoff=?,decision=?,reason_code=?,rollback_selection_event_id=?,automatic_halt_count=?,
		record_sha256=?,record_json=?,recorded_at=? WHERE policy_event_id=?`,
		event.SchemaVersion, event.PolicyVersion, event.AccountRef, event.PaperAccountingSessionID, event.StrategySelectionEventID,
		event.SelectedStrategyResultRef, event.StrategyPerformanceID, event.BaselinePerformanceID, event.LatestPerformanceID, event.SampleCount,
		event.ExpectedPreviousPolicyEventID, event.StrategySelectionSequenceCutoff, event.PaperStrategyPerformanceSequenceCutoff,
		event.ExecutionAuthoritySequenceCutoff, event.Decision, event.ReasonCode, nullable(event.RollbackSelectionEventID), event.AutomaticHaltCount,
		hash, string(canonical), event.RecordedAt, event.PolicyEventID); err != nil {
		t.Fatal(err)
	}
}

func TestG38ESyntheticPublicWritersRejectTamperedAutomaticPolicy(t *testing.T) {
	ctx := context.Background()
	svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
	order := mustRecordK2AOrder(t, svc, "g38e-corrupt-policy-dispatch")
	event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER paper_performance_policy_events_no_update;
		UPDATE paper_performance_policy_events SET record_sha256=? WHERE policy_event_id=?`, emptySHA256, event.PolicyEventID); err != nil {
		t.Fatal(err)
	}
	before := g38EJournalCounts(t, svc)
	var reservationsBefore, ordersBefore int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM risk_reservations`).Scan(&reservationsBefore); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_idempotency`).Scan(&ordersBefore); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.authorizeSyntheticDispatchOnce(ctx, order.OrderID, 1); err == nil ||
		!strings.Contains(err.Error(), "execution authority recovery") {
		t.Fatalf("synthetic dispatch did not fail closed on corrupt automatic policy: %v", err)
	}
	if _, err := svc.recordPaperTarget(ctx, k2aAccountRef, PaperSignal{}, "", nil, time.Time{}, time.Time{}, time.Time{}, 0); err == nil ||
		!strings.Contains(err.Error(), "execution authority recovery") {
		t.Fatalf("paper target did not fail closed on corrupt automatic policy: %v", err)
	}
	var reservationsAfter, ordersAfter int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM risk_reservations`).Scan(&reservationsAfter); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM order_idempotency`).Scan(&ordersAfter); err != nil {
		t.Fatal(err)
	}
	if after := g38EJournalCounts(t, svc); after != before || reservationsAfter != reservationsBefore || ordersAfter != ordersBefore {
		t.Fatalf("corrupt synthetic writer appended rows journal=%+v/%+v reservations=%d/%d orders=%d/%d",
			before, after, reservationsBefore, reservationsAfter, ordersBefore, ordersAfter)
	}
}

func TestG38EAutomaticHaltIDsAreDerivedFromPolicyAndAccount(t *testing.T) {
	ctx := context.Background()
	svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
	secondAccount := "kiwoom_account_BBBBBBBBBBBBBBBBBBBBBBBB"
	if _, err := svc.setSyntheticExecutionArmed(ctx, secondAccount, true); err != nil {
		t.Fatal(err)
	}
	event, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := svc.db.Query(`SELECT account_ref,event_id FROM execution_authority_events
		WHERE paper_performance_policy_event_id=? ORDER BY account_ref`, event.PolicyEventID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var accountRef, eventID string
		if err := rows.Scan(&accountRef, &eventID); err != nil {
			t.Fatal(err)
		}
		if want := paperPerformanceAutomaticHaltID(event.PolicyEventID, accountRef); eventID != want {
			t.Fatalf("automatic halt ID=%q want %q", eventID, want)
		}
		count++
	}
	if err := rows.Err(); err != nil || count != event.AutomaticHaltCount {
		t.Fatalf("automatic halt count=%d/%d err=%v", count, event.AutomaticHaltCount, err)
	}
}

func TestG38EAuthorityRecoveryRejectsRehashedAutomaticHaltRelinkToOtherValidPolicy(t *testing.T) {
	ctx := context.Background()
	svc, firstPerformance := g38EPerformanceWindow(t, []string{"100", "10"})
	firstPolicy, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		firstPerformance.StrategySelectionEventID, firstPerformance.StrategyPerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	artifact := rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(result map[string]any) {
		result["experiment_id"] = "g38e-other-valid-halt-policy"
		result["execution"].(map[string]any)["starting_cash"] = "1000"
	})
	evidence, err := svc.registerStrategyEvidence(ctx, artifact)
	if err != nil {
		t.Fatal(err)
	}
	strategy, err := replayStrategyRegistry(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := svc.selectPaperCandidate(ctx, evidence.ResultSHA256, strategy.CurrentEventID)
	if err != nil {
		t.Fatal(err)
	}
	secondAccount := "kiwoom_account_BBBBBBBBBBBBBBBBBBBBBBBB"
	firstSecondAccountPerformance := g38EOtherAccountStrategyPerformanceAt(t, svc, &PaperStrategyPerformanceEvent{
		SelectedStrategyResultRef: evidence.ResultSHA256, StrategySelectionEventID: selection.CurrentEventID,
	}, mustTime("2026-01-16T00:00:00Z"))
	svc.now = func() time.Time { return mustTime("2026-01-19T07:00:00Z") }
	const asOf = "2026-01-19T06:30:00.000000000Z"
	recordG38C3MarkBar(t, svc, "005930", "g38e-other-valid-halt-policy-mark", asOf, "10")
	point, err := svc.evaluatePaperPerformance(ctx, secondAccount, asOf)
	if err != nil {
		t.Fatal(err)
	}
	secondPerformance, err := svc.evaluatePaperStrategyPerformance(ctx, secondAccount, point.StrategySelectionEventID, point.PerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	if firstSecondAccountPerformance.StrategySelectionEventID != secondPerformance.StrategySelectionEventID {
		t.Fatal("second policy fixture changed strategy selection")
	}
	secondPolicy, err := svc.applyPaperPerformancePolicy(ctx, secondAccount,
		secondPerformance.StrategySelectionEventID, secondPerformance.StrategyPerformanceID)
	if err != nil || secondPolicy.Decision != "HALT_AND_ROLLBACK" {
		t.Fatalf("second policy=%+v err=%v", secondPolicy, err)
	}
	var raw string
	if err := svc.db.QueryRow(`SELECT record_json FROM execution_authority_events
		WHERE paper_performance_policy_event_id=? AND account_ref=?`, secondPolicy.PolicyEventID, secondAccount).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var record executionAuthorityRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatal(err)
	}
	record.PaperPerformancePolicyEventID = firstPolicy.PolicyEventID
	canonical, hash, err := orderJSONHash(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER execution_authority_events_no_update;
		UPDATE execution_authority_events SET paper_performance_policy_event_id=?,record_sha256=?,record_json=? WHERE event_id=?`,
		firstPolicy.PolicyEventID, hash, string(canonical), record.EventID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := proveExecutionAuthorityRecovery(ctx, svc.db); err == nil {
		t.Fatal("authority replay accepted an automatic halt re-linked to another valid HALT policy")
	}
}

func TestG38EMigration20ReenablesAndRechecksForeignKeys(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *sql.DB){
		"success": func(t *testing.T, _ *sql.DB) {},
		"failure": func(t *testing.T, db *sql.DB) {
			if _, err := db.Exec(`DROP INDEX execution_authority_latest_idx`); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			downgradePaperPerformancePolicyForTest(t, svc.db)
			mutate(t, svc.db)
			err := migrate(svc.db)
			if name == "success" && err != nil {
				t.Fatal(err)
			}
			if name == "failure" && err == nil {
				t.Fatal("broken migration 020 was accepted")
			}
			var enabled, violations, version int
			if err := svc.db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil {
				t.Fatal(err)
			}
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil {
				t.Fatal(err)
			}
			if err := svc.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			wantVersion := latestSchema
			if name == "failure" {
				wantVersion = 19
			}
			if enabled != 1 || violations != 0 || version != wantVersion {
				t.Fatalf("migration %s foreign_keys=%d violations=%d version=%d wantVersion=%d", name, enabled, violations, version, wantVersion)
			}
		})
	}
}

func TestG38EMigration20UsesFixedCreateCopyDropRenameOrder(t *testing.T) {
	script, err := migrationFiles.ReadFile("migrations/020_paper_performance_policy_events.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(script)
	createAuthority := strings.Index(sql, "CREATE TABLE execution_authority_events_new")
	copyAuthority := strings.Index(sql, "INSERT INTO execution_authority_events_new")
	dropAuthority := strings.Index(sql, "DROP TABLE execution_authority_events;")
	renameAuthority := strings.Index(sql, "ALTER TABLE execution_authority_events_new RENAME TO execution_authority_events;")
	createPolicy := strings.Index(sql, "CREATE TABLE paper_performance_policy_events")
	if createAuthority < 0 || copyAuthority <= createAuthority || dropAuthority <= copyAuthority ||
		renameAuthority <= dropAuthority || createPolicy <= renameAuthority ||
		strings.Contains(sql, "RENAME TO execution_authority_events_old") ||
		strings.Contains(sql, "RENAME TO strategy_selection_events_old") {
		t.Fatal("migration 020 does not use the authoritative create/copy/drop/rename rebuild order")
	}
}

func TestG38EMigration20RejectsV19ForeignKeyInventoryDriftBeforeCommit(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100", "100"})
	downgradePaperPerformancePolicyForTest(t, svc.db)
	driftSQLiteTableSQLForTest(t, svc.db, "strategy_selection_events",
		"source_event_id TEXT REFERENCES strategy_selection_events(event_id)",
		"source_event_id TEXT REFERENCES strategy_research_evidence(result_sha256)")
	var path string
	if err := svc.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate(db); err == nil {
		t.Fatal("migration 020 accepted a v19 foreign-key inventory it did not preserve")
	}
}

func TestG38EMigration20RejectsNonRebuiltV19ForeignKeyDriftBeforeScript(t *testing.T) {
	svc, path := testService(t, nil, nil)
	downgradePaperPerformancePolicyForTest(t, svc.db)
	driftSQLiteTableSQLForTest(t, svc.db, "risk_reservations",
		"FOREIGN KEY (authority_event_id) REFERENCES execution_authority_events(event_id)",
		"FOREIGN KEY (authority_event_id) REFERENCES strategy_selection_events(event_id)")
	var expectedSourceSQL string
	if err := svc.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='risk_reservations'`).Scan(&expectedSourceSQL); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.Close(); err != nil {
		t.Fatal(err)
	}
	beforeSHA, beforeSize, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err == nil {
		_ = db.Close()
		t.Fatal("migration 020 accepted a non-rebuilt v19 child foreign-key drift")
	}
	var version int
	var sourceSQL string
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='risk_reservations'`).Scan(&sourceSQL); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	afterSHA, afterSize, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if version != 19 || sourceSQL != expectedSourceSQL || beforeSHA != afterSHA || beforeSize != afterSize {
		t.Fatalf("failed migration changed source version=%d source_sql_equal=%t db=(%s,%d)->(%s,%d)",
			version, sourceSQL == expectedSourceSQL, beforeSHA, beforeSize, afterSHA, afterSize)
	}
}

func TestG38EMigration20PinsCanonicalV19ForeignKeyInventory(t *testing.T) {
	db := g38ECanonicalV19MigrationDB(t)
	inventory, err := migration20ForeignKeyInventory(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	const want = "665f412a571d311c990d15f579608f89d91cbd8376b97dc1fdd0dd2537e41cbd"
	if migration20CanonicalV19ForeignKeyInventorySHA256 != want {
		t.Fatalf("migration 020 canonical v19 foreign-key inventory constant=%s want=%s", migration20CanonicalV19ForeignKeyInventorySHA256, want)
	}
	if got := migration20ForeignKeyInventorySHA256(inventory); got != want {
		t.Fatalf("canonical v19 foreign-key inventory SHA-256=%s want=%s", got, want)
	}
}

func g38ECanonicalV19MigrationDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "canonical-v19.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	files := []string{
		"001_init.sql", "002_orders.sql", "003_broker_snapshots.sql", "004_execution_authority.sql",
		"005_ledger_events.sql", "006_strategy_registry.sql", "007_paper_orders.sql", "008_cash_void.sql",
		"009_fx_exchange.sql", "010_fx_observations.sql", "011_security_price_observations.sql",
		"012_kiwoom_security_price_observations.sql", "013_instrument_listing_events.sql",
		"014_paper_evaluation_events.sql", "015_paper_accounting_sessions.sql", "016_paper_market_signals.sql",
		"017_paper_execution_authorizations.sql", "018_paper_performance_events.sql", "019_paper_strategy_performance_events.sql",
	}
	for index, file := range files {
		version := index + 1
		script, err := migrationFiles.ReadFile("migrations/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if version == 7 {
			if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
				t.Fatal(err)
			}
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(string(script)); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, version, "2026-01-10T00:00:00Z"); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if version >= 7 {
			var violations int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != 0 {
				_ = tx.Rollback()
				t.Fatalf("canonical v19 migration %d foreign-key check violations=%d err=%v", version, violations, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if version == 7 {
			if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
				t.Fatal(err)
			}
		}
	}
	return db
}

func TestG38EMigration20PreservesNonemptyV19JournalsProofsAndForeignKeys(t *testing.T) {
	ctx := context.Background()
	svc, performance := g38EPerformanceWindow(t, []string{"100", "100"})
	if _, err := svc.evaluatePaperOperations(ctx, k2aAccountRef, performance.SelectedStrategyResultRef, performance.StrategySelectionEventID); err != nil {
		t.Fatal(err)
	}
	order := mustRecordK2AOrder(t, svc, "g38e-migration20-risk-reservation")
	lease := mustK2CLease(t, svc, k2aAccountRef)
	mustAuthorizeK2C(t, svc, order.OrderID, lease.FencingToken)
	downgradePaperPerformancePolicyForTest(t, svc.db)
	beforeCounts := g38EMigration20Counts(t, svc.db)
	for table, count := range beforeCounts {
		if count == 0 {
			t.Fatalf("non-empty v19 fixture has no %s rows", table)
		}
	}
	before, err := captureMigration20Snapshot(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	performanceBefore, err := provePaperStrategyPerformanceRecovery(ctx, svc.db)
	if err != nil || performanceBefore.Events == 0 || performanceBefore.Samples == 0 {
		t.Fatalf("non-empty v19 G3.8D proof=%+v err=%v", performanceBefore, err)
	}
	if err := migrate(svc.db); err != nil {
		t.Fatal(err)
	}
	authorityAfter, authorityCountAfter, err := proveExecutionAuthorityRecovery(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	strategyAfter, err := proveStrategyRegistryRecovery(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	performanceAfter, err := provePaperStrategyPerformanceRecovery(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	afterCounts := g38EMigration20Counts(t, svc.db)
	afterForeignKeys, err := migration20ForeignKeyInventory(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	expectedForeignKeys, expectedForeignKeyErr := migration20ExpectedForeignKeys(before.ForeignKeys)
	expectedForeignKeys = append(expectedForeignKeys,
		migration20ForeignKey{Child: "paper_runner_leases", Parent: "strategy_selection_events", From: "strategy_selection_event_id", To: "event_id", OnUpdate: "NO ACTION", OnDelete: "NO ACTION", Match: "NONE"},
		migration20ForeignKey{Child: "paper_runner_leases", Parent: "strategy_selection_events", From: "selected_result_sha256", To: "selected_result_sha256", OnUpdate: "NO ACTION", OnDelete: "NO ACTION", Match: "NONE"},
	)
	sort.Slice(expectedForeignKeys, func(i, j int) bool {
		return migration20ForeignKeyKey(expectedForeignKeys[i]) < migration20ForeignKeyKey(expectedForeignKeys[j])
	})
	if expectedForeignKeyErr != nil ||
		authorityAfter != before.AuthoritySHA || authorityCountAfter != before.AuthorityCount ||
		!sameStrategyRegistryProof(before.Strategy, strategyAfter) || performanceBefore != performanceAfter ||
		!sameMigration20ForeignKeys(expectedForeignKeys, afterForeignKeys) ||
		!sameG38EMigration20Counts(beforeCounts, afterCounts) {
		t.Fatalf("v19 migration changed journals/proofs/counts authority=%s/%s events=%d/%d strategy=%+v/%+v performance=%+v/%+v counts=%v/%v",
			before.AuthoritySHA, authorityAfter, before.AuthorityCount, authorityCountAfter, before.Strategy, strategyAfter,
			performanceBefore, performanceAfter, beforeCounts, afterCounts)
	}
	if err := verifyMigration20TemporaryNames(ctx, svc.db, afterForeignKeys); err != nil {
		t.Fatal(err)
	}
}

func g38EMigration20Counts(t testing.TB, db *sql.DB) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for table := range map[string]bool{
		"strategy_selection_events": true, "execution_authority_events": true, "order_idempotency": true,
		"risk_reservations": true, "paper_evaluation_events": true, "paper_accounting_sessions": true,
		"paper_strategy_performance_events": true,
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts[table] = count
	}
	return counts
}

func sameG38EMigration20Counts(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for table, count := range left {
		if right[table] != count {
			return false
		}
	}
	return true
}

func TestG38ERestoreSchemaRejectsPolicyIndexAndGuardDrift(t *testing.T) {
	for name, mutate := range map[string]string{
		"policy index":         `DROP INDEX paper_performance_policy_events_account_selection_idx`,
		"policy guard":         `DROP TRIGGER paper_performance_policy_events_state_guard`,
		"automatic halt guard": `DROP TRIGGER execution_authority_events_state_guard`,
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			if err := requireOrderRestoreSchema(svc.db); err != nil {
				t.Fatalf("baseline schema rejected: %v", err)
			}
			if _, err := svc.db.Exec(mutate); err != nil {
				t.Fatal(err)
			}
			if err := requireOrderRestoreSchema(svc.db); err == nil {
				t.Fatal("restore schema accepted G3.8E policy drift")
			}
		})
	}
}

func TestG38EBackupV14CarriesAndVerifiesPolicyRecoveryProof(t *testing.T) {
	ctx := context.Background()
	svc, performance := g38EPerformanceWindow(t, []string{"100", "10"})
	if _, err := svc.applyPaperPerformancePolicy(ctx, k2aAccountRef,
		performance.StrategySelectionEventID, performance.StrategyPerformanceID); err != nil {
		t.Fatal(err)
	}
	proof, err := provePaperPerformancePolicyRecovery(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "g38e-policy-v14.db")
	golden := writeCurrentSnapshot(t, svc.db)
	manifest, err := createBackup(svc.db, backup, golden, backup+".manifest.json", svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v15" || manifest.SchemaVersion != "omni-folio.sqlite.v21" ||
		manifest.PaperPerformancePolicyStateSHA256 != proof.SHA256 || manifest.PaperPerformancePolicyEventCount != proof.Events ||
		manifest.PaperPerformancePolicyActionCount != proof.Actions || manifest.PaperPerformancePolicyAutomaticHaltCount != proof.AutomaticHalts ||
		manifest.VerificationReceipt.PaperPerformancePolicyCheck != "ok" ||
		manifest.VerificationReceipt.CandidatePaperPerformancePolicyStateSHA256 != proof.SHA256 {
		t.Fatalf("v14 policy proof omitted: %+v", manifest)
	}
	if err := verifyManifest(backup, golden, backup+".manifest.json"); err != nil {
		t.Fatal(err)
	}
}

func TestG38EBackupLegacyV13UsesOwnedV20MigrationWithoutChangingSource(t *testing.T) {
	ctx := context.Background()
	svc, _ := g38EPerformanceWindow(t, []string{"100", "100"})
	strategyBefore, err := proveStrategyRegistryRecovery(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	authorityBefore, authorityEvents, err := proveExecutionAuthorityRecovery(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	performanceBefore, err := provePaperStrategyPerformanceRecovery(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	currentBackup := filepath.Join(t.TempDir(), "g38e-v14-source.db")
	golden := writeCurrentSnapshot(t, svc.db)
	if _, err := createBackup(svc.db, currentBackup, golden, currentBackup+".manifest.json", svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	downgradePaperPerformancePolicyForTest(t, svc.db)
	legacyBackup := filepath.Join(t.TempDir(), "g38e-v13-source.db")
	if _, err := svc.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(legacyBackup, "'", "''") + `'`); err != nil {
		t.Fatal(err)
	}
	legacy := readJSONMap(t, currentBackup+".manifest.json")
	legacy["format_version"], legacy["schema_version"] = "omni-folio-backup.v13", "omni-folio.sqlite.v19"
	for _, field := range []string{
		"paper_performance_policy_state_sha256", "paper_performance_policy_event_count",
		"paper_performance_policy_action_count", "paper_performance_policy_automatic_halt_count",
	} {
		delete(legacy, field)
	}
	receipt := legacy["verification_receipt"].(map[string]any)
	delete(receipt, "paper_performance_policy_check")
	delete(receipt, "candidate_paper_performance_policy_state_sha256")
	sha, size, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	legacy["db_sha256"], legacy["size_bytes"], receipt["candidate_db_sha256"] = sha, size, sha
	legacyManifest := filepath.Join(t.TempDir(), "g38e-v13-source.manifest.json")
	writeJSONFile(t, legacyManifest, legacy)
	sourceSHA, sourceSize, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	manifestSHA, manifestSize, err := hashFile(legacyManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(legacyBackup, golden, legacyManifest); err != nil {
		t.Fatal(err)
	}
	afterSHA, afterSize, sourceErr := hashFile(legacyBackup)
	afterManifestSHA, afterManifestSize, manifestErr := hashFile(legacyManifest)
	if sourceErr != nil || manifestErr != nil || sourceSHA != afterSHA || sourceSize != afterSize ||
		manifestSHA != afterManifestSHA || manifestSize != afterManifestSize {
		t.Fatalf("v13 source changed db=(%s,%d)->(%s,%d) manifest=(%s,%d)->(%s,%d) errors=(%v,%v)",
			sourceSHA, sourceSize, afterSHA, afterSize, manifestSHA, manifestSize, afterManifestSHA, afterManifestSize, sourceErr, manifestErr)
	}
	owned := filepath.Join(t.TempDir(), "g38e-v13-owned.db")
	if err := copyFile(legacyBackup, owned); err != nil {
		t.Fatal(err)
	}
	db, err := openExistingDB(owned)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	strategyAfter, strategyErr := proveStrategyRegistryRecovery(ctx, db)
	authorityAfter, authorityCountAfter, authorityErr := proveExecutionAuthorityRecovery(ctx, db)
	performanceAfter, performanceErr := provePaperStrategyPerformanceRecovery(ctx, db)
	policyAfter, policyErr := provePaperPerformancePolicyRecovery(ctx, db)
	closeErr := db.Close()
	if strategyErr != nil || authorityErr != nil || performanceErr != nil || policyErr != nil || closeErr != nil ||
		!sameStrategyRegistryProof(strategyBefore, strategyAfter) || authorityBefore != authorityAfter || authorityEvents != authorityCountAfter ||
		performanceBefore != performanceAfter || policyAfter.SHA256 != emptySHA256 || policyAfter.Events != 0 || policyAfter.Actions != 0 || policyAfter.AutomaticHalts != 0 {
		t.Fatalf("owned v13 migration strategy=%+v/%+v authority=%s/%s counts=%d/%d performance=%+v/%+v policy=%+v errors=(%v,%v,%v,%v,%v)",
			strategyBefore, strategyAfter, authorityBefore, authorityAfter, authorityEvents, authorityCountAfter, performanceBefore, performanceAfter, policyAfter,
			strategyErr, authorityErr, performanceErr, policyErr, closeErr)
	}
	failedSource, err := openExistingDB(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failedSource.Exec(`DROP INDEX execution_authority_latest_idx`); err != nil {
		_ = failedSource.Close()
		t.Fatal(err)
	}
	if err := failedSource.Close(); err != nil {
		t.Fatal(err)
	}
	failureSHA, failureSize, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	legacy["db_sha256"], legacy["size_bytes"], receipt["candidate_db_sha256"] = failureSHA, failureSize, failureSHA
	writeJSONFile(t, legacyManifest, legacy)
	failureManifestSHA, failureManifestSize, err := hashFile(legacyManifest)
	if err != nil {
		t.Fatal(err)
	}
	beforeCandidates, err := filepath.Glob(filepath.Join(os.TempDir(), "omni-folio-restore-legacy.*"))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(legacyBackup, golden, legacyManifest); err == nil {
		t.Fatal("failed v13 candidate migration was accepted")
	}
	afterCandidates, err := filepath.Glob(filepath.Join(os.TempDir(), "omni-folio-restore-legacy.*"))
	if err != nil {
		t.Fatal(err)
	}
	if !sameStringSet(beforeCandidates, afterCandidates) {
		t.Fatalf("failed v13 candidate directory leaked before=%v after=%v", beforeCandidates, afterCandidates)
	}
	failureAfterSHA, failureAfterSize, sourceFailureErr := hashFile(legacyBackup)
	failureAfterManifestSHA, failureAfterManifestSize, manifestFailureErr := hashFile(legacyManifest)
	if sourceFailureErr != nil || manifestFailureErr != nil || failureSHA != failureAfterSHA || failureSize != failureAfterSize ||
		failureManifestSHA != failureAfterManifestSHA || failureManifestSize != failureAfterManifestSize {
		t.Fatalf("failed v13 candidate changed source db=(%s,%d)->(%s,%d) manifest=(%s,%d)->(%s,%d) errors=(%v,%v)",
			failureSHA, failureSize, failureAfterSHA, failureAfterSize, failureManifestSHA, failureManifestSize,
			failureAfterManifestSHA, failureAfterManifestSize, sourceFailureErr, manifestFailureErr)
	}
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]bool, len(left))
	for _, value := range left {
		seen[value] = true
	}
	for _, value := range right {
		if !seen[value] {
			return false
		}
	}
	return true
}

func downgradePaperPerformancePolicyForTest(t testing.TB, db *sql.DB) {
	t.Helper()
	var currentVersion int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&currentVersion); err != nil {
		t.Fatal(err)
	}
	if currentVersion >= 21 {
		if _, err := db.Exec(`DROP TABLE paper_runner_leases;
			DROP INDEX strategy_selection_events_runner_binding_idx;
			DELETE FROM schema_migrations WHERE version=21`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(`
		DROP TRIGGER paper_performance_policy_events_no_update;
		DROP TRIGGER paper_performance_policy_events_no_delete;
		DROP TRIGGER paper_performance_policy_events_state_guard;
		DROP TRIGGER execution_authority_events_state_guard;
		DROP TRIGGER strategy_selection_events_state_guard;
		DROP TRIGGER execution_authority_events_no_update;
		DROP TRIGGER execution_authority_events_no_delete;
		DROP TRIGGER strategy_selection_events_no_update;
		DROP TRIGGER strategy_selection_events_no_delete;
		DROP INDEX paper_performance_policy_events_account_selection_idx;
		DROP INDEX execution_authority_latest_idx;
		CREATE TABLE execution_authority_events_v19 (
			sequence INTEGER PRIMARY KEY,event_id TEXT NOT NULL UNIQUE,account_ref TEXT NOT NULL,armed INTEGER NOT NULL CHECK (armed IN (0,1)),
			lease_owner TEXT,fencing_token INTEGER NOT NULL CHECK (fencing_token>0),lease_expires_at TEXT,
			reason_code TEXT NOT NULL CHECK (reason_code IN ('manual_arm','manual_halt','lease_acquired')),
			record_sha256 TEXT NOT NULL,record_json TEXT NOT NULL,recorded_at TEXT NOT NULL,
			UNIQUE(account_ref,fencing_token),CHECK((lease_owner IS NULL AND lease_expires_at IS NULL) OR (armed=1 AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)),CHECK(armed=1 OR lease_owner IS NULL)
		) STRICT;
		INSERT INTO execution_authority_events_v19(sequence,event_id,account_ref,armed,lease_owner,fencing_token,lease_expires_at,reason_code,record_sha256,record_json,recorded_at)
		SELECT sequence,event_id,account_ref,armed,lease_owner,fencing_token,lease_expires_at,reason_code,record_sha256,record_json,recorded_at FROM execution_authority_events;
		CREATE TABLE strategy_selection_events_v19 (
			sequence INTEGER PRIMARY KEY,event_id TEXT NOT NULL UNIQUE,event_type TEXT NOT NULL CHECK(event_type IN ('SELECT','ROLLBACK')),
			candidate_result_sha256 TEXT REFERENCES strategy_research_evidence(result_sha256),expected_current_event_id TEXT NOT NULL,
			source_event_id TEXT REFERENCES strategy_selection_events(event_id),previous_selected_result_sha256 TEXT NOT NULL,selected_result_sha256 TEXT NOT NULL,
			reason_code TEXT NOT NULL CHECK(reason_code IN ('manual_selection','manual_rollback')),
			paper_evaluation_sequence INTEGER NOT NULL DEFAULT 0 CHECK(paper_evaluation_sequence>=0),record_sha256 TEXT NOT NULL CHECK(length(record_sha256)=64 AND record_sha256 NOT GLOB '*[^0-9a-f]*'),record_json TEXT NOT NULL,recorded_at TEXT NOT NULL,
			CHECK((event_type='SELECT' AND candidate_result_sha256 IS NOT NULL AND source_event_id IS NULL AND reason_code='manual_selection') OR (event_type='ROLLBACK' AND candidate_result_sha256 IS NULL AND source_event_id IS NOT NULL AND reason_code='manual_rollback'))
		) STRICT;
		INSERT INTO strategy_selection_events_v19(sequence,event_id,event_type,candidate_result_sha256,expected_current_event_id,source_event_id,previous_selected_result_sha256,selected_result_sha256,reason_code,paper_evaluation_sequence,record_sha256,record_json,recorded_at)
		SELECT sequence,event_id,event_type,candidate_result_sha256,expected_current_event_id,source_event_id,previous_selected_result_sha256,selected_result_sha256,reason_code,paper_evaluation_sequence,record_sha256,record_json,recorded_at FROM strategy_selection_events;
		DROP TABLE paper_performance_policy_events;
		PRAGMA legacy_alter_table=ON;
		ALTER TABLE execution_authority_events RENAME TO execution_authority_events_v20_old;
		ALTER TABLE strategy_selection_events RENAME TO strategy_selection_events_v20_old;
		ALTER TABLE execution_authority_events_v19 RENAME TO execution_authority_events;
		ALTER TABLE strategy_selection_events_v19 RENAME TO strategy_selection_events;
		DROP TABLE execution_authority_events_v20_old;
		DROP TABLE strategy_selection_events_v20_old;
		PRAGMA legacy_alter_table=OFF;
		CREATE INDEX execution_authority_latest_idx ON execution_authority_events(account_ref,sequence DESC);
		CREATE TRIGGER execution_authority_events_no_update BEFORE UPDATE ON execution_authority_events BEGIN SELECT RAISE(ABORT,'execution_authority_events is insert-only'); END;
		CREATE TRIGGER execution_authority_events_no_delete BEFORE DELETE ON execution_authority_events BEGIN SELECT RAISE(ABORT,'execution_authority_events is insert-only'); END;
		CREATE TRIGGER strategy_selection_events_no_update BEFORE UPDATE ON strategy_selection_events BEGIN SELECT RAISE(ABORT,'strategy_selection_events is insert-only'); END;
		CREATE TRIGGER strategy_selection_events_no_delete BEFORE DELETE ON strategy_selection_events BEGIN SELECT RAISE(ABORT,'strategy_selection_events is insert-only'); END;
		CREATE TRIGGER strategy_selection_events_state_guard BEFORE INSERT ON strategy_selection_events BEGIN
			SELECT CASE WHEN NEW.expected_current_event_id != COALESCE((SELECT event_id FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1),'no_event') THEN RAISE(ABORT,'strategy selection expected current event is stale') END;
			SELECT CASE WHEN NEW.previous_selected_result_sha256 != COALESCE((SELECT selected_result_sha256 FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1),'no_strategy') THEN RAISE(ABORT,'strategy selection previous result is stale') END;
			SELECT CASE WHEN NEW.event_type='SELECT' AND (NEW.selected_result_sha256 != NEW.candidate_result_sha256 OR NOT EXISTS(SELECT 1 FROM strategy_research_evidence WHERE result_sha256=NEW.candidate_result_sha256 AND target='paper_candidate')) THEN RAISE(ABORT,'strategy selection requires paper_candidate evidence') END;
			SELECT CASE WHEN NEW.event_type='ROLLBACK' AND NEW.source_event_id != COALESCE((SELECT event_id FROM strategy_selection_events ORDER BY sequence DESC LIMIT 1),'no_event') THEN RAISE(ABORT,'strategy rollback source is stale') END;
			SELECT CASE WHEN NEW.paper_evaluation_sequence != COALESCE((SELECT MAX(sequence) FROM paper_evaluation_events),0) THEN RAISE(ABORT,'strategy selection paper evaluation sequence is stale') END;
		END;
		DELETE FROM schema_migrations WHERE version=20;`)
	if restoreErr := restoreForeignKeysForTest(db); err != nil || restoreErr != nil {
		t.Fatal(errors.Join(err, restoreErr))
	}
}

func restoreForeignKeysForTest(db *sql.DB) error {
	_, err := db.Exec(`PRAGMA foreign_keys=ON`)
	return err
}

type g38ECounts struct{ Policy, Authority, Selection int }

func g38EJournalCounts(t testing.TB, svc *Service) g38ECounts {
	t.Helper()
	var result g38ECounts
	for query, destination := range map[string]*int{
		`SELECT COUNT(*) FROM paper_performance_policy_events`: &result.Policy,
		`SELECT COUNT(*) FROM execution_authority_events`:      &result.Authority,
		`SELECT COUNT(*) FROM strategy_selection_events`:       &result.Selection,
	} {
		if err := svc.db.QueryRow(query).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func g38EPerformanceWindow(t *testing.T, closes []string) (*Service, *PaperStrategyPerformanceEvent) {
	t.Helper()
	svc, _ := testService(t, nil, nil)
	ctx := context.Background()
	svc.now = func() time.Time { return mustTime("2026-01-10T07:00:00Z") }
	artifact := rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(result map[string]any) {
		result["experiment_id"] = "g38e-policy-window"
		result["execution"].(map[string]any)["starting_cash"] = "1000"
	})
	evidence, err := svc.registerStrategyEvidence(ctx, artifact)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := svc.selectPaperCandidate(ctx, evidence.ResultSHA256, noStrategySelectionEvent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID); err != nil {
		t.Fatal(err)
	}
	signalBar, err := svc.recordPaperMarketBar(ctx, g38c2PaperMarketBar("g38e-policy-signal", "2026-01-09T00:00:00Z", "2026-01-09T06:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	signal := PaperSignal{
		SchemaVersion: capitalizedPaperSignalSchema, SignalID: "g38e-policy-signal", SignalBarObservationID: signalBar.ObservationID,
		StrategyResultSHA256: evidence.ResultSHA256, StrategySelectionEventID: selected.CurrentEventID,
		DataSHA256: signalBar.InputDataSHA256, Symbol: signalBar.Symbol, TargetQuantity: "2", DataAsOf: signalBar.CloseAt,
		GeneratedAt: signalBar.SourceAvailableAt, ExpiresAt: "2026-01-20T00:00:00.000000000Z",
	}
	lease := mustK2CLease(t, svc, k2aAccountRef)
	_, order, err := svc.admitPaperSignal(ctx, k2aAccountRef, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	recordG38C2FillBar(t, svc, "g38e-policy-fill", "100", "5", "2026-01-10")
	if state, err := svc.runPaperOrder(ctx, order.OrderID, lease.FencingToken); err != nil || state.Status != "FILLED" {
		t.Fatalf("filled state=%+v err=%v", state, err)
	}
	svc.now = func() time.Time { return mustTime("2026-01-15T07:00:00Z") }
	var latest *PaperStrategyPerformanceEvent
	for index, close := range closes {
		asOf := time.Date(2026, time.January, 11+index, 6, 30, 0, 0, time.UTC).Format(canonicalPaperTimeLayout)
		recordG38C3MarkBar(t, svc, "005930", "g38e-policy-mark-"+close+time.Date(2026, time.January, 11+index, 0, 0, 0, 0, time.UTC).Format("20060102"), asOf, close)
		point, err := svc.evaluatePaperPerformance(ctx, k2aAccountRef, asOf)
		if err != nil {
			t.Fatal(err)
		}
		latest, err = svc.evaluatePaperStrategyPerformance(ctx, k2aAccountRef, point.StrategySelectionEventID, point.PerformanceID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if latest == nil {
		t.Fatal("G3.8E policy fixture needs at least one strategy performance point")
	}
	return svc, latest
}

func g38ELaterOtherAccountStrategyPerformance(t *testing.T, svc *Service, primary *PaperStrategyPerformanceEvent) *PaperStrategyPerformanceEvent {
	return g38EOtherAccountStrategyPerformanceAt(t, svc, primary, mustTime("2026-01-13T00:00:00Z"))
}

func g38EOtherAccountStrategyPerformanceAt(t *testing.T, svc *Service, primary *PaperStrategyPerformanceEvent, start time.Time) *PaperStrategyPerformanceEvent {
	t.Helper()
	ctx := context.Background()
	const account = "kiwoom_account_BBBBBBBBBBBBBBBBBBBBBBBB"
	signalDay := start.UTC()
	fillDay := signalDay.AddDate(0, 0, 1)
	markDay := signalDay.AddDate(0, 0, 2)
	svc.now = func() time.Time { return signalDay.Add(7 * time.Hour) }
	if _, err := svc.openPaperAccountingSession(ctx, account, primary.SelectedStrategyResultRef, primary.StrategySelectionEventID); err != nil {
		t.Fatal(err)
	}
	signalInput := g38c2PaperMarketBar("g38e-global-cutoff-signal", signalDay.Format("2006-01-02T15:04:05Z"), signalDay.Add(6*time.Hour+30*time.Minute).Format("2006-01-02T15:04:05Z"))
	signalInput.SourceAvailableAt, signalInput.FetchedAt = signalDay.Add(6*time.Hour+31*time.Minute).Format("2006-01-02T15:04:05Z"), signalDay.Add(6*time.Hour+32*time.Minute).Format("2006-01-02T15:04:05Z")
	signalBar, err := svc.recordPaperMarketBar(ctx, signalInput)
	if err != nil {
		t.Fatal(err)
	}
	signal := PaperSignal{
		SchemaVersion: capitalizedPaperSignalSchema, SignalID: "g38e-global-cutoff-signal", SignalBarObservationID: signalBar.ObservationID,
		StrategyResultSHA256: primary.SelectedStrategyResultRef, StrategySelectionEventID: primary.StrategySelectionEventID,
		DataSHA256: signalBar.InputDataSHA256, Symbol: signalBar.Symbol, TargetQuantity: "2", DataAsOf: signalBar.CloseAt,
		GeneratedAt: signalBar.SourceAvailableAt, ExpiresAt: "2026-01-20T00:00:00.000000000Z",
	}
	lease := mustK2CLease(t, svc, account)
	_, order, err := svc.admitPaperSignal(ctx, account, signal, lease.FencingToken)
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return fillDay.Add(7 * time.Hour) }
	fillInput := g38c2PaperMarketBar("g38e-global-cutoff-fill", fillDay.Format("2006-01-02T15:04:05Z"), fillDay.Add(6*time.Hour+30*time.Minute).Format("2006-01-02T15:04:05Z"))
	fillInput.Open, fillInput.High, fillInput.Low, fillInput.Close, fillInput.Volume = "100", "100", "100", "100", "5"
	fillInput.SourceAvailableAt, fillInput.FetchedAt = fillDay.Add(6*time.Hour+31*time.Minute).Format("2006-01-02T15:04:05Z"), fillDay.Add(6*time.Hour+32*time.Minute).Format("2006-01-02T15:04:05Z")
	if _, err := svc.recordPaperMarketBar(ctx, fillInput); err != nil {
		t.Fatal(err)
	}
	lease = mustK2CLease(t, svc, account)
	if state, err := svc.runPaperOrder(ctx, order.OrderID, lease.FencingToken); err != nil || state.Status != "FILLED" {
		t.Fatalf("cross-account filled state=%+v err=%v", state, err)
	}
	svc.now = func() time.Time { return markDay.Add(7 * time.Hour) }
	asOf := markDay.Add(6*time.Hour + 30*time.Minute).Format(canonicalPaperTimeLayout)
	recordG38C3MarkBar(t, svc, "005930", "g38e-global-cutoff-mark", asOf, "100")
	point, err := svc.evaluatePaperPerformance(ctx, account, asOf)
	if err != nil {
		t.Fatal(err)
	}
	event, err := svc.evaluatePaperStrategyPerformance(ctx, account, point.StrategySelectionEventID, point.PerformanceID)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
