package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const g38F2OtherAccountRef = "kiwoom_account_BBBBBBBBBBBBBBBBBBBBBBBB"

func TestG38F2PaperRunnerLeaseAcquireHeartbeatReleaseRetainsFence(t *testing.T) {
	svc := g38F2LeaseService(t)
	ctx := context.Background()
	now := mustTime("2026-01-15T07:00:00Z")
	svc.now = func() time.Time { return now }

	first, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef)
	if err != nil || first.FencingToken <= 0 {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	replayed, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef)
	if err != nil || replayed.FencingToken != first.FencingToken {
		t.Fatalf("same-owner reacquire claim=%+v first=%+v err=%v", replayed, first, err)
	}
	now = now.Add(time.Second)
	heartbeat, err := svc.heartbeatPaperRunnerLease(ctx, first)
	if err != nil || heartbeat.FencingToken != first.FencingToken || heartbeat.LeaseExpiresAtNS <= first.LeaseExpiresAtNS {
		t.Fatalf("heartbeat=%+v first=%+v err=%v", heartbeat, first, err)
	}
	if err := svc.releasePaperRunnerLease(ctx, heartbeat); err != nil {
		t.Fatal(err)
	}
	second, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef)
	if err != nil || second.FencingToken <= first.FencingToken {
		t.Fatalf("released claim reset its fence: first=%+v second=%+v err=%v", first, second, err)
	}
}

func TestG38F2PaperRunnerLeaseGlobalForeignOwnerAndExactExpiryTakeover(t *testing.T) {
	primary := g38F2LeaseService(t)
	ctx := context.Background()
	now := mustTime("2026-01-15T07:00:00Z")
	primary.now = func() time.Time { return now }
	first, err := primary.acquirePaperRunnerLease(ctx, k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}

	foreign := secondG38C2Service(t, primary, now)
	if _, err := foreign.acquirePaperRunnerLease(ctx, g38F2OtherAccountRef); err == nil ||
		!strings.Contains(err.Error(), "another process") {
		t.Fatalf("foreign account acquired global live claim: err=%v", err)
	}

	foreign.now = func() time.Time { return now.Add(paperRunnerLeaseTTL) }
	taken, err := foreign.acquirePaperRunnerLease(ctx, g38F2OtherAccountRef)
	if err != nil || taken.FencingToken <= first.FencingToken {
		t.Fatalf("exact-expiry takeover=%+v first=%+v err=%v", taken, first, err)
	}
}

func TestG38F2PaperRunnerLeaseFailsClosedOnClockRegressionAndFenceOverflow(t *testing.T) {
	svc := g38F2LeaseService(t)
	ctx := context.Background()
	now := mustTime("2026-01-15T07:00:00Z")
	svc.now = func() time.Time { return now }
	claim, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(-time.Nanosecond)
	if _, err := svc.heartbeatPaperRunnerLease(ctx, claim); err == nil || !strings.Contains(err.Error(), "clock") {
		t.Fatalf("clock regression was accepted: err=%v", err)
	}

	if _, err := svc.db.Exec(`DROP TRIGGER paper_runner_leases_state_guard`); err != nil {
		t.Fatal(err)
	}
	record := paperRunnerLeaseRecord{
		Scope:                    paperRunnerLeaseScope,
		FencingToken:             int64(^uint64(0) >> 1),
		OwnerID:                  claim.OwnerID,
		AccountRef:               claim.AccountRef,
		HeartbeatAtNS:            claim.LeaseExpiresAtNS - int64(paperRunnerLeaseTTL),
		ExpiresAtNS:              claim.LeaseExpiresAtNS,
		StrategySelectionEventID: claim.StrategySelectionEventID,
		SelectedResultSHA256:     claim.SelectedResultSHA256,
	}
	recordJSON, recordSHA := paperRunnerLeaseRecordHash(t, record)
	if _, err := svc.db.Exec(`UPDATE paper_runner_leases SET fencing_token=?,record_json=?,record_sha256=?`, record.FencingToken, recordJSON, recordSHA); err != nil {
		t.Fatal(err)
	}
	migration, err := migrationFiles.ReadFile("migrations/021_paper_runner_leases.sql")
	if err != nil {
		t.Fatal(err)
	}
	triggerStart := strings.Index(string(migration), "CREATE TRIGGER paper_runner_leases_state_guard")
	if triggerStart < 0 {
		t.Fatal("migration 021 state guard is missing")
	}
	if _, err := svc.db.Exec(string(migration)[triggerStart:]); err != nil {
		t.Fatal(err)
	}
	now = mustTime("2026-01-16T07:00:00Z")
	if _, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef); err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("fence overflow was accepted: err=%v", err)
	}
}

func TestG38F2PaperRunnerLeaseRecoveryRejectsCanonicalCorruption(t *testing.T) {
	svc := g38F2LeaseService(t)
	ctx := context.Background()
	if _, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef); err != nil {
		t.Fatal(err)
	}
	if _, err := provePaperRunnerLeaseRecovery(ctx, svc.db); err != nil {
		t.Fatalf("healthy lease recovery: %v", err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER paper_runner_leases_state_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE paper_runner_leases SET record_sha256=?`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := provePaperRunnerLeaseRecovery(ctx, svc.db); err == nil {
		t.Fatal("canonical lease corruption passed recovery")
	}
}

func paperRunnerLeaseRecordHash(t testing.TB, record paperRunnerLeaseRecord) (string, string) {
	t.Helper()
	value, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(value)
	return string(value), hex.EncodeToString(sum[:])
}

func g38F2LeaseService(t *testing.T) *Service {
	t.Helper()
	svc, _ := testService(t, nil, nil)
	artifact := rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(result map[string]any) {
		result["experiment_id"] = "g38f2-lease-fixture"
	})
	evidence, err := svc.registerStrategyEvidence(context.Background(), artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, noStrategySelectionEvent); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestG38F2PaperRunnerLeaseIsDistinctFromSyntheticExecutionAuthority(t *testing.T) {
	svc := g38F2LeaseService(t)
	ctx := context.Background()
	now := mustTime("2026-01-15T07:00:00Z")
	svc.now = func() time.Time { return now }
	if _, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef); err != nil {
		t.Fatalf("scheduler claim required execution authority: %v", err)
	}
	if _, err := svc.setSyntheticExecutionArmed(ctx, k2aAccountRef, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.acquireSyntheticExecutionLease(ctx, k2aAccountRef); err != nil {
		t.Fatalf("scheduler claim altered execution authority: %v", err)
	}

	foreign := secondG38C2Service(t, svc, now)
	if foreign.executionOwner != svc.executionOwner {
		t.Fatal("test setup needs a shared execution owner")
	}
	if _, err := foreign.acquirePaperRunnerLease(ctx, k2aAccountRef); err == nil {
		t.Fatal("shared execution owner bypassed distinct scheduler ownership")
	}
}

func TestG38F2PaperRunnerLeaseBlocksManualSelectionAndRollbackWhileLive(t *testing.T) {
	svc, _ := g38EPerformanceWindow(t, []string{"100"})
	ctx := context.Background()
	if _, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef); err != nil {
		t.Fatal(err)
	}
	current, err := replayStrategyRegistry(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	artifact := rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(result map[string]any) {
		result["experiment_id"] = "g38f2-live-runner-selection-block"
	})
	evidence, err := svc.registerStrategyEvidence(ctx, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.selectPaperCandidate(ctx, evidence.ResultSHA256, current.CurrentEventID); err == nil ||
		!strings.Contains(err.Error(), "runner") {
		t.Fatalf("manual selection changed a live runner lease: err=%v", err)
	}
	if _, err := svc.rollbackPaperCandidate(ctx, current.CurrentEventID, current.CurrentEventID); err == nil ||
		!strings.Contains(err.Error(), "runner") {
		t.Fatalf("manual rollback changed a live runner lease: err=%v", err)
	}
}

func TestG38F2ManualSelectionMutationRejectsExpiredPartialRunnerPrefix(t *testing.T) {
	for _, stage := range []string{"C3", "C3+D"} {
		t.Run(stage, func(t *testing.T) {
			svc, claim, asOf := g38F2ClaimedDueWindow(t)
			point, err := svc.evaluatePaperPerformanceWithClaim(context.Background(), k2aAccountRef, asOf, claim)
			if err != nil {
				t.Fatal(err)
			}
			if stage == "C3+D" {
				if _, err := svc.evaluatePaperStrategyPerformanceWithClaim(context.Background(), k2aAccountRef, point.StrategySelectionEventID, point.PerformanceID, claim); err != nil {
					t.Fatal(err)
				}
			}
			svc.now = func() time.Time { return time.Unix(0, claim.LeaseExpiresAtNS).UTC() }
			if stage == "C3" {
				challenger := rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(result map[string]any) {
					result["experiment_id"] = "g38f2-partial-prefix-challenger"
				})
				evidence, err := svc.registerStrategyEvidence(context.Background(), challenger)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, claim.StrategySelectionEventID); err == nil || !strings.Contains(err.Error(), "incomplete paper performance chain") {
					t.Fatalf("expired C3 prefix allowed manual selection: %v", err)
				}
			} else if _, err := svc.rollbackPaperCandidate(context.Background(), claim.StrategySelectionEventID, claim.StrategySelectionEventID); err == nil || !strings.Contains(err.Error(), "incomplete paper performance chain") {
				t.Fatalf("expired C3+D prefix allowed manual rollback: %v", err)
			}
		})
	}
}

func TestG38F2ReleasedPartialPrefixAllowsManualSelectionAndIdlesOldClose(t *testing.T) {
	svc, claim, asOf := g38F2ClaimedDueWindow(t)
	if _, err := svc.evaluatePaperPerformanceWithClaim(context.Background(), k2aAccountRef, asOf, claim); err != nil {
		t.Fatal(err)
	}
	if err := svc.releasePaperRunnerLease(context.Background(), claim); err != nil {
		t.Fatal(err)
	}
	challenger := rehashedStrategyArtifact(t, strategyArtifact(t, nil), func(result map[string]any) {
		result["experiment_id"] = "g38f2-released-prefix-challenger"
	})
	evidence, err := svc.registerStrategyEvidence(context.Background(), challenger)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := svc.selectPaperCandidate(context.Background(), evidence.ResultSHA256, claim.StrategySelectionEventID)
	if err != nil {
		t.Fatal(err)
	}
	newClaim, err := svc.acquirePaperRunnerLease(context.Background(), k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	if selected.CurrentEventID != newClaim.StrategySelectionEventID {
		t.Fatalf("new claim selection=%s want=%s", newClaim.StrategySelectionEventID, selected.CurrentEventID)
	}
	if event, err := svc.evaluatePaperPerformanceWithClaim(context.Background(), k2aAccountRef, asOf, newClaim); event != nil || !errors.Is(err, errPaperRunnerPriorSelection) {
		t.Fatalf("old close was not classified as prior-selection idle: event=%+v err=%v", event, err)
	}
	if err := svc.releasePaperRunnerLease(context.Background(), newClaim); err != nil {
		t.Fatal(err)
	}
}
