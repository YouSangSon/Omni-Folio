package main

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPortfolioXIRRUsesRecordedExternalCashFlows(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	applyTestCSV(t, svc, "xirr-seed", testCSV(
		"capital,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100",
		"income,account-main,DIVIDEND,2026-01-01T00:00:00Z,AAPL,,,,KRW,10",
	))
	response := httptest.NewRecorder()
	svc.routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/portfolio/xirr", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("XIRR report unavailable: status=%d body=%s", response.Code, response.Body.String())
	}
	var report map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report["status"] != "calculated" || report["annual_rate"] != "0.1" || report["uniqueness"] != "unique" {
		t.Fatalf("deposit or dividend misclassified: %s", response.Body.String())
	}
	raw, err := os.ReadFile("../../contracts/fixtures/golden-portfolio-xirr.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]any
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report, golden) {
		t.Fatalf("shared calculated response differs: got%s want%s", response.Body.String(), raw)
	}
}

func xirrHTTPReport(t *testing.T, svc *Service) *PortfolioXIRR {
	t.Helper()
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/xirr", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("XIRR HTTP %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("financial report may be cached")
	}
	for _, private := range []string{"account-main", "source_event_id", "corrects_source_event_id", "observation_id", "source_observation_id", "receipt_id", "instrument_id"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("private field leaked: %s", private)
		}
	}
	var report PortfolioXIRR
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	return &report
}

func TestPortfolioXIRRSharedNullableStates(t *testing.T) {
	raw, err := os.ReadFile("../../contracts/fixtures/golden-portfolio-xirr-states.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct{ Reports map[string]json.RawMessage }
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for name, want := range fixtures.Reports {
		t.Run(name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
			if name != "empty" {
				applyTestCSV(t, svc, "nullable-seed", testCSV("capital,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,USD,100"))
			}
			if name == "historical_fx_unavailable" {
				recordXIRRFX(t, svc, "current", "1100", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
			}
			report := xirrHTTPReport(t, svc)
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			var actual, expected any
			if err := json.Unmarshal(encoded, &actual); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(want, &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("nullable state differs: got %s want %s", encoded, want)
			}
		})
	}
}

func recordXIRRFX(t *testing.T, svc *Service, source, rate, observed, fetched, recorded string) {
	t.Helper()
	previous := svc.now
	svc.now = func() time.Time { return mustTime(recorded) }
	defer func() { svc.now = previous }()
	if _, err := svc.recordFXObservation(context.Background(), FXObservationInput{Source: "local_fixture", SourceObservationID: source, BaseCurrency: "USD", QuoteCurrency: "KRW", Rate: rate, ObservedAt: observed, FetchedAt: fetched}); err != nil {
		t.Fatal(err)
	}
}

func TestPortfolioXIRRAdditionalCapitalIsNotProfit(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	applyTestCSV(t, svc, "two-deposits", testCSV("first,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100", "second,account-main,DEPOSIT,2025-07-01T00:00:00Z,,,,,KRW,100"))
	calls := 0
	svc.now = func() time.Time { calls++; return mustTime("2026-01-01T00:00:00Z") }
	r := xirrHTTPReport(t, svc)
	if calls != 1 || r.AnnualRate == nil || *r.AnnualRate != "0" || r.NetContributions == nil || r.NetContributions.Amount != "200" {
		t.Fatalf("capital became profit or clock changed: %+v calls=%d", r, calls)
	}
}

func TestPortfolioXIRRNonconventionalResultIsOnlyACandidate(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2023-01-02T00:00:00Z") }
	applyTestCSV(t, svc, "multiple-root-seed", testCSV(
		"capital,account-main,DEPOSIT,2021-01-01T00:00:00Z,,,,,KRW,100",
		"income,account-main,DIVIDEND,2022-01-01T00:00:00Z,AAPL,,,,KRW,130",
		"withdraw,account-main,WITHDRAWAL,2022-01-01T00:00:00Z,,,,,KRW,-230",
		"reinvest,account-main,DEPOSIT,2023-01-01T00:00:00Z,,,,,KRW,132",
		"expense,account-main,FEE,2023-01-01T00:00:00Z,,,,,KRW,-132",
	))
	r := xirrHTTPReport(t, svc)
	if r.Status != "candidate" || r.Uniqueness != "not_proven" || r.AnnualRate != nil || r.CandidateRate == nil || r.Valuation.TotalValue.Amount != "0" {
		t.Fatalf("candidate was promoted or discarded: %+v", r)
	}
	candidate, err := parseDecimal(*r.CandidateRate)
	if err != nil || new(big.Rat).Abs(new(big.Rat).Sub(candidate, big.NewRat(1, 10))).Cmp(big.NewRat(1, 10000000000)) > 0 {
		t.Fatalf("candidate differs from independent root: %s %v", *r.CandidateRate, err)
	}
}

func TestPortfolioXIRRRestatesCashVoids(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-02T00:00:00Z") }
	applyTestCSV(t, svc, "void-seed", testCSV("wrong,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100", "capital,account-main,DEPOSIT,2025-01-02T00:00:00Z,,,,,KRW,200", "fee,account-main,FEE,2025-01-02T00:00:00Z,,,,,KRW,-5", "income,account-main,DIVIDEND,2026-01-02T00:00:00Z,AAPL,,,,KRW,20"))
	applyTestCSV(t, svc, "void-corrections", cashVoidCSV("remove,account-main,CASH_VOID,2025-01-03T00:00:00Z,,,,,KRW,-100,wrong", "refund,account-main,CASH_VOID,2025-01-03T00:00:00Z,,,,,KRW,5,fee"))
	r := xirrHTTPReport(t, svc)
	if r.AnnualRate == nil || *r.AnnualRate != "0.1" || r.PeriodStart == nil || *r.PeriodStart != "2025-01-02" || r.ExternalEventCount == nil || *r.ExternalEventCount != 1 || r.NetContributions.Amount != "200" {
		t.Fatalf("void became a synthetic withdrawal: %+v", r)
	}
}

func TestPortfolioXIRRHistoricalFXUsesEconomicAndKnowledgeCutoffs(t *testing.T) {
	for _, test := range []struct {
		name            string
		age             time.Duration
		futureKnowledge bool
		want            string
	}{
		{"late ingestion is legitimate hindsight", 0, false, "calculated"},
		{"exact24hours", 24 * time.Hour, false, "calculated"},
		{"over24hours", 24*time.Hour + time.Nanosecond, false, "stale_historical_fx"},
		{"future knowledge is unavailable", 0, true, "missing_historical_fx"},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			flowAt := mustTime("2025-01-02T00:00:00Z")
			end := mustTime("2026-01-02T00:00:00Z")
			svc.now = func() time.Time { return end }
			applyTestCSV(t, svc, "usd-capital", testCSV("capital,account-main,DEPOSIT,2025-01-02T00:00:00Z,,,,,USD,100"))
			known := end
			if test.futureKnowledge {
				known = known.Add(time.Nanosecond)
			}
			recordXIRRFX(t, svc, "historic", "1000", flowAt.Add(-test.age).Format(time.RFC3339Nano), known.Format(time.RFC3339Nano), known.Format(time.RFC3339Nano))
			recordXIRRFX(t, svc, "current", "1100", end.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
			r := xirrHTTPReport(t, svc)
			if test.want == "calculated" {
				if r.Status != "calculated" || r.AnnualRate == nil || *r.AnnualRate != "0.1" || r.NetContributions.Amount != "100000" || !r.Sample {
					t.Fatalf("historical conversion drift: %+v", r)
				}
			} else {
				found := false
				for _, issue := range r.Issues {
					found = found || issue.Code == test.want
				}
				if r.Status != "unavailable" || r.AnnualRate != nil || r.CandidateRate != nil || r.NetContributions != nil || !found {
					t.Fatalf("missing/stale FX substituted: %+v", r)
				}
			}
		})
	}
}

func TestPortfolioXIRRConvertsEachEventBeforeDayNetting(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	applyTestCSV(t, svc, "same-day-usd", testCSV("capital,account-main,DEPOSIT,2025-01-01T09:00:00Z,,,,,USD,100", "recovery,account-main,WITHDRAWAL,2025-01-01T10:00:00Z,,,,,USD,-100"))
	recordXIRRFX(t, svc, "early", "1000", "2025-01-01T09:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	recordXIRRFX(t, svc, "late", "1100", "2025-01-01T10:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	r := xirrHTTPReport(t, svc)
	if r.NetContributions == nil || r.NetContributions.Amount != "-10000" || !r.Sample || r.Valuation.Sample || r.AnnualRate != nil {
		t.Fatalf("events were netted in foreign currency or sample was lost: %+v", r)
	}
}

func TestPortfolioXIRRRequiresFullTransitionProofAndRejectsQueries(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	applyTestCSV(t, svc, "proof-seed", testCSV("capital,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,KRW,100"))
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/xirr?as_of=2025-01-01", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("caller owned cutoff: %d", w.Code)
	}
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
		t.Fatal("replay state guard missing from migration")
	}
	if _, err := svc.db.Exec("CREATE TRIGGER ledger_replay_state_transition " + body + "END;"); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/portfolio/xirr", nil))
	if w.Code != http.StatusInternalServerError || w.Body.String() != "{\"code\":\"internal_error\",\"message\":\"internal server error\"}\n" {
		t.Fatalf("transition corruption certified: %d %s", w.Code, w.Body.String())
	}
}
