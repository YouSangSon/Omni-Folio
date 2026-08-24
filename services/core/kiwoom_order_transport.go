package main

import (
	"context"
	"errors"
	"fmt"
)

const (
	kiwoomBuyOrderAPI  = "kt10000"
	kiwoomSellOrderAPI = "kt10001"
)

type kiwoomOrderResponse struct {
	kiwoomResult
	OrderNumber string `json:"ord_no"`
	Exchange    string `json:"dmst_stex_tp"`
}

func (c *KiwoomClient) submitMockLimitOrder(ctx context.Context, intent OrderIntent) (string, error) {
	if c == nil || c.environment != KiwoomMock {
		return "", &KiwoomError{Kind: "environment_mismatch"}
	}
	if err := validateOrderIntent(intent); err != nil {
		return "", &KiwoomError{Kind: "invalid_request"}
	}
	if intent.Mode != "synthetic" {
		return "", &KiwoomError{Kind: "invalid_request"}
	}
	apiID := kiwoomBuyOrderAPI
	if intent.Side == "SELL" {
		apiID = kiwoomSellOrderAPI
	}
	page, err := c.postPage(ctx, apiID, kiwoomOrderPath, map[string]string{
		"dmst_stex_tp": intent.Exchange,
		"stk_cd":       intent.Symbol,
		"ord_qty":      intent.Quantity,
		"ord_uv":       intent.LimitPrice,
		"trde_tp":      "0",
		"cond_uv":      "",
	}, "", "", false)
	if err != nil {
		return "", err
	}
	if page.continued || page.nextKey != "" {
		return "", &KiwoomError{Kind: "invalid_response", APIID: apiID}
	}
	var response kiwoomOrderResponse
	if kiwoomDecode(page.body, &response) != nil {
		return "", &KiwoomError{Kind: "invalid_response", APIID: apiID}
	}
	if err := kiwoomCheckResult(apiID, response.kiwoomResult); err != nil {
		return "", err
	}
	if !kiwoomOrderPattern.MatchString(response.OrderNumber) || (response.Exchange != "" && response.Exchange != string(KiwoomKRX)) {
		return "", &KiwoomError{Kind: "invalid_response", APIID: apiID}
	}
	return c.orderAlias(intent.AccountRef, response.OrderNumber), nil
}

func (s *Service) submitAuthorizedKiwoomMockOrder(ctx context.Context, client *KiwoomClient, orderID string, fencingToken int64) (*OrderState, error) {
	if s == nil || s.db == nil || client == nil || client.environment != KiwoomMock || !safeOrderID(orderID) || fencingToken <= 0 {
		return nil, errors.New("Kiwoom mock submit configuration is invalid")
	}
	intent, err := loadOrderIntentFrom(ctx, s.db, orderID)
	if err != nil {
		return nil, err
	}
	if intent.Mode != "synthetic" {
		return nil, errors.New("Kiwoom mock submit requires synthetic mode")
	}
	if _, err := client.token(ctx); err != nil {
		return nil, fmt.Errorf("Kiwoom mock token preflight failed: %w", err)
	}
	state, dispatched, err := s.authorizeSyntheticDispatchOnce(ctx, orderID, fencingToken)
	if err != nil {
		return nil, err
	}
	if !dispatched {
		return state, errors.New("order submit was already dispatched; reconcile instead of resubmitting")
	}
	providerOrderRef, err := client.submitMockLimitOrder(ctx, intent)
	if err != nil {
		var providerError *KiwoomError
		if errors.As(err, &providerError) && providerError.Kind == "provider_rejected" {
			return s.appendOrderEvent(ctx, OrderEvent{
				EventID: s.id("order_event"), OrderID: orderID, Type: "SUBMIT_REJECTED", Source: "reconciliation",
			})
		}
		return state, fmt.Errorf("Kiwoom mock submit outcome is unknown: %w", err)
	}
	return s.appendOrderEvent(ctx, OrderEvent{
		EventID: s.id("order_event"), OrderID: orderID, Type: "SUBMIT_ACKNOWLEDGED", Source: "reconciliation",
		ProviderOrderRef: providerOrderRef,
	})
}
