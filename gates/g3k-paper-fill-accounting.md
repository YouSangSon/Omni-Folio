# G3.8C2 SELL and Capital-Safe Paper Accounting

Scope: credential-free, ex-post paper BUY/SELL fills with exact replay-derived KRW accounting. This is accounting evidence, not broker execution, equity, performance, production, or live-readiness evidence.

## Acceptance

- [x] `paper_bar_open_v1` uses the open of an immutable closed daily bar together with final volume known only after `source_available_at`. The modeled `occurred_at=open_at` is therefore ex-post simulation, not opening-auction or live execution evidence.
- [x] Each `paper-signal.v3` stores a transaction-owned market observation sequence cutoff, and fills require `signal_bar.sequence <= cutoff < eligible_bar.sequence`. The exact `delay_bars`-th later same-series bar is the first eligible bar.
- [x] A capitalized signal requires the account-global paper session and exact current execution-policy SHA to agree. Strategy changes do not reset cash or lots, and legacy v1/v2 orders remain replay-only and uncapitalized.
- [x] Signed target delta creates BUY or SELL, target zero requests full reduction, and one active order per account and symbol prevents crossing or replacement before terminal state.
- [x] KRX quantity is a canonical whole share no greater than `4611686018427387903`; fill capacity is `floor(volume * max_participation)` after prior account/symbol/bar consumption.
- [x] Each non-zero fill applies the exact fixed KRW fee, SELL-only notional tax, and adverse slippage from eligible-bar open. BUY affordability, SELL holdings, cash, and position cannot become negative.
- [x] `order_events.FILL_RECORDED` is the sole durable fill journal. Cash, FIFO lots/cost, fees, taxes, slippage, realized PnL, and capitalized fill count are derived by complete replay; no mutable balance/lot table or general-ledger paper event exists.
- [x] Admission and every local fill require the current execution lease event and exact fencing token. Paper authorization remains isolated from synthetic K2C reservation policy and never calls Kiwoom mock/production transport.
- [x] Schema v17 and backup v11 bind sessions, bars, signal cutoffs, authorizations, capitalized fills, and canonical replay-derived account state. Backup v10/schema v15 and older supported inputs migrate only through an owned copy; legacy paper evidence is not capitalized.
- [x] Application/shared writers cannot append a capitalized fill outside the replay-before-write path. Direct raw SQLite access is outside that writer authority boundary: SQL guards structure and provenance, while independent recovery and restore activation reject canonical-looking rows with forged arithmetic.
- [x] Task 1-4 independent reviews closed every scoped Critical/Major/Important/Minor finding after bounded fix and re-review rounds. The later `orderdomain` extraction preserves the reviewed transition behavior but is not itself C2 completion proof.
- [x] The later R1 refactor moves the already-proven exact arithmetic and pure paper fill/account rules into infrastructure-free internal domain packages. Direct invalid fills are fail-atomic; SQL, transaction, lease, provenance, durable journal, recovery, schema, backup, and public contracts remain unchanged. This is maintainability evidence, not new C2 or C3 product authority.
- [x] No API, CLI, UI, scheduler, credential, broker request, general-ledger write, live order, external resource, push, merge, or deployment is part of this gate.

## Implementation and Task-Review Evidence

- Design and implementation range: `6e10067..a55a3d0`; Task 1-4 final re-reviews report no remaining scoped findings.
- Focused C2/accounting, backup/restore, K2C/G3 regressions and full core race evidence are recorded in the ignored SDD task reports. These are implementation-task records, not fresh Task 5 final command evidence.
- Characterization and `internal/orderdomain` extraction commits `b4c7c02..bfe4a6c` are separately scoped refactoring evidence.
- Exact-kernel checkpoint `c108300` and the subsequent `internal/paperdomain` checkpoint have direct RED/GREEN suites, exact production-import allowlists, full/race regressions, and independent GO reviews. They do not add marks, equity, returns, drawdown, broker execution, or live authority.

## Fresh Final Verification — 2026-08-31 KST

- [x] `make check` — PASS on 2026-08-31 KST after Task 5 cleanup changes: Go checks, Flutter analyze and 66 tests, Python 17 tests, cleanup regression, and 15 JSON contracts passed.
- [x] `make smoke` — PASS on 2026-08-31 KST after Task 5 cleanup changes.
- [x] `cd services/core && go test -race -count=1 ./...` — PASS on `bfe4a6c`.
- [x] `cd services/core && govulncheck ./...` — PASS on `bfe4a6c`.
- [x] `git diff --check` — PASS on 2026-08-31 KST after the Task 5 documentation and cleanup diff.
- [x] Scoped cleanup audit — no `:18080` listener, Omni-Folio test/smoke/restore temp root, labeled Podman container, Omni-Folio Kind cluster, or owned build/coverage/Python-cache artifact. Unrelated resources and ignored `/api-key/` were preserved.
- [x] Stale-owner cleanup regression — `make test-resource-cleanup` proves a dead-owner smoke root and its owned server are removed, an active owned root is preserved, and a reused PID with mismatched owner command is treated as stale.
- [x] Interruption cleanup — `make test`, `make check`, and `make smoke` were each interrupted once with SIGINT and once with SIGTERM; all six post-run audits found no owned listener, temp root, build/coverage artifact, or Python cache.
- [x] Untrappable-exit recovery — a SIGKILL fixture left one owner-marked smoke root; the next smoke preflight removed it, then its intentional build failure also left no owned root or listener. `make test-resource-cleanup` preserves a live-owner fixture while reclaiming a dead-owner root and its exact-path server process.

These are local/mock evidence only. They do not establish broker connectivity, profit, deploy, production, or G3.8C3 performance evidence.

## Post-R1 Boundary Verification — 2026-08-31 KST

- [x] Direct `internal/exact` and `internal/paperdomain` RED/GREEN suites, focused C2 recovery/restore regressions, full Go tests, and two independent full race runs passed after the final review fix.
- [x] Independent exact-kernel and paper-domain reviews ended GO with no remaining severity finding; the paper review's malformed side/tax/cash-delta finding was closed by fail-atomic preflight examples.
- [x] `make check`, `make smoke`, `govulncheck ./...`, and `git diff --check` passed on the final R1 tree.
- [x] Post-run cleanup found no `:18080` listener, Omni-Folio test/smoke temp root, owned build/coverage/Python-cache artifact, labeled Podman container, or Omni-Folio Kind cluster.

## Still Open

- **G3.8C3:** immutable order/price sequence cutoffs for valuation, durable marks, equity, returns, drawdown, and versioned performance evidence.
- Versioned performance thresholds, scheduler, automatic halt/rollback, and paper/shadow promotion remain forbidden until G3.8C3 and their own authority gates pass.
- Public accounting/equity API or UI, broker reconciliation, Kiwoom or other live/credentialed execution, margin/short/fractional shares, cancel/replace, and multi-order shared-bar allocation.
