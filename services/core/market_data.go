package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"reflect"
	"time"
)

const (
	maxMarketDataRows                    = 500
	maxMarketDataBytes                   = 1 << 20
	marketDataAdjustmentUnspecified      = "unspecified"
	marketDataAdjustmentProviderAdjusted = "provider_adjusted"
)

var errMarketDataNotFound = errors.New("market data not found")

type MarketDataPort interface {
	Candles(context.Context, string, string) (*MarketDataSeries, error)
}

type MarketDataSeries struct {
	Symbol          string
	Venue           string
	Timezone        string
	Interval        string
	PriceAdjustment string
	Bars            []MarketDataBar
}

type MarketDataBar struct {
	At     string `json:"at"`
	Open   string `json:"open"`
	High   string `json:"high"`
	Low    string `json:"low"`
	Close  string `json:"close"`
	Volume string `json:"volume"`
}

type marketDataFixture struct{ series *MarketDataSeries }

func (f *marketDataFixture) Candles(_ context.Context, symbol, interval string) (*MarketDataSeries, error) {
	if f.series.Symbol != symbol || f.series.Interval != interval {
		return nil, errMarketDataNotFound
	}
	return f.series, nil
}

func loadMarketDataFixture(path string) (MarketDataPort, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open market fixture: %w", err)
	}
	defer f.Close()
	return parseMarketDataFixture(f)
}

func parseMarketDataFixture(source io.Reader) (MarketDataPort, error) {
	input, err := io.ReadAll(io.LimitReader(source, maxMarketDataBytes+1))
	if err != nil {
		return nil, errors.New("market fixture is unreadable")
	}
	if len(input) > maxMarketDataBytes {
		return nil, fmt.Errorf("market fixture must not exceed %d bytes", maxMarketDataBytes)
	}
	records, err := csv.NewReader(bytes.NewReader(input)).ReadAll()
	if err != nil {
		return nil, errors.New("market fixture must be valid CSV")
	}
	header := []string{"bar_at", "symbol", "venue", "timezone", "interval", "open", "high", "low", "close", "volume"}
	if len(records) < 2 || !reflect.DeepEqual(records[0], header) {
		return nil, errors.New("market fixture requires the canonical header and 1 to 500 rows")
	}
	if len(records)-1 > maxMarketDataRows {
		return nil, fmt.Errorf("market fixture must not contain more than %d rows", maxMarketDataRows)
	}
	series := &MarketDataSeries{
		Symbol: records[1][1], Venue: records[1][2], Timezone: records[1][3], Interval: records[1][4],
		PriceAdjustment: marketDataAdjustmentUnspecified,
	}
	if series.Symbol == "" || series.Venue == "" || series.Timezone == "" || series.Interval == "" {
		return nil, errors.New("market fixture symbol, venue, timezone, and interval are required")
	}
	var previous time.Time
	for rowNumber, row := range records[1:] {
		if row[1] != series.Symbol || row[2] != series.Venue || row[3] != series.Timezone || row[4] != series.Interval {
			return nil, fmt.Errorf("market fixture row %d has inconsistent metadata", rowNumber+2)
		}
		at, err := time.Parse(time.RFC3339, row[0])
		if err != nil {
			return nil, fmt.Errorf("market fixture row %d has invalid bar_at", rowNumber+2)
		}
		if !previous.IsZero() && !at.After(previous) {
			return nil, fmt.Errorf("market fixture row %d is not strictly ordered by bar_at", rowNumber+2)
		}
		previous = at
		prices := make([]*big.Rat, 4)
		for i, name := range []string{"open", "high", "low", "close"} {
			value, err := parseDecimal(row[i+5])
			if err != nil || value.Sign() <= 0 {
				return nil, fmt.Errorf("market fixture row %d has invalid %s", rowNumber+2, name)
			}
			prices[i] = value
		}
		volume, err := parseDecimal(row[9])
		if err != nil || volume.Sign() < 0 {
			return nil, fmt.Errorf("market fixture row %d has invalid volume", rowNumber+2)
		}
		if prices[2].Cmp(prices[0]) > 0 || prices[2].Cmp(prices[3]) > 0 ||
			prices[0].Cmp(prices[1]) > 0 || prices[3].Cmp(prices[1]) > 0 {
			return nil, fmt.Errorf("market fixture row %d has invalid OHLC range", rowNumber+2)
		}
		series.Bars = append(series.Bars, MarketDataBar{At: at.UTC().Format(time.RFC3339Nano), Open: row[5], High: row[6], Low: row[7], Close: row[8], Volume: row[9]})
	}
	return &marketDataFixture{series: series}, nil
}

func (s *Service) handleMarketDataCandles(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key := range query {
		if key != "symbol" && key != "interval" {
			writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: "unknown query parameter " + key, Field: key}})
			return
		}
	}
	for _, key := range []string{"symbol", "interval"} {
		values := query[key]
		if len(values) == 0 || values[0] == "" {
			writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: key + " query parameter is required", Field: key}})
			return
		}
		if len(values) != 1 {
			writeError(w, &appError{http.StatusBadRequest, APIError{Code: "invalid_query", Message: key + " query parameter must appear exactly once", Field: key}})
			return
		}
	}
	if s.marketData == nil {
		writeError(w, &appError{http.StatusServiceUnavailable, APIError{Code: "market_data_unavailable", Message: "market data is unavailable"}})
		return
	}
	series, err := s.marketData.Candles(r.Context(), query.Get("symbol"), query.Get("interval"))
	if errors.Is(err, errMarketDataNotFound) {
		writeError(w, &appError{http.StatusNotFound, APIError{Code: "market_data_not_found", Message: "market data was not found"}})
		return
	}
	if err != nil {
		writeError(w, internalError(err))
		return
	}
	if series == nil || len(series.Bars) == 0 {
		writeError(w, internalError(errors.New("market data port returned no bars")))
		return
	}
	if series.Symbol != query.Get("symbol") || series.Interval != query.Get("interval") {
		writeError(w, internalError(errors.New("market data port returned a mismatched series")))
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Symbol     string          `json:"symbol"`
		Venue      string          `json:"venue"`
		Timezone   string          `json:"timezone"`
		Interval   string          `json:"interval"`
		Source     string          `json:"source"`
		Sample     bool            `json:"sample"`
		State      string          `json:"state"`
		SourceAsOf string          `json:"source_as_of"`
		FetchedAt  string          `json:"fetched_at"`
		Issues     []APIError      `json:"issues"`
		Bars       []MarketDataBar `json:"bars"`
	}{series.Symbol, series.Venue, series.Timezone, series.Interval, "local_fixture", true, "stale", series.Bars[len(series.Bars)-1].At, s.now().UTC().Format(time.RFC3339Nano), []APIError{{Code: "sample_data", Message: "market data is a local sample and not live"}}, series.Bars})
}
