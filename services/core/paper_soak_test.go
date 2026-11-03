//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This is actual elapsed operation, not the accelerated history profile. It
// exercises a bounded local workload; two minutes cannot certify daily uptime.
func TestPaperPipelineRealClockSoak(t *testing.T) {
	if os.Getenv("OMNI_PAPER_SOAK") != "1" {
		t.Skip("set OMNI_PAPER_SOAK=1 through the root test wrapper")
	}
	bin := buildLocalPaperExecutable(t)
	svc, dbPath, research, evidence, selected, rows := localPaperRestartFixture(t)
	svc.now = time.Now
	inputs := t.TempDir()
	for name, raw := range map[string][]byte{"bars.csv": paperSnapshotCSV(rows...), "research.csv": research, "artifact.json": []byte(evidence.artifactJSON)} {
		replacePaperPipelineInput(t, filepath.Join(inputs, name), raw)
	}
	producer, consumer := startPaperPipeline(t, bin, dbPath, selected.CurrentEventID, inputs, 3*time.Minute)
	waitPaperPipelineCounts(t, svc, producer, consumer, 1, 0)
	first := waitPaperStreamState(t, svc, consumer, 10*time.Second, func(s executionAuthoritySnapshot) bool { return s.Armed && s.LeaseOwner != "" })
	global, err := loadPaperRunnerLease(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastFence, lastHeartbeat := first.FencingToken, global.record.HeartbeatAtNS
	minimumHeadroom := syntheticExecutionLeaseTTL
	nextPublish, published, samples := started.Add(10*time.Second), 0, 0
	lastDay := mustTime("2026-05-09T06:30:00Z")
	for published < 12 || time.Since(started) < 2*time.Minute {
		<-ticker.C
		for _, child := range []*localPaperChild{producer, consumer} {
			select {
			case <-child.done:
				t.Fatalf("soak process ended early: %v %s", child.err, child.stderr.String())
			default:
			}
		}
		if published < 12 && !time.Now().Before(nextPublish) {
			lastDay = lastDay.AddDate(0, 0, 1)
			rows = append(rows, localPaperRestartRow(lastDay.Format("2006-01-02"), "100", "100", "100"))
			replacePaperPipelineInput(t, filepath.Join(inputs, "bars.csv"), paperSnapshotCSV(rows...))
			published++
			nextPublish = nextPublish.Add(10 * time.Second)
			// Watch coalesces snapshots. Acknowledge this close's durable policy
			// before replacing the source again, not merely its earlier fill.
			deadline := time.Now().Add(5 * time.Second)
			for {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				result, found, err := loadScheduledPaperRunResult(ctx, svc.db, k2aAccountRef, lastDay.Format("2006-01-02T15:04:05.000000000Z"))
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				if found {
					if result.Decision != "HOLD" {
						t.Fatalf("unexpected soak policy: %+v", result)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("soak close did not reach durable policy")
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
		// Read both owned leases in one bounded snapshot, never compare rows
		// from opposite sides of a concurrent renewal commit.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		tx, err := svc.db.BeginTx(ctx, nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		current, stateErr := loadExecutionAuthoritySnapshot(ctx, tx, k2aAccountRef)
		lease, leaseErr := loadPaperRunnerLease(ctx, tx)
		_ = tx.Rollback()
		cancel()
		if stateErr != nil || leaseErr != nil {
			t.Fatalf("soak observation failed: %v/%v", stateErr, leaseErr)
		}
		expires, ok := canonicalUTCTime(current.LeaseExpiresAt)
		now := time.Now()
		headroom := min(expires.Sub(now), time.Unix(0, lease.record.ExpiresAtNS).Sub(now))
		if !ok || !current.Armed || current.LeaseOwner != first.LeaseOwner || current.FencingToken < lastFence ||
			lease.record.OwnerID != global.record.OwnerID || lease.record.FencingToken != global.record.FencingToken ||
			lease.record.StrategySelectionEventID != selected.CurrentEventID || lease.record.HeartbeatAtNS < lastHeartbeat || headroom <= 0 {
			t.Fatal("soak lost owner, monotonic fence/heartbeat, selection or lease validity")
		}
		minimumHeadroom = min(minimumHeadroom, headroom)
		lastFence, lastHeartbeat = current.FencingToken, lease.record.HeartbeatAtNS
		samples++
		if samples%20 == 0 {
			t.Logf("soak elapsed=%s published=%d renewals=%d min_observed_headroom=%s", time.Since(started).Round(time.Millisecond), published, lastFence-first.FencingToken, minimumHeadroom.Round(time.Millisecond))
		}
	}
	waitPaperPipelineCounts(t, svc, producer, consumer, 1, 1)
	result, found, err := loadScheduledPaperRunResult(context.Background(), svc.db, k2aAccountRef, lastDay.Format("2006-01-02T15:04:05.000000000Z"))
	if err != nil || !found || result.Decision != "HOLD" || published != 12 || samples < 100 || lastFence-first.FencingToken < 10 || time.Since(started) < 2*time.Minute {
		t.Fatalf("soak did not process sustained input/policy/renewal: %+v %v", result, err)
	}
	if err := consumer.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	consumer.wait(t)
	producer.wait(t)
	if consumer.err == nil || !strings.Contains(consumer.stderr.String(), "context canceled") || consumer.stdout.Len() != 0 ||
		producer.err == nil || producer.stderr.String() != "Paper signal proposal output is closed; producer stopped.\n" {
		t.Fatalf("soak shutdown failed: %v/%v", consumer.err, producer.err)
	}
	// Current-schema runner proof recursively verifies policy, strategy
	// performance, valuation, accounting, orders and execution authority.
	if _, err := provePaperRunnerLeaseRecovery(context.Background(), svc.db); err != nil {
		t.Fatal(err)
	}
	registry, err := replayStrategyRegistry(context.Background(), svc.db)
	if err != nil || registry.CurrentEventID != selected.CurrentEventID {
		t.Fatalf("soak changed strategy selection: %v", err)
	}
	counts := paperAdmissionCountsForTest(t, svc)
	if counts.Signals != 1 || counts.Orders != 1 || counts.Authorizations != 1 || counts.Fills != 1 {
		t.Fatalf("soak duplicated a trading decision: %+v", counts)
	}
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM paper_market_bar_observations`:                                 len(rows),
		`SELECT COUNT(*) FROM paper_performance_events`:                                      13,
		`SELECT SUM(mark_count) FROM paper_performance_events`:                               12,
		`SELECT COUNT(*) FROM paper_strategy_performance_events`:                             13,
		`SELECT SUM(sample_count) FROM paper_strategy_performance_events`:                    91,
		`SELECT COUNT(*) FROM paper_performance_policy_events WHERE decision='INSUFFICIENT'`: 1,
		`SELECT COUNT(*) FROM paper_performance_policy_events WHERE decision='HOLD'`:         12,
		`SELECT SUM(automatic_halt_count) FROM paper_performance_policy_events`:              0,
	} {
		var got int
		if err := svc.db.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Fatalf("soak durable history mismatch: %s got=%d want=%d err=%v", query, got, want, err)
		}
	}
	state, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
	lease, leaseErr := loadPaperRunnerLease(context.Background(), svc.db)
	var arms, policies int
	countErr := svc.db.QueryRow(`SELECT (SELECT COUNT(*) FROM execution_authority_events WHERE reason_code='manual_arm'), (SELECT COUNT(*) FROM paper_performance_policy_events)`).Scan(&arms, &policies)
	if err != nil || leaseErr != nil || countErr != nil || state.Armed || state.LeaseOwner != "" || lease.record.OwnerID != "" || arms != 1 || policies != 13 {
		t.Fatalf("soak cleanup or policy count failed: arms=%d policies=%d errors=%v/%v/%v", arms, policies, err, leaseErr, countErr)
	}
	for name, want := range map[string][]byte{"bars.csv": paperSnapshotCSV(rows...), "research.csv": research, "artifact.json": []byte(evidence.artifactJSON)} {
		got, err := os.ReadFile(filepath.Join(inputs, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("soak source changed: %s %v", name, err)
		}
	}
	files, err := os.ReadDir(inputs)
	if err != nil || len(files) != 3 {
		t.Fatalf("soak source inventory leaked: %v %v", files, err)
	}
	t.Logf("soak passed elapsed=%s samples=%d snapshots=%d policies=%d renewals=%d min_observed_headroom=%s", time.Since(started).Round(time.Millisecond), samples, published, policies, lastFence-first.FencingToken, minimumHeadroom.Round(time.Millisecond))
}
