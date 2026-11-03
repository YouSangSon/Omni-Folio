# G3.8C1 Immutable Paper Accounting Session

Scope: one account-global, immutable starting-capital authority for paper accounting. This is a prerequisite for financial accounting, not runner, performance, profit, production, or live-readiness evidence.

## Acceptance

- [x] One `account_ref` owns one immutable session. Its initial selected research result/event and starting capital never reset when the strategy selection later changes.
- [x] Starting cash and the complete execution-policy JSON/hash are derived only from the initially selected immutable research artifact; callers cannot provide or override capital or cost assumptions.
- [x] Schema v15 and backup v10 bind an independent paper-accounting digest/count. Restore requires the exact session table, account uniqueness, state guard, and insert-only triggers.
- [x] Backup v9/schema v14 is migrated through an owned copy and proves an empty session state. Legacy paper orders remain preserved and replayable, but uncapitalized and never backfilled.
- [x] Selected-policy loading and session recovery prove the current strategy and complete order histories first; corruption in either fails closed before session authority is certified or written.
- [x] Independent reviews found two Important trust-boundary gaps: standalone recovery omitted `proveOrderRecovery`, and SQLite rejected valid `0.01` starting cash while admitting noncanonical `1.0`. Focused RED regressions reproduced both; the fixes aligned order proof and the shared positive canonical-decimal contract, and both re-reviews returned GO.
- [x] No paper runner behavior, fill accounting, cash balance, lots, costs, equity, return, drawdown, automatic authority mutation, API, CLI, UI, scheduler, credential, broker call, or live-money path was added.

## Fresh verification — 2026-08-30 KST

- `make check` — exit 0: Go format/vet/test, Flutter format/analyze and 65 tests, Python compile and 15 tests, and 15 JSON contracts passed.
- `make smoke` — exit 0: demo preview/apply and health, status, snapshot, activity, and market-data smoke passed.
- `cd services/core && go test -race -count=1 ./...` — exit 0: core race suite passed in 18.839s.
- `cd services/core && govulncheck ./...` — exit 0: no vulnerabilities found.
- `git diff --check` — exit 0: no whitespace errors.

## Cleanup

- Removed only four test-created Python bytecode files in `services/research/omni_research/__pycache__`; removal is recoverable by rerunning Python compilation/tests.
- Post-cleanup: no repo `__pycache__` or `*.pyc`, no `/tmp/omni-folio*` or `/tmp/omni_folio*` artifact, and `lsof -nP -iTCP:18080 -sTCP:LISTEN` exited 1 (no listener).
- Podman inspection found no containers and no explicitly Omni-Folio-labeled/name-matched resources. The unlabeled `buildx_buildkit_default_state` volume and pre-existing unlabeled networks were retained. `kind get clusters` exited 0 with no clusters. No Podman or Kind resource was removed.

## Still Open

- **G3.8C2:** target reduction/SELL together with eligible-bar fill and fee, tax, slippage, cash, lots, oversell, and overdraft protection.
- **G3.8C3:** immutable order/price cutoffs, marks, equity, returns, and drawdown.
- Thresholds, automatic halt, and rollback only after those two boundaries are proved.
- Scheduler, alerts, credentials, broker calls, and every live-money path.
