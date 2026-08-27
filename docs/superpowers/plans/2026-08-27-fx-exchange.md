# FX Exchange Ledger Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record and replay exact two-leg FX cash exchanges while preserving schema-v8 backup recovery.

**Architecture:** Extend the existing canonical transaction and insert-only events table; reuse preview/apply/idempotency/snapshot/backup paths. Legacy backup verification operates on an owned temporary copy before using the current schema verifier.

**Tech Stack:** Go, SQLite, JSON Schema/OpenAPI, Flutter/Dart, repository Make targets.

**Spec:** `docs/superpowers/specs/2026-08-27-fx-exchange-design.md`

## Global Constraints

- No new dependency, table, service, port, or exchange-rate calculation.
- Decimal values remain canonical strings and SQLite remains append-only/single-writer.
- Never modify an input backup during legacy verification.
- No broker credential, broker call, order mutation, deploy, or live authority. GitHub push/PR/merge is limited to the user's explicit 2026-08-27 approval for this branch.

---

### Task 1: Failed backup candidate cleanup

**Files:**
- Modify: `services/core/core.go`
- Test: `services/core/core_test.go`

**Interfaces:**
- Consumes: existing `createBackup` output/manifest paths.
- Produces: both newly-created candidate paths absent after any failure; existing targets remain protected.

- [x] Write `TestCreateBackupDiscardsFailedCandidate`, using a valid database and deliberately mismatched golden snapshot.
- [x] Run `go test -count=1 -run '^TestCreateBackupDiscardsFailedCandidate$' ./...` and confirm the candidate remains before the fix.
- [x] Add one deferred cleanup guard after pre-existing target checks; mark success only after manifest write completes.
- [x] Rerun the focused test and mutate the cleanup guard once to prove it detects the regression.

### Task 2: FX transaction, migration, replay, and backup compatibility

**Files:**
- Create: `services/core/migrations/009_fx_exchange.sql`
- Create: `services/core/fx_exchange_test.go`
- Modify: `services/core/core.go`
- Modify: current schema/backup tests and fixtures only where version expectations change.

**Interfaces:**
- Consumes: CSV preview, `Transaction`, `events`, `snapshotFrom`, backup v5 verifier.
- Produces: `FX_EXCHANGE` with exact `counter_currency`/`counter_amount`, schema v9, native-v9 and legacy-v8 verified recovery.

- [x] Write failing valid-flow, invalid-leg, direct-schema-guard, conflict, migration, and old-backup tests with hand-derived cash expectations.
- [x] Run the focused tests and confirm failures are caused by absent FX/schema-v9 behavior.
- [x] Add the two transaction fields, mapping v4 validation, insert/load/replay support, and migration 009 with preserved guards.
- [x] Add legacy-v8 manifest verification through a temporary migrated copy; remove it on all exits.
- [x] Rerun focused tests and mutation-check counter-leg replay and schema guard.

### Task 3: Closed API and accessible import disclosure

**Files:**
- Modify: `contracts/openapi.json`
- Modify: `contracts/backup-manifest.schema.json`
- Modify: `apps/client/lib/models.dart`
- Modify: `apps/client/lib/app.dart`
- Test: `services/core/core_test.go`
- Test: `apps/client/test/vertical_slice_test.dart`

**Interfaces:**
- Consumes: normalized FX transaction JSON.
- Produces: closed counter-leg contract and explicit two-leg import review.

- [x] Write failing OpenAPI, Flutter parser, and 320px/200% widget tests; verify the parser rejects incomplete FX data.
- [x] Add conditional OpenAPI fields, v8/v9 backup schema enum, closed Flutter counter-leg parsing, and two-leg disclosure.
- [x] Run focused Go and Flutter tests, then mutate one counter field/semantics branch and confirm failure.

### Task 4: Evidence, documentation, and local checkpoint

**Files:**
- Modify: `PLAN.md`, `GATES.md`, `CONTEXT.md`, `DESIGN.md`, `docs/omni-folio-plan.md`
- Create: `gates/g1d-fx-exchange.md`

**Interfaces:**
- Consumes: fresh verification output.
- Produces: bounded completion evidence and explicit non-proof.

- [x] Run focused tests, `go test -count=1 ./...`, `go test -race -count=1 ./...`, `make check`, `make smoke`, and the available Go vulnerability scan.
- [x] Confirm no build, coverage, binary, Python cache, listener, smoke temp, Podman, or Kind resource owned by the checks remains.
- [x] Review diff for credentials, private identifiers, sensitive financial error payloads, and accidental scope growth.
- [x] Update canonical plans/gates with exact evidence and prepare the verified branch for the explicitly authorized GitHub PR/merge workflow.
