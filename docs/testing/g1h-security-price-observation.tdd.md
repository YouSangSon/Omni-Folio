# G1.12 durable security price observation TDD evidence

Source: [`../../gates/g1h-security-price-observation.md`](../../gates/g1h-security-price-observation.md). The bounded journey is: as the portfolio owner, I need replayable price evidence before holding valuation, without presenting a local fixture as a current quote.

## RED -> GREEN

- RED `cb35559`: the focused target failed at compile time because the observation input, writer, replay and exact as-of helper did not exist.
- GREEN `5740212`: schema v11 storage, backup v7 proof, legacy v6/schema-v10 copy migration and the internal helper passed focused and full core tests.
- Review RED: an empty backup with a weakened `price_adjustment` CHECK still passed restore verification.
- Review GREEN: restore now compares the normalized table DDL with embedded migration 011; the exact tamper regression passes and independent re-review verdict is GO.

| Guarantee | Test/evidence | Result |
|---|---|---|
| Replay/idempotency/conflict and unchanged portfolio authority | `TestSecurityPriceObservationRecordReplayConflictAndSnapshotBoundary` | PASS |
| Boundary validation, STRICT storage and insert-only mutation guards | `TestSecurityPriceObservationRejectsInvalidBoundaryAndDirectMutation` | PASS |
| Exact identity and no future observed/fetched/recorded knowledge | `TestSecurityPriceObservationLatestIsExactAndExcludesFutureKnowledge` | PASS |
| Canonical hash corruption fails closed | `TestSecurityPriceObservationReplayFailsClosedOnCorruption` | PASS |
| Empty restored table cannot weaken its CHECK constraints | `TestSecurityPriceObservationRestoreRejectsWeakAdjustmentConstraint` | PASS |
| Backup digest/count and legacy v6/schema-v10 owned-copy migration | `TestSecurityPriceObservationBackupProofAndLegacyV10CopyMigration` | PASS |

## Commands and coverage

- `go test ./... -run 'SecurityPriceObservation|SchemaMigratesV1ToV11' -count=1`: PASS.
- Focused coverage: new security-price functions 77.8%-100%; repository statement coverage for the focused selector was 14.9%.
- `make check`: PASS - Go vet/tests, Flutter analyze and 57 tests, Python compile and 13 tests, 15 JSON contracts.
- `make smoke`: PASS.
- `go test -race ./... -count=1`: PASS.
- `govulncheck ./...`: `No vulnerabilities found.`
- `go test -cover ./... -count=1`: repository aggregate 77.6%.
- Independent review: initial NO-GO, blocker fixed by TDD, re-review GO with no unresolved finding.

Still open by design: provider ingestion, public API/UI, holding/cost/PnL/performance valuation, source priority and calendar/freshness policy, scheduler, credentials and every live-money path.
