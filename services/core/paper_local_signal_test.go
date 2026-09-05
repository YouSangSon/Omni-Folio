//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
)

func TestLocalPaperExecutableSignalRecovery(t *testing.T) {
	bin := buildLocalPaperExecutable(t)
	for _, test := range []struct {
		name string
		sig  os.Signal
	}{
		{"SIGINT", os.Interrupt}, {"SIGTERM", syscall.SIGTERM}, {"SIGKILL", syscall.SIGKILL},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, args, orderID := localPaperSignalFixture(t)
			before := paperAdmissionCountsForTest(t, svc)
			baseline, err := loadExecutionAuthoritySnapshot(context.Background(), svc.db, k2aAccountRef)
			if err != nil {
				t.Fatal(err)
			}
			selection, err := replayStrategyRegistry(context.Background(), svc.db)
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.db.Close(); err != nil {
				t.Fatal(err)
			}
			child := startLocalPaperChild(t, bin, args)
			barrier, observed := holdLocalPaperAfterArm(t, args[2], child, baseline.FencingToken, selection.CurrentEventID, selection.SelectedResultSHA256)
			defer barrier.Rollback()
			owned := observed.execution
			if err := child.command.Process.Signal(test.sig); err != nil {
				t.Fatal(err)
			}
			if test.sig == syscall.SIGKILL {
				child.wait(t) // Do not unblock a pending commit before the kill.
			}
			if err := barrier.Rollback(); err != nil {
				t.Fatal(err)
			}
			child.wait(t)
			var exit *exec.ExitError
			if !errors.As(child.err, &exit) || child.stdout.Len() != 0 {
				t.Fatalf("interrupted command reported success: %v\n%s\n%s", child.err, child.stdout.String(), child.stderr.String())
			}
			if test.sig != syscall.SIGKILL && (exit.ExitCode() != 1 || !strings.Contains(child.stderr.String(), "context canceled")) {
				t.Fatalf("signal bypassed main's cancellation handler: %v\n%s", child.err, child.stderr.String())
			}
			if test.sig == syscall.SIGKILL {
				status, ok := exit.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
					t.Fatalf("expected actual SIGKILL, got %v", exit)
				}
			}
			ctx := context.Background()
			db, err := openExistingDB(args[2])
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			svc = newService(db, time.Now, randomID)
			state, err := loadExecutionAuthoritySnapshot(ctx, db, k2aAccountRef)
			if err != nil {
				t.Fatal(err)
			}
			row, err := loadPaperRunnerLease(ctx, svc.db)
			if err != nil {
				t.Fatal(err)
			}
			if test.sig == syscall.SIGKILL {
				if state != owned || row != observed.runner || paperAdmissionCountsForTest(t, svc).Fills != observed.fills {
					t.Fatalf("kill did not interrupt the observed durable owner: %+v", state)
				}
				if !time.Now().Before(time.Unix(0, row.record.ExpiresAtNS)) {
					t.Fatal("fixture missed the live stale-owner exclusion window")
				}
				beforeBusy := paperAdmissionCountsForTest(t, svc)
				busy := startLocalPaperChild(t, bin, args)
				busy.wait(t)
				if busy.err == nil || !strings.Contains(busy.stderr.String(), "paper runner lease is held") || busy.stdout.Len() != 0 {
					t.Fatalf("restart bypassed the unexpired owner: %v\n%s", busy.err, busy.stderr.String())
				}
				busyState, stateErr := loadExecutionAuthoritySnapshot(ctx, db, k2aAccountRef)
				busyRunner, runnerErr := loadPaperRunnerLease(ctx, db)
				if stateErr != nil || runnerErr != nil || busyState != owned || busyRunner != observed.runner || paperAdmissionCountsForTest(t, svc) != beforeBusy {
					t.Fatalf("rejected restart changed the killed owner's state: %v/%v", stateErr, runnerErr)
				}
				// Wait for the actual persisted TTL, never rewrite lease records or
				// inject a future clock into the executable under test.
				expiry, err := time.Parse(time.RFC3339Nano, state.LeaseExpiresAt)
				if err != nil {
					t.Fatal(err)
				}
				if global := time.Unix(0, row.record.ExpiresAtNS); global.After(expiry) {
					expiry = global
				}
				if remaining := time.Until(expiry); remaining > 0 {
					if remaining > paperRunnerLeaseTTL+time.Second {
						t.Fatal("unexpected lease wait")
					}
					t.Logf("waiting %s for the killed owner's persisted expiry", remaining)
					time.Sleep(remaining)
				}
			} else if state.Armed || state.LeaseOwner != "" || row.record.OwnerID != "" || state.FencingToken != owned.FencingToken+1 || row.record.FencingToken != observed.runner.record.FencingToken {
				t.Fatalf("graceful interruption leaked authority: execution=%+v global=%+v", state, row.record)
			}
			after := paperAdmissionCountsForTest(t, svc)
			if after.Orders != before.Orders || after.Signals != before.Signals || after.Authorizations != before.Authorizations || after.Events != before.Events+after.Fills || after.Fills < observed.fills || after.Fills > 10 {
				t.Fatal("interruption changed immutable admission or invalidated the fill prefix")
			}
			// Normal catch-up now includes a safety evaluation at every close.
			// Its work deadline is separate from the unchanged shutdown deadline.
			retry := startLocalPaperChildWithTimeout(t, bin, args, time.Minute)
			select {
			case <-retry.done:
			case <-time.After(time.Minute + 5*time.Second):
				t.Fatal("owned paper recovery exceeded its work deadline")
			}
			retry.wait(t)
			var result LocalPaperStepResult
			if retry.err != nil || json.Unmarshal(retry.stdout.Bytes(), &result) != nil || result.Mode != "paper_fixture_only" || result.Order == nil || result.Order.OrderID != orderID || result.Order.Status != "FILLED" || result.Order.FilledQuantity != "10" {
				t.Fatalf("explicit restart did not return the committed order: %v\n%s\n%s", retry.err, retry.stdout.String(), retry.stderr.String())
			}
			state, err = loadExecutionAuthoritySnapshot(ctx, svc.db, k2aAccountRef)
			row, leaseErr := loadPaperRunnerLease(ctx, svc.db)
			after = paperAdmissionCountsForTest(t, svc)
			wantFence := owned.FencingToken + 4 // halt, manual arm, lease, halt
			if test.sig == syscall.SIGKILL {
				wantFence = owned.FencingToken + 2 // expired lease takeover, halt
			}
			var acquisitions int64
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM execution_authority_events WHERE account_ref=? AND fencing_token>? AND reason_code='lease_acquired'`, k2aAccountRef, owned.FencingToken).Scan(&acquisitions); err != nil || acquisitions < 1 {
				t.Fatalf("restart has no verified lease acquisition: %d %v", acquisitions, err)
			}
			wantFence += acquisitions - 1 // Valid joint renewals during bounded catch-up.
			if err != nil || leaseErr != nil || state.Armed || state.LeaseOwner != "" || row.record.OwnerID != "" || state.FencingToken != wantFence || row.record.FencingToken != observed.runner.record.FencingToken+1 || after.Orders != before.Orders || after.Signals != before.Signals || after.Authorizations != before.Authorizations || after.Events != before.Events+after.Fills || after.Fills != 10 {
				t.Fatalf("restart did not clean owned authority or duplicated history: %+v errors=%v/%v", state, err, leaseErr)
			}
			if _, err := provePaperPerformancePolicyRecovery(ctx, svc.db); err != nil {
				t.Fatal(err)
			}
			accounts, err := replayPaperAccounting(ctx, svc.db)
			if err != nil || accounts[k2aAccountRef].Cash != "8978.99" || accounts[k2aAccountRef].CapitalizedFills != 10 {
				t.Fatalf("restart accounting mismatch: %+v %v", accounts[k2aAccountRef], err)
			}
		})
	}
}

type localPaperChild struct {
	command        *exec.Cmd
	stdout, stderr bytes.Buffer
	done           chan struct{}
	err            error
}

func startLocalPaperChild(t *testing.T, bin string, args []string) *localPaperChild {
	t.Helper()
	return startLocalPaperChildWithTimeout(t, bin, args, 20*time.Second)
}

func startLocalPaperChildWithTimeout(t *testing.T, bin string, args []string, timeout time.Duration) *localPaperChild {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	child := &localPaperChild{command: exec.CommandContext(ctx, bin, args...), done: make(chan struct{})}
	child.command.Stdout, child.command.Stderr = &child.stdout, &child.stderr
	if err := child.command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	go func() { child.err = child.command.Wait(); close(child.done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-child.done:
		case <-time.After(5 * time.Second):
			t.Error("owned paper process was not reaped")
		}
	})
	return child
}

func (child *localPaperChild) wait(t *testing.T) {
	t.Helper()
	select {
	case <-child.done:
	case <-time.After(15 * time.Second):
		_ = child.command.Process.Kill()
		select {
		case <-child.done:
		case <-time.After(5 * time.Second):
			t.Fatal("owned paper process was not reaped after kill")
		}
		t.Fatal("paper process exceeded the shutdown deadline")
	}
}

type localPaperObservation struct {
	execution executionAuthoritySnapshot
	runner    storedPaperRunnerLease
	fills     int
}

func holdLocalPaperAfterArm(t *testing.T, path string, child *localPaperChild, baseline int64, selection, result string) (*sql.Tx, localPaperObservation) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&_busy_timeout=100&_txlock=deferred")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { cancel(); _ = db.Close() })
	var journal string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil || journal != "delete" {
		t.Fatalf("read barrier requires rollback journal: %q %v", journal, err)
	}
	for ctx.Err() == nil {
		select {
		case <-child.done:
			t.Fatalf("command ended before an owned arm was observed: %v\n%s", child.err, child.stderr.String())
		default:
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var observed localPaperObservation
		observed.execution, err = loadExecutionAuthoritySnapshot(ctx, tx, k2aAccountRef)
		if err == nil {
			observed.runner, err = loadPaperRunnerLease(ctx, tx)
		}
		if err == nil {
			err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM order_events WHERE event_type='FILL_RECORDED'").Scan(&observed.fills)
		}
		state, row := observed.execution, observed.runner.record
		if err == nil && state.Armed && state.LeaseOwner != "" && state.FencingToken > baseline && row.OwnerID != "" && row.AccountRef == k2aAccountRef && row.StrategySelectionEventID == selection && row.SelectedResultSHA256 == result && observed.fills < 10 {
			// The retained SHARED snapshot blocks the next write COMMIT, not
			// the signal handler. A polling observation alone races completion.
			return tx, observed
		}
		_ = tx.Rollback()
		var busy sqlite3.Error
		if err != nil && !(errors.As(err, &busy) && (busy.Code == sqlite3.ErrBusy || busy.Code == sqlite3.ErrLocked)) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no durable paper execution owner observed")
	return nil, localPaperObservation{}
}

func localPaperSignalFixture(t *testing.T) (*Service, []string, string) {
	t.Helper()
	svc, dbPath, research, evidence, selected, rows := localPaperRestartFixture(t)
	first, err := time.Parse(time.RFC3339, strings.SplitN(rows[0], ",", 2)[0])
	if err != nil {
		t.Fatal(err)
	}
	// Genuine stored history makes the post-arm recovery work observable; no
	// sleep, test hook or altered lease/clock is added to the product binary.
	var history []string
	for i := 480; i > 0; i-- {
		history = append(history, localPaperRestartRow(first.AddDate(0, 0, -i).Format("2006-01-02"), "99", "99", "100"))
	}
	rows = append(history, rows...)
	raw := paperSnapshotCSV(rows...)
	proposal := localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], raw, "golden_cross")
	initial, err := svc.executeLocalPaper(context.Background(), k2aAccountRef, selected.CurrentEventID, proposal, raw, research)
	if err != nil || initial == nil || initial.Order == nil {
		t.Fatalf("signal fixture: %+v %v", initial, err)
	}
	for day := 0; day < 10; day++ {
		rows = append(rows, localPaperRestartRow(mustTime("2026-05-10T07:00:00Z").AddDate(0, 0, day).Format("2006-01-02"), "101", "101", "2"))
	}
	svc.now = func() time.Time { return mustTime("2026-05-20T07:00:00Z") }
	imported, err := svc.importPaperSnapshot(context.Background(), paperSnapshotCSV(rows...))
	if err != nil || imported.Added != 10 {
		t.Fatalf("pending fill history: %+v %v", imported, err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{"bars.csv": raw, "proposal.json": proposal, "research.csv": research} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"paper-execute", "-db", dbPath, "-account", k2aAccountRef, "-expected-current-event", selected.CurrentEventID,
		"-bars", filepath.Join(dir, "bars.csv"), "-proposal", filepath.Join(dir, "proposal.json"), "-research-bars", filepath.Join(dir, "research.csv"), "-arm-paper"}
	return svc, args, initial.Order.OrderID
}
