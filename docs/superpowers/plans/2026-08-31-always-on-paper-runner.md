# G3.8F2 Always-On Local Paper Runner Implementation Plan

> Execute with strict RED -> GREEN -> refactor. Preserve the three C3/D/E
> transactions and all external/live boundaries.

**Goal:** add one DB-leased/fenced always-on local wrapper over G3.8F1 with
heartbeat/TTL, stale-owner recovery, lease-loss rollback, and owned cleanup.

**Architecture:** one mutable global strategy-selection lease row, explicit
account/selection-bound claim, transaction-local renew/guards in existing C3/D/E
stages, one serial stdlib timer loop, and existing SQLite backup/restore
machinery.

**Stack:** Go stdlib, SQLite STRICT tables/triggers, existing Make cleanup.

## Task 1: RED lease and authority boundaries

1. Add failing tests for acquire/heartbeat/release, retained monotonic fencing,
   race winner, exact-expiry takeover, stale owner, clock regression, overflow,
   canonical recovery, and scheduler/execution-authority separation.
2. Add failing tests that manual selection/rollback reject a live lease and a
   second account cannot overlap an automatic rollback.
3. Run focused `TestG38F2` and record the compile/behavior RED before code.

## Task 2: GREEN schema and lease aggregate

1. Add migration 021 and the minimum concrete lease operations/proof.
2. Add a distinct random runner owner to `Service`; reuse no execution token.
3. Add startup and exact restore-schema validation.
4. Make only the focused lease tests GREEN, then run prior strategy/authority
   regressions.

## Task 3: RED/GREEN transaction fencing and resume

1. Add failing C3/D/E tests for entry loss, expiry before commit, selection
   mismatch, exact E rollback exception, C3 and C3+D takeover resume, and cached
   corruption rejection.
2. Thread an explicit claim through the scheduled path; keep lower-level domain
   calculations and journal identities unchanged.
3. Renew before each stage, add a stage deadline below TTL, and make exact claim
   renewal the final SQL write inside each existing transaction.
4. Route `paper-run-due` incomplete writes through the same lease path.
5. Make focused tests GREEN and run G3.8C3/D/E/F1 regressions with `-race`.

## Task 4: RED/GREEN loop and lifecycle

1. Add failing tests for immediate work, serial idle heartbeat,
   completion-based polling, typed not-due states, single in-flight work, fatal
   stop, context cleanup, successful automatic-rollback stop, and loser
   no-release.
2. Add `paper-run-loop` with stdlib timers and context-aware CLI wiring.
3. Add actual process SIGINT/SIGTERM checks, a heartbeat/write-lock cancellation
   case, and deterministic SIGKILL-style before-expiry rejection/after-expiry
   higher-fence takeover.
4. Prove success, intentional failure, signal, and next-run cleanup inventory.

## Task 5: RED/GREEN backup v15 and legacy v14

1. Add failing source/candidate proof, active-lease preservation plus activation
   refusal, manifest-field, schema-drift, and owned-copy migration tests.
2. Bump schema/backup versions, add lease proof fields/receipt fields, and
   update the contract schema and fixtures.
3. Verify v14/schema20 at source, migrate only an owned copy, require empty v21
   lease state, and preserve source bytes on success/failure.
4. Run all restore, migration, and backup compatibility regressions.

## Task 6: Review, evidence, and delivery

1. Run focused/full/race, `make check`, `make smoke`, vulnerability scanning,
   `git diff --check`, and final scoped process/temp/Podman/Kind inventory.
2. Obtain independent correctness/security and documentation reviews; fix every
   finding with fresh RED/GREEN evidence.
3. Update goal/plan/gates/context/design/readme/testing evidence and project
   wiki only after verification is current.
4. Create bounded local commits. The user explicitly authorized branch push,
   `main` PR creation, required-check wait, merge, and local/remote alignment in
   this turn; perform those actions only while that authorization remains in
   force. Do not deploy or enable credentials/live money.
