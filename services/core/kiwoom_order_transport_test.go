package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestK2B2MockLimitSubmitAcknowledgesOnceAndRedactsProviderIdentity(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	now := mustTime("2026-01-10T15:00:00Z")
	svc.now = func() time.Time { return now }
	order := mustRecordK2AOrder(t, svc, "client-mock-submit")
	lease := mustK2CLease(t, svc, order.AccountRef)
	const rawOrderNumber = "0000024"

	script := &kiwoomScript{t: t}
	script.steps = []func(*http.Request) *http.Response{
		func(request *http.Request) *http.Response {
			assertKiwoomMockRequest(t, request, kiwoomTokenPath, "")
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("synthetic-mock-token"))
		},
		func(request *http.Request) *http.Response {
			assertKiwoomMockRequest(t, request, kiwoomOrderPath, "kt10000")
			assertKiwoomAuthorization(t, request, "synthetic-mock-token")
			assertNoKiwoomCursor(t, request)
			var body map[string]string
			decodeKiwoomRequest(t, request, &body)
			want := map[string]string{
				"dmst_stex_tp": "KRX", "stk_cd": "005930", "ord_qty": "10",
				"ord_uv": "1000", "trde_tp": "0", "cond_uv": "",
			}
			if encoded, expected := mustJSON(t, body), mustJSON(t, want); encoded != expected {
				t.Fatalf("mock submit body=%s want=%s", encoded, expected)
			}
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "kt10000"},
				`{"ord_no":"`+rawOrderNumber+`","dmst_stex_tp":"KRX","return_code":0,"return_msg":"synthetic accepted"}`)
		},
	}
	client := newSyntheticKiwoomClient(t, KiwoomMock, script)
	state, err := svc.submitAuthorizedKiwoomMockOrder(context.Background(), client, order.OrderID, lease.FencingToken)
	if err != nil || state.Status != "OPEN" || state.PendingAction != "" || len(state.ProviderOrderRefs) != 1 {
		t.Fatalf("mock submit state=%+v err=%v", state, err)
	}
	if script.calls != 2 {
		t.Fatalf("requests=%d want=2", script.calls)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var storedEvents string
	if err := svc.db.QueryRow(`SELECT GROUP_CONCAT(event_json, '') FROM order_events WHERE order_id=?`, order.OrderID).Scan(&storedEvents); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{rawOrderNumber, "synthetic accepted", "synthetic-mock-token"} {
		if strings.Contains(string(encoded), forbidden) || strings.Contains(storedEvents, forbidden) {
			t.Fatalf("mock submit leaked %q: state=%s events=%s", forbidden, encoded, storedEvents)
		}
	}
	if _, err := svc.submitAuthorizedKiwoomMockOrder(context.Background(), client, order.OrderID, lease.FencingToken); err == nil || script.calls != 2 {
		t.Fatalf("repeat submit reached transport or succeeded: requests=%d err=%v", script.calls, err)
	}
}

func TestK2B2MockLimitSubmitDistinguishesRejectUnknownAndPreflightFailure(t *testing.T) {
	t.Run("definitive provider rejection", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		now := mustTime("2026-01-10T15:00:00Z")
		svc.now = func() time.Time { return now }
		order := mustRecordK2AOrder(t, svc, "client-mock-rejected")
		lease := mustK2CLease(t, svc, order.AccountRef)
		client := newSyntheticKiwoomClient(t, KiwoomMock, &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
			func(*http.Request) *http.Response {
				return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("synthetic-token"))
			},
			func(*http.Request) *http.Response {
				return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "kt10000"},
					`{"return_code":42,"return_msg":"never persist this provider rejection"}`)
			},
		}})
		state, err := svc.submitAuthorizedKiwoomMockOrder(context.Background(), client, order.OrderID, lease.FencingToken)
		if err != nil || state.Status != "REJECTED" || state.PendingAction != "" {
			t.Fatalf("provider rejection state=%+v err=%v", state, err)
		}
	})

	t.Run("network outcome remains unknown without retry", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		now := mustTime("2026-01-10T15:00:00Z")
		svc.now = func() time.Time { return now }
		order := mustRecordK2AOrder(t, svc, "client-mock-unknown")
		lease := mustK2CLease(t, svc, order.AccountRef)
		calls := 0
		client := newSyntheticKiwoomClient(t, KiwoomMock, kiwoomRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if request.URL.Path == kiwoomTokenPath {
				return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("synthetic-token")), nil
			}
			return nil, errors.New("raw transport detail must not escape")
		}))
		state, err := svc.submitAuthorizedKiwoomMockOrder(context.Background(), client, order.OrderID, lease.FencingToken)
		if err == nil || strings.Contains(err.Error(), "raw transport") || state == nil || state.Status != "SUBMIT_UNKNOWN" || state.PendingAction != "SUBMIT" || calls != 2 {
			t.Fatalf("unknown submit state=%+v calls=%d err=%v", state, calls, err)
		}
		if _, err := svc.submitAuthorizedKiwoomMockOrder(context.Background(), client, order.OrderID, lease.FencingToken); err == nil || calls != 2 {
			t.Fatalf("unknown submit was retried: calls=%d err=%v", calls, err)
		}
	})

	t.Run("write auth failure is never retried", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		now := mustTime("2026-01-10T15:00:00Z")
		svc.now = func() time.Time { return now }
		order := mustRecordK2AOrder(t, svc, "client-mock-auth-unknown")
		lease := mustK2CLease(t, svc, order.AccountRef)
		script := &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
			func(*http.Request) *http.Response {
				return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("synthetic-token"))
			},
			func(*http.Request) *http.Response {
				return kiwoomResponse(http.StatusUnauthorized, nil, `{"return_code":8005,"return_msg":"do not retry a write"}`)
			},
		}}
		client := newSyntheticKiwoomClient(t, KiwoomMock, script)
		state, err := svc.submitAuthorizedKiwoomMockOrder(context.Background(), client, order.OrderID, lease.FencingToken)
		if err == nil || state == nil || state.Status != "SUBMIT_UNKNOWN" || script.calls != 2 {
			t.Fatalf("write auth failure was retried or resolved: state=%+v calls=%d err=%v", state, script.calls, err)
		}
	})

	t.Run("token preflight failure leaves order recorded", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		now := mustTime("2026-01-10T15:00:00Z")
		svc.now = func() time.Time { return now }
		order := mustRecordK2AOrder(t, svc, "client-mock-preflight")
		lease := mustK2CLease(t, svc, order.AccountRef)
		client := newSyntheticKiwoomClient(t, KiwoomMock, kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("synthetic token outage")
		}))
		if state, err := svc.submitAuthorizedKiwoomMockOrder(context.Background(), client, order.OrderID, lease.FencingToken); err == nil || state != nil {
			t.Fatalf("token preflight unexpectedly dispatched: state=%+v err=%v", state, err)
		}
		state, err := svc.loadOrderState(context.Background(), order.OrderID)
		if err != nil || state.Status != "RECORDED" {
			t.Fatalf("preflight failure mutated order: state=%+v err=%v", state, err)
		}
	})

	t.Run("production client is rejected before transport", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		order := mustRecordK2AOrder(t, svc, "client-production-block")
		client := newSyntheticKiwoomClient(t, KiwoomProduction, kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("production transport was called")
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		}))
		if state, err := svc.submitAuthorizedKiwoomMockOrder(context.Background(), client, order.OrderID, 1); err == nil || state != nil {
			t.Fatalf("production client entered mock submit: state=%+v err=%v", state, err)
		}
	})
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func assertKiwoomMockRequest(t *testing.T, request *http.Request, path, apiID string) {
	t.Helper()
	if request.Method != http.MethodPost || request.URL.Scheme != "https" || request.URL.Host != "mockapi.kiwoom.com" || request.URL.Path != path {
		t.Fatalf("unexpected mock request target: %s %s", request.Method, request.URL)
	}
	if request.Header.Get("api-id") != apiID {
		t.Fatalf("api-id=%q want=%q", request.Header.Get("api-id"), apiID)
	}
}
