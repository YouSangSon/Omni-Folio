package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestKiwoomCandlesPaginatesNormalizesAndDeduplicates(t *testing.T) {
	script := &kiwoomScript{t: t}
	script.steps = []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("synthetic-token"))
		},
		func(request *http.Request) *http.Response {
			assertKiwoomRequest(t, request, kiwoomChartPath, "ka10081")
			assertNoKiwoomCursor(t, request)
			var body map[string]string
			decodeKiwoomRequest(t, request, &body)
			want := map[string]string{"stk_cd": "005930", "base_dt": "20300101", "upd_stkpc_tp": "1"}
			if !reflect.DeepEqual(body, want) {
				t.Fatalf("daily request=%#v want=%#v", body, want)
			}
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka10081", "cont-yn": "Y", "next-key": "daily-2"}, `{
				"stk_cd":"005930","stk_dt_pole_chart_qry":[
					{"cur_prc":"-001,250.00","trde_qty":"+001000","dt":"20260824","open_pric":"+001,200","high_pric":"001300","low_pric":"-001150"},
					{"cur_prc":"001200","trde_qty":"000900","dt":"20260823","open_pric":"001100","high_pric":"001250","low_pric":"001050"}
				],"return_code":0,"return_msg":"never expose daily one"
			}`)
		},
		func(request *http.Request) *http.Response {
			assertKiwoomRequest(t, request, kiwoomChartPath, "ka10081")
			assertKiwoomCursor(t, request, "daily-2")
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka10081"}, `{
				"stk_cd":"005930","stk_dt_pole_chart_qry":[
					{"cur_prc":"001200","trde_qty":"000900","dt":"20260823","open_pric":"001100","high_pric":"001250","low_pric":"001050"},
					{"cur_prc":"+001,100.0","trde_qty":"0","dt":"20260822","open_pric":"001050","high_pric":"001150","low_pric":"001000"}
				],"return_code":0,"return_msg":"never expose daily two"
			}`)
		},
	}
	client := newSyntheticKiwoomClient(t, KiwoomProduction, script)
	series, err := client.Candles(context.Background(), "005930", "1d")
	if err != nil {
		t.Fatal(err)
	}
	if script.calls != len(script.steps) {
		t.Fatalf("requests=%d want=%d", script.calls, len(script.steps))
	}
	if series.Symbol != "005930" || series.Venue != "XKRX" || series.Timezone != "Asia/Seoul" || series.Interval != "1d" || series.PriceAdjustment != "provider_adjusted" {
		t.Fatalf("unexpected series metadata: %#v", series)
	}
	want := []MarketDataBar{
		{At: "2026-08-21T15:00:00Z", Open: "1050", High: "1150", Low: "1000", Close: "1100", Volume: "0"},
		{At: "2026-08-22T15:00:00Z", Open: "1100", High: "1250", Low: "1050", Close: "1200", Volume: "900"},
		{At: "2026-08-23T15:00:00Z", Open: "1200", High: "1300", Low: "1150", Close: "1250", Volume: "1000"},
	}
	if !reflect.DeepEqual(series.Bars, want) {
		t.Fatalf("bars=%#v want=%#v", series.Bars, want)
	}
}

func TestKiwoomCandlesMapsOfficialMinuteIntervals(t *testing.T) {
	for _, interval := range []string{"1m", "3m", "5m", "10m", "15m", "30m", "45m", "60m"} {
		t.Run(interval, func(t *testing.T) {
			calls := 0
			client := newSyntheticKiwoomClient(t, KiwoomProduction, kiwoomRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.Path == kiwoomTokenPath {
					return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("token")), nil
				}
				assertKiwoomRequest(t, request, kiwoomChartPath, "ka10080")
				var body map[string]string
				decodeKiwoomRequest(t, request, &body)
				want := map[string]string{"stk_cd": "005930", "tic_scope": interval[:len(interval)-1], "base_dt": "20300101", "upd_stkpc_tp": "1"}
				if !reflect.DeepEqual(body, want) {
					t.Fatalf("minute request=%#v want=%#v", body, want)
				}
				return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka10080"}, `{
					"stk_cd":"005930","stk_min_pole_chart_qry":[
						{"cur_prc":"100","trde_qty":"5","cntr_tm":"20260824103000","open_pric":"90","high_pric":"110","low_pric":"80"}
					],"return_code":0
				}`), nil
			}))
			series, err := client.Candles(context.Background(), "005930", interval)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 || len(series.Bars) != 1 || series.Bars[0].At != "2026-08-24T01:30:00Z" || series.Interval != interval {
				t.Fatalf("calls=%d series=%#v", calls, series)
			}
		})
	}
}

func TestKiwoomCandlesRejectsInvalidInputBeforeNetwork(t *testing.T) {
	client := newSyntheticKiwoomClient(t, KiwoomMock, kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid candle request reached the network")
		return nil, nil
	}))
	for _, input := range []struct{ symbol, interval, kind string }{
		{"A005930", "1d", "invalid_symbol"},
		{"00593", "1d", "invalid_symbol"},
		{"005930", "2m", "unsupported_interval"},
		{"005930", "", "unsupported_interval"},
	} {
		series, err := client.Candles(context.Background(), input.symbol, input.interval)
		if series != nil {
			t.Fatalf("invalid input returned series: %#v", series)
		}
		assertKiwoomErrorKind(t, err, input.kind)
	}
}

func TestKiwoomCandlesRejectsMalformedOrConflictingRows(t *testing.T) {
	tests := []struct {
		name string
		body string
		kind string
	}{
		{"missing result", `{"stk_cd":"005930","stk_dt_pole_chart_qry":[]}`, "invalid_response"},
		{"symbol mismatch", `{"stk_cd":"000660","stk_dt_pole_chart_qry":[],"return_code":0}`, "symbol_mismatch"},
		{"negative volume", candleDailyBody(`{"cur_prc":"10","trde_qty":"-1","dt":"20260824","open_pric":"9","high_pric":"11","low_pric":"8"}`), "invalid_candle"},
		{"zero price", candleDailyBody(`{"cur_prc":"0","trde_qty":"1","dt":"20260824","open_pric":"9","high_pric":"11","low_pric":"8"}`), "invalid_candle"},
		{"invalid OHLC", candleDailyBody(`{"cur_prc":"12","trde_qty":"1","dt":"20260824","open_pric":"9","high_pric":"11","low_pric":"8"}`), "invalid_candle"},
		{"invalid leap date", candleDailyBody(`{"cur_prc":"10","trde_qty":"1","dt":"20260229","open_pric":"9","high_pric":"11","low_pric":"8"}`), "invalid_candle"},
		{"exponent string", candleDailyBody(`{"cur_prc":"1e3","trde_qty":"1","dt":"20260824","open_pric":"9","high_pric":"11","low_pric":"8"}`), "invalid_candle"},
		{"JSON number", candleDailyBody(`{"cur_prc":10,"trde_qty":"1","dt":"20260824","open_pric":"9","high_pric":"11","low_pric":"8"}`), "invalid_response"},
		{"provider order", candleDailyBody(
			`{"cur_prc":"10","trde_qty":"1","dt":"20260823","open_pric":"9","high_pric":"11","low_pric":"8"},` +
				`{"cur_prc":"10","trde_qty":"1","dt":"20260824","open_pric":"9","high_pric":"11","low_pric":"8"}`), "invalid_candle_order"},
		{"conflicting duplicate", candleDailyBody(
			`{"cur_prc":"10","trde_qty":"1","dt":"20260824","open_pric":"9","high_pric":"11","low_pric":"8"},` +
				`{"cur_prc":"10.5","trde_qty":"1","dt":"20260824","open_pric":"9","high_pric":"11","low_pric":"8"}`), "conflicting_candle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := onePageCandleClient(t, test.body)
			series, err := client.Candles(context.Background(), "005930", "1d")
			if series != nil {
				t.Fatalf("malformed response returned partial series: %#v", series)
			}
			assertKiwoomErrorKind(t, err, test.kind)
		})
	}

	client := onePageCandleClient(t, `{"stk_cd":"005930","stk_dt_pole_chart_qry":[],"return_code":0}`)
	series, err := client.Candles(context.Background(), "005930", "1d")
	if series != nil || !errors.Is(err, errMarketDataNotFound) {
		t.Fatalf("empty result series=%#v err=%v", series, err)
	}
}

func TestKiwoomCandlesCapsNewestBarsAtFiveHundred(t *testing.T) {
	rows := make([]map[string]string, 0, maxMarketDataRows+1)
	newest := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < maxMarketDataRows+1; i++ {
		rows = append(rows, map[string]string{
			"cur_prc": "10", "trde_qty": "1", "dt": newest.AddDate(0, 0, -i).Format("20060102"),
			"open_pric": "9", "high_pric": "11", "low_pric": "8",
		})
	}
	payload, err := json.Marshal(map[string]any{"stk_cd": "005930", "stk_dt_pole_chart_qry": rows, "return_code": 0})
	if err != nil {
		t.Fatal(err)
	}
	series, err := onePageCandleClient(t, string(payload)).Candles(context.Background(), "005930", "1d")
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Bars) != maxMarketDataRows || series.Bars[0].At != "2026-08-19T15:00:00Z" || series.Bars[len(series.Bars)-1].At != "2027-12-31T15:00:00Z" {
		t.Fatalf("unexpected capped range: count=%d first=%s last=%s", len(series.Bars), series.Bars[0].At, series.Bars[len(series.Bars)-1].At)
	}
}

func TestKiwoomCandlesChecksOverlapPageAfterReachingCap(t *testing.T) {
	rows := make([]map[string]string, 0, maxMarketDataRows)
	newest := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < maxMarketDataRows; i++ {
		rows = append(rows, map[string]string{
			"cur_prc": "10", "trde_qty": "1", "dt": newest.AddDate(0, 0, -i).Format("20060102"),
			"open_pric": "9", "high_pric": "11", "low_pric": "8",
		})
	}
	firstPage, err := json.Marshal(map[string]any{"stk_cd": "005930", "stk_dt_pole_chart_qry": rows, "return_code": 0})
	if err != nil {
		t.Fatal(err)
	}
	boundaryDate := newest.AddDate(0, 0, -(maxMarketDataRows - 1)).Format("20060102")
	secondPage := candleDailyBody(`{"cur_prc":"10.5","trde_qty":"1","dt":"` + boundaryDate + `","open_pric":"9","high_pric":"11","low_pric":"8"}`)
	script := &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("token"))
		},
		func(request *http.Request) *http.Response {
			assertNoKiwoomCursor(t, request)
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka10081", "cont-yn": "Y", "next-key": "overlap-page"}, string(firstPage))
		},
		func(request *http.Request) *http.Response {
			assertKiwoomCursor(t, request, "overlap-page")
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka10081"}, secondPage)
		},
	}}
	series, err := newSyntheticKiwoomClient(t, KiwoomProduction, script).Candles(context.Background(), "005930", "1d")
	if series != nil || script.calls != 3 {
		t.Fatalf("boundary conflict returned series=%t calls=%d err=%v", series != nil, script.calls, err)
	}
	assertKiwoomErrorKind(t, err, "conflicting_candle")
}

func candleDailyBody(rows string) string {
	return fmt.Sprintf(`{"stk_cd":"005930","stk_dt_pole_chart_qry":[%s],"return_code":0}`, rows)
}

func onePageCandleClient(t *testing.T, body string) *KiwoomClient {
	t.Helper()
	return newSyntheticKiwoomClient(t, KiwoomProduction, &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("token"))
		},
		func(request *http.Request) *http.Response {
			assertKiwoomRequest(t, request, kiwoomChartPath, "ka10081")
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka10081"}, body)
		},
	}})
}
