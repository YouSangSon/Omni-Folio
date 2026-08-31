package main

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestKiwoomRealtimePricesPreserveNaiveProviderClock(t *testing.T) {
	packet, err := kiwoomRealtimeTradeRegistration("005930")
	if err != nil {
		t.Fatal(err)
	}
	wantPacket := map[string]any{
		"trnm": "REG", "grp_no": "1", "refresh": "1",
		"data": []map[string]any{{"item": []string{"005930"}, "type": []string{"0B"}}},
	}
	if !reflect.DeepEqual(packet, wantPacket) {
		t.Fatalf("packet=%#v want=%#v", packet, wantPacket)
	}

	client := newSyntheticKiwoomClient(t, KiwoomProduction, nil)
	receivedAt := time.Date(2026, 8, 24, 1, 30, 1, 123, time.UTC)
	prices, err := client.normalizeRealtimePrices([]byte(`{
		"trnm":"REAL","data":[
			{"type":"0B","name":"주식체결","item":"005930","values":{"20":"103000","10":"-001,250.00","15":"+5","9081":"2"}},
			{"type":"0B","name":"주식체결","item":"005930","values":{"20":"103000","10":"-001,250.00","15":"+5","9081":"2"}},
			{"type":"0B","name":"주식체결","item":"000660","values":{"20":"103001","10":"+200000","unknown":"ignored","9081":"1"}}
		]
	}`), receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	wantPrices := []KiwoomRealtimePrice{
		{Source: "kiwoom", Environment: KiwoomProduction, Symbol: "005930", Currency: "KRW", Price: "1250", ProviderClock: "10:30:00", ReceivedAt: "2026-08-24T01:30:01.000000123Z"},
		{Source: "kiwoom", Environment: KiwoomProduction, Symbol: "000660", Currency: "KRW", Price: "200000", ProviderClock: "10:30:01", ReceivedAt: "2026-08-24T01:30:01.000000123Z"},
	}
	if !reflect.DeepEqual(prices, wantPrices) {
		t.Fatalf("prices=%#v want=%#v", prices, wantPrices)
	}
}

func TestKiwoomRealtimePricesFailClosed(t *testing.T) {
	valid := `{"type":"0B","item":"005930","values":{"20":"103000","10":"10"}}`
	tooMany := realtimeBody(strings.TrimSuffix(strings.Repeat(valid+",", 101), ","))
	for _, test := range []struct {
		name string
		body string
		kind string
	}{
		{"control frame", `{"trnm":"REG","return_code":0}`, "invalid_response"},
		{"missing data", `{"trnm":"REAL"}`, "invalid_response"},
		{"empty data", `{"trnm":"REAL","data":[]}`, "invalid_response"},
		{"wrong type", realtimeBody(`{"type":"0D","item":"005930","values":{"20":"103000","10":"10"}}`), "invalid_realtime_price"},
		{"unsafe symbol", realtimeBody(`{"type":"0B","item":"A005930","values":{"20":"103000","10":"10"}}`), "invalid_realtime_price"},
		{"zero price", realtimeBody(`{"type":"0B","item":"005930","values":{"20":"103000","10":"0"}}`), "invalid_realtime_price"},
		{"numeric price", realtimeBody(`{"type":"0B","item":"005930","values":{"20":"103000","10":10}}`), "invalid_response"},
		{"invalid clock", realtimeBody(`{"type":"0B","item":"005930","values":{"20":"246000","10":"10"}}`), "invalid_realtime_price"},
		{"ambiguous second", realtimeBody(valid + `,{"type":"0B","item":"005930","values":{"20":"103000","10":"11"}}`), "ambiguous_realtime_price"},
		{"too many entries", tooMany, "too_many_realtime_entries"},
		{"oversized frame", string(bytes.Repeat([]byte("x"), maxBodyBytes+1)), "response_too_large"},
		{"trailing JSON", realtimeBody(valid) + `{}`, "invalid_response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newSyntheticKiwoomClient(t, KiwoomProduction, nil)
			prices, err := client.normalizeRealtimePrices([]byte(test.body), time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
			if prices != nil {
				t.Fatalf("unsafe frame returned prices: %#v", prices)
			}
			assertKiwoomErrorKind(t, err, test.kind)
		})
	}

	client := newSyntheticKiwoomClient(t, KiwoomProduction, nil)
	prices, err := client.normalizeRealtimePrices([]byte(realtimeBody(valid)), time.Time{})
	if prices != nil {
		t.Fatalf("zero receive time returned prices: %#v", prices)
	}
	assertKiwoomErrorKind(t, err, "invalid_received_at")

	if packet, err := kiwoomRealtimeTradeRegistration("00593"); packet != nil {
		t.Fatalf("invalid symbol returned registration: %#v", packet)
	} else {
		assertKiwoomErrorKind(t, err, "invalid_symbol")
	}
}

func realtimeBody(entries string) string {
	return fmt.Sprintf(`{"trnm":"REAL","data":[%s]}`, entries)
}
