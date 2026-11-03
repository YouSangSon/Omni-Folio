# G3.8F2 always-on local paper runner — TDD record

## RED

The new focused tests first failed because migration 021, the runner lease, exact C3/D/E claim validation, `paper-run-loop`, signal cleanup, and backup v15 did not exist. Boundary tests also exposed and prevented four unsafe implementations: checking fence overflow before recovery, weakening the available-close cutoff to one timestamp, hiding a release error after a fatal/canceled loop, and allowing manual selection to strand an expired claim's incomplete C3/D/E prefix.

## GREEN

The minimum implementation reuses SQLite constraints, existing recovery proofs, C3/D/E journals, Go contexts, timers, and OS signal handling:

- one global strict singleton lease matching the current global strategy selection;
- owner/account/selection/fence-bound acquire, heartbeat, conditional release, and stale takeover;
- exact in-transaction validation and final renewal for every durable stage;
- immediate due run, six-heartbeat cadence, typed idle states, and bounded cleanup;
- expired partial-prefix protection while preserving the released-prefix manual selection contract;
- backup v15/schema v21 with owned-copy v14 migration.

No new dependency, service, public route, UI, container, PID file, or broker adapter was added.

## REFACTOR AND REGRESSION

The one-shot runner now uses the same fenced path, while completed retries still prove the recovery root. Existing migration fixtures were updated only where schema 21 changes the latest-version or foreign-key inventory boundary. Focused/full/race, `make check`, smoke, cleanup self-tests, contract validation, and `govulncheck` pass; command evidence is recorded in [`../../gates/g3p-always-on-paper-runner.md`](../../gates/g3p-always-on-paper-runner.md).
