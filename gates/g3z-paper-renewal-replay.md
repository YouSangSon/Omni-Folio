# G3.8G2I Transaction-local renewal replay — local acceptance passed

## Ownership and measured problem

Root owns implementation, tests, measurement and documents on `perf/paper-replay-history`; `continuous_lease_review` owns read-only call-chain/security review. This follows [G3.8G2H](g3y-paper-history-profile.md), not a new scheduler, cache, projection or storage format.

The renewal path verified full runner → policy → strategy-performance → performance → accounting → order → execution-authority history, then `requireCurrentSyntheticExecutionLease` scanned the account's entire authority history again. It subsequently reloaded the latest record by ID. In the baseline one-account fixture, each renewal therefore read history twice; standalone policy recovery read it once.

An actual CPU profile of the unmodified production path used `go test -cpuprofile` through the root cleanup wrapper (`CORE_TEST_PACKAGES=.` selects one package; default remains `./...`). It sampled 27.21 CPU seconds over 22.74 wall seconds: `runtime.pthread_cond_signal` was 61.30% flat and `runtime.pthread_cond_wait` 16.65%. These asynchronous/runtime samples do not directly attribute that percentage to one application function. Pinned `go-sqlite3 v1.14.24` starts a worker goroutine on each `SQLiteRows.Next` with a cancellable context. Removing cancellation to avoid this work would weaken shutdown behavior; reducing the redundant scan preserves it. Raw profile/binary were task-owned temporary files and were removed after inspection.

## Change and safety gate

- After full proof in the **same transaction, before any writes**, renewal reads the account's indexed latest event and reuses the canonical record loader. Independent callers still use full-account replay.
- `validateOwnedExecutionLease` is the shared armed/owner/fence/expiry check used by both paths. Selection/session, exact global claim, nonregressing clock, successor transition, atomic append/CAS, TTL and cleanup ownership remain enforced.
- Review exposed a prerequisite hole: policy recovery intentionally returns early for old migration versions. Physical runner tables alone did not prove that full history validation ran. Deleting migration markers 20/21 while preserving the physical schema and corrupting an older authority hash reproduced an incorrectly successful renewal in the intermediate optimization.
- `provePaperRunnerLeaseRecovery` now calls the existing current-schema/history check through a context-aware helper before any legacy-sensitive proof. This fixes the shared boundary for acquisition, renewal, release, scheduler and backup consumers, not only this caller. Legacy restore migrates an owned candidate before current runner proof; original legacy sources retain version-specific verification.
- `TestLocalPaperHeartbeatRejectsCorruptHistoricalAuthority` covers an older bad hash, a rehashed invalid first fence, and downgraded migration markers with an individually valid newest lease. It restores exact migration records and the original trigger in cleanup; failed mutation setup rolls back rather than leaving a missing trigger.

## Verification evidence, 2026-09-05

- Existing expiry/fencing, simultaneous renewal, second-write rollback, fill and restore checks passed in the focused race run. Final focused command: `GOFLAGS='-race -run=TestLocalPaperHeartbeat|TestLocalPaperRenewal|TestLocalPaperStepRenews|TestPaperRunner.*(Recovery|Backup) -count=1 -v' make test FLUTTER=true`; core 8.480s, Python 34 tests/3.988s, cleanup passed. The Runner suffix regex did not match additional named tests; full suite remains the authoritative wider backup gate.
- A deliberate temporary removal of initial full recovery made the historical-corruption test fail, including an incorrectly accepted bad hash. The proof was restored. Downgraded migration markers separately reproduced RED before the shared schema guard, then passed after it.
- One initial corruption-fixture attempt collided with an existing unique fence; it was corrected to an unused value. This was a test-setup failure, not a production defect. A `make -n test-body` probe also ran recursive cleanup-fixture shell code and failed its cleanup assertions; it is not test-pass evidence. The owned fixtures terminated, and explicit cleanup completed. Do not use recursive `make -n` as a side-effect-free test runner.
- Post-change, uninstrumented profile passed: core 17.047s, Python 34 tests/3.990s and cleanup. Same runtime, sample count and growing-history fixture as the retained baseline. Exact values: [before](../.ecc/benchmarks/paper-history-20260905.json), [after](../.ecc/benchmarks/paper-history-20260905-single-replay.json).
- Final full `make check` passed: core 195.521s plus internal packages, Flutter 74 tests, Python 34 tests/4.011s, format/vet/analyze, 17 JSON syntax checks and owned-resource cleanup. `govulncheck ./...` found no vulnerabilities. Benchmark JSON syntax validation passed separately. Dependencies are unchanged; Python declares no third-party packages and pip-audit was not run. Read-only review found no remaining blocker after the migration guard.

| Renewal band | Before p50 / p95 / p99 (ms) | After p50 / p95 / p99 (ms) |
| --- | --- | --- |
| 1–100 | 8.00 / 10.64 / 21.07 | 7.46 / 9.07 / 16.49 |
| 301–400 | 13.67 / 17.70 / 22.68 | 10.80 / 12.29 / 23.41 |
| 901–1,000 | 24.84 / 28.39 / 30.78 | 17.07 / 19.99 / 21.66 |

Last-band renewal p95 fell 29.6% in this comparison. Standalone full recovery remains unchanged code and its last-band p50 was 16.42ms both times. Not every tail improved: middle-band p99 and last-band maximum increased (34.70ms → 39.58ms). These single, non-isolated runs are descriptive, not statistically established tail guarantees or CI thresholds.

## Research and remaining limits

Focused `deep-research` used one Exa query and reads of two official Go sources on 2026-09-05, not a broad multi-engine survey. [Go diagnostics](https://go.dev/doc/diagnostics) distinguishes CPU work from I/O waits and warns that profiling adds overhead; [Go performance diagnostics](https://go.dev/wiki/Performance) documents test CPU profiles and cautions against simultaneous profilers. The cancellation worker behavior was checked directly in the pinned dependency source, [SQLiteRows.Next](https://github.com/mattn/go-sqlite3/blob/v1.14.24/sqlite3.go#L2166).

No cancellation, validation, immutable event, exact arithmetic or lease interval was removed. History still grows indefinitely and full recovery still scans retained history; this measurement does not establish the exact asymptotic cost in broader workloads. Real-time soak, growing trade/policy histories, target-host storage contention, long-phase lease exhaustion, source completeness and durable delivery remain open under G3.8G2. No external push, broker/live, deployment or profitability claim.
