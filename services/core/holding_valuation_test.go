package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

const holdingValuationAsOf = "2026-01-11T14:00:00Z"

func TestHoldingValuationUsesExactNativePriceAndKeepsSnapshotUnavailable(t *testing.T) {
	svc := holdingValuationService(t)
	recordHoldingValuationPrice(t, svc, "fixture_aapl_old", "XNAS", "12", "2026-01-10T13:00:00Z", "2026-01-10T13:00:01Z", "2026-01-11T12:00:00Z")
	price := recordHoldingValuationPrice(t, svc, "fixture_aapl_xnas", "XNAS", "15", "2026-01-10T14:00:00Z", "2026-01-10T14:00:01Z", "2026-01-11T13:00:00Z")
	svc.now = func() time.Time { return mustTime(holdingValuationAsOf) }

	valuation, appErr := svc.holdingValuation(context.Background(), holdingValuationAsOf)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if valuation.Scope != "holdings_only" || valuation.PolicyVersion != "native_holding_valuation_v1" ||
		valuation.MaxObservationAgeSeconds != 86400 || valuation.ValuationAsOf != holdingValuationAsOf ||
		valuation.LedgerRevision != "rev_0000000001" || valuation.Status != "stale_sample" || !valuation.Sample {
		t.Fatalf("holding valuation contract drifted: %+v", valuation)
	}
	if len(valuation.Lines) != 1 {
		t.Fatalf("expected one holding line: %+v", valuation.Lines)
	}
	line := valuation.Lines[0]
	if line.InstrumentID != "instrument_aapl" || line.Symbol != "AAPL" || line.Quantity != "3" ||
		line.CostBasis != "31" || line.Currency != "USD" || line.Status != "valued" || line.Issue != nil ||
		line.MarketValue == nil || *line.MarketValue != "45" || line.UnrealizedPnL == nil || *line.UnrealizedPnL != "14" {
		t.Fatalf("native holding math drifted: %+v", line)
	}
	if line.Price == nil || line.Price.ObservationID != price.ObservationID || line.Price.Source != "local_fixture" ||
		line.Price.Venue != "XNAS" || line.Price.Currency != "USD" || line.Price.Price != "15" ||
		line.Price.PriceAdjustment != marketDataAdjustmentUnspecified || line.Price.ObservedAt != "2026-01-10T14:00:00Z" ||
		line.Price.FetchedAt != "2026-01-10T14:00:01Z" || line.Price.RecordedAt != "2026-01-11T13:00:00Z" ||
		!line.Price.Sample || line.Price.State != "stale" {
		t.Fatalf("price provenance drifted: %+v", line.Price)
	}
	if len(valuation.Totals) != 1 || valuation.Totals[0].Currency != "USD" || valuation.Totals[0].CostBasis != "31" ||
		valuation.Totals[0].MarketValue != "45" || valuation.Totals[0].UnrealizedPnL != "14" {
		t.Fatalf("native currency totals drifted: %+v", valuation.Totals)
	}
	if len(valuation.Issues) != 1 || valuation.Issues[0].Code != "sample_data" {
		t.Fatalf("sample status is not explicit: %+v", valuation.Issues)
	}
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil || snapshot.ValuationStatus != "unavailable" {
		t.Fatalf("internal holding valuation changed public authority: snapshot=%+v err=%v", snapshot, err)
	}
}

func TestHoldingValuationSuppressesTotalsAndFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		prices   []struct{ id, venue, observed, fetched, recorded string }
		wantCode string
	}{
		{name: "missing", wantCode: "missing_security_price"},
		{name: "ambiguous venue", prices: []struct{ id, venue, observed, fetched, recorded string }{
			{"fixture_xnas", "XNAS", "2026-01-11T13:00:00Z", "2026-01-11T13:00:01Z", "2026-01-11T13:00:02Z"},
			{"fixture_bats", "BATS", "2026-01-11T13:01:00Z", "2026-01-11T13:01:01Z", "2026-01-11T13:01:02Z"},
		}, wantCode: "ambiguous_security_price_venue"},
		{name: "older than 24h boundary", prices: []struct{ id, venue, observed, fetched, recorded string }{
			{"fixture_stale", "XNAS", "2026-01-10T13:59:59.999999999Z", "2026-01-10T14:00:00Z", "2026-01-11T13:00:00Z"},
		}, wantCode: "stale_security_price"},
		{name: "future recorded is missing", prices: []struct{ id, venue, observed, fetched, recorded string }{
			{"fixture_future_recorded", "XNAS", "2026-01-11T13:00:00Z", "2026-01-11T13:00:01Z", "2026-01-11T14:00:01Z"},
		}, wantCode: "missing_security_price"},
		{name: "future observed is missing", prices: []struct{ id, venue, observed, fetched, recorded string }{
			{"fixture_future_observed", "XNAS", "2026-01-11T14:00:01Z", "2026-01-11T14:00:01Z", "2026-01-11T14:00:02Z"},
		}, wantCode: "missing_security_price"},
		{name: "future fetched is missing", prices: []struct{ id, venue, observed, fetched, recorded string }{
			{"fixture_future_fetched", "XNAS", "2026-01-11T13:00:00Z", "2026-01-11T14:00:01Z", "2026-01-11T14:00:02Z"},
		}, wantCode: "missing_security_price"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := holdingValuationService(t)
			for _, price := range test.prices {
				recordHoldingValuationPrice(t, svc, price.id, price.venue, "15", price.observed, price.fetched, price.recorded)
			}
			svc.now = func() time.Time { return mustTime("2026-01-11T15:00:00Z") }
			valuation, appErr := svc.holdingValuation(context.Background(), holdingValuationAsOf)
			if appErr != nil {
				t.Fatal(appErr)
			}
			if valuation.Status != "unavailable" || valuation.Totals != nil || len(valuation.Lines) != 1 ||
				valuation.Lines[0].Issue == nil || valuation.Lines[0].Issue.Code != test.wantCode ||
				len(valuation.Issues) != 1 || valuation.Issues[0].Code != test.wantCode {
				t.Fatalf("unavailable holding was aggregated or mislabeled: %+v", valuation)
			}
			for _, forbidden := range []string{"fixture_", "record_sha256", "SELECT ", "security_price_observations"} {
				if strings.Contains(valuation.Lines[0].Issue.Message, forbidden) || strings.Contains(valuation.Issues[0].Message, forbidden) {
					t.Fatalf("issue leaked internal detail %q: %+v", forbidden, valuation.Issues)
				}
			}
		})
	}

	t.Run("corrupt ledger revision", func(t *testing.T) {
		svc := holdingValuationService(t)
		if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=99 WHERE singleton=1`); err != nil {
			t.Fatal(err)
		}
		svc.now = func() time.Time { return mustTime(holdingValuationAsOf) }
		valuation, appErr := svc.holdingValuation(context.Background(), holdingValuationAsOf)
		if valuation != nil || appErr == nil || appErr.status != http.StatusInternalServerError || appErr.body.Message != "internal server error" {
			t.Fatalf("corrupt ledger revision was certified: valuation=%+v err=%v", valuation, appErr)
		}
	})
}

func holdingValuationService(t *testing.T) *Service {
	t.Helper()
	svc, _ := testService(t, nil, nil)
	applyTestCSV(t, svc, "holding-valuation-seed", testCSV(
		"aapl-buy,account-main,BUY,2026-01-10T10:00:00Z,AAPL,3,10,1,USD,-31",
	))
	return svc
}

func recordHoldingValuationPrice(t *testing.T, svc *Service, sourceID, venue, price, observedAt, fetchedAt, recordedAt string) *SecurityPriceObservation {
	t.Helper()
	svc.now = func() time.Time { return mustTime(recordedAt) }
	observation, err := svc.recordSecurityPriceObservation(context.Background(), SecurityPriceObservationInput{
		Source: "local_fixture", SourceObservationID: sourceID, InstrumentID: "instrument_aapl", Symbol: "AAPL",
		Venue: venue, Currency: "USD", Price: price, PriceAdjustment: marketDataAdjustmentUnspecified,
		ObservedAt: observedAt, FetchedAt: fetchedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return observation
}
