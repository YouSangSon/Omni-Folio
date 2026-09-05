# G3.8G2G Native producer/consumer pipeline — local acceptance passed

## Scope and ownership

Root owns the native pipeline integration and cancellation correction; `continuous_lease_review` supplies read-only lifecycle/error review. This extends G3.8G2F's component evidence to an actual Python `--watch --bundle` process piped directly into the shipped Go `paper-execute-stream` binary. It adds no broker, credential, scheduler, queue or process supervisor.

The fixture prepares existing registered research/session data, starts both real programs without a shell intermediary, and publishes successive CSV snapshots by same-directory rename. Parent pipe descriptors close immediately after successful starts; each started child has a bounded context, WaitDelay and cleanup/join even if its sibling fails to start. No test-only switch or sleep is inserted into production code.

## Acceptance and findings

- Initial actual pipeline GREEN: first snapshot admits one BUY; the next snapshot produces one fill; consumer SIGTERM, producer SIGTERM/EOF and adverse-price automatic policy halt all finish both processes and release owned authority. Initial core run 9.758s, research 34 tests in 4.152s, root wrapper cleanup passed.
- Added explicit reconnect and exact policy provenance checks. Reconnect after an ordinary stop must not duplicate durable admission/fill; old selection after policy rollback must be denied. Source files remain byte-identical to the test's final published snapshots and contain no temporary publication tail.
- A native signal race exposed a classification defect: a canceled read in `validateCapitalizedPaperOrderBindings` was replaced by an intent-mismatch error in `validatePaperExecutionAuthorization`. The adjacent authority read had the same issue. Deterministic injected cancellation at each real SQLite read reproduced both failures before the correction.
- Shared validator read-error branches now retain canonical context cancellation without exposing raw storage errors; genuine field mismatches retain their prior behavior. One-shot and stream exits also retain invocation cancellation alongside existing validation and independent cleanup errors, since other legacy redaction paths may omit the sentinel. This changes diagnostics, not authorization or persistence rules.
- Fresh-context full policy/accounting recovery and ownership checks run after both processes exit, separately from their exit-message assertions. A canceled read is not evidence of corruption; a cancellation error is not evidence of successful cleanup either.
- `GOFLAGS='-race -run=TestPaperPipeline|TestPaperAuthorizationReadCancellation -count=1 -v' make test FLUTTER=true` passed: core 13.600s, research 34 tests in 4.063s and wrapper cleanup. Parent Go tests are race-instrumented; native child binaries use the existing non-race helper. The first provenance assertion used a noncanonical timestamp and was corrected to the established nanosecond storage contract; no production timestamp behavior changed.
- Read-only review found no remaining blocker after the cancellation correction. Final `make check` passed 2026-09-05: Go core 243.508s plus internal packages, full Flutter suite, Python 34 tests in 4.026s, 17 JSON syntax checks, format/vet/analyze and wrapper cleanup. `govulncheck ./...` found no vulnerabilities. Dependencies are unchanged; Python has no declared third-party dependencies, and pip-audit is unavailable/not run. No container, cluster, broker connection or persistent listener was created.

## Limits and source check

These are short deterministic local fixture runs, not sustained soak/load, source-completeness, actual market-data availability, broker execution, cloud deployment or profitability proof. A producer failure may give the Go consumer a normal EOF exit: inspect **both** process statuses (`pipefail` in the documented shell recipe), not just the rightmost exit code. Policy halt intentionally closes the pipe, so the producer's redacted closed-output exit can be expected; DB policy/rollback records distinguish that outcome.

Focused research on 2026-09-05 used Exa search and a fetch of the [official Go os/exec contract](https://pkg.go.dev/os/exec): passing `*os.File` connects child descriptors directly; each successful Start needs Wait; WaitDelay bounds child/I/O completion after cancellation. This is API evidence for the harness design, not independent proof of runtime behavior or a broad multi-engine research comparison.
