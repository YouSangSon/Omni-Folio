# G3.8G2H Paper history latency baseline — local diagnostic passed

## Method and ownership

Root owns `TestPaperHistoryProfile`; `continuous_lease_review` reviewed measurement interpretation and cleanup without editing or running a competing wrapper. `benchmark` and `latency-critical-systems` guide measurement before optimization. No production logic, dependency, schema, validation or lease duration changes.

The opt-in test uses the existing real research/session fixture and admission path, then commits 1,000 actual joint-renewal transactions. It measures 100 samples in each growing-history band, not 100 repetitions against a fixed database size. Each renewal is followed by separately timed full policy recovery. Percentiles use nearest rank, tested independently without mutating the input.

```sh
OMNI_PAPER_PROFILE=1 GOFLAGS='-run=TestPaperHistoryProfile|TestPaperLatencySummary -count=1 -v' make test FLUTTER=true
```

The lease clock advances logically by 10 seconds per renewal; elapsed work uses Go's monotonic clock. This is not 10,000 seconds of real uptime. The fixture has one account, selection, symbol and initial open order; order/fill counts are asserted unchanged. Recovery benefits from renewal's warmed caches. No isolated-host, cold-start, growing trade/policy history or multi-process contention claim is made.

## Evidence, 2026-09-05

Exact nanoseconds and runtime metadata: [tracked baseline](../.ecc/benchmarks/paper-history-20260905.json). Values below are rounded milliseconds.

| Completed renewals in band | Renewal p50 / p95 / p99 | Full policy recovery p50 / p95 / p99 |
| --- | --- | --- |
| 1–100 | 8.00 / 10.64 / 21.07 | 5.62 / 6.18 / 6.28 |
| 301–400 | 13.67 / 17.70 / 22.68 | 9.27 / 10.28 / 10.93 |
| 901–1,000 | 24.84 / 28.39 / 30.78 | 16.42 / 17.28 / 17.72 |

Profile test passed in 21.20s (core package 21.450s); Python 34 tests passed in 4.058s. Flutter was explicitly skipped for this diagnostic. Root wrapper cleanup passed. The default suite skips the heavy profile but still tests percentile calculation. Work has a three-minute context; ordinary failure/timeout runs independent authority cleanup before database/temp cleanup. SIGKILL cannot run defers; this diagnostic does not replace the separate process-death/TTL recovery gates.

Final `make check` also passed: core 187.908s plus internal packages, full Flutter suite, Python 34 tests in 4.096s, format/vet/analyze, 17 JSON contract syntax checks and owned-resource cleanup. The benchmark JSON passed a separate syntax check. `govulncheck ./...` found no vulnerabilities. No dependencies changed; Python declares no third-party packages, pip-audit was not run, and this is not a comprehensive dependency/security audit or remote CI result.

## Interpretation and next gate

In this single run, renewal p95 grew about 2.67× between bands. Append-only authority history is a demonstrated scaling axis, not proof of a specific asymptotic bound or the sole bottleneck. Keep this baseline and profile CPU/query work before optimizing; preserve full recovery, current-owner validation and atomic renewal. Do not add a cache, mutable authority projection, larger TTL or new service based only on these timings.

Sustained real-time renewal under growing order/policy histories, contention and target-host storage remains unverified. Larger histories require a measured bounded plan rather than extrapolating 1,000 renewals into a daily SLA. G3.8G2 remains incomplete; there is no broker/live, source-completeness, cloud deployment or profitability evidence here.
