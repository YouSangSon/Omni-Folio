package main

import (
	"context"
	"errors"
)

const (
	kiwoomBuyOrderAPI  = "kt10000"
	kiwoomSellOrderAPI = "kt10001"
)

var errLimitOrderRejected = errors.New("limit order rejected")

// LimitOrderDispatchPort is a capability value, not an open-ended broker
// interface. Provider and mode are sealed with the two adapter operations so
// application code can reject a miswired adapter before credential preflight.
type LimitOrderDispatchPort struct {
	provider  string
	mode      string
	preflight func(context.Context) error
	submit    func(context.Context, OrderIntent) (string, error)
}

func newLimitOrderDispatchPort(
	provider, mode string,
	preflight func(context.Context) error,
	submit func(context.Context, OrderIntent) (string, error),
) (LimitOrderDispatchPort, error) {
	if provider == "" || mode == "" || preflight == nil || submit == nil {
		return LimitOrderDispatchPort{}, errors.New("limit order dispatch capability is invalid")
	}
	return LimitOrderDispatchPort{provider: provider, mode: mode, preflight: preflight, submit: submit}, nil
}

func (p LimitOrderDispatchPort) valid() bool {
	return p.provider != "" && p.mode != "" && p.preflight != nil && p.submit != nil
}

func (p LimitOrderDispatchPort) supports(intent OrderIntent) bool {
	return p.valid() && p.provider == intent.Provider && p.mode == intent.Mode
}

func (p LimitOrderDispatchPort) Preflight(ctx context.Context) error {
	return p.preflight(ctx)
}

func (p LimitOrderDispatchPort) SubmitLimitOrder(ctx context.Context, intent OrderIntent) (string, error) {
	return p.submit(ctx, intent)
}

type kiwoomMockLimitOrderDispatch struct {
	client *KiwoomClient
}

func newKiwoomMockLimitOrderDispatch(client *KiwoomClient) (LimitOrderDispatchPort, error) {
	if client == nil || client.environment != KiwoomMock {
		return LimitOrderDispatchPort{}, errors.New("Kiwoom mock limit order dispatch configuration is invalid")
	}
	dispatch := &kiwoomMockLimitOrderDispatch{client: client}
	return newLimitOrderDispatchPort("kiwoom", "synthetic", dispatch.Preflight, dispatch.SubmitLimitOrder)
}

type kiwoomOrderResponse struct {
	kiwoomResult
	OrderNumber string `json:"ord_no"`
	Exchange    string `json:"dmst_stex_tp"`
}

func (d *kiwoomMockLimitOrderDispatch) Preflight(ctx context.Context) error {
	if d == nil || d.client == nil || d.client.environment != KiwoomMock {
		return errors.New("Kiwoom mock limit order dispatch configuration is invalid")
	}
	if _, err := d.client.token(ctx); err != nil {
		return errors.New("limit order dispatch preflight failed")
	}
	return nil
}

func (d *kiwoomMockLimitOrderDispatch) SubmitLimitOrder(ctx context.Context, intent OrderIntent) (string, error) {
	if d == nil || d.client == nil || d.client.environment != KiwoomMock {
		return "", errors.New("limit order dispatch configuration is invalid")
	}
	if err := validateOrderIntent(intent); err != nil {
		return "", errors.New("limit order dispatch request is invalid")
	}
	if intent.Mode != "synthetic" {
		return "", errors.New("limit order dispatch request is invalid")
	}
	apiID := kiwoomBuyOrderAPI
	if intent.Side == "SELL" {
		apiID = kiwoomSellOrderAPI
	}
	page, err := d.client.postPage(ctx, apiID, kiwoomOrderPath, map[string]string{
		"dmst_stex_tp": intent.Exchange,
		"stk_cd":       intent.Symbol,
		"ord_qty":      intent.Quantity,
		"ord_uv":       intent.LimitPrice,
		"trde_tp":      "0",
		"cond_uv":      "",
	}, "", "", false)
	if err != nil {
		return "", classifyKiwoomLimitOrderError(err)
	}
	if page.continued || page.nextKey != "" {
		return "", errors.New("limit order dispatch outcome is unknown")
	}
	var response kiwoomOrderResponse
	if kiwoomDecode(page.body, &response) != nil {
		return "", errors.New("limit order dispatch outcome is unknown")
	}
	if err := kiwoomCheckResult(apiID, response.kiwoomResult); err != nil {
		return "", classifyKiwoomLimitOrderError(err)
	}
	if !kiwoomOrderPattern.MatchString(response.OrderNumber) || (response.Exchange != "" && response.Exchange != string(KiwoomKRX)) {
		return "", errors.New("limit order dispatch outcome is unknown")
	}
	return d.client.orderAlias(intent.AccountRef, response.OrderNumber), nil
}

func classifyKiwoomLimitOrderError(err error) error {
	var providerError *KiwoomError
	if errors.As(err, &providerError) && providerError.Kind == "provider_rejected" {
		return errLimitOrderRejected
	}
	return errors.New("limit order dispatch outcome is unknown")
}

func (s *Service) submitAuthorizedLimitOrder(ctx context.Context, dispatch LimitOrderDispatchPort, orderID string, fencingToken int64) (*OrderState, error) {
	if s == nil || s.db == nil || !dispatch.valid() || !safeOrderID(orderID) || fencingToken <= 0 {
		return nil, errors.New("limit order submit configuration is invalid")
	}
	intent, err := loadOrderIntentFrom(ctx, s.db, orderID)
	if err != nil {
		return nil, err
	}
	if intent.Mode != "synthetic" {
		return nil, errors.New("limit order submit requires synthetic mode")
	}
	if !dispatch.supports(intent) {
		return nil, errors.New("limit order submit capability does not match intent")
	}
	if err := dispatch.Preflight(ctx); err != nil {
		return nil, errors.New("limit order submit preflight failed")
	}
	state, dispatched, err := s.authorizeSyntheticDispatchOnce(ctx, orderID, fencingToken)
	if err != nil {
		return nil, err
	}
	if !dispatched {
		return state, errors.New("order submit was already dispatched; reconcile instead of resubmitting")
	}
	providerOrderRef, err := dispatch.SubmitLimitOrder(ctx, intent)
	if err != nil {
		if errors.Is(err, errLimitOrderRejected) {
			return s.appendOrderEvent(ctx, OrderEvent{
				EventID: s.id("order_event"), OrderID: orderID, Type: "SUBMIT_REJECTED", Source: "reconciliation",
			})
		}
		return state, errors.New("limit order submit outcome is unknown")
	}
	return s.appendOrderEvent(ctx, OrderEvent{
		EventID: s.id("order_event"), OrderID: orderID, Type: "SUBMIT_ACKNOWLEDGED", Source: "reconciliation",
		ProviderOrderRef: providerOrderRef,
	})
}
