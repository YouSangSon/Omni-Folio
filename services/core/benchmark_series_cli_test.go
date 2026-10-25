package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestBenchmarkSeriesCLIExistingLiteralDatabaseAndReceiptRetry(t *testing.T) {
	svc, original := testService(t, nil, nil)
	if err := svc.db.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "index ?mode=memory#proof.db")
	if err := os.Rename(original, path); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, []byte(benchmarkSeriesTestInput), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-db", path, "-input", input}
	sentinel := errors.New("closed output")
	if err := runBenchmarkSeriesCommand(context.Background(), "benchmark-import", args, ledgerExportFailWriter{sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("receipt error hidden: %v", err)
	}
	var retry bytes.Buffer
	if err := runBenchmarkSeriesCommand(context.Background(), "benchmark-import", args, &retry); err != nil {
		t.Fatal(err)
	}
	var receipt BenchmarkSeriesReceipt
	if err := json.Unmarshal(retry.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.PointCount != 2 || receipt.Authority != "owner_declared" {
		t.Fatalf("bad receipt: %+v", receipt)
	}
	var status bytes.Buffer
	if err := runBenchmarkSeriesCommand(context.Background(), "benchmark-status", []string{"-db", path, "-benchmark-id", "test_reference", "-source-version", "test-v1"}, &status); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retry.Bytes(), status.Bytes()) {
		t.Fatal("status changed the committed receipt")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("URI punctuation created another database: %v", entries)
	}
}

func TestBenchmarkSeriesCLIRejectsUnsafeInputBeforeCreatingDatabase(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")
	input := filepath.Join(dir, "source.json")
	if err := os.WriteFile(input, []byte(benchmarkSeriesTestInput), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		command string
		args    []string
	}{
		{"benchmark-import", []string{"-db", missing, "-input", input}},
		{"benchmark-status", []string{"-db", missing, "-benchmark-id", "test_reference"}},
		{"benchmark-import", []string{"-db", missing, "-input", input, "-benchmark-id", "unexpected"}},
		{"benchmark-import", []string{"-db", missing, "-input", input, "extra"}},
		{"benchmark-status", []string{"-db", missing, "-benchmark-id", "bad id"}},
		{"benchmark-status", []string{"-db", missing, "-benchmark-id", "test_reference", "-input", input}},
	} {
		var out bytes.Buffer
		err := runBenchmarkSeriesCommand(context.Background(), test.command, test.args, &out)
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), dir) {
			t.Fatalf("unsafe/sensitive command result: %v", err)
		}
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing database was created: %v", err)
	}
	fifo := filepath.Join(dir, "fifo.json")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{fifo, link, dir} {
		if err := runBenchmarkSeriesCommand(context.Background(), "benchmark-import", []string{"-db", missing, "-input", source}, io.Discard); err == nil {
			t.Fatal("nonregular input accepted")
		}
	}
	svc, path := testService(t, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runBenchmarkSeriesCommand(ctx, "benchmark-import", []string{"-db", path, "-input", input}, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled import: %v", err)
	}
	proof, err := proveBenchmarkSeriesRecovery(context.Background(), svc.db)
	if err != nil || proof.Versions != 0 {
		t.Fatalf("cancellation wrote data: %+v %v", proof, err)
	}
}
