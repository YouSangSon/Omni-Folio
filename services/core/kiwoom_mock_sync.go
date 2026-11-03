package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"strings"
	"time"
)

const kiwoomMockSyncTimeout = 2 * time.Minute

var kiwoomMockProcessPacer = newKiwoomMockPacer(time.Now, waitKiwoomMockRequest)

type kiwoomMockSyncReceipt struct {
	Provider          string            `json:"provider"`
	Mode              KiwoomEnvironment `json:"mode"`
	FetchedAt         string            `json:"fetched_at"`
	RecordedAt        string            `json:"recorded_at"`
	LedgerRevision    string            `json:"ledger_revision"`
	Positions         int               `json:"positions"`
	OpenOrders        int               `json:"open_orders"`
	AllPositionsMatch bool              `json:"all_positions_match"`
}

func runKiwoomMockSyncCommand(ctx context.Context, args []string, out io.Writer) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	return runKiwoomMockSync(ctx, args, out, readKiwoomMockSecret, transport, time.Now, kiwoomMockProcessPacer)
}

func runKiwoomMockSync(ctx context.Context, args []string, out io.Writer, readSecret func(context.Context) ([]byte, error), transport http.RoundTripper, now func() time.Time, pacer *kiwoomMockPacer) error {
	if ctx == nil {
		return errors.New("kiwoom mock sync context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, kiwoomMockSyncTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	fs := flag.NewFlagSet("kiwoom-mock-sync", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "existing SQLite database")
	ledgerAccount := fs.String("ledger-account", "", "account-main")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *dbPath == "" || *ledgerAccount != "account-main" {
		return errors.New("kiwoom-mock-sync requires only -db and -ledger-account account-main")
	}
	db, err := openReadWriteExistingDB(ctx, *dbPath)
	if err != nil {
		return kiwoomMockSyncError(ctx, "database is unavailable")
	}
	defer db.Close()
	if err := preflightKiwoomMockSync(ctx, db); err != nil {
		return kiwoomMockSyncError(ctx, "database recovery failed")
	}
	if err := requireKiwoomMockBinding(ctx, db, ""); err != nil {
		return kiwoomMockSyncError(ctx, "stored broker account binding differs")
	}
	raw, err := readSecret(ctx)
	if err != nil {
		return kiwoomMockSyncError(ctx, "Keychain record is unavailable")
	}
	credentials, aliasKey, err := decodeKiwoomMockCredentials(raw)
	clear(raw)
	if err != nil {
		return err
	}
	defer clear(aliasKey)
	client, err := NewKiwoomClient(KiwoomMock, credentials.AppKey, credentials.SecretKey, aliasKey)
	if err != nil {
		return kiwoomMockSyncError(ctx, "credential record is invalid")
	}
	defer clear(client.aliasKey)
	expectedAccount := client.alias("account", credentials.AccountNumber)
	if err := requireKiwoomMockBinding(ctx, db, expectedAccount); err != nil {
		return kiwoomMockSyncError(ctx, "stored broker account binding differs")
	}
	client.transport = &kiwoomMockTransport{base: transport, pacer: pacer}
	client.now = now
	snapshot, err := client.Snapshot(ctx, KiwoomKRX)
	if err != nil {
		return kiwoomMockSyncError(ctx, "complete provider read failed; previous snapshot retained")
	}
	if snapshot.AccountRef != expectedAccount {
		return kiwoomMockSyncError(ctx, "provider account differs; previous snapshot retained")
	}
	stored, err := newService(db, now, randomID).recordKiwoomMockSnapshot(ctx, *ledgerAccount, expectedAccount, snapshot)
	if err != nil {
		return kiwoomMockSyncError(ctx, "could not verify and store snapshot")
	}
	return json.NewEncoder(out).Encode(kiwoomMockSyncReceipt{
		Provider: stored.Provider, Mode: stored.Environment, FetchedAt: stored.FetchedAt, RecordedAt: stored.RecordedAt,
		LedgerRevision: stored.LedgerRevision, Positions: len(stored.Snapshot.Positions), OpenOrders: len(stored.Snapshot.OpenOrders),
		AllPositionsMatch: stored.AllPositionsMatch,
	})
}

func kiwoomMockSyncError(ctx context.Context, message string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New("kiwoom mock sync " + message)
}

func preflightKiwoomMockSync(ctx context.Context, q orderQuerier) error {
	if err := requireSchemaContext(ctx, q); err != nil {
		return err
	}
	if err := requireKiwoomMockJournalSchema(ctx, q); err != nil {
		return err
	}
	if err := proveLedgerReplayState(ctx, q, false); err != nil {
		return err
	}
	if _, _, err := (&Service{}).proveLedgerEvents(ctx, q); err != nil {
		return err
	}
	_, err := proveBrokerRecovery(ctx, q)
	return err
}

func requireKiwoomMockJournalSchema(ctx context.Context, q orderQuerier) error {
	script, err := migrationFiles.ReadFile("migrations/003_broker_snapshots.sql")
	if err != nil {
		return err
	}
	for _, object := range []struct{ name, kind, table string }{
		{"broker_snapshots", "TABLE", "broker_snapshots"},
		{"broker_snapshots_latest_idx", "INDEX", "broker_snapshots"},
		{"broker_snapshots_no_update", "TRIGGER", "broker_snapshots"},
		{"broker_snapshots_no_delete", "TRIGGER", "broker_snapshots"},
		{"broker_snapshot_reconciliations", "TABLE", "broker_snapshot_reconciliations"},
		{"broker_snapshot_reconciliations_latest_idx", "INDEX", "broker_snapshot_reconciliations"},
		{"broker_snapshot_reconciliations_no_update", "TRIGGER", "broker_snapshot_reconciliations"},
		{"broker_snapshot_reconciliations_no_delete", "TRIGGER", "broker_snapshot_reconciliations"},
		{"events_no_update", "TRIGGER", "events"},
		{"events_no_delete", "TRIGGER", "events"},
	} {
		expected := ""
		// shortcut: controlled migration objects are blank-line-separated; use a SQL parser if that format changes.
		for _, block := range strings.Split(string(script), "\n\n") {
			definition := strings.Join(strings.Fields(block), " ")
			if strings.HasPrefix(definition, "CREATE "+object.kind+" "+object.name+" ") && strings.HasSuffix(definition, ";") {
				expected = strings.TrimSuffix(definition, ";")
				break
			}
		}
		var actual string
		if expected == "" || q.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name=? AND type=? AND tbl_name=?`, object.name, strings.ToLower(object.kind), object.table).Scan(&actual) != nil || strings.Join(strings.Fields(actual), " ") != expected {
			return errors.New("kiwoom mock physical journal schema changed")
		}
	}
	for _, table := range []string{"broker_snapshots", "broker_snapshot_reconciliations", "events"} {
		var shadows int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_temp_master WHERE name=? OR tbl_name=?`, table, table).Scan(&shadows); err != nil {
			return err
		}
		if shadows != 0 {
			return errors.New("kiwoom mock temporary schema shadows the journal")
		}
	}
	for _, table := range []string{"broker_snapshots", "broker_snapshot_reconciliations"} {
		var objects int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE tbl_name=? AND type IN ('index','trigger')`, table).Scan(&objects); err != nil {
			return err
		}
		if objects != 5 {
			return errors.New("kiwoom mock physical journal inventory changed")
		}
	}
	return nil
}

func requireKiwoomMockBinding(ctx context.Context, q orderQuerier, expectedAccount string) error {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT environment,account_ref FROM broker_snapshots WHERE provider='kiwoom' AND exchange='KRX'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var environment KiwoomEnvironment
		var account string
		if err := rows.Scan(&environment, &account); err != nil {
			return err
		}
		count++
		if count > 1 || environment != KiwoomMock || (expectedAccount != "" && account != expectedAccount) {
			return errors.New("stored Kiwoom KRX history has a different account binding")
		}
	}
	return rows.Err()
}

func (s *Service) recordKiwoomMockSnapshot(ctx context.Context, ledgerAccount, expectedAccount string, snapshot *KiwoomSnapshot) (*BrokerKnownGoodSnapshot, error) {
	if ledgerAccount != "account-main" || !orderAlias(expectedAccount, "account") || snapshot == nil ||
		snapshot.Environment != KiwoomMock || snapshot.AccountRef != expectedAccount {
		return nil, errors.New("invalid Kiwoom mock account binding")
	}
	if err := validateKiwoomSnapshot(snapshot); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := preflightKiwoomMockSync(ctx, tx); err != nil {
		return nil, err
	}
	if err := requireKiwoomMockBinding(ctx, tx, expectedAccount); err != nil {
		return nil, err
	}
	stored, err := s.appendKiwoomSnapshot(ctx, tx, ledgerAccount, snapshot)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stored, nil
}

type kiwoomMockPacer struct {
	gate chan struct{}
	next time.Time
	now  func() time.Time
	wait func(context.Context, time.Duration) error
}

func newKiwoomMockPacer(now func() time.Time, wait func(context.Context, time.Duration) error) *kiwoomMockPacer {
	return &kiwoomMockPacer{gate: make(chan struct{}, 1), now: now, wait: wait}
}

func waitKiwoomMockRequest(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type kiwoomMockTransport struct {
	base  http.RoundTripper
	pacer *kiwoomMockPacer
}

func (t *kiwoomMockTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodPost || request.URL.Scheme != "https" || request.URL.Host != "mockapi.kiwoom.com" ||
		request.URL.RawQuery != "" || request.URL.User != nil ||
		!((request.URL.Path == kiwoomTokenPath && request.Header.Get("api-id") == "") ||
			(request.URL.Path == kiwoomAccountPath && (request.Header.Get("api-id") == "ka00001" || request.Header.Get("api-id") == "kt00018" || request.Header.Get("api-id") == "ka10075"))) {
		return nil, &KiwoomError{Kind: "invalid_request"}
	}
	return t.pacer.roundTrip(request, t.base)
}

func (p *kiwoomMockPacer) roundTrip(request *http.Request, base http.RoundTripper) (*http.Response, error) {
	select {
	case <-request.Context().Done():
		return nil, request.Context().Err()
	case p.gate <- struct{}{}:
	}
	defer func() { <-p.gate }()
	if delay := p.next.Sub(p.now()); delay > 0 {
		if err := p.wait(request.Context(), delay); err != nil {
			return nil, err
		}
	}
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	response, err := base.RoundTrip(request)
	p.next = p.now().Add(time.Second)
	return response, err
}
