package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
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
	_, saved, found, err := loadPaperPromotion(context.Background(), f.svc.db, receipt.AttemptID)
	if err != nil || !found || saved == nil {
		t.Fatalf("saved promotion: %+v, %v", saved, err)
	}
	decidedAt, ok := parsePaperTime(saved.RecordedAt)
	if !ok {
		t.Fatal("invalid saved decision time")
	}
	f.svc.now = func() time.Time { return decidedAt.Add(time.Nanosecond) }
	rollback, err := f.svc.rollbackPaperCandidate(context.Background(), receipt.SelectionEventID, receipt.SelectionEventID)
	if err != nil || rollback == nil || rollback.SelectedResultSHA256 != f.incumbent.ResultSHA256 {
		t.Fatalf("existing rollback after handoff: %+v, %v", rollback, err)
	}
	before := paperImprovementStateForTest(t, f.svc)
	var out bytes.Buffer
	if err := runPaperImprovementCommand(context.Background(), []string{"-db", f.dbPath, "-account", k2aAccountRef,
		"-artifact", path, "-bars", f.barsPath, "-attempt-id", receipt.AttemptID}, &out); err != nil {
		t.Fatal(err)
	}
	result := decodePaperImprovementReceiptForTest(t, out.Bytes())
	if result.ResultSHA256 != evidence.ResultSHA256 || !result.Registered || !result.AttemptRecorded || result.Disposition != "comparison" || !reflect.DeepEqual(result.Comparison, receipt) {
		t.Fatalf("retry after rollback replaced the saved comparison: %s", out.String())
	}
	if after := paperImprovementStateForTest(t, f.svc); after != before {
		t.Fatal("exact CLI retry after rollback changed a journal or selection")
	}
}

func TestPaperPromotionImprovementNoOpsLeaveAttemptUnreservedAndAuthorityUnchanged(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	if _, err := f.svc.setSyntheticExecutionArmed(ctx, k2aAccountRef, true); err != nil {
		t.Fatal(err)
	}
	const attemptID = "improvement-unreserved"
	before := paperImprovementStateForTest(t, f.svc)
	receipt, err := f.svc.improvePaperCandidate(ctx, k2aAccountRef, attemptID, []byte(f.incumbent.artifactJSON), f.bars)
	if err != nil || receipt == nil || receipt.SchemaVersion != "paper-improvement.v1" || receipt.ResultSHA256 != f.incumbent.ResultSHA256 || !receipt.Registered || receipt.AttemptRecorded || receipt.Disposition != "already_selected" || receipt.Comparison != nil {
		t.Fatalf("already-selected observation: %+v, %v", receipt, err)
	}
	if after := paperImprovementStateForTest(t, f.svc); after != before {
		t.Fatal("already-selected observation changed trading or research state")
	}
	artifact := paperImprovementResearchOnlyArtifactForTest(t)
	evidence, err := decodeStrategyArtifact(artifact)
	if err != nil || evidence.Target != "no_promotion" {
		t.Fatalf("research-only fixture: %+v, %v", evidence, err)
	}
	receipt, err = f.svc.improvePaperCandidate(ctx, k2aAccountRef, attemptID, artifact, f.bars)
	if err != nil || receipt == nil || receipt.ResultSHA256 != evidence.ResultSHA256 || !receipt.Registered || receipt.AttemptRecorded || receipt.Disposition != "research_not_candidate" || receipt.Comparison != nil {
		t.Fatalf("research-only fractional quantity must retain v1 registration semantics: %+v, %v", receipt, err)
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	decodePaperImprovementReceiptForTest(t, encoded)
	after := paperImprovementStateForTest(t, f.svc)
	assertPaperImprovementNoOperationalChange(t, before, after)
	if paperImprovementEvidenceCountForTest(t, f.svc, evidence.ResultSHA256) != 1 || after.Research == before.Research {
		t.Fatal("research-only result was not durably registered")
	}
	if _, err := f.svc.setSyntheticExecutionArmed(ctx, k2aAccountRef, false); err != nil {
		t.Fatal(err)
	}
	receipt, err = f.svc.improvePaperCandidate(ctx, k2aAccountRef, attemptID, []byte(f.challenger.artifactJSON), f.bars)
	if err != nil || receipt == nil || !receipt.AttemptRecorded || receipt.Disposition != "comparison" || receipt.Comparison == nil || receipt.Comparison.Decision != "PROMOTE" {
		t.Fatalf("no-op reserved an attempt ID: %+v, %v", receipt, err)
	}
	// An already-selected result still requires recovery of older registered evidence.
	var trigger string
	if err := f.svc.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='strategy_research_evidence_no_update' AND type='trigger'`).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.db.Exec(`DROP TRIGGER strategy_research_evidence_no_update`); err != nil {
		t.Fatal(err)
	}
	_, updateErr := f.svc.db.Exec(`UPDATE strategy_research_evidence SET artifact_json='PRIVATE_CORRUPT_RESEARCH' WHERE result_sha256=?`, f.incumbent.ResultSHA256)
	_, triggerErr := f.svc.db.Exec(trigger)
	if updateErr != nil || triggerErr != nil {
		t.Fatalf("corrupt research or restore trigger: %v / %v", updateErr, triggerErr)
	}
	before = paperImprovementStateForTest(t, f.svc)
	if _, err := f.svc.improvePaperCandidate(ctx, k2aAccountRef, "unreserved-corrupt-noop", []byte(f.challenger.artifactJSON), f.bars); err == nil {
		t.Fatal("already-selected shortcut bypassed registry recovery")
	}
	if after := paperImprovementStateForTest(t, f.svc); after != before {
		t.Fatal("rejected recovery repaired or changed stored research")
	}
}

func TestPaperPromotionImprovementClaimRetryRejectsConflictsBeforeRegistration(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	const attemptID = "improvement-bound-request"
	artifact := []byte(f.challenger.artifactJSON)
	first, err := f.svc.improvePaperCandidate(ctx, k2aAccountRef, attemptID, artifact, f.bars)
	if err != nil || first == nil || first.Comparison == nil || first.Comparison.Decision != "PROMOTE" {
		t.Fatalf("initial comparison: %+v, %v", first, err)
	}
	before := paperImprovementStateForTest(t, f.svc)
	retry, err := f.svc.improvePaperCandidate(ctx, k2aAccountRef, attemptID, artifact, f.bars)
	if err != nil || !reflect.DeepEqual(retry, first) {
		t.Fatalf("exact retry became an already-selected no-op: %+v, %v", retry, err)
	}
	if after := paperImprovementStateForTest(t, f.svc); after != before {
		t.Fatal("exact completed retry added records")
	}
	later := f.svc.now().Add(time.Nanosecond)
	f.svc.now = func() time.Time { return later }
	otherArtifact := paperImprovementArtifactForTest(t, f, "conflicting-candidate")
	researchOnly := paperImprovementResearchOnlyArtifactForTest(t)
	changedBars := []byte(strings.ReplaceAll(string(f.bars), ",100\n", ",101\n"))
	if bytes.Equal(changedBars, f.bars) {
		t.Fatal("CSV conflict fixture did not change")
	}
	for _, tc := range []struct {
		name, account  string
		artifact, bars []byte
	}{
		{"different candidate", k2aAccountRef, otherArtifact, f.bars},
		{"research-only shortcut", k2aAccountRef, researchOnly, f.bars},
		{"exact CSV binding", k2aAccountRef, artifact, changedBars},
		{"account binding", "kiwoom_account_BBBBBBBBBBBBBBBBBBBBBBBB", artifact, f.bars},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.svc.improvePaperCandidate(ctx, tc.account, attemptID, tc.artifact, tc.bars); err == nil {
				t.Fatal("bound attempt accepted a different request")
			}
			if after := paperImprovementStateForTest(t, f.svc); after != before {
				t.Fatal("conflicting attempt registered research or changed existing records")
			}
		})
	}
}

func TestPaperPromotionImprovementPreflightRejectsUnsupportedInputsBeforeRegistration(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	artifact := strategyArtifact(t, nil)
	bars := paperPromotionForwardBars(t, mustTime("2026-05-01T00:00:00Z"), 24*time.Hour)
	v2 := strategyArtifact(t, func(config map[string]any) { config["schema_version"] = "strategy-improvement.v2" })
	fractional := strategyArtifact(t, func(config map[string]any) {
		config["strategy"].(map[string]any)["quantity"] = "0.5"
		execution := config["execution"].(map[string]any)
		execution["fee"], execution["tax"], execution["slippage_bps"] = "0", "0", "0"
	})
	unsupported := rehashedStrategyArtifact(t, artifact, func(result map[string]any) {
		result["manifest"].(map[string]any)["strategy"].(map[string]any)["version"] = "unsupported-private-version"
	})
	for name, raw := range map[string][]byte{"fractional candidate": fractional, "unsupported candidate": unsupported} {
		if evidence, err := decodeStrategyArtifact(raw); err != nil || evidence.Target != "paper_candidate" {
			t.Fatalf("%s must pass existing v1 research admission: %+v, %v", name, evidence, err)
		}
	}
	before := paperImprovementStateForTest(t, svc)
	for _, tc := range []struct {
		name, account, attemptID string
		artifact, bars           []byte
	}{
		{"invalid account", "PRIVATE_INVALID_ACCOUNT", "preflight", artifact, bars},
		{"invalid attempt", k2aAccountRef, "private invalid attempt", artifact, bars},
		{"malformed artifact", k2aAccountRef, "preflight", []byte("PRIVATE_INVALID_JSON"), bars},
		{"trailing artifact", k2aAccountRef, "preflight", append(append([]byte(nil), artifact...), []byte(" {}")...), bars},
		{"oversized artifact", k2aAccountRef, "preflight", bytes.Repeat([]byte("x"), maxBodyBytes+1), bars},
		{"actual Python v2", k2aAccountRef, "preflight", v2, bars},
		{"fractional paper candidate", k2aAccountRef, "preflight", fractional, bars},
		{"unsupported paper candidate", k2aAccountRef, "preflight", unsupported, bars},
		{"malformed CSV", k2aAccountRef, "preflight", artifact, []byte("PRIVATE_INVALID_CSV")},
		{"oversized CSV", k2aAccountRef, "preflight", artifact, bytes.Repeat([]byte("x"), maxMarketDataBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.improvePaperCandidate(context.Background(), tc.account, tc.attemptID, tc.artifact, tc.bars); err == nil {
				t.Fatal("unsupported input reached registration")
			}
			if after := paperImprovementStateForTest(t, svc); after != before {
				t.Fatal("preflight rejection changed database state")
			}
		})
	}
}

func TestPaperPromotionImprovementRegistrationRemainsAfterClaimFailureWithoutAbort(t *testing.T) {
	f := newPaperPromotionFixture(t)
	ctx := context.Background()
	if _, err := f.svc.setSyntheticExecutionArmed(ctx, k2aAccountRef, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"armed", "pre-research"} {
		artifact := paperImprovementArtifactForTest(t, f, "registered-before-"+name)
		evidence, err := decodeStrategyArtifact(artifact)
		if err != nil {
			t.Fatal(err)
		}
		bars := f.bars
		if name == "pre-research" {
			if _, err := f.svc.setSyntheticExecutionArmed(ctx, k2aAccountRef, false); err != nil {
				t.Fatal(err)
			}
			bars = paperPromotionForwardBars(t, mustTime("2025-01-01T00:00:00Z"), 24*time.Hour)
		}
		before := paperImprovementStateForTest(t, f.svc)
		if _, err := f.svc.improvePaperCandidate(ctx, k2aAccountRef, "rejected-"+name, artifact, bars); err == nil {
			t.Fatalf("claim bypassed %s admission", name)
		}
		after := paperImprovementStateForTest(t, f.svc)
		assertPaperImprovementNoOperationalChange(t, before, after)
		if paperImprovementEvidenceCountForTest(t, f.svc, evidence.ResultSHA256) != 1 || before.Research == after.Research {
			t.Fatalf("%s failure lost independently committed registration", name)
		}
	}
	claim := paperPromotionReportClaimForTest(t, f, "existing-improvement-pending")
	claimedAt, ok := parsePaperTime(claim.RecordedAt)
	if !ok {
		t.Fatal("invalid pending claim time")
	}
	f.svc.now = func() time.Time { return claimedAt.Add(time.Nanosecond) }
	artifact := paperImprovementArtifactForTest(t, f, "registered-before-pending-rejection")
	evidence, err := decodeStrategyArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	before := paperImprovementStateForTest(t, f.svc)
	if _, err := f.svc.improvePaperCandidate(ctx, k2aAccountRef, "new-improvement-pending", artifact, f.bars); err == nil {
		t.Fatal("a second attempt bypassed the pending comparison")
	}
	assertPaperImprovementNoOperationalChange(t, before, paperImprovementStateForTest(t, f.svc))
	if paperImprovementEvidenceCountForTest(t, f.svc, evidence.ResultSHA256) != 1 {
		t.Fatal("pending comparison rejection lost the prior registration")
	}
	status, err := f.svc.paperPromotionStatus(ctx, claim.AttemptID)
	if err != nil || status == nil || status.Decision != "PENDING" || status.Reason != "comparison_claimed" {
		t.Fatalf("existing pending attempt was automatically finished or aborted: %+v, %v", status, err)
	}
}

func TestPaperPromotionImprovementConcurrentCallsConvergeOnOneClaim(t *testing.T) {
	for _, sameAttempt := range []bool{true, false} {
		name := "distinct attempts"
		if sameAttempt {
			name = "same attempt"
		}
		t.Run(name, func(t *testing.T) {
			f := newPaperPromotionFixture(t)
			secondDB, err := openExistingDB(f.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = secondDB.Close() })
			second := newService(secondDB, f.svc.now, func(prefix string) string { return prefix + "_improvement_secondary" })
			ids := []string{"improvement-race-first", "improvement-race-second"}
			if sameAttempt {
				ids[1] = ids[0]
			}
			before := paperImprovementStateForTest(t, f.svc)
			start := make(chan struct{})
			results := make([]*PaperImprovementReceipt, 2)
			errs := make([]error, 2)
			var wg sync.WaitGroup
			for index, svc := range []*Service{f.svc, second} {
				wg.Add(1)
				go func(index int, svc *Service) {
					defer wg.Done()
					<-start
					results[index], errs[index] = svc.improvePaperCandidate(context.Background(), k2aAccountRef, ids[index], []byte(f.challenger.artifactJSON), f.bars)
				}(index, svc)
			}
			close(start)
			wg.Wait()
			completed := false
			for index, result := range results {
				if errs[index] != nil {
					if sameAttempt {
						t.Fatalf("identical concurrent retry failed: %v", errs[index])
					}
					continue
				}
				if result == nil || !result.Registered || result.ResultSHA256 != f.challenger.ResultSHA256 {
					t.Fatalf("concurrent receipt: %+v", result)
				}
				if result.Disposition == "comparison" {
					if !result.AttemptRecorded || result.Comparison == nil || result.Comparison.AttemptID != ids[index] || (result.Comparison.Decision != "PROMOTE" && result.Comparison.Decision != "PENDING") {
						t.Fatalf("concurrent comparison: %+v", result)
					}
					completed = completed || result.Comparison.Decision == "PROMOTE"
				} else if result.Disposition != "already_selected" || result.AttemptRecorded || result.Comparison != nil || sameAttempt {
					t.Fatalf("concurrent no-op hid a bound claim: %+v", result)
				}
			}
			if !completed {
				t.Fatalf("no concurrent call returned the completed comparison: %+v, %v", results, errs)
			}
			proof, err := provePaperPromotionRecovery(context.Background(), f.svc.db)
			registry, registryErr := replayStrategyRegistry(context.Background(), f.svc.db)
			if err != nil || registryErr != nil || proof.Claims != 1 || proof.Decisions != 1 || registry.Evidence != 2 || registry.Events != 2 || registry.SelectedResultSHA256 != f.challenger.ResultSHA256 {
				t.Fatalf("race duplicated a claim or selection: %+v, %+v, %v / %v", proof, registry, err, registryErr)
			}
			after := paperImprovementStateForTest(t, f.svc)
			if after.Journal.Authority != before.Journal.Authority || after.Journal.Schema != before.Journal.Schema || after.Admission != before.Admission || after.Research != before.Research {
				t.Fatal("comparison race changed execution authority or registered duplicate research")
			}
		})
	}
}

func TestPaperPromotionImprovementPreservesContextIdentityWithoutWrites(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	before := paperImprovementStateForTest(t, svc)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, expire := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer expire()
	for _, ctx := range []context.Context{cancelled, expired} {
		if _, err := svc.improvePaperCandidate(ctx, k2aAccountRef, "cancelled-improvement", nil, nil); !errors.Is(err, ctx.Err()) {
			t.Fatalf("service context identity lost: %v", err)
		}
		if err := runPaperImprovementCommand(ctx, nil, &bytes.Buffer{}); !errors.Is(err, ctx.Err()) {
			t.Fatalf("CLI context identity lost: %v", err)
		}
	}
	if _, err := svc.improvePaperCandidate(nil, k2aAccountRef, "nil-context", nil, nil); err == nil {
		t.Fatal("missing context accepted")
	}
	if err := runPaperImprovementCommand(nil, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("missing command context accepted")
	}
	if after := paperImprovementStateForTest(t, svc); after != before {
		t.Fatal("cancelled or invalid-context handoff changed database state")
	}
}

type paperImprovementTestState struct {
	Journal   paperPromotionReportStateForTestValue
	Admission paperAdmissionCounts
	Research  string
}

func paperImprovementStateForTest(t *testing.T, svc *Service) paperImprovementTestState {
	t.Helper()
	state := paperImprovementTestState{Journal: paperPromotionReportStateForTest(t, svc), Admission: paperAdmissionCountsForTest(t, svc)}
	if err := svc.db.QueryRow(`SELECT json_group_array(artifact_json) FROM (SELECT artifact_json FROM strategy_research_evidence ORDER BY sequence)`).Scan(&state.Research); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertPaperImprovementNoOperationalChange(t *testing.T, before, after paperImprovementTestState) {
	t.Helper()
	before.Journal.Changes, after.Journal.Changes = 0, 0
	if before.Journal != after.Journal || before.Admission != after.Admission {
		t.Fatal("registration/no-op changed promotion, selection, execution authority, or trading records")
	}
}

func paperImprovementEvidenceCountForTest(t *testing.T, svc *Service, sha string) int {
	t.Helper()
	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM strategy_research_evidence WHERE result_sha256=?`, sha).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func paperImprovementArtifactForTest(t *testing.T, f paperPromotionFixture, experimentID string) []byte {
	t.Helper()
	return rehashedStrategyArtifact(t, []byte(f.challenger.artifactJSON), func(result map[string]any) { result["experiment_id"] = experimentID })
}

func paperImprovementResearchOnlyArtifactForTest(t *testing.T) []byte {
	t.Helper()
	return strategyArtifact(t, func(config map[string]any) {
		config["strategy"].(map[string]any)["quantity"] = "0.5"
		config["promotion"].(map[string]any)["minimum_holdout_after_cost_return"] = "100"
	})
}

func decodePaperImprovementReceiptForTest(t *testing.T, raw []byte) *PaperImprovementReceipt {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || !exactKeys(body, "schema_version", "result_sha256", "registered", "attempt_recorded", "disposition", "comparison") {
		t.Fatalf("improvement receipt must have the closed public shape: %s, %v", raw, err)
	}
	var receipt PaperImprovementReceipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil || receipt.SchemaVersion != "paper-improvement.v1" {
		t.Fatalf("improvement receipt: %s, %v", raw, err)
	}
	return &receipt
}
