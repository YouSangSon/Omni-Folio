//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Wire real file descriptors, not a Go copy goroutine or a shell that hides
// either exit status. Parent copies close after Start so EOF/HUP are genuine.
func startPaperPipeline(t *testing.T, bin, dbPath, selection, inputs string, timeout time.Duration) (*localPaperChild, *localPaperChild) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	consumer := &localPaperChild{command: exec.CommandContext(ctx, bin, "paper-execute-stream", "-db", dbPath,
		"-account", k2aAccountRef, "-expected-current-event", selection, "-arm-paper"), done: make(chan struct{})}
	producer := &localPaperChild{command: exec.CommandContext(ctx, "python3", "-B", "-m", "omni_research.signal_cli",
		"--bars", filepath.Join(inputs, "bars.csv"), "--research-bars", filepath.Join(inputs, "research.csv"),
		"--artifact", filepath.Join(inputs, "artifact.json"), "--watch", "--bundle"), done: make(chan struct{})}
	producer.command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONPATH="+filepath.Join(root, "services/research"))
	consumer.command.Stdin, consumer.command.Stdout = reader, &consumer.stdout
	producer.command.Stdout = writer
	for _, child := range []*localPaperChild{consumer, producer} {
		child.command.Stderr = &child.stderr
		child.command.WaitDelay = 5 * time.Second
		if err := child.command.Start(); err != nil {
			t.Fatal(err)
		}
		go func() { child.err = child.command.Wait(); close(child.done) }()
		t.Cleanup(func() { cancel(); child.wait(t) })
	}
	return producer, consumer
}

func replacePaperPipelineInput(t *testing.T, path string, raw []byte) {
	t.Helper()
	// Same-directory rename models the documented source-publication contract.
	temporary := path + ".next"
	if err := os.WriteFile(temporary, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
}

func waitPaperPipelineCounts(t *testing.T, svc *Service, producer, consumer *localPaperChild, orders, fills int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, child := range []*localPaperChild{producer, consumer} {
			select {
			case <-child.done:
				t.Fatalf("pipeline ended before durable progress: %v %s", child.err, child.stderr.String())
			default:
			}
		}
		counts := paperAdmissionCountsForTest(t, svc)
		if counts.Orders == orders && counts.Fills == fills {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("pipeline did not reach expected durable orders/fills")
}

func TestPaperPipelineActualWatchAndExecution(t *testing.T) {
	bin := buildLocalPaperExecutable(t)
	for _, ending := range []string{"consumer_signal", "producer_signal", "policy_halt"} {
		t.Run(ending, func(t *testing.T) {
			svc, dbPath, research, evidence, selected, rows := localPaperRestartFixture(t)
			svc.now = time.Now
			inputs := t.TempDir()
			barsPath := filepath.Join(inputs, "bars.csv")
			for name, raw := range map[string][]byte{"bars.csv": paperSnapshotCSV(rows...), "research.csv": research, "artifact.json": []byte(evidence.artifactJSON)} {
				replacePaperPipelineInput(t, filepath.Join(inputs, name), raw)
			}
			producer, consumer := startPaperPipeline(t, bin, dbPath, selected.CurrentEventID, inputs, 45*time.Second)
			waitPaperPipelineCounts(t, svc, producer, consumer, 1, 0)
			rows = append(rows, localPaperRestartRow("2026-05-10", "100", "100", "100"))
			replacePaperPipelineInput(t, barsPath, paperSnapshotCSV(rows...))
			waitPaperPipelineCounts(t, svc, producer, consumer, 1, 1)
			switch ending {
			case "consumer_signal":
				if err := consumer.command.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			case "producer_signal":
				if err := producer.command.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			case "policy_halt":
				rows = append(rows, localPaperRestartRow("2026-05-11", "1", "1", "100"))
				replacePaperPipelineInput(t, barsPath, paperSnapshotCSV(rows...))
			}
			// Join both before assertions or reading their captured stderr.
			consumer.wait(t)
			producer.wait(t)
			// A cancellation error must not be mistaken for durable corruption.
			if _, err := provePaperPerformancePolicyRecovery(context.Background(), svc.db); err != nil {
				t.Fatalf("post-exit durable recovery failed: %v", err)
			}
			cleanState, cleanErr := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
			cleanLease, leaseCleanErr := loadPaperRunnerLease(context.Background(), svc.db)
			if cleanErr != nil || leaseCleanErr != nil || cleanState.Armed || cleanState.LeaseOwner != "" || cleanLease.record.OwnerID != "" {
				t.Fatalf("post-exit cleanup failed: %+v %+v %v/%v", cleanState, cleanLease, cleanErr, leaseCleanErr)
			}
			if consumer.stdout.Len() != 0 || producer.err == nil {
				t.Fatalf("unexpected pipeline result: %v/%v", producer.err, consumer.err)
			}
			if ending == "consumer_signal" {
				if consumer.err == nil || !strings.Contains(consumer.stderr.String(), "context canceled") {
					t.Fatalf("consumer signal was not handled: %v %s", consumer.err, consumer.stderr.String())
				}
			} else if consumer.err != nil {
				t.Fatalf("EOF/policy exit failed: %v %s", consumer.err, consumer.stderr.String())
			}
			if ending != "producer_signal" && producer.stderr.String() != "Paper signal proposal output is closed; producer stopped.\n" {
				t.Fatalf("idle producer did not observe closed reader: %v %s", producer.err, producer.stderr.String())
			}
			state, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
			lease, leaseErr := loadPaperRunnerLease(context.Background(), svc.db)
			counts := paperAdmissionCountsForTest(t, svc)
			if err != nil || leaseErr != nil || state.Armed || state.LeaseOwner != "" || lease.record.OwnerID != "" || counts.Orders != 1 || counts.Fills != 1 || counts.Signals != 1 {
				t.Fatalf("pipeline leaked or duplicated: %+v %+v %+v %v/%v", state, lease, counts, err, leaseErr)
			}
			registry, err := replayStrategyRegistry(context.Background(), svc.db)
			if err != nil {
				t.Fatal(err)
			}
			if ending == "policy_halt" {
				if registry.SelectedResultSHA256 != noStrategySelection {
					t.Fatal("adverse new close did not roll back")
				}
				result, found, err := loadScheduledPaperRunResult(context.Background(), svc.db, k2aAccountRef, "2026-05-11T06:30:00.000000000Z")
				if err != nil || !found || result.Decision != "HALT_AND_ROLLBACK" || result.AutomaticHaltCount != 1 || result.RollbackSelectionEventID != registry.CurrentEventID {
					t.Fatalf("missing automatic halt provenance: %+v %v", result, err)
				}
			} else if registry.CurrentEventID != selected.CurrentEventID {
				t.Fatal("signal changed strategy selection")
			}
			// New explicit invocation, same files: ordinary stop permits a
			// duplicate-safe replay, whereas a policy rollback denies old scope.
			retryProducer, retryConsumer := startPaperPipeline(t, bin, dbPath, selected.CurrentEventID, inputs, 45*time.Second)
			if ending != "policy_halt" {
				waitPaperStreamState(t, svc, retryConsumer, 10*time.Second, func(s executionAuthoritySnapshot) bool { return s.Armed && s.LeaseOwner != "" })
				if err := retryProducer.command.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			retryConsumer.wait(t)
			retryProducer.wait(t)
			if (retryConsumer.err != nil) != (ending == "policy_halt") || retryProducer.err == nil || retryConsumer.stdout.Len() != 0 || paperAdmissionCountsForTest(t, svc) != counts {
				t.Fatalf("reconnect changed durable decisions: %v/%v %s", retryProducer.err, retryConsumer.err, retryConsumer.stderr.String())
			}
			state, err = loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
			lease, leaseErr = loadPaperRunnerLease(context.Background(), svc.db)
			if err != nil || leaseErr != nil || state.Armed || state.LeaseOwner != "" || lease.record.OwnerID != "" {
				t.Fatalf("reconnect leaked: %+v %+v %v/%v", state, lease, err, leaseErr)
			}
			if _, err := provePaperPerformancePolicyRecovery(context.Background(), svc.db); err != nil {
				t.Fatalf("pipeline history does not recover: %v", err)
			}
			// The processes own no source writes, and publish never leaves a tail.
			for name, want := range map[string][]byte{"bars.csv": paperSnapshotCSV(rows...), "research.csv": research, "artifact.json": []byte(evidence.artifactJSON)} {
				got, err := os.ReadFile(filepath.Join(inputs, name))
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("source mutated: %s %v", name, err)
				}
			}
			files, err := os.ReadDir(inputs)
			if err != nil || len(files) != 3 {
				t.Fatalf("source inventory leaked: %v %v", files, err)
			}
		})
	}
}
