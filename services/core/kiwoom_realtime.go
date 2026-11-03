package main

import "time"

const kiwoomMaxRealtimeEntries = 100

type KiwoomRealtimePrice struct {
	Source        string            `json:"source"`
	Environment   KiwoomEnvironment `json:"environment"`
	Symbol        string            `json:"symbol"`
	Currency      string            `json:"currency"`
	Price         string            `json:"price"`
	ProviderClock string            `json:"provider_clock"`
	ReceivedAt    string            `json:"received_at"`
}

type kiwoomRealtimeMessage struct {
	TransactionName string                 `json:"trnm"`
	Data            *[]kiwoomRealtimeEntry `json:"data"`
}

type kiwoomRealtimeEntry struct {
	Type   string            `json:"type"`
	Item   string            `json:"item"`
	Values map[string]string `json:"values"`
}

func kiwoomRealtimeTradeRegistration(symbol string) (map[string]any, error) {
	if !kiwoomStockPattern.MatchString(symbol) {
		return nil, &KiwoomError{Kind: "invalid_symbol", APIID: "0B"}
	}
	return map[string]any{
		"trnm": "REG", "grp_no": "1", "refresh": "1",
		"data": []map[string]any{{"item": []string{symbol}, "type": []string{"0B"}}},
	}, nil
}

func (c *KiwoomClient) normalizeRealtimePrices(message []byte, receivedAt time.Time) ([]KiwoomRealtimePrice, error) {
	if c == nil {
		return nil, &KiwoomError{Kind: "invalid_config", APIID: "0B"}
	}
	received := receivedAt.UTC().Format(time.RFC3339Nano)
	if receivedAt.IsZero() || !canonicalUTCString(received) {
		return nil, &KiwoomError{Kind: "invalid_received_at", APIID: "0B"}
	}
	if len(message) > maxBodyBytes {
		return nil, &KiwoomError{Kind: "response_too_large", APIID: "0B"}
	}
	var response kiwoomRealtimeMessage
	if err := kiwoomDecode(message, &response); err != nil || response.TransactionName != "REAL" || response.Data == nil || len(*response.Data) == 0 {
		return nil, &KiwoomError{Kind: "invalid_response", APIID: "0B"}
	}
	if len(*response.Data) > kiwoomMaxRealtimeEntries {
		return nil, &KiwoomError{Kind: "too_many_realtime_entries", APIID: "0B"}
	}
	prices := make([]KiwoomRealtimePrice, 0, len(*response.Data))
	seen := make(map[string]string, len(*response.Data))
	for _, entry := range *response.Data {
		price, priceOK := kiwoomMagnitudeDecimal(entry.Values["10"])
		clock := entry.Values["20"]
		if entry.Type != "0B" || !kiwoomStockPattern.MatchString(entry.Item) || !priceOK || !positiveCanonicalDecimal(price) || !kiwoomValidTime(clock) {
			return nil, &KiwoomError{Kind: "invalid_realtime_price", APIID: "0B"}
		}
		providerClock := clock[:2] + ":" + clock[2:4] + ":" + clock[4:]
		key := entry.Item + "\x00" + providerClock
		if previous, duplicate := seen[key]; duplicate {
			if previous != price {
				return nil, &KiwoomError{Kind: "ambiguous_realtime_price", APIID: "0B"}
			}
			continue
		}
		seen[key] = price
		prices = append(prices, KiwoomRealtimePrice{
			Source: "kiwoom", Environment: c.environment,
			Symbol: entry.Item, Currency: "KRW", Price: price,
			ProviderClock: providerClock, ReceivedAt: received,
		})
	}
	return prices, nil
}
