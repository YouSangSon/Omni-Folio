package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPaperPromotionReportEmptyReadOnlyObservation(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/paper/promotion-report", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("paper promotion report status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	attempt, present := body["attempt"]
	if body["schema_version"] != "paper-promotion-report.v1" || body["mode"] != "paper_comparison_only" || !present || attempt != nil {
		t.Fatalf("empty report claimed a comparison: %v", body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("paper promotion report must not be cached")
	}
}
