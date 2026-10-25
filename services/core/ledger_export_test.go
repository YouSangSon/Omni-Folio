package main

import (
	"context"
	"encoding/csv"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLedgerExportExecutable(t *testing.T) {
	svc, dbPath := testService(t, nil, nil)
	seedLedgerActivities(t, svc)
	path := filepath.Join(t.TempDir(), "원장.csv")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, buildLocalPaperExecutable(t), "ledger-export", "-db", dbPath, "-out", path).CombinedOutput()
	if err != nil {
		t.Fatalf("read-only ledger CSV export is unavailable: %v; %s", err, output)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	wantHeader := []string{"type", "occurred_at", "recorded_at", "symbol", "quantity", "price", "fee", "currency", "amount", "counter_currency", "counter_amount", "is_correction"}
	if err != nil || len(rows) != 5 || !reflect.DeepEqual(rows[0], wantHeader) {
		t.Fatalf("export lost its closed public activity shape: rows=%v err=%v", rows, err)
	}
	if rows[1][0] != "FEE" || rows[1][8] != "-1" || rows[2][0] != "FX_EXCHANGE" || rows[2][8] != "-100" || rows[2][10] != "137000" {
		t.Fatalf("exact signed cash legs changed: %v", rows)
	}
	for _, private := range []string{"account-main", "event-deposit", "source-deposit", "receipt-seed", "account_id", "event_id", "source_event_id", "instrument_id", "receipt_id"} {
		if strings.Contains(string(raw)+string(output), private) {
			t.Fatalf("export leaked %q", private)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("export is not private: info=%v err=%v", info, err)
	}
}
