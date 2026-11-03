package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"
)

func runOwnerFXImportCommand(ctx context.Context, args []string, out io.Writer) error {
	if ctx == nil {
		return errors.New("owner FX command context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fs := flag.NewFlagSet("owner-fx-import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "existing SQLite database")
	inputPath := fs.String("input", "", "bounded complete owner FX series JSON")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *dbPath == "" || *inputPath == "" {
		return errors.New("owner-fx-import requires only -db and -input")
	}
	raw, err := readBoundedRegularFile(*inputPath, maxBodyBytes)
	if err != nil {
		return errors.New("owner FX input must be a bounded readable regular file")
	}
	if _, _, _, err := decodeOwnerFXSeries(ctx, raw); err != nil {
		return err
	}
	db, err := openReadWriteExistingDB(ctx, *dbPath)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("owner FX database is unavailable")
	}
	defer db.Close()
	receipt, err := newService(db, time.Now, randomID).importOwnerFXSeries(ctx, raw)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("owner FX import could not verify and store the complete version")
	}
	return json.NewEncoder(out).Encode(receipt)
}
