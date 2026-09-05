package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"time"
)

var paperSnapshotCSVHeader = []string{
	"bar_at", "symbol", "venue", "timezone", "interval", "open", "high", "low", "close", "volume",
	"open_at", "source_available_at", "fetched_at",
}

type PaperSnapshotImport struct {
	InputSHA256            string `json:"input_sha256"`
	SignalBarObservationID string `json:"signal_bar_observation_id"`
	Bars                   int    `json:"bars"`
	Added                  int    `json:"added"`
}

func (s *Service) importPaperSnapshot(ctx context.Context, raw []byte) (*PaperSnapshotImport, error) {
	if s == nil || s.db == nil || s.now == nil {
		return nil, errors.New("paper snapshot importer is not configured")
	}
	if len(raw) > maxMarketDataBytes {
		return nil, fmt.Errorf("paper snapshot must not exceed %d bytes", maxMarketDataBytes)
	}
	records, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		return nil, errors.New("paper snapshot must be valid CSV")
	}
	if len(records) < 2 || !reflect.DeepEqual(records[0], paperSnapshotCSVHeader) {
		return nil, errors.New("paper snapshot requires the canonical header and 1 to 500 rows")
	}
	if len(records)-1 > maxMarketDataRows {
		return nil, fmt.Errorf("paper snapshot must not contain more than %d rows", maxMarketDataRows)
	}

	hash := sha256.Sum256(raw)
	inputSHA := hex.EncodeToString(hash[:])
	now := s.now().UTC()
	bars := make([]PaperMarketBarObservation, 0, len(records)-1)
	for index, row := range records[1:] {
		bar, err := paperSnapshotBar(row, inputSHA, now)
		if err != nil {
			return nil, fmt.Errorf("paper snapshot row %d: %w", index+2, err)
		}
		if index > 0 {
			if bar.Symbol != bars[0].Symbol || bar.Venue != bars[0].Venue || bar.Timezone != bars[0].Timezone || bar.Interval != bars[0].Interval {
				return nil, fmt.Errorf("paper snapshot row %d has inconsistent metadata", index+2)
			}
			if bar.CloseAt <= bars[index-1].CloseAt {
				return nil, fmt.Errorf("paper snapshot row %d is not strictly ordered by bar_at", index+2)
			}
		}
		bars = append(bars, bar)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := replayPaperMarketRecovery(ctx, tx); err != nil {
		return nil, fmt.Errorf("paper market recovery: %w", err)
	}
	existing, latestClose, err := loadPaperSnapshotRange(ctx, tx, bars[0], bars[len(bars)-1])
	if err != nil {
		return nil, err
	}
	provided := make(map[string]bool, len(bars))
	for _, bar := range bars {
		provided[bar.CloseAt] = true
	}
	for closeAt := range existing {
		if !provided[closeAt] {
			return nil, errors.New("paper snapshot omits stored history inside its range")
		}
	}

	added := 0
	var anchor *PaperMarketBarObservation
	for index := range bars {
		bar := bars[index]
		matches := existing[bar.CloseAt]
		if len(matches) > 1 {
			return nil, errors.New("paper snapshot history is ambiguous")
		}
		if len(matches) == 1 {
			if !samePaperSnapshotBar(matches[0], bar) {
				return nil, errors.New("paper snapshot conflicts with stored history")
			}
			anchor = &matches[0]
			continue
		}
		if latestClose != "" && bar.CloseAt <= latestClose {
			return nil, errors.New("paper snapshot cannot insert retroactive history")
		}
		stored, err := recordPaperMarketBarTx(ctx, tx, bar)
		if err != nil {
			return nil, err
		}
		anchor, added, latestClose = stored, added+1, bar.CloseAt
	}
	if anchor == nil {
		return nil, errors.New("paper snapshot has no anchor")
	}
	if anchor.InputDataSHA256 != inputSHA {
		return nil, errors.New("paper snapshot bytes conflict with the stored anchor")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &PaperSnapshotImport{InputSHA256: inputSHA, SignalBarObservationID: anchor.ObservationID, Bars: len(bars), Added: added}, nil
}

func paperSnapshotBar(row []string, inputSHA string, now time.Time) (PaperMarketBarObservation, error) {
	if len(row) != len(paperSnapshotCSVHeader) {
		return PaperMarketBarObservation{}, errors.New("has the wrong number of fields")
	}
	if !kiwoomStockPattern.MatchString(row[1]) || row[2] != "KRX" || row[3] != "Asia/Seoul" || row[4] != "1d" {
		return PaperMarketBarObservation{}, errors.New("has invalid metadata")
	}
	if err := validateMarketDataValues(row[5], row[6], row[7], row[8], row[9]); err != nil {
		return PaperMarketBarObservation{}, err
	}
	parsedTimes := make([]time.Time, 4)
	for index, raw := range []string{row[10], row[0], row[11], row[12]} {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil || value.Location() != time.UTC || value.Nanosecond() != 0 || value.Format(time.RFC3339) != raw {
			return PaperMarketBarObservation{}, errors.New("requires UTC whole-second timestamps")
		}
		parsedTimes[index] = value
	}
	if !parsedTimes[0].Before(parsedTimes[1]) || parsedTimes[1].After(parsedTimes[2]) || parsedTimes[2].After(parsedTimes[3]) || parsedTimes[3].After(now) {
		return PaperMarketBarObservation{}, errors.New("has invalid market-data timing")
	}
	closeAt := parsedTimes[1].Format(canonicalPaperTimeLayout)
	bar := PaperMarketBarObservation{
		ObservationID: paperMarketBarObservationID("paper_fixture", paperEventID("snapshot_bar", row[1], closeAt)),
		SchemaVersion: paperMarketBarSchema, Source: "paper_fixture", SourceObservationID: paperEventID("snapshot_bar", row[1], closeAt),
		InputDataSHA256: inputSHA, Symbol: row[1], Venue: row[2], Currency: "KRW", Interval: row[4], Timezone: row[3],
		PriceAdjustment: marketDataAdjustmentUnspecified, Open: row[5], High: row[6], Low: row[7], Close: row[8], Volume: row[9],
		OpenAt: parsedTimes[0].Format(canonicalPaperTimeLayout), CloseAt: closeAt,
		SourceAvailableAt: parsedTimes[2].Format(canonicalPaperTimeLayout), FetchedAt: parsedTimes[3].Format(canonicalPaperTimeLayout),
		RecordedAt: now.Format(canonicalPaperTimeLayout),
	}
	if err := validatePaperMarketBar(bar); err != nil {
		return PaperMarketBarObservation{}, err
	}
	return bar, nil
}

func loadPaperSnapshotRange(ctx context.Context, tx *sql.Tx, first, last PaperMarketBarObservation) (map[string][]PaperMarketBarObservation, string, error) {
	var latest string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(close_at),'') FROM paper_market_bar_observations
		WHERE source=? AND symbol=? AND venue=? AND interval=? AND timezone=? AND price_adjustment=?`,
		first.Source, first.Symbol, first.Venue, first.Interval, first.Timezone, first.PriceAdjustment).Scan(&latest); err != nil {
		return nil, "", err
	}
	rows, err := tx.QueryContext(ctx, `SELECT observation_id,schema_version,source,source_observation_id,input_data_sha256,symbol,venue,currency,
		interval,timezone,price_adjustment,open,high,low,close,volume,open_at,close_at,source_available_at,fetched_at,recorded_at
		FROM paper_market_bar_observations WHERE source=? AND symbol=? AND venue=? AND interval=? AND timezone=? AND price_adjustment=?
		AND close_at>=? AND close_at<=? ORDER BY close_at,sequence`, first.Source, first.Symbol, first.Venue, first.Interval,
		first.Timezone, first.PriceAdjustment, first.CloseAt, last.CloseAt)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	result := make(map[string][]PaperMarketBarObservation)
	for rows.Next() {
		var bar PaperMarketBarObservation
		if err := rows.Scan(&bar.ObservationID, &bar.SchemaVersion, &bar.Source, &bar.SourceObservationID, &bar.InputDataSHA256,
			&bar.Symbol, &bar.Venue, &bar.Currency, &bar.Interval, &bar.Timezone, &bar.PriceAdjustment, &bar.Open, &bar.High, &bar.Low,
			&bar.Close, &bar.Volume, &bar.OpenAt, &bar.CloseAt, &bar.SourceAvailableAt, &bar.FetchedAt, &bar.RecordedAt); err != nil {
			return nil, "", err
		}
		result[bar.CloseAt] = append(result[bar.CloseAt], bar)
	}
	return result, latest, rows.Err()
}

func samePaperSnapshotBar(stored, supplied PaperMarketBarObservation) bool {
	stored.ObservationID, stored.SourceObservationID, stored.InputDataSHA256, stored.RecordedAt = "", "", "", ""
	supplied.ObservationID, supplied.SourceObservationID, supplied.InputDataSHA256, supplied.RecordedAt = "", "", "", ""
	return stored == supplied
}
