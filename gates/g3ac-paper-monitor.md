# G3.8H Local paper policy monitor

State: local acceptance passed on `feat/paper-monitor`, based on local `47c999f`. Root owns Go, OpenAPI and documentation; `paper_monitor_ui` owns Flutter; `continuous_ingress_review` owns read-only review. No external push or deployment.

## Contract and acceptance

Expose existing durable local paper evidence through GET `/v1/paper/monitor` and a Connections detail page. The `paper-monitor.v1` response contains server observation time, selected-candidate presence, session count, incomplete committed C3/D/E chains, the singleton policy-runner lease observation, and the most recently appended policy across all local paper accounts. No account/owner/event/result identifiers, financial metrics, external calls, writes, heartbeat, arm or trading actions.

One read transaction proves the full runner prerequisite chain before projecting any result. Empty registry returns false, not an error. A failed proof returns a redacted error, never a partial or older healthy result. Runner precedence is unowned, clock-regressed, expired, selection-changed, then lease-recorded. None proves process liveness, execution lease validity, current market freshness or safe takeover. Pending chains include prior selections and are not a runnable queue. Latest policy is ordered by append sequence and may precede the current selection; automatic rollback normally makes its selection match false.

Flutter fetches only when opening or manually refreshing the page. Refresh failure retains the entire original observation and timestamp, displays a fixed recovery message, and does not recompute lease authority on the client clock. Show empty/loading/error/pending/expired/halt and prior-selection states with text, semantic tokens and accessible 320px/200% layouts. No polling, alarm delivery or execution controls are added.

Acceptance requires HTTP lifecycle, redaction/no-write, corruption and contract tests; Flutter parser/HTTP/widget checks; full repository check; owned cleanup; read-only review. Physical screen-reader/device evidence and actual browser rendering are reported separately.

## Design research

Focused deep-research searched one query and read two official Prometheus documents on 2026-09-05: [alerting](https://prometheus.io/docs/practices/alerting/) and [monitoring principles](https://prometheus.io/docs/practices/the_zen/). They distinguish user-impact symptoms from diagnostic causes and require confidence in monitoring itself. Application here is an inference: expose incomplete durable processing and the stored stop reason, but do not promote a lease row or successful HTTP response to end-to-end health. This is an on-demand console, not Prometheus integration or delivered alerts; broader alerting remains open.

## Verification

- Initial empty-view test failed with HTTP 404, then passed with a verified empty projection (core 0.697s, Python 34/4.007s, owned cleanup zero; Flutter skipped). An earlier test-helper typo was a setup failure, not product RED evidence.
- Stored-policy/lease test failed against the empty projection, then passed after connecting existing validated loaders. A separate incomplete-chain RED caught the missing pending count before its SQL projection passed.
- Full `make check` exited zero: all Go packages (core 279.285s), Flutter 83 tests, Python 34/4.037s, formatter, analyzer, vet, 17 JSON contracts and owned cleanup. This run did not enable the optional real-clock soak.
- Review follow-up tightened the closed policy decision/reason pair. Flutter accepted a contradictory pair in RED, then passed all 84 tests after the guard. The same owned `make test CORE_TEST_PACKAGES=./internal/exact` wrapper passed Python 34 and Go exact. Tests cover on-demand HTTP, empty/error/retry, complete retained observations, post-disposal completion, 320px/200% light/dark and warning semantics.
- Final `GOFLAGS='-race -run=TestPaperMonitor -count=1' make check FLUTTER=true` exited zero: monitor core 6.622s, Python 34/4.101s, vet/format/17 JSON contracts and owned cleanup. This is focused Go evidence, not a second full suite. Separate final Flutter analyze passed (3.1s); `govulncheck ./...` reported no vulnerabilities. No npm project or third-party Python runtime dependencies were introduced, so npm/pip audits are not applicable to this delta.
- No dependencies, schema/backup migration, Podman/Kind resources, running dev servers, external push, deployment or live credentials were added. Physical screen-reader/device testing and actual browser rendering were not performed in this gate.

## Review decisions and limits

Final independent read-only review found no remaining blocker after the policy-pair and accessibility corrections. The reviewer withdrew the deferred-BEGIN and client-time inference findings after checking actual driver configuration and producer semantics. No review-agent tests were claimed.

The deferred-BEGIN race suggestion does not match current wiring: `openDB` configures `_txlock=immediate`; installed mattn/go-sqlite3 v1.14.24 `BeginTx` delegates to `begin` and ignores `ReadOnly`. The lock/snapshot is established before the observation clock read. Read-only here means no application writes, not absence of writer contention. Full journal proof and the immediate lock are intentionally retained until measured contention justifies a verified projection.

Decision/reason pairs are validated in both the public schema and Flutter. No client-clock lease reclassification is added. Wall clocks may regress between a stored performance window and policy append, so `as_of <= recorded_at` is not an existing invariant and must not be invented in the consumer. Server-side lease observations are tested at normal, backward and exact-expiry times. This on-demand slice does not complete alert delivery, durable input transport, sustained operations or the whole Goal.
