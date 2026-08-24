package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type KiwoomEnvironment string

const (
	KiwoomProduction KiwoomEnvironment = "production"
	KiwoomMock       KiwoomEnvironment = "mock"

	kiwoomProductionBase = "https://api.kiwoom.com"
	kiwoomMockBase       = "https://mockapi.kiwoom.com"
	kiwoomTokenPath      = "/oauth2/token"
	kiwoomAccountPath    = "/api/dostk/acnt"
	kiwoomChartPath      = "/api/dostk/chart"
	kiwoomOrderPath      = "/api/dostk/ordr"
	kiwoomCallTimeout    = 10 * time.Second
	kiwoomTokenSkew      = 30 * time.Second
	// ponytail: fixed cap and no 429/5xx retries until Kiwoom publishes usable
	// quota/page-size contracts; tune the cap and add bounded backoff then.
	kiwoomMaxPages = 32
)

type KiwoomExchange string

const (
	KiwoomKRX KiwoomExchange = "KRX"
	KiwoomNXT KiwoomExchange = "NXT"
)

var (
	kiwoomDecimalPattern = regexp.MustCompile(`^[+-]?(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+)(?:\.[0-9]+)?$`)
	kiwoomAccountPattern = regexp.MustCompile(`^[0-9]{10}$`)
	kiwoomOrderPattern   = regexp.MustCompile(`^[0-9]{1,20}$`)
	kiwoomStockPattern   = regexp.MustCompile(`^[0-9]{6}$`)
	kiwoomTimePattern    = regexp.MustCompile(`^[0-9]{6}$`)
)

type KiwoomError struct {
	Kind         string
	APIID        string
	HTTPStatus   int
	ProviderCode *int
}

func (e *KiwoomError) Error() string {
	if e == nil {
		return "kiwoom request failed"
	}
	message := "kiwoom " + e.Kind
	if e.APIID != "" {
		message += " (" + e.APIID + ")"
	}
	if e.HTTPStatus != 0 {
		message += fmt.Sprintf(" status=%d", e.HTTPStatus)
	}
	if e.ProviderCode != nil {
		message += fmt.Sprintf(" code=%d", *e.ProviderCode)
	}
	return message
}

type KiwoomClient struct {
	environment KiwoomEnvironment
	appKey      string
	secretKey   string
	aliasKey    []byte
	transport   http.RoundTripper
	now         func() time.Time

	tokenMu        sync.Mutex
	accessToken    string
	tokenExpiresAt time.Time
}

func NewKiwoomClient(environment KiwoomEnvironment, appKey, secretKey string, aliasKey []byte) (*KiwoomClient, error) {
	if kiwoomBaseURL(environment) == "" || appKey == "" || secretKey == "" || len(aliasKey) < 32 {
		return nil, &KiwoomError{Kind: "invalid_config"}
	}
	return &KiwoomClient{
		environment: environment,
		appKey:      appKey,
		secretKey:   secretKey,
		aliasKey:    append([]byte(nil), aliasKey...),
		transport:   http.DefaultTransport,
		now:         time.Now,
	}, nil
}

type KiwoomTotals struct {
	PurchaseAmount      string `json:"purchase_amount"`
	EvaluationAmount    string `json:"evaluation_amount"`
	UnrealizedPnL       string `json:"unrealized_pnl"`
	ReturnRatePercent   string `json:"return_rate_percent"`
	EstimatedAssets     string `json:"estimated_assets"`
	LoanAmount          string `json:"loan_amount"`
	CreditLoanAmount    string `json:"credit_loan_amount"`
	CreditLendingAmount string `json:"credit_lending_amount"`
}

type KiwoomPosition struct {
	Symbol               string `json:"symbol"`
	Name                 string `json:"name"`
	Quantity             string `json:"quantity"`
	TradableQuantity     string `json:"tradable_quantity"`
	AveragePurchasePrice string `json:"average_purchase_price"`
	CurrentPrice         string `json:"current_price"`
	PurchaseAmount       string `json:"purchase_amount"`
	EvaluationAmount     string `json:"evaluation_amount"`
	UnrealizedPnL        string `json:"unrealized_pnl"`
	ReturnRatePercent    string `json:"return_rate_percent"`
	WeightPercent        string `json:"weight_percent"`
}

type KiwoomOpenOrder struct {
	OrderRef          string `json:"order_ref"`
	Symbol            string `json:"symbol"`
	Name              string `json:"name"`
	Status            string `json:"status"`
	Side              string `json:"side"`
	Quantity          string `json:"quantity"`
	Price             string `json:"price"`
	RemainingQuantity string `json:"remaining_quantity"`
	Time              string `json:"time"`
	Exchange          string `json:"exchange"`
}

type KiwoomSnapshot struct {
	Source        string            `json:"source"`
	Environment   KiwoomEnvironment `json:"environment"`
	Exchange      KiwoomExchange    `json:"exchange"`
	AccountRef    string            `json:"account_ref"`
	MaskedAccount string            `json:"masked_account"`
	FetchedAt     string            `json:"fetched_at"`
	Complete      bool              `json:"complete"`
	Currency      string            `json:"currency"`
	Totals        KiwoomTotals      `json:"totals"`
	Positions     []KiwoomPosition  `json:"positions"`
	OpenOrders    []KiwoomOpenOrder `json:"open_orders"`
}

func (c *KiwoomClient) Snapshot(ctx context.Context, exchange KiwoomExchange) (*KiwoomSnapshot, error) {
	if c == nil {
		return nil, &KiwoomError{Kind: "invalid_config"}
	}
	// The first read-only slice is intentionally KRX-only. NXT needs a separately
	// verified open-order aggregation contract before it can be returned safely.
	if exchange != KiwoomKRX {
		kind := "unsupported_exchange"
		if c.environment == KiwoomMock && exchange == KiwoomNXT {
			kind = "unsupported_mock_exchange"
		}
		return nil, &KiwoomError{Kind: kind}
	}
	rawAccount, err := c.accountNumber(ctx)
	if err != nil {
		return nil, err
	}
	totals, positions, err := c.evaluation(ctx, exchange)
	if err != nil {
		return nil, err
	}
	orders, err := c.openOrders(ctx, rawAccount)
	if err != nil {
		return nil, err
	}
	return &KiwoomSnapshot{
		Source:        "kiwoom",
		Environment:   c.environment,
		Exchange:      exchange,
		AccountRef:    c.alias("account", rawAccount),
		MaskedAccount: "******" + rawAccount[6:],
		FetchedAt:     c.now().UTC().Format(time.RFC3339Nano),
		Complete:      true,
		Currency:      "KRW",
		Totals:        totals,
		Positions:     positions,
		OpenOrders:    orders,
	}, nil
}

type kiwoomResult struct {
	ReturnCode *int `json:"return_code"`
}

type kiwoomAccountNumberResponse struct {
	kiwoomResult
	AccountNumber string `json:"acctNo"`
}

type kiwoomEvaluationRow struct {
	StockCode        string `json:"stk_cd"`
	StockName        string `json:"stk_nm"`
	UnrealizedPnL    string `json:"evltv_prft"`
	ReturnRate       string `json:"prft_rt"`
	PurchasePrice    string `json:"pur_pric"`
	Quantity         string `json:"rmnd_qty"`
	TradableQuantity string `json:"trde_able_qty"`
	CurrentPrice     string `json:"cur_prc"`
	PurchaseAmount   string `json:"pur_amt"`
	EvaluationAmount string `json:"evlt_amt"`
	Weight           string `json:"poss_rt"`
}

type kiwoomEvaluationResponse struct {
	kiwoomResult
	PurchaseAmount      string                `json:"tot_pur_amt"`
	EvaluationAmount    string                `json:"tot_evlt_amt"`
	UnrealizedPnL       string                `json:"tot_evlt_pl"`
	ReturnRate          string                `json:"tot_prft_rt"`
	EstimatedAssets     string                `json:"prsm_dpst_aset_amt"`
	LoanAmount          string                `json:"tot_loan_amt"`
	CreditLoanAmount    string                `json:"tot_crd_loan_amt"`
	CreditLendingAmount string                `json:"tot_crd_ls_amt"`
	Positions           []kiwoomEvaluationRow `json:"acnt_evlt_remn_indv_tot"`
}

type kiwoomOpenOrderRow struct {
	AccountNumber     string `json:"acnt_no"`
	OrderNumber       string `json:"ord_no"`
	StockCode         string `json:"stk_cd"`
	StockName         string `json:"stk_nm"`
	Quantity          string `json:"ord_qty"`
	Price             string `json:"ord_pric"`
	RemainingQuantity string `json:"oso_qty"`
	Side              string `json:"io_tp_nm"`
	Time              string `json:"tm"`
	Exchange          string `json:"stex_tp"`
}

type kiwoomOpenOrdersResponse struct {
	kiwoomResult
	Orders []kiwoomOpenOrderRow `json:"oso"`
}

func (c *KiwoomClient) accountNumber(ctx context.Context) (string, error) {
	page, err := c.readPage(ctx, "ka00001", struct{}{}, "", "")
	if err != nil {
		return "", err
	}
	if page.continued {
		return "", &KiwoomError{Kind: "invalid_pagination", APIID: "ka00001"}
	}
	var response kiwoomAccountNumberResponse
	if err := kiwoomDecode(page.body, &response); err != nil {
		return "", &KiwoomError{Kind: "invalid_response", APIID: "ka00001"}
	}
	if err := kiwoomCheckResult("ka00001", response.kiwoomResult); err != nil {
		return "", err
	}
	if !kiwoomAccountPattern.MatchString(response.AccountNumber) {
		return "", &KiwoomError{Kind: "invalid_account", APIID: "ka00001"}
	}
	return response.AccountNumber, nil
}

func (c *KiwoomClient) evaluation(ctx context.Context, exchange KiwoomExchange) (KiwoomTotals, []KiwoomPosition, error) {
	var totals KiwoomTotals
	positions := make([]KiwoomPosition, 0)
	first := true
	err := c.accountPages(ctx, "kt00018", map[string]string{"qry_tp": "1", "dmst_stex_tp": string(exchange)}, func(body []byte) error {
		var response kiwoomEvaluationResponse
		if err := kiwoomDecode(body, &response); err != nil {
			return &KiwoomError{Kind: "invalid_response", APIID: "kt00018"}
		}
		if err := kiwoomCheckResult("kt00018", response.kiwoomResult); err != nil {
			return err
		}
		pageTotals, ok := kiwoomNormalizeTotals(response)
		if !ok {
			return &KiwoomError{Kind: "invalid_decimal", APIID: "kt00018"}
		}
		if first {
			totals, first = pageTotals, false
		} else if totals != pageTotals {
			return &KiwoomError{Kind: "inconsistent_totals", APIID: "kt00018"}
		}
		for _, row := range response.Positions {
			position, ok := kiwoomNormalizePosition(row)
			if !ok {
				return &KiwoomError{Kind: "invalid_position", APIID: "kt00018"}
			}
			positions = append(positions, position)
		}
		return nil
	})
	sort.Slice(positions, func(i, j int) bool {
		left, right := positions[i], positions[j]
		return left.Symbol+"\x00"+left.Name+"\x00"+left.Quantity+"\x00"+left.AveragePurchasePrice <
			right.Symbol+"\x00"+right.Name+"\x00"+right.Quantity+"\x00"+right.AveragePurchasePrice
	})
	return totals, positions, err
}

func (c *KiwoomClient) openOrders(ctx context.Context, rawAccount string) ([]KiwoomOpenOrder, error) {
	orders := make([]KiwoomOpenOrder, 0)
	seen := make(map[string]struct{})
	err := c.accountPages(ctx, "ka10075", map[string]string{"all_stk_tp": "0", "trde_tp": "0", "stex_tp": "1"}, func(body []byte) error {
		var response kiwoomOpenOrdersResponse
		if err := kiwoomDecode(body, &response); err != nil {
			return &KiwoomError{Kind: "invalid_response", APIID: "ka10075"}
		}
		if err := kiwoomCheckResult("ka10075", response.kiwoomResult); err != nil {
			return err
		}
		for _, row := range response.Orders {
			if row.AccountNumber != rawAccount || !kiwoomOrderPattern.MatchString(row.OrderNumber) {
				return &KiwoomError{Kind: "invalid_order_identity", APIID: "ka10075"}
			}
			order, ok := kiwoomNormalizeOrder(row)
			if !ok || order.Exchange != string(KiwoomKRX) {
				return &KiwoomError{Kind: "invalid_order", APIID: "ka10075"}
			}
			order.OrderRef = c.alias("order", rawAccount+"\x00"+row.OrderNumber)
			if _, duplicate := seen[order.OrderRef]; duplicate {
				return &KiwoomError{Kind: "duplicate_order", APIID: "ka10075"}
			}
			seen[order.OrderRef] = struct{}{}
			orders = append(orders, order)
		}
		return nil
	})
	sort.Slice(orders, func(i, j int) bool {
		return orders[i].Symbol+"\x00"+orders[i].Time+"\x00"+orders[i].OrderRef <
			orders[j].Symbol+"\x00"+orders[j].Time+"\x00"+orders[j].OrderRef
	})
	return orders, err
}

type kiwoomPage struct {
	body      []byte
	continued bool
	nextKey   string
}

func (c *KiwoomClient) accountPages(ctx context.Context, apiID string, requestBody any, handle func([]byte) error) error {
	return c.readPages(ctx, apiID, requestBody, func(body []byte) (bool, error) {
		return false, handle(body)
	})
}

func (c *KiwoomClient) readPages(ctx context.Context, apiID string, requestBody any, handle func([]byte) (bool, error)) error {
	seen := make(map[string]struct{})
	cont, next := "", ""
	for pageNumber := 1; pageNumber <= kiwoomMaxPages; pageNumber++ {
		page, err := c.readPage(ctx, apiID, requestBody, cont, next)
		if err != nil {
			return err
		}
		stop, err := handle(page.body)
		if err != nil {
			return err
		}
		if stop || !page.continued {
			return nil
		}
		if pageNumber == kiwoomMaxPages {
			return &KiwoomError{Kind: "pagination_limit", APIID: apiID}
		}
		if _, duplicate := seen[page.nextKey]; duplicate {
			return &KiwoomError{Kind: "repeated_cursor", APIID: apiID}
		}
		seen[page.nextKey] = struct{}{}
		cont, next = "Y", page.nextKey
	}
	return &KiwoomError{Kind: "pagination_limit", APIID: apiID}
}

func (c *KiwoomClient) readPage(ctx context.Context, apiID string, requestBody any, cont, next string) (kiwoomPage, error) {
	path := kiwoomReadPath(apiID)
	if path == "" {
		return kiwoomPage{}, &KiwoomError{Kind: "api_not_allowed"}
	}
	return c.postPage(ctx, apiID, path, requestBody, cont, next, true)
}

func (c *KiwoomClient) postPage(ctx context.Context, apiID, path string, requestBody any, cont, next string, retryUnauthorized bool) (kiwoomPage, error) {
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return kiwoomPage{}, &KiwoomError{Kind: "invalid_request", APIID: apiID}
	}
	token, err := c.token(ctx)
	if err != nil {
		return kiwoomPage{}, err
	}
	attempts := 1
	if retryUnauthorized {
		attempts = 2
	}
	for attempt := 0; attempt < attempts; attempt++ {
		headers := http.Header{
			"Content-Type":  {"application/json;charset=UTF-8"},
			"Authorization": {"Bearer " + token},
			"Api-Id":        {apiID},
		}
		if cont != "" {
			headers.Set("cont-yn", cont)
			headers.Set("next-key", next)
		}
		response, body, err := c.roundTrip(ctx, path, payload, headers)
		if err != nil {
			return kiwoomPage{}, err
		}
		if response.StatusCode == http.StatusUnauthorized {
			c.invalidateToken(token)
			if retryUnauthorized && attempt == 0 {
				token, err = c.token(ctx)
				if err != nil {
					return kiwoomPage{}, err
				}
				continue
			}
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return kiwoomPage{}, kiwoomHTTPError(apiID, response.StatusCode)
		}
		if response.Header.Get("api-id") != apiID {
			return kiwoomPage{}, &KiwoomError{Kind: "api_id_mismatch", APIID: apiID}
		}
		var result kiwoomResult
		if kiwoomDecode(body, &result) == nil && kiwoomAuthFailure(result.ReturnCode) {
			c.invalidateToken(token)
			if retryUnauthorized && attempt == 0 {
				token, err = c.token(ctx)
				if err != nil {
					return kiwoomPage{}, err
				}
				continue
			}
		}
		continued, cursor, err := kiwoomPagination(response.Header)
		if err != nil {
			return kiwoomPage{}, &KiwoomError{Kind: "invalid_pagination", APIID: apiID}
		}
		return kiwoomPage{body: body, continued: continued, nextKey: cursor}, nil
	}
	return kiwoomPage{}, &KiwoomError{Kind: "unauthorized", APIID: apiID, HTTPStatus: http.StatusUnauthorized}
}

func (c *KiwoomClient) token(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.accessToken != "" && c.now().Add(kiwoomTokenSkew).Before(c.tokenExpiresAt) {
		return c.accessToken, nil
	}
	payload, _ := json.Marshal(map[string]string{
		"grant_type": "client_credentials",
		"appkey":     c.appKey,
		"secretkey":  c.secretKey,
	})
	response, body, err := c.roundTrip(ctx, kiwoomTokenPath, payload, http.Header{"Content-Type": {"application/json;charset=UTF-8"}})
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", kiwoomHTTPError("token", response.StatusCode)
	}
	var tokenResponse struct {
		kiwoomResult
		ExpiresAt string `json:"expires_dt"`
		TokenType string `json:"token_type"`
		Token     string `json:"token"`
	}
	if err := kiwoomDecode(body, &tokenResponse); err != nil {
		return "", &KiwoomError{Kind: "invalid_response", APIID: "token"}
	}
	if err := kiwoomCheckResult("token", tokenResponse.kiwoomResult); err != nil {
		return "", err
	}
	if !strings.EqualFold(tokenResponse.TokenType, "Bearer") || tokenResponse.Token == "" || len(tokenResponse.Token) > 1000 {
		return "", &KiwoomError{Kind: "invalid_token", APIID: "token"}
	}
	// Kiwoom documents the shape but not the timezone of expires_dt. Treat it as
	// Asia/Seoul (UTC+9) until verified; interpreting UTC as KST expires early.
	expiresAt, err := time.ParseInLocation("20060102150405", tokenResponse.ExpiresAt, time.FixedZone("Asia/Seoul", 9*60*60))
	if err != nil || !expiresAt.After(c.now().Add(kiwoomTokenSkew)) {
		return "", &KiwoomError{Kind: "invalid_token_expiry", APIID: "token"}
	}
	c.accessToken, c.tokenExpiresAt = tokenResponse.Token, expiresAt
	return c.accessToken, nil
}

func (c *KiwoomClient) invalidateToken(stale string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if hmac.Equal([]byte(c.accessToken), []byte(stale)) {
		c.accessToken = ""
		c.tokenExpiresAt = time.Time{}
	}
}

func (c *KiwoomClient) roundTrip(ctx context.Context, path string, payload []byte, headers http.Header) (*http.Response, []byte, error) {
	callCtx, cancel := context.WithTimeout(ctx, kiwoomCallTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, kiwoomBaseURL(c.environment)+path, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, &KiwoomError{Kind: "invalid_request"}
	}
	request.Header = headers.Clone()
	response, err := c.transport.RoundTrip(request)
	if err != nil {
		kind := "network_error"
		if callCtx.Err() != nil {
			kind = "timeout"
		}
		return nil, nil, &KiwoomError{Kind: kind}
	}
	if response == nil || response.Body == nil {
		return nil, nil, &KiwoomError{Kind: "invalid_response"}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(maxBodyBytes)+1))
	if err != nil {
		return nil, nil, &KiwoomError{Kind: "network_error"}
	}
	if len(body) > maxBodyBytes {
		return nil, nil, &KiwoomError{Kind: "response_too_large"}
	}
	return response, body, nil
}

func (c *KiwoomClient) alias(kind, raw string) string {
	mac := hmac.New(sha256.New, c.aliasKey)
	mac.Write([]byte(c.environment))
	mac.Write([]byte{0})
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(raw))
	return "kiwoom_" + kind + "_" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:18])
}

func kiwoomBaseURL(environment KiwoomEnvironment) string {
	switch environment {
	case KiwoomProduction:
		return kiwoomProductionBase
	case KiwoomMock:
		return kiwoomMockBase
	default:
		return ""
	}
}

func kiwoomReadAPIAllowed(apiID string) bool {
	return kiwoomReadPath(apiID) != ""
}

func kiwoomReadPath(apiID string) string {
	switch apiID {
	case "ka00001", "kt00018", "ka10075", "kt00009":
		return kiwoomAccountPath
	case "ka10080", "ka10081":
		return kiwoomChartPath
	default:
		return ""
	}
}

func kiwoomHTTPError(apiID string, status int) error {
	kind := "provider_http_error"
	switch {
	case status == http.StatusUnauthorized:
		kind = "unauthorized"
	case status == http.StatusTooManyRequests:
		kind = "rate_limited"
	case status >= 500:
		kind = "provider_unavailable"
	}
	return &KiwoomError{Kind: kind, APIID: apiID, HTTPStatus: status}
}

func kiwoomCheckResult(apiID string, result kiwoomResult) error {
	if result.ReturnCode == nil {
		return &KiwoomError{Kind: "invalid_response", APIID: apiID}
	}
	if *result.ReturnCode == 0 {
		return nil
	}
	code := *result.ReturnCode
	kind := "provider_rejected"
	switch code {
	case 1700, 1701, 1702:
		kind = "rate_limited"
	case 8005, 8103:
		kind = "unauthorized"
	case 8030, 8031:
		kind = "environment_mismatch"
	}
	return &KiwoomError{Kind: kind, APIID: apiID, ProviderCode: &code}
}

func kiwoomAuthFailure(code *int) bool {
	return code != nil && (*code == 8005 || *code == 8103)
}

func kiwoomPagination(header http.Header) (bool, string, error) {
	continuation := header.Get("cont-yn")
	cursor := header.Get("next-key")
	switch continuation {
	case "", "N":
		if cursor != "" {
			return false, "", &KiwoomError{Kind: "invalid_pagination"}
		}
		return false, "", nil
	case "Y":
		if cursor == "" || len(cursor) > 50 || strings.ContainsAny(cursor, "\r\n") || !utf8.ValidString(cursor) {
			return false, "", &KiwoomError{Kind: "invalid_pagination"}
		}
		return true, cursor, nil
	default:
		return false, "", &KiwoomError{Kind: "invalid_pagination"}
	}
}

func kiwoomDecode(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return &KiwoomError{Kind: "invalid_response"}
	}
	return nil
}

func kiwoomNormalizeTotals(response kiwoomEvaluationResponse) (KiwoomTotals, bool) {
	purchase, ok := kiwoomNonNegativeDecimal(response.PurchaseAmount)
	evaluation, evaluationOK := kiwoomNonNegativeDecimal(response.EvaluationAmount)
	unrealized, unrealizedOK := kiwoomSignedDecimal(response.UnrealizedPnL)
	returnRate, returnRateOK := kiwoomSignedDecimal(response.ReturnRate)
	estimated, estimatedOK := kiwoomSignedDecimal(response.EstimatedAssets)
	loan, loanOK := kiwoomNonNegativeDecimal(response.LoanAmount)
	creditLoan, creditLoanOK := kiwoomNonNegativeDecimal(response.CreditLoanAmount)
	creditLending, creditLendingOK := kiwoomNonNegativeDecimal(response.CreditLendingAmount)
	ok = ok && evaluationOK && unrealizedOK && returnRateOK && estimatedOK && loanOK && creditLoanOK && creditLendingOK
	if !ok {
		return KiwoomTotals{}, false
	}
	return KiwoomTotals{
		PurchaseAmount: purchase, EvaluationAmount: evaluation, UnrealizedPnL: unrealized, ReturnRatePercent: returnRate,
		EstimatedAssets: estimated, LoanAmount: loan, CreditLoanAmount: creditLoan, CreditLendingAmount: creditLending,
	}, true
}

func kiwoomNormalizePosition(row kiwoomEvaluationRow) (KiwoomPosition, bool) {
	symbol, ok := kiwoomStockCode(row.StockCode)
	name, nameOK := kiwoomText(row.StockName)
	quantity, quantityOK := kiwoomNonNegativeDecimal(row.Quantity)
	tradable, tradableOK := kiwoomNonNegativeDecimal(row.TradableQuantity)
	purchasePrice, purchasePriceOK := kiwoomNonNegativeDecimal(row.PurchasePrice)
	currentPrice, currentPriceOK := kiwoomMagnitudeDecimal(row.CurrentPrice)
	purchase, purchaseOK := kiwoomNonNegativeDecimal(row.PurchaseAmount)
	evaluation, evaluationOK := kiwoomNonNegativeDecimal(row.EvaluationAmount)
	unrealized, unrealizedOK := kiwoomSignedDecimal(row.UnrealizedPnL)
	returnRate, returnRateOK := kiwoomSignedDecimal(row.ReturnRate)
	weight, weightOK := kiwoomNonNegativeDecimal(row.Weight)
	decimalOK := quantityOK && tradableOK && purchasePriceOK && currentPriceOK && purchaseOK && evaluationOK && unrealizedOK && returnRateOK && weightOK
	if !ok || !nameOK || !decimalOK {
		return KiwoomPosition{}, false
	}
	return KiwoomPosition{
		Symbol: symbol, Name: name, Quantity: quantity, TradableQuantity: tradable, AveragePurchasePrice: purchasePrice,
		CurrentPrice: currentPrice, PurchaseAmount: purchase, EvaluationAmount: evaluation, UnrealizedPnL: unrealized,
		ReturnRatePercent: returnRate, WeightPercent: weight,
	}, true
}

func kiwoomNormalizeOrder(row kiwoomOpenOrderRow) (KiwoomOpenOrder, bool) {
	symbol, symbolOK := kiwoomStockCode(row.StockCode)
	name, nameOK := kiwoomText(row.StockName)
	rawSide, sideOK := kiwoomText(row.Side)
	side := ""
	switch normalized := strings.TrimLeft(rawSide, "+-"); {
	case strings.HasPrefix(normalized, "매수"):
		side = "BUY"
	case strings.HasPrefix(normalized, "매도"):
		side = "SELL"
	}
	quantity, quantityOK := kiwoomNonNegativeDecimal(row.Quantity)
	price, priceOK := kiwoomNonNegativeDecimal(row.Price)
	remaining, remainingOK := kiwoomNonNegativeDecimal(row.RemainingQuantity)
	exchange := ""
	switch row.Exchange {
	case "0":
		exchange = "INTEGRATED"
	case "1":
		exchange = string(KiwoomKRX)
	case "2":
		exchange = string(KiwoomNXT)
	}
	if !symbolOK || !nameOK || !sideOK || side == "" || !quantityOK || !priceOK || !remainingOK || !kiwoomValidTime(row.Time) || exchange == "" {
		return KiwoomOpenOrder{}, false
	}
	return KiwoomOpenOrder{
		Symbol: symbol, Name: name, Status: "OPEN", Side: side, Quantity: quantity, Price: price,
		RemainingQuantity: remaining, Time: row.Time, Exchange: exchange,
	}, true
}

func kiwoomSignedDecimal(raw string) (string, bool) {
	if len(raw) == 0 || len(raw) > 64 {
		return "", false
	}
	value := strings.TrimSpace(raw)
	if !kiwoomDecimalPattern.MatchString(value) {
		return "", false
	}
	value = strings.ReplaceAll(strings.TrimPrefix(value, "+"), ",", "")
	number, ok := new(big.Rat).SetString(value)
	if !ok {
		return "", false
	}
	canonical, err := formatDecimal(number)
	return canonical, err == nil
}

func kiwoomNonNegativeDecimal(raw string) (string, bool) {
	value, ok := kiwoomSignedDecimal(raw)
	return value, ok && !strings.HasPrefix(value, "-")
}

func kiwoomMagnitudeDecimal(raw string) (string, bool) {
	value, ok := kiwoomSignedDecimal(raw)
	return strings.TrimPrefix(value, "-"), ok
}

func kiwoomValidTime(raw string) bool {
	if !kiwoomTimePattern.MatchString(raw) {
		return false
	}
	_, err := time.Parse("150405", raw)
	return err == nil
}

func kiwoomStockCode(raw string) (string, bool) {
	if len(raw) == 7 && strings.ContainsRune("AJQ", rune(raw[0])) {
		raw = raw[1:]
	}
	return raw, kiwoomStockPattern.MatchString(raw)
}

func kiwoomText(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 80 {
		return "", false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return value, true
}
