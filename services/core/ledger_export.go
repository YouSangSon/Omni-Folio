package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

const maxLedgerExportRows = 10_000
const maxLedgerExportBytes = 32 << 20

var ledgerExportSymbolPattern = regexp.MustCompile(`^[\pL\pN][\pL\pN._/-]*$`)

type ledgerExportReceipt struct {
	SchemaVersion   string `json:"schema_version"`
	Source          string `json:"source"`
	BrokerFreshness string `json:"broker_freshness"`
	LedgerRevision  string `json:"ledger_revision"`
	RecordedAt      string `json:"recorded_at"`
	Rows            int    `json:"rows"`
	SHA256          string `json:"sha256"`
}

func runLedgerExportCommand(ctx context.Context, args []string, output io.Writer) error {
	if ctx == nil {
		return errors.New("ledger export context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fs := flag.NewFlagSet("ledger-export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "existing SQLite database")
	out := fs.String("out", "", "new private ledger activity CSV")
	if err := fs.Parse(args); err != nil {
		return errors.New("ledger-export options are invalid")
	}
	if fs.NArg() != 0 || *dbPath == "" || *out == "" {
		return errors.New("ledger-export requires -db and -out")
	}
	invalid := errors.New("ledger export failed; existing files were not overwritten; a completed export may remain after publication")
	db, err := openReadOnlyExistingDB(ctx, *dbPath)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return invalid
	}
	defer db.Close()
	page, err := newService(db, time.Now, randomID).ledgerActivities(ctx, maxLedgerExportRows, nil)
	if err != nil || page.NextCursor != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return invalid
	}
	digest, err := publishLedgerExport(ctx, *dbPath, *out, page.Events)
	if err != nil {
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), invalid)
		}
		return invalid
	}
	err = json.NewEncoder(output).Encode(ledgerExportReceipt{
		SchemaVersion: "ledger-activity-export.v1", Source: page.Source, BrokerFreshness: page.BrokerFreshness,
		LedgerRevision: page.LedgerRevision, RecordedAt: page.RecordedAt, Rows: len(page.Events), SHA256: digest,
	})
	if err != nil {
		return invalid
	}
	return nil
}

func ledgerExportTarget(dbPath, out string) (string, string, error) {
	out, err := filepath.Abs(out)
	if err != nil {
		return "", "", err
	}
	database, err := filepath.EvalSymlinks(dbPath)
	if err != nil {
		return "", "", err
	}
	database, err = filepath.Abs(database)
	if err != nil {
		return "", "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(out))
	if err != nil {
		return "", "", err
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return "", "", err
	}
	for path := parent; ; path = filepath.Dir(path) {
		info, err := os.Stat(path)
		if err != nil {
			return "", "", err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.IsDir() || !ok || (stat.Uid != uint32(os.Getuid()) && stat.Uid != 0) || (path == parent && stat.Uid != uint32(os.Getuid())) {
			return "", "", errors.New("ledger export requires a trusted owned directory path")
		}
		rootSticky := path != parent && stat.Uid == 0 && info.Mode()&os.ModeSticky != 0
		if info.Mode().Perm()&0022 != 0 && !rootSticky {
			return "", "", errors.New("ledger export directory path permits shared writes")
		}
		if path == filepath.Dir(path) {
			break
		}
	}
	target := filepath.Join(parent, filepath.Base(out))
	input, err := filepath.Abs(dbPath)
	if err != nil {
		return "", "", err
	}
	inputParent, err := filepath.EvalSymlinks(filepath.Dir(input))
	if err != nil {
		return "", "", err
	}
	for _, name := range []string{database, filepath.Join(inputParent, filepath.Base(input))} {
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			if target == name+suffix {
				return "", "", errors.New("ledger export cannot target database files")
			}
		}
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return "", "", errors.New("ledger export requires an unused output path")
	}
	return parent, target, nil
}

func publishLedgerExport(ctx context.Context, dbPath, out string, events []LedgerActivity) (digest string, resultErr error) {
	parent, target, err := ledgerExportTarget(dbPath, out)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(parent, ".omni-ledger-export-*")
	if err != nil {
		return "", err
	}
	temporary := f.Name()
	defer func() {
		if f != nil {
			resultErr = errors.Join(resultErr, f.Close())
		}
		if temporary != "" {
			resultErr = errors.Join(resultErr, os.Remove(temporary))
		}
	}()
	hash := sha256.New()
	limited := &ledgerExportLimitWriter{writer: io.MultiWriter(f, hash), remaining: maxLedgerExportBytes}
	if err := writeLedgerExportCSV(ctx, limited, events); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	err = f.Close()
	f = nil
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.Link(temporary, target); err != nil {
		return "", err
	}
	if err := os.Remove(temporary); err != nil {
		return "", err
	}
	temporary = ""
	dir, err := os.Open(parent)
	if err != nil {
		return "", err
	}
	if err := errors.Join(dir.Sync(), dir.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeLedgerExportCSV(ctx context.Context, output io.Writer, events []LedgerActivity) error {
	w := csv.NewWriter(output)
	if err := w.Write([]string{"type", "occurred_at", "recorded_at", "symbol", "quantity", "price", "fee", "currency", "amount", "counter_currency", "counter_amount", "is_correction"}); err != nil {
		return err
	}
	text := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return err
		}
		symbol := text(event.Symbol)
		if symbol != "" && !ledgerExportSymbolPattern.MatchString(symbol) {
			return errors.New("ledger export symbol is unsafe for CSV")
		}
		if err := w.Write([]string{event.Type, event.OccurredAt, event.RecordedAt, symbol, text(event.Quantity), text(event.Price), text(event.Fee), event.Currency, event.Amount, text(event.CounterCurrency), text(event.CounterAmount), strconv.FormatBool(event.IsCorrection)}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

type ledgerExportLimitWriter struct {
	writer    io.Writer
	remaining int
}

func (w *ledgerExportLimitWriter) Write(raw []byte) (int, error) {
	if len(raw) > w.remaining {
		return 0, errors.New("ledger CSV exceeds the export size limit")
	}
	n, err := w.writer.Write(raw)
	w.remaining -= n
	if n != len(raw) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}
