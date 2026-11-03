package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestKiwoomMockCandlesDispatchRequiresExplicitArguments(t *testing.T) {
	for _, args := range [][]string{
		{}, {"-symbol", "005930", "-interval", "1d"},
		{"-symbol", "005930", "-interval", "1d", "-out", "unused.json", "-base-date", "20261103"},
	} {
		err := runContext(context.Background(), append([]string{"kiwoom-mock-candles"}, args...))
		if err == nil || !strings.Contains(err.Error(), "requires only -symbol, -interval and -out") {
			t.Fatalf("explicit candle command argument validation: %v", err)
		}
	}
}

const captureDailyRow = `{"dt":"20260824","cur_prc":"-001,250.00","open_pric":"+001200","high_pric":"1300","low_pric":"-1150","trde_qty":"+001000"}`

func TestKiwoomMockCandlesLocalTLSRetainsExactRequestRawTimeAndFetchClocks(t *testing.T) {
	for _, interval := range []string{"1d", "5m"} {
		t.Run(interval, func(t *testing.T) {
			start := time.Date(2030, 1, 1, 14, 59, 59, 123, time.UTC)
			end := time.Date(2030, 1, 1, 15, 0, 0, 456, time.UTC)
			var finished atomic.Bool
			requests := make(chan kiwoomCandleRequestBody, 2)
			transport, calls := candleCaptureTLS(t, func(w http.ResponseWriter, r *http.Request) {
				var body kiwoomCandleRequestBody
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				requests <- body
				field, row := "stk_dt_pole_chart_qry", captureDailyRow
				if interval != "1d" {
					field = "stk_min_pole_chart_qry"
					row = strings.Replace(captureDailyRow, `"dt":"20260824"`, `"cntr_tm":"20260824103000"`, 1)
				}
				if r.Header.Get("next-key") == "" {
					w.Header().Set("cont-yn", "Y")
					w.Header().Set("next-key", "private-page-cursor")
				} else if r.Header.Get("next-key") != "private-page-cursor" || r.Header.Get("cont-yn") != "Y" {
					t.Error("continuation request lost the provider cursor")
				}
				finished.Store(true)
				fmt.Fprintf(w, `{"stk_cd":"005930","%s":[%s],"return_code":0,"return_msg":"private-provider-message"}`, field, row)
			})
			now := func() time.Time {
				if finished.Load() {
					return end
				}
				return start
			}
			path := filepath.Join(t.TempDir(), "capture.json")
			rawSecret := mockSyncSecret("9876543210")
			var output bytes.Buffer
			err := runKiwoomMockCandles(context.Background(), captureArgs(path, interval), &output, func(context.Context) ([]byte, error) {
				return rawSecret, nil
			}, transport, now, mockSyncPacer())
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range rawSecret {
				if value != 0 {
					t.Fatal("raw credential buffer was retained")
				}
			}
			capture, raw := readCapture(t, path)
			if calls.Load() != 3 || capture.SchemaVersion != "kiwoom-candle-capture.v1" || capture.Scope != "research_observation" || capture.Environment != KiwoomMock || capture.Provider != "kiwoom" || capture.Venue != "XKRX" || capture.Currency != "KRW" || capture.Symbol != "005930" || capture.Interval != interval {
				t.Fatalf("capture metadata or page count differs: %+v calls=%d", capture, calls.Load())
			}
			apiID, body, _ := kiwoomCandleRequest("005930", interval, "20300101")
			if capture.Request.APIID != apiID || capture.Request.Body != body || <-requests != capture.Request.Body || <-requests != capture.Request.Body {
				t.Fatal("serialized request differs from the requests sent across KST midnight")
			}
			if capture.FetchStartedAt != start.Format(time.RFC3339Nano) || capture.FetchedAt != end.Format(time.RFC3339Nano) {
				t.Fatal("capture clocks were substituted or rounded")
			}
			field, format, providerTime := "dt", "YYYYMMDD", "20260824"
			if interval != "1d" {
				field, format, providerTime = "cntr_tm", "YYYYMMDDHHmmss", "20260824103000"
			}
			if capture.TimeEvidence != (kiwoomCandleTimeEvidence{field, format, "not_provided", "not_provided", "not_provided", "unverified"}) || capture.Bounds != (kiwoomCandleCaptureBounds{500, 32, "latest_up_to_limit_after_overlap_validation"}) {
				t.Fatal("missing source semantics or bounded selection was upgraded to a guarantee")
			}
			if len(capture.Bars) != 1 || capture.Bars[0] != (kiwoomCapturedCandle{ProviderTime: providerTime, Open: "1200", High: "1300", Low: "1150", Close: "1250", Volume: "1000"}) {
				t.Fatalf("raw time/decimal pairing or overlap deduplication differs: %+v", capture.Bars)
			}
			assertCaptureReceipt(t, output.Bytes(), raw, 1)
			for _, forbidden := range []string{"9876543210", "synthetic-app-key", "synthetic-secret-key", "synthetic-token", "private-page-cursor", "private-provider-message", `"at"`, `"source_available_at"`, `"close_at"`, `"open_at"`, `"instrument_id"`, `"price_adjustment"`, `"account`, `"alias`, `"cursor"`} {
				if bytes.Contains(append(append([]byte{}, raw...), output.Bytes()...), []byte(forbidden)) {
					t.Fatalf("capture or receipt exposed %q", forbidden)
				}
			}
		})
	}
}

func TestKiwoomMockCandlesRefusesInputAndTargetsBeforeSecrets(t *testing.T) {
	for _, scenario := range []string{"symbol", "interval", "extra", "host", "existing", "symlink", "missing-parent", "shared-parent", "shared-ancestor", "cancelled", "nil-context"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "capture.json")
			args := captureArgs(path, "1d")
			ctx := context.Background()
			switch scenario {
			case "symbol":
				args[1] = "A005930"
			case "interval":
				args[3] = "2m"
			case "extra":
				args = append(args, "extra")
			case "host":
				args = append(args, "-host", "https://api.kiwoom.com")
			case "existing":
				if err := os.WriteFile(path, []byte("existing-private-data"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(dir, "untouched"), path); err != nil {
					t.Fatal(err)
				}
			case "missing-parent":
				args[5] = filepath.Join(dir, "missing", "capture.json")
			case "shared-parent":
				if err := os.Chmod(dir, 0777); err != nil {
					t.Fatal(err)
				}
			case "shared-ancestor":
				leaf := filepath.Join(dir, "leaf")
				if err := os.Mkdir(leaf, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(dir, 0777); err != nil {
					t.Fatal(err)
				}
				args[5] = filepath.Join(leaf, "capture.json")
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			case "nil-context":
				ctx = nil
			}
			err := runKiwoomMockCandles(ctx, args, io.Discard, func(context.Context) ([]byte, error) {
				t.Fatal("invalid capture reached credential read")
				return nil, nil
			}, kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid capture reached network")
				return nil, nil
			}), mockSyncNow, mockSyncPacer())
			if err == nil {
				t.Fatal("invalid capture was accepted")
			}
			if scenario == "existing" {
				raw, readErr := os.ReadFile(path)
				if readErr != nil || string(raw) != "existing-private-data" {
					t.Fatal("existing output changed")
				}
			} else if scenario == "symlink" {
				if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("existing symlink changed")
				}
			} else {
				assertCaptureAbsent(t, args[5])
			}
		})
	}
}

func TestKiwoomMockCandlesClockReversalDoesNotChangeLegacyCandles(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			var fetched atomic.Bool
			transport, _ := candleCaptureTLS(t, func(w http.ResponseWriter, r *http.Request) {
				fetched.Store(true)
				io.WriteString(w, candleDailyBody(captureDailyRow))
			})
			now := func() time.Time {
				if fetched.Load() {
					return mockSyncNow().Add(-time.Second)
				}
				return mockSyncNow()
			}
			if legacy {
				client := newSyntheticKiwoomClient(t, KiwoomMock, transport)
				client.now = now
				series, err := client.Candles(context.Background(), "005930", "1d")
				if err != nil || len(series.Bars) != 1 || series.Bars[0].At != "2026-08-23T15:00:00Z" || series.PriceAdjustment != marketDataAdjustmentProviderAdjusted {
					t.Fatalf("capture chronology guard changed the existing Candles contract: %+v %v", series, err)
				}
				return
			}
			path := filepath.Join(t.TempDir(), "reversed.json")
			if err := captureRun(context.Background(), path, "1d", io.Discard, transport, now, mockSyncPacer()); err == nil {
				t.Fatal("reversed capture clock was published")
			}
			assertCaptureAbsent(t, path)
		})
	}
}

func TestKiwoomMockCandlesRawWireFormatDoesNotChangeLegacyCandles(t *testing.T) {
	for _, fraction := range []string{".123", ",123"} {
		for _, legacy := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/legacy=%t", fraction, legacy), func(t *testing.T) {
				rawTime := "20260824103000" + fraction
				transport, _ := candleCaptureTLS(t, func(w http.ResponseWriter, r *http.Request) {
					row := strings.Replace(captureDailyRow, `"dt":"20260824"`, `"cntr_tm":"`+rawTime+`"`, 1)
					fmt.Fprintf(w, `{"stk_cd":"005930","stk_min_pole_chart_qry":[%s],"return_code":0}`, row)
				})
				if legacy {
					client := newSyntheticKiwoomClient(t, KiwoomMock, transport)
					series, err := client.Candles(context.Background(), "005930", "1m")
					if err != nil || len(series.Bars) != 1 || series.Bars[0].At != "2026-08-24T01:30:00Z" {
						t.Fatalf("legacy fractional-time behavior changed: %+v %v", series, err)
					}
					return
				}
				path := filepath.Join(t.TempDir(), "fraction.json")
				err := captureRun(context.Background(), path, "1m", io.Discard, transport, mockSyncNow, mockSyncPacer())
				if err == nil {
					capture, _ := readCapture(t, path)
					t.Fatalf("capture published provider_time=%s with declared %s", capture.Bars[0].ProviderTime, capture.TimeEvidence.ProviderFormat)
				}
				assertCaptureAbsent(t, path)
			})
		}
	}
}

func TestKiwoomMockCandlesProviderFailuresNeverPublishOrLeak(t *testing.T) {
	for _, scenario := range []string{"401", "429", "503", "malformed", "symbol", "conflict", "page-limit", "timeout", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			var pages atomic.Int32
			release := make(chan struct{})
			defer close(release)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			transport, _ := candleCaptureTLS(t, func(w http.ResponseWriter, r *http.Request) {
				page := pages.Add(1)
				switch scenario {
				case "401", "429", "503":
					code := map[string]int{"401": 401, "429": 429, "503": 503}[scenario]
					w.WriteHeader(code)
					io.WriteString(w, "private-error 9876543210 synthetic-token")
				case "malformed":
					io.WriteString(w, `{"return_code":0,"stk_cd":"005930","stk_dt_pole_chart_qry":[{"dt":"20260229"}]}`)
				case "symbol":
					io.WriteString(w, strings.Replace(candleDailyBody(captureDailyRow), "005930", "000660", 1))
				case "conflict":
					if page == 1 {
						w.Header().Set("cont-yn", "Y")
						w.Header().Set("next-key", "private-cursor")
						io.WriteString(w, candleDailyBody(captureDailyRow))
					} else {
						io.WriteString(w, candleDailyBody(strings.Replace(captureDailyRow, "250.00", "260.00", 1)))
					}
				case "page-limit":
					w.Header().Set("cont-yn", "Y")
					w.Header().Set("next-key", fmt.Sprint(page))
					io.WriteString(w, candleDailyBody(captureDailyRow))
				case "timeout":
					select {
					case <-r.Context().Done():
					case <-release:
					}
				case "cancelled":
					cancel()
				}
			})
			if scenario == "timeout" {
				short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
				defer stop()
				ctx = short
			}
			path := filepath.Join(t.TempDir(), "failed.json")
			var output bytes.Buffer
			err := captureRun(ctx, path, "1d", &output, transport, mockSyncNow, mockSyncPacer())
			if err == nil || output.Len() != 0 {
				t.Fatalf("partial read published or succeeded: %v", err)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "9876543210") || strings.Contains(err.Error(), "synthetic-token") {
				t.Fatalf("provider error escaped: %v", err)
			}
			wantPages := int32(1)
			if scenario == "401" || scenario == "conflict" {
				wantPages = 2
			} else if scenario == "page-limit" {
				wantPages = kiwoomMaxPages
			}
			if pages.Load() != wantPages {
				t.Fatalf("unexpected retries/pages=%d want=%d", pages.Load(), wantPages)
			}
			if scenario == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity was lost")
			}
			assertCaptureAbsent(t, path)
		})
	}
}

func TestKiwoomMockCandlesInvalidSecretsNeverReachNetwork(t *testing.T) {
	for _, readFailure := range []bool{false, true} {
		raw := []byte(`{"schema_version":"kiwoom-mock-read.v1","app_key":"private"}`)
		path := filepath.Join(t.TempDir(), "failed.json")
		err := runKiwoomMockCandles(context.Background(), captureArgs(path, "1d"), io.Discard, func(context.Context) ([]byte, error) {
			if readFailure {
				return raw, errors.New("private-secret-error")
			}
			return raw, nil
		}, kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid credentials reached network")
			return nil, nil
		}), mockSyncNow, mockSyncPacer())
		if err == nil || strings.Contains(err.Error(), "private") || !bytes.Equal(raw, make([]byte, len(raw))) {
			t.Fatalf("invalid secret was accepted, disclosed or retained: %v", err)
		}
		assertCaptureAbsent(t, path)
	}
}

func TestKiwoomMockCandlesTransportKeepsSeparateCapabilities(t *testing.T) {
	var calls atomic.Int32
	base := kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return kiwoomResponse(200, nil, "{}"), nil
	})
	chart := &kiwoomMockCandleTransport{base, mockSyncPacer()}
	for _, request := range []struct{ method, target, api string }{
		{http.MethodGet, kiwoomMockBase + kiwoomChartPath, "ka10081"},
		{http.MethodPost, kiwoomProductionBase + kiwoomTokenPath, ""},
		{http.MethodPost, "http://mockapi.kiwoom.com" + kiwoomChartPath, "ka10081"},
		{http.MethodPost, kiwoomMockBase + kiwoomAccountPath, "ka00001"},
		{http.MethodPost, kiwoomMockBase + kiwoomAccountPath, "kt00018"},
		{http.MethodPost, kiwoomMockBase + kiwoomAccountPath, "ka10075"},
		{http.MethodPost, kiwoomMockBase + kiwoomOrderPath, "kt10000"},
		{http.MethodPost, kiwoomMockBase + kiwoomChartPath, "ka10079"},
		{http.MethodPost, kiwoomMockBase + kiwoomChartPath + "?private=1", "ka10081"},
		{http.MethodPost, kiwoomMockBase + kiwoomChartPath + "?", "ka10081"},
		{http.MethodPost, "https://private@mockapi.kiwoom.com" + kiwoomChartPath, "ka10081"},
		{http.MethodPost, kiwoomMockBase + kiwoomTokenPath, "ka10081"},
	} {
		r, _ := http.NewRequest(request.method, request.target, nil)
		r.Header.Set("api-id", request.api)
		if _, err := chart.RoundTrip(r); err == nil {
			t.Fatalf("chart transport allowed %s %s %s", request.method, request.target, request.api)
		}
	}
	account := &kiwoomMockTransport{base, mockSyncPacer()}
	for _, api := range []string{"ka10080", "ka10081"} {
		r, _ := http.NewRequest(http.MethodPost, kiwoomMockBase+kiwoomChartPath, nil)
		r.Header.Set("api-id", api)
		if _, err := account.RoundTrip(r); err == nil {
			t.Fatal("existing account caller acquired chart authority")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("disallowed request reached base transport")
	}
}

func TestKiwoomMockCandlesPacesOAuthRefreshPagesAndAccountCaller(t *testing.T) {
	var unauthorized atomic.Bool
	transport, _ := candleCaptureTLS(t, func(w http.ResponseWriter, r *http.Request) {
		if unauthorized.CompareAndSwap(false, true) {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("next-key") == "" {
			w.Header().Set("cont-yn", "Y")
			w.Header().Set("next-key", "page-two")
		}
		io.WriteString(w, candleDailyBody(captureDailyRow))
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
		if err := captureRun(context.Background(), filepath.Join(t.TempDir(), "capture.json"), "1d", io.Discard, base, mockSyncNow, pacer); err != nil {
			t.Fatal(err)
		}
	}
	account := &kiwoomMockTransport{base, pacer}
	r, _ := http.NewRequest(http.MethodPost, kiwoomMockBase+kiwoomTokenPath, strings.NewReader(`{"grant_type":"client_credentials","appkey":"synthetic-app-key","secretkey":"synthetic-secret-key"}`))
	response, err := account.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(starts) != 9 {
		t.Fatalf("refresh/pages/two commands/account OAuth calls=%d", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < time.Second {
			t.Fatal("mock commands did not share process-local pacing")
		}
	}
}

func TestKiwoomMockCandlesConcurrentTargetAndReceiptFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.json")
	ready, release := make(chan struct{}, 2), make(chan struct{})
	results := make(chan error, 2)
	transport := kiwoomRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == kiwoomTokenPath {
			return kiwoomResponse(200, nil, syntheticTokenJSON("synthetic-token")), nil
		}
		return kiwoomResponse(200, map[string]string{"api-id": "ka10081"}, candleDailyBody(captureDailyRow)), nil
	})
	pacer := mockSyncPacer()
	for i := 0; i < 2; i++ {
		go func() {
			results <- runKiwoomMockCandles(context.Background(), captureArgs(path, "1d"), io.Discard, func(context.Context) ([]byte, error) {
				ready <- struct{}{}
				<-release
				return mockSyncSecret("9876543210"), nil
			}, transport, mockSyncNow, pacer)
		}()
	}
	<-ready
	<-ready
	close(release)
	winners := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("same-target winners=%d", winners)
	}
	readCapture(t, path)
	assertCaptureTempsAbsent(t, filepath.Dir(path))
	path = filepath.Join(t.TempDir(), "receipt-failed.json")
	err := captureRun(context.Background(), path, "1d", ledgerExportFailWriter{errors.New("private-receipt-error")}, transport, mockSyncNow, mockSyncPacer())
	if err == nil || strings.Contains(err.Error(), "private-receipt-error") || !strings.Contains(err.Error(), "completed capture may remain") {
		t.Fatalf("post-publication failure was hidden or disclosed: %v", err)
	}
	readCapture(t, path)
	assertCaptureTempsAbsent(t, filepath.Dir(path))
}

func TestKiwoomMockCandlesCaptureByteLimit(t *testing.T) {
	capture := kiwoomCandleCapture{}
	raw, err := json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	capture.Symbol = strings.Repeat("A", maxKiwoomCandleCaptureBytes-len(raw)-1)
	path := filepath.Join(t.TempDir(), "exact-bound.json")
	if _, err := publishKiwoomCandleCapture(context.Background(), path, capture); err != nil {
		t.Fatalf("exact encoded byte bound rejected: %v", err)
	}
	if raw, err := os.ReadFile(path); err != nil || len(raw) != maxKiwoomCandleCaptureBytes {
		t.Fatal("exact bound did not include the published newline")
	}
	capture.Symbol += "A"
	path = filepath.Join(t.TempDir(), "over-bound.json")
	if _, err := publishKiwoomCandleCapture(context.Background(), path, capture); err == nil {
		t.Fatal("oversized encoded capture was published")
	}
	assertCaptureAbsent(t, path)
}

func TestKiwoomCandleBatchKeepsRawTimesPairedAfterLatestBarCap(t *testing.T) {
	newest := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]kiwoomCandleRow, maxMarketDataRows+1)
	for i := range rows {
		price := fmt.Sprint(i + 1)
		rows[i] = kiwoomCandleRow{Date: newest.AddDate(0, 0, -i).Format("20060102"), Open: price, High: price, Low: price, Close: price, Volume: "1"}
	}
	raw, err := json.Marshal(kiwoomCandleResponse{kiwoomResult: kiwoomResult{ReturnCode: new(int)}, StockCode: "005930", DailyBars: rows})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := onePageCandleClient(t, string(raw)).readCandleBatch(context.Background(), "005930", "1d")
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.bars) != maxMarketDataRows {
		t.Fatalf("batch rows=%d", len(batch.bars))
	}
	for i, bar := range batch.bars {
		row := rows[maxMarketDataRows-1-i]
		if bar.ProviderTime != row.Date || bar.Close != row.Close {
			t.Fatal("latest bounded selection detached raw time from its values")
		}
	}
}

func TestKiwoomMockCandlesMainLocalTLSAndSignals(t *testing.T) {
	for _, mode := range []string{"success", "interrupt", "terminate"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.json")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKiwoomMockCandlesMainHelper$")
			cmd.Env = append(os.Environ(), "OMNI_TEST_CANDLE_MAIN="+mode, "OMNI_TEST_CANDLE_OUT="+path)
			var output bytes.Buffer
			cmd.Stdout = &output
			if mode == "success" {
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				if err := cmd.Run(); err != nil {
					t.Fatalf("controlled actual main failed: %v %s", err, stderr.Bytes())
				}
				_, raw := readCapture(t, path)
				fixture, err := os.ReadFile("../../contracts/fixtures/kiwoom-candle-capture.json")
				if err != nil {
					t.Fatal(err)
				}
				var actual, expected any
				if json.Unmarshal(raw, &actual) != nil || json.Unmarshal(fixture, &expected) != nil || !reflect.DeepEqual(actual, expected) {
					t.Fatal("actual main capture differs from the versioned closed JSON fixture")
				}
				line, _, _ := bytes.Cut(output.Bytes(), []byte("\n"))
				assertCaptureReceipt(t, line, raw, 1)
				return
			}
			stderr, err := cmd.StderrPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(stderr)
			if !scanner.Scan() || scanner.Text() != "candle-main-awaiting-signal" {
				cmd.Process.Kill()
				cmd.Wait()
				t.Fatal("actual main never reached the cancellable reader")
			}
			signal := os.Interrupt
			if mode == "terminate" {
				signal = syscall.SIGTERM
			}
			if err := cmd.Process.Signal(signal); err != nil {
				cmd.Process.Kill()
				cmd.Wait()
				t.Fatal(err)
			}
			var lines []string
			for scanner.Scan() {
				lines = append(lines, scanner.Text())
			}
			err = cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || ctx.Err() != nil || !strings.Contains(strings.Join(lines, "\n"), "context canceled") || output.Len() != 0 {
				t.Fatalf("actual main did not cancel normally after %s: %v %v", mode, err, lines)
			}
			assertCaptureAbsent(t, path)
		})
	}
}

func TestKiwoomMockCandlesMainHelper(t *testing.T) {
	mode := os.Getenv("OMNI_TEST_CANDLE_MAIN")
	if mode == "" {
		return
	}
	closed := false
	if mode == "success" {
		transport, _ := candleCaptureTLS(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, candleDailyBody(captureDailyRow))
		})
		kiwoomMockCandleRuntimeFactory = func() kiwoomMockCandleRuntime {
			return kiwoomMockCandleRuntime{
				readSecret: func(context.Context) ([]byte, error) { return mockSyncSecret("9876543210"), nil },
				transport:  transport, now: mockSyncNow, pacer: mockSyncPacer(), close: func() { closed = true },
			}
		}
	} else {
		kiwoomMockCandleRuntimeFactory = func() kiwoomMockCandleRuntime {
			return kiwoomMockCandleRuntime{
				readSecret: func(ctx context.Context) ([]byte, error) {
					fmt.Fprintln(os.Stderr, "candle-main-awaiting-signal")
					<-ctx.Done()
					return nil, ctx.Err()
				},
				transport: kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) { panic("signal test reached network") }),
				now:       mockSyncNow, pacer: mockSyncPacer(), close: func() { closed = true },
			}
		}
	}
	os.Args = append([]string{"omni-core", "kiwoom-mock-candles"}, captureArgs(os.Getenv("OMNI_TEST_CANDLE_OUT"), "1d")...)
	main()
	if !closed {
		t.Fatal("command runtime was not closed")
	}
}

func candleCaptureTLS(t *testing.T, chart http.HandlerFunc) (http.RoundTripper, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		api := r.Header.Get("api-id")
		if r.Method != http.MethodPost {
			t.Error("unexpected provider method")
		}
		if r.URL.Path == kiwoomTokenPath && api == "" {
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || !reflect.DeepEqual(body, map[string]string{"grant_type": "client_credentials", "appkey": "synthetic-app-key", "secretkey": "synthetic-secret-key"}) {
				t.Error("synthetic OAuth body differs")
			}
			io.WriteString(w, strings.Replace(syntheticTokenJSON("synthetic-token"), "20300102000000", "20310102000000", 1))
			return
		}
		if r.URL.Path != kiwoomChartPath || (api != "ka10080" && api != "ka10081") || r.Header.Get("Authorization") != "Bearer synthetic-token" {
			t.Error("unexpected chart capability or authorization")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("api-id", api)
		chart(w, r)
	}))
	t.Cleanup(server.Close)
	base := server.Client().Transport.(*http.Transport)
	t.Cleanup(base.CloseIdleConnections)
	local, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return kiwoomRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "mockapi.kiwoom.com" {
			return nil, errors.New("test transport refuses non-mock origin")
		}
		request := r.Clone(r.Context())
		request.URL.Scheme, request.URL.Host, request.Host = local.Scheme, local.Host, local.Host
		return base.RoundTrip(request)
	}), calls
}

func captureArgs(path, interval string) []string {
	return []string{"-symbol", "005930", "-interval", interval, "-out", path}
}

func captureRun(ctx context.Context, path, interval string, out io.Writer, transport http.RoundTripper, now func() time.Time, pacer *kiwoomMockPacer) error {
	return runKiwoomMockCandles(ctx, captureArgs(path, interval), out, func(context.Context) ([]byte, error) {
		return mockSyncSecret("9876543210"), nil
	}, transport, now, pacer)
}

func readCapture(t *testing.T, path string) (kiwoomCandleCapture, []byte) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var capture kiwoomCandleCapture
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&capture) != nil || decoder.Decode(new(any)) != io.EOF {
		t.Fatal("published capture is not closed typed JSON")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("capture output is not private mode 0600")
	}
	return capture, raw
}

func assertCaptureReceipt(t *testing.T, output, raw []byte, rows int) {
	t.Helper()
	var receipt kiwoomCandleCaptureReceipt
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil || decoder.Decode(new(any)) != io.EOF {
		t.Fatalf("receipt is not closed JSON: %s %v", output, err)
	}
	digest := sha256.Sum256(raw)
	if receipt.SchemaVersion != "kiwoom-candle-capture-receipt.v1" || receipt.Rows != rows || receipt.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("receipt does not identify exact published bytes")
	}
}

func assertCaptureAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed capture published a final file: %v", err)
	}
	assertCaptureTempsAbsent(t, filepath.Dir(path))
}

func assertCaptureTempsAbsent(t *testing.T, directory string) {
	t.Helper()
	temporary, err := filepath.Glob(filepath.Join(directory, ".omni-kiwoom-candles-*"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("owned capture temp remains: %v %v", temporary, err)
	}
}
