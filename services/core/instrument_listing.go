package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	instrumentListingDeclare   = "DECLARE"
	instrumentListingRevoke    = "REVOKE"
	instrumentListingAuthority = "owner_declared"
	noInstrumentListingEvent   = "no_event"
)

var errInstrumentListingNotFound = errors.New("instrument listing not found")

type InstrumentListingInput struct {
	InstrumentID string
	Venue        string
	Symbol       string
	Currency     string
}

type InstrumentListingEvent struct {
	EventID                 string
	EventType               string
	Authority               string
	InstrumentID            string
	Venue                   string
	Symbol                  string
	Currency                string
	ExpectedPreviousEventID string
	RecordedAt              string
	recordSHA256            string
}

type instrumentListingRecoveryProof struct {
	SHA256 string
	Events int
	Active int
}

func validateInstrumentListingInput(input InstrumentListingInput) error {
	if !safeOrderID(input.InstrumentID) || !safeOrderID(input.Venue) || !safeOrderID(input.Symbol) ||
		input.Venue != strings.ToUpper(input.Venue) || input.Symbol != strings.ToUpper(input.Symbol) ||
		!currencyCodePattern.MatchString(input.Currency) {
		return errors.New("instrument listing identity is invalid")
	}
	return nil
}

func validateInstrumentListingEvent(event InstrumentListingEvent) error {
	if err := validateInstrumentListingInput(InstrumentListingInput{
		InstrumentID: event.InstrumentID, Venue: event.Venue, Symbol: event.Symbol, Currency: event.Currency,
	}); err != nil {
		return err
	}
	if !safeOrderID(event.EventID) || !safeOrderID(event.ExpectedPreviousEventID) ||
		(event.EventType != instrumentListingDeclare && event.EventType != instrumentListingRevoke) ||
		event.Authority != instrumentListingAuthority || !canonicalUTCString(event.RecordedAt) {
		return errors.New("instrument listing event metadata is invalid")
	}
	return nil
}

func instrumentListingEventSHA(event InstrumentListingEvent) (string, error) {
	event.recordSHA256 = ""
	_, hash, err := orderJSONHash(event)
	return hash, err
}

func instrumentListingKey(venue, symbol, currency string) string {
	return venue + "\x00" + symbol + "\x00" + currency
}

// ponytail: replay the small owner-managed registry on every authority check; add a projection only after measured volume makes it hot.
func replayInstrumentListingRegistry(ctx context.Context, q orderQuerier) (map[string]InstrumentListingEvent, instrumentListingRecoveryProof, error) {
	rows, err := q.QueryContext(ctx, `SELECT sequence,event_id,event_type,authority,instrument_id,venue,symbol,currency,expected_previous_event_id,record_sha256,recorded_at
		FROM instrument_listing_events ORDER BY sequence`)
	if err != nil {
		return nil, instrumentListingRecoveryProof{}, err
	}
	defer rows.Close()
	active := map[string]InstrumentListingEvent{}
	latest := map[string]InstrumentListingEvent{}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	events := 0
	for rows.Next() {
		var sequence int64
		var event InstrumentListingEvent
		if err := rows.Scan(&sequence, &event.EventID, &event.EventType, &event.Authority, &event.InstrumentID,
			&event.Venue, &event.Symbol, &event.Currency, &event.ExpectedPreviousEventID, &event.recordSHA256,
			&event.RecordedAt); err != nil {
			return nil, instrumentListingRecoveryProof{}, err
		}
		if err := validateInstrumentListingEvent(event); err != nil {
			return nil, instrumentListingRecoveryProof{}, fmt.Errorf("instrument listing event %q metadata mismatch: %w", event.EventID, err)
		}
		wantSHA, err := instrumentListingEventSHA(event)
		if err != nil || wantSHA != event.recordSHA256 {
			return nil, instrumentListingRecoveryProof{}, fmt.Errorf("instrument listing event %q metadata or hash mismatch", event.EventID)
		}
		key := instrumentListingKey(event.Venue, event.Symbol, event.Currency)
		previous, exists := latest[key]
		if !exists {
			if event.ExpectedPreviousEventID != noInstrumentListingEvent || event.EventType != instrumentListingDeclare {
				return nil, instrumentListingRecoveryProof{}, fmt.Errorf("instrument listing event %q has an invalid initial transition", event.EventID)
			}
		} else if event.ExpectedPreviousEventID != previous.EventID ||
			(previous.EventType == instrumentListingDeclare && event.EventType != instrumentListingRevoke) ||
			(previous.EventType == instrumentListingRevoke && event.EventType != instrumentListingDeclare) ||
			(event.EventType == instrumentListingRevoke && event.InstrumentID != previous.InstrumentID) {
			return nil, instrumentListingRecoveryProof{}, fmt.Errorf("instrument listing event %q has an invalid transition", event.EventID)
		}
		if err := encoder.Encode([]any{sequence, event.EventID, event.EventType, event.Authority, event.InstrumentID,
			event.Venue, event.Symbol, event.Currency, event.ExpectedPreviousEventID, event.recordSHA256, event.RecordedAt}); err != nil {
			return nil, instrumentListingRecoveryProof{}, err
		}
		latest[key] = event
		if event.EventType == instrumentListingDeclare {
			active[key] = event
		} else {
			delete(active, key)
		}
		events++
	}
	if err := rows.Err(); err != nil {
		return nil, instrumentListingRecoveryProof{}, err
	}
	return active, instrumentListingRecoveryProof{
		SHA256: hex.EncodeToString(hash.Sum(nil)), Events: events, Active: len(active),
	}, nil
}

func proveInstrumentListingRecovery(ctx context.Context, q orderQuerier) (instrumentListingRecoveryProof, error) {
	_, proof, err := replayInstrumentListingRegistry(ctx, q)
	return proof, err
}

func resolveInstrumentListing(ctx context.Context, q orderQuerier, venue, symbol, currency string) (*InstrumentListingEvent, error) {
	if !safeOrderID(venue) || !safeOrderID(symbol) || venue != strings.ToUpper(venue) ||
		symbol != strings.ToUpper(symbol) || !currencyCodePattern.MatchString(currency) {
		return nil, errors.New("instrument listing query is invalid")
	}
	active, _, err := replayInstrumentListingRegistry(ctx, q)
	if err != nil {
		return nil, err
	}
	event, ok := active[instrumentListingKey(venue, symbol, currency)]
	if !ok {
		return nil, errInstrumentListingNotFound
	}
	return &event, nil
}

func (s *Service) declareInstrumentListing(ctx context.Context, input InstrumentListingInput) (*InstrumentListingEvent, error) {
	return s.recordInstrumentListingEvent(ctx, instrumentListingDeclare, input)
}

func (s *Service) revokeInstrumentListing(ctx context.Context, input InstrumentListingInput) (*InstrumentListingEvent, error) {
	return s.recordInstrumentListingEvent(ctx, instrumentListingRevoke, input)
}

func (s *Service) recordInstrumentListingEvent(ctx context.Context, eventType string, input InstrumentListingInput) (*InstrumentListingEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("instrument listing service is unavailable")
	}
	if err := validateInstrumentListingInput(input); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	active, _, err := replayInstrumentListingRegistry(ctx, tx)
	if err != nil {
		return nil, err
	}
	key := instrumentListingKey(input.Venue, input.Symbol, input.Currency)
	current, isActive := active[key]
	if eventType == instrumentListingDeclare && isActive {
		if current.InstrumentID == input.InstrumentID {
			return &current, nil
		}
		return nil, errors.New("instrument listing is already declared")
	}
	if eventType == instrumentListingRevoke && (!isActive || current.InstrumentID != input.InstrumentID) {
		return nil, errInstrumentListingNotFound
	}
	var previousEventID string
	err = tx.QueryRowContext(ctx, `SELECT event_id FROM instrument_listing_events
		WHERE venue=? AND symbol=? AND currency=? ORDER BY sequence DESC LIMIT 1`,
		input.Venue, input.Symbol, input.Currency).Scan(&previousEventID)
	if errors.Is(err, sql.ErrNoRows) {
		previousEventID = noInstrumentListingEvent
	} else if err != nil {
		return nil, err
	}
	event := &InstrumentListingEvent{
		EventID: s.id("instrument_listing"), EventType: eventType, Authority: instrumentListingAuthority,
		InstrumentID: input.InstrumentID, Venue: input.Venue, Symbol: input.Symbol, Currency: input.Currency,
		ExpectedPreviousEventID: previousEventID, RecordedAt: s.now().UTC().Format(time.RFC3339Nano),
	}
	if err := validateInstrumentListingEvent(*event); err != nil {
		return nil, err
	}
	event.recordSHA256, err = instrumentListingEventSHA(*event)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO instrument_listing_events(
		event_id,event_type,authority,instrument_id,venue,symbol,currency,expected_previous_event_id,record_sha256,recorded_at
	) VALUES(?,?,?,?,?,?,?,?,?,?)`, event.EventID, event.EventType, event.Authority, event.InstrumentID,
		event.Venue, event.Symbol, event.Currency, event.ExpectedPreviousEventID, event.recordSHA256, event.RecordedAt); err != nil {
		return nil, fmt.Errorf("store instrument listing event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return event, nil
}
