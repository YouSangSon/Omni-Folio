package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPortfolioTWRNeutralizesAdditionalCapital(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	applyTestCSV(t, svc, "twr-capital", testCSV(
		"first,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100",
		"gain-one,account-main,DIVIDEND,2025-06-30T00:00:00Z,AAPL,,,,KRW,10",
		"second,account-main,DEPOSIT,2025-07-01T00:00:00Z,,,,,KRW,100",
		"gain-two,account-main,DIVIDEND,2026-01-01T00:00:00Z,AAPL,,,,KRW,21",
	))
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/twr", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("TWR report unavailable: HTTP %d %s", w.Code, w.Body.String())
	}
	var r map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r["status"] != "calculated" || r["cumulative_rate"] != "0.21" {
		t.Fatalf("capital became profit or subperiods were not compounded: %s", w.Body.String())
	}
	data, err := os.ReadFile("../../contracts/fixtures/golden-portfolio-twr.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]any
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r, golden) {
		t.Fatalf("shared TWR response differs: %s", w.Body.String())
	}
}

func twrHTTPReport(t *testing.T, svc *Service) *PortfolioTWR {
	t.Helper()
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/twr", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("TWR HTTP %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("financial response may be cached")
	}
	for _, key := range []string{"account-main", "source_event_id", "corrects_source_event_id", "observation_id", "receipt_id", "instrument_id"} {
		if strings.Contains(w.Body.String(), key) {
			t.Fatalf("private field leaked: %s", key)
		}
	}
	var r PortfolioTWR
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	return &r
}

func requireTWR(t *testing.T, r *PortfolioTWR, wantRate, wantIssue string) {
	t.Helper()
	if wantIssue == "" {
		if r.Status != "calculated" || r.CumulativeRate == nil || *r.CumulativeRate != wantRate {
			t.Fatalf("unexpected TWR %+v", r)
		}
	} else {
		found := false
		for _, issue := range r.Issues {
			found = found || issue.Code == wantIssue
		}
		if r.Status != "unavailable" || r.CumulativeRate != nil || r.NetContributions != nil || !found {
			t.Fatalf("unsupported inputs certified: %+v", r)
		}
	}
}

func TestPortfolioTWRCashPaths(t *testing.T) {
	for _, test := range []struct {
		name, rate, issue string
		rows              []string
	}{
		{"capital only", "0", "", []string{"a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100", "b,account-main,DEPOSIT,2025-07-01T00:00:00Z,,,,,KRW,100"}},
		{"partial withdrawal", "0.21", "", []string{"a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100", "b,account-main,DIVIDEND,2025-06-30T00:00:00Z,AAPL,,,,KRW,10", "c,account-main,WITHDRAWAL,2025-07-01T00:00:00Z,,,,,KRW,-50", "d,account-main,DIVIDEND,2026-01-01T00:00:00Z,AAPL,,,,KRW,6"}},
		{"full withdrawal is not loss", "", "capital_path_unavailable", []string{"a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100", "b,account-main,WITHDRAWAL,2025-07-01T00:00:00Z,,,,,KRW,-100"}},
		{"reinvestment after depletion", "", "capital_path_unavailable", []string{"a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100", "b,account-main,WITHDRAWAL,2025-07-01T00:00:00Z,,,,,KRW,-100", "c,account-main,DEPOSIT,2025-08-01T00:00:00Z,,,,,KRW,100"}},
		{"actual total loss", "-1", "", []string{"a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100", "b,account-main,FEE,2026-01-01T00:00:00Z,,,,,KRW,-100"}},
		{"simultaneous internal fee", "-0.01", "", []string{"fee,account-main,FEE,2025-01-01T00:00:00Z,,,,,KRW,-1", "capital,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100"}},
		{"zero return", "0", "", []string{"a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100"}},
		{"no external capital", "", "no_external_cash_flows", []string{"a,account-main,DIVIDEND,2025-01-01T00:00:00Z,AAPL,,,,KRW,10"}},
		{"balance before inception", "", "capital_path_unavailable", []string{"a,account-main,DIVIDEND,2025-01-01T00:00:00Z,AAPL,,,,KRW,10", "b,account-main,DEPOSIT,2025-07-01T00:00:00Z,,,,,KRW,100"}},
		{"negative terminal", "", "capital_path_unavailable", []string{"a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100", "b,account-main,FEE,2026-01-01T00:00:00Z,,,,,KRW,-101"}},
		{"nanosecond boundary and append order", "0.21", "", []string{"d,account-main,DIVIDEND,2025-01-01T00:00:00.3Z,AAPL,,,,KRW,21", "c,account-main,DEPOSIT,2025-01-01T00:00:00.2Z,,,,,KRW,100", "b,account-main,DIVIDEND,2025-01-01T00:00:00.1Z,AAPL,,,,KRW,10", "a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100"}},
		{"simultaneous net external capital", "-0.1", "", []string{"a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100", "b,account-main,WITHDRAWAL,2025-01-01T00:00:00Z,,,,,KRW,-10", "c,account-main,FEE,2025-01-01T00:00:00Z,,,,,KRW,-9"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
			applyTestCSV(t, svc, "twr-path", testCSV(test.rows...))
			calls := 0
			svc.now = func() time.Time { calls++; return mustTime("2026-01-01T00:00:00Z") }
			requireTWR(t, twrHTTPReport(t, svc), test.rate, test.issue)
			if calls != 1 {
				t.Fatalf("clock read %d times", calls)
			}
		})
	}
}

func TestPortfolioTWRSharedNullableStates(t *testing.T) {
	data, err := os.ReadFile("../../contracts/fixtures/golden-portfolio-twr-states.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct{ Reports map[string]json.RawMessage }
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for name, want := range fixtures.Reports {
		t.Run(name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
			switch name {
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
			raw, err := json.Marshal(twrHTTPReport(t, svc))
			if err != nil {
				t.Fatal(err)
			}
			var actual, expected any
			if err := json.Unmarshal(raw, &actual); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(want, &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("shared state differs: %s want %s", raw, want)
			}
		})
	}
}

func TestPortfolioTWRRestatesInternalAndExternalVoids(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-02T00:00:00Z") }
	applyTestCSV(t, svc, "twr-void-base", testCSV("wrong,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100", "capital,account-main,DEPOSIT,2025-01-02T00:00:00Z,,,,,KRW,200", "fee,account-main,FEE,2025-01-02T00:00:00Z,,,,,KRW,-5", "income,account-main,DIVIDEND,2026-01-02T00:00:00Z,AAPL,,,,KRW,20"))
	applyTestCSV(t, svc, "twr-void-corrections", cashVoidCSV("remove,account-main,CASH_VOID,2025-01-03T00:00:00Z,,,,,KRW,-100,wrong", "refund,account-main,CASH_VOID,2025-01-03T00:00:00Z,,,,,KRW,5,fee"))
	r := twrHTTPReport(t, svc)
	requireTWR(t, r, "0.1", "")
	if *r.PeriodStart != "2025-01-02T00:00:00Z" || *r.ExternalEventCount != 1 || r.NetContributions.Amount != "200" {
		t.Fatalf("void distorted capital %+v", r)
	}
}

func TestPortfolioTWRHistoricalFXTwoClocksAndAge(t *testing.T) {
	for _, test := range []struct {
		name   string
		age    time.Duration
		future bool
		issue  string
	}{
		{"retrospective ingestion", 0, false, ""}, {"exact24h", 24 * time.Hour, false, ""}, {"over24h", 24*time.Hour + time.Nanosecond, false, "stale_historical_fx"}, {"future knowledge", 0, true, "missing_historical_fx"},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			end := mustTime("2026-01-02T00:00:00Z")
			flow := mustTime("2025-01-02T00:00:00Z")
			svc.now = func() time.Time { return end }
			applyTestCSV(t, svc, "twr-fx", testCSV("a,account-main,DEPOSIT,2025-01-02T00:00:00Z,,,,,USD,100"))
			known := end
			if test.future {
				known = known.Add(time.Nanosecond)
			}
			recordXIRRFX(t, svc, "historic", "1000", flow.Add(-test.age).Format(time.RFC3339Nano), known.Format(time.RFC3339Nano), known.Format(time.RFC3339Nano))
			recordXIRRFX(t, svc, "terminal", "1100", end.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
			r := twrHTTPReport(t, svc)
			requireTWR(t, r, "0.1", test.issue)
			if !r.Sample {
				t.Fatal("sample historical inputs lost")
			}
		})
	}
}

func TestPortfolioTWRHistoricalSampleSurvivesPureKRWTerminal(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	end := mustTime("2026-01-01T00:00:00Z")
	svc.now = func() time.Time { return end }
	applyTestCSV(t, svc, "twr-internal-fx", fxCSV("a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,USD,100,,,", "b,account-main,FX_EXCHANGE,2025-01-02T00:00:00Z,,,,,USD,-100,KRW,100000,"))
	recordXIRRFX(t, svc, "historical", "1000", "2025-01-01T00:00:00Z", end.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
	r := twrHTTPReport(t, svc)
	requireTWR(t, r, "0", "")
	if !r.Sample || r.Valuation.Sample || r.NetContributions.Amount != "100000" {
		t.Fatalf("historic sample/fx classification lost %+v", r)
	}
}

func TestPortfolioTWRHistoricalPriceAndSplitUnits(t *testing.T) {
	for _, test := range []struct {
		name, observed, issue                    string
		simultaneous, futureKnowledge, ambiguous bool
	}{
		{"post split", "2025-01-02T00:00:01Z", "", false, false, false},
		{"pre split within24h", "2025-01-01T23:59:59Z", "split_price_unavailable", false, false, false},
		{"future economic price", "2025-01-02T00:01:00.000000001Z", "missing_security_price", false, false, false},
		{"future knowledge", "2025-01-02T00:00:01Z", "missing_security_price", false, true, false},
		{"ambiguous venue", "2025-01-02T00:00:01Z", "ambiguous_security_price_venue", false, false, true},
		{"flow and split same timestamp", "2025-01-02T00:00:01Z", "ambiguous_split_boundary", true, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			end := mustTime("2025-01-02T02:00:00Z")
			svc.now = func() time.Time { return end }
			flowAt := "2025-01-02T00:01:00Z"
			if test.simultaneous {
				flowAt = "2025-01-02T00:00:00Z"
			}
			applyTestCSV(t, svc, "twr-split", testCSV("a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,USD,100", "b,account-main,BUY,2025-01-01T01:00:00Z,AAPL,1,100,0,USD,-100", "c,account-main,SPLIT,2025-01-02T00:00:00Z,AAPL,2,,,USD,0", "d,account-main,DEPOSIT,"+flowAt+",,,,,USD,100"))
			for i, at := range []string{"2025-01-01T00:00:00Z", "2025-01-02T00:00:00Z", "2025-01-02T02:00:00Z"} {
				recordXIRRFX(t, svc, fmt.Sprintf("fx-%d", i), "1000", at, end.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
			}
			known := end
			if test.futureKnowledge {
				known = known.Add(time.Nanosecond)
			}
			recordHoldingValuationPrice(t, svc, "boundary", "XNAS", "50", test.observed, known.Format(time.RFC3339Nano), known.Format(time.RFC3339Nano))
			if test.ambiguous {
				recordHoldingValuationPrice(t, svc, "other-venue", "XNYS", "50", test.observed, end.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
			}
			recordHoldingValuationPrice(t, svc, "terminal", "XNAS", "50", "2025-01-02T01:00:00Z", end.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
			svc.now = func() time.Time { return end }
			r := twrHTTPReport(t, svc)
			// The existing current valuation also fails closed on multiple eligible venues.
			want := test.issue
			if test.ambiguous {
				want = "valuation_unavailable"
			}
			requireTWR(t, r, "0", want)
		})
	}
}

func TestPortfolioTWRFinalSplitRequiresFreshUnitRegime(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	end := mustTime("2025-01-02T02:00:00Z")
	svc.now = func() time.Time { return end }
	applyTestCSV(t, svc, "twr-final-split", testCSV("a,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,USD,100", "b,account-main,BUY,2025-01-01T01:00:00Z,AAPL,1,100,0,USD,-100", "c,account-main,SPLIT,2025-01-02T00:00:00Z,AAPL,2,,,USD,0"))
	recordXIRRFX(t, svc, "initial", "1000", "2025-01-01T00:00:00Z", end.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
	recordXIRRFX(t, svc, "terminal", "1000", end.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
	recordHoldingValuationPrice(t, svc, "pre-split", "XNAS", "100", "2025-01-01T23:59:59Z", end.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
	svc.now = func() time.Time { return end }
	requireTWR(t, twrHTTPReport(t, svc), "", "split_price_unavailable")
}

func TestPortfolioTWRBoundaryLimit(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	end := mustTime("2026-01-01T00:00:00Z")
	svc.now = func() time.Time { return end }
	rows := make([]string, 1001)
	for i := range rows {
		at := mustTime("2025-01-01T00:00:00Z").Add(time.Duration(i) * time.Nanosecond)
		rows[i] = fmt.Sprintf("capital-%d,account-main,DEPOSIT,%s,,,,,KRW,1", i, at.Format(time.RFC3339Nano))
	}
	applyTestCSV(t, svc, "twr-limit", testCSV(rows...))
	requireTWR(t, twrHTTPReport(t, svc), "", "cash_flow_limit")
}

func TestPortfolioTWRProofAndCutoff(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	applyTestCSV(t, svc, "twr-proof", testCSV("capital,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100"))
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/twr?as_of=2025-01-01", nil))
	if w.Code != 400 {
		t.Fatal("caller controlled report cutoff")
	}
	svc.now = func() time.Time { return mustTime("2025-12-31T23:59:59.999999999Z") }
	w = httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/twr", nil))
	if w.Code != 500 {
		t.Fatal("future recorded ledger accepted")
	}
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	if _, err := svc.db.Exec(`DROP TRIGGER ledger_replay_state_transition; UPDATE ledger_replay_state SET legacy_revision=1`); err != nil {
		t.Fatal(err)
	}
	script, err := migrationFiles.ReadFile("migrations/022_ledger_replay_policy.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, after, found := strings.Cut(string(script), "CREATE TRIGGER ledger_replay_state_transition ")
	body, _, ended := strings.Cut(after, "END;")
	if !found || !ended {
		t.Fatal("trigger missing")
	}
	if _, err := svc.db.Exec("CREATE TRIGGER ledger_replay_state_transition " + body + "END;"); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/twr", nil))
	if w.Code != 500 || w.Body.String() != "{\"code\":\"internal_error\",\"message\":\"internal server error\"}\n" {
		t.Fatalf("corrupt proof certified or leaked: %d %s", w.Code, w.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.portfolioTWR(ctx); err == nil {
		t.Fatal("canceled request continued")
	}
}
