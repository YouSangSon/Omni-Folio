# Paper Accounting Session Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish one replay-verifiable, account-global paper starting-capital authority derived only from the exact selected research artifact.

**Architecture:** Convert the existing G3.8B execution validator into a typed decoder, then store one immutable session per paper account in a STRICT SQLite registry. Fold its digest/count into backup v10 while migrating v9/schema-v14 artifacts through an owned copy with zero inferred sessions.

**Tech Stack:** Go standard library, `database/sql`, `math/big`, SQLite STRICT tables/triggers, canonical JSON/SHA helpers, existing migration and backup verifier.

**Spec:** `docs/superpowers/specs/2026-08-30-paper-accounting-session-design.md`

## Global Constraints

- A session is unique by `account_ref`; strategy changes never reset starting capital.
- Starting cash and policy JSON/hash come only from stored, currently selected `strategy-improvement-result.v1` evidence.
- Existing paper orders are never backfilled into a session and remain recovery-only until later accounting gates.
- No order, fill, cash balance, lot, fee/tax/slippage application, equity/PnL, API, CLI, UI, scheduler, credential, broker call, or authority mutation.
- Schema becomes v15 and backup becomes v10; v9/schema-v14 remains a supported owned-copy legacy input.
- Every production behavior begins with an observed RED test.

---

### Task 1: Typed selected execution policy

**Files:**
- Modify: `services/core/strategy_registry.go`
- Modify: `services/core/strategy_registry_test.go`

**Interfaces:**
- Consumes: stored `strategy_research_evidence.artifact_json`, `replayStrategyRegistry`, `strategyCanonicalJSON`, `parseDecimal`.
- Produces: `strategyExecutionPolicy`, `decodeStrategyExecutionContract(any)`, and `loadCurrentStrategyExecutionPolicy(context.Context, orderQuerier, string, string)`.

- [ ] **Step 1: Write the failing loader test**

Add `TestG38C1LoadsOnlyCurrentSelectedExecutionPolicy`. Register and select the fixture, call:

```go
policy, err := loadCurrentStrategyExecutionPolicy(ctx, svc.db, evidence.ResultSHA256, selected.CurrentEventID)
```

Assert `StartingCash == "10000"`, `Fee == "1"`, `Tax == "0.001"`, `SlippageBPS == "10"`, `DelayBars == 1`, `MaxParticipation == "0.5"`, fixed signal/fill values, and a lowercase 64-character policy SHA. Roll back selection and assert the same result/event pair now fails.

- [ ] **Step 2: Verify RED**

Run: `cd services/core && go test . -run '^TestG38C1LoadsOnlyCurrentSelectedExecutionPolicy$' -count=1`

Expected: compile failure because `loadCurrentStrategyExecutionPolicy` does not exist.

- [ ] **Step 3: Implement the typed decoder**

Use this exact internal shape:

```go
type strategyExecutionPolicy struct {
    StartingCash, Fee, Tax, SlippageBPS, MaxParticipation string
    DelayBars int64
    SignalPrice, FillPrice, SHA256, canonicalJSON string
}
```

Refactor `validateStrategyExecutionContract` into `decodeStrategyExecutionContract(value any) (strategyExecutionPolicy, error)`. Preserve exact keys and every G3.8B range check. Canonicalize the execution object with `strategyCanonicalJSON`, hash it, and keep `decodeStrategyArtifact` calling the typed decoder so registration and recovery remain fail-closed.

`loadCurrentStrategyExecutionPolicy` replays the registry, requires exact current result/event, reads only that result's `artifact_json`, calls `decodeStrategyArtifact`, and returns its unexported policy. Add no cache or projection.

- [ ] **Step 4: Verify GREEN and regression**

Run:

```bash
cd services/core
go test . -run '^(TestG38B|TestG38C1LoadsOnlyCurrentSelectedExecutionPolicy|TestG3Registry)' -count=1
```

Expected: PASS, including all malicious rehash cases.

- [ ] **Step 5: Commit**

```bash
git add services/core/strategy_registry.go services/core/strategy_registry_test.go
git commit -m "feat(core): 선택 실행 정책 로더 추가"
```

### Task 2: Immutable account-global session registry

**Files:**
- Create: `services/core/paper_accounting.go`
- Create: `services/core/paper_accounting_test.go`
- Create: `services/core/migrations/015_paper_accounting_sessions.sql`
- Modify: `services/core/core.go`

**Interfaces:**
- Consumes: Task 1 loader, `proveOrderRecovery`, `replayStrategyRegistry`, `orderJSONHash`, `canonicalUTCString`, `orderAlias`.
- Produces: `PaperAccountingSession`, `openPaperAccountingSession`, `provePaperAccountingRecovery`, and `paperAccountingRecoveryProof`.

- [ ] **Step 1: Write the failing session behavior tests**

Add `TestG38C1PaperAccountingSessionDerivesImmutableCapital` that selects the fixture and calls:

```go
session, err := svc.openPaperAccountingSession(ctx, k2aAccountRef, evidence.ResultSHA256, selected.CurrentEventID)
```

Assert schema `paper-accounting-session.v1`, account, initial result/event, `StartingCash="10000"`, `Currency="KRW"`, exact policy SHA, one row, and exact idempotency. Assert there is no money/cost parameter in the method signature by using only the four arguments above.

Add `TestG38C1PaperAccountingSessionFailsClosed` with subtests for stale selection, mismatched result, prior account paper order, corrupt order proof, and corrupt registry. After each failure assert zero session, order, risk reservation, authority, ledger, and price-row deltas.

- [ ] **Step 2: Verify RED**

Run: `cd services/core && go test . -run '^TestG38C1PaperAccountingSession' -count=1`

Expected: compile failure because the session type and opener do not exist.

- [ ] **Step 3: Add migration 015**

Create a STRICT `paper_accounting_sessions` table with the exact fields from the spec, `UNIQUE(account_ref)`, foreign keys to `strategy_research_evidence(result_sha256)` and `strategy_selection_events(event_id)`, fixed schema/currency checks, positive canonical starting-cash text, lowercase SHA checks, and canonical record columns.

Add `paper_accounting_sessions_no_update`, `paper_accounting_sessions_no_delete`, and `paper_accounting_sessions_state_guard`. The state guard must compare against the latest selection event/result, require that event's selected result to match, and reject when any `order_idempotency` row has `mode='paper' AND account_ref=NEW.account_ref`.

- [ ] **Step 4: Implement minimal session/replay**

Use:

```go
type PaperAccountingSession struct {
    SessionID, SchemaVersion, AccountRef string
    StrategyResultSHA256, StrategySelectionEventID string
    ExecutionPolicySHA256, ExecutionPolicyJSON string
    StartingCash, Currency, RecordedAt string
}
```

The opener starts one transaction, proves order and strategy recovery, returns the existing exact account session if present, loads the current policy, rejects any account paper order, derives the deterministic session ID and canonical record hash, inserts once, and commits. Replay scans sequence order, validates every field/hash/foreign binding and emits SHA/count.

- [ ] **Step 5: Add direct-writer and corruption tests**

Prove stale/mismatched/prior-order direct INSERT rejection, UPDATE/DELETE rejection, changed policy JSON/hash rejection, deterministic ID mismatch rejection, and two concurrent exact opens yielding one row.

- [ ] **Step 6: Verify GREEN**

Run:

```bash
cd services/core
go test . -run '^TestG38C1PaperAccountingSession' -count=1
go test . -run '^(TestG3PaperRunner|TestG38PaperEvaluation|TestG38C1)' -count=1
```

Expected: PASS; existing runner behavior is unchanged.

- [ ] **Step 7: Commit**

```bash
git add services/core/paper_accounting.go services/core/paper_accounting_test.go services/core/migrations/015_paper_accounting_sessions.sql services/core/core.go
git commit -m "feat(core): 페이퍼 회계 세션 원장 추가"
```

### Task 3: Schema v15 and backup v10 recovery

**Files:**
- Modify: `services/core/core.go`
- Modify: `services/core/core_test.go`
- Modify: `services/core/order_backup_test.go`
- Modify: `services/core/order_schema_restore_test.go`
- Modify: `contracts/backup-manifest.schema.json`
- Modify: `contracts/fixtures/golden-backup-manifest.json`

**Interfaces:**
- Consumes: Task 2 `paperAccountingRecoveryProof`.
- Produces: backup manifest and receipt fields `paper_accounting_state_sha256`, `paper_accounting_session_count`, and `candidate_paper_accounting_state_sha256`.

- [ ] **Step 1: Write failing backup and legacy tests**

Add a current backup round-trip with one session and assert exact digest/count in manifest and receipt. Remove or alter the table, unique account auto-index, state guard, update/delete triggers in separate candidates and assert activation fails.

Create or reuse a genuine v9/schema-v14 fixture containing paper orders but no sessions. Assert source hash/size remain unchanged, owned-copy migration reaches schema v15, order proof is identical, and paper-accounting proof is the empty SHA/count zero.

- [ ] **Step 2: Verify RED**

Run: `cd services/core && go test . -run '^(TestG38C1PaperAccountingBackup|TestRestore.*V9)' -count=1`

Expected: compile or assertion failure because v10 fields and v9 legacy handling do not exist.

- [ ] **Step 3: Extend versions, manifest, and restore proof**

Set `latestSchema=15`, `backupFormat="omni-folio-backup.v10"`, and `backupSchema="omni-folio.sqlite.v15"`. Name v9/schema-v14 as the immediate legacy format. Append migration 015 to the fixed migration list.

Compute source/candidate paper-accounting proofs during backup, compare them, write digest/count and candidate receipt fields, and require them only for v10. After owned-copy migration, every older supported backup must yield the empty session proof.

- [ ] **Step 4: Pin exact SQLite objects**

Extend restore schema validation to require the migration-015 table definition, the account uniqueness index columns, and exact state/update/delete trigger bodies. Do not accept merely similar object names.

- [ ] **Step 5: Update JSON contracts**

Require the two manifest fields and candidate receipt digest in `backup-manifest.schema.json`; update the golden manifest to v10/schema-v15 with the empty SHA-256 and zero count.

- [ ] **Step 6: Verify GREEN and full core**

Run:

```bash
cd services/core
go test . -run '^(TestG38C1|TestRestore|TestBackup)' -count=1
go test -count=1 ./...
```

Expected: PASS with current and all supported legacy restores.

- [ ] **Step 7: Commit**

```bash
git add services/core contracts/backup-manifest.schema.json contracts/fixtures/golden-backup-manifest.json
git commit -m "feat(core): 회계 세션 복구 증명 추가"
```

### Task 4: Gate evidence and completion verification

**Files:**
- Create: `gates/g3j-paper-accounting-session.md`
- Modify: `goal.md`
- Modify: `PLAN.md`
- Modify: `GATES.md`
- Modify: `CONTEXT.md`
- Modify: `docs/omni-folio-plan.md`

**Interfaces:**
- Consumes: all implementation and verification evidence from Tasks 1-3.
- Produces: G3.8C1 completion record and explicit G3.8C2/G3.8C3 boundaries.

- [ ] **Step 1: Record the exact boundary**

Document account-global capital authority, no strategy-change reset, no legacy backfill, and zero performance/runner authority. Keep SELL, costs, balances, marks, equity, thresholds, scheduler, broker, and live paths open.

- [ ] **Step 2: Independent review**

Require a read-only reviewer to inspect account-vs-selection semantics, policy derivation, transaction order, direct-writer guards, replay, v9 owned-copy migration, and absence of order/authority side effects. Resolve every blocker with a new RED test.

- [ ] **Step 3: Full verification**

Run:

```bash
make check
make smoke
cd services/core && go test -race -count=1 ./...
cd services/core && govulncheck ./...
git diff --check
```

- [ ] **Step 4: Resource cleanup**

Remove only test-owned Python bytecode/cache and explicit temporary artifacts. Verify no port `18080` listener, Omni-Folio `/tmp` artifact, labeled Podman container, or Kind cluster remains.

- [ ] **Step 5: Sync durable memory and commit evidence**

Update the Omni-Folio project hub and staged-architecture page. Commit only the related docs and report that push/deploy/live authority remain untouched.
