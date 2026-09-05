package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"
)

func runLocalPaperCommand(ctx context.Context, args []string, output io.Writer) error {
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	dbPath := fs.String("db", "omni-folio.db", "existing migrated SQLite database")
	var account, selection, result, barsPath, proposalPath, researchPath, bundlePath string
	var arm bool
	if args[0] != "paper-import-bars" {
		fs.StringVar(&account, "account", "", "isolated paper account alias")
		fs.StringVar(&selection, "expected-current-event", "", "exact current selection event")
	}
	if args[0] == "paper-init" {
		fs.StringVar(&result, "result-sha256", "", "registered initial-capital research result")
	} else {
		fs.StringVar(&barsPath, "bars", "", "explicit-time paper fixture CSV")
	}
	if args[0] == "paper-execute" {
		fs.StringVar(&bundlePath, "bundle", "", "paper input bundle JSON; exclusive with separate input files")
		fs.StringVar(&proposalPath, "proposal", "", "offline paper proposal JSON")
		fs.StringVar(&researchPath, "research-bars", "", "original research CSV bound to the registered result")
		fs.BoolVar(&arm, "arm-paper", false, "explicitly arm this one-shot paper run; halt on exit")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || (args[0] != "paper-import-bars" && (account == "" || selection == "")) ||
		(args[0] == "paper-init" && result == "") || (args[0] == "paper-import-bars" && barsPath == "") ||
		(args[0] == "paper-execute" && (!arm ||
			(bundlePath == "" && (barsPath == "" || proposalPath == "" || researchPath == "")) ||
			(bundlePath != "" && (barsPath != "" || proposalPath != "" || researchPath != "")))) {
		return errors.New("local paper command requires all explicit inputs; paper-execute also requires -arm-paper")
	}
	var bars, proposal, research []byte
	var err error
	if bundlePath != "" {
		raw, readErr := readBoundedRegularFile(bundlePath, maxPaperBundleBytes)
		if readErr != nil {
			return errors.New("local paper bundle is unreadable or exceeds 4 MiB")
		}
		proposal, bars, research, err = decodePaperInputBundle(raw)
		if err != nil {
			return err
		}
	}
	if barsPath != "" {
		bars, err = readStrategyArtifact(barsPath)
		if err != nil {
			return errors.New("local paper CSV is unreadable or exceeds 1 MiB")
		}
	}
	if proposalPath != "" {
		proposal, err = readStrategyArtifact(proposalPath)
		if err != nil {
			return errors.New("local paper proposal is unreadable or exceeds 1 MiB")
		}
	}
	if researchPath != "" {
		research, err = readStrategyArtifact(researchPath)
		if err != nil {
			return errors.New("local paper research CSV is unreadable or exceeds 1 MiB")
		}
	}
	db, err := openExistingDB(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := requireSchema(db); err != nil {
		return err
	}
	svc := newService(db, time.Now, randomID)
	var value any
	switch args[0] {
	case "paper-init":
		value, err = svc.openPaperAccountingSessionChecked(ctx, account, result, selection, true)
	case "paper-import-bars":
		value, err = svc.importPaperSnapshot(ctx, bars)
	case "paper-execute":
		value, err = svc.executeLocalPaper(ctx, account, selection, proposal, bars, research)
	default:
		return errors.New("unsupported local paper command")
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(value)
}
