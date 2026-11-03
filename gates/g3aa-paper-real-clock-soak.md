# G3.8G2J Native real-clock paper soak — local acceptance passed

## Scope and acceptance

Root owns the test and documents; `continuous_ingress_review` provides read-only acceptance review. This extends the existing native Python watch → OS pipe → Go executable fixture, not the production scheduler or strategy. It follows [G3.8G2I](g3z-paper-renewal-replay.md). No new dependency, process supervisor, delivery queue or broker connection is introduced.

`TestPaperPipelineRealClockSoak` is explicitly enabled by `OMNI_PAPER_SOAK=1`; ordinary tests skip it. The existing helper receives a timeout argument: existing scenarios retain 45 seconds and this scenario allows three minutes. Every child retains cancellation, bounded wait and test-owned cleanup.

- Actual `time.Now` is used in both child execution and observation. Twelve historical synthetic EOD snapshots are scheduled at elapsed 10…120 seconds, through same-directory atomic rename. Every close must reach its durable `HOLD` policy before the next publication, because the watch producer coalesces snapshots rather than promising a durable queue.
- A one-second ticker samples execution and global leases within one three-second-bounded DB transaction. Fixed owners, fixed global fence and strategy selection, nondecreasing execution fence/global heartbeat, and positive conservatively observed expiry headroom are required. At least 100 samples and ten execution renewals must occur over at least 120 elapsed seconds.
- Final durable history must contain one signal/order/authorization/fill, 13 performance and strategy-performance events, 12 valuation marks, cumulative strategy sample count 91, and 13 policies (one initial `INSUFFICIENT`, 12 `HOLD`, no automatic halt). All initial bars plus 12 additions must remain present. There is exactly one manual arm and no changed strategy selection.
- Consumer `SIGTERM` must result in a handled cancellation error and no stdout. The actual producer must join with its fixed redacted closed-output error. Fresh-context current-schema runner recovery recursively verifies policy, strategy performance, valuation, accounting, market/signals, order/execution authority and authorization; no duplicate top-level recovery implementations are added. Both leases must be unowned and execution unarmed afterward.
- Final source bytes must equal the test's published bytes, and only the three expected source files may exist. The root wrapper checks and removes owned test resources on completion, failure and interruption.

## Verification

Final command on 2026-09-05: `OMNI_PAPER_SOAK=1 GOFLAGS='-run=TestPaperPipeline -count=1 -v' make test FLUTTER=true`. Core passed in 135.378s, including the existing native signal/EOF/policy-halt and reconnect scenarios (10.71s) and the new soak (124.30s including setup). The measured active soak completed in **122.029s: 120 samples, 12 acknowledged snapshots, 13 policies, 11 renewals, minimum observed lease headroom 12.005s**. Scheduled publication times are targets, not exact real-time deadlines; acknowledgement and DB observation add work.

Python's 34 tests passed in 4.039s, and the root resource-cleanup wrapper completed with exit 0. `make format-check` passed (9 Dart files unchanged), and `go vet ./...` passed from `services/core`. An initial vet invocation at the repository root only reported that the root is not a Go module; it was corrected, not counted as a pass. Flutter is explicitly skipped for this test-only change; the preceding production optimization's complete `make check` evidence remains in G3.8G2I. Read-only final review found no remaining blocker; the reviewer did not run tests.

Review corrected an early first publication and a final-fill check that did not acknowledge the newest policy. An earlier two-minute run passed the weaker draft but is not the final acceptance evidence. The current-schema runner proof already includes the prerequisite recovery chain, confirmed by tracing the production functions rather than adding redundant calls.

## Interpretation and remaining work

The mental model is **elapsed lease survival + acknowledged durable progress + post-exit recovery**. A live PID alone proves none of the latter two. Conversely, one-second samples cannot establish uninterrupted validity between observations; the observer's DB reads are part of this workload. Go's [time documentation](https://pkg.go.dev/time#NewTicker) notes that tickers can drop ticks when receivers are slow; elapsed duration, sample count and committed closes are separately asserted, not inferred from a nominal ticker count.

This is a two-minute bounded local check with historical EOD fixture dates, not a market-calendar/freshness proof, full-day uptime, cold-start benchmark, leak/RSS measurement, storage-contention test or live trading evidence. New-process reconnect remains covered by the existing native pipeline test, not by the soak itself. Larger growing trade histories, target-host contention, long-phase lease exhaustion, source completeness and durable delivery remain open under G3.8G2. No deployment, credential change, live activation or profitability claim.
