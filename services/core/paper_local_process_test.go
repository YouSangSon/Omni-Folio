package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Exercise the shipped main, not a test helper's substitute signal handler.
func TestLocalPaperExecutableRejectsFIFO(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "omni-core")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOFLAGS=")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	// Prove this newly linked binary can start before testing file rejection;
	// first-launch loader/security checks are not FIFO writer waits.
	startup, stopStartup := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopStartup()
	started := time.Now()
	output, err := exec.CommandContext(startup, bin).CombinedOutput()
	var startupExit *exec.ExitError
	if startup.Err() != nil || !errors.As(err, &startupExit) || startupExit.ExitCode() != 1 || !bytes.Contains(output, []byte("usage:")) {
		t.Fatalf("executable startup failed: %v\n%s", err, output)
	}
	t.Logf("cold executable startup: %s", time.Since(started))
	fifo := filepath.Join(dir, "bars.csv")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(dir, "regular.csv")
	if err := os.WriteFile(regular, []byte("regular input"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked.csv")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
	}{
		{"import", []string{"paper-import-bars", "-bars", fifo}},
		{"symlink", []string{"paper-import-bars", "-bars", link}},
		{"execute_bars", []string{"paper-execute", "-bars", fifo, "-proposal", regular, "-research-bars", regular}},
		{"execute_proposal", []string{"paper-execute", "-bars", regular, "-proposal", fifo, "-research-bars", regular}},
		{"execute_research", []string{"paper-execute", "-bars", regular, "-proposal", regular, "-research-bars", fifo}},
		{"register", []string{"strategy-register", "-artifact", fifo}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// This is a liveness bound including OS scheduling, not a latency SLA.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			db := filepath.Join(dir, "absent.db")
			args := append(test.args, "-db", db)
			if args[0] == "paper-execute" {
				args = append(args, "-account", k2aAccountRef, "-expected-current-event", "selection_test", "-arm-paper")
			}
			cmd := exec.CommandContext(ctx, bin, args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatal("non-regular input blocked before validation; process required forced termination")
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || stdout.Len() != 0 ||
				!(strings.Contains(stderr.String(), "unreadable") || strings.Contains(stderr.String(), "regular file")) {
				t.Fatalf("FIFO must fail at the file boundary without a result: err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(db); !os.IsNotExist(err) {
				t.Fatal("invalid input created a database")
			}
		})
	}
}

func TestStrategyArtifactRegularInputBounds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")
	for _, size := range []int{0, maxBodyBytes, maxBodyBytes + 1} {
		want := bytes.Repeat([]byte{'x'}, size)
		if err := os.WriteFile(path, want, 0600); err != nil {
			t.Fatal(err)
		}
		got, err := readStrategyArtifact(path)
		if size > maxBodyBytes {
			if err == nil || got != nil {
				t.Fatal("oversized input accepted")
			}
		} else if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("regular file size %d was changed: %v", size, err)
		}
	}
	if _, err := readStrategyArtifact(dir); err == nil {
		t.Fatal("directory accepted")
	}
}
