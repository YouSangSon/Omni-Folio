package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func securityPriceInput() SecurityPriceObservationInput {
	return SecurityPriceObservationInput{
		Source: "local_fixture", SourceObservationID: "aapl_close_20260110", InstrumentID: "US0378331005",
		Symbol: "AAPL", Venue: "XNAS", Currency: "USD", Price: "250.25",
		PriceAdjustment: marketDataAdjustmentUnspecified, ObservedAt: "2026-01-10T15:00:00Z", FetchedAt: "2026-01-10T15:00:01Z",
	}
}

func declareTestInstrumentListing(t *testing.T, svc *Service, instrumentID string) {
	t.Helper()
	if _, err := svc.declareInstrumentListing(context.Background(), InstrumentListingInput{
		InstrumentID: instrumentID, Venue: "XKRX", Symbol: "005930", Currency: "KRW",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestKiwoomLatestTradeCapturePersistsOnceAndPreservesKnownGood(t *testing.T) {
	svc, _ := testService(t, []time.Time{mustTime("2026-08-24T01:30:02Z")}, nil)
	declareTestInstrumentListing(t, svc, "instrument_005930")
	row := `{"cur_prc":"1250","trde_qty":"5","cntr_tm":"20260824103000","open_pric":"1200","high_pric":"1300","low_pric":"1150"}`
	client := onePageLatestTradeClient(t, tradeBody("005930", row))
	client.now = func() time.Time { return mustTime("2026-08-24T01:30:01Z") }

	stored, err := svc.captureKiwoomLatestTradeObservation(context.Background(), client, "005930")
	if err != nil {
		t.Fatal(err)
	}
	before, proof, err := replaySecurityPriceObservations(context.Background(), svc.db)
	if err != nil || len(before) != 1 || before[0].Source != "kiwoom_production" || before[0].InstrumentID != "instrument_005930" {
		t.Fatalf("captured observation=%+v proof=%+v err=%v", before, proof, err)
	}

	replayClient := onePageLatestTradeClient(t, tradeBody("005930", row))
	replayClient.now = func() time.Time { return mustTime("2026-08-24T01:30:03Z") }
	replayed, err := svc.captureKiwoomLatestTradeObservation(context.Background(), replayClient, "005930")
	if err != nil || !reflect.DeepEqual(stored, replayed) {
		t.Fatalf("same-slot capture drifted: stored=%+v replayed=%+v err=%v", stored, replayed, err)
	}

	failedScript := &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("token"))
		},
		func(*http.Request) *http.Response { return kiwoomResponse(http.StatusInternalServerError, nil, `{}`) },
	}}
	failedClient := newSyntheticKiwoomClient(t, KiwoomProduction, failedScript)
	if observation, err := svc.captureKiwoomLatestTradeObservation(context.Background(), failedClient, "005930"); err == nil || observation != nil || failedScript.calls != 2 {
		t.Fatalf("provider failure was retried or stored: observation=%+v calls=%d err=%v", observation, failedScript.calls, err)
	}
	conflictClient := onePageLatestTradeClient(t, tradeBody("005930", strings.Replace(row, `"1250"`, `"1251"`, 1)))
	conflictClient.now = func() time.Time { return mustTime("2026-08-24T01:30:04Z") }
	if observation, err := svc.captureKiwoomLatestTradeObservation(context.Background(), conflictClient, "005930"); err == nil || observation != nil {
		t.Fatalf("same-slot price conflict was stored: observation=%+v err=%v", observation, err)
	}
	after, afterProof, err := replaySecurityPriceObservations(context.Background(), svc.db)
	if err != nil || !reflect.DeepEqual(before, after) || proof != afterProof {
		t.Fatalf("failed capture changed known-good evidence: before=%+v after=%+v proof=%+v after_proof=%+v err=%v", before, after, proof, afterProof, err)
	}

	networkCalls := 0
	guardedClient := newSyntheticKiwoomClient(t, KiwoomMock, kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
		networkCalls++
		return nil, errors.New("unexpected network call")
	}))
	for _, input := range []struct {
		client *KiwoomClient
		symbol string
	}{
		{nil, "005930"},
		{guardedClient, "A005930"},
	} {
		if observation, err := svc.captureKiwoomLatestTradeObservation(context.Background(), input.client, input.symbol); err == nil || observation != nil {
			t.Fatalf("invalid capture identity was accepted: input=%+v observation=%+v err=%v", input, observation, err)
		}
	}
	if networkCalls != 0 {
		t.Fatalf("invalid capture identity reached network %d times", networkCalls)
	}
}

func TestKiwoomLatestTradeRecordsDurablePriceObservation(t *testing.T) {
	svc, _ := testService(t, []time.Time{
		mustTime("2026-08-24T01:30:02Z"),
		mustTime("2026-08-24T01:30:03Z"),
	}, nil)
	declareTestInstrumentListing(t, svc, "instrument_005930")
	trade := KiwoomLatestTrade{
		Source: "kiwoom", Environment: KiwoomProduction, Exchange: KiwoomKRX,
		Symbol: "005930", Currency: "KRW", Price: "1250",
		ObservedAt: "2026-08-24T01:30:00Z", FetchedAt: "2026-08-24T01:30:01Z",
	}

	stored, err := svc.recordKiwoomLatestTradeObservation(context.Background(), trade)
	if err != nil {
		t.Fatal(err)
	}
	if stored.InstrumentID != "instrument_005930" {
		t.Fatalf("Kiwoom observation did not use the ledger instrument identity: %+v", stored)
	}
	replayed, err := svc.recordKiwoomLatestTradeObservation(context.Background(), trade)
	if err != nil || !reflect.DeepEqual(stored, replayed) {
		t.Fatalf("exact Kiwoom observation replay drifted: stored=%+v replayed=%+v err=%v", stored, replayed, err)
	}
	nextFetch := trade
	nextFetch.FetchedAt = "2026-08-24T01:30:02Z"
	replayed, err = svc.recordKiwoomLatestTradeObservation(context.Background(), nextFetch)
	if err != nil || !reflect.DeepEqual(stored, replayed) {
		t.Fatalf("same-slot Kiwoom observation replay drifted: stored=%+v replayed=%+v err=%v", stored, replayed, err)
	}
	changedPrice := nextFetch
	changedPrice.Price = "1251"
	if _, err := svc.recordKiwoomLatestTradeObservation(context.Background(), changedPrice); err == nil {
		t.Fatal("same-second Kiwoom price ambiguity was stored")
	}
	directReplay := SecurityPriceObservationInput{
		Source: stored.Source, SourceObservationID: stored.SourceObservationID, InstrumentID: stored.InstrumentID,
		Symbol: stored.Symbol, Venue: stored.Venue, Currency: stored.Currency, Price: stored.Price,
		PriceAdjustment: stored.PriceAdjustment, ObservedAt: stored.ObservedAt, FetchedAt: nextFetch.FetchedAt,
	}
	if replayed, err = svc.recordSecurityPriceObservation(context.Background(), directReplay); err != nil || !reflect.DeepEqual(stored, replayed) {
		t.Fatalf("atomic same-slot replay drifted: stored=%+v replayed=%+v err=%v", stored, replayed, err)
	}
	tamperedID := directReplay
	tamperedID.SourceObservationID = strings.Repeat("0", 64)
	if _, err := svc.recordSecurityPriceObservation(context.Background(), tamperedID); err == nil {
		t.Fatal("Kiwoom observation accepted an identity not derived from its slot")
	}
	tamperedInstrument := directReplay
	tamperedInstrument.InstrumentID = "krx_005930"
	tamperedInstrument.SourceObservationID, _ = kiwoomLatestTradeObservationID(
		tamperedInstrument.Source, tamperedInstrument.InstrumentID, tamperedInstrument.Symbol,
		tamperedInstrument.Venue, tamperedInstrument.Currency, tamperedInstrument.PriceAdjustment,
		tamperedInstrument.ObservedAt,
	)
	if _, err := svc.recordSecurityPriceObservation(context.Background(), tamperedInstrument); err == nil {
		t.Fatal("Kiwoom observation accepted an instrument identity outside the ledger rule")
	}
	for name, mutate := range map[string]func(*SecurityPriceObservationInput){
		"symbol":   func(v *SecurityPriceObservationInput) { v.Symbol = "AAPL" },
		"venue":    func(v *SecurityPriceObservationInput) { v.Venue = "XNAS" },
		"currency": func(v *SecurityPriceObservationInput) { v.Currency = "USD" },
	} {
		t.Run("direct_"+name, func(t *testing.T) {
			badMarket := directReplay
			mutate(&badMarket)
			badMarket.SourceObservationID, _ = kiwoomLatestTradeObservationID(
				badMarket.Source, badMarket.InstrumentID, badMarket.Symbol, badMarket.Venue,
				badMarket.Currency, badMarket.PriceAdjustment, badMarket.ObservedAt,
			)
			if _, err := svc.recordSecurityPriceObservation(context.Background(), badMarket); err == nil {
				t.Fatalf("direct Kiwoom observation accepted an invalid market identity: %+v", badMarket)
			}
		})
	}
	mockTrade := trade
	mockTrade.Environment = KiwoomMock
	if _, err := svc.recordKiwoomLatestTradeObservation(context.Background(), mockTrade); err != nil {
		t.Fatal(err)
	}
	invalidTrades := map[string]func(*KiwoomLatestTrade){
		"source":      func(v *KiwoomLatestTrade) { v.Source = "other" },
		"environment": func(v *KiwoomLatestTrade) { v.Environment = "other" },
		"exchange":    func(v *KiwoomLatestTrade) { v.Exchange = KiwoomNXT },
		"symbol":      func(v *KiwoomLatestTrade) { v.Symbol = "AAPL" },
		"currency":    func(v *KiwoomLatestTrade) { v.Currency = "USD" },
		"price":       func(v *KiwoomLatestTrade) { v.Price = "0" },
		"observed_at": func(v *KiwoomLatestTrade) { v.ObservedAt = "not-a-time" },
	}
	for name, mutate := range invalidTrades {
		t.Run(name, func(t *testing.T) {
			invalid := trade
			mutate(&invalid)
			if _, err := svc.recordKiwoomLatestTradeObservation(context.Background(), invalid); err == nil {
				t.Fatalf("invalid Kiwoom trade was recorded: %+v", invalid)
			}
		})
	}
	observations, proof, err := replaySecurityPriceObservations(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 2 || proof.Observations != 2 || observations[0].Source != "kiwoom_production" ||
		observations[1].Source != "kiwoom_mock" || observations[0].SourceObservationID == observations[1].SourceObservationID ||
		!strategySHA256Pattern.MatchString(observations[0].SourceObservationID) || observations[0].Venue != "XKRX" ||
		observations[0].PriceAdjustment != marketDataAdjustmentUnspecified {
		t.Fatalf("Kiwoom durable observation contract drifted: observations=%+v proof=%+v", observations, proof)
	}
	if latest, err := latestSecurityPriceObservation(context.Background(), svc.db, "kiwoom_production", "instrument_005930", "005930", "XKRX", "KRW", marketDataAdjustmentUnspecified, "2026-08-24T01:30:03Z"); err == nil || latest != nil {
		t.Fatalf("Kiwoom observation became a public/local-fixture valuation source: latest=%+v err=%v", latest, err)
	}
}

func TestKiwoomCaptureRequiresActiveListingBeforeNetwork(t *testing.T) {
	svc, _ := testService(t, []time.Time{
		mustTime("2026-08-24T01:30:04Z"), mustTime("2026-08-24T01:30:05Z"),
	}, nil)
	networkCalls := 0
	client := newSyntheticKiwoomClient(t, KiwoomMock, kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
		networkCalls++
		return nil, errors.New("unexpected network call")
	}))
	if observation, err := svc.captureKiwoomLatestTradeObservation(context.Background(), client, "005930"); err == nil || observation != nil {
		t.Fatalf("unlisted capture was accepted: observation=%+v err=%v", observation, err)
	}
	if networkCalls != 0 {
		t.Fatalf("unlisted capture reached network %d times", networkCalls)
	}

	listing := InstrumentListingInput{InstrumentID: "instrument_005930", Venue: "XKRX", Symbol: "005930", Currency: "KRW"}
	if _, err := svc.declareInstrumentListing(context.Background(), listing); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.revokeInstrumentListing(context.Background(), listing); err != nil {
		t.Fatal(err)
	}
	if observation, err := svc.captureKiwoomLatestTradeObservation(context.Background(), client, "005930"); err == nil || observation != nil {
		t.Fatalf("revoked capture was accepted: observation=%+v err=%v", observation, err)
	}
	if networkCalls != 0 {
		t.Fatalf("revoked capture reached network %d times", networkCalls)
	}
	if _, err := svc.declareInstrumentListing(context.Background(), listing); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER instrument_listing_events_no_update;
		UPDATE instrument_listing_events SET record_sha256=? WHERE event_id=(
			SELECT event_id FROM instrument_listing_events ORDER BY sequence DESC LIMIT 1
		)`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if observation, err := svc.captureKiwoomLatestTradeObservation(context.Background(), client, "005930"); err == nil || observation != nil {
		t.Fatalf("corrupt listing capture was accepted: observation=%+v err=%v", observation, err)
	}
	if networkCalls != 0 {
		t.Fatalf("corrupt listing capture reached network %d times", networkCalls)
	}
}

func TestKiwoomListingControlsInstrumentAndWriteRace(t *testing.T) {
	trade := KiwoomLatestTrade{
		Source: "kiwoom", Environment: KiwoomProduction, Exchange: KiwoomKRX,
		Symbol: "005930", Currency: "KRW", Price: "1250",
		ObservedAt: "2026-08-24T01:30:00Z", FetchedAt: "2026-08-24T01:30:01Z",
	}
	svc, _ := testService(t, []time.Time{
		mustTime("2026-08-24T01:30:04Z"), mustTime("2026-08-24T01:30:05Z"),
	}, nil)
	declareTestInstrumentListing(t, svc, "instrument_samsung_equity")
	stored, err := svc.recordKiwoomLatestTradeObservation(context.Background(), trade)
	if err != nil || stored.InstrumentID != "instrument_samsung_equity" {
		t.Fatalf("owner-declared instrument was not used: observation=%+v err=%v", stored, err)
	}
	mismatched := SecurityPriceObservationInput{
		Source: "kiwoom_production", InstrumentID: "instrument_005930", Symbol: "005930", Venue: "XKRX", Currency: "KRW",
		Price: "1251", PriceAdjustment: marketDataAdjustmentUnspecified,
		ObservedAt: "2026-08-24T01:30:02Z", FetchedAt: "2026-08-24T01:30:03Z",
	}
	mismatched.SourceObservationID, _ = kiwoomLatestTradeObservationID(
		mismatched.Source, mismatched.InstrumentID, mismatched.Symbol, mismatched.Venue,
		mismatched.Currency, mismatched.PriceAdjustment, mismatched.ObservedAt,
	)
	if observation, err := svc.recordSecurityPriceObservation(context.Background(), mismatched); err == nil || observation != nil {
		t.Fatalf("direct Kiwoom writer bypassed listing ownership: observation=%+v err=%v", observation, err)
	}

	raceSvc, _ := testService(t, []time.Time{
		mustTime("2026-08-24T01:30:04Z"), mustTime("2026-08-24T01:30:05Z"),
	}, nil)
	raceListing := InstrumentListingInput{InstrumentID: "instrument_005930", Venue: "XKRX", Symbol: "005930", Currency: "KRW"}
	if _, err := raceSvc.declareInstrumentListing(context.Background(), raceListing); err != nil {
		t.Fatal(err)
	}
	row := `{"cur_prc":"1250","trde_qty":"5","cntr_tm":"20260824103000","open_pric":"1200","high_pric":"1300","low_pric":"1150"}`
	script := &kiwoomScript{t: t, steps: []func(*http.Request) *http.Response{
		func(*http.Request) *http.Response {
			return kiwoomResponse(http.StatusOK, nil, syntheticTokenJSON("token"))
		},
		func(request *http.Request) *http.Response {
			assertKiwoomRequest(t, request, kiwoomChartPath, "ka10079")
			if _, err := raceSvc.revokeInstrumentListing(context.Background(), raceListing); err != nil {
				t.Fatal(err)
			}
			return kiwoomResponse(http.StatusOK, map[string]string{"api-id": "ka10079"}, tradeBody("005930", row))
		},
	}}
	client := newSyntheticKiwoomClient(t, KiwoomProduction, script)
	client.now = func() time.Time { return mustTime("2026-08-24T01:30:01Z") }
	if observation, err := raceSvc.captureKiwoomLatestTradeObservation(context.Background(), client, "005930"); err == nil || observation != nil {
		t.Fatalf("capture stored after listing revocation: observation=%+v err=%v", observation, err)
	}
	observations, _, err := replaySecurityPriceObservations(context.Background(), raceSvc.db)
	if err != nil || len(observations) != 0 {
		t.Fatalf("revocation race left price evidence: observations=%+v err=%v", observations, err)
	}
}

func TestLegacyV12KiwoomPriceRemainsReplayableButUnowned(t *testing.T) {
	svc, _ := testService(t, []time.Time{mustTime("2026-08-24T01:30:04Z")}, nil)
	downgradePaperMarketSignalsForTest(t, svc.db)
	downgradePaperEvaluationForTest(t, svc.db)
	if _, err := svc.db.Exec(`DROP TRIGGER instrument_listing_events_no_update;
		DROP TRIGGER instrument_listing_events_no_delete;
		DROP TRIGGER instrument_listing_events_state_guard;
		DROP TABLE instrument_listing_events;
		DELETE FROM schema_migrations WHERE version=13`); err != nil {
		t.Fatal(err)
	}
	input := SecurityPriceObservationInput{
		Source: "kiwoom_production", InstrumentID: "krx_005930", Symbol: "005930", Venue: "XKRX", Currency: "KRW",
		Price: "1250", PriceAdjustment: marketDataAdjustmentUnspecified,
		ObservedAt: "2026-08-24T01:30:00Z", FetchedAt: "2026-08-24T01:30:01Z",
	}
	input.SourceObservationID, _ = kiwoomLatestTradeObservationID(
		input.Source, input.InstrumentID, input.Symbol, input.Venue, input.Currency, input.PriceAdjustment, input.ObservedAt,
	)
	legacy := SecurityPriceObservation{
		ObservationID: "security_price_observation_legacy", Source: input.Source, SourceObservationID: input.SourceObservationID,
		InstrumentID: input.InstrumentID, Symbol: input.Symbol, Venue: input.Venue, Currency: input.Currency, Price: input.Price,
		PriceAdjustment: input.PriceAdjustment, ObservedAt: input.ObservedAt, FetchedAt: input.FetchedAt,
		RecordedAt: "2026-08-24T01:30:02Z",
	}
	legacy.recordSHA256, _ = securityPriceObservationSHA(legacy)
	if _, err := svc.db.Exec(`INSERT INTO security_price_observations(
		observation_id,source,source_observation_id,instrument_id,symbol,venue,currency,price,price_adjustment,observed_at,fetched_at,record_sha256,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, legacy.ObservationID, legacy.Source, legacy.SourceObservationID,
		legacy.InstrumentID, legacy.Symbol, legacy.Venue, legacy.Currency, legacy.Price, legacy.PriceAdjustment,
		legacy.ObservedAt, legacy.FetchedAt, legacy.recordSHA256, legacy.RecordedAt); err != nil {
		t.Fatal(err)
	}
	if err := migrate(svc.db); err != nil {
		t.Fatal(err)
	}
	observations, _, err := replaySecurityPriceObservations(context.Background(), svc.db)
	if err != nil || len(observations) != 1 || observations[0].InstrumentID != "krx_005930" {
		t.Fatalf("legacy v12 price was not preserved: observations=%+v err=%v", observations, err)
	}
	if listing, err := resolveInstrumentListing(context.Background(), svc.db, "XKRX", "005930", "KRW"); err == nil || listing != nil {
		t.Fatalf("legacy price inferred listing ownership: listing=%+v err=%v", listing, err)
	}
	replayed, err := svc.recordSecurityPriceObservation(context.Background(), input)
	if err != nil || replayed.ObservationID != legacy.ObservationID {
		t.Fatalf("exact legacy replay drifted: observation=%+v err=%v", replayed, err)
	}
	newInput := input
	newInput.Price = "1251"
	newInput.ObservedAt = "2026-08-24T01:30:02Z"
	newInput.FetchedAt = "2026-08-24T01:30:03Z"
	newInput.SourceObservationID, _ = kiwoomLatestTradeObservationID(
		newInput.Source, newInput.InstrumentID, newInput.Symbol, newInput.Venue, newInput.Currency, newInput.PriceAdjustment, newInput.ObservedAt,
	)
	if observation, err := svc.recordSecurityPriceObservation(context.Background(), newInput); err == nil || observation != nil {
		t.Fatalf("legacy identity gained write authority: observation=%+v err=%v", observation, err)
	}
}

func TestSecurityPriceObservationAcceptsCanonicalInternalInstrumentID(t *testing.T) {
	svc, _ := testService(t, []time.Time{mustTime("2026-01-10T15:02:00Z")}, nil)
	input := securityPriceInput()
	input.InstrumentID = "instrument_aapl"

	stored, err := svc.recordSecurityPriceObservation(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if stored.InstrumentID != "instrument_aapl" || stored.Symbol != "AAPL" || stored.Venue != "XNAS" {
		t.Fatalf("stored identity drifted: %+v", stored)
	}
	replayed, _, err := replaySecurityPriceObservations(context.Background(), svc.db)
	if err != nil || len(replayed) != 1 || replayed[0].InstrumentID != "instrument_aapl" || replayed[0].Symbol != "AAPL" || replayed[0].Venue != "XNAS" {
		t.Fatalf("replayed identity drifted: observations=%+v err=%v", replayed, err)
	}
	latest, err := latestSecurityPriceObservation(context.Background(), svc.db, input.Source, input.InstrumentID, input.Symbol, input.Venue, input.Currency, input.PriceAdjustment, "2026-01-10T15:03:00Z")
	if err != nil || latest.SourceObservationID != input.SourceObservationID || latest.InstrumentID != "instrument_aapl" || latest.Symbol != "AAPL" || latest.Venue != "XNAS" {
		t.Fatalf("exact latest lookup drifted: observation=%+v err=%v", latest, err)
	}
}

func TestSecurityPriceObservationRecordReplayConflictAndSnapshotBoundary(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	ctx := context.Background()
	first := securityPriceInput()
	stored, err := svc.recordSecurityPriceObservation(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if !canonicalUTCString(stored.ObservedAt) || !canonicalUTCString(stored.FetchedAt) || !canonicalUTCString(stored.RecordedAt) {
		t.Fatalf("stored timestamps are not canonical UTC: %+v", stored)
	}
	replayed, err := svc.recordSecurityPriceObservation(ctx, first)
	if err != nil || !reflect.DeepEqual(stored, replayed) {
		t.Fatalf("exact replay drifted: stored=%+v replayed=%+v err=%v", stored, replayed, err)
	}
	changed := first
	changed.Price = "251"
	if _, err := svc.recordSecurityPriceObservation(ctx, changed); err == nil {
		t.Fatal("source observation identity was rebound to a different price")
	}
	sameSlot := first
	sameSlot.SourceObservationID = "aapl_close_other"
	if _, err := svc.recordSecurityPriceObservation(ctx, sameSlot); err == nil {
		t.Fatal("source/instrument/venue/currency/time/adjustment slot accepted two identities")
	}
	second := first
	second.SourceObservationID = "aapl_close_20260111"
	second.Price = "252"
	second.ObservedAt = "2026-01-10T15:00:02Z"
	second.FetchedAt = "2026-01-10T15:00:03Z"
	if _, err := svc.recordSecurityPriceObservation(ctx, second); err != nil {
		t.Fatal(err)
	}
	observations, proof, err := replaySecurityPriceObservations(ctx, svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 2 || observations[0].SourceObservationID != first.SourceObservationID ||
		observations[1].SourceObservationID != second.SourceObservationID || observations[0].Price != first.Price ||
		observations[1].Price != second.Price || proof.Observations != 2 || len(proof.SHA256) != 64 {
		t.Fatalf("security price series/proof drifted: observations=%+v proof=%+v", observations, proof)
	}
	snapshot, err := snapshotFrom(ctx, svc.db)
	if err != nil || snapshot.ValuationStatus != "unavailable" {
		t.Fatalf("price storage changed valuation authority: snapshot=%+v err=%v", snapshot, err)
	}
}

func TestSecurityPriceObservationRejectsInvalidBoundaryAndDirectMutation(t *testing.T) {
	valid := securityPriceInput()
	tests := map[string]func(*SecurityPriceObservationInput){
		"empty source id":         func(v *SecurityPriceObservationInput) { v.SourceObservationID = "" },
		"unknown source":          func(v *SecurityPriceObservationInput) { v.Source = "kiwoom" },
		"empty instrument":        func(v *SecurityPriceObservationInput) { v.InstrumentID = "" },
		"lower symbol":            func(v *SecurityPriceObservationInput) { v.Symbol = "aapl" },
		"empty venue":             func(v *SecurityPriceObservationInput) { v.Venue = "" },
		"lower currency":          func(v *SecurityPriceObservationInput) { v.Currency = "usd" },
		"zero price":              func(v *SecurityPriceObservationInput) { v.Price = "0" },
		"negative price":          func(v *SecurityPriceObservationInput) { v.Price = "-1" },
		"exponent price":          func(v *SecurityPriceObservationInput) { v.Price = "1e3" },
		"trailing zero price":     func(v *SecurityPriceObservationInput) { v.Price = "250.250" },
		"unknown adjustment":      func(v *SecurityPriceObservationInput) { v.PriceAdjustment = "split_adjusted" },
		"provider adjustment":     func(v *SecurityPriceObservationInput) { v.PriceAdjustment = marketDataAdjustmentProviderAdjusted },
		"offset observed":         func(v *SecurityPriceObservationInput) { v.ObservedAt = "2026-01-11T00:00:00+09:00" },
		"offset fetched":          func(v *SecurityPriceObservationInput) { v.FetchedAt = "2026-01-11T00:00:01+09:00" },
		"fetched before observed": func(v *SecurityPriceObservationInput) { v.FetchedAt = "2026-01-10T14:59:59Z" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			observation := valid
			mutate(&observation)
			if _, err := svc.recordSecurityPriceObservation(context.Background(), observation); err == nil {
				t.Fatalf("invalid security price observation was accepted: %+v", observation)
			}
		})
	}
	recordedBeforeFetched, _ := testService(t, []time.Time{mustTime("2026-01-10T15:00:00Z")}, nil)
	if _, err := recordedBeforeFetched.recordSecurityPriceObservation(context.Background(), valid); err == nil {
		t.Fatal("security price observation was recorded before its fetched_at timestamp")
	}

	svc, _ := testService(t, nil, nil)
	var strict int
	if err := svc.db.QueryRow(`SELECT strict FROM pragma_table_list WHERE schema='main' AND type='table' AND name='security_price_observations'`).Scan(&strict); err != nil || strict != 1 {
		t.Fatalf("security price table is not STRICT: strict=%d err=%v", strict, err)
	}
	if _, err := svc.db.Exec(`INSERT INTO security_price_observations(observation_id,source,source_observation_id,instrument_id,symbol,venue,currency,price,price_adjustment,observed_at,fetched_at,record_sha256,recorded_at) VALUES('bad-price','local_fixture','bad-price','US0378331005','AAPL','XNAS','USD','250.250','unspecified','2026-01-10T15:00:00Z','2026-01-10T15:00:01Z','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','2026-01-10T15:00:02Z')`); err == nil {
		t.Fatal("STRICT storage accepted a non-canonical direct price")
	}
	if _, err := svc.recordSecurityPriceObservation(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE security_price_observations SET price=price`,
		`DELETE FROM security_price_observations`,
	} {
		if _, err := svc.db.Exec(statement); err == nil {
			t.Fatalf("insert-only security price storage accepted mutation: %s", statement)
		}
	}
}

func TestSecurityPriceObservationLatestIsExactAndExcludesFutureKnowledge(t *testing.T) {
	svc, _ := testService(t, []time.Time{
		mustTime("2026-01-10T15:02:00Z"), mustTime("2026-01-10T15:03:00Z"), mustTime("2026-01-10T15:05:00Z"), mustTime("2026-01-10T15:07:00Z"),
	}, nil)
	current := securityPriceInput()
	futureRecorded := current
	futureRecorded.SourceObservationID = "aapl_future_recorded"
	futureRecorded.Price = "250.5"
	futureRecorded.ObservedAt = "2026-01-10T14:59:00Z"
	futureRecorded.FetchedAt = "2026-01-10T14:59:01Z"
	futureFetched := current
	futureFetched.SourceObservationID = "aapl_future_fetch"
	futureFetched.Price = "251"
	futureFetched.ObservedAt = "2026-01-10T15:01:00Z"
	futureFetched.FetchedAt = "2026-01-10T15:05:00Z"
	futureObserved := current
	futureObserved.SourceObservationID = "aapl_future_observed"
	futureObserved.Price = "252"
	futureObserved.ObservedAt = "2026-01-10T15:05:00Z"
	futureObserved.FetchedAt = "2026-01-10T15:05:01Z"
	for _, input := range []SecurityPriceObservationInput{current, futureRecorded, futureFetched, futureObserved} {
		if _, err := svc.recordSecurityPriceObservation(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	lookup := func(source, instrumentID, symbol, venue, currency, adjustment string) (*SecurityPriceObservation, error) {
		return latestSecurityPriceObservation(context.Background(), svc.db, source, instrumentID, symbol, venue, currency, adjustment, "2026-01-10T15:02:00Z")
	}
	got, err := lookup(current.Source, current.InstrumentID, current.Symbol, current.Venue, current.Currency, current.PriceAdjustment)
	if err != nil || got.SourceObservationID != current.SourceObservationID || got.Price != current.Price {
		t.Fatalf("exact as-of lookup drifted: observation=%+v err=%v", got, err)
	}
	for _, mismatch := range [][6]string{
		{"kiwoom", current.InstrumentID, current.Symbol, current.Venue, current.Currency, current.PriceAdjustment},
		{current.Source, "US5949181045", current.Symbol, current.Venue, current.Currency, current.PriceAdjustment},
		{current.Source, current.InstrumentID, "MSFT", current.Venue, current.Currency, current.PriceAdjustment},
		{current.Source, current.InstrumentID, current.Symbol, "XNYS", current.Currency, current.PriceAdjustment},
		{current.Source, current.InstrumentID, current.Symbol, current.Venue, "KRW", current.PriceAdjustment},
		{current.Source, current.InstrumentID, current.Symbol, current.Venue, current.Currency, marketDataAdjustmentProviderAdjusted},
	} {
		if observation, err := lookup(mismatch[0], mismatch[1], mismatch[2], mismatch[3], mismatch[4], mismatch[5]); err == nil || observation != nil {
			t.Fatalf("exact lookup accepted mismatched identity: observation=%+v err=%v", observation, err)
		}
	}
}

func TestSecurityPriceObservationReplayFailsClosedOnCorruption(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	if _, err := svc.recordSecurityPriceObservation(context.Background(), securityPriceInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER security_price_observations_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`UPDATE security_price_observations SET price='251'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := replaySecurityPriceObservations(context.Background(), svc.db); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("corrupt security price observation was certified: %v", err)
	}
}

func TestSecurityPriceObservationRestoreRejectsWeakAdjustmentConstraint(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "security-prices-weak-adjustment.db")
	if _, err := createBackup(svc.db, backup, golden, backup+".manifest.json", svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	candidate, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`PRAGMA writable_schema=ON`); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`UPDATE sqlite_master SET sql=replace(sql, "price_adjustment = 'unspecified'", "price_adjustment <> ''") WHERE type='table' AND name='security_price_observations'`); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`PRAGMA writable_schema=OFF`); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err == nil {
		t.Fatal("restore accepted security prices without the pinned price_adjustment constraint")
	}
}

func TestSecurityPriceObservationBackupProofAndLegacyCopyMigrations(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	if _, err := svc.recordSecurityPriceObservation(context.Background(), securityPriceInput()); err != nil {
		t.Fatal(err)
	}
	golden := writeCurrentSnapshot(t, svc.db)
	backup := filepath.Join(t.TempDir(), "security-prices.db")
	manifestPath := backup + ".manifest.json"
	manifest, err := createBackup(svc.db, backup, golden, manifestPath, svc.now, svc.id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != "omni-folio-backup.v11" || manifest.SchemaVersion != "omni-folio.sqlite.v16" ||
		manifest.SecurityPriceObservationCount != 1 || len(manifest.SecurityPriceObservationStateSHA256) != 64 ||
		manifest.VerificationReceipt.SecurityPriceObservationCheck != "ok" ||
		manifest.VerificationReceipt.CandidateSecurityPriceObservationStateSHA256 != manifest.SecurityPriceObservationStateSHA256 {
		t.Fatalf("backup omitted security price proof: %+v", manifest)
	}
	if err := verifyManifest(backup, golden, manifestPath); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err != nil {
		t.Fatal(err)
	}
	candidate, err := openExistingDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`DROP TRIGGER security_price_observations_no_delete`); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, golden); err == nil {
		t.Fatal("restore accepted a security price table without its insert-only trigger")
	}
	indexBackup := filepath.Join(t.TempDir(), "security-prices-missing-index.db")
	if _, err := createBackup(svc.db, indexBackup, golden, indexBackup+".manifest.json", svc.now, svc.id); err != nil {
		t.Fatal(err)
	}
	candidate, err = openExistingDB(indexBackup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.Exec(`DROP INDEX security_price_observations_latest_idx`); err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(indexBackup, golden); err == nil {
		t.Fatal("restore accepted security prices without the exact latest index")
	}

	downgradePaperMarketSignalsForTest(t, svc.db)
	downgradePaperEvaluationForTest(t, svc.db)
	if _, err := svc.db.Exec(`DROP TRIGGER instrument_listing_events_no_update; DROP TRIGGER instrument_listing_events_no_delete; DROP TRIGGER instrument_listing_events_state_guard; DROP TABLE instrument_listing_events; DELETE FROM schema_migrations WHERE version=13; DROP TRIGGER security_price_observations_no_update; DROP TRIGGER security_price_observations_no_delete; DROP INDEX security_price_observations_latest_idx; ALTER TABLE security_price_observations RENAME TO security_price_observations_v12`); err != nil {
		t.Fatal(err)
	}
	v11Migration, err := migrationFiles.ReadFile("migrations/011_security_price_observations.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(string(v11Migration)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO security_price_observations SELECT * FROM security_price_observations_v12; DROP TABLE security_price_observations_v12; DELETE FROM schema_migrations WHERE version=12`); err != nil {
		t.Fatal(err)
	}
	legacyV11Backup := filepath.Join(t.TempDir(), "legacy-v11.db")
	if _, err := svc.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(legacyV11Backup, "'", "''") + `'`); err != nil {
		t.Fatal(err)
	}
	legacyV11Manifest := readJSONMap(t, manifestPath)
	legacyV11Manifest["format_version"] = "omni-folio-backup.v7"
	legacyV11Manifest["schema_version"] = "omni-folio.sqlite.v11"
	delete(legacyV11Manifest, "paper_evaluation_event_count")
	delete(legacyV11Manifest, "paper_accounting_state_sha256")
	delete(legacyV11Manifest, "paper_accounting_session_count")
	delete(legacyV11Manifest, "paper_market_bar_observation_count")
	delete(legacyV11Manifest, "paper_signal_event_count")
	delete(legacyV11Manifest, "paper_execution_authorization_count")
	delete(legacyV11Manifest, "paper_capitalized_fill_count")
	delete(legacyV11Manifest, "instrument_listing_state_sha256")
	delete(legacyV11Manifest, "instrument_listing_event_count")
	delete(legacyV11Manifest, "active_instrument_listing_count")
	legacyV11Receipt := legacyV11Manifest["verification_receipt"].(map[string]any)
	delete(legacyV11Receipt, "candidate_paper_accounting_state_sha256")
	delete(legacyV11Receipt, "instrument_listing_check")
	delete(legacyV11Receipt, "candidate_instrument_listing_state_sha256")
	v11SHA, v11Size, err := hashFile(legacyV11Backup)
	if err != nil {
		t.Fatal(err)
	}
	legacyV11Manifest["db_sha256"] = v11SHA
	legacyV11Manifest["size_bytes"] = v11Size
	legacyV11Manifest["verification_receipt"].(map[string]any)["candidate_db_sha256"] = v11SHA
	legacyV11ManifestPath := filepath.Join(t.TempDir(), "legacy-v11.manifest.json")
	writeJSONFile(t, legacyV11ManifestPath, legacyV11Manifest)
	beforeV11SHA, beforeV11Size, err := hashFile(legacyV11Backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(legacyV11Backup, golden, legacyV11ManifestPath); err != nil {
		t.Fatal(err)
	}
	afterV11SHA, afterV11Size, err := hashFile(legacyV11Backup)
	if err != nil || beforeV11SHA != afterV11SHA || beforeV11Size != afterV11Size {
		t.Fatalf("legacy v11 source was changed during copy migration: before=(%s,%d) after=(%s,%d) err=%v", beforeV11SHA, beforeV11Size, afterV11SHA, afterV11Size, err)
	}

	if _, err := svc.db.Exec(`DROP TRIGGER security_price_observations_no_update; DROP TRIGGER security_price_observations_no_delete; DROP TABLE security_price_observations; DELETE FROM schema_migrations WHERE version IN (11,12)`); err != nil {
		t.Fatal(err)
	}
	legacyBackup := filepath.Join(t.TempDir(), "legacy-v10.db")
	if _, err := svc.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(legacyBackup, "'", "''") + `'`); err != nil {
		t.Fatal(err)
	}
	legacyManifest := readJSONMap(t, manifestPath)
	legacyManifest["format_version"] = "omni-folio-backup.v6"
	legacyManifest["schema_version"] = "omni-folio.sqlite.v10"
	delete(legacyManifest, "paper_evaluation_event_count")
	delete(legacyManifest, "paper_accounting_state_sha256")
	delete(legacyManifest, "paper_accounting_session_count")
	delete(legacyManifest, "paper_market_bar_observation_count")
	delete(legacyManifest, "paper_signal_event_count")
	delete(legacyManifest, "paper_execution_authorization_count")
	delete(legacyManifest, "paper_capitalized_fill_count")
	delete(legacyManifest, "security_price_observation_state_sha256")
	delete(legacyManifest, "security_price_observation_count")
	delete(legacyManifest, "instrument_listing_state_sha256")
	delete(legacyManifest, "instrument_listing_event_count")
	delete(legacyManifest, "active_instrument_listing_count")
	receipt := legacyManifest["verification_receipt"].(map[string]any)
	delete(receipt, "candidate_paper_accounting_state_sha256")
	delete(receipt, "security_price_observation_check")
	delete(receipt, "candidate_security_price_observation_state_sha256")
	delete(receipt, "instrument_listing_check")
	delete(receipt, "candidate_instrument_listing_state_sha256")
	sha, size, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	legacyManifest["db_sha256"] = sha
	legacyManifest["size_bytes"] = size
	legacyManifest["verification_receipt"].(map[string]any)["candidate_db_sha256"] = sha
	legacyManifestPath := filepath.Join(t.TempDir(), "legacy-v10.manifest.json")
	writeJSONFile(t, legacyManifestPath, legacyManifest)
	beforeSHA, beforeSize, err := hashFile(legacyBackup)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(legacyBackup, golden, legacyManifestPath); err != nil {
		t.Fatal(err)
	}
	mixed := readJSONMap(t, legacyManifestPath)
	mixed["security_price_observation_state_sha256"] = strings.Repeat("0", 64)
	mixed["security_price_observation_count"] = float64(0)
	mixedReceipt := mixed["verification_receipt"].(map[string]any)
	mixedReceipt["security_price_observation_check"] = "ok"
	mixedReceipt["candidate_security_price_observation_state_sha256"] = strings.Repeat("0", 64)
	mixedPath := filepath.Join(t.TempDir(), "mixed-v6-v7.manifest.json")
	writeJSONFile(t, mixedPath, mixed)
	if err := verifyManifest(legacyBackup, golden, mixedPath); err == nil {
		t.Fatal("legacy v6 manifest with v7-only security price fields was accepted")
	}
	afterSHA, afterSize, err := hashFile(legacyBackup)
	if err != nil || beforeSHA != afterSHA || beforeSize != afterSize {
		t.Fatalf("legacy v10 source was changed during copy migration: before=(%s,%d) after=(%s,%d) err=%v", beforeSHA, beforeSize, afterSHA, afterSize, err)
	}
	var version int
	if err := svc.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 10 {
		t.Fatalf("legacy v10 source was migrated in place: version=%d err=%v", version, err)
	}
}
