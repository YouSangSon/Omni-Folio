# G3.8F1 — Scheduled one-shot local paper policy runner

## Scope

G3.8F1 adds the smallest scheduler-safe local entrypoint for paper performance evaluation and safety action:

- internal `runDuePaperPerformancePolicy(ctx, accountRef)`
- CLI `omni-core paper-run-due -db <path> -account <kiwoom_account_...>`

It does not add a daemon, cron resource, Kubernetes object, public API, Flutter screen, broker call, credential access, promotion authority, live execution authority, or profitability claim.

## Contract

- The runner derives `as_of` only from the latest available local `paper_fixture` KRX/KRW/1d/Asia-Seoul `price_adjustment=unspecified` close whose `source_available_at`, `fetched_at`, and `recorded_at` are all at or before the runner clock.
- It reuses existing G3.8C3 account-global performance, G3.8D current-selection strategy-window performance, and G3.8E policy/action journals.
- It writes no scheduler table in this checkpoint. Duplicate-process and crash retry safety comes from existing unique/idempotent performance, strategy-performance, and policy keys.
- It proves the paper performance policy recovery root before returning a completed chain, so old prerequisite corruption cannot be hidden by cached results.
- If a completed chain already exists for the latest available close, retry returns it even after a prior `HALT_AND_ROLLBACK` changed the current selection to `no_strategy`.
- If no current paper strategy exists, or the latest close cannot provide complete marks for held positions, the runner fails closed without writing a new performance row.

## Evidence

- RED: `go test -count=1 -run '^TestG38FScheduled' .` failed because `runDuePaperPerformancePolicy` and `PaperScheduledRunResult` did not exist.
- RED: `go test -count=1 -run '^TestG38FPaperRunDueCLI$' .` failed with `unknown command "paper-run-due"`.
- RED: `go test -count=1 -run '^TestG38FScheduledPaperRunRetryRejectsPrerequisiteCorruption$' .` failed when cached retry hid a corrupted prerequisite journal.
- GREEN: `go test -count=1 -run '^TestG38F' .` passed.
- GREEN: `go test -race -count=1 -run '^TestG38F' .` passed.
- GREEN: `make check` passed after format, vet, Flutter analyze/test, Go tests, Python tests, JSON contract validation, and cleanup.
- GREEN: `make smoke` passed for health, status, preview, apply, snapshot, activity, and market data.
- CLEANUP: `make test-resource-cleanup` passed active-owner preservation, PID/command/start-time reuse rejection, stale child process-group cleanup, and owned descendant cleanup after the group leader exits.
- CLEANUP: intentional-failure and SIGINT/SIGTERM matrices for `make test`, `make check`, and `make smoke`, plus actual SIGKILL stale-owner recovery, left the owned temp/build/coverage/bytecode inventory clean.

## Still Open

- No always-on scheduler process, alerting, shadow promotion, broker-backed evaluation, credential/live authority, deployment, or real-money readiness.
- G3.8F2 still needs DB lease/fencing, heartbeat/TTL, stale-owner recovery, lease-loss fail-closed behavior, and success/failure/SIGINT/SIGTERM/SIGKILL cleanup proof.
- Official Kiwoom calendar/timezone/freshness behavior is not proven. The checkpoint only trusts local fixture availability timestamps.
