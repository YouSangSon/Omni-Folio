package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: omni-core <migrate|serve|backup|verify-restore>")
	}
	switch args[0] {
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
		if err := requireSchema(db); err != nil {
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
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
