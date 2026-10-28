package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"
)

func runPaperPromotionCommand(ctx context.Context, command string, args []string, out io.Writer) error {
	if ctx == nil {
		return errors.New("paper promotion command context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if command != "paper-promote" && command != "paper-promotion-status" && command != "paper-promotion-abort" {
		return errors.New("paper promotion command is invalid")
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "existing SQLite database")
	attemptID := fs.String("attempt-id", "", "promotion attempt identity")
	var account, candidate, selection, barsPath string
	if command == "paper-promote" {
		fs.StringVar(&account, "account", "", "paper account reference")
		fs.StringVar(&candidate, "candidate-result-sha", "", "registered candidate result SHA-256")
		fs.StringVar(&selection, "expected-selection-event", "", "current selection event")
		fs.StringVar(&barsPath, "bars", "", "bounded forward market bars CSV")
	}
	if fs.Parse(args) != nil || fs.NArg() != 0 || *dbPath == "" || *attemptID == "" ||
		(command == "paper-promote" && (account == "" || candidate == "" || selection == "" || barsPath == "")) {
		return errors.New("paper promotion command arguments are invalid")
	}
	var raw []byte
	if command == "paper-promote" {
		var err error
		raw, err = readBoundedRegularFile(barsPath, maxMarketDataBytes)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("paper promotion bars must be a bounded readable regular file")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	var db *sql.DB
	var err error
	if command == "paper-promotion-status" {
		db, err = openReadOnlyExistingDB(ctx, *dbPath)
	} else {
		db, err = openReadWriteExistingDB(ctx, *dbPath)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return errors.New("paper promotion database is unavailable")
	}
	defer db.Close()
	svc := newService(db, time.Now, randomID)
	var receipt *PaperPromotionReceipt
	switch command {
	case "paper-promote":
		receipt, err = svc.promotePaperCandidate(ctx, account, candidate, selection, *attemptID, raw)
	case "paper-promotion-status":
		receipt, err = svc.paperPromotionStatus(ctx, *attemptID)
	case "paper-promotion-abort":
		receipt, err = svc.abortPaperPromotion(ctx, *attemptID)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return errors.New("paper promotion operation failed; check status using the same attempt ID")
	}
	return json.NewEncoder(out).Encode(receipt)
}
