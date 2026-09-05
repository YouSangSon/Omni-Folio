package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "paper-run-loop" || args[0] == "paper-execute" || args[0] == "paper-import-bars" || args[0] == "paper-init") {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := runContext(ctx, args); err != nil && !(args[0] == "paper-run-loop" && errors.Is(err, context.Canceled)) {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	if err := run(args); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	return runContext(context.Background(), args)
}

func runContext(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: omni-core <migrate|serve|backup|verify-restore|strategy-register|strategy-status|strategy-select|strategy-rollback|paper-run-due|paper-run-loop|paper-init|paper-import-bars|paper-execute>")
	}
	switch args[0] {
	case "paper-init", "paper-import-bars", "paper-execute":
		return runLocalPaperCommand(ctx, args, os.Stdout)
	case "migrate":
		fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
		dbPath := fs.String("db", "omni-folio.db", "SQLite database path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		db, err := openDB(*dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		return migrate(db)
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ContinueOnError)
		dbPath := fs.String("db", "omni-folio.db", "SQLite database path")
		addr := fs.String("addr", "127.0.0.1:8080", "listen address")
		allowOrigin := fs.String("allow-origin", "", "exact browser origin allowed for local development")
		marketFixture := fs.String("market-fixture", "", "optional local market-data CSV fixture path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if err := validateAllowedOrigin(*allowOrigin); err != nil {
			return err
		}
		db, err := openExistingDB(*dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := requireServerStartupRecovery(db); err != nil {
			return err
		}
		svc := newService(db, time.Now, randomID)
		if *marketFixture != "" {
			marketData, err := loadMarketDataFixture(*marketFixture)
			if err != nil {
				return err
			}
			svc.marketData = marketData
		}
		srv := &http.Server{
			Addr:              *addr,
			Handler:           withCORS(svc.routes(), *allowOrigin),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		log.Printf("omni-core listening on %s", *addr)
		return srv.ListenAndServe()
	case "backup":
		fs := flag.NewFlagSet("backup", flag.ContinueOnError)
		dbPath := fs.String("db", "omni-folio.db", "source SQLite database path")
		out := fs.String("out", "", "backup candidate path")
		golden := fs.String("golden", "", "expected portfolio snapshot JSON")
		manifest := fs.String("manifest", "", "verified backup manifest output path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *out == "" || *golden == "" || *manifest == "" {
			return fmt.Errorf("backup requires -out, -golden, and -manifest")
		}
		db, err := openExistingDB(*dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		_, err = createBackup(db, *out, *golden, *manifest, time.Now, randomID)
		return err
	case "verify-restore":
		fs := flag.NewFlagSet("verify-restore", flag.ContinueOnError)
		dbPath := fs.String("db", "", "restored SQLite database path")
		golden := fs.String("golden", "", "expected portfolio snapshot JSON")
		manifest := fs.String("manifest", "", "backup manifest path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *dbPath == "" || *golden == "" || *manifest == "" {
			return fmt.Errorf("verify-restore requires -db, -golden, and -manifest")
		}
		return verifyManifest(*dbPath, *golden, *manifest)
	case "strategy-register":
		fs := flag.NewFlagSet("strategy-register", flag.ContinueOnError)
		dbPath := fs.String("db", "omni-folio.db", "SQLite database path")
		artifactPath := fs.String("artifact", "", "local strategy-improvement-result JSON path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *artifactPath == "" || fs.NArg() != 0 {
			return errors.New("strategy-register requires -artifact and no positional arguments")
		}
		artifact, err := readStrategyArtifact(*artifactPath)
		if err != nil {
			return err
		}
		db, err := openExistingDB(*dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := requireSchema(db); err != nil {
			return err
		}
		evidence, err := newService(db, time.Now, randomID).registerStrategyEvidence(context.Background(), artifact)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(evidence)
	case "strategy-status":
		fs := flag.NewFlagSet("strategy-status", flag.ContinueOnError)
		dbPath := fs.String("db", "omni-folio.db", "SQLite database path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("strategy-status accepts no positional arguments")
		}
		db, err := openExistingDB(*dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := requireSchema(db); err != nil {
			return err
		}
		proof, err := proveStrategyRegistryRecovery(context.Background(), db)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(StrategySelectionState{
			CurrentEventID: proof.CurrentEventID, SelectedResultSHA256: proof.SelectedResultSHA256,
		})
	case "strategy-select":
		fs := flag.NewFlagSet("strategy-select", flag.ContinueOnError)
		dbPath := fs.String("db", "omni-folio.db", "SQLite database path")
		resultSHA := fs.String("result-sha256", "", "registered paper_candidate result SHA-256")
		expected := fs.String("expected-current-event", "", "current selection event ID or no_event")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *resultSHA == "" || *expected == "" || fs.NArg() != 0 {
			return errors.New("strategy-select requires -result-sha256, -expected-current-event, and no positional arguments")
		}
		db, err := openExistingDB(*dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := requireSchema(db); err != nil {
			return err
		}
		state, err := newService(db, time.Now, randomID).selectPaperCandidate(context.Background(), *resultSHA, *expected)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(state)
	case "strategy-rollback":
		fs := flag.NewFlagSet("strategy-rollback", flag.ContinueOnError)
		dbPath := fs.String("db", "omni-folio.db", "SQLite database path")
		expected := fs.String("expected-current-event", "", "current selection event ID")
		source := fs.String("source-event", "", "selection event being rolled back")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *expected == "" || *source == "" || fs.NArg() != 0 {
			return errors.New("strategy-rollback requires -expected-current-event, -source-event, and no positional arguments")
		}
		db, err := openExistingDB(*dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := requireSchema(db); err != nil {
			return err
		}
		state, err := newService(db, time.Now, randomID).rollbackPaperCandidate(context.Background(), *expected, *source)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(state)
	case "paper-run-due":
		fs := flag.NewFlagSet("paper-run-due", flag.ContinueOnError)
		dbPath := fs.String("db", "omni-folio.db", "SQLite database path")
		accountRef := fs.String("account", "", "paper account reference")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *accountRef == "" || fs.NArg() != 0 {
			return errors.New("paper-run-due requires -account and no positional arguments")
		}
		db, err := openExistingDB(*dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := requireSchema(db); err != nil {
			return err
		}
		result, err := newService(db, time.Now, randomID).runDuePaperPerformancePolicy(context.Background(), *accountRef)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "paper-run-loop":
		fs := flag.NewFlagSet("paper-run-loop", flag.ContinueOnError)
		dbPath := fs.String("db", "omni-folio.db", "SQLite database path")
		accountRef := fs.String("account", "", "paper account reference")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *accountRef == "" || fs.NArg() != 0 {
			return errors.New("paper-run-loop requires -account and no positional arguments")
		}
		db, err := openExistingDB(*dbPath)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := requireSchema(db); err != nil {
			return err
		}
		return newService(db, time.Now, randomID).runPaperPerformanceLoop(ctx, *accountRef)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
