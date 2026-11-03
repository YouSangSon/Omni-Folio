package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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

func ownerDataErrorForTest(t *testing.T, h http.Handler, action string, raw []byte, status int, code string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/market-data/imports/"+action, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var result APIError
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || w.Code != status || result.Code != code || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected owner error: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-sentinel") {
		t.Fatal("private error leaked")
	}
}

func TestOwnerDataImportClosedBoundaryAndOrigin(t *testing.T) {
	svc := ownerPriceServiceForTest(t)
	before := ownerDataJournalDigestsForTest(t, svc)
	h := withCORS(svc.routes(), "http://localhost:3000")
	for _, raw := range [][]byte{
		nil, []byte(`{}`), []byte(ownerFXTestInput + `{}`), []byte(strings.Replace(ownerFXTestInput, `"sample":false`, `"sample":null`, 1)),
		[]byte(strings.Replace(ownerFXTestInput, `"sample":false`, `"sample":false,"s\u0061mple":true`, 1)),
		[]byte(strings.Replace(ownerFXTestInput, `"sample":false`, `"sample":false,"unexpected":true`, 1)),
		[]byte(strings.Replace(ownerFXTestInput, "manual-v1", `\ud800`, 1)), append([]byte(ownerFXTestInput), 0xff),
		[]byte(strings.Repeat(" ", maxBodyBytes) + ownerFXTestInput),
	} {
		for _, action := range []string{"preview", "apply"} {
			ownerDataErrorForTest(t, h, action, raw, 400, "owner_data_invalid")
		}
	}
	for _, origin := range []struct {
		values  []string
		allowed string
		want    int
	}{
		{nil, "", 200}, {[]string{""}, "", 403}, {[]string{"null"}, "", 403},
		{[]string{"http://localhost:3000"}, "", 403},
		{[]string{"http://localhost:3000"}, "http://localhost:3000", 200},
		{[]string{"https://evil.invalid"}, "http://localhost:3000", 403},
		{[]string{"http://localhost:3000", "http://localhost:3000"}, "http://localhost:3000", 403},
	} {
		req := httptest.NewRequest("POST", "/v1/market-data/imports/preview", strings.NewReader(ownerFXTestInput))
		req.Header.Set("Content-Type", "application/json")
		if origin.values != nil {
			req.Header["Origin"] = origin.values
		}
		w := httptest.NewRecorder()
		withCORS(svc.routes(), origin.allowed).ServeHTTP(w, req)
		if w.Code != origin.want {
			t.Fatalf("origin %v: %d", origin.values, w.Code)
		}
		if origin.want == http.StatusForbidden {
			req = httptest.NewRequest("POST", "/v1/market-data/imports/apply", strings.NewReader(ownerFXTestInput))
			req.Header.Set("Content-Type", "application/json")
			req.Header["Origin"] = origin.values
			w = httptest.NewRecorder()
			withCORS(svc.routes(), origin.allowed).ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatal("rejected browser origin reached apply")
			}
		}
	}
	for _, suffix := range []string{"?", "?unexpected=1"} {
		req := httptest.NewRequest("POST", "/v1/market-data/imports/preview"+suffix, strings.NewReader(ownerFXTestInput))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatal("query accepted")
		}
	}
	req := httptest.NewRequest("POST", "/v1/market-data/imports/preview", strings.NewReader(ownerFXTestInput))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal("non JSON content accepted")
	}
	req = httptest.NewRequest("OPTIONS", "/v1/market-data/imports/apply", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatal("preflight unavailable")
	}
	if before != ownerDataJournalDigestsForTest(t, svc) {
		t.Fatal("rejected requests changed storage")
	}
}

func TestOwnerDataImportAdmissionClockAndUncertainty(t *testing.T) {
	for _, kind := range []string{"security_price", "fx", "benchmark"} {
		t.Run(kind, func(t *testing.T) {
			svc := ownerPriceServiceForTest(t)
			h := withCORS(svc.routes(), "")
			raw := []byte(ownerPriceTestInput)
			if kind == "fx" {
				raw = []byte(ownerFXTestInput)
			}
			if kind == "benchmark" {
				raw = []byte(benchmarkSeriesTestInput)
			}
			future := bytes.ReplaceAll(raw, []byte("2026-01-01T00:00:00Z"), []byte("2026-01-02T00:00:00Z"))
			ownerDataRequestForTest(t, h, "preview", future)
			ownerDataErrorForTest(t, h, "apply", future, 400, "owner_data_invalid")
			first := ownerDataRequestForTest(t, h, "apply", raw)
			changed := bytes.Replace(raw, []byte(`"sample":false`), []byte(`"sample":true`), 1)
			if kind == "benchmark" {
				changed = bytes.Replace(raw, []byte(`"sample":true`), []byte(`"sample":false`), 1)
			}
			ownerDataErrorForTest(t, h, "apply", changed, 409, "owner_data_conflict")
			for _, clock := range []time.Time{time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), mustTime("2025-12-31T23:59:59Z")} {
				svc.now = func() time.Time { return clock }
				second := bytes.ReplaceAll(future, []byte("-v1"), []byte("-v2"))
				ownerDataErrorForTest(t, h, "apply", second, 500, "owner_data_unavailable")
				if retry := ownerDataRequestForTest(t, h, "apply", raw); !reflect.DeepEqual(first, retry) {
					t.Fatal("clock changed exact retry")
				}
			}
		})
	}
	t.Run("missing ledger instrument", func(t *testing.T) {
		svc := ownerFXServiceForTest(t)
		h := withCORS(svc.routes(), "")
		ownerDataRequestForTest(t, h, "preview", []byte(ownerPriceTestInput))
		ownerDataErrorForTest(t, h, "apply", []byte(ownerPriceTestInput), 400, "owner_data_invalid")
	})
	t.Run("legacy source conflict", func(t *testing.T) {
		svc := ownerPriceServiceForTest(t)
		recordXIRRFX(t, svc, "fixture", "1000", "2025-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
		ownerDataErrorForTest(t, withCORS(svc.routes(), ""), "apply", []byte(ownerFXTestInput), 409, "owner_data_conflict")
	})
	t.Run("preview is independent of database and clock", func(t *testing.T) {
		svc := ownerPriceServiceForTest(t)
		svc.db.Close()
		svc.now = func() time.Time { t.Fatal("preview called clock"); return time.Time{} }
		h := withCORS(svc.routes(), "")
		ownerDataRequestForTest(t, h, "preview", []byte(ownerFXTestInput))
		ownerDataErrorForTest(t, h, "apply", []byte(ownerFXTestInput), 500, "owner_data_unavailable")
	})
}

func TestOwnerDataImportWireGolden(t *testing.T) {
	var cases []struct {
		Preview map[string]any `json:"preview"`
		Result  map[string]any `json:"result"`
	}
	if err := json.Unmarshal(fixture(t, "golden-owner-data-import.json"), &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		svc := ownerPriceServiceForTest(t)
		h := withCORS(svc.routes(), "")
		raw := []byte(c.Preview["input"].(string))
		if actual := ownerDataRequestForTest(t, h, "preview", raw); !reflect.DeepEqual(actual, c.Preview) {
			t.Fatalf("producer preview differs from golden: %+v", actual)
		}
		if actual := ownerDataRequestForTest(t, h, "apply", raw); !reflect.DeepEqual(actual, c.Result) {
			t.Fatalf("producer receipt differs from golden: %+v", actual)
		}
	}
}

func TestOwnerDataImportEmptySeriesAndEscapedResponseBound(t *testing.T) {
	svc := ownerPriceServiceForTest(t)
	h := withCORS(svc.routes(), "")
	for _, raw := range [][]byte{
		ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.Points = []OwnerPricePoint{} }),
		ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.Points = []OwnerFXPoint{} }),
	} {
		p := ownerDataRequestForTest(t, h, "preview", raw)
		s := p["summary"].(map[string]any)
		if s["point_count"] != float64(0) || s["first_observed_at"] != nil || s["last_observed_at"] != nil {
			t.Fatal("empty withdrawal misrepresented")
		}
		ownerDataRequestForTest(t, h, "apply", raw)
	}
	raw := benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.Points = []BenchmarkIndexPoint{} })
	ownerDataErrorForTest(t, h, "preview", raw, 400, "owner_data_invalid")
	padded := append(bytes.Repeat([]byte("\t"), maxBodyBytes-len(ownerFXTestInput)), []byte(ownerFXTestInput)...)
	req := httptest.NewRequest("POST", "/v1/market-data/imports/preview", bytes.NewReader(padded))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.Len() > ownerDataPreviewResponseLimit || w.Body.Len() <= maxBodyBytes {
		t.Fatalf("escaped response bound: %d %d", w.Code, w.Body.Len())
	}
	var p OwnerDataImportPreview
	if json.Unmarshal(w.Body.Bytes(), &p) != nil || p.Input != string(padded) {
		t.Fatal("full-size raw echo changed")
	}
}

func TestOwnerDataImportOpenAPIClosedWire(t *testing.T) {
	var doc map[string]any
	if json.Unmarshal(fixture(t, "../openapi.json"), &doc) != nil {
		t.Fatal("invalid OpenAPI")
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	var cases []map[string]any
	if json.Unmarshal(fixture(t, "golden-owner-data-import.json"), &cases) != nil {
		t.Fatal("invalid wire fixture")
	}
	for _, c := range cases {
		p := c["preview"].(map[string]any)
		kind := p["kind"].(string)
		names := map[string]string{"security_price": "SecurityPrice", "fx": "Fx", "benchmark": "Benchmark"}
		schema := schemas["OwnerData"+names[kind]+"Summary"].(map[string]any)
		if schema["additionalProperties"] != false || !reflect.DeepEqual(jsonStringSet(schema["required"]), jsonKeySet(p["summary"].(map[string]any))) {
			t.Fatal("review contract differs from producer")
		}
	}
	for _, action := range []string{"preview", "apply"} {
		if doc["paths"].(map[string]any)["/v1/market-data/imports/"+action] == nil {
			t.Fatal("missing documented action")
		}
	}
}

type ownerDataFailedWriter struct{ header http.Header }

func (w *ownerDataFailedWriter) Header() http.Header { return w.header }
func (w *ownerDataFailedWriter) WriteHeader(int)     {}
func (w *ownerDataFailedWriter) Write([]byte) (int, error) {
	return 0, errors.New("private-sentinel connection lost")
}

func TestOwnerDataImportLostReceiptDoesNotUndoCommit(t *testing.T) {
	svc := ownerPriceServiceForTest(t)
	h := withCORS(svc.routes(), "")
	req := httptest.NewRequest("POST", "/v1/market-data/imports/apply", strings.NewReader(ownerFXTestInput))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(&ownerDataFailedWriter{header: make(http.Header)}, req)
	proof, err := proveOwnerFXSeriesRecovery(context.Background(), svc.db)
	if err != nil || proof.Versions != 1 {
		t.Fatalf("failed response lost committed journal: %+v %v", proof, err)
	}
	svc.now = func() time.Time { t.Fatal("lost response retry read clock"); return time.Time{} }
	ownerDataRequestForTest(t, h, "apply", []byte(ownerFXTestInput))
}

func TestOwnerDataImportBrokenProofIsNotAConfirmedRejection(t *testing.T) {
	svc := ownerPriceServiceForTest(t)
	if _, err := svc.db.Exec(`DROP TRIGGER owner_fx_series_versions_no_update`); err != nil {
		t.Fatal(err)
	}
	ownerDataErrorForTest(t, withCORS(svc.routes(), ""), "apply", []byte(ownerFXTestInput), 500, "owner_data_unavailable")
}
