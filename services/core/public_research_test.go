package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPublicResearchNotConfigured(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/research/feed?market=US&symbol=AAPL", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "research_not_configured") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("research contract: %d %s", w.Code, w.Body.String())
	}
}

func TestPublicResearchHTTPJSONEscapingPreservesLargeValidArtifact(t *testing.T) {
	var feed map[string]any
	_ = json.Unmarshal(researchFixture(t), &feed)
	original := feed["items"].([]any)[0]
	rawOriginal, _ := json.Marshal(original)
	items := make([]any, 100)
	for i := range items {
		var item map[string]any
		_ = json.Unmarshal(rawOriginal, &item)
		item["id"] = fmt.Sprintf("%064x", i)
		item["reasons"] = []string{strings.Repeat("&", 2048)}
		items[i] = item
	}
	feed["items"] = items
	raw, _ := json.Marshal(feed)
	// The artifact budget counts UTF-8 bytes before the transport escapes HTML.
	raw = []byte(strings.ReplaceAll(string(raw), `\u0026`, "&"))
	if len(raw) >= publicResearchMaxBytes {
		t.Fatalf("test artifact over budget: %d", len(raw))
	}
	svc, _ := testService(t, nil, nil)
	svc.researchFeed = filepath.Join(t.TempDir(), "feed.json")
	if err := os.WriteFile(svc.researchFeed, raw, 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/research/feed?market=US&symbol=AAPL", nil))
	if w.Code != 200 || w.Body.Len() <= publicResearchMaxBytes+4096 {
		t.Fatalf("escaped response: %d bytes=%d", w.Code, w.Body.Len())
	}
	var view PublicResearchView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || len(view.Items) != 100 {
		t.Fatalf("lost response items: %v", err)
	}
	var first map[string]any
	_ = json.Unmarshal(view.Items[0], &first)
	if first["reasons"].([]any)[0] != strings.Repeat("&", 2048) {
		t.Fatal("transport changed research text")
	}
}

func researchFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../contracts/fixtures/public-research.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPublicResearchExactIdentityEmptyAndNoWrites(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.researchFeed = filepath.Join(t.TempDir(), "feed.json")
	raw := researchFixture(t)
	var feed map[string]any
	if err := json.Unmarshal(raw, &feed); err != nil {
		t.Fatal(err)
	}
	items := feed["items"].([]any)
	var extra map[string]any
	copyRaw, _ := json.Marshal(items[0])
	_ = json.Unmarshal(copyRaw, &extra)
	extra["market"], extra["id"] = "KR", strings.Repeat("a", 64)
	feed["items"] = append(items, extra)
	raw, _ = json.Marshal(feed)
	if err := os.WriteFile(svc.researchFeed, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := svc.db.QueryRow("SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		market, symbol string
		count          int
	}{{"US", "AAPL", 3}, {"KR", "AAPL", 1}, {"US", "MSFT", 0}} {
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/research/feed?market="+tc.market+"&symbol="+tc.symbol, nil))
		var body PublicResearchView
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Items) != tc.count || body.Market != tc.market || body.Symbol != tc.symbol || body.SchemaVersion != "public-research-view.v1" || !body.Sample {
			t.Fatalf("identity view: %d %s", w.Code, w.Body.String())
		}
		for _, field := range []string{"idempotency_key", "account_id", "holding", "data_root", "permissions", "basis"} {
			if strings.Contains(w.Body.String(), `"`+field+`"`) {
				t.Fatalf("private field %s exposed", field)
			}
		}
	}
	if err := svc.db.QueryRow("SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatalf("research wrote DB: %d -> %d %v", before, after, err)
	}
}

func TestPublicResearchQueryAndFileFailureBoundary(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	svc.researchFeed = filepath.Join(t.TempDir(), "private-path.json")
	for _, path := range []string{
		"", "?market=US", "?market=GLOBAL&symbol=AAPL", "?market=US&symbol=aapl",
		"?market=US&symbol=AAPL&market=KR", "?market=US&symbol=AAPL&token=secret",
		"?market=US&symbol=AAPL%00", "?market=US;symbol=AAPL", "?market=%zz&symbol=AAPL",
	} {
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/research/feed"+path, nil))
		if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_query") {
			t.Fatalf("query %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	request := func(status int, code string) {
		t.Helper()
		w := httptest.NewRecorder()
		svc.routes().ServeHTTP(w, httptest.NewRequest("GET", "/v1/research/feed?market=US&symbol=AAPL", nil))
		if w.Code != status || !strings.Contains(w.Body.String(), code) || strings.Contains(w.Body.String(), "private-path") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("file boundary: %d %s", w.Code, w.Body.String())
		}
	}
	request(404, "research_feed_missing")
	if err := os.WriteFile(svc.researchFeed, []byte("{private-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	request(503, "research_feed_unavailable")
	if err := os.WriteFile(svc.researchFeed, make([]byte, publicResearchMaxBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	request(503, "research_feed_unavailable")
	if err := os.Remove(svc.researchFeed); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(svc.researchFeed, 0600); err != nil {
		t.Fatal(err)
	}
	request(503, "research_feed_unavailable")
	svc.researchFeed = t.TempDir()
	request(503, "research_feed_unavailable")
}

func TestPublicResearchClosedArtifactBoundary(t *testing.T) {
	valid := researchFixture(t)
	if _, err := decodePublicResearch(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"unknown root":    func(v map[string]any) { v["secret"] = "no" },
		"version":         func(v map[string]any) { v["schema_version"] = "v2" },
		"invalid date":    func(v map[string]any) { v["since"] = "2026-02-30" },
		"window":          func(v map[string]any) { v["since"] = "2026-09-01" },
		"future range":    func(v map[string]any) { v["until"] = "2026-11-04" },
		"omitted":         func(v map[string]any) { v["omitted"].(map[string]any)["unscoped_news"] = -1 },
		"null items":      func(v map[string]any) { v["items"] = nil },
		"duplicate id":    func(v map[string]any) { a := v["items"].([]any); v["items"] = append(a, a[0]) },
		"over item limit": func(v map[string]any) { v["items"] = make([]any, 501) },
	} {
		t.Run(name, func(t *testing.T) {
			var v map[string]any
			_ = json.Unmarshal(valid, &v)
			mutate(v)
			raw, _ := json.Marshal(v)
			if _, err := decodePublicResearch(raw); err == nil {
				t.Fatal("invalid artifact accepted")
			}
		})
	}
	for _, tc := range []struct {
		field string
		value any
	}{
		{"unknown", "private"}, {"symbol", "aapl"}, {"market", "GLOBAL"}, {"id", "bad"},
		{"occurred_at", "2026-11-03T00:00:00+00:00"}, {"captured_at", "2026-11-04T00:00:00Z"},
		{"sources", nil}, {"source", "private path"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			var v map[string]any
			_ = json.Unmarshal(valid, &v)
			v["items"].([]any)[0].(map[string]any)[tc.field] = tc.value
			raw, _ := json.Marshal(v)
			if _, err := decodePublicResearch(raw); err == nil {
				t.Fatal("invalid item accepted")
			}
		})
	}
	for _, raw := range [][]byte{
		append(append([]byte{}, valid...), []byte(" {}")...),
		[]byte(strings.Replace(string(valid), `"sample":true`, `"sample":true,"sample":true`, 1)),
		[]byte(strings.Replace(string(valid), `Synthetic filing`, `bad\ud800`, 1)),
		bytesWithInvalidUTF8(valid),
	} {
		if _, err := decodePublicResearch(raw); err == nil {
			t.Fatal("invalid JSON boundary accepted")
		}
	}
}

func bytesWithInvalidUTF8(raw []byte) []byte {
	return []byte(strings.Replace(string(raw), "Synthetic filing", "bad\xff", 1))
}

func TestPublicResearchArticleAndEvidenceBounds(t *testing.T) {
	valid := researchFixture(t)
	for _, tc := range []struct {
		field string
		value any
	}{
		{"url", "https://name:secret@example.test/"}, {"url", "https://example.test/?token=secret"},
		{"url", "javascript:alert(1)"}, {"title", strings.Repeat("한", 171)}, {"summary", "bad\x00"},
		{"title", 1}, {"association", "inferred"},
	} {
		var v map[string]any
		_ = json.Unmarshal(valid, &v)
		for _, item := range v["items"].([]any) {
			m := item.(map[string]any)
			if m["kind"] == "news" {
				m[tc.field] = tc.value
			}
		}
		raw, _ := json.Marshal(v)
		if _, err := decodePublicResearch(raw); err == nil {
			t.Fatalf("accepted article %s=%v", tc.field, tc.value)
		}
	}
	for _, tc := range []struct {
		field string
		value any
	}{
		{"stars", 0}, {"stars", 1.5}, {"confidence", 1.1}, {"reasons", nil},
		{"as_of", "2026-11-02"}, {"disclaimer", ""}, {"direction", "BUY"},
	} {
		var v map[string]any
		_ = json.Unmarshal(valid, &v)
		for _, item := range v["items"].([]any) {
			m := item.(map[string]any)
			if m["kind"] == "insight" {
				m[tc.field] = tc.value
			}
		}
		raw, _ := json.Marshal(v)
		if _, err := decodePublicResearch(raw); err == nil {
			t.Fatalf("accepted insight %s=%v", tc.field, tc.value)
		}
	}
	var v map[string]any
	_ = json.Unmarshal(valid, &v)
	for _, item := range v["items"].([]any) {
		m := item.(map[string]any)
		if m["kind"] == "filing" {
			m["title"], m["summary"] = nil, nil
		}
	}
	raw, _ := json.Marshal(v)
	if _, err := decodePublicResearch(raw); err != nil {
		t.Fatalf("nullable article rejected: %v", err)
	}
	if _, err := benchmarkObject([]byte(`{"title":null}`), "title"); err == nil {
		t.Fatal("existing closed-object caller unexpectedly accepts null")
	}
}

func TestPublicResearchAttributionAndClosedOpenAPI(t *testing.T) {
	valid := researchFixture(t)
	for _, tc := range []struct {
		field string
		value any
	}{
		{"publisher", ""}, {"target", "https://user:secret@example.test/feed"},
		{"source_url", "https://example.test/?api_key=secret"},
		{"rights_source_url", "file:///private/path"}, {"checked_on", "2026-02-30"},
		{"attribution", nil}, {"author", strings.Repeat("x", 2049)},
		{"terms_urls", []string{}}, {"terms_urls", []string{"https://example.test/?password=secret"}},
		{"permissions", []string{"publish"}},
	} {
		var v map[string]any
		_ = json.Unmarshal(valid, &v)
		for _, rawItem := range v["items"].([]any) {
			item := rawItem.(map[string]any)
			if item["kind"] == "insight" {
				item["sources"].([]any)[0].(map[string]any)[tc.field] = tc.value
			}
		}
		raw, _ := json.Marshal(v)
		if _, err := decodePublicResearch(raw); err == nil {
			t.Fatalf("accepted invalid evidence %s", tc.field)
		}
	}
	duplicate := strings.Replace(string(valid), `"publisher":"Synthetic publisher"`, `"publisher":"Synthetic publisher","publisher":"other"`, 1)
	if _, err := decodePublicResearch([]byte(duplicate)); err == nil {
		t.Fatal("nested duplicate key accepted")
	}
	raw, err := os.ReadFile("../../contracts/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	path := spec["paths"].(map[string]any)["/v1/research/feed"].(map[string]any)
	if len(path) != 1 || path["get"] == nil {
		t.Fatal("research endpoint is not GET-only")
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"PublicResearchView", "MimirPublicResearchFeed", "PublicResearchFiling", "PublicResearchNews", "PublicResearchInsight", "PublicResearchSourceMetadata", "PublicResearchUsageEvidence", "PublicResearchOmitted"} {
		schema := schemas[name].(map[string]any)
		properties := schema["properties"].(map[string]any)
		if schema["additionalProperties"] != false || len(schema["required"].([]any)) != len(properties) {
			t.Fatalf("open public shape: %s", name)
		}
	}
}
