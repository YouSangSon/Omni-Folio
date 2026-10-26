package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const ownerFXTestInput = `{"schema_version":"owner-fx-series.v1","source_version":"manual-v1","base_currency":"USD","quote_currency":"KRW","sample":false,"fetched_at":"2026-01-01T00:00:00Z","points":[{"observed_at":"2025-01-01T00:00:00Z","rate":"1000"},{"observed_at":"2026-01-01T00:00:00Z","rate":"900"}]}`

func ownerFXInputForTest(t testing.TB, change func(*OwnerFXSeriesInput)) []byte {
	t.Helper()
	var input OwnerFXSeriesInput
	if err := json.Unmarshal([]byte(ownerFXTestInput), &input); err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(&input)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func importOwnerFXForTest(t testing.TB, svc *Service, raw []byte) *OwnerFXSeriesReceipt {
	t.Helper()
	r, err := svc.importOwnerFXSeries(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func ownerFXServiceForTest(t *testing.T) *Service {
	t.Helper()
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00Z") }
	return svc
}

func TestOwnerFXClosedInputAndBounds(t *testing.T) {
	invalid := map[string][]byte{
		"unknown field":         []byte(strings.Replace(ownerFXTestInput, `"sample":false`, `"sample":false,"provider_verified":true`, 1)),
		"duplicate escaped key": []byte(strings.Replace(ownerFXTestInput, `"sample":false`, `"sample":false,"s\u0061mple":true`, 1)),
		"unknown point":         []byte(strings.Replace(ownerFXTestInput, `"rate":"900"`, `"rate":"900","extra":true`, 1)),
		"null sample":           []byte(strings.Replace(ownerFXTestInput, `"sample":false`, `"sample":null`, 1)),
		"null points":           ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.Points = nil }),
		"same direction":        ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.BaseCurrency = "KRW" }),
		"lower direction":       ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.BaseCurrency = "usd" }),
		"zero":                  ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.Points[0].Rate = "0" }),
		"numeric rate":          []byte(strings.Replace(ownerFXTestInput, `"rate":"900"`, `"rate":900`, 1)),
		"noncanonical rate":     ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.Points[0].Rate = "1000.0" }),
		"duplicate point time":  ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.Points[1].ObservedAt = i.Points[0].ObservedAt }),
		"point after fetch":     ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.Points[1].ObservedAt = "2026-01-01T00:00:00.000000001Z" }),
		"version byte bound":    ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.SourceVersion = strings.Repeat("한", 43) }),
		"surrogate":             []byte(strings.Replace(ownerFXTestInput, `manual-v1`, `\ud800`, 1)),
		"trailing JSON":         []byte(ownerFXTestInput + ` {}`),
		"body bound":            []byte(strings.Repeat(" ", maxBodyBytes) + ownerFXTestInput),
	}
	for name, raw := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := decodeOwnerFXSeries(context.Background(), raw); err == nil {
				t.Fatal("invalid owner declaration accepted")
			}
		})
	}
	for _, count := range []int{0, 10000, 10001} {
		raw := ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) {
			i.Points = []OwnerFXPoint{}
			for n := 0; n < count; n++ {
				i.Points = append(i.Points, OwnerFXPoint{ObservedAt: mustTime("2025-01-01T00:00:00Z").Add(time.Duration(n)).Format(time.RFC3339Nano), Rate: "1000"})
			}
		})
		input, canonical, _, err := decodeOwnerFXSeries(context.Background(), raw)
		if count > 10000 {
			if err == nil {
				t.Fatal("point bound ignored")
			}
		} else if err != nil || len(input.Points) != count || count == 0 && !strings.Contains(string(canonical), `"points":[]`) {
			t.Fatalf("valid point boundary rejected: count=%d err=%v", count, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := decodeOwnerFXSeries(ctx, []byte(ownerFXTestInput)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
}

func TestOwnerFXWholeVersionCorrectionWithdrawalAndOldRetry(t *testing.T) {
	svc := ownerFXServiceForTest(t)
	first := importOwnerFXForTest(t, svc, []byte(ownerFXTestInput))
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00.1Z") }
	second := ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) {
		i.SourceVersion = "a-correction"
		i.Sample = true
		i.Points[1].Rate = "1200"
	})
	importOwnerFXForTest(t, svc, second)
	for _, test := range []struct {
		cutoff, rate, version string
		sample                bool
	}{
		{"2026-01-01T00:00:00Z", "900", "manual-v1", false},
		{"2026-01-01T00:00:00.1Z", "1200", "a-correction", true},
	} {
		marks, err := valuationFXObservations(context.Background(), svc.db, mustTime(test.cutoff))
		if err != nil || len(marks) != 2 || marks[1].Rate != test.rate || marks[1].ownerVersion != test.version || fxIsSample(&marks[1]) != test.sample {
			t.Fatalf("whole known version drift: %+v %v", marks, err)
		}
	}
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00.2Z") }
	withdrawal := ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.SourceVersion = "withdrawn"; i.Points = []OwnerFXPoint{} })
	cleared := importOwnerFXForTest(t, svc, withdrawal)
	if cleared.PointCount != 0 || cleared.FirstObservedAt != nil || cleared.LastObservedAt != nil {
		t.Fatal("withdrawal invented a mark")
	}
	svc.now = func() time.Time { t.Fatal("exact retry called clock"); return time.Time{} }
	if retry := importOwnerFXForTest(t, svc, []byte(ownerFXTestInput)); !reflect.DeepEqual(retry, first) {
		t.Fatal("old receipt changed")
	}
	marks, err := valuationFXObservations(context.Background(), svc.db, mustTime("2026-01-01T00:00:00.3Z"))
	if err != nil || len(marks) != 0 {
		t.Fatal("withdrawal or old retry fell back to prior points")
	}
	if _, err := svc.importOwnerFXSeries(context.Background(), ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.Sample = true })); err == nil {
		t.Fatal("same version changed sample declaration")
	}
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00.15Z") }
	if _, err := svc.importOwnerFXSeries(context.Background(), ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.SourceVersion = "backwards" })); err == nil {
		t.Fatal("record clock moved backwards")
	}
	proof, err := proveOwnerFXSeriesRecovery(context.Background(), svc.db)
	if err != nil || proof.Versions != 3 || proof.Points != 4 {
		t.Fatalf("rejected/retried input changed proof: %+v %v", proof, err)
	}
}

func TestOwnerFXPairBindingSurvivesWithdrawalAndIDsAreNamespaced(t *testing.T) {
	t.Run("fixture pair cannot become owner", func(t *testing.T) {
		svc := ownerFXServiceForTest(t)
		recordXIRRFX(t, svc, "fixture", "1000", "2025-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
		if _, err := svc.importOwnerFXSeries(context.Background(), []byte(ownerFXTestInput)); err == nil {
			t.Fatal("mixed source accepted")
		}
		proof, err := proveOwnerFXSeriesRecovery(context.Background(), svc.db)
		if err != nil || proof.Versions != 0 {
			t.Fatal("failed import appended")
		}
	})
	t.Run("withdrawn owner pair cannot become fixture", func(t *testing.T) {
		svc := ownerFXServiceForTest(t)
		importOwnerFXForTest(t, svc, ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.Points = []OwnerFXPoint{} }))
		if _, err := svc.recordFXObservation(context.Background(), FXObservationInput{Source: "local_fixture", SourceObservationID: "fixture", BaseCurrency: "USD", QuoteCurrency: "KRW", Rate: "900", ObservedAt: "2026-01-01T00:00:00Z", FetchedAt: "2026-01-01T00:00:00Z"}); err == nil {
			t.Fatal("withdrawal cleared source binding")
		}
	})
	t.Run("public ID collision cannot alias time", func(t *testing.T) {
		svc := ownerFXServiceForTest(t)
		originalID := svc.id
		svc.id = func(prefix string) string {
			if prefix == "fx_observation" {
				return "owner_fx_1_0"
			}
			return originalID(prefix)
		}
		if _, err := svc.recordFXObservation(context.Background(), FXObservationInput{Source: "local_fixture", SourceObservationID: "eur", BaseCurrency: "EUR", QuoteCurrency: "KRW", Rate: "1500", ObservedAt: "2025-01-01T00:00:00.000000001Z", FetchedAt: "2026-01-01T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		importOwnerFXForTest(t, svc, []byte(ownerFXTestInput))
		marks, err := valuationFXObservations(context.Background(), svc.db, mustTime("2026-01-01T00:00:00Z"))
		if err != nil {
			t.Fatal(err)
		}
		times, err := currentFXObservationTimes(marks)
		if err != nil || len(times) != 3 || !times["owner_fx_1_0"][0].Equal(mustTime("2025-01-01T00:00:00.000000001Z")) || !times["\x00owner_fx_1_0"][0].Equal(mustTime("2025-01-01T00:00:00Z")) {
			t.Fatalf("namespaces collided: %+v %v", times, err)
		}
		public, err := json.Marshal(publicCashValuationFX(&marks[1]))
		if err != nil || strings.Contains(string(public), `\u0000`) {
			t.Fatal("internal time key leaked")
		}
	})
}

func TestOwnerFXSchemaInventoryAndLaterCorruptionFailClosed(t *testing.T) {
	for _, table := range []string{"owner_fx_series_versions", "fx_observations"} {
		t.Run(table, func(t *testing.T) {
			svc := ownerFXServiceForTest(t)
			svc.db.SetMaxOpenConns(1)
			if _, err := svc.db.Exec(`CREATE TEMP TRIGGER owner_test_ignore BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(IGNORE); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.importOwnerFXSeries(context.Background(), []byte(ownerFXTestInput)); err == nil {
				t.Fatal("extra connection-local trigger accepted")
			}
		})
	}
	svc := ownerFXServiceForTest(t)
	importOwnerFXForTest(t, svc, []byte(ownerFXTestInput))
	var guard string
	if err := svc.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='owner_fx_series_versions_no_update'`).Scan(&guard); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER owner_fx_series_versions_no_update; UPDATE owner_fx_series_versions SET record_sha256=?`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(guard); err != nil {
		t.Fatal(err)
	}
	if r, appErr := svc.currentPortfolioValuation(context.Background()); appErr == nil || appErr.body.Message != "internal server error" || r != nil {
		t.Fatal("empty portfolio bypassed later journal proof")
	}
}

func TestOwnerFXFutureCaptureAndRequestBinding(t *testing.T) {
	svc := ownerFXServiceForTest(t)
	if _, err := svc.importOwnerFXSeries(context.Background(), ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) { i.FetchedAt = "2026-01-01T00:00:00.000000001Z" })); err == nil {
		t.Fatal("capture newer than the server clock accepted")
	}
	ctx := context.Background()
	tx, err := svc.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	at := mustTime("2026-01-01T00:00:00Z")
	inputs, err := readValuationFXInputs(ctx, tx, at)
	if err != nil {
		t.Fatal(err)
	}
	prices, err := readValuationPriceInputs(ctx, tx, at)
	if err != nil {
		t.Fatal(err)
	}
	if value, appErr := svc.currentPortfolioValuationTx(ctx, tx, at.Add(time.Nanosecond), inputs, prices); appErr == nil || value != nil {
		t.Fatal("FX selection escaped its report cutoff")
	}
	foreign := inputs
	foreign.tx = nil
	if report, appErr := svc.portfolioTWRTx(ctx, tx, at, foreign, prices); appErr == nil || report != nil {
		t.Fatal("FX selection escaped its transaction")
	}
}

func TestOwnerFXSharedInputAndReceiptContract(t *testing.T) {
	schema := readJSONMap(t, filepath.Join("..", "..", "contracts", "owner-fx-series.schema.json"))
	input, _, _, err := decodeOwnerFXSeries(context.Background(), fixture(t, "owner-fx-usdkrw-sample.json"))
	if err != nil || input == nil || !input.Sample || input.BaseCurrency != "USD" || input.QuoteCurrency != "KRW" {
		t.Fatalf("shared owner sample drift: %v", err)
	}
	receipt := importOwnerFXForTest(t, ownerFXServiceForTest(t), []byte(ownerFXTestInput))
	for name, value := range map[string]any{"input": input, "receipt": receipt} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		definition := schema
		if name == "receipt" {
			definition = schema["$defs"].(map[string]any)["receipt"].(map[string]any)
		}
		if !reflect.DeepEqual(jsonKeySet(fields), jsonStringSet(definition["required"])) {
			t.Fatalf("%s contract fields differ", name)
		}
	}
}
