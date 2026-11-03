package main

import (
	"context"
	"math/big"
	"sort"
	"strings"
	"time"
)

type KiwoomDatedExecutionScan struct {
	Provider           string                    `json:"provider"`
	Environment        KiwoomEnvironment         `json:"environment"`
	AccountRef         string                    `json:"account_ref"`
	RequestedOrderDate string                    `json:"requested_order_date"`
	FetchedAt          string                    `json:"fetched_at"`
	PaginationComplete bool                      `json:"pagination_complete"`
	ExecutionsComplete bool                      `json:"executions_complete"`
	Rows               []KiwoomDatedExecutionRow `json:"rows"`
}

type KiwoomDatedExecutionRow struct {
	DatedOrderRef     string `json:"dated_order_ref"`
	DatedExecutionRef string `json:"dated_execution_ref"`
	Symbol            string `json:"symbol"`
	Exchange          string `json:"exchange"`
	Side              string `json:"side"`
	ProviderOrderType string `json:"provider_order_type"`
	OrderQuantity     string `json:"order_quantity"`
	OrderPrice        string `json:"order_price"`
	FillQuantity      string `json:"fill_quantity"`
	FillPrice         string `json:"fill_price"`
	ExecutionClock    string `json:"execution_clock"`
}

type kiwoomDatedExecutionResponse struct {
	kiwoomResult
	Rows *[]kiwoomDatedExecutionProviderRow `json:"acnt_ord_cntr_prst_array"`
}

type kiwoomDatedExecutionProviderRow struct {
	StockBondType   string `json:"stk_bond_tp"`
	OrderNumber     string `json:"ord_no"`
	StockCode       string `json:"stk_cd"`
	OrderType       string `json:"trde_tp"`
	Side            string `json:"io_tp_nm"`
	OrderQuantity   string `json:"ord_qty"`
	OrderPrice      string `json:"ord_uv"`
	ExecutionNumber string `json:"cntr_no"`
	FillQuantity    string `json:"cntr_qty"`
	FillPrice       string `json:"cntr_uv"`
	ExecutionClock  string `json:"cntr_tm"`
	Exchange        string `json:"dmst_stex_tp"`
}

type kiwoomDatedOrderIdentity struct {
	Symbol, Exchange, Side, ProviderOrderType, Quantity, Price string
}

func (c *KiwoomClient) scanDatedExecutions(ctx context.Context, requestedOrderDate string) (*KiwoomDatedExecutionScan, error) {
	if c == nil {
		return nil, &KiwoomError{Kind: "invalid_config"}
	}
	date, err := time.Parse("2006-01-02", requestedOrderDate)
	if err != nil || date.Format("2006-01-02") != requestedOrderDate {
		return nil, &KiwoomError{Kind: "invalid_order_date", APIID: "kt00009"}
	}
	rawAccount, err := c.accountNumber(ctx)
	if err != nil {
		return nil, err
	}
	requestBody := map[string]string{
		"ord_dt": date.Format("20060102"), "stk_bond_tp": "1", "mrkt_tp": "0", "sell_tp": "0",
		"qry_tp": "1", "stk_cd": "", "fr_ord_no": "", "dmst_stex_tp": "KRX",
	}
	rows := make([]KiwoomDatedExecutionRow, 0)
	seenExecutions := make(map[string]struct{})
	orders := make(map[string]kiwoomDatedOrderIdentity)
	filledByOrder := make(map[string]*big.Rat)
	err = c.readPages(ctx, "kt00009", requestBody, func(body []byte) (bool, error) {
		var response kiwoomDatedExecutionResponse
		if err := kiwoomDecode(body, &response); err != nil || response.Rows == nil {
			return false, &KiwoomError{Kind: "invalid_response", APIID: "kt00009"}
		}
		if err := kiwoomCheckResult("kt00009", response.kiwoomResult); err != nil {
			return false, err
		}
		for _, providerRow := range *response.Rows {
			row, ok := c.normalizeDatedExecution(rawAccount, requestedOrderDate, providerRow)
			if !ok {
				return false, &KiwoomError{Kind: "invalid_execution", APIID: "kt00009"}
			}
			if _, duplicate := seenExecutions[row.DatedExecutionRef]; duplicate {
				return false, &KiwoomError{Kind: "duplicate_execution", APIID: "kt00009"}
			}
			identity := kiwoomDatedOrderIdentity{
				Symbol: row.Symbol, Exchange: row.Exchange, Side: row.Side, ProviderOrderType: row.ProviderOrderType,
				Quantity: row.OrderQuantity, Price: row.OrderPrice,
			}
			if previous, exists := orders[row.DatedOrderRef]; exists && previous != identity {
				return false, &KiwoomError{Kind: "conflicting_order", APIID: "kt00009"}
			}
			fillQuantity, _ := parseDecimal(row.FillQuantity)
			cumulativeFill := new(big.Rat).Set(fillQuantity)
			if previous := filledByOrder[row.DatedOrderRef]; previous != nil {
				cumulativeFill.Add(cumulativeFill, previous)
			}
			orderQuantity, _ := parseDecimal(row.OrderQuantity)
			if cumulativeFill.Cmp(orderQuantity) > 0 {
				return false, &KiwoomError{Kind: "invalid_execution", APIID: "kt00009"}
			}
			seenExecutions[row.DatedExecutionRef] = struct{}{}
			orders[row.DatedOrderRef] = identity
			filledByOrder[row.DatedOrderRef] = cumulativeFill
			rows = append(rows, row)
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].ExecutionClock+"\x00"+rows[i].DatedExecutionRef < rows[j].ExecutionClock+"\x00"+rows[j].DatedExecutionRef
	})
	return &KiwoomDatedExecutionScan{
		Provider: "kiwoom", Environment: c.environment, AccountRef: c.alias("account", rawAccount),
		RequestedOrderDate: requestedOrderDate, FetchedAt: c.now().UTC().Format(time.RFC3339Nano),
		PaginationComplete: true, ExecutionsComplete: false, Rows: rows,
	}, nil
}

func (c *KiwoomClient) normalizeDatedExecution(rawAccount, requestedOrderDate string, raw kiwoomDatedExecutionProviderRow) (KiwoomDatedExecutionRow, bool) {
	if raw.StockBondType != "1" || raw.Exchange != "KRX" || len(raw.StockCode) != 7 || raw.StockCode[0] != 'A' ||
		!kiwoomSevenDigitID(raw.OrderNumber) || !kiwoomSevenDigitID(raw.ExecutionNumber) {
		return KiwoomDatedExecutionRow{}, false
	}
	symbol, symbolOK := kiwoomStockCode(raw.StockCode)
	providerOrderType, orderTypeOK := kiwoomText(raw.OrderType)
	orderQuantity, orderQuantityOK := kiwoomNonNegativeDecimal(raw.OrderQuantity)
	orderPrice, orderPriceOK := kiwoomNonNegativeDecimal(raw.OrderPrice)
	fillQuantity, fillQuantityOK := kiwoomNonNegativeDecimal(raw.FillQuantity)
	fillPrice, fillPriceOK := kiwoomNonNegativeDecimal(raw.FillPrice)
	clock, clockOK := kiwoomExecutionClock(raw.ExecutionClock)
	side := ""
	switch strings.TrimSpace(raw.Side) {
	case "현금매수":
		side = "BUY"
	case "현금매도":
		side = "SELL"
	}
	if !symbolOK || !orderTypeOK || !orderQuantityOK || !validOrderInteger(orderQuantity) || !orderPriceOK ||
		!fillQuantityOK || !validOrderInteger(fillQuantity) || !fillPriceOK || !positiveCanonicalDecimal(fillPrice) || !clockOK || side == "" {
		return KiwoomDatedExecutionRow{}, false
	}
	orderQuantityValue, _ := parseDecimal(orderQuantity)
	fillQuantityValue, _ := parseDecimal(fillQuantity)
	if fillQuantityValue.Cmp(orderQuantityValue) > 0 {
		return KiwoomDatedExecutionRow{}, false
	}
	orderKey := rawAccount + "\x00" + requestedOrderDate + "\x00" + raw.OrderNumber
	executionKey := orderKey + "\x00" + raw.ExecutionNumber
	return KiwoomDatedExecutionRow{
		DatedOrderRef: c.alias("dated_order", orderKey), DatedExecutionRef: c.alias("dated_execution", executionKey),
		Symbol: symbol, Exchange: "KRX", Side: side, ProviderOrderType: providerOrderType,
		OrderQuantity: orderQuantity, OrderPrice: orderPrice,
		FillQuantity: fillQuantity, FillPrice: fillPrice, ExecutionClock: clock,
	}, true
}

func kiwoomSevenDigitID(raw string) bool {
	if len(raw) != 7 {
		return false
	}
	for index := range raw {
		if raw[index] < '0' || raw[index] > '9' {
			return false
		}
	}
	return raw != "0000000"
}

func kiwoomExecutionClock(raw string) (string, bool) {
	if len(raw) != len("15:04:05") {
		return "", false
	}
	parsed, err := time.Parse("15:04:05", raw)
	return raw, err == nil && parsed.Format("15:04:05") == raw
}
