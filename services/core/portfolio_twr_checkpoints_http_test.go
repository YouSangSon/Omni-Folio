package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPortfolioTWRCheckpointsExposeTheSameObservation(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	applyTestCSV(t, svc, "twr-checkpoints", testCSV(
		"first,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100",
		"gain-one,account-main,DIVIDEND,2025-06-30T00:00:00Z,AAPL,,,,KRW,10",
		"second,account-main,DEPOSIT,2025-07-01T00:00:00Z,,,,,KRW,100",
		"gain-two,account-main,DIVIDEND,2026-01-01T00:00:00Z,AAPL,,,,KRW,21",
	))
	clocks := 0
	svc.now = func() time.Time { clocks++; return mustTime("2026-01-01T00:00:00Z") }
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/twr/checkpoints", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("TWR checkpoints unavailable: status=%d body=%s", w.Code, w.Body.String())
	}
	if clocks != 1 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("checkpoint observation did not retain one server cutoff and no-store")
	}
	var wire map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(fixture(t, "golden-portfolio-twr.json"), &report); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"policy_version": "twr_cash_flow_checkpoints.v1",
		"report":         report,
		"points": []any{
			map[string]any{"at": "2025-01-01T00:00:00Z", "kind": "external_flow", "cumulative_rate": "0"},
			map[string]any{"at": "2025-07-01T00:00:00Z", "kind": "external_flow", "cumulative_rate": "0.1"},
			map[string]any{"at": "2026-01-01T00:00:00Z", "kind": "valuation", "cumulative_rate": "0.21"},
		},
	}
	if !reflect.DeepEqual(wire, want) {
		t.Fatalf("cash contributions became plotted return or scalar/curve observation drifted: %s", w.Body.String())
	}
	old := httptest.NewRecorder()
	svc.routes().ServeHTTP(old, httptest.NewRequest("GET", "/v1/portfolio/twr", nil))
	var scalar map[string]any
	if old.Code != 200 || json.Unmarshal(old.Body.Bytes(), &scalar) != nil || !reflect.DeepEqual(scalar, report) {
		t.Fatal("existing scalar TWR contract changed")
	}
}

func twrCheckpointsHTTP(t *testing.T, svc *Service) *PortfolioTWRCheckpoints {
	t.Helper()
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/portfolio/twr/checkpoints", nil))
	if w.Code != 200 || w.Body.Len() > twrCheckpointResponseLimit || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("checkpoint HTTP %d %s", w.Code, w.Body.String())
	}
	for _, private := range []string{"account-main", "source_event_id", "receipt_id", "observation_id", "instrument_id"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("private field leaked")
		}
	}
	var result PortfolioTWRCheckpoints
	if json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("invalid checkpoint response")
	}
	return &result
}

func TestPortfolioTWRCheckpointsSharedNullableStates(t *testing.T) {
	var wire struct{ Reports map[string]json.RawMessage }
	if json.Unmarshal(fixture(t, "golden-portfolio-twr-checkpoints.json"), &wire) != nil {
		t.Fatal("invalid fixture")
	}
	for name, want := range wire.Reports {
		t.Run(name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
			switch name {
			case "compound":
				applyTestCSV(t, svc, "twr-checkpoints", testCSV("first,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100", "gain-one,account-main,DIVIDEND,2025-06-30T00:00:00Z,AAPL,,,,KRW,10", "second,account-main,DEPOSIT,2025-07-01T00:00:00Z,,,,,KRW,100", "gain-two,account-main,DIVIDEND,2026-01-01T00:00:00Z,AAPL,,,,KRW,21"))
			case "empty":
			case "zero_return", "total_loss", "full_withdrawal":
				rows := []string{"capital,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100"}
				if name == "total_loss" {
					rows = append(rows, "loss,account-main,FEE,2026-01-01T00:00:00Z,,,,,KRW,-100")
				}
				if name == "full_withdrawal" {
					rows = append(rows, "exit,account-main,WITHDRAWAL,2025-07-01T00:00:00Z,,,,,KRW,-100")
				}
				applyTestCSV(t, svc, "twr-nullable", testCSV(rows...))
			default:
				applyTestCSV(t, svc, "twr-nullable", testCSV("capital,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,USD,100"))
			}
			if name == "historical_fx_unavailable" {
				recordXIRRFX(t, svc, "current", "1100", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
			}
			actual, _ := json.Marshal(twrCheckpointsHTTP(t, svc))
			var a, b any
			if json.Unmarshal(actual, &a) != nil || json.Unmarshal(want, &b) != nil || !reflect.DeepEqual(a, b) {
				t.Fatalf("producer differs from shared checkpoint wire: %s", actual)
			}
		})
	}
}

func TestPortfolioTWRCheckpointsPhasesAndLateFailure(t *testing.T) {
	t.Run("same time flow then fee", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
		applyTestCSV(t, svc, "same", testCSV("a,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,KRW,100", "b,account-main,FEE,2026-01-01T00:00:00Z,,,,,KRW,-1"))
		r := twrCheckpointsHTTP(t, svc)
		if r.Report.Status != "calculated" || len(r.Points) != 2 || r.Points[0].At != r.Points[1].At || r.Points[0].Kind != "external_flow" || r.Points[1].Kind != "valuation" || r.Points[0].CumulativeRate != "0" || r.Points[1].CumulativeRate != "-0.01" {
			t.Fatalf("same-time phases collapsed: %+v", r)
		}
	})
	t.Run("one nanosecond flow boundaries", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00.000000002Z") }
		applyTestCSV(t, svc, "nano", testCSV("a,account-main,DEPOSIT,2026-01-01T00:00:00.000000001Z,,,,,KRW,100", "b,account-main,DEPOSIT,2026-01-01T00:00:00.000000002Z,,,,,KRW,100"))
		r := twrCheckpointsHTTP(t, svc)
		if len(r.Points) != 3 || r.Points[0].At == r.Points[1].At || r.Points[1].At != r.Points[2].At {
			t.Fatal("nanosecond/terminal ordering changed")
		}
	})
	t.Run("late missing input discards prefix", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
		applyTestCSV(t, svc, "late", testCSV("a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,USD,100", "b,account-main,DEPOSIT,2025-07-01T00:00:00Z,,,,,KRW,100"))
		recordXIRRFX(t, svc, "first", "1000", "2025-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
		recordXIRRFX(t, svc, "current", "1100", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
		r := twrCheckpointsHTTP(t, svc)
		if r.Report.Status != "unavailable" || len(r.Points) != 0 || r.Report.BoundaryCount == nil || *r.Report.BoundaryCount != 2 {
			t.Fatal("partial prefix masqueraded as full curve")
		}
	})
	t.Run("positive prefix rounded to minus one recovers", func(t *testing.T) {
		svc, _ := testService(t, nil, nil)
		svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
		applyTestCSV(t, svc, "nearzero", testCSV("a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,1", "b,account-main,FEE,2025-02-01T00:00:00Z,,,,,KRW,-0.9999999999996", "c,account-main,DEPOSIT,2025-03-01T00:00:00Z,,,,,KRW,0.9999999999996", "d,account-main,DIVIDEND,2026-01-01T00:00:00Z,AAPL,,,,KRW,2499999999999"))
		r := twrCheckpointsHTTP(t, svc)
		if r.Report.Status != "calculated" || len(r.Points) != 3 || r.Points[1].CumulativeRate != "-1" || r.Points[2].CumulativeRate != "0" {
			t.Fatalf("rounding became capital depletion: %+v", r)
		}
	})
}

func TestPortfolioTWRCheckpointsHugePrefixPreservesScalar(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	rows := []string{"start,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,1"}
	magnitude := strings.Repeat("9", 32)
	for i := 0; i < 16; i++ {
		day := mustTime("2025-01-02T00:00:00Z").Add(time.Duration(i) * 24 * time.Hour)
		rows = append(rows, fmt.Sprintf("gain%d,account-main,DIVIDEND,%s,AAPL,,,,KRW,%s", i, day.Format(time.RFC3339Nano), magnitude), fmt.Sprintf("withdraw%d,account-main,WITHDRAWAL,%s,,,,,KRW,-%s", i, day.Add(time.Hour).Format(time.RFC3339Nano), magnitude))
	}
	rows = append(rows, "loss,account-main,FEE,2026-01-01T00:00:00Z,,,,,KRW,-1")
	applyTestCSV(t, svc, "huge", testCSV(rows...))
	scalar := twrHTTPReport(t, svc)
	r := twrCheckpointsHTTP(t, svc)
	if r.Report.Status != "calculated" || len(r.Points) != 18 || r.Points[16].CumulativeRate != strings.Repeat("9", 512) || r.Points[17].CumulativeRate != "-1" || !reflect.DeepEqual(r.Report, scalar) {
		t.Fatalf("prefix limit changed scalar authority: %+v", r.Report)
	}
}

func TestPortfolioTWRCheckpointsBoundsAndReadFailure(t *testing.T) {
	for _, count := range []int{1000, 1001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
			rows := []string{}
			for i := 0; i < count; i++ {
				rows = append(rows, fmt.Sprintf("flow%d,account-main,DEPOSIT,%s,,,,,KRW,1", i, mustTime("2025-01-01T00:00:00Z").Add(time.Duration(i)).Format(time.RFC3339Nano)))
			}
			applyTestCSV(t, svc, "bound", testCSV(rows...))
			r := twrCheckpointsHTTP(t, svc)
			if count == 1000 && (r.Report.Status != "calculated" || len(r.Points) != 1001) {
				t.Fatal("valid checkpoint bound rejected")
			}
			if count == 1001 && (r.Report.Status != "unavailable" || len(r.Points) != 0) {
				t.Fatal("checkpoint bound bypassed")
			}
		})
	}
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { t.Fatal("query read clock"); return time.Time{} }
	for _, query := range []string{"?", "?range=1d"} {
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/portfolio/twr/checkpoints"+query, nil))
		if w.Code != 400 {
			t.Fatal("query accepted")
		}
	}
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	svc.db.Close()
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/portfolio/twr/checkpoints", nil))
	if w.Code != 500 || strings.Contains(w.Body.String(), "sql:") {
		t.Fatal("database failure not redacted")
	}
}
