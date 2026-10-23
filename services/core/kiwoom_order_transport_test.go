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

type fakeLimitOrderDispatchPort struct {
	preflightCalls int
	submitCalls    int
	submitted      OrderIntent
	preflightErr   error
	submitRef      string
	submitErr      error
	beforeSubmit   func()
}

func (f *fakeLimitOrderDispatchPort) Preflight(context.Context) error {
	f.preflightCalls++
	return f.preflightErr
}

func (f *fakeLimitOrderDispatchPort) SubmitLimitOrder(_ context.Context, intent OrderIntent) (string, error) {
	f.submitCalls++
	f.submitted = intent
	if f.beforeSubmit != nil {
		f.beforeSubmit()
	}
	return f.submitRef, f.submitErr
}

func TestAuthorizedLimitOrderUsesProviderNeutralPortAndRedactsUnknownOutcome(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	now := mustTime("2026-01-10T15:00:00Z")
	svc.now = func() time.Time { return now }
	order := mustRecordK2AOrder(t, svc, "client-port-unknown")
	lease := mustK2CLease(t, svc, order.AccountRef)
	fake := &fakeLimitOrderDispatchPort{submitErr: errors.New("raw broker transport detail")}
	fake.beforeSubmit = func() {
		durable, err := svc.loadOrderState(context.Background(), order.OrderID)
		if err != nil || durable.Status != "SUBMIT_UNKNOWN" || durable.PendingAction != "SUBMIT" {
			t.Fatalf("transport ran before durable dispatch: state=%+v err=%v", durable, err)
		}
	}
	port := mustLimitOrderDispatchPort(t, "kiwoom", "synthetic", fake)

	state, err := svc.submitAuthorizedLimitOrder(context.Background(), port, order.OrderID, lease.FencingToken)
	if err == nil || strings.Contains(err.Error(), "raw broker transport detail") {
		t.Fatalf("unknown provider detail escaped: %v", err)
	}
	if state == nil || state.Status != "SUBMIT_UNKNOWN" || state.PendingAction != "SUBMIT" {
		t.Fatalf("unknown submit state=%+v", state)
	}
	if fake.preflightCalls != 1 || fake.submitCalls != 1 || fake.submitted.ClientOrderID != "client-port-unknown" {
		t.Fatalf("port calls preflight=%d submit=%d intent=%+v", fake.preflightCalls, fake.submitCalls, fake.submitted)
	}

	if _, err := svc.submitAuthorizedLimitOrder(context.Background(), port, order.OrderID, lease.FencingToken); err == nil {
		t.Fatal("repeat submit unexpectedly succeeded")
	}
	if fake.submitCalls != 1 {
		t.Fatalf("unknown submit was retried: calls=%d", fake.submitCalls)
	}
}

func TestAuthorizedLimitOrderRejectsZeroAndMismatchedCapabilitiesBeforePreflight(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	order := mustRecordK2AOrder(t, svc, "client-port-mismatch")
	lease := mustK2CLease(t, svc, order.AccountRef)
	fake := &fakeLimitOrderDispatchPort{}
	mismatched := mustLimitOrderDispatchPort(t, "toss", "synthetic", fake)

	for name, port := range map[string]LimitOrderDispatchPort{
		"zero": {}, "provider mismatch": mismatched,
	} {
		t.Run(name, func(t *testing.T) {
			state, err := svc.submitAuthorizedLimitOrder(context.Background(), port, order.OrderID, lease.FencingToken)
			if err == nil || state != nil {
				t.Fatalf("invalid capability entered submit: state=%+v err=%v", state, err)
			}
		})
	}
	if fake.preflightCalls != 0 || fake.submitCalls != 0 {
		t.Fatalf("mismatched capability was called: preflight=%d submit=%d", fake.preflightCalls, fake.submitCalls)
	}
	state, err := svc.loadOrderState(context.Background(), order.OrderID)
	if err != nil || state.Status != "RECORDED" {
		t.Fatalf("invalid capability mutated order: state=%+v err=%v", state, err)
	}
}

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
	dispatch := mustKiwoomMockLimitOrderDispatch(t, client)
	state, err := svc.submitAuthorizedLimitOrder(context.Background(), dispatch, order.OrderID, lease.FencingToken)
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
	if _, err := svc.submitAuthorizedLimitOrder(context.Background(), dispatch, order.OrderID, lease.FencingToken); err == nil || script.calls != 2 {
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
		dispatch := mustKiwoomMockLimitOrderDispatch(t, client)
		state, err := svc.submitAuthorizedLimitOrder(context.Background(), dispatch, order.OrderID, lease.FencingToken)
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
		dispatch := mustKiwoomMockLimitOrderDispatch(t, client)
		state, err := svc.submitAuthorizedLimitOrder(context.Background(), dispatch, order.OrderID, lease.FencingToken)
		if err == nil || strings.Contains(err.Error(), "raw transport") || state == nil || state.Status != "SUBMIT_UNKNOWN" || state.PendingAction != "SUBMIT" || calls != 2 {
			t.Fatalf("unknown submit state=%+v calls=%d err=%v", state, calls, err)
		}
		if _, err := svc.submitAuthorizedLimitOrder(context.Background(), dispatch, order.OrderID, lease.FencingToken); err == nil || calls != 2 {
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
		dispatch := mustKiwoomMockLimitOrderDispatch(t, client)
		state, err := svc.submitAuthorizedLimitOrder(context.Background(), dispatch, order.OrderID, lease.FencingToken)
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
		dispatch := mustKiwoomMockLimitOrderDispatch(t, client)
		if state, err := svc.submitAuthorizedLimitOrder(context.Background(), dispatch, order.OrderID, lease.FencingToken); err == nil || state != nil || strings.Contains(err.Error(), "synthetic token outage") {
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
		if dispatch, err := newKiwoomMockLimitOrderDispatch(client); err == nil || dispatch.valid() {
			t.Fatalf("production client created mock dispatch: dispatch=%+v err=%v", dispatch, err)
		}
		if dispatch, err := newKiwoomMockLimitOrderDispatch(nil); err == nil || dispatch.valid() {
			t.Fatalf("nil client created mock dispatch: dispatch=%+v err=%v", dispatch, err)
		}
		state, err := svc.loadOrderState(context.Background(), order.OrderID)
		if err != nil || state.Status != "RECORDED" {
			t.Fatalf("rejected adapter construction mutated order: state=%+v err=%v", state, err)
		}
	})
}

func mustKiwoomMockLimitOrderDispatch(t *testing.T, client *KiwoomClient) LimitOrderDispatchPort {
	t.Helper()
	dispatch, err := newKiwoomMockLimitOrderDispatch(client)
	if err != nil {
		t.Fatal(err)
	}
	return dispatch
}

func mustLimitOrderDispatchPort(t *testing.T, provider, mode string, fake *fakeLimitOrderDispatchPort) LimitOrderDispatchPort {
	t.Helper()
	dispatch, err := newLimitOrderDispatchPort(provider, mode, fake.Preflight, fake.SubmitLimitOrder)
	if err != nil {
		t.Fatal(err)
	}
	return dispatch
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
