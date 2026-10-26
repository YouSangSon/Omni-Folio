package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestOwnerPriceCLIExistingLiteralDatabaseAndReceiptRetry(t *testing.T) {
	svc, original := testService(t, nil, nil)
	applyTestCSV(t, svc, "cli-price-ledger", testCSV("buy,account-main,BUY,2025-01-02T00:00:00Z,AAPL,1,100,0,USD,-100"))
	if err := svc.db.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "owner ?mode=memory#proof.db")
	if err := os.Rename(original, dbPath); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, []byte(ownerPriceTestInput), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-db", dbPath, "-input", input}
	sentinel := errors.New("closed receipt output")
	if err := runOwnerPriceImportCommand(context.Background(), args, ledgerExportFailWriter{sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("committed output error hidden: %v", err)
	}
	var first, retry bytes.Buffer
	if err := runOwnerPriceImportCommand(context.Background(), args, &first); err != nil {
		t.Fatal(err)
	}
	if err := runOwnerPriceImportCommand(context.Background(), args, &retry); err != nil || !bytes.Equal(first.Bytes(), retry.Bytes()) {
		t.Fatal("retry changed receipt")
	}
	var receipt OwnerPriceSeriesReceipt
	if json.Unmarshal(first.Bytes(), &receipt) != nil || receipt.Authority != "owner_declared" || receipt.Sample || receipt.PointCount != 2 {
		t.Fatalf("bad owner receipt: %s", first.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("literal path created unexpected files: %v %v", entries, err)
	}
}

func TestOwnerPriceCLIRejectsUnsafeInputsBeforeOpeningDatabase(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, []byte(ownerPriceTestInput), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-db", missing, "-input", input}, {"-db", missing, "-input", input, "extra"}, {"-db", missing, "-input", input, "-source", "local_fixture"}, {"-db", missing}} {
		var out bytes.Buffer
		err := runOwnerPriceImportCommand(context.Background(), args, &out)
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), dir) {
			t.Fatalf("unsafe command result: %v", err)
		}
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing DB was created")
	}
	fifo := filepath.Join(dir, "input.fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "input.link")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fifo, link, dir} {
		var out bytes.Buffer
		if err := runOwnerPriceImportCommand(context.Background(), []string{"-db", missing, "-input", path}, &out); err == nil || out.Len() != 0 {
			t.Fatal("nonregular input accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runOwnerPriceImportCommand(ctx, []string{"-db", missing, "-input", input}, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
}
