package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var watchAAPL = WatchlistEntry{"XNAS", "AAPL", "USD"}
var watchKR = WatchlistEntry{"XKRX", "005930", "KRW"}

func watchlistRequest(t testing.TB, handler http.Handler, method, path, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("watchlist HTTP %d, want %d: %s", w.Code, status, w.Body.String())
	}
	return w
}

func watchlistReadForTest(t testing.TB, svc *Service) *WatchlistReport {
	t.Helper()
	w := watchlistRequest(t, svc.routes(), "GET", "/v1/watchlist", "", 200)
	if w.Header().Get("Cache-Control") != "no-store" || w.Body.Len() > watchlistResponseLimit {
		t.Fatal("watchlist cache/body boundary")
	}
	for _, private := range []string{"instrument_id", "account_id", "source_event_id", "observation_id", "state_sha256"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("watchlist leaked private field", private)
		}
	}
	var report WatchlistReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	return &report
}

func watchlistWriteForTest(t testing.TB, svc *Service, revision int64, entries ...WatchlistEntry) *WatchlistState {
	t.Helper()
	raw, err := json.Marshal(WatchlistChange{revision, append([]WatchlistEntry{}, entries...)})
	if err != nil {
		t.Fatal(err)
	}
	w := watchlistRequest(t, svc.routes(), "POST", "/v1/watchlist", string(raw), 200)
	var state WatchlistState
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return &state
}

func TestWatchlistStartsEmptyWithoutChangingTheLedger(t *testing.T) {
	svc, path := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-02T00:00:00Z") }
	before, err := snapshotFrom(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	empty := watchlistReadForTest(t, svc)
	var shared struct{ Reports map[string]json.RawMessage }
	if json.Unmarshal(fixture(t, "golden-watchlist.json"), &shared) != nil {
		t.Fatal("shared fixture")
	}
	raw, _ := json.Marshal(empty)
	var got, want any
	if json.Unmarshal(raw, &got) != nil || json.Unmarshal(shared.Reports["empty"], &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("empty shared report: %s", raw)
	}
	saved := watchlistWriteForTest(t, svc, 0, watchAAPL, watchKR)
	if saved.Revision != 1 || len(saved.Entries) != 2 || saved.Entries[0] != watchKR {
		t.Fatal("watchlist was not saved canonically")
	}
	retry := watchlistWriteForTest(t, svc, 0, watchKR, watchAAPL)
	if !reflect.DeepEqual(retry, saved) {
		t.Fatal("same intent retry changed its acknowledgement")
	}
	if noop := watchlistWriteForTest(t, svc, 1, watchAAPL, watchKR); !reflect.DeepEqual(noop, saved) {
		t.Fatal("no-op advanced revision")
	}
	db, err := openExistingDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reopened := newService(db, svc.now, svc.id)
	if reopenedReport := watchlistReadForTest(t, reopened); !reflect.DeepEqual(&reopenedReport.Watchlist, saved) {
		t.Fatal("list was not persistent")
	}
	removed := watchlistWriteForTest(t, svc, 1)
	if removed.Revision != 2 || len(removed.Entries) != 0 || removed.UpdatedAt == nil {
		t.Fatal("removal reset singleton/revision")
	}
	// A later add must make the original delayed CAS obsolete, even with the same desired entries.
	watchlistWriteForTest(t, svc, 2, watchAAPL, watchKR)
	old, _ := json.Marshal(WatchlistChange{0, []WatchlistEntry{watchAAPL, watchKR}})
	watchlistRequest(t, svc.routes(), "POST", "/v1/watchlist", string(old), 409)
	after, err := snapshotFrom(context.Background(), svc.db)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("watchlist changed ledger", err)
	}
	if _, err := resolveInstrumentListing(context.Background(), svc.db, "XNAS", "AAPL", "USD"); err != errInstrumentListingNotFound {
		t.Fatal("watchlist granted listing authority", err)
	}
	if _, err := svc.importOwnerPriceSeries(context.Background(), []byte(ownerPriceTestInput)); err == nil {
		t.Fatal("watchlist bypassed ledger price admission")
	}
}

func TestWatchlistConcurrentChangesDoNotOverwriteOneAnother(t *testing.T) {
	svc, path := testService(t, nil, nil)
	db, err := openExistingDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other := newService(db, svc.now, svc.id)
	start := make(chan struct{})
	results := make(chan int, 2)
	var group sync.WaitGroup
	for i, item := range []WatchlistEntry{watchAAPL, watchKR} {
		group.Add(1)
		service := []*Service{svc, other}[i]
		go func(s *Service, e WatchlistEntry) {
			defer group.Done()
			<-start
			raw, _ := json.Marshal(WatchlistChange{0, []WatchlistEntry{e}})
			r := httptest.NewRequest("POST", "/v1/watchlist", strings.NewReader(string(raw)))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.routes().ServeHTTP(w, r)
			results <- w.Code
		}(service, item)
	}
	close(start)
	group.Wait()
	close(results)
	statuses := map[int]int{}
	for code := range results {
		statuses[code]++
	}
	if statuses[200] != 1 || statuses[409] != 1 {
		t.Fatal("CAS did not serialize writers", statuses)
	}
	state := watchlistReadForTest(t, svc).Watchlist
	if state.Revision != 1 || len(state.Entries) != 1 {
		t.Fatal("partial/concurrent update")
	}
}

func TestWatchlistSingletonReplacementCannotResetRevision(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	var initial, hash string
	if err := svc.db.QueryRow(`SELECT state_json,state_sha256 FROM watchlist_state`).Scan(&initial, &hash); err != nil {
		t.Fatal(err)
	}
	saved := watchlistWriteForTest(t, svc, 0, watchAAPL)
	if _, err := svc.db.Exec(`INSERT OR REPLACE INTO watchlist_state(singleton,revision,state_json,state_sha256) VALUES(1,0,?,?)`, initial, hash); err == nil {
		t.Fatal("REPLACE reset the singleton without an UPDATE")
	}
	if _, err := svc.db.Exec(`DELETE FROM watchlist_state`); err == nil {
		t.Fatal("singleton deletion accepted")
	}
	state := watchlistReadForTest(t, svc).Watchlist
	if !reflect.DeepEqual(&state, saved) {
		t.Fatal("rejected replacement changed state")
	}
}

func TestWatchlistClosedInputBoundsAndOrigin(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	valid := `{"expected_revision":0,"entries":[{"venue":"XNAS","symbol":"AAPL","currency":"USD"}]}`
	bad := []string{
		`{}`, `null`, `{"expected_revision":0,"entries":null}`, `{"expected_revision":0,"entries":[] } {}`,
		strings.Replace(valid, `"expected_revision":0`, `"expected_revision":0,"expected_revision":1`, 1),
		strings.Replace(valid, `"symbol":"AAPL"`, `"symbol":"AAPL","sy\u006dbol":"MSFT"`, 1),
		strings.Replace(valid, `"expected_revision":0`, `"expected_revision":0.0`, 1),
		strings.Replace(valid, `"expected_revision":0`, `"expected_revision":9007199254740992`, 1),
		strings.Replace(valid, `"expected_revision":0`, `"expected_revision":-1`, 1),
		strings.Replace(valid, `"USD"`, `"KRW"`, 1), strings.Replace(valid, `"XNAS"`, `"XOTHER"`, 1),
		strings.Replace(valid, `"AAPL"`, `"aapl"`, 1), strings.Replace(valid, `"AAPL"`, `"AAPL\n"`, 1),
		strings.Replace(valid, `"AAPL"`, `"\ud800"`, 1), strings.Replace(valid, `"AAPL"`, `null`, 1),
		strings.Replace(valid, `"currency":"USD"`, `"currency":"USD","private_account":"secret"`, 1),
		valid + strings.Repeat(" ", watchlistBodyLimit),
	}
	duplicate, _ := json.Marshal(WatchlistChange{0, []WatchlistEntry{watchAAPL, watchAAPL}})
	bad = append(bad, string(duplicate))
	tooMany := []WatchlistEntry{}
	for i := 0; i < 51; i++ {
		tooMany = append(tooMany, WatchlistEntry{"XNAS", fmt.Sprintf("S%d", i), "USD"})
	}
	raw, _ := json.Marshal(WatchlistChange{0, tooMany})
	bad = append(bad, string(raw))
	for i, input := range bad {
		t.Run(fmt.Sprint(i), func(t *testing.T) { watchlistRequest(t, svc.routes(), "POST", "/v1/watchlist", input, 400) })
	}
	for _, path := range []string{"/v1/watchlist?", "/v1/watchlist?as_of=2026-01-01"} {
		watchlistRequest(t, svc.routes(), "GET", path, "", 400)
		watchlistRequest(t, svc.routes(), "POST", path, valid, 400)
	}
	r := httptest.NewRequest("POST", "/v1/watchlist", strings.NewReader(valid))
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("non-JSON write accepted")
	}
	handler := withCORS(svc.routes(), "http://localhost:9000")
	for _, origins := range [][]string{{"https://untrusted.invalid"}, {""}, {"null"}, {"http://localhost:9000", "http://localhost:9000"}} {
		r := httptest.NewRequest("POST", "/v1/watchlist", strings.NewReader(valid))
		r.Header.Set("Content-Type", "application/json")
		r.Header["Origin"] = origins
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "watchlist_origin_rejected") {
			t.Fatal("origin guard", w.Code)
		}
	}
	if state := watchlistReadForTest(t, svc).Watchlist; state.Revision != 0 {
		t.Fatal("rejected request mutated state")
	}
	r = httptest.NewRequest("POST", "/v1/watchlist", strings.NewReader(valid))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://localhost:9000")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("allowed origin rejected")
	}
	watchlistWriteForTest(t, svc, 1, tooMany[:50]...)
}

func watchlistRecordFixture(t testing.TB, svc *Service, id string, e WatchlistEntry, price, at string) {
	t.Helper()
	_, err := svc.recordSecurityPriceObservation(context.Background(), SecurityPriceObservationInput{Source: "local_fixture", SourceObservationID: id, InstrumentID: "instrument_" + id, Symbol: e.Symbol, Venue: e.Venue, Currency: e.Currency, Price: price, PriceAdjustment: "unspecified", ObservedAt: at, FetchedAt: at})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWatchlistSharedMixedQuoteStatesAndWholeVersionWithdrawal(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.now = func() time.Time { return mustTime("2026-01-02T00:00:00Z") }
	applyTestCSV(t, svc, "watch-ledger", testCSV("capital,account-main,DEPOSIT,2025-01-01T00:00:00Z,,,,,USD,100", "buy,account-main,BUY,2025-01-02T00:00:00Z,AAPL,1,100,0,USD,-100"))
	input := ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) {
		i.FetchedAt = "2026-01-01T23:00:00Z"
		i.Sample = true
		i.Points = []OwnerPricePoint{{ObservedAt: i.FetchedAt, Price: "150.25"}}
	})
	importOwnerPriceForTest(t, svc, input)
	msft := WatchlistEntry{"XNAS", "MSFT", "USD"}
	brk := WatchlistEntry{"XNYS", "BRK.B", "USD"}
	watchlistRecordFixture(t, svc, "kr", watchKR, "70000", "2026-01-01T23:00:00Z")
	watchlistRecordFixture(t, svc, "brk1", brk, "450", "2026-01-01T23:00:00Z")
	watchlistRecordFixture(t, svc, "brk2", brk, "451", "2026-01-01T23:00:00Z")
	watchlistWriteForTest(t, svc, 0, watchAAPL, brk, msft, watchKR)
	clocks := 0
	svc.now = func() time.Time { clocks++; return mustTime("2026-01-02T00:00:00Z") }
	report := watchlistReadForTest(t, svc)
	if clocks != 1 {
		t.Fatal("read used more than one cutoff")
	}
	var shared struct{ Reports map[string]json.RawMessage }
	if json.Unmarshal(fixture(t, "golden-watchlist.json"), &shared) != nil {
		t.Fatal("shared fixture")
	}
	raw, _ := json.Marshal(report)
	var got, want any
	if json.Unmarshal(raw, &got) != nil || json.Unmarshal(shared.Reports["mixed"], &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("mixed shared report drift: %s", raw)
	}
	for _, age := range []time.Duration{24 * time.Hour, 24*time.Hour + time.Nanosecond} {
		svc.now = func() time.Time { return mustTime("2026-01-01T23:00:00Z").Add(age) }
		q := watchlistReadForTest(t, svc).Quotes[1]
		if (age == 24*time.Hour && q.Status != "unknown") || (age > 24*time.Hour && q.Status != "stale") {
			t.Fatal("24h exact boundary", q)
		}
	}
	svc.now = func() time.Time { return mustTime("2026-01-03T00:00:00Z") }
	importOwnerPriceForTest(t, svc, ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.SourceVersion = "withdrawn"; i.Points = []OwnerPricePoint{} }))
	if q := watchlistReadForTest(t, svc).Quotes[1]; q.Price != nil || q.Status != "missing" {
		t.Fatal("withdrawal revived previous whole version")
	}
	importOwnerPriceForTest(t, svc, ownerPriceInputForTest(t, func(i *OwnerPriceSeriesInput) { i.SourceVersion = "venue-changed"; i.Venue = "XNYS" }))
	if q := watchlistReadForTest(t, svc).Quotes[1]; q.Price != nil || q.Status != "missing" {
		t.Fatal("venue filter revived previous whole version")
	}
}

func TestWatchlistQuoteNeverGuessesIdentityTimeOrProvenance(t *testing.T) {
	at := mustTime("2026-01-02T00:00:00Z")
	original := SecurityPriceObservation{ObservationID: "first", Source: "local_fixture", InstrumentID: "instrument_aapl", Symbol: "AAPL", Venue: "XNAS", Currency: "USD", Price: "100", PriceAdjustment: "unspecified", ObservedAt: "2026-01-01T23:00:00Z", FetchedAt: "2026-01-01T23:00:00Z", RecordedAt: "2026-01-01T23:00:00Z"}
	tests := []struct {
		name   string
		edit   func(*SecurityPriceObservation)
		status string
	}{
		{"other venue", func(p *SecurityPriceObservation) { p.Venue = "XNYS" }, "stale"},
		{"other currency", func(p *SecurityPriceObservation) { p.Currency = "KRW" }, "stale"},
		{"other symbol", func(p *SecurityPriceObservation) { p.Symbol = "MSFT" }, "stale"},
		{"other instrument", func(p *SecurityPriceObservation) { p.InstrumentID = "unrelated" }, "ambiguous"},
		{"price conflict", func(p *SecurityPriceObservation) { p.Price = "101" }, "ambiguous"},
		{"time provenance conflict", func(p *SecurityPriceObservation) { p.FetchedAt = "2026-01-01T23:01:00Z"; p.RecordedAt = p.FetchedAt }, "ambiguous"},
		{"no broker promotion", func(p *SecurityPriceObservation) { p.Source = "kiwoom_production"; p.Price = "999" }, "stale"},
		{"future recorded", func(p *SecurityPriceObservation) { p.Price = "999"; p.RecordedAt = "2026-01-02T00:00:00.000000001Z" }, "stale"},
		{"future all", func(p *SecurityPriceObservation) {
			p.Price = "999"
			p.ObservedAt = "2026-01-02T00:00:00.000000001Z"
			p.FetchedAt = p.ObservedAt
			p.RecordedAt = p.ObservedAt
		}, "stale"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			other := original
			other.ObservationID = "second"
			tc.edit(&other)
			observations := []SecurityPriceObservation{original, other}
			times, err := currentPriceObservationTimes(observations)
			if err != nil {
				t.Fatal(err)
			}
			q := watchlistQuote(watchAAPL, observations, times, at)
			if q.Status != tc.status || (q.Status == "ambiguous" && q.Price != nil) || (q.Price != nil && q.Price.Price != "100") {
				t.Fatalf("selection guessed: %+v", q)
			}
		})
	}
	owner := original
	owner.Source = "owner_declared"
	owner.PriceAdjustment = "unadjusted"
	owner.ownerVersion = "v1"
	conflicting := owner
	conflicting.ObservationID = "second"
	conflicting.ownerSample = true
	observations := []SecurityPriceObservation{owner, conflicting}
	times, err := currentPriceObservationTimes(observations)
	if err != nil {
		t.Fatal(err)
	}
	if q := watchlistQuote(watchAAPL, observations, times, at); q.Status != "ambiguous" || q.Price != nil {
		t.Fatal("sample provenance silently chosen")
	}
}

func TestWatchlistFailsClosedOnCorruptStateAndUnexpectedSchema(t *testing.T) {
	for _, query := range []string{
		`UPDATE watchlist_state SET revision=revision+1,state_json='private-account-secret'`,
		`DROP TRIGGER watchlist_state_no_delete; DELETE FROM watchlist_state`,
		`CREATE TEMP TRIGGER watchlist_ignore BEFORE UPDATE ON watchlist_state BEGIN SELECT RAISE(IGNORE); END`,
		`CREATE TEMP VIEW watchlist_state AS SELECT 1 singleton,0 revision,'{"revision":0,"updated_at":null,"entries":[]}' state_json,'ebe3680d9edf19cb6190a0f1482280921ed943efff3d7911eb91453049ba5a1b' state_sha256`,
	} {
		t.Run(query[:12], func(t *testing.T) {
			svc, _ := testService(t, nil, nil)
			watchlistWriteForTest(t, svc, 0, watchAAPL)
			if _, err := svc.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			for _, method := range []string{"GET", "POST"} {
				w := watchlistRequest(t, svc.routes(), method, "/v1/watchlist", `{"expected_revision":0,"entries":[]}`, 503)
				for _, secret := range []string{"private-account-secret", "sqlite", "watchlist_state"} {
					if strings.Contains(w.Body.String(), secret) {
						t.Fatal("error leaked storage details")
					}
				}
			}
			if err := requireServerStartupRecovery(svc.db); err == nil {
				t.Fatal("corrupt watchlist admitted at startup")
			}
		})
	}
}

func TestWatchlistCorruptPriceFailsWholeObservation(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	watchlistRecordFixture(t, svc, "valid", watchKR, "70000", "2026-01-10T00:00:00Z")
	watchlistRecordFixture(t, svc, "private-price-bad", watchAAPL, "150", "2026-01-10T00:00:00Z")
	watchlistWriteForTest(t, svc, 0, watchKR, watchAAPL)
	if report := watchlistReadForTest(t, svc); report.Quotes[0].Price == nil || report.Quotes[1].Price == nil {
		t.Fatal("precondition: both stored prices must be readable")
	}
	var trigger string
	if err := svc.db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='security_price_observations_no_update'`).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`DROP TRIGGER security_price_observations_no_update; UPDATE security_price_observations SET price='432123.45' WHERE source_observation_id='private-price-bad'`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	w := watchlistRequest(t, svc.routes(), "GET", "/v1/watchlist", "", 503)
	for _, secret := range []string{"70000", "432123.45", "private-price-bad", "quotes", "state_sha256"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("partial/private price escaped failed whole observation")
		}
	}
}
