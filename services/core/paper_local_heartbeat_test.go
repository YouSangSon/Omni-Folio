package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalPaperHeartbeatRetainsAuthorityPastOriginalExpiry(t *testing.T) {
	svc, signal, _ := paperProposalFixture(t)
	ctx := context.Background()
	now := mustTime("2026-01-10T07:00:00Z")
	svc.now = func() time.Time { return now }
	claim, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := svc.startLocalPaperExecutionWithClaim(ctx, k2aAccountRef, signal.StrategySelectionEventID, claim)
	if err != nil {
		t.Fatal(err)
	}
	firstClaim, firstFence := *claim, lease.FencingToken
	proposal := paperProposalForTest(t, svc, signal, "golden_cross")
	_, order, err := svc.processPaperProposal(ctx, k2aAccountRef, signal.StrategySelectionEventID, proposal, signal.SignalBarObservationID, lease.FencingToken, true, claim)
	if err != nil || order == nil {
		t.Fatalf("initial admission: %+v %v", order, err)
	}
	for i := 0; i < 4; i++ {
		now = now.Add(10 * time.Second)
		nextClaim, nextLease, err := svc.heartbeatLocalPaperExecution(ctx, claim, lease.FencingToken)
		if err != nil {
			t.Fatal(err)
		}
		if nextClaim.FencingToken != claim.FencingToken || nextLease.FencingToken != lease.FencingToken+1 || nextLease.LeaseOwner != lease.LeaseOwner {
			t.Fatal("unexpected renewal identity")
		}
		claim, lease = nextClaim, nextLease
	}
	if _, err := svc.requireCurrentSyntheticExecutionLease(ctx, svc.db, k2aAccountRef, firstFence, now); err == nil {
		t.Fatal("stale execution fence retained authority")
	}
	if err := validatePaperRunnerLeaseTx(ctx, svc.db, &firstClaim, k2aAccountRef, svc.paperRunnerOwner, now); err == nil {
		t.Fatal("old global heartbeat tuple retained authority")
	}
	_, replay, err := svc.processPaperProposal(ctx, k2aAccountRef, signal.StrategySelectionEventID, proposal, signal.SignalBarObservationID, lease.FencingToken, true, claim)
	if err != nil || replay == nil || replay.OrderID != order.OrderID {
		t.Fatalf("renewed authority lost original admission: %+v %v", replay, err)
	}
	recordG38C2FillBar(t, svc, "renewed-fill-bar", "101", "100", "2026-01-10")
	if _, err := svc.runPaperOrderWithClaim(ctx, order.OrderID, firstFence, claim); err == nil {
		t.Fatal("stale execution fence filled order")
	}
	filled, err := svc.runPaperOrderWithClaim(ctx, order.OrderID, lease.FencingToken, claim)
	if err != nil || filled.Status != "FILLED" || filled.FilledQuantity != "10" {
		t.Fatalf("renewed fill: %+v %v", filled, err)
	}
	latest, err := loadExecutionAuthoritySnapshot(ctx, svc.db, k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	var fillFence int64
	var fillAuthority string
	if err := svc.db.QueryRow(`SELECT json_extract(event_json,'$.fencing_token'),json_extract(event_json,'$.execution_authority_event_id') FROM order_events WHERE order_id=? AND event_type='FILL_RECORDED'`, order.OrderID).Scan(&fillFence, &fillAuthority); err != nil {
		t.Fatal(err)
	}
	if fillFence != lease.FencingToken || fillAuthority != latest.EventID {
		t.Fatal("fill did not use newest renewal authority")
	}
	if _, err := provePaperPerformancePolicyRecovery(ctx, svc.db); err != nil {
		t.Fatal(err)
	}
	if err := svc.haltOwnedSyntheticExecutionLease(ctx, k2aAccountRef, lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if err := svc.releasePaperRunnerLease(ctx, claim); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "renewed.db")
	golden := writeCurrentSnapshot(t, svc.db)
	if _, err := createBackup(svc.db, backup, golden, backup+".manifest.json", svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(backup, golden, backup+".manifest.json"); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err != nil {
		t.Fatal(err)
	}
}

func TestLocalPaperHeartbeatRejectsLostAuthorityWithoutPartialWrites(t *testing.T) {
	for _, kind := range []string{"halted", "stale_fence", "foreign_owner", "global_expiry", "execution_expiry", "clock_regression", "append_failure", "global_write_failure"} {
		t.Run(kind, func(t *testing.T) {
			svc, signal, _ := paperProposalFixture(t)
			ctx := context.Background()
			now := mustTime("2026-01-10T07:00:00Z")
			svc.now = func() time.Time { return now }
			claim, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := svc.startLocalPaperExecutionWithClaim(ctx, k2aAccountRef, signal.StrategySelectionEventID, claim)
			if err != nil {
				t.Fatal(err)
			}
			now = now.Add(10 * time.Second)
			switch kind {
			case "halted":
				if _, err := svc.setSyntheticExecutionArmed(ctx, k2aAccountRef, false); err != nil {
					t.Fatal(err)
				}
			case "stale_fence":
				lease.FencingToken--
			case "foreign_owner":
				svc.executionOwner = "execution_owner_foreign"
			case "global_expiry":
				now = now.Add(20 * time.Second)
			case "execution_expiry":
				claim, err = svc.heartbeatPaperRunnerLease(ctx, claim)
				if err != nil {
					t.Fatal(err)
				}
				now = now.Add(20 * time.Second)
			case "clock_regression":
				now = now.Add(-11 * time.Second)
			case "append_failure":
				previous, err := loadExecutionAuthoritySnapshot(ctx, svc.db, k2aAccountRef)
				if err != nil {
					t.Fatal(err)
				}
				svc.id = func(string) string { return previous.EventID }
			case "global_write_failure":
				if _, err := svc.db.Exec(`CREATE TEMP TRIGGER test_abort_heartbeat BEFORE UPDATE ON main.paper_runner_leases BEGIN SELECT RAISE(ABORT,'test heartbeat failure'); END;`); err != nil {
					t.Fatal(err)
				}
			}
			before, err := loadExecutionAuthoritySnapshot(ctx, svc.db, k2aAccountRef)
			if err != nil {
				t.Fatal(err)
			}
			globalBefore, err := loadPaperRunnerLease(ctx, svc.db)
			if err != nil {
				t.Fatal(err)
			}
			oldClaim := *claim
			nextClaim, nextLease, err := svc.heartbeatLocalPaperExecution(ctx, claim, lease.FencingToken)
			if err == nil || nextClaim != nil || nextLease != nil {
				t.Fatalf("%s renewed: %+v %+v %v", kind, nextClaim, nextLease, err)
			}
			if kind == "global_write_failure" && !strings.Contains(err.Error(), "test heartbeat failure") {
				t.Fatalf("test missed second-write failure: %v", err)
			}
			after, err := loadExecutionAuthoritySnapshot(ctx, svc.db, k2aAccountRef)
			if err != nil || after != before || *claim != oldClaim {
				t.Fatalf("failed renewal changed execution: %v", err)
			}
			globalAfter, err := loadPaperRunnerLease(ctx, svc.db)
			if err != nil || globalBefore.record != globalAfter.record {
				t.Fatalf("failed renewal changed runner: %v", err)
			}
		})
	}
}

func TestLocalPaperRenewalReplayRejectsForeignClockAndExpiryDrift(t *testing.T) {
	previous := executionAuthorityRecord{EventID: "authority_first", AccountRef: k2aAccountRef, Armed: true, LeaseOwner: "execution_owner_local", FencingToken: 2, LeaseExpiresAt: "2026-01-10T07:00:30Z", ReasonCode: "lease_acquired", RecordedAt: "2026-01-10T07:00:00Z"}
	next := previous
	next.EventID, next.FencingToken, next.RecordedAt, next.LeaseExpiresAt = "authority_next", 3, "2026-01-10T07:00:10Z", "2026-01-10T07:00:40Z"
	if err := validateExecutionAuthorityRecord(next, &previous); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*executionAuthorityRecord){
		func(r *executionAuthorityRecord) { r.LeaseOwner = "execution_owner_foreign" },
		func(r *executionAuthorityRecord) { r.RecordedAt = "2026-01-10T06:59:59Z" },
		func(r *executionAuthorityRecord) { r.LeaseExpiresAt = "2026-01-10T07:00:30Z" },
		func(r *executionAuthorityRecord) { r.LeaseExpiresAt = "2026-01-10T07:00:29Z" },
		func(r *executionAuthorityRecord) { r.FencingToken = 2 },
	} {
		bad := next
		mutate(&bad)
		if err := validateExecutionAuthorityRecord(bad, &previous); err == nil {
			t.Fatalf("invalid renewal replayed: %+v", bad)
		}
	}
}

func TestLocalPaperStepRenewsBeforeNextPhase(t *testing.T) {
	svc, _, research, evidence, selected, rows := localPaperRestartFixture(t)
	now := svc.now()
	svc.now = func() time.Time { return now }
	ids := svc.id
	authorityIDs := 0
	svc.id = func(prefix string) string {
		if prefix == "execution_authority" {
			authorityIDs++
			// Advance the injected clock after the initial lease's timestamp
			// was sampled. No sleep or production test-only hook is needed.
			if authorityIDs == 2 {
				now = now.Add(10 * time.Second)
			}
		}
		return ids(prefix)
	}
	bars := paperSnapshotCSV(rows...)
	proposal := localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], bars, "golden_cross")
	result, err := svc.executeLocalPaper(context.Background(), k2aAccountRef, selected.CurrentEventID, proposal, bars, research)
	if err != nil || result == nil || result.Order == nil {
		t.Fatalf("step failed: %+v %v", result, err)
	}
	authority, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
	// Explicit arm -> acquire -> renew -> owned halt, with no second arm.
	if err != nil || authority.Armed || authority.LeaseOwner != "" || authority.FencingToken != 4 || authority.count != 4 {
		t.Fatalf("step did not renew and clean up latest fence: %+v %v", authority, err)
	}
}

func TestLocalPaperHeartbeatConcurrentSameTokenHasOneWinner(t *testing.T) {
	svc, signal, _ := paperProposalFixture(t)
	ctx := context.Background()
	now := mustTime("2026-01-10T07:00:00Z")
	svc.now = func() time.Time { return now }
	claim, err := svc.acquirePaperRunnerLease(ctx, k2aAccountRef)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := svc.startLocalPaperExecutionWithClaim(ctx, k2aAccountRef, signal.StrategySelectionEventID, claim)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second)
	type outcome struct {
		claim *paperRunnerClaim
		lease *ExecutionAuthorityState
		err   error
	}
	results := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			c, l, e := svc.heartbeatLocalPaperExecution(ctx, claim, lease.FencingToken)
			results <- outcome{c, l, e}
		}()
	}
	winners := 0
	var winner outcome
	// Join both workers even if the first outcome later fails an assertion.
	for _, result := range []outcome{<-results, <-results} {
		if result.err == nil {
			winners++
			winner = result
		} else if result.claim != nil || result.lease != nil {
			t.Fatal("loser published tokens")
		}
	}
	if winners != 1 || winner.lease.FencingToken != lease.FencingToken+1 {
		t.Fatalf("renewal winners=%d", winners)
	}
	if _, err := provePaperRunnerLeaseRecovery(ctx, svc.db); err != nil {
		t.Fatal(err)
	}
	if err := svc.haltOwnedSyntheticExecutionLease(ctx, k2aAccountRef, winner.lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	if err := svc.releasePaperRunnerLease(ctx, winner.claim); err != nil {
		t.Fatal(err)
	}
}
