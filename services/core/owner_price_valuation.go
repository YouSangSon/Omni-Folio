package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type valuationPriceInputs struct {
	tx           *sql.Tx
	knownAt      time.Time
	observations []SecurityPriceObservation
	times        map[string][3]time.Time
	lastSplits   map[string]time.Time
}

func ownerPriceKey(instrument, currency string) string { return instrument + "\x00" + currency }

func admittedOwnerPriceIdentities(ctx context.Context, q queryer) (map[string]struct{}, error) {
	if err := proveLedgerReplayState(ctx, q, false); err != nil {
		return nil, err
	}
	if _, _, err := (&Service{}).proveLedgerEvents(ctx, q); err != nil {
		return nil, err
	}
	if _, err := snapshotFrom(ctx, q); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT instrument_id,symbol,currency FROM events WHERE instrument_id IS NOT NULL AND instrument_id<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	admitted := map[string]struct{}{}
	for rows.Next() {
		var id, symbol, currency string
		if err := rows.Scan(&id, &symbol, &currency); err != nil {
			return nil, errors.New("owner price ledger identity is invalid")
		}
		if instrumentIDForSymbol(symbol) != id {
			return nil, errors.New("owner price ledger identity is not canonical")
		}
		admitted[ownerPriceKey(id, currency)] = struct{}{}
	}
	return admitted, rows.Err()
}

func valuationPriceObservations(ctx context.Context, q queryer, knownAt time.Time) ([]SecurityPriceObservation, error) {
	observations, _, err := replaySecurityPriceObservations(ctx, q)
	if err != nil {
		return nil, err
	}
	if _, err := proveOwnerPriceSeriesRecovery(ctx, q); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT o.sequence,o.series_json,o.recorded_at FROM owner_price_series_versions o
        JOIN (SELECT instrument_id,currency,MAX(sequence) AS sequence FROM owner_price_series_versions
            WHERE rtrim(recorded_at,'Z')<=rtrim(?,'Z') GROUP BY instrument_id,currency) latest ON latest.sequence=o.sequence
        ORDER BY o.sequence`, knownAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int64
		var payload, recorded string
		if err := rows.Scan(&sequence, &payload, &recorded); err != nil {
			return nil, err
		}
		input, _, _, err := decodeOwnerPriceSeries(ctx, []byte(payload))
		if err != nil {
			return nil, err
		}
		for i, p := range input.Points {
			id := fmt.Sprintf("owner_price_%d_%d", sequence, i)
			observations = append(observations, SecurityPriceObservation{ObservationID: id, Source: ownerPriceSource, SourceObservationID: id, InstrumentID: input.InstrumentID, Symbol: input.Symbol, Venue: input.Venue, Currency: input.Currency, Price: p.Price, PriceAdjustment: input.PriceAdjustment, ObservedAt: p.ObservedAt, FetchedAt: input.FetchedAt, RecordedAt: recorded, ownerVersion: input.SourceVersion, ownerSample: input.Sample})
		}
	}
	return observations, rows.Err()
}

func readValuationPriceInputs(ctx context.Context, tx *sql.Tx, knownAt time.Time) (valuationPriceInputs, error) {
	observations, err := valuationPriceObservations(ctx, tx, knownAt)
	if err != nil {
		return valuationPriceInputs{}, err
	}
	times, err := currentPriceObservationTimes(observations)
	if err != nil {
		return valuationPriceInputs{}, err
	}
	inputs := valuationPriceInputs{tx: tx, knownAt: knownAt, observations: observations, times: times, lastSplits: map[string]time.Time{}}
	owner := false
	for _, p := range observations {
		owner = owner || p.Source == ownerPriceSource
	}
	if !owner {
		return inputs, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT instrument_id,currency,occurred_at FROM events WHERE type='SPLIT' ORDER BY rtrim(occurred_at,'Z'),sequence`)
	if err != nil {
		return valuationPriceInputs{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, currency, at string
		if err := rows.Scan(&id, &currency, &at); err != nil {
			return valuationPriceInputs{}, err
		}
		parsed, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return valuationPriceInputs{}, err
		}
		inputs.lastSplits[ownerPriceKey(id, currency)] = parsed
	}
	return inputs, rows.Err()
}

func priceTimeKey(p SecurityPriceObservation) string {
	if p.Source == ownerPriceSource {
		return "\x00" + p.ObservationID
	}
	return p.ObservationID
}

func priceIsSample(p *SecurityPriceObservation) bool {
	return p != nil && (p.Source == "local_fixture" || p.ownerSample)
}

func ownerPriceIssue() APIError {
	return APIError{Code: "owner_declared_price", Message: "selected security prices were declared by the owner, not verified by a market provider"}
}

func usesOwnerPrice(issues []APIError) bool {
	for _, i := range issues {
		if i.Code == "owner_declared_price" {
			return true
		}
	}
	return false
}

func (p valuationPriceInputs) afterLastSplit(mark *SecurityPriceObservation) bool {
	if mark.Source != ownerPriceSource {
		return true
	}
	last := p.lastSplits[ownerPriceKey(mark.InstrumentID, mark.Currency)]
	return last.IsZero() || p.times[priceTimeKey(*mark)][0].After(last)
}
