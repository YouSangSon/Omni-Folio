package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestG38C2PaperMarketBarImmutableClosedObservation(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-10T07:00:00Z") }
	ctx := context.Background()
	input := g38c2PaperMarketBar("fixture-005930-20260109", "2026-01-09T00:00:00Z", "2026-01-09T06:30:00Z")
	input.ObservationID, input.SchemaVersion, input.RecordedAt = "caller-id", "caller-schema", "1999-01-01T00:00:00Z"

	first, err := svc.recordPaperMarketBar(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	retry := input
	retry.ObservationID, retry.SchemaVersion, retry.RecordedAt = "different-caller-id", "different-caller-schema", "2000-01-01T00:00:00Z"
	second, err := svc.recordPaperMarketBar(ctx, retry)
	if err != nil || *second != *first {
		t.Fatalf("exact retry first=%+v second=%+v err=%v", first, second, err)
	}
	if first.SchemaVersion != "paper-market-bar.v1" || first.ObservationID == input.ObservationID || first.RecordedAt != "2026-01-10T07:00:00Z" {
		t.Fatalf("service did not own bar identity/schema/time: %+v", first)
	}
	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_market_bar_observations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("bar count=%d err=%v", count, err)
	}

	changed := input
	changed.Close = "1001"
	if _, err := svc.recordPaperMarketBar(ctx, changed); err == nil {
		t.Fatal("changed retry reused a source observation identity")
	}
	for _, statement := range []string{
		`UPDATE paper_market_bar_observations SET close='1001'`,
		`DELETE FROM paper_market_bar_observations`,
	} {
		if _, err := svc.db.Exec(statement); err == nil {
			t.Fatalf("SQLite accepted immutable bar mutation: %s", statement)
		}
	}

	direct := *first
	direct.ObservationID = "paper_market_bar_direct_malformed"
	direct.SourceObservationID = "fixture-direct-malformed"
	direct.OpenAt = "2026-01-08T00:00:00Z"
	direct.CloseAt = "2026-01-08T06:30:00Z"
	direct.SourceAvailableAt = "2026-01-08T06:31:00Z"
	direct.FetchedAt = "2026-01-08T06:32:00Z"
	direct.Open = "01"
	if err := insertPaperMarketBarDirectForTest(svc, direct); err == nil {
		t.Fatal("SQLite accepted a directly inserted malformed bar")
	}
}

func TestG38C2PaperMarketBarRejectsInvalidContract(t *testing.T) {
	valid := g38c2PaperMarketBar("fixture-base", "2026-01-09T00:00:00Z", "2026-01-09T06:30:00Z")
	tests := []struct {
		name   string
		mutate func(*PaperMarketBarObservation)
	}{
		{"source", func(v *PaperMarketBarObservation) { v.Source = "other" }},
		{"venue", func(v *PaperMarketBarObservation) { v.Venue = "NYSE" }},
		{"currency", func(v *PaperMarketBarObservation) { v.Currency = "USD" }},
		{"interval", func(v *PaperMarketBarObservation) { v.Interval = "1m" }},
		{"timezone", func(v *PaperMarketBarObservation) { v.Timezone = "UTC" }},
		{"adjustment", func(v *PaperMarketBarObservation) { v.PriceAdjustment = "split" }},
		{"open after close", func(v *PaperMarketBarObservation) { v.OpenAt = v.CloseAt }},
		{"available before close", func(v *PaperMarketBarObservation) { v.SourceAvailableAt = "2026-01-09T06:29:59Z" }},
		{"fetched before available", func(v *PaperMarketBarObservation) { v.FetchedAt = "2026-01-09T06:30:30Z" }},
		{"future fetched", func(v *PaperMarketBarObservation) { v.FetchedAt = "2026-01-10T07:00:00.000000001Z" }},
		{"noncanonical open", func(v *PaperMarketBarObservation) { v.Open = "01000" }},
		{"nonpositive low", func(v *PaperMarketBarObservation) { v.Low = "0" }},
		{"high range", func(v *PaperMarketBarObservation) { v.High = "999" }},
		{"low range", func(v *PaperMarketBarObservation) { v.Low = "1001" }},
		{"negative volume", func(v *PaperMarketBarObservation) { v.Volume = "-1" }},
		{"noncanonical volume", func(v *PaperMarketBarObservation) { v.Volume = "1.0" }},
		{"uppercase input hash", func(v *PaperMarketBarObservation) { v.InputDataSHA256 = strings.Repeat("A", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			svc.now = func() time.Time { return mustTime("2026-01-10T07:00:00Z") }
			candidate := valid
			test.mutate(&candidate)
			if _, err := svc.recordPaperMarketBar(context.Background(), candidate); err == nil {
				t.Fatal("invalid closed bar was accepted")
			}
			var count int
			if err := svc.db.QueryRow(`SELECT COUNT(*) FROM paper_market_bar_observations`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rejected bar left rows: count=%d err=%v", count, err)
			}
		})
	}
}

func g38c2PaperMarketBar(sourceID, openAt, closeAt string) PaperMarketBarObservation {
	return PaperMarketBarObservation{
		Source: "paper_fixture", SourceObservationID: sourceID,
		InputDataSHA256: strings.Repeat("a", 64), Symbol: "005930", Venue: "KRX", Currency: "KRW",
		Interval: "1d", Timezone: "Asia/Seoul", PriceAdjustment: "unspecified",
		Open: "1000", High: "1010", Low: "990", Close: "1005", Volume: "10000",
		OpenAt: openAt, CloseAt: closeAt, SourceAvailableAt: "2026-01-09T06:31:00Z", FetchedAt: "2026-01-09T06:32:00Z",
	}
}

func insertPaperMarketBarDirectForTest(svc *Service, bar PaperMarketBarObservation) error {
	recordJSON, recordSHA, err := orderJSONHash(bar)
	if err != nil {
		return err
	}
	_, err = svc.db.Exec(`INSERT INTO paper_market_bar_observations(
		observation_id,schema_version,source,source_observation_id,input_data_sha256,symbol,venue,currency,interval,timezone,
		price_adjustment,open,high,low,close,volume,open_at,close_at,source_available_at,fetched_at,record_sha256,record_json,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		bar.ObservationID, bar.SchemaVersion, bar.Source, bar.SourceObservationID, bar.InputDataSHA256, bar.Symbol, bar.Venue, bar.Currency,
		bar.Interval, bar.Timezone, bar.PriceAdjustment, bar.Open, bar.High, bar.Low, bar.Close, bar.Volume, bar.OpenAt, bar.CloseAt,
		bar.SourceAvailableAt, bar.FetchedAt, recordSHA, string(recordJSON), bar.RecordedAt)
	return err
}
