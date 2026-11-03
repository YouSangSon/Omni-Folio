# G3.8D Paper Strategy-Window Performance Implementation Plan

> Execute with TDD. Keep the public API and Flutter surfaces unchanged.

**Goal:** persist exact, recoverable performance evidence for one current strategy selection without attributing pre-selection account movement.

**Architecture:** reuse recovered G3.8C3 points and `paperdomain.CalculatePerformance`; add one internal application use case and one append-only table. Do not create a repository interface or duplicate accounting/mark logic.

**Tech stack:** Go 1.25, `math/big.Rat` through `internal/exact`, SQLite STRICT tables/triggers, existing backup/restore harness, root Make cleanup wrappers.

## Task 1: RED strategy-window behavior

**Files**

- Create `services/core/paper_strategy_performance_test.go`.
- Do not change production files yet.

**Steps**

1. Add `TestG38DStrategyWindow` subtests for a one-point zero baseline, a second point's exact return/drawdown, reset after a new selection, and exclusion of account-global inherited max drawdown.
2. Add `TestG38DNoStrategy` and `TestG38DStale` proving no write on `no_strategy`, missing current-selection sample, zero baseline, stale expected selection, or stale latest performance ID.
3. Add `TestG38DIdempotency`, `TestG38DConcurrent`, and `TestG38DAtomicity` for exact retry, new latest append, same-key convergence, stale concurrent input, and forced insert failure.
4. Add `TestG38DRecovery`, `TestG38DBackup`, and `TestG38DRestore` placeholders that assert the exact schema/recovery/backup behavior rather than skipping.
5. Run:

   ```sh
   cd services/core
   go test -count=1 ./... -run '^TestG38D'
   ```

   Expected RED: compile failure for the missing event type/use case and failed schema expectations.

6. Commit only the RED tests with message `test(core): 전략 구간 성과 증거 요구사항 고정`.

## Task 2: GREEN application and persistence

**Files**

- Create `services/core/paper_strategy_performance.go`.
- Create `services/core/migrations/019_paper_strategy_performance_events.sql`.
- Modify `services/core/core.go` only for migration registration and recovery integration initially.

**Steps**

1. Define the fixed schema/policy constants, event shape, deterministic ID, insert/load helpers, and recovery proof.
2. In `evaluatePaperStrategyPerformance`, prove C3/D recovery before loading an existing key, validate current selection/latest C3 point, reject `no_strategy`, select same-selection C3 points, and use the first point's equity as baseline.
3. Reuse `paperdomain.CalculatePerformance`. Do not add arithmetic helpers.
4. Store exact canonical fields and append the new row. Re-run D recovery before commit.
5. Add SQL constraints for identifiers, policy, sample count, exact record shape, prerequisite foreign keys, deterministic current predecessor, current selection, and current latest C3 point. Add update/delete guards.
6. Run focused tests until the same command from Task 1 is GREEN.
7. Commit the minimal implementation with message `feat(core): 전략 구간 성과 증거 추가`.

## Task 3: Backup v13 and legacy v12 migration

**Files**

- Modify `services/core/core.go`.
- Modify `services/core/order_backup_test.go` and `services/core/order_schema_restore_test.go` only as required.
- Modify `contracts/backup-manifest.schema.json`.
- Update `contracts/fixtures/golden-backup-manifest.json`; historical compatibility stays covered by generated downgrade fixtures.

**Steps**

1. Bump database schema to 19, backup format to 13, and backup schema to `omni-folio.sqlite.v19`.
2. Add strategy-window digest/event/sample fields to source proof, manifest, verification receipt, schema, and current golden fixture.
3. Require table, foreign keys, index, state guard, and update/delete triggers during restore activation.
4. Verify current backup/restore independently reconstructs the D proof.
5. Verify a v12/schema-v18 source first, migrate only an owned copy, preserve the source, and initialize zero D history.
6. Verify failed restore candidate cleanup.
7. Run:

   ```sh
   cd services/core
   go test -count=1 ./... -run '^(TestG38DBackup|TestG38DRestore|TestBackup|TestRestore)'
   go test -race -count=1 ./...
   ```

8. Commit with message `feat(core): 전략 구간 성과 복구 계약 완성`.

## Task 4: Integration and evidence

**Files**

- No production changes unless a failing check exposes a scoped defect.

**Steps**

1. Run focused tests, full core race, `make check`, `make smoke`, `govulncheck ./...`, and `git diff --check`.
2. Run `make clean-test-resources` after every path and inspect the owned temp roots, `:18080`, build/coverage/bytecode artifacts, test-labelled Podman resources, and Kind clusters.
3. Inspect the implementation for public route/UI, thresholds/actions, broker/credentials, general-ledger writes, new dependencies, and mutable projections; all must be absent.
4. Obtain independent correctness/recovery and documentation reviews. Fix findings with RED/GREEN evidence before closing them.
5. Hand the verified implementation to the documentation leaf. Do not claim threshold or automatic rollback completion.
