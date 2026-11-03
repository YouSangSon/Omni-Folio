package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKiwoomMockSyncDispatchRequiresExplicitArguments(t *testing.T) {
	for _, args := range [][]string{
		{}, {"-db", "missing.db"}, {"-ledger-account", "account-main"},
		{"-db", "missing.db", "-ledger-account", "other"},
		{"-db", "missing.db", "-ledger-account", "account-main", "-mode", "production"},
		{"-db", "missing.db", "-ledger-account", "account-main", "extra"},
	} {
		err := runContext(context.Background(), append([]string{"kiwoom-mock-sync"}, args...))
		if err == nil || !strings.Contains(err.Error(), "requires only -db and -ledger-account account-main") {
			t.Fatalf("args=%v: expected explicit mock-only argument validation, got %v", args, err)
		}
	}
}

func TestKiwoomMockSyncCredentialBoundary(t *testing.T) {
	valid := string(mockSyncSecret("9876543210"))
	for name, raw := range map[string]string{
		"empty": "", "array": "[]", "unknown": strings.Replace(valid, `"app_key":`, `"mode":"production","app_key":`, 1),
		"duplicate":         strings.Replace(valid, `"app_key":`, `"app_key":"other","app_key":`, 1),
		"escaped duplicate": strings.Replace(valid, `"app_key":`, `"app_\u006bey":"other","app_key":`, 1),
		"missing":           strings.Replace(valid, `"schema_version":"kiwoom-mock-read.v1",`, "", 1),
		"wrong schema":      strings.Replace(valid, "kiwoom-mock-read.v1", "kiwoom-live.v1", 1),
		"null":              strings.Replace(valid, `"synthetic-app-key"`, "null", 1),
		"blank key":         strings.Replace(valid, "synthetic-app-key", "   ", 1),
		"control":           strings.Replace(valid, "synthetic-app-key", `app\nkey`, 1),
		"long key":          strings.Replace(valid, "synthetic-app-key", strings.Repeat("k", 1025), 1),
		"bad account":       strings.Replace(valid, "9876543210", "123456789", 1),
		"large":             strings.Repeat(" ", kiwoomMockSecretLimit+1), "trailing": valid + "{}",
		"bad base64":   strings.Replace(valid, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWZ=", 1),
		"short alias":  strings.Replace(valid, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", base64.StdEncoding.EncodeToString([]byte("short")), 1),
		"invalid UTF8": valid + string([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeKiwoomMockCredentials([]byte(raw)); err == nil || err.Error() != "kiwoom mock credential record is invalid" {
				t.Fatalf("invalid credential boundary accepted or leaked input: %v", err)
			}
		})
	}
	parsed, key, err := decodeKiwoomMockCredentials([]byte(valid + "\n"))
	if err != nil || parsed.AccountNumber != "9876543210" || len(key) != 32 {
		t.Fatalf("valid bounded credential record rejected: %v", err)
	}
}

func TestKiwoomMockSyncLocalTLSFeedsExistingReadViewAndRetry(t *testing.T) {
	svc, path := testService(t, nil, nil)
	seedKRXLedger(t, svc)
	transport, calls := mockSyncTLS(t, "9876543210", nil)
	var out bytes.Buffer
	if err := mockSyncRun(context.Background(), path, &out, transport, "9876543210"); err != nil {
		t.Fatal(err)
	}
	var receipt kiwoomMockSyncReceipt
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || receipt.Provider != "kiwoom" || receipt.Mode != KiwoomMock ||
		receipt.LedgerRevision != "rev_0000000002" || receipt.Positions != 2 || receipt.OpenOrders != 1 || receipt.AllPositionsMatch || calls.Load() != 6 {
		t.Fatalf("incomplete runtime receipt: %+v requests=%d err=%v", receipt, calls.Load(), err)
	}
	first := mockSyncReadView(t, svc)
	if !strings.Contains(first, `"environment":"mock"`) || !strings.Contains(first, `"broker_quantity":"10","ledger_quantity":"7"`) {
		t.Fatalf("stored provider result did not reach existing GET: %s", first)
	}
	var raw string
	if err := svc.db.QueryRow(`SELECT snapshot_json FROM broker_snapshots`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"9876543210", "0000042", "synthetic-app-key", "synthetic-secret-key", "synthetic-token"} {
		if strings.Contains(raw+out.String()+first, secret) {
			t.Fatal("private provider identity or credential escaped normalization")
		}
	}
	for _, private := range []string{"account_ref", "masked_account", "snapshot_id", "reconciliation_id", "symbol", "quantity", "price", "cash", "name"} {
		if strings.Contains(out.String(), `"`+private+`"`) {
			t.Fatalf("receipt exposed private field %s", private)
		}
	}
	if err := mockSyncRun(context.Background(), path, io.Discard, transport, "9876543210"); err != nil {
		t.Fatal(err)
	}
	mockSyncCounts(t, svc, 1, 1)
	if first != mockSyncReadView(t, svc) {
		t.Fatal("identical snapshot retry changed the existing read view")
	}
}

func TestKiwoomMockSyncProviderFailureRetainsKnownGood(t *testing.T) {
	for _, scenario := range []string{"401", "429", "503", "timeout", "partial"} {
		t.Run(scenario, func(t *testing.T) {
			svc, path := testService(t, nil, nil)
			transport, _ := mockSyncTLS(t, "9876543210", nil)
			if err := mockSyncRun(context.Background(), path, io.Discard, transport, "9876543210"); err != nil {
				t.Fatal(err)
			}
			before := mockSyncReadView(t, svc)
			failed, calls := mockSyncTLS(t, "9876543210", func(w http.ResponseWriter, r *http.Request) bool {
				if r.Header.Get("api-id") != "kt00018" || r.Header.Get("next-key") == "" {
					return false
				}
				switch scenario {
				case "timeout":
					<-r.Context().Done()
				case "partial":
					w.Header().Set("api-id", "kt00018")
					io.WriteString(w, `{"return_code":0,"acnt_evlt_remn_indv_tot":[]}`)
				default:
					code := map[string]int{"401": 401, "429": 429, "503": 503}[scenario]
					w.WriteHeader(code)
					io.WriteString(w, "synthetic-private-provider-error 9876543210")
				}
				return true
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if scenario == "timeout" {
				ctx, cancel = context.WithTimeout(ctx, 150*time.Millisecond)
				defer cancel()
			}
			var out bytes.Buffer
			err := mockSyncRun(ctx, path, &out, failed, "9876543210")
			if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "9876543210") || strings.Contains(err.Error(), "synthetic-private") {
				t.Fatalf("failed read wrote output or leaked private error: %v", err)
			}
			wantCalls := int32(4)
			if scenario == "401" {
				wantCalls = 6
			}
			if calls.Load() != wantCalls {
				t.Fatalf("unexpected retry count: requests=%d want=%d", calls.Load(), wantCalls)
			}
			mockSyncCounts(t, svc, 1, 1)
			if before != mockSyncReadView(t, svc) {
				t.Fatal("failed provider read replaced known-good view")
			}
		})
	}
}

func TestKiwoomMockSyncWrongAccountNeverWrites(t *testing.T) {
	svc, path := testService(t, nil, nil)
	transport, calls := mockSyncTLS(t, "1234567890", nil)
	err := mockSyncRun(context.Background(), path, io.Discard, transport, "9876543210")
	if err == nil || calls.Load() != 6 || strings.Contains(err.Error(), "1234567890") {
		t.Fatalf("wrong account did not fail after complete fetch: requests=%d err=%v", calls.Load(), err)
	}
	mockSyncCounts(t, svc, 0, 0)
}

func TestKiwoomMockSyncConfiguredAccountMismatchBeforeNetwork(t *testing.T) {
	svc, path := testService(t, nil, nil)
	transport, calls := mockSyncTLS(t, "9876543210", nil)
	if err := mockSyncRun(context.Background(), path, io.Discard, transport, "9876543210"); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"account", "alias key"} {
		t.Run(scenario, func(t *testing.T) {
			read := func(context.Context) ([]byte, error) {
				if scenario == "account" {
					return mockSyncSecret("1234567890"), nil
				}
				return bytes.ReplaceAll(mockSyncSecret("9876543210"), []byte("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="), []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))), nil
			}
			err := runKiwoomMockSync(context.Background(), mockSyncArgs(path), io.Discard, read, transport, mockSyncNow, mockSyncPacer())
			if err == nil || calls.Load() != 6 {
				t.Fatalf("changed credential identity reached outbound auth: calls=%d err=%v", calls.Load(), err)
			}
			mockSyncCounts(t, svc, 1, 1)
		})
	}
}

func TestKiwoomMockSyncPreflightBeforeSecretsOrNetwork(t *testing.T) {
	for _, scenario := range []string{"missing", "schema", "ledger", "broker", "mixed history", "production history"} {
		t.Run(scenario, func(t *testing.T) {
			svc, path := testService(t, nil, nil)
			switch scenario {
			case "missing":
				path = filepath.Join(t.TempDir(), "never-created.db")
			case "schema":
				if _, err := svc.db.Exec(`DELETE FROM schema_migrations WHERE version=27`); err != nil {
					t.Fatal(err)
				}
			case "ledger":
				if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=1 WHERE singleton=1`); err != nil {
					t.Fatal(err)
				}
			case "broker":
				mockSyncOrphan(t, svc)
			case "mixed history", "production history":
				snapshot := g4hSnapshot("2026-01-10T15:00:59Z")
				if scenario == "production history" {
					snapshot.Environment = KiwoomProduction
				}
				if _, err := svc.recordKiwoomSnapshot(context.Background(), "account-main", snapshot); err != nil {
					t.Fatal(err)
				}
				if scenario == "mixed history" {
					snapshot.AccountRef = "kiwoom_account_BBBBBBBBBBBBBBBBBBBBBBBB"
					if _, err := svc.recordKiwoomSnapshot(context.Background(), "account-main", snapshot); err != nil {
						t.Fatal(err)
					}
				}
			}
			readSecret := func(context.Context) ([]byte, error) {
				t.Error("database preflight reached secret reader")
				return mockSyncSecret("9876543210"), nil
			}
			transport := kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("database preflight reached network")
				return nil, errors.New("unexpected network")
			})
			err := runKiwoomMockSync(context.Background(), mockSyncArgs(path), io.Discard, readSecret, transport, mockSyncNow, mockSyncPacer())
			if err == nil || strings.Contains(err.Error(), path) {
				t.Fatalf("preflight accepted corruption or disclosed path: %v", err)
			}
			if scenario == "missing" {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("sync created a missing database")
				}
			}
		})
	}
}

func TestKiwoomMockSyncRechecksHistoryInsideWriteTransaction(t *testing.T) {
	for _, scenario := range []string{"binding", "orphan", "tamper", "schema", "ledger", "missing broker guard", "weak broker guard", "missing ledger guard", "weak ledger guard"} {
		t.Run(scenario, func(t *testing.T) {
			svc, path := testService(t, nil, nil)
			var mutate sync.Once
			transport, calls := mockSyncTLS(t, "9876543210", func(w http.ResponseWriter, r *http.Request) bool {
				if r.Header.Get("api-id") != "ka10075" || r.Header.Get("next-key") == "" {
					return false
				}
				mutate.Do(func() {
					switch scenario {
					case "binding", "tamper":
						stored, err := svc.recordKiwoomSnapshot(context.Background(), "account-main", g4hSnapshot("2026-01-10T15:00:59Z"))
						if err != nil {
							t.Error(err)
							return
						}
						if scenario == "tamper" {
							if _, err := svc.db.Exec(`DROP TRIGGER broker_snapshot_reconciliations_no_update`); err != nil {
								t.Error(err)
							}
							if _, err := svc.db.Exec(`UPDATE broker_snapshot_reconciliations SET record_sha256='tampered' WHERE reconciliation_id=?`, stored.ReconciliationID); err != nil {
								t.Error(err)
							}
						}
					case "orphan":
						mockSyncOrphan(t, svc)
					case "schema":
						if _, err := svc.db.Exec(`DELETE FROM schema_migrations WHERE version=27`); err != nil {
							t.Error(err)
						}
					case "ledger":
						if _, err := svc.db.Exec(`UPDATE ledger_meta SET revision=1 WHERE singleton=1`); err != nil {
							t.Error(err)
						}
					default:
						mockSyncMutateSchema(t, svc, scenario)
					}
				})
				return false
			})
			err := mockSyncRun(context.Background(), path, io.Discard, transport, "9876543210")
			if err == nil || calls.Load() != 6 {
				t.Fatalf("interleaved history escaped write preflight: requests=%d err=%v", calls.Load(), err)
			}
			wantSnapshots, wantReconciliations := 0, 0
			if scenario == "binding" || scenario == "tamper" {
				wantSnapshots, wantReconciliations = 1, 1
			} else if scenario == "orphan" {
				wantSnapshots = 1
			}
			mockSyncCounts(t, svc, wantSnapshots, wantReconciliations)
		})
	}
}

func TestKiwoomMockSyncJournalSchemaBeforeSecrets(t *testing.T) {
	for _, scenario := range []string{"missing broker guard", "weak broker guard", "missing ledger guard", "weak ledger guard", "missing index", "unexpected trigger"} {
		t.Run(scenario, func(t *testing.T) {
			svc, path := testService(t, nil, nil)
			snapshot := g4hSnapshot("2026-01-10T15:00:59Z")
			client := newSyntheticKiwoomClient(t, KiwoomMock, nil)
			snapshot.AccountRef = client.alias("account", "9876543210")
			if _, err := svc.recordKiwoomSnapshot(context.Background(), "account-main", snapshot); err != nil {
				t.Fatal(err)
			}
			mockSyncMutateSchema(t, svc, scenario)
			readSecret := func(context.Context) ([]byte, error) {
				t.Error("unprotected clean journal reached secret reader")
				return mockSyncSecret("9876543210"), nil
			}
			transport := kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("unprotected clean journal reached network")
				return nil, errors.New("unexpected network")
			})
			if err := runKiwoomMockSync(context.Background(), mockSyncArgs(path), io.Discard, readSecret, transport, mockSyncNow, mockSyncPacer()); err == nil {
				t.Fatal("sync accepted physical journal drift")
			}
			mockSyncCounts(t, svc, 1, 1)
		})
	}
}

func TestKiwoomMockSyncJournalRejectsTemporaryShadows(t *testing.T) {
	for _, statement := range []string{
		`CREATE TEMP TABLE broker_snapshots (snapshot_id TEXT)`,
		`CREATE TEMP TRIGGER shadow_broker BEFORE INSERT ON main.broker_snapshots BEGIN SELECT 1; END`,
		`CREATE TEMP TABLE events (event_id TEXT)`,
	} {
		svc, _ := testService(t, nil, nil)
		if _, err := svc.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
		if err := preflightKiwoomMockSync(context.Background(), svc.db); err == nil {
			t.Fatal("preflight accepted temporary journal objects")
		}
	}
}

func TestKiwoomMockSyncConcurrentFirstAccountsKeepSingleBinding(t *testing.T) {
	svc, path := testService(t, nil, nil)
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	defer close(release)
	finish := func(_ http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("api-id") == "ka10075" && r.Header.Get("next-key") != "" {
			ready <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		return false
	}
	first, _ := mockSyncTLS(t, "9876543210", finish)
	second, _ := mockSyncTLS(t, "1234567890", finish)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- mockSyncRun(ctx, path, io.Discard, first, "9876543210") }()
	go func() { results <- mockSyncRun(ctx, path, io.Discard, second, "1234567890") }()
	for i := 0; i < 2; i++ {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("both first syncs did not finish fetching")
		}
	}
	release <- struct{}{}
	release <- struct{}{}
	passed := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			passed++
		}
	}
	if passed != 1 {
		t.Fatalf("different-account first syncs committed %d bindings", passed)
	}
	mockSyncCounts(t, svc, 1, 1)
	if _, err := proveBrokerRecovery(context.Background(), svc.db); err != nil {
		t.Fatal(err)
	}
}

func TestKiwoomMockSyncLedgerImportDuringFetchUsesCurrentRevision(t *testing.T) {
	svc, path := testService(t, nil, nil)
	seedKRXLedger(t, svc)
	var imported sync.Once
	transport, _ := mockSyncTLS(t, "9876543210", func(_ http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("api-id") == "ka10075" {
			imported.Do(func() {
				applyTestCSV(t, svc, "during-mock-read", testCSV("deposit-more,account-main,DEPOSIT,2026-01-03T00:00:00Z,,,,,KRW,3000"))
			})
		}
		return false
	})
	var out bytes.Buffer
	if err := mockSyncRun(context.Background(), path, &out, transport, "9876543210"); err != nil {
		t.Fatal(err)
	}
	view := mockSyncReadView(t, svc)
	if !strings.Contains(view, `"ledger_revision":"rev_0000000003"`) || !strings.Contains(view, `"broker_quantity":"10","ledger_quantity":"7","difference":"3","match":false`) {
		t.Fatalf("fetch-time import was lost or reconciled against old revision: %s", view)
	}
}

func TestKiwoomMockSyncWriterFailureAfterCommit(t *testing.T) {
	svc, path := testService(t, nil, nil)
	transport, _ := mockSyncTLS(t, "9876543210", nil)
	want := errors.New("owned writer failure")
	err := mockSyncRun(context.Background(), path, mockSyncWriter(func([]byte) (int, error) { return 0, want }), transport, "9876543210")
	if err != want {
		t.Fatalf("writer error identity lost: %v", err)
	}
	mockSyncCounts(t, svc, 1, 1)
	view := mockSyncReadView(t, svc)
	if err := mockSyncRun(context.Background(), path, io.Discard, transport, "9876543210"); err != nil {
		t.Fatal(err)
	}
	mockSyncCounts(t, svc, 1, 1)
	if view != mockSyncReadView(t, svc) {
		t.Fatal("retry after writer failure changed durable snapshot")
	}
	for _, table := range []string{"events", "execution_authority_events", "order_events"} {
		var count int
		if err := svc.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("read-only sync mutated %s: count=%d err=%v", table, count, err)
		}
	}
}

func TestKiwoomMockSyncPacesOAuthRefreshPaginationAndInvocations(t *testing.T) {
	_, path := testService(t, nil, nil)
	var unauthorized atomic.Bool
	transport, _ := mockSyncTLS(t, "9876543210", func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("api-id") == "ka00001" && unauthorized.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusUnauthorized)
			return true
		}
		return false
	})
	clock := mockSyncNow()
	var starts []time.Time
	pacer := newKiwoomMockPacer(func() time.Time { return clock }, func(ctx context.Context, delay time.Duration) error {
		clock = clock.Add(delay)
		return ctx.Err()
	})
	base := kiwoomRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		starts = append(starts, clock)
		clock = clock.Add(200 * time.Millisecond)
		return transport.RoundTrip(r)
	})
	for i := 0; i < 2; i++ {
		err := runKiwoomMockSync(context.Background(), mockSyncArgs(path), io.Discard, func(context.Context) ([]byte, error) {
			return mockSyncSecret("9876543210"), nil
		}, base, mockSyncNow, pacer)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(starts) != 14 {
		t.Fatalf("OAuth refresh and paginated two invocations made %d requests", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < time.Second {
			t.Fatal("requests exceeded process-local pacing")
		}
	}
}

func TestKiwoomMockSyncTransportAllowlistAndCancellation(t *testing.T) {
	var calls atomic.Int32
	transport := &kiwoomMockTransport{base: kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return kiwoomResponse(200, nil, "{}"), nil
	}), pacer: mockSyncPacer()}
	for _, target := range []string{"https://api.kiwoom.com/oauth2/token", "http://mockapi.kiwoom.com/oauth2/token", "https://mockapi.kiwoom.com/api/dostk/ordr", "https://mockapi.kiwoom.com/oauth2/token?secret=private"} {
		req, _ := http.NewRequest(http.MethodPost, target, nil)
		if _, err := transport.RoundTrip(req); err == nil {
			t.Fatal("transport accepted an unapproved endpoint")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, kiwoomMockBase+kiwoomTokenPath, nil)
	if _, err := transport.RoundTrip(req); !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("cancelled/invalid requests reached network: calls=%d err=%v", calls.Load(), err)
	}
	pacer := newKiwoomMockPacer(time.Now, waitKiwoomMockRequest)
	pacer.gate <- struct{}{}
	transport.pacer = pacer
	if _, err := transport.RoundTrip(req); !errors.Is(err, context.Canceled) {
		t.Fatalf("pacer lock ignored context cancellation: %v", err)
	}
	<-pacer.gate
	pacer.next = time.Now().Add(time.Hour)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, kiwoomMockBase+kiwoomTokenPath, nil)
	if _, err := transport.RoundTrip(req); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 0 || len(pacer.gate) != 0 {
		t.Fatalf("pacer wait leaked gate or network request: %v", err)
	}
}

func mockSyncTLS(t *testing.T, account string, intercept func(http.ResponseWriter, *http.Request) bool) (http.RoundTripper, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("api-id") != "" {
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
		}
		if intercept != nil && intercept(w, r) {
			return
		}
		api := r.Header.Get("api-id")
		w.Header().Set("api-id", api)
		switch api {
		case "":
			var payload map[string]string
			if r.URL.Path != kiwoomTokenPath || json.NewDecoder(r.Body).Decode(&payload) != nil || !reflect.DeepEqual(payload, map[string]string{
				"grant_type": "client_credentials", "appkey": "synthetic-app-key", "secretkey": "synthetic-secret-key",
			}) {
				t.Error("invalid synthetic OAuth request")
			}
			io.WriteString(w, syntheticTokenJSON("synthetic-token"))
		case "ka00001":
			io.WriteString(w, `{"acctNo":"`+account+`","return_code":0}`)
		case "kt00018":
			symbol, quantity := "000660", "2"
			if r.Header.Get("next-key") == "" {
				w.Header().Set("cont-yn", "Y")
				w.Header().Set("next-key", "evaluation-page-2")
			} else {
				if r.Header.Get("next-key") != "evaluation-page-2" || r.Header.Get("cont-yn") != "Y" {
					t.Error("invalid synthetic evaluation pagination")
				}
				symbol, quantity = "005930", "10"
			}
			io.WriteString(w, syntheticEvaluationJSON(`[{"stk_cd":"`+symbol+`","stk_nm":"synthetic-name","evltv_prft":"1","prft_rt":"1","pur_pric":"10","rmnd_qty":"`+quantity+`","trde_able_qty":"`+quantity+`","cur_prc":"11","pur_amt":"10","evlt_amt":"11","poss_rt":"50"}]`))
		case "ka10075":
			if r.Header.Get("next-key") == "" {
				w.Header().Set("cont-yn", "Y")
				w.Header().Set("next-key", "orders-page-2")
				io.WriteString(w, `{"oso":[{"acnt_no":"`+account+`","ord_no":"0000042","stk_cd":"005930","stk_nm":"synthetic-name","ord_stt":"접수","ord_qty":"2","ord_pric":"10","oso_qty":"1","io_tp_nm":"매수","tm":"101530","stex_tp":"1"}],"return_code":0}`)
			} else {
				if r.Header.Get("next-key") != "orders-page-2" || r.Header.Get("cont-yn") != "Y" {
					t.Error("invalid synthetic open-order pagination")
				}
				io.WriteString(w, `{"oso":[],"return_code":0}`)
			}
		default:
			t.Error("unexpected synthetic API")
			w.WriteHeader(400)
		}
		if api != "" && (r.URL.Path != kiwoomAccountPath || r.Header.Get("Authorization") != "Bearer synthetic-token") {
			t.Error("invalid synthetic authorized account request")
		}
	}))
	t.Cleanup(server.Close)
	base := server.Client().Transport.(*http.Transport)
	t.Cleanup(base.CloseIdleConnections)
	local, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return kiwoomRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "mockapi.kiwoom.com" || r.Method != http.MethodPost ||
			(r.URL.Path != kiwoomTokenPath && r.URL.Path != kiwoomAccountPath) {
			t.Error("synthetic transport rejected request outside fixed mock allowlist")
			return nil, errors.New("synthetic endpoint rejected")
		}
		request := r.Clone(r.Context())
		request.URL.Scheme, request.URL.Host, request.Host = local.Scheme, local.Host, local.Host
		return base.RoundTrip(request)
	}), calls
}

func mockSyncNow() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }

func mockSyncPacer() *kiwoomMockPacer {
	clock := mockSyncNow()
	return newKiwoomMockPacer(func() time.Time { return clock }, func(ctx context.Context, delay time.Duration) error {
		clock = clock.Add(delay)
		return ctx.Err()
	})
}

func mockSyncSecret(account string) []byte {
	raw, _ := json.Marshal(kiwoomMockCredentials{
		SchemaVersion: "kiwoom-mock-read.v1", AppKey: "synthetic-app-key", SecretKey: "synthetic-secret-key",
		AliasKeyBase64: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), AccountNumber: account,
	})
	return raw
}

func mockSyncArgs(path string) []string {
	return []string{"-db", path, "-ledger-account", "account-main"}
}

func mockSyncRun(ctx context.Context, path string, out io.Writer, transport http.RoundTripper, account string) error {
	return runKiwoomMockSync(ctx, mockSyncArgs(path), out, func(context.Context) ([]byte, error) {
		return mockSyncSecret(account), nil
	}, transport, mockSyncNow, mockSyncPacer())
}

func mockSyncCounts(t *testing.T, svc *Service, snapshots, reconciliations int) {
	t.Helper()
	var a, b int
	if err := svc.db.QueryRow(`SELECT (SELECT COUNT(*) FROM broker_snapshots),(SELECT COUNT(*) FROM broker_snapshot_reconciliations)`).Scan(&a, &b); err != nil || a != snapshots || b != reconciliations {
		t.Fatalf("journal counts=(%d,%d) want=(%d,%d) err=%v", a, b, snapshots, reconciliations, err)
	}
}

func mockSyncReadView(t *testing.T, svc *Service) string {
	t.Helper()
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/broker-reconciliation/latest", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("existing read view status=%d body=%s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

func mockSyncOrphan(t *testing.T, svc *Service) {
	t.Helper()
	snapshot := g4hSnapshot("2026-01-10T15:00:59Z")
	raw, sha, err := orderJSONHash(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO broker_snapshots(snapshot_id,provider,environment,exchange,account_ref,fetched_at,snapshot_sha256,snapshot_json,recorded_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		"broker_snapshot_orphan", "kiwoom", snapshot.Environment, snapshot.Exchange, snapshot.AccountRef, snapshot.FetchedAt, sha, string(raw), "2026-01-10T15:01:01Z"); err != nil {
		t.Fatal(err)
	}
}

func mockSyncMutateSchema(t *testing.T, svc *Service, scenario string) {
	t.Helper()
	statements := map[string][]string{
		"missing broker guard": {`DROP TRIGGER broker_snapshots_no_delete`},
		"weak broker guard": {
			`DROP TRIGGER broker_snapshots_no_delete`,
			`CREATE TRIGGER broker_snapshots_no_delete BEFORE DELETE ON broker_snapshots WHEN 0 BEGIN SELECT RAISE(ABORT,'broker_snapshots is insert-only'); END`,
		},
		"missing ledger guard": {`DROP TRIGGER events_no_update`},
		"weak ledger guard": {
			`DROP TRIGGER events_no_update`,
			`CREATE TRIGGER events_no_update BEFORE UPDATE ON events WHEN 0 BEGIN SELECT RAISE(ABORT,'events is insert-only'); END`,
		},
		"missing index":      {`DROP INDEX broker_snapshots_latest_idx`},
		"unexpected trigger": {`CREATE TRIGGER unexpected_broker BEFORE INSERT ON broker_snapshots BEGIN SELECT 1; END`},
	}
	for _, statement := range statements[scenario] {
		if _, err := svc.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

type mockSyncWriter func([]byte) (int, error)

func (w mockSyncWriter) Write(p []byte) (int, error) { return w(p) }
