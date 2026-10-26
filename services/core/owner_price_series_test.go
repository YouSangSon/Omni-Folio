package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

const ownerPriceTestInput = `{"schema_version":"owner-price-series.v1","source_version":"price-v1","instrument_id":"instrument_aapl","symbol":"AAPL","venue":"XNAS","currency":"USD","price_adjustment":"unadjusted","sample":false,"fetched_at":"2026-01-01T00:00:00Z","points":[{"observed_at":"2025-01-01T00:00:00Z","price":"100"},{"observed_at":"2026-01-01T00:00:00Z","price":"200"}]}`

func ownerPriceInputForTest(t testing.TB, change func(*OwnerPriceSeriesInput)) []byte {
	t.Helper()
	var input OwnerPriceSeriesInput
	if err := json.Unmarshal([]byte(ownerPriceTestInput), &input); err != nil {
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

func ownerPriceServiceForTest(t *testing.T) *Service {
	t.Helper()
	svc := ownerFXServiceForTest(t)
	applyTestCSV(t, svc, "owner-price-ledger", testCSV("capital,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,USD,100", "buy,account-main,BUY,2025-01-02T00:00:00Z,AAPL,1,100,0,USD,-100"))
	return svc
}

func importOwnerPriceForTest(t testing.TB, svc *Service, raw []byte) *OwnerPriceSeriesReceipt {
	t.Helper()
	r, err := svc.importOwnerPriceSeries(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOwnerPriceClosedInputAndWholeVersionBounds(t *testing.T) {
	bad := map[string][]byte{
		"unknown":              []byte(strings.Replace(ownerPriceTestInput, `"sample":false`, `"sample":false,"provider_verified":true`, 1)),
		"escaped duplicate":    []byte(strings.Replace(ownerPriceTestInput, `"sample":false`, `"sample":false,"s\u0061mple":true`, 1)),
		"null":                 []byte(strings.Replace(ownerPriceTestInput, `"sample":false`, `"sample":null`, 1)),
		"null points":          ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.Points = nil }),
		"phantom canonical id": ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.InstrumentID = "unrelated" }),
		"unknown point":        []byte(strings.Replace(ownerPriceTestInput, `"price":"200"`, `"price":"200","extra":true`, 1)),
		"adjusted":             ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.PriceAdjustment = "split_adjusted" }),
		"unspecified":          ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.PriceAdjustment = "unspecified" }),
		"zero":                 ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.Points[0].Price = "0" }),
		"noncanonical":         ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.Points[0].Price = "100.0" }),
		"duplicate time":       ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.Points[1].ObservedAt = i.Points[0].ObservedAt }),
		"point after fetch":    ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.Points[1].ObservedAt = "2026-01-01T00:00:00.000000001Z" }),
		"version bytes":        ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.SourceVersion = strings.Repeat("한", 43) }),
		"surrogate":            []byte(strings.Replace(ownerPriceTestInput, "price-v1", `\ud800`, 1)),
		"trailing":             []byte(ownerPriceTestInput + ` {}`),
		"raw bound":            []byte(strings.Repeat(" ", maxBodyBytes) + ownerPriceTestInput),
	}
	for name, raw := range bad {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := decodeOwnerPriceSeries(context.Background(), raw); err == nil {
				t.Fatal("invalid price input accepted")
			}
		})
	}
	for _, n := range []int{0, 10000, 10001} {
		raw := ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) {
			i.Points = []OwnerPricePoint{}
			for j := 0; j < n; j++ {
				i.Points = append(i.Points, OwnerPricePoint{ObservedAt: mustTime("2025-01-01T00:00:00Z").Add(time.Duration(j)).Format(time.RFC3339Nano), Price: "1"})
			}
		})
		i, _, _, err := decodeOwnerPriceSeries(context.Background(), raw)
		if n > 10000 {
			if err == nil {
				t.Fatal("point bound ignored")
			}
		} else if err != nil || len(i.Points) != n {
			t.Fatalf("valid count %d rejected: %v", n, err)
		}
	}
}

func TestOwnerPriceCorrectionWithdrawalOldRetryAndSourcePin(t *testing.T) {
	svc := ownerPriceServiceForTest(t)
	first := importOwnerPriceForTest(t, svc, []byte(ownerPriceTestInput))
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00.1Z") }
	importOwnerPriceForTest(t, svc, ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) {
		i.SourceVersion = "a-correction"
		i.Venue = "XNYS"
		i.Sample = true
		i.Points = i.Points[1:]
		i.Points[0].Price = "300"
	}))
	for _, test := range []struct {
		cutoff, version, price string
		count                  int
	}{{"2026-01-01T00:00:00Z", "price-v1", "200", 2}, {"2026-01-01T00:00:00.1Z", "a-correction", "300", 1}} {
		marks, err := valuationPriceObservations(context.Background(), svc.db, mustTime(test.cutoff))
		if err != nil || len(marks) != test.count || marks[len(marks)-1].ownerVersion != test.version || marks[len(marks)-1].Price != test.price {
			t.Fatalf("whole version drift: %+v %v", marks, err)
		}
	}
	importOwnerPriceForTest(t, svc, ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.SourceVersion = "withdrawn"; i.Points = []OwnerPricePoint{} }))
	svc.now = func() time.Time { t.Fatal("old retry called clock"); return time.Time{} }
	if r := importOwnerPriceForTest(t, svc, []byte(ownerPriceTestInput)); !reflect.DeepEqual(r, first) {
		t.Fatal("old receipt changed")
	}
	marks, err := valuationPriceObservations(context.Background(), svc.db, mustTime("2026-01-01T00:00:00.2Z"))
	if err != nil || len(marks) != 0 {
		t.Fatal("withdrawal or retry fell back")
	}
	svc.now = func() time.Time { return mustTime("2026-01-01T00:00:00.2Z") }
	if _, err := svc.recordSecurityPriceObservation(context.Background(), SecurityPriceObservationInput{Source: "local_fixture", SourceObservationID: "blocked", InstrumentID: "instrument_aapl", Symbol: "AAPL", Venue: "XNAS", Currency: "USD", Price: "200", PriceAdjustment: "unspecified", ObservedAt: "2026-01-01T00:00:00Z", FetchedAt: "2026-01-01T00:00:00Z"}); err == nil {
		t.Fatal("withdrawal released price source")
	}
	if _, err := svc.importOwnerPriceSeries(context.Background(), ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.Sample = true })); err == nil {
		t.Fatal("same version changed payload")
	}
}

func TestOwnerPriceLedgerMembershipAndMixedCaseHolding(t *testing.T) {
	svc := ownerPriceServiceForTest(t)
	if _, err := svc.importOwnerPriceSeries(context.Background(), ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.Symbol = "MSFT"; i.InstrumentID = "instrument_msft" })); err == nil {
		t.Fatal("unadmitted instrument accepted")
	}
	if _, err := svc.db.Exec(`INSERT INTO events(event_id,source_event_id,account_id,type,occurred_at,instrument_id,symbol,quantity,price,fee,currency,amount,receipt_id,recorded_at) VALUES('legacy-case','legacy-case','account-main','BUY','2025-01-03T00:00:00Z','instrument_aapl','aapl','1','10','0','USD','-10','receipt-case','2026-01-01T00:00:00Z'); UPDATE ledger_meta SET revision=3,recorded_at='2026-01-01T00:00:00Z' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	importOwnerPriceForTest(t, svc, []byte(ownerPriceTestInput))
	value, appErr := svc.holdingValuation(context.Background(), "2026-01-01T00:00:00Z")
	if appErr != nil || len(value.Lines) != 1 || value.Lines[0].MarketValue == nil || *value.Lines[0].MarketValue != "400" || value.Sample || value.Status != "complete" {
		t.Fatalf("canonical mixed-case identity lost: %+v %v", value, appErr)
	}
	// A self-consistent rewrite still needs canonical ledger membership during recovery.
	var guards [2]string
	for n, name := range []string{"owner_price_series_versions_no_update", "owner_price_series_versions_insert_guard"} {
		if err := svc.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name=?`, name).Scan(&guards[n]); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`DROP TRIGGER ` + name); err != nil {
			t.Fatal(err)
		}
	}
	raw := ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.Symbol = "MSFT"; i.InstrumentID = "instrument_msft" })
	i, canonical, sha, err := decodeOwnerPriceSeries(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	r := ownerPriceSeriesRecord{Sequence: 1, Input: *i, DataSHA256: sha, RecordedAt: "2026-01-01T00:00:00Z"}
	recordSHA, err := ownerPriceRecordSHA(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE owner_price_series_versions SET instrument_id=?,series_json=?,series_sha256=?,record_sha256=?`, i.InstrumentID, string(canonical), sha, recordSHA); err != nil {
		t.Fatal(err)
	}
	for _, guard := range guards {
		if _, err := svc.db.Exec(guard); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := proveOwnerPriceSeriesRecovery(context.Background(), svc.db); err == nil {
		t.Fatal("self-consistent phantom journal passed recovery")
	}
}

func TestOwnerPriceSplitBoundaryAndIndependentSampleFlags(t *testing.T) {
	for _, test := range []struct{ at, status string }{{"2025-12-31T23:00:00Z", "unavailable"}, {"2025-12-31T23:00:00.000000001Z", "complete"}} {
		svc := ownerPriceServiceForTest(t)
		applyTestCSV(t, svc, "split", testCSV("split,account-main,SPLIT,2025-12-31T23:00:00Z,AAPL,2,,,USD,0"))
		importOwnerPriceForTest(t, svc, ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.Points = []OwnerPricePoint{{ObservedAt: test.at, Price: "50"}} }))
		value, appErr := svc.holdingValuation(context.Background(), "2026-01-01T00:00:00Z")
		if appErr != nil || value.Status != test.status || value.Sample || !usesOwnerPrice(value.Issues) {
			t.Fatalf("split boundary drift: %+v %v", value, appErr)
		}
		if test.status == "unavailable" && (value.Lines[0].Price == nil || value.Lines[0].Issue.Code != "split_price_unavailable" || len(value.Totals) != 0) {
			t.Fatal("split failure hid source or exposed total")
		}
		importOwnerFXForTest(t, svc, []byte(ownerFXTestInput))
		current, appErr := svc.currentPortfolioValuation(context.Background())
		if appErr != nil || current.Status != test.status || current.Sample || !usesOwnerPrice(current.Issues) {
			t.Fatalf("current split boundary drift: %+v %v", current, appErr)
		}
		twr, appErr := svc.portfolioTWR(context.Background())
		if appErr != nil || (test.status == "complete" && twr.Status != "calculated") || (test.status == "unavailable" && twr.CumulativeRate != nil) || !usesOwnerPrice(twr.Issues) {
			t.Fatalf("TWR split boundary drift: %+v %v", twr, appErr)
		}
	}
}

func TestOwnerPriceHistoricalCorrectionDoesNotFillFromOldVersion(t *testing.T) {
	svc := ownerPriceServiceForTest(t)
	applyTestCSV(t, svc, "later-capital", testCSV("later,account-main,DEPOSIT,2025-01-03T00:00:00Z,,,,,USD,10"))
	importOwnerFXForTest(t, svc, ownerFXInputForTest(t, func(i *OwnerFXSeriesInput) {
		i.Points = append(i.Points[:1], OwnerFXPoint{ObservedAt: "2025-01-03T00:00:00Z", Rate: "1000"}, i.Points[1])
	}))
	importOwnerPriceForTest(t, svc, ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) {
		i.Points = append(i.Points[:1], OwnerPricePoint{ObservedAt: "2025-01-03T00:00:00Z", Price: "100"}, i.Points[1])
	}))
	if report, err := svc.portfolioTWR(context.Background()); err != nil || report.Status != "calculated" {
		t.Fatalf("complete historical version unavailable: %+v %v", report, err)
	}
	importOwnerPriceForTest(t, svc, ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.SourceVersion = "removed-history" }))
	current, err := svc.currentPortfolioValuation(context.Background())
	if err != nil || current.Status != "complete" {
		t.Fatalf("terminal price unexpectedly removed: %+v %v", current, err)
	}
	report, err := svc.portfolioTWR(context.Background())
	if err != nil || report.Status != "unavailable" || report.CumulativeRate != nil || !usesOwnerPrice(report.Issues) {
		t.Fatalf("old historical point silently filled: %+v %v", report, err)
	}
}

func TestOwnerPriceRequestBindingAndFutureCapture(t *testing.T) {
	svc := ownerPriceServiceForTest(t)
	ctx := context.Background()
	if _, err := svc.importOwnerPriceSeries(ctx, ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.FetchedAt = "2026-01-01T00:00:00.000000001Z" })); err == nil {
		t.Fatal("future capture accepted")
	}
	tx, err := svc.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	at := svc.now()
	fx, err := readValuationFXInputs(ctx, tx, at)
	if err != nil {
		t.Fatal(err)
	}
	prices, err := readValuationPriceInputs(ctx, tx, at)
	if err != nil {
		t.Fatal(err)
	}
	foreign := prices
	foreign.tx = nil
	if value, appErr := svc.currentPortfolioValuationTx(ctx, tx, at, fx, foreign); appErr == nil || value != nil {
		t.Fatal("price proof escaped transaction")
	}
	foreign = prices
	foreign.knownAt = at.Add(time.Nanosecond)
	if report, appErr := svc.portfolioTWRTx(ctx, tx, at, fx, foreign); appErr == nil || report != nil {
		t.Fatal("price version escaped report cutoff")
	}
}

func TestOwnerPriceFixtureBindingAndInternalTimeNamespace(t *testing.T) {
	t.Run("fixture cannot become owner", func(t *testing.T) {
		svc := ownerPriceServiceForTest(t)
		if _, err := svc.recordSecurityPriceObservation(context.Background(), SecurityPriceObservationInput{Source: "local_fixture", SourceObservationID: "aapl", InstrumentID: "instrument_aapl", Symbol: "AAPL", Venue: "XNAS", Currency: "USD", Price: "100", PriceAdjustment: "unspecified", ObservedAt: "2026-01-01T00:00:00Z", FetchedAt: "2026-01-01T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.importOwnerPriceSeries(context.Background(), []byte(ownerPriceTestInput)); err == nil {
			t.Fatal("fixture source became owner")
		}
		proof, err := proveOwnerPriceSeriesRecovery(context.Background(), svc.db)
		if err != nil || proof.Versions != 0 {
			t.Fatal("failed admission changed journal")
		}
	})
	t.Run("public ID prefix is not a namespace", func(t *testing.T) {
		svc := ownerPriceServiceForTest(t)
		originalID := svc.id
		svc.id = func(prefix string) string {
			if prefix == "security_price_observation" {
				return "owner_price_1_0"
			}
			return originalID(prefix)
		}
		if _, err := svc.recordSecurityPriceObservation(context.Background(), SecurityPriceObservationInput{Source: "local_fixture", SourceObservationID: "msft", InstrumentID: "instrument_msft", Symbol: "MSFT", Venue: "XNAS", Currency: "USD", Price: "1", PriceAdjustment: "unspecified", ObservedAt: "2025-01-01T00:00:00.000000001Z", FetchedAt: "2026-01-01T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		importOwnerPriceForTest(t, svc, []byte(ownerPriceTestInput))
		marks, err := valuationPriceObservations(context.Background(), svc.db, svc.now())
		if err != nil {
			t.Fatal(err)
		}
		times, err := currentPriceObservationTimes(marks)
		if err != nil || len(times) != 3 || !times["owner_price_1_0"][0].Equal(mustTime("2025-01-01T00:00:00.000000001Z")) || !times["\x00owner_price_1_0"][0].Equal(mustTime("2025-01-01T00:00:00Z")) {
			t.Fatalf("price time namespaces collided: %+v %v", times, err)
		}
		checkOwnerFXHTTPGolden(t, svc, "/v1/portfolio/holding-valuation?as_of=2026-01-01T00:00:00Z", ownerPriceGoldenForTest(t)["holding"])
	})
}

func TestOwnerPriceSharedInputAndReceiptContract(t *testing.T) {
	schema := readJSONMap(t, "../../contracts/owner-price-series.schema.json")
	input, _, _, err := decodeOwnerPriceSeries(context.Background(), fixture(t, "owner-price-aapl-sample.json"))
	if err != nil || !input.Sample || input.PriceAdjustment != "unadjusted" {
		t.Fatalf("shared price sample drift: %v", err)
	}
	receipt := importOwnerPriceForTest(t, ownerPriceServiceForTest(t), []byte(ownerPriceTestInput))
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
			t.Fatalf("%s schema/runtime fields differ", name)
		}
	}
}
