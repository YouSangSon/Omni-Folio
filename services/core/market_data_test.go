package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type marketDataPortFunc func(context.Context, string, string) (*MarketDataSeries, error)

func (f marketDataPortFunc) Candles(ctx context.Context, symbol, interval string) (*MarketDataSeries, error) {
	return f(ctx, symbol, interval)
}

func TestMarketDataCandlesFixtureResponseIsExact(t *testing.T) {
	marketData, err := loadMarketDataFixture(fixturePath("market-bars.csv"))
	if err != nil {
		t.Fatal(err)
	}
	svc, _ := testService(t, []time.Time{mustTime("2026-01-10T15:01:00Z")}, nil)
	svc.marketData = marketData
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/market-data/candles?symbol=AAPL&interval=1d", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	const want = "{\"symbol\":\"AAPL\",\"venue\":\"XNAS\",\"timezone\":\"America/New_York\",\"interval\":\"1d\",\"source\":\"local_fixture\",\"sample\":true,\"state\":\"stale\",\"source_as_of\":\"2026-01-07T00:00:00Z\",\"fetched_at\":\"2026-01-10T15:01:00Z\",\"issues\":[{\"code\":\"sample_data\",\"message\":\"market data is a local sample and not live\"}],\"bars\":[{\"at\":\"2026-01-02T00:00:00Z\",\"open\":\"10\",\"high\":\"10.5\",\"low\":\"9.5\",\"close\":\"10\",\"volume\":\"100\"},{\"at\":\"2026-01-03T00:00:00Z\",\"open\":\"11\",\"high\":\"11.75\",\"low\":\"10.5\",\"close\":\"11.5\",\"volume\":\"6\"},{\"at\":\"2026-01-04T00:00:00Z\",\"open\":\"12\",\"high\":\"13\",\"low\":\"11.5\",\"close\":\"12.5\",\"volume\":\"8\"},{\"at\":\"2026-01-05T00:00:00Z\",\"open\":\"13\",\"high\":\"14.5\",\"low\":\"12.75\",\"close\":\"14\",\"volume\":\"20\"},{\"at\":\"2026-01-06T00:00:00Z\",\"open\":\"14\",\"high\":\"15.5\",\"low\":\"13.5\",\"close\":\"15\",\"volume\":\"100\"},{\"at\":\"2026-01-07T00:00:00Z\",\"open\":\"16\",\"high\":\"16.5\",\"low\":\"15.5\",\"close\":\"16\",\"volume\":\"100\"}]}\n"
	if w.Body.String() != want {
		t.Fatalf("unexpected response:\n%s", w.Body.String())
	}
}

func TestMarketDataFixtureRejectsInvalidRows(t *testing.T) {
	header := "bar_at,symbol,venue,timezone,interval,open,high,low,close,volume\n"
	valid := "2026-01-02T00:00:00Z,AAPL,XNAS,America/New_York,1d,10,11,9,10,100\n"
	tests := map[string]string{
		"empty":        header,
		"too many":     header + strings.Repeat(valid, maxMarketDataRows+1),
		"metadata":     header + valid + "2026-01-03T00:00:00Z,MSFT,XNAS,America/New_York,1d,10,11,9,10,100\n",
		"unordered":    header + valid + "2026-01-01T00:00:00Z,AAPL,XNAS,America/New_York,1d,10,11,9,10,100\n",
		"duplicate":    header + valid + valid,
		"invalid ohlc": header + "2026-01-02T00:00:00Z,AAPL,XNAS,America/New_York,1d,10,9,8,10,100\n",
		"volume":       header + "2026-01-02T00:00:00Z,AAPL,XNAS,America/New_York,1d,10,11,9,10,-1\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseMarketDataFixture(strings.NewReader(input)); err == nil {
				t.Fatal("invalid fixture was accepted")
			}
		})
	}
}

func TestMarketDataFixtureCapsInputBeforeCSVParsing(t *testing.T) {
	_, err := parseMarketDataFixture(strings.NewReader(strings.Repeat("x", maxMarketDataBytes+1)))
	if err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("oversized fixture error=%v", err)
	}
}

func TestMarketDataCandlesErrorsAndQueryValidation(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	request := func(path string) (int, APIError) {
		t.Helper()
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var body APIError
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return w.Code, body
	}
	for _, path := range []string{
		"/v1/market-data/candles?interval=1d",
		"/v1/market-data/candles?symbol=AAPL&symbol=AAPL&interval=1d",
		"/v1/market-data/candles?symbol=AAPL&interval=1d&limit=1",
	} {
		status, body := request(path)
		if status != http.StatusBadRequest || body.Code != "invalid_query" {
			t.Fatalf("path=%s status=%d body=%+v", path, status, body)
		}
	}
	status, body := request("/v1/market-data/candles?symbol=AAPL&interval=1d")
	if status != http.StatusServiceUnavailable || body != (APIError{Code: "market_data_unavailable", Message: "market data is unavailable"}) {
		t.Fatalf("unavailable status=%d body=%+v", status, body)
	}
	marketData, err := loadMarketDataFixture(fixturePath("market-bars.csv"))
	if err != nil {
		t.Fatal(err)
	}
	svc.marketData = marketData
	status, body = request("/v1/market-data/candles?symbol=MSFT&interval=1d")
	if status != http.StatusNotFound || body != (APIError{Code: "market_data_not_found", Message: "market data was not found"}) {
		t.Fatalf("not found status=%d body=%+v", status, body)
	}
	svc.marketData = marketDataPortFunc(func(context.Context, string, string) (*MarketDataSeries, error) {
		return &MarketDataSeries{Symbol: "MSFT", Interval: "1d", Bars: []MarketDataBar{{At: "2026-01-07T00:00:00Z"}}}, nil
	})
	status, body = request("/v1/market-data/candles?symbol=AAPL&interval=1d")
	if status != http.StatusInternalServerError || body != (APIError{Code: "internal_error", Message: "internal server error"}) {
		t.Fatalf("mismatch status=%d body=%+v", status, body)
	}
}
