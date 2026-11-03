package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxKiwoomCandleCaptureBytes = 1 << 20

type kiwoomCandleTimeEvidence struct {
	ProviderField    string `json:"provider_field"`
	ProviderFormat   string `json:"provider_format"`
	ProviderTimezone string `json:"provider_timezone"`
	BarBoundaries    string `json:"bar_boundaries"`
	PublicationTime  string `json:"publication_time"`
	BarCompletion    string `json:"bar_completion"`
}

type kiwoomCandleCaptureBounds struct {
	BarsLimit  int    `json:"bars_limit"`
	PagesLimit int    `json:"pages_limit"`
	Selection  string `json:"selection"`
}

type kiwoomCandleCapture struct {
	SchemaVersion  string                      `json:"schema_version"`
	Scope          string                      `json:"scope"`
	Provider       string                      `json:"provider"`
	Environment    KiwoomEnvironment           `json:"environment"`
	Venue          string                      `json:"venue"`
	Currency       string                      `json:"currency"`
	Symbol         string                      `json:"symbol"`
	Interval       string                      `json:"interval"`
	Request        kiwoomCandleRequestEvidence `json:"request"`
	FetchStartedAt string                      `json:"fetch_started_at"`
	FetchedAt      string                      `json:"fetched_at"`
	TimeEvidence   kiwoomCandleTimeEvidence    `json:"time_evidence"`
	Bounds         kiwoomCandleCaptureBounds   `json:"bounds"`
	Bars           []kiwoomCapturedCandle      `json:"bars"`
}

type kiwoomCandleCaptureReceipt struct {
	SchemaVersion string `json:"schema_version"`
	Rows          int    `json:"rows"`
	SHA256        string `json:"sha256"`
}

type kiwoomMockCandleRuntime struct {
	readSecret func(context.Context) ([]byte, error)
	transport  http.RoundTripper
	now        func() time.Time
	pacer      *kiwoomMockPacer
	close      func()
}

var kiwoomMockCandleRuntimeFactory = func() kiwoomMockCandleRuntime {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return kiwoomMockCandleRuntime{readKiwoomMockSecret, transport, time.Now, kiwoomMockProcessPacer, transport.CloseIdleConnections}
}

func runKiwoomMockCandlesCommand(ctx context.Context, args []string, out io.Writer) error {
	runtime := kiwoomMockCandleRuntimeFactory()
	defer runtime.close()
	return runKiwoomMockCandles(ctx, args, out, runtime.readSecret, runtime.transport, runtime.now, runtime.pacer)
}

func runKiwoomMockCandles(ctx context.Context, args []string, out io.Writer, readSecret func(context.Context) ([]byte, error), transport http.RoundTripper, now func() time.Time, pacer *kiwoomMockPacer) error {
	if ctx == nil {
		return errors.New("kiwoom mock candle context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, kiwoomMockSyncTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	fs := flag.NewFlagSet("kiwoom-mock-candles", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	symbol := fs.String("symbol", "", "six-digit KRX stock code")
	interval := fs.String("interval", "", "daily or supported minute interval")
	path := fs.String("out", "", "new private research JSON")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *symbol == "" || *interval == "" || *path == "" {
		return errors.New("kiwoom-mock-candles requires only -symbol, -interval and -out")
	}
	if !kiwoomStockPattern.MatchString(*symbol) {
		return errors.New("kiwoom mock candle symbol is invalid")
	}
	if apiID, _, _ := kiwoomCandleRequest(*symbol, *interval, ""); apiID == "" {
		return errors.New("kiwoom mock candle interval is unsupported")
	}
	invalid := errors.New("kiwoom mock candle capture failed; existing files were not overwritten; a completed capture may remain after publication")
	if _, _, err := privateFileTarget(*path); err != nil {
		return invalid
	}
	raw, err := readSecret(ctx)
	if err != nil {
		clear(raw)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("kiwoom mock candle Keychain record is unavailable")
	}
	credentials, aliasKey, err := decodeKiwoomMockCredentials(raw)
	clear(raw)
	if err != nil {
		return err
	}
	defer clear(aliasKey)
	client, err := NewKiwoomClient(KiwoomMock, credentials.AppKey, credentials.SecretKey, aliasKey)
	if err != nil {
		return invalid
	}
	defer clear(client.aliasKey)
	client.transport = &kiwoomMockCandleTransport{base: transport, pacer: pacer}
	client.now = now
	batch, err := client.readCandleBatch(ctx, *symbol, *interval)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return invalid
	}
	if batch.fetchedAt.Before(batch.fetchStartedAt) {
		return invalid
	}
	timeEvidence := kiwoomCandleTimeEvidence{
		ProviderField: "dt", ProviderFormat: "YYYYMMDD", ProviderTimezone: "not_provided",
		BarBoundaries: "not_provided", PublicationTime: "not_provided", BarCompletion: "unverified",
	}
	rawTimeDigits := 8
	if *interval != "1d" {
		timeEvidence.ProviderField, timeEvidence.ProviderFormat = "cntr_tm", "YYYYMMDDHHmmss"
		rawTimeDigits = 14
	}
	for _, bar := range batch.bars {
		if len(bar.ProviderTime) != rawTimeDigits || strings.Trim(bar.ProviderTime, "0123456789") != "" {
			return invalid
		}
	}
	capture := kiwoomCandleCapture{
		SchemaVersion: "kiwoom-candle-capture.v1", Scope: "research_observation", Provider: "kiwoom", Environment: KiwoomMock,
		Venue: "XKRX", Currency: "KRW", Symbol: *symbol, Interval: *interval, Request: batch.request,
		FetchStartedAt: batch.fetchStartedAt.Format(time.RFC3339Nano), FetchedAt: batch.fetchedAt.Format(time.RFC3339Nano),
		TimeEvidence: timeEvidence, Bounds: kiwoomCandleCaptureBounds{maxMarketDataRows, kiwoomMaxPages, "latest_up_to_limit_after_overlap_validation"}, Bars: batch.bars,
	}
	digest, err := publishKiwoomCandleCapture(ctx, *path, capture)
	if err != nil {
		return errors.Join(ctx.Err(), invalid)
	}
	if err := json.NewEncoder(out).Encode(kiwoomCandleCaptureReceipt{SchemaVersion: "kiwoom-candle-capture-receipt.v1", Rows: len(batch.bars), SHA256: digest}); err != nil {
		return invalid
	}
	return nil
}

func publishKiwoomCandleCapture(ctx context.Context, out string, capture kiwoomCandleCapture) (string, error) {
	parent, target, err := privateFileTarget(out)
	if err != nil {
		return "", err
	}
	return publishPrivateFile(ctx, parent, target, ".omni-kiwoom-candles-*", func(output io.Writer) error {
		raw, err := json.Marshal(capture)
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		if len(raw) > maxKiwoomCandleCaptureBytes {
			return errors.New("kiwoom candle JSON exceeds the capture size limit")
		}
		n, err := output.Write(raw)
		if err == nil && n != len(raw) {
			err = io.ErrShortWrite
		}
		return err
	})
}

type kiwoomMockCandleTransport struct {
	base  http.RoundTripper
	pacer *kiwoomMockPacer
}

func (t *kiwoomMockCandleTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodPost || request.URL.Scheme != "https" || request.URL.Host != "mockapi.kiwoom.com" ||
		request.URL.RawQuery != "" || request.URL.ForceQuery || request.URL.User != nil ||
		!((request.URL.Path == kiwoomTokenPath && request.Header.Get("api-id") == "") ||
			(request.URL.Path == kiwoomChartPath && (request.Header.Get("api-id") == "ka10080" || request.Header.Get("api-id") == "ka10081"))) {
		return nil, &KiwoomError{Kind: "invalid_request"}
	}
	return t.pacer.roundTrip(request, t.base)
}
