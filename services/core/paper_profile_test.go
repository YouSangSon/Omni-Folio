package main

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"
)

type paperLatencyProfile struct {
	Samples int   `json:"samples"`
	P50     int64 `json:"p50_ns"`
	P95     int64 `json:"p95_ns"`
	P99     int64 `json:"p99_ns"`
	Max     int64 `json:"max_ns"`
}

func paperLatencySummary(samples []int64) paperLatencyProfile {
	ordered := slices.Clone(samples)
	slices.Sort(ordered)
	return paperLatencyProfile{len(ordered), ordered[(len(ordered)*50+99)/100-1], ordered[(len(ordered)*95+99)/100-1], ordered[(len(ordered)*99+99)/100-1], ordered[len(ordered)-1]}
}

func TestPaperLatencySummaryNearestRank(t *testing.T) {
	values := make([]int64, 100)
	for i := range values {
		values[i] = int64(100 - i)
	}
	got := paperLatencySummary(values)
	if got.Samples != 100 || got.P50 != 50 || got.P95 != 95 || got.P99 != 99 || got.Max != 100 || values[0] != 100 {
		t.Fatalf("invalid nearest-rank summary or input mutation: %+v", got)
	}
}

// Opt-in diagnostic, not a CI latency threshold or real-time soak. The logical
// lease clock advances; elapsed work uses the monotonic wall clock. Every
// history record is produced by the real renewal transaction, never bulk SQL.
func TestPaperHistoryProfile(t *testing.T) {
	if os.Getenv("OMNI_PAPER_PROFILE") != "1" {
		t.Skip("set OMNI_PAPER_PROFILE=1 through the root test wrapper")
	}
	svc, _, research, evidence, selected, rows := localPaperRestartFixture(t)
	now := svc.now()
	svc.now = func() time.Time { return now }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	run := &localPaperRun{service: svc, account: k2aAccountRef, selection: selected.CurrentEventID}
	defer func() {
		if err := run.close(); err != nil {
			t.Errorf("profile cleanup: %v", err)
		}
	}()
	bars := paperSnapshotCSV(rows...)
	proposal := localPaperRestartProposal(t, svc, evidence.ResultSHA256, rows[len(rows)-1], bars, "golden_cross")
	if result, err := run.step(ctx, proposal, bars, research); err != nil || result.Order == nil {
		t.Fatalf("profile initial step: %+v %v", result, err)
	}
	before := paperAdmissionCountsForTest(t, svc)
	type band struct {
		Before   int                 `json:"prior_renewals"`
		After    int                 `json:"final_renewals"`
		Renewal  paperLatencyProfile `json:"renewal"`
		Recovery paperLatencyProfile `json:"recovery"`
	}
	var bands []band
	completed := 0
	renew := func() int64 {
		now = now.Add(paperRunnerLoopHeartbeatInterval)
		start := time.Now()
		claim, lease, err := svc.heartbeatLocalPaperExecution(ctx, run.claim, run.lease.FencingToken)
		elapsed := time.Since(start).Nanoseconds()
		if err != nil {
			t.Fatalf("renewal %d: %v", completed, err)
		}
		run.claim, run.lease = claim, lease
		completed++
		return elapsed
	}
	for _, prior := range []int{0, 300, 900} {
		for completed < prior {
			renew()
		}
		latencies, recoveries := make([]int64, 100), make([]int64, 100)
		for i := range latencies {
			latencies[i] = renew()
			start := time.Now()
			if _, err := provePaperPerformancePolicyRecovery(ctx, svc.db); err != nil {
				t.Fatal(err)
			}
			recoveries[i] = time.Since(start).Nanoseconds()
		}
		bands = append(bands, band{prior, completed, paperLatencySummary(latencies), paperLatencySummary(recoveries)})
		t.Logf("completed profile band %d..%d renewals", prior, completed)
	}
	if paperAdmissionCountsForTest(t, svc) != before {
		t.Fatal("idle profiling changed orders or fills")
	}
	var sqlite string
	if err := svc.db.QueryRow("SELECT sqlite_version()").Scan(&sqlite); err != nil {
		t.Fatal(err)
	}
	result := struct {
		Schema string `json:"schema_version"`
		Go     string `json:"go"`
		OS     string `json:"os"`
		Arch   string `json:"arch"`
		CPUs   int    `json:"logical_cpus"`
		Procs  int    `json:"gomaxprocs"`
		SQLite string `json:"sqlite"`
		Bands  []band `json:"bands"`
	}{"paper-history-profile.v1", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.GOMAXPROCS(0), sqlite, bands}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PAPER_HISTORY_PROFILE=%s", raw)
}
