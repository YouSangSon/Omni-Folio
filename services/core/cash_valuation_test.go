package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCashValuationExactDirectAndNoInverseCross(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "cash-valuation-seed", testCSV(
		"krw,account-main,DEPOSIT,2026-01-10T10:00:00Z,,,,,KRW,1000",
		"usd,account-main,DEPOSIT,2026-01-10T10:01:00Z,,,,,USD,2",
		"jpy,account-main,WITHDRAWAL,2026-01-10T10:02:00Z,,,,,JPY,-100",
		"eur-in,account-main,DEPOSIT,2026-01-10T10:03:00Z,,,,,EUR,1",
		"eur-out,account-main,WITHDRAWAL,2026-01-10T10:04:00Z,,,,,EUR,-1",
	))
	svc.now = func() time.Time { return mustTime("2026-01-11T14:00:00Z") }
	for _, input := range []FXObservationInput{
		{Source: "local_fixture", SourceObservationID: "usd-krw", BaseCurrency: "USD", QuoteCurrency: "KRW", Rate: "1370.25", ObservedAt: "2026-01-11T13:00:00Z", FetchedAt: "2026-01-11T13:00:01Z"},
		{Source: "local_fixture", SourceObservationID: "jpy-krw", BaseCurrency: "JPY", QuoteCurrency: "KRW", Rate: "9.1", ObservedAt: "2026-01-11T13:00:00Z", FetchedAt: "2026-01-11T13:00:01Z"},
	} {
		if _, err := svc.recordFXObservation(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}

	valuation, appErr := svc.cashValuation(context.Background(), "KRW", "2026-01-11T14:00:00Z")
	if appErr != nil {
		t.Fatal(appErr)
	}
	if valuation.Scope != "cash_only" || valuation.PolicyVersion != "direct_fx_cash_v1" || valuation.Status != "stale_sample" ||
		!valuation.Sample || valuation.Total == nil || *valuation.Total != (Money{"KRW", "2830.5"}) {
		t.Fatalf("unexpected exact cash valuation: %+v", valuation)
	}
	wantStatuses := []string{"zero", "valued", "identity", "valued"}
	gotStatuses := make([]string, len(valuation.Lines))
	for index, line := range valuation.Lines {
		gotStatuses[index] = line.Status
	}
	if !reflect.DeepEqual(gotStatuses, wantStatuses) {
		t.Fatalf("cash lines are not deterministic: got=%v want=%v", gotStatuses, wantStatuses)
	}
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil || snapshot.ValuationStatus != "unavailable" {
		t.Fatalf("cash-only read changed whole-portfolio authority: snapshot=%+v err=%v", snapshot, err)
	}

	missing, _ := testService(t, nil, nil)
	applyTestCSV(t, missing, "missing-direct-seed", testCSV("usd,account-main,DEPOSIT,2026-01-10T10:00:00Z,,,,,USD,2"))
	missing.now = func() time.Time { return mustTime("2026-01-11T14:00:00Z") }
	for _, input := range []FXObservationInput{
		{Source: "local_fixture", SourceObservationID: "inverse", BaseCurrency: "KRW", QuoteCurrency: "USD", Rate: "0.001", ObservedAt: "2026-01-11T13:00:00Z", FetchedAt: "2026-01-11T13:00:01Z"},
		{Source: "local_fixture", SourceObservationID: "cross-one", BaseCurrency: "USD", QuoteCurrency: "JPY", Rate: "150", ObservedAt: "2026-01-11T13:00:00Z", FetchedAt: "2026-01-11T13:00:01Z"},
		{Source: "local_fixture", SourceObservationID: "cross-two", BaseCurrency: "JPY", QuoteCurrency: "KRW", Rate: "9.1", ObservedAt: "2026-01-11T13:00:00Z", FetchedAt: "2026-01-11T13:00:01Z"},
	} {
		if _, err := missing.recordFXObservation(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	valuation, appErr = missing.cashValuation(context.Background(), "KRW", "2026-01-11T14:00:00Z")
	if appErr != nil || valuation.Status != "unavailable" || valuation.Total != nil || valuation.Lines[0].Status != "missing" {
		t.Fatalf("inverse/cross rate was used or missing was hidden: valuation=%+v err=%v", valuation, appErr)
	}
}

func TestCashValuationSelectsLatestCanonicalInstantAndRestoresExactly(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "latest-restore-seed", testCSV("usd,account-main,DEPOSIT,2026-01-10T10:00:00Z,,,,,USD,2"))
	clock := mustTime("2026-01-11T14:00:00Z")
	svc.now = func() time.Time { return clock }
	for _, input := range []FXObservationInput{
		{Source: "local_fixture", SourceObservationID: "whole-second", BaseCurrency: "USD", QuoteCurrency: "KRW", Rate: "1370", ObservedAt: "2026-01-11T13:00:00Z", FetchedAt: "2026-01-11T13:00:01Z"},
		{Source: "local_fixture", SourceObservationID: "fractional-second", BaseCurrency: "USD", QuoteCurrency: "KRW", Rate: "1380", ObservedAt: "2026-01-11T13:00:00.5Z", FetchedAt: "2026-01-11T13:00:01Z"},
	} {
		if _, err := svc.recordFXObservation(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	want, appErr := svc.cashValuation(context.Background(), "KRW", "2026-01-11T14:00:00Z")
	if appErr != nil || want.Total == nil || want.Total.Amount != "2760" || want.Lines[0].FX.Rate != "1380" {
		t.Fatalf("latest canonical instant was not selected: valuation=%+v err=%v", want, appErr)
	}

	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "cash-valuation.db")
	if _, err := createBackup(svc.db, backup, golden, backup+".manifest.json", svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(backup, golden, backup+".manifest.json"); err != nil {
		t.Fatal(err)
	}
	restoredDB, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restoredDB.Close() })
	restored := newService(restoredDB, func() time.Time { return clock }, randomID)
	got, appErr := restored.cashValuation(context.Background(), "KRW", "2026-01-11T14:00:00Z")
	if appErr != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("restore changed deterministic cash valuation: got=%+v want=%+v err=%v", got, want, appErr)
	}
}

func TestCashValuationAsOfFreshnessAndCoverage(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "freshness-seed", testCSV(
		"usd,account-main,DEPOSIT,2026-01-09T10:00:00Z,,,,,USD,2",
		"jpy,account-main,DEPOSIT,2026-01-09T10:01:00Z,,,,,JPY,100",
	))
	svc.now = func() time.Time { return mustTime("2026-01-11T14:00:00Z") }
	for _, input := range []FXObservationInput{
		{Source: "local_fixture", SourceObservationID: "usd-boundary", BaseCurrency: "USD", QuoteCurrency: "KRW", Rate: "1370", ObservedAt: "2026-01-10T14:00:00Z", FetchedAt: "2026-01-10T14:00:01Z"},
		{Source: "local_fixture", SourceObservationID: "jpy-stale", BaseCurrency: "JPY", QuoteCurrency: "KRW", Rate: "9", ObservedAt: "2026-01-10T13:59:59.999999999Z", FetchedAt: "2026-01-10T14:00:01Z"},
	} {
		if _, err := svc.recordFXObservation(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	valuation, appErr := svc.cashValuation(context.Background(), "KRW", "2026-01-11T14:00:00Z")
	if appErr != nil || valuation.Status != "unavailable" || valuation.Total != nil || valuation.Lines[0].Status != "stale" || valuation.Lines[1].Status != "valued" {
		t.Fatalf("freshness boundary or all-or-none total drifted: valuation=%+v err=%v", valuation, appErr)
	}

	late, _ := testService(t, nil, nil)
	applyTestCSV(t, late, "recorded-cutoff-seed", testCSV("usd,account-main,DEPOSIT,2026-01-10T10:00:00Z,,,,,USD,1"))
	late.now = func() time.Time { return mustTime("2026-01-11T14:00:00Z") }
	if _, err := late.recordFXObservation(context.Background(), FXObservationInput{
		Source: "local_fixture", SourceObservationID: "late-record", BaseCurrency: "USD", QuoteCurrency: "KRW", Rate: "1380",
		ObservedAt: "2026-01-11T13:00:00Z", FetchedAt: "2026-01-11T13:00:01Z",
	}); err != nil {
		t.Fatal(err)
	}
	valuation, appErr = late.cashValuation(context.Background(), "KRW", "2026-01-11T13:30:00Z")
	if appErr != nil || valuation.Status != "unavailable" || valuation.Lines[0].Status != "missing" {
		t.Fatalf("future-recorded observation leaked into as-of valuation: valuation=%+v err=%v", valuation, appErr)
	}
}

func TestCashValuationRejectsCutoffAndFailsClosed(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "cutoff-seed", testCSV("usd,account-main,DEPOSIT,2026-01-10T10:00:00Z,,,,,USD,1"))
	svc.now = func() time.Time { return mustTime("2026-01-11T14:00:00Z") }
	for _, asOf := range []string{"bad", "2026-01-11T23:00:00+09:00", "2026-01-11T14:00:01Z"} {
		if _, appErr := svc.cashValuation(context.Background(), "KRW", asOf); appErr == nil || appErr.status != http.StatusBadRequest {
			t.Fatalf("invalid/future cutoff was accepted: as_of=%s err=%v", asOf, appErr)
		}
	}
	if _, appErr := svc.cashValuation(context.Background(), "KRW", "2026-01-10T09:59:59Z"); appErr == nil || appErr.status != http.StatusConflict {
		t.Fatalf("cutoff before ledger state was accepted: %v", appErr)
	}

	if _, err := svc.recordFXObservation(context.Background(), FXObservationInput{
		Source: "local_fixture", SourceObservationID: "corrupt", BaseCurrency: "USD", QuoteCurrency: "KRW", Rate: "1370",
		ObservedAt: "2026-01-11T13:00:00Z", FetchedAt: "2026-01-11T13:00:01Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER fx_observations_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE fx_observations SET rate='1371'`); err != nil {
		t.Fatal(err)
	}
	if _, appErr := svc.cashValuation(context.Background(), "KRW", "2026-01-11T14:00:00Z"); appErr == nil || appErr.status != http.StatusInternalServerError || appErr.body.Message != "internal server error" {
		t.Fatalf("corrupt FX state did not fail closed: %v", appErr)
	}
}

func TestCashValuationFailsClosedWhenLedgerRevisionIsCorrupt(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "ledger-proof-seed", testCSV("krw,account-main,DEPOSIT,2026-01-10T10:00:00Z,,,,,KRW,1000"))
	svc.now = func() time.Time { return mustTime("2026-01-11T14:00:00Z") }
	if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=99 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}

	if valuation, appErr := svc.cashValuation(context.Background(), "KRW", "2026-01-11T14:00:00Z"); appErr == nil || appErr.status != http.StatusInternalServerError || appErr.body.Message != "internal server error" || valuation != nil {
		t.Fatalf("corrupt ledger revision was certified: valuation=%+v err=%v", valuation, appErr)
	}
}

func TestFXObservationTemporalOrderFailsCashValuationClosed(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "temporal-corruption-seed", testCSV("usd,account-main,DEPOSIT,2026-01-10T10:00:00Z,,,,,USD,1"))
	svc.now = func() time.Time { return mustTime("2026-01-11T14:00:00Z") }
	if _, err := svc.recordFXObservation(context.Background(), FXObservationInput{
		Source: "local_fixture", SourceObservationID: "time-reversed", BaseCurrency: "USD", QuoteCurrency: "KRW", Rate: "1370",
		ObservedAt: "2026-01-11T13:00:01Z", FetchedAt: "2026-01-11T13:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, appErr := svc.cashValuation(context.Background(), "KRW", "2026-01-11T14:00:00Z"); appErr == nil || appErr.status != http.StatusInternalServerError || appErr.body.Message != "internal server error" {
		t.Fatalf("impossible FX timestamp order was certified: %v", appErr)
	}
}

func TestCashValuationHTTPContractIsClosedReadOnlyAndSanitized(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	path := document["paths"].(map[string]any)["/v1/portfolio/cash-valuation"].(map[string]any)
	if len(path) != 1 || path["get"] == nil {
		t.Fatal("cash valuation route must be GET-only")
	}
	schema := document["components"].(map[string]any)["schemas"].(map[string]any)["CashValuation"].(map[string]any)
	encoded, err := json.Marshal(schema)
	if err != nil || schema["additionalProperties"] != false {
		t.Fatal("cash valuation schema must be closed")
	}
	for _, forbidden := range []string{"source_observation_id", "record_sha256", "account_id", "holdings", "realized_pnl", "inverse_rate", "cross_rate"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("cash valuation contract exposes %s", forbidden)
		}
	}

	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-11T14:00:00Z") }
	request := func(path string) (int, map[string]any) {
		t.Helper()
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return w.Code, body
	}
	for path, want := range map[string]int{
		"/v1/portfolio/cash-valuation?base_currency=KRW&as_of=2026-01-11T14:00:00Z":                      http.StatusOK,
		"/v1/portfolio/cash-valuation?base_currency=KRW":                                                 http.StatusBadRequest,
		"/v1/portfolio/cash-valuation?base_currency=KRW&base_currency=USD&as_of=2026-01-11T14:00:00Z":    http.StatusBadRequest,
		"/v1/portfolio/cash-valuation?base_currency=KRW&as_of=2026-01-11T14:00:00Z&source=local_fixture": http.StatusBadRequest,
		"/v1/portfolio/cash-valuation?base_currency=krw&as_of=2026-01-11T14:00:00Z":                      http.StatusBadRequest,
	} {
		status, _ := request(path)
		if status != want {
			t.Fatalf("cash valuation query status=%d want=%d path=%s", status, want, path)
		}
	}
}
