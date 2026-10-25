package main

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
)

func openReadOnlyExistingDB(ctx context.Context, path string) (*sql.DB, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("read-only command requires an existing regular database")
	}
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = url.Values{
		"mode": {"ro"}, "_txlock": {"deferred"},
		"_busy_timeout": {"5000"}, "_foreign_keys": {"on"},
	}.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
