package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	bin := buildLocalPaperExecutable(t)
	output, err := exec.CommandContext(ctx, bin, "ledger-export", "-db", dbPath, "-out", path).CombinedOutput()
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
	if rows[1][0] != "FEE" || rows[1][8] != "-1000" || rows[2][0] != "FX_EXCHANGE" || rows[2][8] != "-100" || rows[2][10] != "137000" {
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
	var receipt ledgerExportReceipt
	if err := json.Unmarshal(output, &receipt); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if receipt.SchemaVersion != "ledger-activity-export.v1" || receipt.Rows != 4 || receipt.LedgerRevision != revision(4) || receipt.Source != "local_ledger" || receipt.BrokerFreshness != "unverified" || receipt.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("receipt is not bound to the exact CSV: %+v", receipt)
	}
	output, err = exec.CommandContext(ctx, bin, "ledger-export", "-unknown-private-sentinel", "-out", path).CombinedOutput()
	if err == nil || strings.Contains(string(output), "private-sentinel") {
		t.Fatalf("invalid option leaked or succeeded: err=%v output=%s", err, output)
	}
}

func TestLedgerExportAllEventsAndExactOrdering(t *testing.T) {
	svc, dbPath := testService(t, nil, nil)
	applyTestCSV(t, svc, "export-types", testCSV(
		"d,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,1000",
		"b,account-main,BUY,2026-01-02T00:00:00Z,AAPL,2,10,0.00000001,USD,-20.00000001",
		"sp,account-main,SPLIT,2026-01-03T00:00:00Z,AAPL,2,,,USD,0",
		"s,account-main,SELL,2026-01-04T00:00:00Z,AAPL,1,15,0,USD,15",
		"di,account-main,DIVIDEND,2026-01-05T00:00:00Z,AAPL,,,,USD,1.00000001",
		"w,account-main,WITHDRAWAL,2026-01-06T00:00:00Z,,,,,USD,-2",
		"f,account-main,FEE,2026-01-07T00:00:00Z,,,,,USD,-1",
		"t,account-main,TAX,2026-01-07T00:00:00.000000001Z,,,,,USD,-0.01",
	))
	applyTestCSV(t, svc, "export-correction", cashVoidCSV("v,account-main,CASH_VOID,2026-01-08T00:00:00Z,,,,,USD,1,f"))
	applyTestCSV(t, svc, "export-fx", "source_event_id,account_id,type,occurred_at,symbol,quantity,price,fee,currency,amount,counter_currency,counter_amount\nfx,account-main,FX_EXCHANGE,2026-01-09T00:00:00Z,,,,,USD,-100,KRW,137000\n")
	path := filepath.Join(t.TempDir(), "all.csv")
	var output bytes.Buffer
	if err := runLedgerExportCommand(context.Background(), []string{"-db", dbPath, "-out", path}, &output); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil || len(rows) != 11 {
		t.Fatalf("lost supported events: rows=%v err=%v", rows, err)
	}
	for i, kind := range []string{"FX_EXCHANGE", "CASH_VOID", "TAX", "FEE", "WITHDRAWAL", "DIVIDEND", "SELL", "SPLIT", "BUY", "DEPOSIT"} {
		if rows[i+1][0] != kind {
			t.Fatalf("row %d: %v", i, rows[i+1])
		}
	}
	if rows[2][11] != "true" || rows[4][11] != "false" || rows[9][6] != "0.00000001" || rows[9][8] != "-20.00000001" || rows[3][1] != "2026-01-07T00:00:00.000000001Z" {
		t.Fatal("correction, decimal or nanosecond precision was changed")
	}
	if strings.Contains(string(raw), "account-main") || strings.Contains(string(raw), "source_event_id") {
		t.Fatal("private metadata leaked")
	}
}

func TestLedgerExportBoundsAndEmpty(t *testing.T) {
	for _, count := range []int{0, maxLedgerExportRows, maxLedgerExportRows + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			svc, dbPath := testService(t, nil, nil)
			if count != 0 {
				_, err := svc.db.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
					INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,currency,amount,receipt_id,recorded_at)
					SELECT printf('export-event-%d',i),printf('export-source-%d',i),'private-account','DEPOSIT','2026-01-01T00:00:00Z','KRW','1','private-receipt','2026-01-10T15:01:00Z' FROM n`, count)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=?,recorded_at='2026-01-10T15:01:00Z' WHERE singleton=1`, count); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(t.TempDir(), "bounded.csv")
			var output bytes.Buffer
			err := runLedgerExportCommand(context.Background(), []string{"-db", dbPath, "-out", path}, &output)
			if count > maxLedgerExportRows {
				if err == nil || output.Len() != 0 {
					t.Fatal("oversized export succeeded or emitted a receipt")
				}
				assertLedgerExportAbsent(t, path)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var receipt ledgerExportReceipt
			if err := json.Unmarshal(output.Bytes(), &receipt); err != nil || receipt.Rows != count || receipt.LedgerRevision != revision(int64(count)) {
				t.Fatalf("incomplete export: receipt=%+v err=%v", receipt, err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
			if err != nil || len(rows) != count+1 {
				t.Fatalf("export truncated: rows=%d err=%v", len(rows), err)
			}
		})
	}
}

func TestLedgerExportRejectsUnsafeLabelsWithoutChangingLedger(t *testing.T) {
	for _, symbol := range []string{"=HYPERLINK(1)", "+1", "-1", "@sum(1)", "＝1", "\t=1", " AAPL", "AAPL\r\n=1", `AAPL",=1`, "AAPL;=1"} {
		t.Run(fmt.Sprintf("%q", symbol), func(t *testing.T) {
			svc, dbPath := testService(t, nil, nil)
			if _, err := svc.db.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,instrument_id,symbol,quantity,price,fee,currency,amount,receipt_id,recorded_at) VALUES('private-event','private-source','private-account','BUY','2026-01-01T00:00:00Z',?,?, '1','1','0','USD','-1','private-receipt','2026-01-10T15:01:00Z')`, instrumentIDForSymbol(symbol), symbol); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=1,recorded_at='2026-01-10T15:01:00Z'`); err != nil {
				t.Fatal(err)
			}
			// Prove this valid stored label reaches the new export boundary.
			if _, err := svc.ledgerActivities(context.Background(), 1, nil); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "unsafe.csv")
			var output bytes.Buffer
			err := runLedgerExportCommand(context.Background(), []string{"-db", dbPath, "-out", path}, &output)
			if err == nil || output.Len() != 0 || strings.Contains(err.Error(), symbol) {
				t.Fatalf("unsafe label succeeded or leaked: err=%v output=%s", err, output.Bytes())
			}
			assertLedgerExportAbsent(t, path)
			var stored string
			if err := svc.db.QueryRow(`SELECT symbol FROM events`).Scan(&stored); err != nil || stored != symbol {
				t.Fatal("export changed the stored label")
			}
		})
	}
}

func TestLedgerExportPreservesExistingFilesAndDatabaseAuxiliaries(t *testing.T) {
	_, dbPath := testService(t, nil, nil)
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.csv")
	if err := os.WriteFile(existing, []byte("private-original"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.csv")
	if err := os.Symlink(existing, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", dbPath + "-journal", existing, link} {
		var output bytes.Buffer
		if err := runLedgerExportCommand(context.Background(), []string{"-db", dbPath, "-out", path}, &output); err == nil || output.Len() != 0 {
			t.Fatalf("protected output accepted: %s", path)
		}
	}
	raw, err := os.ReadFile(existing)
	if err != nil || string(raw) != "private-original" {
		t.Fatal("existing output changed")
	}
	if _, err := os.Readlink(link); err != nil {
		t.Fatal("output symlink changed")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(dbPath + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("export created a database auxiliary: %s err=%v", suffix, err)
		}
	}
	alias := filepath.Join(dir, "database-link.db")
	if err := os.Symlink(dbPath, alias); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		path := alias + suffix
		if err := runLedgerExportCommand(context.Background(), []string{"-db", alias, "-out", path}, io.Discard); err == nil {
			t.Fatal("source alias auxiliary was accepted")
		}
		assertLedgerExportAbsent(t, path)
	}
}

func TestLedgerExportErrorsCancellationAndReadOnlyConnection(t *testing.T) {
	svc, dbPath := testService(t, nil, nil)
	dir := t.TempDir()
	special := filepath.Join(dir, "ledger #?mode=rw.db")
	if err := copyFile(dbPath, special); err != nil {
		t.Fatal(err)
	}
	db, err := openReadOnlyExistingDB(context.Background(), special)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE forbidden_write(id)`); err == nil {
		t.Fatal("export database connection permits writes")
	}
	db.Close()
	if err := runLedgerExportCommand(context.Background(), []string{"-db", special, "-out", filepath.Join(dir, "special.csv")}, io.Discard); err != nil {
		t.Fatalf("escaped database path was not exportable: %v", err)
	}
	for _, kind := range []string{"cancelled", "missing-db", "directory", "output-directory", "shared-directory", "invalid-options", "missing-schema"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "output.csv")
			args := []string{"-db", special, "-out", path}
			switch kind {
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "missing-db":
				args[1] = filepath.Join(dir, "missing.db")
			case "directory":
				args[1] = dir
			case "output-directory":
				args[3] = filepath.Dir(path) + string(os.PathSeparator)
			case "shared-directory":
				parent := filepath.Dir(path)
				if err := os.Chmod(parent, 0777); err != nil {
					t.Fatal(err)
				}
			case "invalid-options":
				args = append(args, "-secret-option-sentinel")
			case "missing-schema":
				if _, err := svc.db.Exec(`DELETE FROM schema_migrations WHERE version=22`); err != nil {
					t.Fatal(err)
				}
				args[1] = dbPath
			}
			var output bytes.Buffer
			err := runLedgerExportCommand(ctx, args, &output)
			if err == nil || output.Len() != 0 || strings.Contains(err.Error(), "secret-option-sentinel") {
				t.Fatalf("error was hidden or leaked: err=%v output=%s", err, output.Bytes())
			}
			if kind == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity was lost")
			}
			assertLedgerExportAbsent(t, path)
		})
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing source database was created")
	}
}

func TestLedgerExportRejectsUnactivatedAndTamperedReplay(t *testing.T) {
	for _, activated := range []bool{false, true} {
		t.Run(fmt.Sprint(activated), func(t *testing.T) {
			svc := legacyChronologyService(t)
			if err := migrate(svc.db); err != nil {
				t.Fatal(err)
			}
			if activated {
				preview, err := previewLedgerReplay(context.Background(), svc.db)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := activateLedgerReplay(context.Background(), svc.db, preview.LedgerRevision, preview.EventsSHA256, preview.SnapshotSHA256, svc.now()); err != nil {
					t.Fatal(err)
				}
				var originalGuard string
				if err := svc.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='ledger_replay_transition_no_update'`).Scan(&originalGuard); err != nil {
					t.Fatal(err)
				}
				if _, err := svc.db.Exec(`DROP TRIGGER ledger_replay_transition_no_update; UPDATE ledger_replay_transition SET snapshot_sha256='0000000000000000000000000000000000000000000000000000000000000000'; ` + originalGuard); err != nil {
					t.Fatal(err)
				}
				if _, err := svc.ledgerActivities(context.Background(), 1, nil); err == nil || !strings.Contains(err.Error(), "transition proof mismatch") {
					t.Fatalf("transition corruption did not reach its proof: %v", err)
				}
			}
			var sequence int
			var name, dbPath string
			if err := svc.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &dbPath); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "invalid.csv")
			var output bytes.Buffer
			if err := runLedgerExportCommand(context.Background(), []string{"-db", dbPath, "-out", path}, &output); err == nil || output.Len() != 0 || strings.Contains(err.Error(), "transition proof mismatch") {
				t.Fatalf("unverified replay was exported or leaked: err=%v output=%s", err, output.Bytes())
			}
			assertLedgerExportAbsent(t, path)
		})
	}
}

func TestLedgerExportConcurrentPublicationNeverOverwrites(t *testing.T) {
	_, dbPath := testService(t, nil, nil)
	path := filepath.Join(t.TempDir(), "concurrent.csv")
	finished := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := publishLedgerExport(context.Background(), dbPath, path, nil)
			finished <- err
		}()
	}
	successes := 0
	for i := 0; i < 2; i++ {
		if <-finished == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("publication winners=%d", successes)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(raw), "type,occurred_at,") || bytes.Count(raw, []byte("\n")) != 1 {
		t.Fatalf("winner published an incomplete file: err=%v", err)
	}
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".omni-ledger-export-*"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("publication left owned temporary files: %v err=%v", temporary, err)
	}
}

func TestLedgerExportCSVWriterFailureAndByteLimit(t *testing.T) {
	sentinel := errors.New("private-writer-error")
	if err := writeLedgerExportCSV(context.Background(), ledgerExportFailWriter{sentinel}, nil); !errors.Is(err, sentinel) {
		t.Fatalf("CSV flush hid a write error: %v", err)
	}
	var out bytes.Buffer
	limited := &ledgerExportLimitWriter{writer: &out, remaining: 3}
	if n, err := limited.Write([]byte("1234")); n != 0 || err == nil || out.Len() != 0 {
		t.Fatal("byte limit wrote a partial chunk")
	}
	if n, err := limited.Write([]byte("123")); n != 3 || err != nil || out.String() != "123" {
		t.Fatal("exact byte bound was rejected")
	}
	limited = &ledgerExportLimitWriter{writer: ledgerExportShortWriter{}, remaining: 3}
	if _, err := limited.Write([]byte("123")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("short writes were hidden")
	}
}

func TestLedgerExportRejectsWritableAncestorAndCleansPartialFiles(t *testing.T) {
	_, dbPath := testService(t, nil, nil)
	unsafe := t.TempDir()
	leaf := filepath.Join(unsafe, "owned-leaf")
	if err := os.Mkdir(leaf, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafe, 0777); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(leaf, "blocked.csv")
	if _, err := publishLedgerExport(context.Background(), dbPath, path, nil); err == nil {
		t.Fatal("safe leaf under a shared writable ancestor was accepted")
	}
	assertLedgerExportAbsent(t, path)

	path = filepath.Join(t.TempDir(), "too-large.csv")
	symbol := strings.Repeat("A", 1<<20)
	events := make([]LedgerActivity, maxLedgerExportBytes/len(symbol)+1)
	for i := range events {
		events[i] = LedgerActivity{Type: "DIVIDEND", OccurredAt: "2026-01-01T00:00:00Z", RecordedAt: "2026-01-10T15:01:00Z", Symbol: &symbol, Currency: "USD", Amount: "1"}
	}
	if _, err := publishLedgerExport(context.Background(), dbPath, path, events); err == nil || !strings.Contains(err.Error(), "CSV exceeds the export size limit") {
		t.Fatalf("partial file did not reach the encoded-byte cap: %v", err)
	}
	assertLedgerExportAbsent(t, path)

	path = filepath.Join(t.TempDir(), "cancelled.csv")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := publishLedgerExport(ctx, dbPath, path, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("publication lost cancellation: %v", err)
	}
	assertLedgerExportAbsent(t, path)

	path = filepath.Join(t.TempDir(), "stdout-failed.csv")
	if err := runLedgerExportCommand(context.Background(), []string{"-db", dbPath, "-out", path}, ledgerExportFailWriter{errors.New("private-stdout-sentinel")}); err == nil || strings.Contains(err.Error(), "private-stdout-sentinel") {
		t.Fatalf("post-publication output failure was hidden or leaked: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(raw), "type,occurred_at,") || bytes.Count(raw, []byte("\n")) != 1 {
		t.Fatal("completed export was deleted after stdout failure")
	}
}

type ledgerExportFailWriter struct{ err error }

func (w ledgerExportFailWriter) Write([]byte) (int, error) { return 0, w.err }

type ledgerExportShortWriter struct{}

func (ledgerExportShortWriter) Write([]byte) (int, error) { return 0, nil }

func assertLedgerExportAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed export published output: %s err=%v", path, err)
	}
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".omni-ledger-export-*"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("owned export temporary files remain: %v err=%v", temporary, err)
	}
}
