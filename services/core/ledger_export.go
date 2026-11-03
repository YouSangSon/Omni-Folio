package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
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
	database, err := filepath.EvalSymlinks(dbPath)
	if err != nil {
		return "", "", err
	}
	database, err = filepath.Abs(database)
	if err != nil {
		return "", "", err
	}
	parent, target, err := privateFileTarget(out)
	if err != nil {
		return "", "", err
	}
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
	return parent, target, nil
}

func publishLedgerExport(ctx context.Context, dbPath, out string, events []LedgerActivity) (string, error) {
	parent, target, err := ledgerExportTarget(dbPath, out)
	if err != nil {
		return "", err
	}
	return publishPrivateFile(ctx, parent, target, ".omni-ledger-export-*", func(output io.Writer) error {
		limited := &ledgerExportLimitWriter{writer: output, remaining: maxLedgerExportBytes}
		return writeLedgerExportCSV(ctx, limited, events)
	})
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
