//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func startPaperStreamChild(t *testing.T, bin string, args []string) (*localPaperChild, *os.File) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	child := &localPaperChild{command: exec.CommandContext(ctx, bin, args...), done: make(chan struct{})}
	child.command.Stdin = reader
	child.command.Stdout, child.command.Stderr = &child.stdout, &child.stderr
	if err := child.command.Start(); err != nil {
		cancel()
		_ = reader.Close()
		_ = writer.Close()
		t.Fatal(err)
	}
	_ = reader.Close()
	go func() { child.err = child.command.Wait(); close(child.done) }()
	t.Cleanup(func() {
		_ = writer.Close()
		cancel()
		child.wait(t)
	})
	return child, writer
}

func waitPaperStreamState(t *testing.T, svc *Service, child *localPaperChild, timeout time.Duration, ready func(executionAuthoritySnapshot) bool) executionAuthoritySnapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-child.done:
			t.Fatalf("stream exited before state: %v %s", child.err, child.stderr.String())
		default:
		}
		state, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
		if err != nil {
			t.Fatal(err)
		}
		if ready(state) {
			return state
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("stream state deadline")
	return executionAuthoritySnapshot{}
}

func TestPaperStreamExecutableLifecycle(t *testing.T) {
	bin := buildLocalPaperExecutable(t)
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String()+"_armed", func(t *testing.T) {
			svc, dbPath, research, evidence, selected, rows := localPaperRestartFixture(t)
			svc.now = time.Now
			raw := paperStreamBundle(t, svc, evidence.ResultSHA256, rows, research, "golden_cross")
			args := []string{"paper-execute-stream", "-db", dbPath, "-account", k2aAccountRef, "-expected-current-event", selected.CurrentEventID, "-arm-paper"}
			child, writer := startPaperStreamChild(t, bin, args)
			if _, err := writer.Write(raw); err != nil {
				t.Fatal(err)
			}
			owned := waitPaperStreamState(t, svc, child, 10*time.Second, func(s executionAuthoritySnapshot) bool { return s.Armed && s.LeaseOwner != "" })
			if err := child.command.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			child.wait(t)
			var exit *exec.ExitError
			if !errors.As(child.err, &exit) || exit.ExitCode() != 1 || !strings.Contains(child.stderr.String(), "context canceled") || child.stdout.Len() != 0 {
				t.Fatalf("unhandled armed signal: %v %s", child.err, child.stderr.String())
			}
			state, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
			global, globalErr := loadPaperRunnerLease(context.Background(), svc.db)
			if err != nil || globalErr != nil || state.Armed || state.LeaseOwner != "" || state.FencingToken != owned.FencingToken+1 || global.record.OwnerID != "" {
				t.Fatalf("armed signal leaked: %+v %+v %v/%v", state, global, err, globalErr)
			}
		})
		t.Run(sig.String()+"_partial", func(t *testing.T) {
			svc, dbPath, _, _, selected, _ := localPaperRestartFixture(t)
			args := []string{"paper-execute-stream", "-db", dbPath, "-account", k2aAccountRef, "-expected-current-event", selected.CurrentEventID, "-arm-paper"}
			child, writer := startPaperStreamChild(t, bin, args)
			// Larger than the native pipe buffer: write completion proves the
			// child entered its reader after installing signal handling. The
			// unterminated frame remains below the protocol limit.
			if _, err := writer.Write(bytes.Repeat([]byte{' '}, 2*maxBodyBytes)); err != nil {
				t.Fatal(err)
			}
			if err := child.command.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			child.wait(t)
			var exit *exec.ExitError
			if !errors.As(child.err, &exit) || exit.ExitCode() != 1 || !strings.Contains(child.stderr.String(), "context canceled") || child.stdout.Len() != 0 {
				t.Fatalf("unhandled partial-input signal: %v %s", child.err, child.stderr.String())
			}
			state, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
			if err != nil || state.Armed || state.FencingToken != 0 {
				t.Fatalf("partial input armed: %+v %v", state, err)
			}
			if _, err := writer.Write([]byte("}")); err == nil {
				t.Fatal("child retained input reader")
			}
		})
	}
	t.Run("idle_renewal_halt_and_explicit_replay", func(t *testing.T) {
		svc, dbPath, research, evidence, selected, rows := localPaperRestartFixture(t)
		svc.now = time.Now
		raw := paperStreamBundle(t, svc, evidence.ResultSHA256, rows, research, "golden_cross")
		args := []string{"paper-execute-stream", "-db", dbPath, "-account", k2aAccountRef, "-expected-current-event", selected.CurrentEventID, "-arm-paper"}
		child, writer := startPaperStreamChild(t, bin, args)
		if _, err := writer.Write(raw); err != nil {
			t.Fatal(err)
		}
		first := waitPaperStreamState(t, svc, child, 10*time.Second, func(s executionAuthoritySnapshot) bool { return s.Armed && s.LeaseOwner != "" })
		originalExpiry, err := time.Parse(time.RFC3339Nano, first.LeaseExpiresAt)
		if err != nil {
			t.Fatal(err)
		}
		renewed := waitPaperStreamState(t, svc, child, 45*time.Second, func(s executionAuthoritySnapshot) bool {
			return time.Now().After(originalExpiry) && s.FencingToken > first.FencingToken
		})
		if renewed.LeaseOwner != first.LeaseOwner {
			t.Fatal("idle runner changed owner")
		}
		before := paperAdmissionCountsForTest(t, svc)
		if before.Orders != 1 {
			t.Fatalf("first frame not admitted: %+v", before)
		}
		halted, err := svc.setSyntheticExecutionArmed(context.Background(), k2aAccountRef, false)
		if err != nil {
			t.Fatal(err)
		}
		// Keep pipe open without new input: the idle heartbeat must discover halt.
		child.wait(t)
		if child.err == nil || child.stdout.Len() != 0 {
			t.Fatalf("halt did not stop stream: %v", child.err)
		}
		state, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
		global, globalErr := loadPaperRunnerLease(context.Background(), svc.db)
		var arms int
		countErr := svc.db.QueryRow("SELECT COUNT(*) FROM execution_authority_events WHERE reason_code='manual_arm'").Scan(&arms)
		if err != nil || globalErr != nil || countErr != nil || state.Armed || state.FencingToken != halted.FencingToken || global.record.OwnerID != "" || arms != 1 || paperAdmissionCountsForTest(t, svc) != before {
			t.Fatalf("halt rearmed/leaked: %+v %+v arms=%d errors=%v/%v/%v", state, global, arms, err, globalErr, countErr)
		}
		if _, err := writer.Write(raw); err == nil {
			t.Fatal("halt retained reader")
		}
		retry, retryWriter := startPaperStreamChild(t, bin, args)
		if _, err := retryWriter.Write(raw); err != nil {
			t.Fatal(err)
		}
		_ = retryWriter.Close()
		retry.wait(t)
		if retry.err != nil || retry.stdout.Len() != 0 || paperAdmissionCountsForTest(t, svc) != before {
			t.Fatalf("explicit EOF replay duplicated/failed: %v %s", retry.err, retry.stderr.String())
		}
		state, err = loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
		global, globalErr = loadPaperRunnerLease(context.Background(), svc.db)
		if err != nil || globalErr != nil || state.Armed || state.LeaseOwner != "" || global.record.OwnerID != "" {
			t.Fatalf("EOF leaked: %+v %+v %v/%v", state, global, err, globalErr)
		}
	})
}
