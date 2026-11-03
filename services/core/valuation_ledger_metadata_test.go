package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValuationDuplicateImportKeepsMetadataContract(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	csv := testCSV(
		"krw,account-main,DEPOSIT,2026-01-10T09:00:00Z,,,,,KRW,1000",
		"usd,account-main,DEPOSIT,2026-01-10T09:01:00Z,,,,,USD,100",
		"aapl-buy,account-main,BUY,2026-01-10T10:00:00Z,AAPL,3,10,1,USD,-31",
	)
	applyTestCSV(t, svc, "initial", csv)
	recordCurrentPortfolioFX(t, svc, "usd-krw", "USD", "KRW", "1370", "2026-01-11T13:00:00Z")
	recordHoldingValuationPrice(t, svc, "aapl-xnas", "XNAS", "15", "2026-01-11T13:00:00Z", "2026-01-11T13:00:01Z", "2026-01-11T13:00:02Z")
	paths := []string{
		"/v1/portfolio/cash-valuation?base_currency=KRW&as_of=" + currentPortfolioValuationAsOf,
		"/v1/portfolio/holding-valuation?as_of=" + currentPortfolioValuationAsOf,
		"/v1/portfolio/current-valuation",
	}
	read := func(t *testing.T, path string) map[string]any {
		t.Helper()
		svc.now = func() time.Time { return mustTime(currentPortfolioValuationAsOf) }
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("valuation status=%d body=%s", w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	before := make([]map[string]any, len(paths))
	for i, path := range paths {
		before[i] = read(t, path)
		if before[i]["status"] != "stale_sample" || before[i]["ledger_revision"] != "rev_0000000003" {
			t.Fatalf("expected complete sample valuation: %v", before[i])
		}
	}
	const duplicateAt = "2026-01-11T13:30:00Z"
	svc.now = func() time.Time { return mustTime(duplicateAt) }
	applyTestCSV(t, svc, "duplicate", csv)
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil || snapshot.RecordedAt != duplicateAt || snapshot.LedgerRevision != "rev_0000000003" {
		t.Fatalf("duplicate metadata contract changed: %+v %v", snapshot, err)
	}
	for i, path := range paths {
		t.Run(path, func(t *testing.T) {
			after := read(t, path)
			if after["ledger_recorded_at"] != duplicateAt {
				t.Fatalf("metadata timestamp lost: %v", after)
			}
			before[i]["ledger_recorded_at"] = duplicateAt
			if !reflect.DeepEqual(before[i], after) {
				t.Fatalf("duplicate import changed valuation: before=%v after=%v", before[i], after)
			}
		})
	}
	afterReads, err := snapshotFrom(context.Background(), svc.db)
	if err != nil || !reflect.DeepEqual(snapshot, afterReads) {
		t.Fatalf("valuation mutated snapshot: %+v %v", afterReads, err)
	}
	const beforeMetadata = "2026-01-11T13:29:59.999999999Z"
	svc.now = func() time.Time { return mustTime(beforeMetadata) }
	for i, path := range paths {
		t.Run("metadata cutoff "+path, func(t *testing.T) {
			w := httptest.NewRecorder()
			svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, strings.ReplaceAll(path, currentPortfolioValuationAsOf, beforeMetadata), nil))
			want := http.StatusConflict
			if i == 2 {
				want = http.StatusInternalServerError
			}
			if w.Code != want {
				t.Fatalf("metadata cutoff status=%d want=%d body=%s", w.Code, want, w.Body.String())
			}
		})
	}
}

func TestValuationRejectsFutureRecordedEventsAfterClockRollback(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	first := testCSV("first,account-main,DEPOSIT,2026-01-10T00:00:00Z,,,,,KRW,1000")
	svc.now = func() time.Time { return mustTime("2026-01-11T00:00:00.9Z") }
	applyTestCSV(t, svc, "first", first)
	svc.now = func() time.Time { return mustTime("2026-01-11T00:00:00Z") }
	applyTestCSV(t, svc, "second", testCSV("second,account-main,DEPOSIT,2026-01-10T01:00:00Z,,,,,KRW,1"))
	svc.now = func() time.Time { return mustTime("2026-01-11T00:00:00.05Z") }
	applyTestCSV(t, svc, "duplicate", first)
	for _, asOf := range []string{"2026-01-11T00:00:00.899999999Z", "2026-01-11T00:00:00.9Z"} {
		svc.now = func() time.Time { return mustTime(asOf) }
		for _, tc := range []struct {
			path   string
			denied int
		}{
			{"/v1/portfolio/cash-valuation?base_currency=KRW&as_of=" + asOf, http.StatusConflict},
			{"/v1/portfolio/holding-valuation?as_of=" + asOf, http.StatusConflict},
			{"/v1/portfolio/current-valuation", http.StatusInternalServerError},
		} {
			t.Run(asOf+tc.path, func(t *testing.T) {
				w := httptest.NewRecorder()
				svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
				want := tc.denied
				if asOf == "2026-01-11T00:00:00.9Z" {
					want = http.StatusOK
				}
				if w.Code != want {
					t.Fatalf("cutoff status=%d want=%d body=%s", w.Code, want, w.Body.String())
				}
				if want == http.StatusInternalServerError && w.Body.String() != "{\"code\":\"internal_error\",\"message\":\"internal server error\"}\n" {
					t.Fatalf("clock failure was not redacted: %s", w.Body.String())
				}
			})
		}
	}
}
