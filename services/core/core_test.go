package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGoldenVerticalSliceAndBackupRestore(t *testing.T) {
	svc, dbPath := testService(t,
		[]time.Time{
			mustTime("2026-01-10T15:00:30Z"),
			mustTime("2026-01-10T15:01:00Z"),
			mustTime("2026-01-10T15:01:00.125Z"),
		},
		map[string][]string{
			"event":   {"event_cash_001", "event_trade_001", "event_trade_002"},
			"preview": {"preview_golden_001"},
			"receipt": {"receipt_golden_001"},
		},
	)
	csvBody := fixture(t, "golden-import.csv")
	preview, appErr := svc.preview(context.Background(), csvBody)
	if appErr != nil {
		t.Fatal(appErr)
	}
	assertJSONFixture(t, preview, "golden-preview.json")

	var req ApplyRequest
	decodeFixture(t, "golden-apply-request.json", &req)
	receipt, appErr := svc.apply(context.Background(), req)
	if appErr != nil {
		t.Fatal(appErr)
	}
	assertJSONFixture(t, receipt, "golden-apply.json")
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONFixture(t, snapshot, "golden-snapshot.json")

	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := backupAndVerify(svc.db, backup, fixturePath("golden-snapshot.json")); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(backup, fixturePath("golden-snapshot.json"), backup+".manifest.json"); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(backup, fixturePath("golden-snapshot.json"), fixturePath("invalid-backup-manifest.json")); err == nil {
		t.Fatal("rejected backup manifest was accepted")
	}
	if err := svc.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRestore(backup, fixturePath("golden-snapshot.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatal("backup changed the active database:", err)
	}
}

func TestInvalidAndDuplicatePreviewMatchesFixture(t *testing.T) {
	svc, _ := testService(t,
		[]time.Time{
			mustTime("2026-01-10T15:00:30Z"), mustTime("2026-01-10T15:01:00Z"), mustTime("2026-01-10T15:01:00.125Z"),
			mustTime("2026-01-10T15:02:00Z"),
		},
		map[string][]string{
			"event":   {"event_cash_001", "event_trade_001", "event_trade_002", "unused_duplicate", "unused_invalid"},
			"preview": {"preview_golden_001", "preview_edge_001"},
			"receipt": {"receipt_golden_001"},
		},
	)
	golden, appErr := svc.preview(context.Background(), fixture(t, "golden-import.csv"))
	if appErr != nil {
		t.Fatal(appErr)
	}
	_, appErr = svc.apply(context.Background(), ApplyRequest{golden.PreviewID, "apply_golden_001"})
	if appErr != nil {
		t.Fatal(appErr)
	}
	edge, appErr := svc.preview(context.Background(), fixture(t, "edge-import.csv"))
	if appErr != nil {
		t.Fatal(appErr)
	}
	assertJSONFixture(t, edge, "edge-preview.json")
	if _, appErr := svc.apply(context.Background(), ApplyRequest{edge.PreviewID, "edge"}); appErr == nil || appErr.status != http.StatusBadRequest || appErr.body.Code != "preview_has_errors" {
		t.Fatalf("expected preview_has_errors, got %#v", appErr)
	}
}

func TestIdempotencyReplayConflictAndStalePreview(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	ctx := context.Background()
	first, appErr := svc.preview(ctx, []byte(testCSV("d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,10")))
	if appErr != nil {
		t.Fatal(appErr)
	}
	req := ApplyRequest{first.PreviewID, "same-key"}
	receipt, appErr := svc.apply(ctx, req)
	if appErr != nil {
		t.Fatal(appErr)
	}
	replay, appErr := svc.apply(ctx, req)
	if appErr != nil || !reflect.DeepEqual(receipt, replay) {
		t.Fatalf("replay differs: %#v %#v %v", receipt, replay, appErr)
	}
	other, appErr := svc.preview(ctx, []byte(testCSV("d2,account-main,DEPOSIT,2026-01-02T00:00:00Z,,,,,USD,5")))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(ctx, ApplyRequest{other.PreviewID, "same-key"}); appErr == nil || appErr.status != http.StatusConflict || appErr.body.Code != "idempotency_conflict" {
		t.Fatalf("expected idempotency conflict, got %#v", appErr)
	}

	stale, appErr := svc.preview(ctx, []byte(testCSV("d3,account-main,DEPOSIT,2026-01-03T00:00:00Z,,,,,USD,5")))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(ctx, ApplyRequest{other.PreviewID, "other-key"}); appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(ctx, ApplyRequest{stale.PreviewID, "stale-key"}); appErr == nil || appErr.status != http.StatusConflict || appErr.body.Code != "stale_preview" {
		t.Fatalf("expected stale preview, got %#v", appErr)
	}

	expired, appErr := svc.preview(ctx, []byte(testCSV("d4,account-main,DEPOSIT,2026-01-04T00:00:00Z,,,,,USD,5")))
	if appErr != nil {
		t.Fatal(appErr)
	}
	svc.ttl = -time.Second
	if _, err := svc.db.Exec(`UPDATE previews SET expires_at=? WHERE preview_id=?`, zeroTime, expired.PreviewID); err != nil {
		t.Fatal(err)
	}
	if _, appErr := svc.apply(ctx, ApplyRequest{expired.PreviewID, "expired-key"}); appErr == nil || appErr.status != http.StatusConflict || appErr.body.Code != "stale_preview" {
		t.Fatalf("expected expired stale preview, got %#v", appErr)
	}
}

func TestApplyRollbackHasNoPartialMutation(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	preview, appErr := svc.preview(context.Background(), []byte(testCSV(
		"d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,100",
		"s1,account-main,SELL,2026-01-02T00:00:00Z,AAPL,1,20,1,USD,19",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(context.Background(), ApplyRequest{preview.PreviewID, "rollback"}); appErr == nil || appErr.body.Code != "invalid_ledger" {
		t.Fatalf("expected invalid_ledger, got %#v", appErr)
	}
	var events, revision int
	if err := svc.db.QueryRow(`SELECT count(*) FROM events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT revision FROM ledger_meta`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if events != 0 || revision != 0 {
		t.Fatalf("partial mutation: events=%d revision=%d", events, revision)
	}
}

func TestFIFOAllocatesBuyAndSellFeesExactly(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	preview, appErr := svc.preview(context.Background(), []byte(testCSV(
		"d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,1000",
		"b1,account-main,BUY,2026-01-02T00:00:00Z,AAPL,3,10,3,USD,-33",
		"b2,account-main,BUY,2026-01-03T00:00:00Z,AAPL,2,20,2,USD,-42",
		"s1,account-main,SELL,2026-01-04T00:00:00Z,AAPL,4,30,4,USD,116",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if _, appErr := svc.apply(context.Background(), ApplyRequest{preview.PreviewID, "fifo"}); appErr != nil {
		t.Fatal(appErr)
	}
	snapshot, err := snapshotFrom(context.Background(), svc.db)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Cash, []Money{{"USD", "1041"}}) ||
		!reflect.DeepEqual(snapshot.Holdings, []Holding{{"instrument_aapl", "AAPL", "1", "21", "USD"}}) ||
		!reflect.DeepEqual(snapshot.RealizedPnL, []Money{{"USD", "62"}}) {
		t.Fatalf("unexpected FIFO snapshot: %+v", snapshot)
	}
}

func TestHTTPBodyAndStructuredErrors(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	r := httptest.NewRequest(http.MethodPost, "/v1/imports/apply", strings.NewReader(`{"preview_id":"x","idempotency_key":"y","extra":true}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("financial API response was cacheable: %v", w.Header())
	}
	var apiErr APIError
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil || apiErr.Code != "invalid_json" || apiErr.Message == "" {
		t.Fatalf("unstructured error: %s (%v)", w.Body.String(), err)
	}
}

func TestInternalErrorsAreLoggedButNotExposed(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	if _, err := svc.db.Exec(`DROP TABLE ledger_meta`); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousOutput) })

	r := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var apiErr APIError
	if err := json.Unmarshal(w.Body.Bytes(), &apiErr); err != nil {
		t.Fatal(err)
	}
	if apiErr != (APIError{Code: "internal_error", Message: "internal server error"}) {
		t.Fatalf("unstable internal response: %+v", apiErr)
	}
	if strings.Contains(w.Body.String(), "ledger_meta") || strings.Contains(w.Body.String(), "no such table") {
		t.Fatalf("internal details leaked: %s", w.Body.String())
	}
	if !strings.Contains(logs.String(), "ledger_meta") {
		t.Fatalf("internal cause was not logged: %s", logs.String())
	}
}

func TestHealthAndReadinessAreSeparate(t *testing.T) {
	svc, _ := testService(t, nil, nil)

	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	if w := request("/healthz"); w.Code != http.StatusOK || w.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("health status=%d body=%s", w.Code, w.Body.String())
	}
	if w := request("/readyz"); w.Code != http.StatusOK || w.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("ready status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := svc.db.Exec(`DELETE FROM schema_migrations WHERE version=1`); err != nil {
		t.Fatal(err)
	}
	if w := request("/readyz"); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"code":"not_ready"`) {
		t.Fatalf("readiness accepted an incomplete migration history: status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := svc.db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(1, ?)`, "2026-01-10T15:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(5, ?)`, "2026-01-10T15:01:00Z"); err != nil {
		t.Fatal(err)
	}
	if w := request("/readyz"); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"code":"not_ready"`) {
		t.Fatalf("unready status=%d body=%s", w.Code, w.Body.String())
	}
	if w := request("/healthz"); w.Code != http.StatusOK {
		t.Fatalf("process health depended on schema: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCORSAllowsOnlyConfiguredExactOrigin(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	const allowed = "http://localhost:7357"
	handler := withCORS(svc.routes(), allowed)

	request := func(method, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/v1/imports/apply", nil)
		r.Header.Set("Origin", origin)
		if method == http.MethodOptions {
			r.Header.Set("Access-Control-Request-Method", http.MethodPost)
			r.Header.Set("Access-Control-Request-Headers", "content-type")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	preflight := request(http.MethodOptions, allowed)
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Origin") != allowed ||
		preflight.Header().Get("Access-Control-Allow-Methods") != "GET, POST, OPTIONS" ||
		preflight.Header().Get("Access-Control-Allow-Headers") != "Content-Type" {
		t.Fatalf("allowed preflight failed: status=%d headers=%v", preflight.Code, preflight.Header())
	}
	allowedRequest := request(http.MethodPost, allowed)
	if allowedRequest.Header().Get("Access-Control-Allow-Origin") != allowed {
		t.Fatalf("configured origin omitted: %v", allowedRequest.Header())
	}
	for _, denied := range []string{"http://localhost:7357/", "http://127.0.0.1:7357", "https://evil.example"} {
		response := request(http.MethodOptions, denied)
		if response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("origin %q was allowed: %v", denied, response.Header())
		}
	}
	defaultHandler := withCORS(svc.routes(), "")
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Header.Set("Origin", allowed)
	w := httptest.NewRecorder()
	defaultHandler.ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("default widened CORS access: %v", w.Header())
	}
}

func TestAllowedOriginValidation(t *testing.T) {
	for _, valid := range []string{"", "http://localhost:7357", "https://app.example"} {
		if err := validateAllowedOrigin(valid); err != nil {
			t.Fatalf("valid origin %q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{"*", "localhost:7357", "ftp://localhost", "http://user@localhost", "http://localhost/path", "http://localhost?x=1"} {
		if err := validateAllowedOrigin(invalid); err == nil {
			t.Fatalf("invalid origin %q accepted", invalid)
		}
	}
}

func TestStatusUnresolvedLimitsAndFingerprintBinding(t *testing.T) {
	svc, _ := testService(t, nil, nil)

	r := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"trust_state":"never_verified"`) || !strings.Contains(w.Body.String(), `"last_verified_at":null`) {
		t.Fatalf("unexpected first-launch status: %s", w.Body.String())
	}

	unresolvedPreview, appErr := svc.preview(context.Background(), []byte(testCSV(
		"u1,unknown-account,BUY,2026-01-01T00:00:00Z,MSFT,1,10,0,USD,-10",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if unresolvedPreview.CanApply || unresolvedPreview.Totals.UnresolvedRows != 1 || unresolvedPreview.Rows[0].Status != "unresolved" || unresolvedPreview.Rows[0].Resolution == nil {
		t.Fatalf("unresolved row did not fail closed: %+v", unresolvedPreview)
	}

	tooMany := make([]string, maxImportRows+1)
	for i := range tooMany {
		tooMany[i] = "x,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,1"
	}
	if _, appErr := svc.preview(context.Background(), []byte(testCSV(tooMany...))); appErr == nil || appErr.body.Code != "too_many_rows" {
		t.Fatalf("expected row cap, got %#v", appErr)
	}
	if _, appErr := svc.preview(context.Background(), bytes.Repeat([]byte("x"), maxBodyBytes+1)); appErr == nil || appErr.body.Code != "csv_too_large" {
		t.Fatalf("expected byte cap, got %#v", appErr)
	}

	valid, appErr := svc.preview(context.Background(), []byte(testCSV(
		"d1,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,USD,10",
	)))
	if appErr != nil {
		t.Fatal(appErr)
	}
	if valid.PreviewFingerprint != previewFingerprint(valid.FileSHA256, csvSchema, mappingSchema, valid.LedgerRevision) {
		t.Fatal("preview fingerprint did not bind contract versions")
	}
	var stored string
	if err := svc.db.QueryRow(`SELECT preview_json FROM previews WHERE preview_id=?`, valid.PreviewID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	stored = strings.Replace(stored, `"mapping_version":"canonical-transaction.v1"`, `"mapping_version":"changed.v2"`, 1)
	if _, err := svc.db.Exec(`UPDATE previews SET preview_json=? WHERE preview_id=?`, stored, valid.PreviewID); err != nil {
		t.Fatal(err)
	}
	if _, appErr := svc.apply(context.Background(), ApplyRequest{valid.PreviewID, "tampered"}); appErr == nil || appErr.status != http.StatusConflict || appErr.body.Code != "stale_preview" {
		t.Fatalf("expected fingerprint conflict, got %#v", appErr)
	}
}

func testService(t *testing.T, times []time.Time, ids map[string][]string) (*Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "core.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	clockIndex := 0
	clock := func() time.Time {
		if len(times) == 0 {
			return mustTime("2026-01-10T15:01:00Z").Add(time.Duration(clockIndex) * time.Millisecond)
		}
		if clockIndex >= len(times) {
			return times[len(times)-1].Add(time.Duration(clockIndex-len(times)+1) * time.Millisecond)
		}
		v := times[clockIndex]
		clockIndex++
		return v
	}
	counters := map[string]int{}
	id := func(prefix string) string {
		i := counters[prefix]
		counters[prefix]++
		if i < len(ids[prefix]) {
			return ids[prefix][i]
		}
		return prefix + "_test_" + time.Unix(0, int64(i)).Format("150405.000000000")
	}
	return newService(db, clock, id), path
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(fixturePath(name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixturePath(name string) string { return filepath.Join("..", "..", "contracts", "fixtures", name) }

func decodeFixture(t *testing.T, name string, dst any) {
	t.Helper()
	if err := json.Unmarshal(fixture(t, name), dst); err != nil {
		t.Fatal(err)
	}
}

func assertJSONFixture(t *testing.T, actual any, name string) {
	t.Helper()
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var actualValue, expectedValue any
	if err := json.Unmarshal(actualJSON, &actualValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixture(t, name), &expectedValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actualValue, expectedValue) {
		prettyActual := &bytes.Buffer{}
		_ = json.Indent(prettyActual, actualJSON, "", "  ")
		t.Fatalf("does not match %s\nactual: %s", name, prettyActual)
	}
}

func testCSV(rows ...string) string {
	return "source_event_id,account_id,type,occurred_at,symbol,quantity,price,fee,currency,amount\n" + strings.Join(rows, "\n") + "\n"
}

func mustTime(raw string) time.Time {
	v, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		panic(err)
	}
	return v
}
