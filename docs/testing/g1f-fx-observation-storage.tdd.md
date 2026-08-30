# G1.10 direct FX observation TDD evidence

Source: [`../../gates/g1f-fx-observation-storage.md`](../../gates/g1f-fx-observation-storage.md). The bounded journey is: as the portfolio owner, I need an exact, timestamped, directly observed FX series so later valuation can be reproduced without inferring a rate from ledger cash movements.

## RED → GREEN

- RED checkpoint `26e285a`: the focused Go target failed at compile time because `FXObservationInput`, recording, replay, read, schema v10 and backup proof did not exist.
- GREEN: the same focused target passes with the minimal storage/read/recovery implementation. Independent review initially found route-scope, invalid-query and mixed-legacy-manifest gaps; all three were fixed and the re-review verdict is GO.

| Guarantee | Test/evidence | Type | Result |
|---|---|---|---|
| Exact replay is idempotent; identity or pair/time conflicts fail closed | `TestFXObservationRecordReplayConflictAndExactSeries` | integration | PASS |
| Invalid decimal, currency, timestamp and direct SQL shapes are rejected | `TestFXObservationRejectsInvalidBoundaryAndDirectWrites` | trust boundary | PASS |
| Row-hash corruption and update/delete attempts are detected | `TestFXObservationRecoveryDetectsCorruptionAndMutation` | recovery | PASS |
| Backup v6 binds FX digest/count and rejects tampering | `TestFXObservationBackupProof` | recovery | PASS |
| Legacy v5/schema-v8 and v9 artifacts stay byte-identical and mixed v6 fields are rejected | `TestVerifyManifestMigratesV8BackupCopy`, `TestVerifyManifestMigratesV9BackupCopy` | compatibility | PASS |
| Exact-direction/as-of GET is sanitized, sample/stale, read-only and returns invalid values as 400 | `TestFXObservationLatestDirectReadIsExactAndSanitized`, `TestFXObservationOpenAPIIsClosedReadOnlyAndDirect` | API contract | PASS |
| Existing portfolio valuation authority is unchanged | snapshot assertion in `TestFXObservationRecordReplayConflictAndExactSeries` | regression | PASS |

## Commands and coverage

- `make check`: PASS — Go vet/tests, Flutter analyze and 56 tests, Python compile and 13 tests, 15 JSON contracts.
- `make smoke`: PASS.
- `go test -race ./... -count=1`: PASS.
- `govulncheck ./...`: `No vulnerabilities found.`
- `go test -cover ./... -count=1`: repository aggregate 77.0% (existing baseline); the new `fx_observation.go` statements are 98/117, 83.8%.

Known gaps are deliberate next leaves: provider ingestion, versioned freshness selection, cash/holding valuation, inverse/cross-rate policy, Flutter UI, correction and live authority.
