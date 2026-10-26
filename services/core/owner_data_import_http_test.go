package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func ownerDataRequestForTest(t *testing.T, handler http.Handler, action string, raw []byte) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/market-data/imports/"+action, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("owner data %s unavailable: status=%d body=%s", action, w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("owner input response may be cached")
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func ownerDataJournalDigestsForTest(t *testing.T, svc *Service) [3]string {
	t.Helper()
	ctx := context.Background()
	price, err := proveOwnerPriceSeriesRecovery(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	fx, err := proveOwnerFXSeriesRecovery(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	benchmark, err := proveBenchmarkSeriesRecovery(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	return [3]string{price.SHA256, fx.SHA256, benchmark.SHA256}
}

func TestOwnerDataImportHTTPReviewApplyAndExactRetry(t *testing.T) {
	svc := ownerPriceServiceForTest(t)
	handler := withCORS(svc.routes(), "")
	for _, input := range []struct {
		kind string
		raw  []byte
	}{
		{"security_price", []byte(ownerPriceTestInput)},
		{"fx", []byte(ownerFXTestInput)},
		{"benchmark", []byte(benchmarkSeriesTestInput)},
		{"benchmark", benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.BenchmarkID = "usd_reference"; i.Currency = "USD" })},
	} {
		before := ownerDataJournalDigestsForTest(t, svc)
		preview := ownerDataRequestForTest(t, handler, "preview", input.raw)
		if preview["kind"] != input.kind || preview["input"] != string(input.raw) {
			t.Fatal("file review lost its request identity")
		}
		metadata := preview["summary"].(map[string]any)
		if metadata["storage_check"] != nil || metadata["recorded_at"] != nil || metadata["point_count"] != float64(2) {
			t.Fatal("input review claimed storage or changed points")
		}
		if after := ownerDataJournalDigestsForTest(t, svc); before != after {
			t.Fatal("review changed the stored journal")
		}
		stored := ownerDataRequestForTest(t, handler, "apply", input.raw)
		receipt := stored["receipt"].(map[string]any)
		if stored["kind"] != input.kind || receipt["storage_check"] != "verified" || receipt["recorded_at"] == nil {
			t.Fatal("stored receipt lost authority")
		}
		for key, value := range metadata {
			if !reflect.DeepEqual(receipt[key], value) {
				t.Fatalf("receipt differs from reviewed input: %s", key)
			}
		}
		clock := svc.now
		svc.now = func() time.Time { t.Fatal("exact retry called the record clock"); return time.Time{} }
		retry := ownerDataRequestForTest(t, handler, "apply", input.raw)
		svc.now = clock
		if !reflect.DeepEqual(retry, stored) {
			t.Fatal("exact HTTP retry changed its receipt")
		}
	}
	value, err := svc.currentPortfolioValuation(context.Background())
	if err != nil || value.TotalValue == nil || value.TotalValue.Amount != "180000" || value.Sample || !usesOwnerPrice(value.Issues) || !usesOwnerFX(value.Issues) {
		t.Fatalf("HTTP input did not reach the portfolio value: %+v %v", value, err)
	}
	comparison, err := svc.portfolioBenchmarks(context.Background())
	if err != nil || len(comparison.Benchmarks) != 2 {
		t.Fatalf("both benchmark currencies did not reach comparison: %+v %v", comparison, err)
	}
}
