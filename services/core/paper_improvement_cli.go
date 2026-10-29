package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"
)

func runPaperImprovementCommand(ctx context.Context, args []string, out io.Writer) error {
	if ctx == nil {
		return errors.New("paper improvement command context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fs := flag.NewFlagSet("paper-improve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "existing SQLite database")
	account := fs.String("account", "", "paper account reference")
	artifactPath := fs.String("artifact", "", "bounded strategy-improvement-result JSON")
	barsPath := fs.String("bars", "", "bounded forward market bars CSV")
	attemptID := fs.String("attempt-id", "", "comparison attempt identity")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *dbPath == "" || *account == "" || *artifactPath == "" || *barsPath == "" || *attemptID == "" {
		return errors.New("paper improvement command arguments are invalid")
	}
	artifact, err := readStrategyArtifact(*artifactPath)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("paper improvement artifact must be a bounded readable regular file")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	bars, err := readBoundedRegularFile(*barsPath, maxMarketDataBytes)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("paper improvement bars must be a bounded readable regular file")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	failureMessage := "paper improvement database is unavailable"
	db, err := openReadWriteExistingDB(ctx, *dbPath)
	if err == nil {
		defer db.Close()
		var receipt *PaperImprovementReceipt
		receipt, err = newService(db, time.Now, randomID).improvePaperCandidate(ctx, *account, *attemptID, artifact, bars)
		if err == nil {
			return json.NewEncoder(out).Encode(receipt)
		}
		failureMessage = "paper improvement failed; registration or comparison may be saved; inspect strategy-status and inspect paper-promotion-status with the same attempt ID before retrying the same files and account"
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errors.New(failureMessage)
}
