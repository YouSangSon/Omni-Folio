package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestK2B1DatedExecutionScanPaginatesAndNormalizesWithoutCompletenessClaim(t *testing.T) {
	const rawAccount = "9876543210"
	const rawOrder = "0000050"
	script := &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("synthetic-token"))
		},
		func(request *http.Request) *http.Response {
			assertKiwoomRequest(t, request, kiwoomAccountPath, "ka00001")
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka00001"}, `{"acctNo":"`+rawAccount+`","return_code":0}`)
		},
		func(request *http.Request) *http.Response {
			assertK2B1ExecutionRequest(t, request, "")
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "kt00009", "cont-yn": "Y", "next-key": "execution-page-2"}, `{
				"acnt_ord_cntr_prst_array":[{
					"stk_bond_tp":"1","ord_no":"`+rawOrder+`","stk_cd":"A005930","trde_tp":"시장가","io_tp_nm":"현금매수",
					"ord_qty":"0000000010","ord_uv":"+001,000.00","cntr_no":"0000002","cntr_qty":"0000000002",
					"cntr_uv":"+001,002.0","cntr_tm":"13:08:00","dmst_stex_tp":"KRX"
				}],"return_code":0,"return_msg":"never expose page one"
			}`)
		},
		func(request *http.Request) *http.Response {
			assertK2B1ExecutionRequest(t, request, "execution-page-2")
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "kt00009"}, `{
				"acnt_ord_cntr_prst_array":[{
					"stk_bond_tp":"1","ord_no":"`+rawOrder+`","stk_cd":"A005930","trde_tp":"시장가","io_tp_nm":"현금매수",
					"ord_qty":"0000000010","ord_uv":"+001,000.00","cntr_no":"0000001","cntr_qty":"0000000003",
					"cntr_uv":"+001,001.0","cntr_tm":"13:07:47","dmst_stex_tp":"KRX"
				}],"return_code":0,"return_msg":"never expose page two"
			}`)
		},
	}}
	client := newSyntheticKiwoomClient(t, KiwoomProduction, script)

	scan, err := client.scanDatedExecutions(context.Background(), "2026-08-24")
	if err != nil {
		t.Fatal(err)
	}
	if script.calls != len(script.steps) {
		t.Fatalf("requests=%d want=%d", script.calls, len(script.steps))
	}
	if scan.Provider != "kiwoom" || scan.Environment != KiwoomProduction || scan.RequestedOrderDate != "2026-08-24" ||
		scan.FetchedAt != "2030-01-01T00:00:00Z" || !scan.PaginationComplete || scan.ExecutionsComplete {
		t.Fatalf("unexpected scan metadata: %#v", scan)
	}
	if !strings.HasPrefix(scan.AccountRef, "kiwoom_account_") || len(scan.Rows) != 2 {
		t.Fatalf("unexpected scan identity or rows: %#v", scan)
	}
	wantRows := []struct {
		clock, side, symbol, orderQuantity, orderPrice, fillQuantity, fillPrice string
	}{
		{"13:07:47", "BUY", "005930", "10", "1000", "3", "1001"},
		{"13:08:00", "BUY", "005930", "10", "1000", "2", "1002"},
	}
	for index, want := range wantRows {
		row := scan.Rows[index]
		got := []string{row.ExecutionClock, row.Side, row.Symbol, row.OrderQuantity, row.OrderPrice, row.FillQuantity, row.FillPrice}
		expected := []string{want.clock, want.side, want.symbol, want.orderQuantity, want.orderPrice, want.fillQuantity, want.fillPrice}
		if !reflect.DeepEqual(got, expected) || row.Exchange != "KRX" || row.ProviderOrderType != "시장가" ||
			!strings.HasPrefix(row.DatedOrderRef, "kiwoom_dated_order_") ||
			!strings.HasPrefix(row.DatedExecutionRef, "kiwoom_dated_execution_") {
			t.Fatalf("row[%d]=%#v", index, row)
		}
	}
	if scan.Rows[0].DatedOrderRef != scan.Rows[1].DatedOrderRef || scan.Rows[0].DatedExecutionRef == scan.Rows[1].DatedExecutionRef {
		t.Fatalf("aliases did not preserve order grouping and execution identity: %#v", scan.Rows)
	}
	encoded, err := json.Marshal(scan)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{rawAccount, rawOrder, "0000001", "0000002", "never expose"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("scan leaked raw provider value %q: %s", forbidden, encoded)
		}
	}
}

func TestK2B1DatedExecutionScanRejectsDuplicateExecutionRows(t *testing.T) {
	row := `{
		"stk_bond_tp":"1","ord_no":"0000050","stk_cd":"A005930","trde_tp":"시장가","io_tp_nm":"현금매수",
		"ord_qty":"0000000010","ord_uv":"0000001000","cntr_no":"0000001","cntr_qty":"0000000003",
		"cntr_uv":"0000001001","cntr_tm":"13:07:47","dmst_stex_tp":"KRX"
	}`
	client := k2b1OnePageExecutionClient(t, `{"acnt_ord_cntr_prst_array":[`+row+`,`+row+`],"return_code":0}`)

	scan, err := client.scanDatedExecutions(context.Background(), "2026-08-24")
	if scan != nil {
		t.Fatalf("duplicate execution returned a partial scan: %#v", scan)
	}
	assertKiwoomErrorKind(t, err, "duplicate_execution")
}

func TestK2B1DatedExecutionScanRejectsConflictingOrderRows(t *testing.T) {
	body := `{"acnt_ord_cntr_prst_array":[
		{"stk_bond_tp":"1","ord_no":"0000050","stk_cd":"A005930","trde_tp":"시장가","io_tp_nm":"현금매수","ord_qty":"10","ord_uv":"1000","cntr_no":"0000001","cntr_qty":"3","cntr_uv":"1001","cntr_tm":"13:07:47","dmst_stex_tp":"KRX"},
		{"stk_bond_tp":"1","ord_no":"0000050","stk_cd":"A000660","trde_tp":"시장가","io_tp_nm":"현금매수","ord_qty":"10","ord_uv":"1000","cntr_no":"0000002","cntr_qty":"2","cntr_uv":"1002","cntr_tm":"13:08:00","dmst_stex_tp":"KRX"}
	],"return_code":0}`
	client := k2b1OnePageExecutionClient(t, body)

	scan, err := client.scanDatedExecutions(context.Background(), "2026-08-24")
	if scan != nil {
		t.Fatalf("conflicting order rows returned a partial scan: %#v", scan)
	}
	assertKiwoomErrorKind(t, err, "conflicting_order")
}

func TestK2B1DatedExecutionScanRejectsConflictingProviderOrderTypes(t *testing.T) {
	body := `{"acnt_ord_cntr_prst_array":[
		{"stk_bond_tp":"1","ord_no":"0000050","stk_cd":"A005930","trde_tp":"시장가","io_tp_nm":"현금매수","ord_qty":"10","ord_uv":"0","cntr_no":"0000001","cntr_qty":"3","cntr_uv":"1001","cntr_tm":"13:07:47","dmst_stex_tp":"KRX"},
		{"stk_bond_tp":"1","ord_no":"0000050","stk_cd":"A005930","trde_tp":"보통","io_tp_nm":"현금매수","ord_qty":"10","ord_uv":"0","cntr_no":"0000002","cntr_qty":"2","cntr_uv":"1002","cntr_tm":"13:08:00","dmst_stex_tp":"KRX"}
	],"return_code":0}`
	client := k2b1OnePageExecutionClient(t, body)

	scan, err := client.scanDatedExecutions(context.Background(), "2026-08-24")
	if scan != nil {
		t.Fatalf("conflicting provider order types returned a scan: %#v", scan)
	}
	assertKiwoomErrorKind(t, err, "conflicting_order")
}

func TestK2B1DatedExecutionScanRejectsFillAboveOrderQuantity(t *testing.T) {
	body := `{"acnt_ord_cntr_prst_array":[{
		"stk_bond_tp":"1","ord_no":"0000050","stk_cd":"A005930","trde_tp":"시장가","io_tp_nm":"현금매수",
		"ord_qty":"2","ord_uv":"1000","cntr_no":"0000001","cntr_qty":"3",
		"cntr_uv":"1001","cntr_tm":"13:07:47","dmst_stex_tp":"KRX"
	}],"return_code":0}`
	client := k2b1OnePageExecutionClient(t, body)

	scan, err := client.scanDatedExecutions(context.Background(), "2026-08-24")
	if scan != nil {
		t.Fatalf("oversized fill returned a scan: %#v", scan)
	}
	assertKiwoomErrorKind(t, err, "invalid_execution")
}

func TestK2B1DatedExecutionScanRejectsCumulativeFillsAboveOrderQuantity(t *testing.T) {
	body := `{"acnt_ord_cntr_prst_array":[
		{"stk_bond_tp":"1","ord_no":"0000050","stk_cd":"A005930","trde_tp":"시장가","io_tp_nm":"현금매수","ord_qty":"5","ord_uv":"1000","cntr_no":"0000001","cntr_qty":"3","cntr_uv":"1001","cntr_tm":"13:07:47","dmst_stex_tp":"KRX"},
		{"stk_bond_tp":"1","ord_no":"0000050","stk_cd":"A005930","trde_tp":"시장가","io_tp_nm":"현금매수","ord_qty":"5","ord_uv":"1000","cntr_no":"0000002","cntr_qty":"3","cntr_uv":"1002","cntr_tm":"13:08:00","dmst_stex_tp":"KRX"}
	],"return_code":0}`
	client := k2b1OnePageExecutionClient(t, body)

	scan, err := client.scanDatedExecutions(context.Background(), "2026-08-24")
	if scan != nil {
		t.Fatalf("oversized cumulative fills returned a scan: %#v", scan)
	}
	assertKiwoomErrorKind(t, err, "invalid_execution")
}

func TestK2B1DatedExecutionScanRejectsMissingExecutionArray(t *testing.T) {
	client := k2b1OnePageExecutionClient(t, `{"return_code":0}`)

	scan, err := client.scanDatedExecutions(context.Background(), "2026-08-24")
	if scan != nil {
		t.Fatalf("missing execution array returned a scan: %#v", scan)
	}
	assertKiwoomErrorKind(t, err, "invalid_response")
}

func TestK2B1DatedExecutionScanRejectsInvalidDatesBeforeNetwork(t *testing.T) {
	client := newSyntheticKiwoomClient(t, KiwoomProduction, kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid date reached the network")
		return nil, nil
	}))
	for _, date := range []string{"", "2026-02-29", "20260824", "2026-8-24"} {
		t.Run(date, func(t *testing.T) {
			scan, err := client.scanDatedExecutions(context.Background(), date)
			if scan != nil {
				t.Fatalf("invalid date returned a scan: %#v", scan)
			}
			assertKiwoomErrorKind(t, err, "invalid_order_date")
		})
	}
}

func TestK2B1DatedExecutionScanRejectsNilClient(t *testing.T) {
	var client *KiwoomClient
	scan, err := client.scanDatedExecutions(context.Background(), "2026-08-24")
	if scan != nil {
		t.Fatalf("nil client returned a scan: %#v", scan)
	}
	assertKiwoomErrorKind(t, err, "invalid_config")
}

func TestK2B1DatedExecutionScanRejectsAccountWithoutReturnCode(t *testing.T) {
	script := &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("synthetic-token"))
		},
		func(request *http.Request) *http.Response {
			assertKiwoomRequest(t, request, kiwoomAccountPath, "ka00001")
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka00001"}, `{"acctNo":"9876543210"}`)
		},
	}}
	client := newSyntheticKiwoomClient(t, KiwoomProduction, script)

	scan, err := client.scanDatedExecutions(context.Background(), "2026-08-24")
	if scan != nil {
		t.Fatalf("account response without return_code returned a scan: %#v", scan)
	}
	assertKiwoomErrorKind(t, err, "invalid_response")
}

func TestK2B1DatedExecutionScanValidatesResponseEnvelope(t *testing.T) {
	tests := []struct {
		name, body, kind string
	}{
		{"missing return code", `{"acnt_ord_cntr_prst_array":[]}`, "invalid_response"},
		{"wrong field type", `{"acnt_ord_cntr_prst_array":[{"ord_no":50}],"return_code":0}`, "invalid_response"},
		{"provider rejection", `{"acnt_ord_cntr_prst_array":[],"return_code":1234}`, "provider_rejected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := k2b1OnePageExecutionClient(t, test.body)
			scan, err := client.scanDatedExecutions(context.Background(), "2026-08-24")
			if scan != nil {
				t.Fatalf("invalid response returned a scan: %#v", scan)
			}
			assertKiwoomErrorKind(t, err, test.kind)
		})
	}
}

func TestK2B1DatedExecutionScanAcceptsExplicitEmptyArray(t *testing.T) {
	client := k2b1OnePageExecutionClient(t, `{"acnt_ord_cntr_prst_array":[],"return_code":0}`)

	scan, err := client.scanDatedExecutions(context.Background(), "2026-08-24")
	if err != nil {
		t.Fatal(err)
	}
	if scan == nil || scan.Rows == nil || len(scan.Rows) != 0 || !scan.PaginationComplete || scan.ExecutionsComplete {
		t.Fatalf("unexpected empty scan: %#v", scan)
	}
}

func TestK2B1DatedExecutionScanDiscardsEarlierPagesOnLaterTransportFailure(t *testing.T) {
	script := &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("synthetic-token"))
		},
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka00001"}, `{"acctNo":"9876543210","return_code":0}`)
		},
		func(request *http.Request) *http.Response {
			assertK2B1ExecutionRequest(t, request, "")
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "kt00009", "cont-yn": "Y", "next-key": "page-2"}, `{
				"acnt_ord_cntr_prst_array":[{"stk_bond_tp":"1","ord_no":"0000050","stk_cd":"A005930","trde_tp":"시장가","io_tp_nm":"현금매수","ord_qty":"5","ord_uv":"1000","cntr_no":"0000001","cntr_qty":"3","cntr_uv":"1001","cntr_tm":"13:07:47","dmst_stex_tp":"KRX"}],
				"return_code":0
			}`)
		},
		func(request *http.Request) *http.Response {
			assertK2B1ExecutionRequest(t, request, "page-2")
			return kiwoomResponse(http.StatusTooManyRequests, map[string]string{"api-id": "kt00009"}, `{"return_code":1700,"return_msg":"9876543210 0000050 synthetic-token"}`)
		},
	}}
	client := newSyntheticKiwoomClient(t, KiwoomProduction, script)

	scan, err := client.scanDatedExecutions(context.Background(), "2026-08-24")
	if scan != nil {
		t.Fatalf("later page failure returned partial data: %#v", scan)
	}
	assertKiwoomErrorKind(t, err, "rate_limited")
	for _, forbidden := range []string{"9876543210", "0000050", "synthetic-token"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("transport error leaked %q: %v", forbidden, err)
		}
	}
}

func TestK2B1DatedExecutionNormalizerRejectsUnsafeRows(t *testing.T) {
	base := kiwoomDatedExecutionProviderRow{
		StockBondType: "1", OrderNumber: "0000050", StockCode: "A005930", OrderType: "시장가", Side: "현금매수",
		OrderQuantity: "5", OrderPrice: "0", ExecutionNumber: "0000001", FillQuantity: "3",
		FillPrice: "1001", ExecutionClock: "13:07:47", Exchange: "KRX",
	}
	tests := []struct {
		name   string
		mutate func(*kiwoomDatedExecutionProviderRow)
	}{
		{"bond row", func(row *kiwoomDatedExecutionProviderRow) { row.StockBondType = "2" }},
		{"ELW prefix", func(row *kiwoomDatedExecutionProviderRow) { row.StockCode = "J005930" }},
		{"ETN prefix", func(row *kiwoomDatedExecutionProviderRow) { row.StockCode = "Q005930" }},
		{"missing provider order type", func(row *kiwoomDatedExecutionProviderRow) { row.OrderType = "" }},
		{"short order id", func(row *kiwoomDatedExecutionProviderRow) { row.OrderNumber = "000050" }},
		{"zero order id", func(row *kiwoomDatedExecutionProviderRow) { row.OrderNumber = "0000000" }},
		{"nonnumeric execution id", func(row *kiwoomDatedExecutionProviderRow) { row.ExecutionNumber = "00000A1" }},
		{"zero execution id", func(row *kiwoomDatedExecutionProviderRow) { row.ExecutionNumber = "0000000" }},
		{"invalid symbol", func(row *kiwoomDatedExecutionProviderRow) { row.StockCode = "A05930" }},
		{"unsupported exchange", func(row *kiwoomDatedExecutionProviderRow) { row.Exchange = "NXT" }},
		{"unsupported side", func(row *kiwoomDatedExecutionProviderRow) { row.Side = "신용매수" }},
		{"zero order quantity", func(row *kiwoomDatedExecutionProviderRow) { row.OrderQuantity = "0" }},
		{"fractional fill quantity", func(row *kiwoomDatedExecutionProviderRow) { row.FillQuantity = "1.5" }},
		{"negative order price", func(row *kiwoomDatedExecutionProviderRow) { row.OrderPrice = "-1" }},
		{"zero fill price", func(row *kiwoomDatedExecutionProviderRow) { row.FillPrice = "0" }},
		{"invalid execution clock", func(row *kiwoomDatedExecutionProviderRow) { row.ExecutionClock = "23:59:60" }},
	}
	client := newSyntheticKiwoomClient(t, KiwoomProduction, http.DefaultTransport)
	if row, ok := client.normalizeDatedExecution("9876543210", "2026-08-24", base); !ok || row.OrderPrice != "0" {
		t.Fatalf("valid market-price row was rejected: %#v, %v", row, ok)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := base
			test.mutate(&row)
			if normalized, ok := client.normalizeDatedExecution("9876543210", "2026-08-24", row); ok {
				t.Fatalf("unsafe row normalized: %#v", normalized)
			}
		})
	}
}

func TestK2B1DatedExecutionAliasesAreDateAndEnvironmentScoped(t *testing.T) {
	raw := kiwoomDatedExecutionProviderRow{
		StockBondType: "1", OrderNumber: "0000050", StockCode: "A005930", OrderType: "시장가", Side: "현금매도",
		OrderQuantity: "5", OrderPrice: "1000", ExecutionNumber: "0000001", FillQuantity: "3",
		FillPrice: "1001", ExecutionClock: "13:07:47", Exchange: "KRX",
	}
	production := newSyntheticKiwoomClient(t, KiwoomProduction, http.DefaultTransport)
	mock := newSyntheticKiwoomClient(t, KiwoomMock, http.DefaultTransport)
	dayOne, ok := production.normalizeDatedExecution("9876543210", "2026-08-24", raw)
	if !ok {
		t.Fatal("valid production row was rejected")
	}
	dayTwo, _ := production.normalizeDatedExecution("9876543210", "2026-08-25", raw)
	mockDayOne, _ := mock.normalizeDatedExecution("9876543210", "2026-08-24", raw)
	otherAccount, _ := production.normalizeDatedExecution("0123456789", "2026-08-24", raw)
	for _, candidate := range []KiwoomDatedExecutionRow{dayTwo, mockDayOne, otherAccount} {
		if candidate.DatedOrderRef == dayOne.DatedOrderRef || candidate.DatedExecutionRef == dayOne.DatedExecutionRef {
			t.Fatalf("dated aliases escaped their date/environment scope: %#v %#v", dayOne, candidate)
		}
	}
	if orderAlias(dayOne.DatedOrderRef, "order") || orderAlias(dayOne.DatedExecutionRef, "execution") {
		t.Fatalf("dated aliases can be consumed as durable order-event aliases: %#v", dayOne)
	}
}

func assertK2B1ExecutionRequest(t *testing.T, request *http.Request, cursor string) {
	t.Helper()
	assertKiwoomRequest(t, request, kiwoomAccountPath, "kt00009")
	if cursor == "" {
		assertNoKiwoomCursor(t, request)
	} else {
		assertKiwoomCursor(t, request, cursor)
	}
	var body map[string]string
	decodeKiwoomRequest(t, request, &body)
	want := map[string]string{
		"ord_dt": "20260824", "stk_bond_tp": "1", "mrkt_tp": "0", "sell_tp": "0",
		"qry_tp": "1", "stk_cd": "", "fr_ord_no": "", "dmst_stex_tp": "KRX",
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("kt00009 request=%#v want=%#v", body, want)
	}
}

func k2b1OnePageExecutionClient(t *testing.T, body string) *KiwoomClient {
	t.Helper()
	return newSyntheticKiwoomClient(t, KiwoomProduction, &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("synthetic-token"))
		},
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka00001"}, `{"acctNo":"9876543210","return_code":0}`)
		},
		func(request *http.Request) *http.Response {
			assertKiwoomRequest(t, request, kiwoomAccountPath, "kt00009")
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "kt00009"}, body)
		},
	}})
}
