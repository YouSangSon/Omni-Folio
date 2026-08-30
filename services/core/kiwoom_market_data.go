package main

import (
	"context"
	"sort"
	"time"
)

// ponytail: fixed KST is the credential-free K1 assumption; load IANA tzdata
// before pre-1988 history is promoted into a backtest dataset.
var kiwoomMarketLocation = time.FixedZone("Asia/Seoul", 9*60*60)

type kiwoomCandleRow struct {
	Close      string `json:"cur_prc"`
	Volume     string `json:"trde_qty"`
	Date       string `json:"dt"`
	ExecutedAt string `json:"cntr_tm"`
	Open       string `json:"open_pric"`
	High       string `json:"high_pric"`
	Low        string `json:"low_pric"`
}

type kiwoomCandleResponse struct {
	kiwoomResult
	StockCode  string            `json:"stk_cd"`
	TickBars   []kiwoomCandleRow `json:"stk_tic_chart_qry"`
	DailyBars  []kiwoomCandleRow `json:"stk_dt_pole_chart_qry"`
	MinuteBars []kiwoomCandleRow `json:"stk_min_pole_chart_qry"`
}

type KiwoomLatestTrade struct {
	Source      string            `json:"source"`
	Environment KiwoomEnvironment `json:"environment"`
	Exchange    KiwoomExchange    `json:"exchange"`
	Symbol      string            `json:"symbol"`
	Currency    string            `json:"currency"`
	Price       string            `json:"price"`
	ObservedAt  string            `json:"observed_at"`
	FetchedAt   string            `json:"fetched_at"`
}

func (c *KiwoomClient) LatestTrade(ctx context.Context, symbol string) (*KiwoomLatestTrade, error) {
	if c == nil {
		return nil, &KiwoomError{Kind: "invalid_config"}
	}
	if !kiwoomStockPattern.MatchString(symbol) {
		return nil, &KiwoomError{Kind: "invalid_symbol"}
	}
	const apiID = "ka10079"
	page, err := c.readPage(ctx, apiID, map[string]string{
		"stk_cd": symbol, "tic_scope": "1", "upd_stkpc_tp": "0",
	}, "", "")
	if err != nil {
		return nil, err
	}
	var response kiwoomCandleResponse
	if err := kiwoomDecode(page.body, &response); err != nil || response.ReturnCode == nil {
		return nil, &KiwoomError{Kind: "invalid_response", APIID: apiID}
	}
	if err := kiwoomCheckResult(apiID, response.kiwoomResult); err != nil {
		return nil, err
	}
	responseSymbol, ok := kiwoomStockCode(response.StockCode)
	if !ok || responseSymbol != symbol {
		return nil, &KiwoomError{Kind: "symbol_mismatch", APIID: apiID}
	}
	if len(response.TickBars) == 0 {
		return nil, errMarketDataNotFound
	}
	latest, observedAt, ok := kiwoomNormalizeCandle(response.TickBars[0], true)
	if !ok {
		return nil, &KiwoomError{Kind: "invalid_trade", APIID: apiID}
	}
	previous := observedAt
	previousPrice := latest.Close
	for _, row := range response.TickBars[1:] {
		trade, at, ok := kiwoomNormalizeCandle(row, true)
		if !ok {
			return nil, &KiwoomError{Kind: "invalid_trade", APIID: apiID}
		}
		if at.After(previous) {
			return nil, &KiwoomError{Kind: "invalid_trade_order", APIID: apiID}
		}
		if at.Equal(previous) && trade.Close != previousPrice {
			return nil, &KiwoomError{Kind: "ambiguous_trade_time", APIID: apiID}
		}
		previous = at
		previousPrice = trade.Close
	}
	fetchedAt := c.now().UTC()
	if observedAt.After(fetchedAt) {
		return nil, &KiwoomError{Kind: "future_trade", APIID: apiID}
	}
	return &KiwoomLatestTrade{
		Source: "kiwoom", Environment: c.environment, Exchange: KiwoomKRX,
		Symbol: symbol, Currency: "KRW", Price: latest.Close,
		ObservedAt: observedAt.UTC().Format(time.RFC3339Nano), FetchedAt: fetchedAt.Format(time.RFC3339Nano),
	}, nil
}

func (c *KiwoomClient) Candles(ctx context.Context, symbol, interval string) (*MarketDataSeries, error) {
	if c == nil {
		return nil, &KiwoomError{Kind: "invalid_config"}
	}
	if !kiwoomStockPattern.MatchString(symbol) {
		return nil, &KiwoomError{Kind: "invalid_symbol"}
	}
	apiID, requestBody, minute := kiwoomCandleRequest(symbol, interval, c.now().In(kiwoomMarketLocation).Format("20060102"))
	if apiID == "" {
		return nil, &KiwoomError{Kind: "unsupported_interval"}
	}

	bars := make([]MarketDataBar, 0, maxMarketDataRows)
	seen := make(map[string]MarketDataBar)
	var previousProviderTime time.Time
	err := c.readPages(ctx, apiID, requestBody, func(body []byte) (bool, error) {
		// ponytail: read one page past the 500-bar cap to validate the provider's
		// page-boundary overlap; deeper history remains intentionally bounded.
		stopAfterPage := len(bars) >= maxMarketDataRows
		var response kiwoomCandleResponse
		if err := kiwoomDecode(body, &response); err != nil || response.ReturnCode == nil {
			return false, &KiwoomError{Kind: "invalid_response", APIID: apiID}
		}
		if err := kiwoomCheckResult(apiID, response.kiwoomResult); err != nil {
			return false, err
		}
		responseSymbol, ok := kiwoomStockCode(response.StockCode)
		if !ok || responseSymbol != symbol {
			return false, &KiwoomError{Kind: "symbol_mismatch", APIID: apiID}
		}
		rows := response.DailyBars
		if minute {
			rows = response.MinuteBars
		}
		for _, row := range rows {
			bar, at, ok := kiwoomNormalizeCandle(row, minute)
			if !ok {
				return false, &KiwoomError{Kind: "invalid_candle", APIID: apiID}
			}
			if !previousProviderTime.IsZero() && at.After(previousProviderTime) {
				return false, &KiwoomError{Kind: "invalid_candle_order", APIID: apiID}
			}
			previousProviderTime = at
			if existing, duplicate := seen[bar.At]; duplicate {
				if existing != bar {
					return false, &KiwoomError{Kind: "conflicting_candle", APIID: apiID}
				}
				continue
			}
			seen[bar.At] = bar
			bars = append(bars, bar)
		}
		return stopAfterPage, nil
	})
	if err != nil {
		return nil, err
	}
	if len(bars) == 0 {
		return nil, errMarketDataNotFound
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].At < bars[j].At })
	if len(bars) > maxMarketDataRows {
		bars = bars[len(bars)-maxMarketDataRows:]
	}
	return &MarketDataSeries{
		Symbol: symbol, Venue: "XKRX", Timezone: "Asia/Seoul", Interval: interval,
		PriceAdjustment: marketDataAdjustmentProviderAdjusted, Bars: bars,
	}, nil
}

func kiwoomCandleRequest(symbol, interval, baseDate string) (string, map[string]string, bool) {
	switch interval {
	case "1d":
		return "ka10081", map[string]string{"stk_cd": symbol, "base_dt": baseDate, "upd_stkpc_tp": "1"}, false
	case "1m", "3m", "5m", "10m", "15m", "30m", "45m", "60m":
		return "ka10080", map[string]string{
			"stk_cd": symbol, "tic_scope": interval[:len(interval)-1], "base_dt": baseDate, "upd_stkpc_tp": "1",
		}, true
	default:
		return "", nil, false
	}
}

func kiwoomNormalizeCandle(row kiwoomCandleRow, minute bool) (MarketDataBar, time.Time, bool) {
	rawTime, layout := row.Date, "20060102"
	if minute {
		rawTime, layout = row.ExecutedAt, "20060102150405"
	}
	at, err := time.ParseInLocation(layout, rawTime, kiwoomMarketLocation)
	if err != nil {
		return MarketDataBar{}, time.Time{}, false
	}
	open, openOK := kiwoomMagnitudeDecimal(row.Open)
	high, highOK := kiwoomMagnitudeDecimal(row.High)
	low, lowOK := kiwoomMagnitudeDecimal(row.Low)
	closeValue, closeOK := kiwoomMagnitudeDecimal(row.Close)
	volume, volumeOK := kiwoomNonNegativeDecimal(row.Volume)
	if !openOK || !highOK || !lowOK || !closeOK || !volumeOK {
		return MarketDataBar{}, time.Time{}, false
	}
	openNumber, openErr := parseDecimal(open)
	highNumber, highErr := parseDecimal(high)
	lowNumber, lowErr := parseDecimal(low)
	closeNumber, closeErr := parseDecimal(closeValue)
	if openErr != nil || highErr != nil || lowErr != nil || closeErr != nil ||
		openNumber.Sign() <= 0 || highNumber.Sign() <= 0 || lowNumber.Sign() <= 0 || closeNumber.Sign() <= 0 ||
		lowNumber.Cmp(openNumber) > 0 || lowNumber.Cmp(closeNumber) > 0 ||
		openNumber.Cmp(highNumber) > 0 || closeNumber.Cmp(highNumber) > 0 {
		return MarketDataBar{}, time.Time{}, false
	}
	return MarketDataBar{
		At: at.UTC().Format(time.RFC3339), Open: open, High: high, Low: low, Close: closeValue, Volume: volume,
	}, at, true
}
