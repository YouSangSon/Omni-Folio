package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const benchmarkSeriesTestInput = `{
 "schema_version":"benchmark-index-series.v1",
 "benchmark_id":"test_reference",
 "label":"Synthetic test reference",
 "currency":"KRW",
 "return_basis":"gross_total_return",
 "source_reference":"https://example.invalid/test-methodology",
 "source_version":"test-v1",
 "sample":true,
 "fetched_at":"2026-01-01T00:00:00Z",
 "points":[{"observed_at":"2025-01-01T00:00:00Z","level":"100"},{"observed_at":"2026-01-01T00:00:00Z","level":"110"}]
}`

func TestBenchmarkSeriesCLIImportsAnExplicitIndexVersion(t *testing.T) {
	svc, path := testService(t, nil, nil)
	input := filepath.Join(t.TempDir(), "index.json")
	if err := os.WriteFile(input, []byte(benchmarkSeriesTestInput), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runContext(context.Background(), []string{"benchmark-import", "-db", path, "-input", input}); err != nil {
		t.Fatalf("versioned benchmark admission unavailable: %v", err)
	}
	var count int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM benchmark_series_versions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one immutable version, got %d", count)
	}
}

func benchmarkInputForTest(t *testing.T, edit func(*BenchmarkSeriesInput)) []byte {
	t.Helper()
	var input BenchmarkSeriesInput
	if err := json.Unmarshal([]byte(benchmarkSeriesTestInput), &input); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(&input)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func importBenchmarkForTest(t *testing.T, svc *Service, raw []byte) *BenchmarkSeriesReceipt {
	t.Helper()
	receipt, err := svc.importBenchmarkSeries(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestBenchmarkSeriesClosedInput(t *testing.T) {
	for _, currency := range []string{"KRW", "USD"} {
		for _, basis := range []string{"price_index", "gross_total_return", "net_total_return"} {
			raw := benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.Currency, i.ReturnBasis = currency, basis })
			input, canonical, sha, err := decodeBenchmarkSeries(context.Background(), raw)
			if err != nil || input.Currency != currency || input.ReturnBasis != basis || len(canonical) == 0 || len(sha) != 64 {
				t.Fatalf("valid %s/%s rejected: %v", currency, basis, err)
			}
		}
	}
	replace := func(old, new string) string { return strings.Replace(benchmarkSeriesTestInput, old, new, 1) }
	cases := map[string]string{
		"unknown":               replace(`"sample":true`, `"extra":1,"sample":true`),
		"duplicate":             replace(`"sample":true`, `"sample":false,"sample":true`),
		"escaped duplicate":     replace(`"sample":true`, `"\u0073ample":false,"sample":true`),
		"missing":               replace(`"sample":true,`, ``),
		"null":                  replace(`"sample":true`, `"sample":null`),
		"wrong boolean":         replace(`"sample":true`, `"sample":"true"`),
		"unknown schema":        replace(benchmarkSeriesSchema, "benchmark-index-series.v2"),
		"currency":              replace(`"KRW"`, `"EUR"`),
		"basis":                 replace(`"gross_total_return"`, `"adjusted_close"`),
		"id whitespace":         replace(`"test_reference"`, `"test reference"`),
		"label whitespace":      replace(`Synthetic test reference`, ` Synthetic test reference`),
		"label control":         replace(`Synthetic test reference`, `Synthetic\nreference`),
		"source credentials":    replace(`https://example.invalid/test-methodology`, `https://secret:token@example.invalid/test`),
		"source query":          replace(`https://example.invalid/test-methodology`, `https://example.invalid/test?token=secret`),
		"source empty query":    replace(`https://example.invalid/test-methodology`, `https://example.invalid/test?`),
		"source fragment":       replace(`https://example.invalid/test-methodology`, `https://example.invalid/test#secret`),
		"source scheme":         replace(`https://example.invalid/test-methodology`, `file:///tmp/index.json`),
		"offset time":           replace(`2025-01-01T00:00:00Z`, `2025-01-01T09:00:00+09:00`),
		"noncanonical time":     replace(`2025-01-01T00:00:00Z`, `2025-01-01T00:00:00.0Z`),
		"future point":          replace(`2025-01-01T00:00:00Z`, `2027-01-01T00:00:00Z`),
		"duplicate time":        replace(`2025-01-01T00:00:00Z`, `2026-01-01T00:00:00Z`),
		"duplicate point field": replace(`"level":"100"`, `"level":"99","level":"100"`),
		"unknown point field":   replace(`"level":"100"`, `"level":"100","currency":"KRW"`),
		"null point":            replace(`"level":"100"`, `"level":null`),
		"number level":          replace(`"level":"100"`, `"level":100`),
		"negative level":        replace(`"level":"100"`, `"level":"-1"`),
		"noncanonical level":    replace(`"level":"100"`, `"level":"100.0"`),
		"exponent level":        replace(`"level":"100"`, `"level":"1e2"`),
		"unpaired surrogate":    replace(`Synthetic test reference`, `Synthetic\ud800reference`),
		"trailing":              benchmarkSeriesTestInput + `{}`,
		"invalid UTF8":          replace(`Synthetic test reference`, string([]byte{0xff})),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := decodeBenchmarkSeries(context.Background(), []byte(raw)); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	for _, field := range []string{"schema_version", "benchmark_id", "label", "currency", "return_basis", "source_reference", "source_version", "sample", "fetched_at", "points"} {
		var object map[string]any
		if err := json.Unmarshal([]byte(benchmarkSeriesTestInput), &object); err != nil {
			t.Fatal(err)
		}
		delete(object, field)
		raw, _ := json.Marshal(object)
		if _, _, _, err := decodeBenchmarkSeries(context.Background(), raw); err == nil {
			t.Fatalf("missing %s accepted", field)
		}
	}
}

func TestBenchmarkSeriesInputBoundsAndCancellation(t *testing.T) {
	for name, edit := range map[string]func(*BenchmarkSeriesInput){
		"no points":     func(i *BenchmarkSeriesInput) { i.Points = nil },
		"id bytes":      func(i *BenchmarkSeriesInput) { i.BenchmarkID = strings.Repeat("한", 43) },
		"label bytes":   func(i *BenchmarkSeriesInput) { i.Label = strings.Repeat("한", 54) },
		"version bytes": func(i *BenchmarkSeriesInput) { i.SourceVersion = strings.Repeat("v", 129) },
		"reference bytes": func(i *BenchmarkSeriesInput) {
			i.SourceReference = "https://example.invalid/" + strings.Repeat("x", 512)
		},
		"decimal bytes": func(i *BenchmarkSeriesInput) { i.Points[0].Level = strings.Repeat("9", 65) },
		"zero timestamp duplicate": func(i *BenchmarkSeriesInput) {
			i.Points[0].ObservedAt, i.Points[1].ObservedAt = "0001-01-01T00:00:00Z", "0001-01-01T00:00:00Z"
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := decodeBenchmarkSeries(context.Background(), benchmarkInputForTest(t, edit)); err == nil {
				t.Fatal("bound exceeded")
			}
		})
	}
	for _, count := range []int{10000, 10001} {
		raw := benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) {
			i.Points = make([]BenchmarkIndexPoint, count)
			for n := range i.Points {
				i.Points[n] = BenchmarkIndexPoint{time.Date(2025, 1, 1, 0, 0, n, 0, time.UTC).Format(time.RFC3339Nano), "0"}
			}
		})
		if _, _, _, err := decodeBenchmarkSeries(context.Background(), raw); (err == nil) != (count == 10000) {
			t.Fatalf("point bound %d: %v", count, err)
		}
	}
	oversized := append(bytes.Repeat([]byte(" "), maxBodyBytes), []byte(benchmarkSeriesTestInput)...)
	if _, _, _, err := decodeBenchmarkSeries(context.Background(), oversized); err == nil {
		t.Fatal("oversized source accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := decodeBenchmarkSeries(ctx, []byte(benchmarkSeriesTestInput)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestBenchmarkSeriesVersionsAreImmutableAndRetryDoesNotPromote(t *testing.T) {
	svc, _ := testService(t, nil, nil)
	first := importBenchmarkForTest(t, svc, []byte(benchmarkSeriesTestInput))
	if first.StorageCheck != "verified" || first.Authority != "owner_declared" || !first.Sample || first.PointCount != 2 {
		t.Fatalf("misleading receipt: %+v", first)
	}
	secondRaw := benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.SourceVersion = "test-v2"; i.Points[1].Level = "120" })
	second := importBenchmarkForTest(t, svc, secondRaw)
	svc.now = func() time.Time { t.Fatal("exact retry read the clock"); return time.Time{} }
	retry := importBenchmarkForTest(t, svc, benchmarkInputForTest(t, nil))
	if !reflect.DeepEqual(first, retry) {
		t.Fatal("canonical retry changed original receipt")
	}
	latest, err := readBenchmarkSeriesStatus(context.Background(), svc.db, first.BenchmarkID, "")
	if err != nil || !reflect.DeepEqual(second, latest) {
		t.Fatalf("old retry promoted past version: %v", err)
	}
	for name, edit := range map[string]func(*BenchmarkSeriesInput){
		"same version new data":    func(i *BenchmarkSeriesInput) { i.Points[1].Level = "999" },
		"same version new capture": func(i *BenchmarkSeriesInput) { i.FetchedAt = "2026-01-02T00:00:00Z" },
		"currency":                 func(i *BenchmarkSeriesInput) { i.SourceVersion, i.Currency = "test-v3", "USD" },
		"basis":                    func(i *BenchmarkSeriesInput) { i.SourceVersion, i.ReturnBasis = "test-v3", "price_index" },
		"reference": func(i *BenchmarkSeriesInput) {
			i.SourceVersion, i.SourceReference = "test-v3", "https://other.invalid/index"
		},
		"sample": func(i *BenchmarkSeriesInput) { i.SourceVersion, i.Sample = "test-v3", false },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.importBenchmarkSeries(context.Background(), benchmarkInputForTest(t, edit)); err == nil {
				t.Fatal("conflicting version admitted")
			}
		})
	}
	if _, err := svc.db.Exec(`UPDATE benchmark_series_versions SET source_version='changed'`); err == nil {
		t.Fatal("version updated")
	}
	if _, err := svc.db.Exec(`DELETE FROM benchmark_series_versions`); err == nil {
		t.Fatal("version deleted")
	}
	if _, err := readBenchmarkSeriesStatus(context.Background(), svc.db, first.BenchmarkID, "absent"); !errors.Is(err, errBenchmarkSeriesNotFound) {
		t.Fatalf("missing version: %v", err)
	}
}

func TestBenchmarkSeriesConcurrentImportIsAtomic(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "same", true: "conflict"}[conflict], func(t *testing.T) {
			svc, path := testService(t, nil, nil)
			other, err := openReadWriteExistingDB(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			second := newService(other, func() time.Time { return mustTime("2026-01-10T15:01:00Z") }, randomID)
			inputs := [][]byte{[]byte(benchmarkSeriesTestInput), benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) {
				if conflict {
					i.Points[1].Level = "111"
				}
			})}
			start := make(chan struct{})
			outcomes := make(chan error, 2)
			var wg sync.WaitGroup
			for n, service := range []*Service{svc, second} {
				wg.Add(1)
				go func(s *Service, raw []byte) {
					defer wg.Done()
					<-start
					_, err := s.importBenchmarkSeries(context.Background(), raw)
					outcomes <- err
				}(service, inputs[n])
			}
			close(start)
			wg.Wait()
			close(outcomes)
			failures := 0
			for err := range outcomes {
				if err != nil {
					failures++
				}
			}
			want := 0
			if conflict {
				want = 1
			}
			if failures != want {
				t.Fatalf("unexpected race outcomes: failures=%d", failures)
			}
			proof, err := proveBenchmarkSeriesRecovery(context.Background(), svc.db)
			if err != nil || proof.Versions != 1 || proof.Points != 2 {
				t.Fatalf("duplicate/partial race write: %+v %v", proof, err)
			}
		})
	}
}

func TestBenchmarkSeriesSharedFixturesAndContract(t *testing.T) {
	schema := readJSONMap(t, filepath.Join("..", "..", "contracts", "benchmark-index-series.schema.json"))
	var fields map[string]any
	if err := json.Unmarshal(benchmarkInputForTest(t, nil), &fields); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(jsonKeySet(fields), jsonStringSet(schema["required"])) {
		t.Fatal("input contract and runtime fields differ")
	}
	for _, currency := range []string{"KRW", "USD"} {
		input, _, _, err := decodeBenchmarkSeries(context.Background(), fixture(t, "benchmark-"+strings.ToLower(currency)+"-sample.json"))
		if err != nil || input.Currency != currency || !input.Sample || input.ReturnBasis != "price_index" || len(input.Points) != 2 {
			t.Fatalf("shared %s fixture drift: %v", currency, err)
		}
	}
}

func TestBenchmarkSeriesRejectsFutureAndBackwardsRecordClock(t *testing.T) {
	svc, _ := testService(t, []time.Time{mustTime("2026-01-10T00:00:00.1Z"), mustTime("2026-01-10T00:00:00Z")}, nil)
	importBenchmarkForTest(t, svc, []byte(benchmarkSeriesTestInput))
	if _, err := svc.importBenchmarkSeries(context.Background(), benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.SourceVersion = "test-v2" })); err == nil {
		t.Fatal("backwards record clock accepted")
	}
	if _, err := svc.importBenchmarkSeries(context.Background(), benchmarkInputForTest(t, func(i *BenchmarkSeriesInput) { i.SourceVersion = "test-v3"; i.FetchedAt = "2027-01-01T00:00:00Z" })); err == nil {
		t.Fatal("future capture accepted")
	}
	proof, err := proveBenchmarkSeriesRecovery(context.Background(), svc.db)
	if err != nil || proof.Versions != 1 {
		t.Fatalf("rejected write changed journal: %+v %v", proof, err)
	}
}
