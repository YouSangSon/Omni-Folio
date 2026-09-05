package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const paperSnapshotHeader = "bar_at,symbol,venue,timezone,interval,open,high,low,close,volume,open_at,source_available_at,fetched_at\n"

func TestPaperSnapshotImportIsAtomicIdempotentAndAppendOnly(t *testing.T) {
	svc := paperSnapshotTestService(t)
	svc.now = func() time.Time { return paperSnapshotMustTime("2026-01-10T07:00:00Z") }
	firstRaw := paperSnapshotCSV(
		paperSnapshotRow("2026-01-07", "005930", "1005"),
		paperSnapshotRow("2026-01-08", "005930", "1006"),
	)
	first, err := svc.importPaperSnapshot(context.Background(), firstRaw)
	if err != nil {
		t.Fatal(err)
	}
	firstHash := sha256.Sum256(firstRaw)
	if first.InputSHA256 != hex.EncodeToString(firstHash[:]) || first.Bars != 2 || first.Added != 2 || first.SignalBarObservationID == "" {
		t.Fatalf("first import=%+v", first)
	}
	var firstRecordedAt string
	if err := svc.db.QueryRow(`SELECT recorded_at FROM paper_market_bar_observations ORDER BY close_at LIMIT 1`).Scan(&firstRecordedAt); err != nil {
		t.Fatal(err)
	}

	retry, err := svc.importPaperSnapshot(context.Background(), firstRaw)
	if err != nil || retry.InputSHA256 != first.InputSHA256 || retry.SignalBarObservationID != first.SignalBarObservationID || retry.Bars != first.Bars || retry.Added != 0 {
		t.Fatalf("retry=%+v first=%+v err=%v", retry, first, err)
	}

	svc.now = func() time.Time { return paperSnapshotMustTime("2026-01-11T07:00:00Z") }
	appendRaw := paperSnapshotCSV(
		paperSnapshotRow("2026-01-07", "005930", "1005"),
		paperSnapshotRow("2026-01-08", "005930", "1006"),
		paperSnapshotRow("2026-01-09", "005930", "1007"),
	)
	appended, err := svc.importPaperSnapshot(context.Background(), appendRaw)
	if err != nil {
		t.Fatal(err)
	}
	if appended.Bars != 3 || appended.Added != 1 || appended.SignalBarObservationID == first.SignalBarObservationID {
		t.Fatalf("append=%+v", appended)
	}
	var count int
	var preservedHash, preservedRecordedAt, anchorHash string
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_market_bar_observations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT input_data_sha256,recorded_at FROM paper_market_bar_observations ORDER BY close_at LIMIT 1`).Scan(&preservedHash, &preservedRecordedAt); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT input_data_sha256 FROM paper_market_bar_observations WHERE observation_id=?`, appended.SignalBarObservationID).Scan(&anchorHash); err != nil {
		t.Fatal(err)
	}
	appendHash := sha256.Sum256(appendRaw)
	if count != 3 || preservedHash != first.InputSHA256 || preservedRecordedAt != firstRecordedAt || anchorHash != hex.EncodeToString(appendHash[:]) {
		t.Fatalf("count=%d prefix hash=%s recorded=%s anchor hash=%s", count, preservedHash, preservedRecordedAt, anchorHash)
	}
	rollingRaw := paperSnapshotCSV(
		paperSnapshotRow("2026-01-08", "005930", "1006"),
		paperSnapshotRow("2026-01-09", "005930", "1007"),
		paperSnapshotRow("2026-01-10", "005930", "1008"),
	)
	rolling, err := svc.importPaperSnapshot(context.Background(), rollingRaw)
	if err != nil || rolling.Added != 1 || rolling.Bars != 3 {
		t.Fatalf("rolling import=%+v err=%v", rolling, err)
	}

	altered := bytes.ReplaceAll(rollingRaw, []byte("\n"), []byte("\r\n"))
	if _, err := svc.importPaperSnapshot(context.Background(), altered); err == nil || !strings.Contains(err.Error(), "stored anchor") {
		t.Fatalf("altered bytes with the same final bar error=%v", err)
	}
}

func TestPaperSnapshotImportRollsBackWhenLaterInsertFails(t *testing.T) {
	svc := paperSnapshotTestService(t)
	svc.now = func() time.Time { return paperSnapshotMustTime("2026-01-10T07:00:00Z") }
	if _, err := svc.db.Exec(`CREATE TRIGGER paper_snapshot_test_failure BEFORE INSERT ON paper_market_bar_observations
		WHEN NEW.close_at='2026-01-08T06:30:00.000000000Z' BEGIN SELECT RAISE(ABORT, 'injected later row failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := svc.importPaperSnapshot(context.Background(), paperSnapshotCSV(
		paperSnapshotRow("2026-01-07", "005930", "1005"),
		paperSnapshotRow("2026-01-08", "005930", "1006"),
	))
	if err == nil {
		t.Fatal("injected later-row failure was ignored")
	}
	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_market_bar_observations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial snapshot remained: count=%d err=%v", count, err)
	}
}

func TestPaperSnapshotImportRejectsClosedContractViolations(t *testing.T) {
	validRow := paperSnapshotRow("2026-01-07", "005930", "1005")
	tests := map[string][]byte{
		"empty":            []byte(paperSnapshotHeader),
		"wrong header":     []byte(strings.Replace(paperSnapshotHeader, "bar_at", "at", 1) + validRow + "\n"),
		"duplicate header": []byte(paperSnapshotHeader + strings.TrimSuffix(paperSnapshotHeader, "\n") + "\n"),
		"new symbol":       paperSnapshotCSV(validRow, strings.Replace(paperSnapshotRow("2026-01-08", "005930", "1006"), "005930", "000660", 1)),
		"bad venue":        paperSnapshotCSV(strings.Replace(validRow, ",KRX,", ",XNAS,", 1)),
		"bad timezone":     paperSnapshotCSV(strings.Replace(validRow, ",Asia/Seoul,", ",UTC,", 1)),
		"bad interval":     paperSnapshotCSV(strings.Replace(validRow, ",1d,", ",1m,", 1)),
		"bad symbol":       paperSnapshotCSV(strings.Replace(validRow, "005930", "AAPL", 1)),
		"noncanonical":     paperSnapshotCSV(strings.Replace(validRow, ",1000,1010,", ",01000,1010,", 1)),
		"bad OHLC":         paperSnapshotCSV(strings.Replace(validRow, ",1000,1010,990,1005,", ",1000,1001,990,1005,", 1)),
		"negative volume":  paperSnapshotCSV(strings.Replace(validRow, ",10000,", ",-1,", 1)),
		"unordered":        paperSnapshotCSV(paperSnapshotRow("2026-01-08", "005930", "1006"), validRow),
		"duplicate close":  paperSnapshotCSV(validRow, validRow),
		"fractional time":  paperSnapshotCSV(strings.Replace(validRow, "T06:30:00Z", "T06:30:00.1Z", 1)),
		"offset time":      paperSnapshotCSV(strings.Replace(validRow, "T06:30:00Z", "T15:30:00+09:00", 1)),
		"open at close":    paperSnapshotCSV(strings.Replace(validRow, "T00:00:00Z", "T06:30:00Z", 1)),
		"early available":  paperSnapshotCSV(strings.Replace(validRow, "T06:31:00Z", "T06:29:00Z", 1)),
		"early fetch":      paperSnapshotCSV(strings.Replace(validRow, "T06:32:00Z", "T06:30:00Z", 1)),
		"future fetch":     paperSnapshotCSV(strings.Replace(validRow, "2026-01-07T06:32:00Z", "2026-01-11T06:32:00Z", 1)),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			svc := paperSnapshotTestService(t)
			svc.now = func() time.Time { return paperSnapshotMustTime("2026-01-10T07:00:00Z") }
			if _, err := svc.importPaperSnapshot(context.Background(), raw); err == nil {
				t.Fatal("invalid snapshot was accepted")
			}
			var count int
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_market_bar_observations`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("invalid snapshot left rows: count=%d err=%v", count, err)
			}
		})
	}

	svc := paperSnapshotTestService(t)
	if _, err := svc.importPaperSnapshot(context.Background(), []byte(strings.Repeat("x", maxMarketDataBytes+1))); err == nil {
		t.Fatal("oversized snapshot was accepted")
	}
	rows := make([]string, maxMarketDataRows+1)
	for index := range rows {
		rows[index] = paperSnapshotRow(fmt.Sprintf("2024-01-%02d", index+1), "005930", "1005")
	}
	if _, err := svc.importPaperSnapshot(context.Background(), paperSnapshotCSV(rows...)); err == nil {
		t.Fatal("snapshot over row cap was accepted")
	}
}

func TestPaperSnapshotImportRejectsHistoryRewritesRetroactiveRowsAndGaps(t *testing.T) {
	svc := paperSnapshotTestService(t)
	svc.now = func() time.Time { return paperSnapshotMustTime("2026-01-10T07:00:00Z") }
	base := paperSnapshotCSV(
		paperSnapshotRow("2026-01-06", "005930", "1004"),
		paperSnapshotRow("2026-01-07", "005930", "1005"),
		paperSnapshotRow("2026-01-08", "005930", "1006"),
	)
	if _, err := svc.importPaperSnapshot(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{
		"OHLC rewrite": paperSnapshotCSV(
			paperSnapshotRow("2026-01-07", "005930", "1004"),
			paperSnapshotRow("2026-01-08", "005930", "1006"),
		),
		"retroactive insert": paperSnapshotCSV(
			paperSnapshotRow("2026-01-05", "005930", "1003"),
			paperSnapshotRow("2026-01-08", "005930", "1006"),
		),
		"stored gap omitted": paperSnapshotCSV(
			paperSnapshotRow("2026-01-06", "005930", "1004"),
			paperSnapshotRow("2026-01-08", "005930", "1006"),
			paperSnapshotRow("2026-01-09", "005930", "1007"),
		),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.importPaperSnapshot(context.Background(), raw); err == nil {
				t.Fatal("conflicting snapshot was accepted")
			}
			var count int
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_market_bar_observations`).Scan(&count); err != nil || count != 3 {
				t.Fatalf("history changed: count=%d err=%v", count, err)
			}
		})
	}
}

func paperSnapshotCSV(rows ...string) []byte {
	return []byte(paperSnapshotHeader + strings.Join(rows, "\n") + "\n")
}

func paperSnapshotRow(day, symbol, close string) string {
	return fmt.Sprintf("%sT06:30:00Z,%s,KRX,Asia/Seoul,1d,1000,1010,990,%s,10000,%sT00:00:00Z,%sT06:31:00Z,%sT06:32:00Z", day, symbol, close, day, day, day)
}

func paperSnapshotTestService(t *testing.T) *Service {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	return newService(db, func() time.Time { return paperSnapshotMustTime("2026-01-10T07:00:00Z") }, func(prefix string) string { return prefix + "_snapshot_test" })
}

func paperSnapshotMustTime(raw string) time.Time {
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		panic(err)
	}
	return value
}
