package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const (
	g38F2PaperRunnerSignalHelperEnv  = "OMNI_FOLIO_G38F2_PAPER_RUNNER_SIGNAL_HELPER"
	g38F2PaperRunnerSignalDBEnv      = "OMNI_FOLIO_G38F2_PAPER_RUNNER_SIGNAL_DB"
	g38F2PaperRunnerSignalAccountEnv = "OMNI_FOLIO_G38F2_PAPER_RUNNER_SIGNAL_ACCOUNT"
)

func TestG38F2PaperRunnerCLIStopsOnSignal(t *testing.T) {
	for _, test := range []struct {
		name string
		sig  os.Signal
	}{
		{name: "SIGINT", sig: os.Interrupt},
		{name: "SIGTERM", sig: syscall.SIGTERM},
	} {
		t.Run(test.name, func(t *testing.T) {
			dbPath := g38F2PaperRunnerNoDueDB(t)
			command := exec.Command(os.Args[0], "-test.run", "^TestG38F2PaperRunnerCLIHelper$")
			command.Env = append(os.Environ(),
				g38F2PaperRunnerSignalHelperEnv+"=1",
				g38F2PaperRunnerSignalDBEnv+"="+dbPath,
				g38F2PaperRunnerSignalAccountEnv+"="+k2aAccountRef,
			)
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			waitDone := make(chan struct{})
			var waitErr error
			go func() {
				waitErr = command.Wait()
				close(waitDone)
			}()
			t.Cleanup(func() {
				select {
				case <-waitDone:
					return
				default:
				}
				_ = command.Process.Kill()
				select {
				case <-waitDone:
				case <-time.After(time.Second):
				}
			})

			owner, fence, err := waitForG38F2PaperRunnerLease(dbPath, true, 0)
			if err != nil {
				_ = command.Process.Kill()
				<-waitDone
				t.Fatalf("paper-run-loop did not acquire the exact global lease: %v\n%s", err, output.String())
			}
			if owner == "" || fence <= 0 {
				t.Fatalf("active lease owner=%q fencing_token=%d", owner, fence)
			}
			if err := command.Process.Signal(test.sig); err != nil {
				_ = command.Process.Kill()
				<-waitDone
				t.Fatalf("send %s: %v\n%s", test.name, err, output.String())
			}

			select {
			case <-waitDone:
				if waitErr != nil {
					t.Fatalf("paper-run-loop %s exit=%v\n%s", test.name, waitErr, output.String())
				}
			case <-time.After(3 * time.Second):
				_ = command.Process.Kill()
				<-waitDone
				t.Fatalf("paper-run-loop %s did not exit within the bounded shutdown deadline\n%s", test.name, output.String())
			}

			releasedOwner, releasedFence, err := waitForG38F2PaperRunnerLease(dbPath, false, fence)
			if err != nil {
				t.Fatal(err)
			}
			if releasedOwner != "" || releasedFence != fence {
				t.Fatalf("signal %s changed the retained lease fence or left owner active: owner=%q fence=%d want fence=%d", test.name, releasedOwner, releasedFence, fence)
			}
		})
	}
}

func TestG38F2PaperRunnerCLIHelper(t *testing.T) {
	if os.Getenv(g38F2PaperRunnerSignalHelperEnv) != "1" {
		return
	}
	dbPath, accountRef := os.Getenv(g38F2PaperRunnerSignalDBEnv), os.Getenv(g38F2PaperRunnerSignalAccountEnv)
	if dbPath == "" || accountRef == "" {
		t.Fatal("paper runner signal helper requires database and account environment")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, []string{"paper-run-loop", "-db", dbPath, "-account", accountRef}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func g38F2PaperRunnerNoDueDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "paper-runner.db")
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = db.Close()
		}
	})
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	svc := newService(db, time.Now, randomID)
	evidence, selected := selectedPaperStrategy(t, svc)
	if selected.SelectedResultSHA256 != evidence.ResultSHA256 || selected.CurrentEventID == "" {
		t.Fatalf("paper runner fixture did not select a strategy: evidence=%+v selection=%+v", evidence, selected)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return dbPath
}

func waitForG38F2PaperRunnerLease(dbPath string, wantActive bool, wantFence int64) (string, int64, error) {
	db, err := openExistingDB(dbPath)
	if err != nil {
		return "", 0, err
	}
	defer db.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var owner string
		var fence int64
		err := db.QueryRow(`SELECT COALESCE(owner_id,''), fencing_token
			FROM paper_runner_leases WHERE scope=?`, paperRunnerLeaseScope).Scan(&owner, &fence)
		if err != nil {
			return "", 0, err
		}
		if (owner != "") == wantActive && (wantFence == 0 || fence == wantFence) {
			return owner, fence, nil
		}
		if !time.Now().Before(deadline) {
			return owner, fence, fmt.Errorf("timed out waiting for paper runner lease active=%t owner=%q fence=%d want_fence=%d", wantActive, owner, fence, wantFence)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		<-timer.C
	}
}
