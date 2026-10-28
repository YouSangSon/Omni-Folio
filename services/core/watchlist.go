package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const watchlistPolicy = "watchlist_stored_prices_24h.v1"
const watchlistBodyLimit = 16 * 1024
const watchlistResponseLimit = 128 * 1024
const watchlistRevisionLimit int64 = 9007199254740991

var watchlistUSSymbol = regexp.MustCompile(`^[A-Z0-9][A-Z0-9.-]{0,31}$`)
var watchlistKRSymbol = regexp.MustCompile(`^[0-9]{6}$`)
var errWatchlistInput = errors.New("invalid watchlist input")
var errWatchlistChanged = errors.New("watchlist revision changed")

type WatchlistEntry struct {
	Venue    string `json:"venue"`
	Symbol   string `json:"symbol"`
	Currency string `json:"currency"`
}

type WatchlistState struct {
	Revision  int64            `json:"revision"`
	UpdatedAt *string          `json:"updated_at"`
	Entries   []WatchlistEntry `json:"entries"`
}

type WatchlistChange struct {
	ExpectedRevision int64            `json:"expected_revision"`
	Entries          []WatchlistEntry `json:"entries"`
}

type WatchlistPrice struct {
	Source          string  `json:"source"`
	SourceVersion   *string `json:"source_version"`
	Price           string  `json:"price"`
	PriceAdjustment string  `json:"price_adjustment"`
	ObservedAt      string  `json:"observed_at"`
	FetchedAt       string  `json:"fetched_at"`
	RecordedAt      string  `json:"recorded_at"`
	Sample          bool    `json:"sample"`
}

type WatchlistQuote struct {
	WatchlistEntry
	Status string          `json:"status"`
	Price  *WatchlistPrice `json:"price"`
}

type WatchlistReport struct {
	PolicyVersion            string           `json:"policy_version"`
	MaxObservationAgeSeconds int              `json:"max_observation_age_seconds"`
	ObservedAt               string           `json:"observed_at"`
	Watchlist                WatchlistState   `json:"watchlist"`
	Quotes                   []WatchlistQuote `json:"quotes"`
}

type watchlistRecoveryProof struct {
	SHA256   string
	Revision int64
	Entries  int
}

func watchlistKey(e WatchlistEntry) string {
	return instrumentListingKey(e.Venue, e.Symbol, e.Currency)
}

func canonicalWatchlistEntries(entries []WatchlistEntry) ([]WatchlistEntry, error) {
	if entries == nil || len(entries) > 50 {
		return nil, errWatchlistInput
	}
	result := append([]WatchlistEntry{}, entries...)
	for _, e := range result {
		kr := e.Venue == "XKRX" && e.Currency == "KRW" && watchlistKRSymbol.MatchString(e.Symbol)
		us := (e.Venue == "XNAS" || e.Venue == "XNYS") && e.Currency == "USD" && watchlistUSSymbol.MatchString(e.Symbol)
		if !kr && !us {
			return nil, errWatchlistInput
		}
	}
	sort.Slice(result, func(i, j int) bool { return watchlistKey(result[i]) < watchlistKey(result[j]) })
	for i := 1; i < len(result); i++ {
		if result[i-1] == result[i] {
			return nil, errWatchlistInput
		}
	}
	return result, nil
}

func decodeWatchlistChange(raw []byte) (*WatchlistChange, error) {
	if len(raw) == 0 || len(raw) > watchlistBodyLimit || !utf8.Valid(raw) || !validJSONSurrogates(raw) {
		return nil, errWatchlistInput
	}
	fields, err := benchmarkObject(raw, "expected_revision", "entries")
	if err != nil {
		return nil, errWatchlistInput
	}
	var change WatchlistChange
	var entries []json.RawMessage
	if json.Unmarshal(fields["expected_revision"], &change.ExpectedRevision) != nil || change.ExpectedRevision < 0 || change.ExpectedRevision > watchlistRevisionLimit ||
		json.Unmarshal(fields["entries"], &entries) != nil || entries == nil || len(entries) > 50 {
		return nil, errWatchlistInput
	}
	change.Entries = []WatchlistEntry{}
	for _, rawEntry := range entries {
		if _, err := benchmarkObject(rawEntry, "venue", "symbol", "currency"); err != nil {
			return nil, errWatchlistInput
		}
		var entry WatchlistEntry
		if json.Unmarshal(rawEntry, &entry) != nil {
			return nil, errWatchlistInput
		}
		change.Entries = append(change.Entries, entry)
	}
	change.Entries, err = canonicalWatchlistEntries(change.Entries)
	return &change, err
}

func requireWatchlistSchema(ctx context.Context, q queryer) error {
	script, err := migrationFiles.ReadFile("migrations/026_watchlist_state.sql")
	if err != nil {
		return err
	}
	for _, object := range []struct{ kind, name string }{{"TABLE", "watchlist_state"}, {"TRIGGER", "watchlist_state_no_delete"}, {"TRIGGER", "watchlist_state_update_guard"}, {"TRIGGER", "watchlist_state_no_insert"}} {
		expected := ""
		// shortcut: controlled migration DDL uses blank-line-separated blocks; use a SQL parser if that format changes.
		for _, block := range strings.Split(string(script), "\n\n") {
			definition := strings.Join(strings.Fields(block), " ")
			if strings.HasPrefix(definition, "CREATE "+object.kind+" "+object.name+" ") {
				expected = strings.TrimSuffix(definition, ";")
			}
		}
		var actual string
		if expected == "" || q.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name=? AND type=? AND tbl_name='watchlist_state'`, object.name, strings.ToLower(object.kind)).Scan(&actual) != nil || strings.Join(strings.Fields(actual), " ") != expected {
			return errors.New("watchlist physical schema changed")
		}
	}
	var count int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
        SELECT name FROM sqlite_master WHERE tbl_name='watchlist_state' AND type IN ('index','trigger')
        UNION ALL SELECT name FROM sqlite_temp_master WHERE tbl_name='watchlist_state' OR name='watchlist_state'
    )`).Scan(&count); err != nil || count != 3 {
		return errors.New("watchlist schema inventory changed")
	}
	return nil
}

func readWatchlistState(ctx context.Context, q queryer) (*WatchlistState, watchlistRecoveryProof, error) {
	var proof watchlistRecoveryProof
	if err := requireWatchlistSchema(ctx, q); err != nil {
		return nil, proof, err
	}
	var rows int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM watchlist_state`).Scan(&rows); err != nil || rows != 1 {
		return nil, proof, errors.New("watchlist singleton is missing")
	}
	var revision int64
	var raw, hash string
	if err := q.QueryRowContext(ctx, `SELECT revision,state_json,state_sha256 FROM watchlist_state WHERE singleton=1`).Scan(&revision, &raw, &hash); err != nil {
		return nil, proof, err
	}
	var state WatchlistState
	if len(raw) > watchlistBodyLimit || json.Unmarshal([]byte(raw), &state) != nil || state.Revision != revision || revision < 0 || revision > watchlistRevisionLimit {
		return nil, proof, errors.New("watchlist state is invalid")
	}
	entries, err := canonicalWatchlistEntries(state.Entries)
	if err != nil || !reflect.DeepEqual(entries, state.Entries) || (revision == 0 && (state.UpdatedAt != nil || len(entries) != 0)) ||
		(revision > 0 && (state.UpdatedAt == nil || !canonicalUTCString(*state.UpdatedAt))) {
		return nil, proof, errors.New("watchlist state metadata is invalid")
	}
	canonical, sha, err := orderJSONHash(state)
	if err != nil || string(canonical) != raw || sha != hash {
		return nil, proof, errors.New("watchlist state proof mismatch")
	}
	return &state, watchlistRecoveryProof{sha, revision, len(entries)}, nil
}

func proveWatchlistRecovery(ctx context.Context, q queryer) (watchlistRecoveryProof, error) {
	_, proof, err := readWatchlistState(ctx, q)
	return proof, err
}

func (s *Service) updateWatchlist(ctx context.Context, change WatchlistChange) (*WatchlistState, error) {
	entries, err := canonicalWatchlistEntries(change.Entries)
	if err != nil || change.ExpectedRevision < 0 || change.ExpectedRevision > watchlistRevisionLimit {
		return nil, errWatchlistInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	state, _, err := readWatchlistState(ctx, tx)
	if err != nil {
		return nil, err
	}
	same := reflect.DeepEqual(state.Entries, entries)
	if state.Revision != change.ExpectedRevision {
		if state.Revision == change.ExpectedRevision+1 && same {
			return state, nil
		}
		return nil, errWatchlistChanged
	}
	if same {
		return state, nil
	}
	if state.Revision == watchlistRevisionLimit {
		return nil, errWatchlistInput
	}
	at := s.now().UTC()
	if !canonicalUTCString(at.Format(time.RFC3339Nano)) || (state.UpdatedAt != nil && at.Before(mustParseWatchlistTime(*state.UpdatedAt))) {
		return nil, errors.New("watchlist clock precedes saved state")
	}
	next := WatchlistState{Revision: state.Revision + 1, UpdatedAt: stringPointer(at.Format(time.RFC3339Nano)), Entries: entries}
	raw, sha, err := orderJSONHash(next)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE watchlist_state SET revision=?,state_json=?,state_sha256=? WHERE singleton=1 AND revision=?`, next.Revision, string(raw), sha, state.Revision)
	if err != nil {
		return nil, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return nil, errors.New("watchlist update did not affect one state")
	}
	stored, _, err := readWatchlistState(ctx, tx)
	if err != nil || !reflect.DeepEqual(stored, &next) {
		return nil, errors.New("watchlist write verification failed")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stored, nil
}

func mustParseWatchlistTime(value string) time.Time {
	at, _ := time.Parse(time.RFC3339Nano, value) // Validated at the input/storage boundary.
	return at
}

func (s *Service) watchlist(ctx context.Context) (*WatchlistReport, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	at := s.now().UTC()
	state, _, err := readWatchlistState(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !canonicalUTCString(at.Format(time.RFC3339Nano)) || (state.UpdatedAt != nil && at.Before(mustParseWatchlistTime(*state.UpdatedAt))) {
		return nil, errors.New("watchlist clock precedes saved state")
	}
	observations, err := valuationPriceObservations(ctx, tx, at)
	if err != nil {
		return nil, err
	}
	times, err := currentPriceObservationTimes(observations)
	if err != nil {
		return nil, err
	}
	report := &WatchlistReport{PolicyVersion: watchlistPolicy, MaxObservationAgeSeconds: 86400, ObservedAt: at.Format(time.RFC3339Nano), Watchlist: *state, Quotes: []WatchlistQuote{}}
	for _, entry := range state.Entries {
		report.Quotes = append(report.Quotes, watchlistQuote(entry, observations, times, at))
	}
	return report, nil
}

func watchlistQuote(entry WatchlistEntry, observations []SecurityPriceObservation, times map[string][3]time.Time, at time.Time) WatchlistQuote {
	quote := WatchlistQuote{WatchlistEntry: entry, Status: "missing"}
	identities := map[string]bool{}
	var selected *SecurityPriceObservation
	ambiguous := false
	for i := range observations {
		p := &observations[i]
		owner := p.Source == ownerPriceSource && p.PriceAdjustment == ownerPriceAdjustment
		fixture := p.Source == "local_fixture" && p.PriceAdjustment == marketDataAdjustmentUnspecified
		if (!owner && !fixture) || p.Symbol != entry.Symbol || p.Venue != entry.Venue || p.Currency != entry.Currency {
			continue
		}
		t := times[priceTimeKey(*p)]
		if t[0].After(at) || t[1].After(at) || t[2].After(at) {
			continue
		}
		identities[p.InstrumentID] = true
		if selected == nil || t[0].After(times[priceTimeKey(*selected)][0]) {
			selected, ambiguous = p, false
		} else if t[0].Equal(times[priceTimeKey(*selected)][0]) && !reflect.DeepEqual(watchlistPrice(p), watchlistPrice(selected)) {
			ambiguous = true
		}
	}
	if selected == nil {
		return quote
	}
	if ambiguous || len(identities) != 1 {
		quote.Status = "ambiguous"
		return quote
	}
	quote.Price = watchlistPrice(selected)
	quote.Status = "unknown"
	if selected.Source == "local_fixture" || at.Sub(times[priceTimeKey(*selected)][0]) > 24*time.Hour {
		quote.Status = "stale"
	}
	return quote
}

func watchlistPrice(p *SecurityPriceObservation) *WatchlistPrice {
	price := &WatchlistPrice{Source: p.Source, Price: p.Price, PriceAdjustment: p.PriceAdjustment, ObservedAt: p.ObservedAt, FetchedAt: p.FetchedAt, RecordedAt: p.RecordedAt, Sample: priceIsSample(p)}
	if p.Source == ownerPriceSource {
		price.SourceVersion = stringPointer(p.ownerVersion)
	}
	return price
}
