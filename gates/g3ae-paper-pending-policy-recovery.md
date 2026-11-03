# G3.8F3 Standalone paper runner pending-policy recovery

## Contract and cause

The performance-only `paper-run-due` and `paper-run-loop` used to select a newer market close before completing D/E for the latest stored C3. Because D and E require the latest predecessor, advancing first could permanently strand the older loss policy. A recovered cumulative return could then produce HOLD even though the pending loss required HALT.

The runner now checks the account's latest committed C3 in the existing preparation transaction. If it is older than the latest available close, belongs to the exact current strategy selection and lacks D or E, that one point is completed first using the existing fenced pipeline. The result reports its actual `as_of`. HALT ends the loop; HOLD/INSUFFICIENT permits advancement on the next invocation. Completed histories retain the latest-close behavior.

Only the latest stored point is eligible. An explicitly released old-selection prefix does not block a newer close under a manually selected strategy and is not reattributed. Existing availability clocks, recovery proofs, lease ownership/renewal, cached latest-close retry and selection checks remain. No schema, API shape, order/fill logic, queue or dependency changes.

After completing an older HALT while a newer close remains available, a rollback to `no_strategy` makes the next invocation fail without new performance, policy or trading events. If rollback instead selects a prior valid candidate, the existing selection rules permit a later invocation to evaluate the newer close for that candidate. Cached-HALT return remains limited to a completed chain at the latest available close; it is not a global last-HALT cache.

## Verification

The RED test failed on the original production path for both C3-only and C3+D interruptions: it returned HOLD at the recovery close instead of halting at the pending loss. The fixture uses a roughly 6% cumulative loss, below the 10% drawdown limit, followed by price recovery. A deeper drawdown would remain visible later and would not isolate this skipped cumulative-return decision.

Acceptance covers a reopened DB connection/Service for C3/C3+D and loop recovery, unchanged trading events, no-strategy retry, HOLD then latest-close progression and duplicate retry, released-prefix manual selection, concurrent owners, fencing/expiry, native shutdown and prerequisite corruption. Scheduler fixtures explicitly complete their baseline policy where latest-close behavior is the subject; the shared policy-test fixture remains unchanged.

The new loss cases construct durable C3/C3+D prefixes and reopen the database; they do not claim an OS crash at every stage. Existing native shutdown tests remain separate evidence.

The initial targeted suite reproduced six existing fixture assumptions about unfinished baselines; the next run had only the loop's incomplete-mark idle fixture remaining. These were corrected without changing production policy, clocks or lease timeouts. Initial failures are not acceptance evidence.

Final focused `make test CORE_TEST_PACKAGES='-race -run TestG38F -count=1 .' FLUTTER=true` passed: core 135.576s, Python 34 tests/4.830s, resource preflight and cleanup. Flutter was explicitly skipped in this focused run. The observed owned root `omni-folio-test.HjE856` and its processes were absent afterward. The macOS linker emitted an `LC_DYSYMTAB` warning; the race test exited successfully.

Final `make check CORE_TEST_PACKAGES='-timeout=30m ./...'` passed on 2026-10-23 with Go 1.24.5, Flutter 3.47.1 and Python 3.14.5: core 332.063s, five internal packages cached, Flutter 108 tests, Python 34 tests/4.917s, format/vet/analyze, Python compilation, resource preflight/cleanup and 17 JSON syntax checks. The opt-in real-clock soak and browser suite were not rerun: this unit changes only the standalone performance scheduler; the shared exact-close execution path and UI are unchanged. Prior soak/browser evidence remains scoped to their original gates.

Final `make smoke` passed health/status/preview/apply/snapshot/activity/market data. Observed check/test roots `omni-folio-test.4LDZMy` and `omni-folio-test.DfXhVW`, smoke root `omni-folio-smoke.BzZJFU`, their owned processes and the port 18080 listener were absent afterward. Logs are local ignored files under `docs/_internal/skill-outputs/goal-20261023-pending-policy-{red,focused,green,race,check,smoke}.log`. GitHub Actions currently has zero workflows; this is local validation, not remote CI evidence.

Independent read-only design and final-diff review found no blocking code issue. It requested the explicit no-strategy retry regression and the bounded cached-HALT explanation above; both are included. The reviewer did not execute tests.

## Limits

This does not fill or admit orders, evaluate unvalued intermediate bars, repair gaps before the newest C3, reassign prior-selection evidence or infer missing market input. Standalone manual command composition is not the chronological fill/policy executor. Broader G3.8G2, durable delivery, target-host long-history contention, deployment and broker/live readiness remain open.
