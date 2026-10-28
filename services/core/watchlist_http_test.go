package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWatchlistStartsEmptyWithoutChangingTheLedger(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/watchlist", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("watchlist unavailable: status=%d body=%s", w.Code, w.Body.String())
	}
}
