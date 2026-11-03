package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

func validatePaperResearchInput(raw []byte, expectedSHA, symbol, proposalAsOf string) error {
	if len(raw) > maxMarketDataBytes {
		return fmt.Errorf("paper research input must not exceed %d bytes", maxMarketDataBytes)
	}
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != expectedSHA {
		return errors.New("paper research input SHA-256 mismatch")
	}
	if !utf8.Valid(raw) || !kiwoomStockPattern.MatchString(symbol) {
		return errors.New("paper research input metadata is invalid")
	}
	records, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		return errors.New("paper research input must be valid CSV with fixed row widths")
	}
	if len(records) < 2 {
		return errors.New("paper research input requires a header and at least one row")
	}
	if len(records)-1 > maxMarketDataRows {
		return fmt.Errorf("paper research input must not contain more than %d rows", maxMarketDataRows)
	}
	columns := make(map[string]int, len(records[0]))
	for index, name := range records[0] {
		if _, exists := columns[name]; exists {
			return errors.New("paper research input column names must be unique")
		}
		columns[name] = index
	}
	required := []string{"bar_at", "symbol", "open", "high", "low", "close", "volume"}
	for _, name := range required {
		if _, exists := columns[name]; !exists {
			return errors.New("paper research input lacks a required column")
		}
	}

	var last time.Time
	for rowIndex, row := range records[1:] {
		if row[columns["symbol"]] != symbol {
			return fmt.Errorf("paper research input row %d has a mismatched symbol", rowIndex+2)
		}
		at, ok := paperResearchWholeSecond(row[columns["bar_at"]])
		if !ok || (!last.IsZero() && !at.After(last)) {
			return fmt.Errorf("paper research input row %d has an invalid or unordered bar_at", rowIndex+2)
		}
		last = at
		if err := validateMarketDataValues(row[columns["open"]], row[columns["high"]], row[columns["low"]], row[columns["close"]], row[columns["volume"]]); err != nil {
			return fmt.Errorf("paper research input row %d: %w", rowIndex+2, err)
		}
	}
	proposalAt, ok := paperResearchWholeSecond(proposalAsOf)
	if !ok || !proposalAt.After(last) {
		return errors.New("paper proposal must be a UTC whole-second timestamp after the research sample")
	}
	return nil
}

func paperResearchWholeSecond(raw string) (time.Time, bool) {
	value, err := time.Parse(time.RFC3339, raw)
	return value, err == nil && value.Location() == time.UTC && value.Nanosecond() == 0 && value.Format(time.RFC3339) == raw
}
