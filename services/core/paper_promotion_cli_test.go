package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPaperPromotionCLIRequiresExplicitCommandArguments(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "missing ?mode=memory#private.db")
	barsPath := filepath.Join(dir, "private-bars.csv")
	if err := os.WriteFile(barsPath, []byte("bars"), 0600); err != nil {
		t.Fatal(err)
	}
	valid := []string{"-db", dbPath, "-account", "account-paper", "-candidate-result-sha", strings.Repeat("a", 64),
		"-expected-selection-event", "selected", "-attempt-id", "attempt", "-bars", barsPath}
	for i := 0; i < len(valid); i += 2 {
		args := append([]string(nil), valid[:i]...)
		args = append(args, valid[i+2:]...)
		if err := runPaperPromotionCommand(context.Background(), "paper-promote", args, io.Discard); err == nil || err.Error() != "paper promotion command arguments are invalid" {
			t.Fatalf("missing %s: %v", valid[i], err)
		}
	}
	for _, test := range []struct {
		command string
		args    []string
	}{
		{"paper-promote", append(append([]string(nil), valid...), "extra")},
		{"paper-promote", append(append([]string(nil), valid...), "-unknown", "PRIVATE_VALUE")},
		{"paper-promotion-status", []string{"-db", dbPath}},
		{"paper-promotion-status", []string{"-db", dbPath, "-attempt-id", "attempt", "extra"}},
		{"paper-promotion-status", []string{"-db", dbPath, "-attempt-id", "attempt", "-bars", barsPath}},
		{"paper-promotion-abort", []string{"-attempt-id", "attempt"}},
		{"paper-promotion-abort", []string{"-db", dbPath, "-attempt-id", "attempt", "-account", "account-paper"}},
	} {
		var out bytes.Buffer
		err := runPaperPromotionCommand(context.Background(), test.command, test.args, &out)
		if err == nil || err.Error() != "paper promotion command arguments are invalid" || out.Len() != 0 {
			t.Fatalf("%s accepted unexpected arguments: %v", test.command, err)
		}
	}
	for _, test := range []struct {
		command string
		args    []string
	}{
		{"paper-promote", valid},
		{"paper-promotion-status", []string{"-db", dbPath, "-attempt-id", "attempt"}},
		{"paper-promotion-abort", []string{"-db", dbPath, "-attempt-id", "attempt"}},
	} {
		var out bytes.Buffer
		err := runPaperPromotionCommand(context.Background(), test.command, test.args, &out)
		if err == nil || err.Error() != "paper promotion database is unavailable" || out.Len() != 0 || strings.Contains(err.Error(), dir) {
			t.Fatalf("%s missing database: %v", test.command, err)
		}
	}
	if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing database was created: %v", err)
	}
}

func TestPaperPromotionCLIRejectsUnboundedOrSpecialBars(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "missing.db")
	fifo := filepath.Join(dir, "private.fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "private.link")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	oversized := filepath.Join(dir, "private-oversized.csv")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("x"), maxMarketDataBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fifo, link, dir, oversized, filepath.Join(dir, "absent.csv")} {
		args := []string{"-db", dbPath, "-account", "account-paper", "-candidate-result-sha", strings.Repeat("a", 64),
			"-expected-selection-event", "selected", "-attempt-id", "attempt", "-bars", path}
		var out bytes.Buffer
		err := runPaperPromotionCommand(context.Background(), "paper-promote", args, &out)
		if err == nil || err.Error() != "paper promotion bars must be a bounded readable regular file" || out.Len() != 0 || strings.Contains(err.Error(), dir) {
			t.Fatalf("unsafe bars input: %v", err)
		}
	}
	if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe input created database: %v", err)
	}
	boundary := filepath.Join(dir, "boundary.csv")
	if err := os.WriteFile(boundary, bytes.Repeat([]byte("x"), maxMarketDataBytes), 0600); err != nil {
		t.Fatal(err)
	}
	regularLink := filepath.Join(dir, "regular.link")
	if err := os.Symlink(boundary, regularLink); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{boundary, regularLink} {
		args := []string{"-db", dbPath, "-account", "account-paper", "-candidate-result-sha", strings.Repeat("a", 64),
			"-expected-selection-event", "selected", "-attempt-id", "attempt", "-bars", path}
		if err := runPaperPromotionCommand(context.Background(), "paper-promote", args, io.Discard); err == nil || err.Error() != "paper promotion database is unavailable" {
			t.Fatalf("bounded regular input rejected: %v", err)
		}
	}
}

func TestPaperPromotionCLIPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, command := range []string{"paper-promote", "paper-promotion-status", "paper-promotion-abort"} {
		if err := runPaperPromotionCommand(ctx, command, nil, io.Discard); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s lost cancellation: %v", command, err)
		}
	}
}

func TestPaperPromotionCLIRedactsInputAndDatabaseErrors(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "PRIVATE_PATH.db")
	barsPath := filepath.Join(dir, "PRIVATE_PATH.csv")
	if err := os.WriteFile(dbPath, []byte("PRIVATE_SQLITE_SENTINEL"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(barsPath, []byte("PRIVATE_CSV_SENTINEL"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		command string
		args    []string
	}{
		{"paper-promote", []string{"-db", dbPath, "-account", "account-paper", "-candidate-result-sha", strings.Repeat("a", 64),
			"-expected-selection-event", "selected", "-attempt-id", "attempt", "-bars", barsPath}},
		{"paper-promotion-status", []string{"-db", dbPath, "-attempt-id", "attempt"}},
		{"paper-promotion-abort", []string{"-db", dbPath, "-attempt-id", "attempt"}},
	} {
		var out bytes.Buffer
		err := runPaperPromotionCommand(context.Background(), test.command, test.args, &out)
		if err == nil || strings.Contains(err.Error(), "PRIVATE_") || strings.Contains(err.Error(), dir) || out.Len() != 0 {
			t.Fatalf("%s exposed input or database error: %v", test.command, err)
		}
	}
}
