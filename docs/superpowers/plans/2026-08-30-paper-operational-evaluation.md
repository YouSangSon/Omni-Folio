# Paper Operational Evaluation Implementation Plan

> **For agentic workers:** implement task-by-task with TDD. Do not widen this gate into financial performance or automatic authority mutation.

**Goal:** Persist replay-verifiable operational evaluations for the currently selected paper strategy without inventing return or drawdown evidence.

**Architecture:** Reuse the existing Go order replay and strategy registry. Derive a tuple-scoped order-state digest and fixed safety decision, append it to one new STRICT SQLite log, and fold that log into the existing strategy-registry backup proof.

**Tech Stack:** Go standard library, `database/sql`, SQLite STRICT tables/triggers, existing canonical JSON/SHA/time helpers, existing backup contracts.

**Spec:** `docs/superpowers/specs/2026-08-30-paper-operational-evaluation-design.md`

## Constraints

- No return, drawdown, profit, cash, fee, tax, slippage, latency, or valuation claim.
- No Python DB write, public route, CLI, UI, scheduler, alert, broker call, credential, strategy rollback, execution halt, shadow/live promotion, or deployment resource.
- Migration 014 only adds the evaluation log; legacy artifacts are copied before migration.
- Every production behavior begins with an observed RED test and ends with the smallest GREEN implementation.
- Tests clean all owned files and processes.

## Task 1: RED evaluation behavior

**Files:**
- Create: `services/core/paper_evaluation_test.go`

- [ ] Add a test that selects a strategy and evaluates zero matching paper orders as `INSUFFICIENT/no_terminal_sample`.
- [ ] Add a filled paper order and prove `PASS/operationally_complete` is derived from replay rather than caller metrics.
- [ ] Add a durable `SUBMIT_UNKNOWN` order and prove `DEGRADED/unresolved_action` takes precedence.
- [ ] Prove exact replay is idempotent, a changed scoped snapshot chains, and a stale selection or wrong account fails without a row.
- [ ] Prove a degraded evaluation does not alter strategy selection or execution authority.
- [ ] Run `cd services/core && go test . -run '^TestG38PaperEvaluation' -count=1` and record the compile failure.

## Task 2: Minimal evaluation log and replay

**Files:**
- Create: `services/core/migrations/014_paper_evaluation_events.sql`
- Create: `services/core/paper_evaluation.go`
- Modify: `services/core/strategy_registry.go`
- Modify: `services/core/core.go`

- [ ] Add the exact STRICT table, tuple-current index, insert-only triggers, and predecessor-state guard from the spec.
- [ ] Implement `evaluatePaperOperations`, scoped state derivation, deterministic ID, exact replay idempotency, and append.
- [ ] Reuse `proveOrderRecovery`, `replayStrategyRegistry`, `loadOrderIntentFrom`, `loadOrderStateFrom`, `orderJSONHash`, `safeOrderID`, and canonical time helpers; add no dependency or interface.
- [ ] Extend strategy-registry replay and proof with evaluation validation and count.
- [ ] Run focused tests until GREEN, then add corruption and direct-writer guard cases.

## Task 3: Schema v14 and backup v9

**Files:**
- Modify: `services/core/core.go`
- Modify: `services/core/core_test.go`
- Modify: schema-version expectation tests as required
- Modify: `contracts/backup-manifest.schema.json`
- Modify: `contracts/fixtures/golden-backup-manifest.json`

- [ ] Bump `latestSchema` to 14, `backupFormat` to v9, and `backupSchema` to v14; retain v8/schema-v13 as named legacy.
- [ ] Add `paper_evaluation_event_count` to the manifest and compare it through the existing strategy-registry proof.
- [ ] Require exact evaluation table/index/triggers during restore.
- [ ] Prove current backup/restore, corrupt evaluation rejection, and legacy schema-v13 owned-copy migration with empty evaluation proof.
- [ ] Run focused migration and backup tests, then `cd services/core && go test ./... -count=1`.

## Task 4: Evidence and verification

**Files:**
- Create: `gates/g3h-paper-operational-evaluation.md`
- Modify: `PLAN.md`
- Modify: `GATES.md`
- Modify: `CONTEXT.md`
- Modify: `docs/omni-folio-plan.md`

- [ ] Record RED/GREEN evidence and explicitly retain financial performance and automatic rollback as open follow-ups.
- [ ] Obtain an independent read-only review of derivation, state guards, recovery, legacy migration, and no-authority-mutation boundary.
- [ ] Run `make check`, `make smoke`, `cd services/core && go test -race -count=1 ./...`, `cd services/core && govulncheck ./...`, and `git diff --check`.
- [ ] Confirm no owned listener, smoke directory, test database, Python cache, Flutter build artifact, container, or pod remains.
- [ ] Sync the durable boundary to the Omni-Folio project wiki.
