# G3.8G2K Chronological local paper recovery

## Work item and acceptance

Root owns code, tests and documentation on `fix/paper-chronological-recovery` based on `1208738`; `continuous_ingress_review` owns read-only boundary and acceptance review. This advances local paper recovery under G3.8G2, not broker/live authority or complete market delivery.

The observed defect: `localPaperRun.step` imported a whole snapshot, drained every eligible fill, then evaluated only the latest close. An intermediate fall followed by recovery could disappear from safety evaluation, and later fills could occur before the missed halt. Appending historical policy after draining all fills would not fix that ordering defect.

## Implementation

- Shared one-shot/bundle/stream execution enumerates stored available closes from the latest immutable performance through the exact proposal anchor. First use evaluates only the anchor: preceding input rows remain strategy warmup.
- Compatibility exception: only a preflight-verified, already committed signal may recover its existing order through the latest stored available close. Final admission remains the same immutable decision; a fresh proposal cannot expand its frontier this way. This preserves the existing actual-signal restart and historical committed-order replay contracts.
- At each close, existing orders fill only through that close; performance, strategy performance and policy then commit before advancing. The cutoff only narrows eligible fill candidates; it does not change real authority time, immutable fill arithmetic, IDs, delay, capacity, accounting or fencing.
- A committed performance point is a retry boundary. Complete its missing strategy/performance policy without adding fills behind that point's fixed order cutoff. A completed older point is only a history boundary when moving forward. A same-close cached retry still passes the current-selection C3 guard, and `HALT_AND_ROLLBACK` ends the batch before any later fill or final proposal admission.
- Reuse the existing exact-as-of C3/D/E pipeline extracted from the scheduled runner. No schema, backup format, client contract, new projection, queue or dependency is introduced.
- New points must also be at or after the current selection's recorded time. Stored pre-selection closes are not mislabeled as that strategy's window; an existing frontier remains available for retry or rejection by its original selection guard.

## Verification evidence

`TestLocalPaperRestartStopsAtIntermediateLossBeforeLaterFills` failed on the old production path: the batch returned success without the intermediate halt. The same regression passed after interleaving. In its fixture, six shares fill at 100, then one at 1; the intermediate loss must halt with seven shares filled and leave the remaining three unfilled despite the later recovery bar. Old-selection retry must add no trading events.

`TestLocalPaperRestartDrainsPartialFillsAcrossMissedBars` retains exact five-fill accounting and duplicate-safe replay and now requires every intervening close's completed HOLD policy. `TestLocalPaperRestartCompletesInterruptedLossPolicyBeforeLaterFills` injects connection-local D/E insert faults after the loss valuation commits, reopens the DB with a new Service and requires completion of that same loss policy with unchanged trading events. Test faults do not alter persistent schema objects.

Local G3.8G2K acceptance passed on 2026-09-05; broader G3.8G2 remains incomplete. The final read-only boundary review found no blocking regression; it did not execute tests. A pre-existing incomplete C3 whose selection changes before D/E remains fail-closed and requires operational recovery, not reassignment to the new selection.

The final focused selection/native-signal command `GOFLAGS='-run=TestLocalPaperCachedPolicy|TestLocalPaperExecutableSignalRecovery -count=1 -v' make test FLUTTER=true` passed: core 88.092s, including SIGINT 18.51s, SIGTERM 18.05s and SIGKILL 48.21s with a real 29.970s expiry wait. Python 34 tests/6.548s and owned cleanup passed; Flutter was explicitly skipped in this focused run.

Final `OMNI_PAPER_SOAK=1 make check` passed: core 449.775s (not cached), internal packages cached, Flutter 74 tests, Python 34 tests/3.990s, Go/Dart formatting, vet/analyze, Python compilation and 17 JSON syntax checks. The opt-in real-clock soak ran within this full suite; its individual timings were not printed in non-verbose output, so this run adds no new percentile/headroom claim. Owned cleanup returned zero, and the observed session root `omni-folio-test.RAjpYI` was absent afterward. No Podman/Kind resources were created. `govulncheck ./...` found no vulnerabilities; dependencies are unchanged, Python declares no third-party requirements, and npm/pip audit were not run.

Final focused `GOFLAGS='-race -run=TestLocalPaperRestart|TestLocalPaperCachedPolicy|TestLocalPaperStepRenews|TestLocalPaperLostGlobal -count=1 -v' make test FLUTTER=true` passed: six top-level core tests and both policy-interruption subtests, core 67.920s, Python 34 tests/4.076s, cleanup exit zero. This is focused race evidence, not a full-suite race claim; the full native interruption/soak evidence comes from the commands above.

Review exposed a cached-policy selection hole in the intermediate implementation. `TestLocalPaperCachedPolicyCannotAuthorizeNewSelection` reproduced a successful same-close reuse after selecting the same artifact under a new selection event. Routing every evaluated close through the existing C3 current-selection guard fixes it. Its positive next-close case must be after the new selection's recorded time; the first fixture incorrectly used a pre-selection close and was corrected without changing the attribution rule.

Further review showed the pre-selection case also arises naturally in a batch containing both pre- and post-selection closes. The expanded regression reproduced C3 committing the old close and D rejecting it, stranding an immutable incomplete frontier. Filtering **new** points by selection time prevents that prefix while keeping already-stored frontiers available for idempotent completion or selection rejection.

The first broader race run failed existing historical committed-proposal replay and actual-signal recovery tests because an unconditional proposal-anchor cap dropped their established later-stored-bar recovery behavior. The committed-signal-only frontier exception restores that contract without weakening new-proposal validation. These initial failures are not acceptance evidence; final results must supersede them.

The 486-bar native interruption fixture is retained. Chronological recovery now includes ten policy evaluations and exceeded the helper's 15-second shutdown wait when that wait was used for ordinary work. Only the explicit retry receives a 60-second work deadline and is awaited before applying the unchanged shutdown join; other child calls keep 20-second contexts. The fence expectation includes actual verified renewal records instead of assuming recovery always finishes before the first heartbeat. This is not a longer production TTL or relaxed shutdown assertion.

## Research and interpretation

Focused `deep-research` used two Exa queries and read three official sources on 2026-09-05. Questions: why must event processing follow a time frontier, how does model time differ from live availability, and what does a DB transaction prove across phases?

[LEAN's algorithm engine](https://www.quantconnect.com/docs/v2/writing-algorithms/key-concepts/algorithm-engine) describes chronological streaming and a time frontier rather than exposing future data indiscriminately. [QuantConnect reconciliation](https://www.quantconnect.com/docs/v2/cloud-platform/live-trading/reconciliation) warns that simulated timing, data availability and fills differ from live trading. The application here is an inference: advance modeled fills and risk together, but retain the real lease clock and label bar-open fills ex-post. This does not establish LEAN parity or trading profitability.

[SQLite isolation](https://sqlite.org/isolation.html) establishes transaction visibility, not atomicity across several committed application phases. Accordingly, recovery reads durable journal boundaries instead of treating successful snapshot import or a live process as completed policy execution.

## Remaining limits

Already committed gaps before the newest performance are not backfilled or rewritten; changing those histories needs explicit migration/evaluation semantics. No missing market dates are inferred and intermediate signals are not turned into stale new orders. Receipt expiry is not extended for catch-up. Full replay remains history-dependent, and a long phase can still exhaust its lease.

The standalone performance-only `paper-run-due`/`paper-run-loop` composition retains its latest-close contract and does not own this fill loop; manually importing/filling/evaluating through separate commands is not the chronological local execution contract. Durable delivery/source completeness, larger histories, target-host contention and broader paper/shadow readiness remain open. No external push, deployment, credential change or live action is part of this checkpoint.
