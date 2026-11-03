# G3.8E Paper Performance Safety Policy Implementation Plan

> Execute with TDD. Keep public APIs and Flutter surfaces unchanged.

**Goal:** turn one exact recovered G3.8D window into a durable local paper
decision and, only at a fixed v1 breach, atomically reuse G3.7 halt/rollback.

**Architecture:** add one pure `riskdomain` function, one internal application
use case, and one append-only cross-journal recovery proof. Reuse existing exact
decimal, transaction, fencing, selection-stack, backup, and cleanup machinery.

**Tech stack:** Go 1.25, `math/big.Rat` through `internal/exact`, SQLite STRICT
tables/triggers, existing backup/restore and Make cleanup wrappers.

## Task 1: RED pure policy

**Files**

- Create `services/core/internal/riskdomain/paper_performance_policy_test.go`.
- Do not create production policy code yet.

**Steps**

1. Assert the exact v1 constants and closed decision/reason pairs.
2. Assert sample counts 0/1 are insufficient and 2 is eligible.
3. Assert both inclusive boundaries, just-inside values, and drawdown-first tie.
4. Assert malformed/non-canonical decimals, a negative sample count, and a
   negative maximum drawdown fail before any decision.
5. Run `cd services/core && go test -count=1 ./internal/riskdomain`; record RED.

## Task 2: GREEN pure policy, then RED application path

**Files**

- Create `services/core/internal/riskdomain/paper_performance_policy.go`.
- Create `services/core/paper_performance_policy_test.go` after pure GREEN.

**Steps**

1. Implement only the pure fixed policy, reusing `internal/exact`.
2. Make the focused domain tests GREEN; do not add configuration or interfaces.
3. Add application RED tests for insufficient/hold/action, exact current/latest
   binding, stale and cross-selection evidence, automatic reason provenance,
   all armed accounts, no armed accounts, exact retry after rollback, two-handle
   convergence, and zero partial writes on forced failures.
4. Run `cd services/core && go test -count=1 ./... -run '^TestG38E'`; record RED.

## Task 3: GREEN atomic journal and action

**Files**

- Create `services/core/paper_performance_policy.go`.
- Create `services/core/migrations/020_paper_performance_policy_events.sql`.
- Modify the existing G3.7 transaction helpers and migration registration only
  where required.

**Steps**

1. Add the canonical event type, deterministic ID, insert/load helpers, and
   migration-020 append-only schema.
2. Add policy linkage and automatic reason codes to the existing authority and
   selection journals while preserving manual behavior and historical records.
3. Extract reason/link-aware tx helpers from the existing halt/rollback path;
   keep public manual wrappers unchanged.
4. Implement `applyPaperPerformancePolicy` with recovery-first retry,
   exact-current validation, fixed decision, atomic action, and proposed-state
   replay before commit.
5. Split non-recursive base journal validation from the root semantic policy
   proof; require the root at every schema-v20 strategy writer, authority
   writer, policy call, startup, backup, and restore trust boundary. Corrupt a
   linked policy row before SELECT, ROLLBACK, arm, halt, lease, batch halt,
   policy retry, startup, backup, and restore to prove no bypass or recursion.
6. Make focused G3.8E tests GREEN. Re-run G3.7 and G3.8D regressions.

## Task 4: RED/GREEN independent recovery and backup v14

**Files**

- Extend `services/core/paper_performance_policy.go` recovery.
- Modify `services/core/core.go`, schema restore tests, and backup tests.
- Modify `contracts/backup-manifest.schema.json` and the current golden fixture.

**Steps**

1. RED mutation tests for policy/hash/link/cutoff/action coverage and old retry
   after prerequisite corruption.
2. GREEN full replay with forward and reverse action coverage and digest/counts.
3. RED schema-drift and manifest-forgery tests for every new object and field.
4. Bump schema to 20 and backup to v14; wire source/candidate proof and receipt.
5. Rebuild authority and selection through `_new` tables under the one scoped
   foreign-key-off migration exception; preserve exact rows/digests and the full
   child-FK inventory, then require pre/post-enable `foreign_key_check`.
6. Exercise a non-empty v19 fixture with all existing child relationships and
   prove no FK targets a temporary table name after migration.
7. Verify v13/schema19 at source, migrate only an owned copy, preserve old
   digests and source bytes, and start G3.8E empty.
8. Verify failed candidate cleanup.

## Task 5: Integration, review, and evidence

1. Run focused tests, full core tests, full race, `make check`, `make smoke`,
   `govulncheck ./...`, and `git diff --check`.
2. Exercise intentional failure and SIGINT/SIGTERM cleanup paths; inspect owned
   processes/listeners/temp/build/coverage/bytecode/Podman/Kind inventory.
3. Independently review policy boundaries, transaction rollback, recovery,
   schema/backup compatibility, manual regressions, and external-authority
   non-goals. Fix findings with new RED/GREEN evidence.
4. Update goal/plan/gates/context/design/readme/testing evidence and the
   project wiki only after all verification is fresh.
5. Create bounded local Git checkpoints. Do not push, merge, deploy, publish,
   access credentials, or enable live money without explicit approval.
