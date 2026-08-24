package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type kiwoomRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn kiwoomRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type kiwoomScript struct {
	t     *testing.T
	mu    sync.Mutex
	steps []func(*http.Request) *http.Response
	calls int
}

func (script *kiwoomScript) RoundTrip(request *http.Request) (*http.Response, error) {
	script.mu.Lock()
	defer script.mu.Unlock()
	if script.calls >= len(script.steps) {
		script.t.Fatalf("unexpected Kiwoom request %d: %s", script.calls+1, request.URL.Path)
	}
	step := script.steps[script.calls]
	script.calls++
	return step(request), nil
}

func TestKiwoomSnapshotPaginatesNormalizesAndRedacts(t *testing.T) {
	const rawAccount = "9876543210"
	const rawOrderOne = "0000042"
	const rawOrderTwo = "0000043"
	script := &kiwoomScript{t: t}
	script.steps = []func(*http.Request) *http.Response{
		func(request *http.Request) *http.Response {
			assertKiwoomRequest(t, request, kiwoomTokenPath, "")
			var body map[string]string
			decodeKiwoomRequest(t, request, &body)
			if body["grant_type"] != "client_credentials" || body["appkey"] != "synthetic-app-key" || body["secretkey"] != "synthetic-secret-key" {
				t.Fatalf("unexpected token request: %#v", body)
			}
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("synthetic-token"))
		},
		func(request *http.Request) *http.Response {
			assertKiwoomRequest(t, request, kiwoomAccountPath, "ka00001")
			assertKiwoomAuthorization(t, request, "synthetic-token")
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka00001"}, `{"acctNo":"`+rawAccount+`","return_code":0,"return_msg":"synthetic account message"}`)
		},
		func(request *http.Request) *http.Response {
			assertKiwoomRequest(t, request, kiwoomAccountPath, "kt00018")
			assertNoKiwoomCursor(t, request)
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "kt00018", "cont-yn": "Y", "next-key": "synthetic-eval-2"}, syntheticEvaluationJSON(`[
					{"stk_cd":"J222222","stk_nm":"합성전자","evltv_prft":"-000,000.000","prft_rt":"+0004.500","pur_pric":"000,010.00","rmnd_qty":"+0000010","trde_able_qty":"00000008","cur_prc":"-0000012.50","pur_amt":"000000100","evlt_amt":"000000125","poss_rt":"050.000"}
			]`))
		},
		func(request *http.Request) *http.Response {
			assertKiwoomRequest(t, request, kiwoomAccountPath, "kt00018")
			assertKiwoomCursor(t, request, "synthetic-eval-2")
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "kt00018"}, syntheticEvaluationJSON(`[
				{"stk_cd":"Q111111","stk_nm":"테스트바이오","evltv_prft":"+00000025","prft_rt":"+25.000","pur_pric":"0000001","rmnd_qty":"00000100","trde_able_qty":"00000090","cur_prc":"-0000001.25","pur_amt":"00000100","evlt_amt":"00000125","poss_rt":"050.000"}
			]`))
		},
		func(request *http.Request) *http.Response {
			assertKiwoomRequest(t, request, kiwoomAccountPath, "ka10075")
			assertNoKiwoomCursor(t, request)
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka10075", "cont-yn": "Y", "next-key": "synthetic-order-2"}, `{
				"oso":[{"acnt_no":"`+rawAccount+`","ord_no":"`+rawOrderOne+`","stk_cd":"222222","stk_nm":"합성전자","ord_stt":"접수","ord_qty":"+00010","ord_pric":"001,250.00","oso_qty":"0000008","io_tp_nm":"+매수신용","tm":"101530","stex_tp":"1"}],
				"return_code":0,"return_msg":"synthetic order page one"
			}`)
		},
		func(request *http.Request) *http.Response {
			assertKiwoomRequest(t, request, kiwoomAccountPath, "ka10075")
			assertKiwoomCursor(t, request, "synthetic-order-2")
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka10075"}, `{
				"oso":[{"acnt_no":"`+rawAccount+`","ord_no":"`+rawOrderTwo+`","stk_cd":"111111","stk_nm":"테스트바이오","ord_stt":"접수","ord_qty":"0002","ord_pric":"-000.000","oso_qty":"+0002","io_tp_nm":"-매도","tm":"101531","stex_tp":"1"}],
				"return_code":0,"return_msg":"synthetic order page two"
			}`)
		},
	}
	client := newSyntheticKiwoomClient(t, KiwoomProduction, script)
	snapshot, err := client.Snapshot(context.Background(), KiwoomKRX)
	if err != nil {
		t.Fatal(err)
	}
	if script.calls != len(script.steps) {
		t.Fatalf("requests=%d want=%d", script.calls, len(script.steps))
	}
	if snapshot.MaskedAccount != "******3210" || !strings.HasPrefix(snapshot.AccountRef, "kiwoom_account_") {
		t.Fatalf("unsafe account identity: %#v", snapshot)
	}
	if len(snapshot.Positions) != 2 || snapshot.Positions[0].Symbol != "111111" || snapshot.Positions[0].CurrentPrice != "1.25" || snapshot.Positions[1].Symbol != "222222" || snapshot.Positions[1].Quantity != "10" || snapshot.Positions[1].UnrealizedPnL != "0" || snapshot.Positions[1].CurrentPrice != "12.5" {
		t.Fatalf("positions were not normalized: %#v", snapshot.Positions)
	}
	if snapshot.Totals.PurchaseAmount != "1200.5" || snapshot.Totals.UnrealizedPnL != "-50.25" || snapshot.Totals.LoanAmount != "0" {
		t.Fatalf("totals were not normalized: %#v", snapshot.Totals)
	}
	if len(snapshot.OpenOrders) != 2 || snapshot.OpenOrders[0].Symbol != "111111" || snapshot.OpenOrders[0].Price != "0" || snapshot.OpenOrders[0].Side != "SELL" || snapshot.OpenOrders[0].Status != "OPEN" || snapshot.OpenOrders[1].Price != "1250" || snapshot.OpenOrders[1].Side != "BUY" {
		t.Fatalf("orders were not normalized: %#v", snapshot.OpenOrders)
	}
	wantRefs := map[string]bool{
		client.alias("order", snapshot.AccountRef+"\x00"+rawOrderOne): true,
		client.alias("order", snapshot.AccountRef+"\x00"+rawOrderTwo): true,
	}
	for _, order := range snapshot.OpenOrders {
		if !wantRefs[order.OrderRef] {
			t.Fatalf("snapshot order ref cannot match a submit ACK: %q", order.OrderRef)
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{rawAccount, rawOrderOne, rawOrderTwo, "synthetic-app-key", "synthetic-secret-key", "synthetic-token", "return_msg"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("canonical snapshot leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestKiwoomRejectsMalformedFinancialStringsAndJSONNumbers(t *testing.T) {
	tests := []struct {
		name string
		row  string
	}{
		{"exponent string", `{"stk_cd":"111111","stk_nm":"합성전자","evltv_prft":"0","prft_rt":"0","pur_pric":"1","rmnd_qty":"1e3","trde_able_qty":"1","cur_prc":"1","pur_amt":"1","evlt_amt":"1","poss_rt":"1"}`},
		{"JSON number", `{"stk_cd":"111111","stk_nm":"합성전자","evltv_prft":"0","prft_rt":"0","pur_pric":"1","rmnd_qty":1000,"trde_able_qty":"1","cur_prc":"1","pur_amt":"1","evlt_amt":"1","poss_rt":"1"}`},
		{"negative quantity", `{"stk_cd":"111111","stk_nm":"합성전자","evltv_prft":"0","prft_rt":"0","pur_pric":"1","rmnd_qty":"-1","trde_able_qty":"1","cur_prc":"1","pur_amt":"1","evlt_amt":"1","poss_rt":"1"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := snapshotUntilEvaluationTransport(t, syntheticEvaluationJSON("["+test.row+"]"), map[string]string{"api-id": "kt00018"})
			client := newSyntheticKiwoomClient(t, KiwoomProduction, transport)
			if _, err := client.Snapshot(context.Background(), KiwoomKRX); err == nil {
				t.Fatal("malformed financial value was accepted")
			} else {
				var typed *KiwoomError
				if !errors.As(err, &typed) || (typed.Kind != "invalid_position" && typed.Kind != "invalid_response") {
					t.Fatalf("unexpected error: %#v", err)
				}
			}
		})
	}
}

func TestKiwoomErrorsNeverExposeProviderOrCredentialData(t *testing.T) {
	const marker = "never-expose-provider-message"
	client := newSyntheticKiwoomClient(t, KiwoomProduction, &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("synthetic-token"))
		},
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka00001"}, `{"acctNo":"9876543210","return_code":17,"return_msg":"`+marker+` synthetic-app-key synthetic-secret-key synthetic-token"}`)
		},
	}})
	_, err := client.Snapshot(context.Background(), KiwoomKRX)
	if err == nil {
		t.Fatal("provider rejection was accepted")
	}
	var typed *KiwoomError
	if !errors.As(err, &typed) || typed.Kind != "provider_rejected" || typed.ProviderCode == nil || *typed.ProviderCode != 17 {
		t.Fatalf("unexpected typed error: %#v", err)
	}
	for _, forbidden := range []string{marker, "synthetic-app-key", "synthetic-secret-key", "synthetic-token", "9876543210"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error leaked %q: %v", forbidden, err)
		}
	}

	calls := 0
	client = newSyntheticKiwoomClient(t, KiwoomProduction, kiwoomRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Path == kiwoomTokenPath {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("synthetic-token")), nil
		}
		return kiwoomResponse(http.StatusInternalServerError, nil, marker+" synthetic-secret-key"), nil
	}))
	_, err = client.Snapshot(context.Background(), KiwoomKRX)
	if err == nil || strings.Contains(err.Error(), marker) || calls != 2 {
		t.Fatalf("unsafe or retried 5xx error: calls=%d err=%v", calls, err)
	}

	calls = 0
	client = newSyntheticKiwoomClient(t, KiwoomProduction, kiwoomRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Path == kiwoomTokenPath {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("synthetic-token")), nil
		}
		return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka00001"}, `{"return_code":1700,"return_msg":"`+marker+`"}`), nil
	}))
	err = snapshotError(client)
	assertKiwoomErrorKind(t, err, "rate_limited")
	if calls != 2 || strings.Contains(err.Error(), marker) {
		t.Fatalf("unsafe or retried body rate-limit: calls=%d err=%v", calls, err)
	}
}

func TestKiwoomRetriesOneUnauthorizedWithCompareAndInvalidate(t *testing.T) {
	var mu sync.Mutex
	tokenCalls, accountCalls := 0, 0
	client := newSyntheticKiwoomClient(t, KiwoomProduction, kiwoomRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		if request.URL.Path == kiwoomTokenPath {
			tokenCalls++
			token := "old-token"
			if tokenCalls > 1 {
				token = "fresh-token"
			}
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON(token)), nil
		}
		switch request.Header.Get("api-id") {
		case "ka00001":
			accountCalls++
			if request.Header.Get("Authorization") == "Bearer old-token" {
				return kiwoomResponse(http.StatusUnauthorized, nil, `{"return_msg":"expired old-token"}`), nil
			}
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka00001"}, `{"acctNo":"9876543210","return_code":0}`), nil
		case "kt00018":
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "kt00018"}, syntheticEvaluationJSON(`[]`)), nil
		case "ka10075":
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka10075"}, `{"oso":[],"return_code":0}`), nil
		default:
			t.Fatalf("unexpected api-id %q", request.Header.Get("api-id"))
			return nil, nil
		}
	}))
	if _, err := client.Snapshot(context.Background(), KiwoomKRX); err != nil {
		t.Fatal(err)
	}
	if tokenCalls != 2 || accountCalls != 2 || client.accessToken != "fresh-token" {
		t.Fatalf("unexpected refresh counts: tokens=%d accounts=%d cached=%q", tokenCalls, accountCalls, client.accessToken)
	}
	client.invalidateToken("old-token")
	if client.accessToken != "fresh-token" {
		t.Fatal("a stale 401 invalidated the refreshed token")
	}

	bodyTokenCalls := 0
	client = newSyntheticKiwoomClient(t, KiwoomProduction, kiwoomRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == kiwoomTokenPath {
			bodyTokenCalls++
			token := "body-old-token"
			if bodyTokenCalls > 1 {
				token = "body-fresh-token"
			}
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON(token)), nil
		}
		switch request.Header.Get("api-id") {
		case "ka00001":
			if request.Header.Get("Authorization") == "Bearer body-old-token" {
				return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka00001"}, `{"return_code":8005,"return_msg":"synthetic auth failure"}`), nil
			}
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka00001"}, `{"acctNo":"9876543210","return_code":0}`), nil
		case "kt00018":
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "kt00018"}, syntheticEvaluationJSON(`[]`)), nil
		case "ka10075":
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka10075"}, `{"oso":[],"return_code":0}`), nil
		default:
			t.Fatalf("unexpected api-id %q", request.Header.Get("api-id"))
			return nil, nil
		}
	}))
	if _, err := client.Snapshot(context.Background(), KiwoomKRX); err != nil || bodyTokenCalls != 2 {
		t.Fatalf("body auth failure did not refresh once: tokens=%d err=%v", bodyTokenCalls, err)
	}
	code := 8103
	if !kiwoomAuthFailure(&code) {
		t.Fatal("known body auth failure 8103 was not recognized")
	}
}

func TestKiwoomRejectsBoundaryAndPaginationFailures(t *testing.T) {
	t.Run("bounded response", func(t *testing.T) {
		client := newSyntheticKiwoomClient(t, KiwoomProduction, &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
			func(*http.Request) *http.Response {
				return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("token"))
			},
			func(*http.Request) *http.Response {
				return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka00001"}, strings.Repeat("x", maxBodyBytes+1))
			},
		}})
		assertKiwoomErrorKind(t, snapshotError(client), "response_too_large")
	})

	t.Run("api id mismatch", func(t *testing.T) {
		client := newSyntheticKiwoomClient(t, KiwoomProduction, &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
			func(*http.Request) *http.Response {
				return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("token"))
			},
			func(*http.Request) *http.Response {
				return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "kt00018"}, `{"acctNo":"9876543210","return_code":0}`)
			},
		}})
		assertKiwoomErrorKind(t, snapshotError(client), "api_id_mismatch")
	})

	t.Run("missing cursor", func(t *testing.T) {
		transport := snapshotUntilEvaluationTransport(t, syntheticEvaluationJSON(`[]`), map[string]string{"api-id": "kt00018", "cont-yn": "Y"})
		client := newSyntheticKiwoomClient(t, KiwoomProduction, transport)
		assertKiwoomErrorKind(t, snapshotError(client), "invalid_pagination")
	})

	t.Run("repeated cursor", func(t *testing.T) {
		script := snapshotUntilEvaluationTransport(t, syntheticEvaluationJSON(`[]`), map[string]string{"api-id": "kt00018", "cont-yn": "Y", "next-key": "same-cursor"})
		script.steps = append(script.steps, func(request *http.Request) *http.Response {
			assertKiwoomCursor(t, request, "same-cursor")
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "kt00018", "cont-yn": "Y", "next-key": "same-cursor"}, syntheticEvaluationJSON(`[]`))
		})
		client := newSyntheticKiwoomClient(t, KiwoomProduction, script)
		assertKiwoomErrorKind(t, snapshotError(client), "repeated_cursor")
	})
}

func TestKiwoomRejectsMockNXTAndNonReadAPIIDsBeforeNetwork(t *testing.T) {
	transport := kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("rejected request reached the network")
		return nil, nil
	})
	client := newSyntheticKiwoomClient(t, KiwoomMock, transport)
	assertKiwoomErrorKind(t, snapshotExchangeError(client, KiwoomNXT), "unsupported_mock_exchange")
	_, err := client.readPage(context.Background(), "kt10000", struct{}{}, "", "")
	assertKiwoomErrorKind(t, err, "api_not_allowed")
	for _, allowed := range []string{"ka00001", "kt00018", "ka10075", "kt00009", "ka10080", "ka10081"} {
		if !kiwoomReadAPIAllowed(allowed) {
			t.Fatalf("read API %s is missing from the closed allowlist", allowed)
		}
	}
	if kiwoomReadPath("kt00009") != kiwoomAccountPath || kiwoomReadPath("ka10080") != kiwoomChartPath || kiwoomReadPath("ka10081") != kiwoomChartPath || kiwoomReadPath("kt10000") != "" {
		t.Fatal("Kiwoom read API IDs escaped their fixed path boundary")
	}
	for _, raw := range []string{"A111111", "J111111", "Q111111", "111111"} {
		if symbol, ok := kiwoomStockCode(raw); !ok || symbol != "111111" {
			t.Fatalf("stock code %q normalized to %q, %v", raw, symbol, ok)
		}
	}
	production := newSyntheticKiwoomClient(t, KiwoomProduction, transport)
	if kiwoomBaseURL(KiwoomProduction) != kiwoomProductionBase || kiwoomBaseURL(KiwoomMock) != kiwoomMockBase || kiwoomBaseURL("other") != "" {
		t.Fatal("Kiwoom environment escaped the closed official base URLs")
	}
	if production.alias("account", "9876543210") == client.alias("account", "9876543210") {
		t.Fatal("production and mock account aliases collided")
	}
}

func TestKiwoomRejectsInvalidOpenOrderBoundary(t *testing.T) {
	tests := []struct {
		name string
		row  string
	}{
		{"negative price", `{"acnt_no":"9876543210","ord_no":"42","stk_cd":"111111","stk_nm":"합성전자","ord_qty":"1","ord_pric":"-1","oso_qty":"1","io_tp_nm":"+매수","tm":"101530","stex_tp":"1"}`},
		{"invalid time", `{"acnt_no":"9876543210","ord_no":"42","stk_cd":"111111","stk_nm":"합성전자","ord_qty":"1","ord_pric":"1","oso_qty":"1","io_tp_nm":"+매수","tm":"999999","stex_tp":"1"}`},
		{"non KRX exchange", `{"acnt_no":"9876543210","ord_no":"42","stk_cd":"111111","stk_nm":"합성전자","ord_qty":"1","ord_pric":"1","oso_qty":"1","io_tp_nm":"+매수","tm":"101530","stex_tp":"2"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertKiwoomErrorKind(t, syntheticOpenOrderError(t, test.row), "invalid_order")
		})
	}
}

func syntheticOpenOrderError(t *testing.T, row string) error {
	t.Helper()
	client := newSyntheticKiwoomClient(t, KiwoomProduction, &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("token"))
		},
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka00001"}, `{"acctNo":"9876543210","return_code":0}`)
		},
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "kt00018"}, syntheticEvaluationJSON(`[]`))
		},
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka10075"}, `{"oso":[`+row+`],"return_code":0}`)
		},
	}})
	return snapshotError(client)
}

func newSyntheticKiwoomClient(t *testing.T, environment KiwoomEnvironment, transport http.RoundTripper) *KiwoomClient {
	t.Helper()
	client, err := NewKiwoomClient(environment, "synthetic-app-key", "synthetic-secret-key", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	client.transport = transport
	client.now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	return client
}

func syntheticTokenJSON(token string) string {
	return `{"expires_dt":"20300102000000","token_type":"Bearer","token":"` + token + `","return_code":0,"return_msg":"synthetic token message"}`
}

func syntheticEvaluationJSON(rows string) string {
	return `{
		"tot_pur_amt":"+000,001,200.500","tot_evlt_amt":"000001250.00","tot_evlt_pl":"-000,000,050.250","tot_prft_rt":"+0004.500",
		"prsm_dpst_aset_amt":"000001500","tot_loan_amt":"-000.000","tot_crd_loan_amt":"000000","tot_crd_ls_amt":"+000000",
		"acnt_evlt_remn_indv_tot":` + rows + `,"return_code":0,"return_msg":"synthetic evaluation message"
	}`
}

func snapshotUntilEvaluationTransport(t *testing.T, evaluationBody string, headers map[string]string) *kiwoomScript {
	t.Helper()
	return &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("token"))
		},
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka00001"}, `{"acctNo":"9876543210","return_code":0}`)
		},
		func(*http.Request) *http.Response { return kiwoomResponse(http.StatusOK, headers, evaluationBody) },
	}}
}

func kiwoomResponse(status int, headers map[string]string, body string) *http.Response {
	header := make(http.Header)
	for key, value := range headers {
		header.Set(key, value)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func assertKiwoomRequest(t *testing.T, request *http.Request, path, apiID string) {
	t.Helper()
	if request.Method != http.MethodPost || request.URL.Scheme != "https" || request.URL.Host != "api.kiwoom.com" || request.URL.Path != path {
		t.Fatalf("unexpected request target: %s %s", request.Method, request.URL)
	}
	if request.Header.Get("api-id") != apiID {
		t.Fatalf("api-id=%q want=%q", request.Header.Get("api-id"), apiID)
	}
}

func assertKiwoomAuthorization(t *testing.T, request *http.Request, token string) {
	t.Helper()
	if request.Header.Get("Authorization") != "Bearer "+token {
		t.Fatalf("unexpected authorization header")
	}
}

func assertNoKiwoomCursor(t *testing.T, request *http.Request) {
	t.Helper()
	if request.Header.Get("cont-yn") != "" || request.Header.Get("next-key") != "" {
		t.Fatalf("unexpected initial cursor headers")
	}
}

func assertKiwoomCursor(t *testing.T, request *http.Request, expected string) {
	t.Helper()
	if request.Header.Get("cont-yn") != "Y" || request.Header.Get("next-key") != expected {
		t.Fatalf("cursor=(%q,%q) want=(Y,%q)", request.Header.Get("cont-yn"), request.Header.Get("next-key"), expected)
	}
}

func decodeKiwoomRequest(t *testing.T, request *http.Request, destination any) {
	t.Helper()
	defer request.Body.Close()
	if err := json.NewDecoder(request.Body).Decode(destination); err != nil {
		t.Fatal(err)
	}
}

func snapshotError(client *KiwoomClient) error {
	_, err := client.Snapshot(context.Background(), KiwoomKRX)
	return err
}

func snapshotExchangeError(client *KiwoomClient, exchange KiwoomExchange) error {
	_, err := client.Snapshot(context.Background(), exchange)
	return err
}

func assertKiwoomErrorKind(t *testing.T, err error, expected string) {
	t.Helper()
	var typed *KiwoomError
	if !errors.As(err, &typed) || typed.Kind != expected {
		t.Fatalf("error=%#v want kind=%q", err, expected)
	}
}
