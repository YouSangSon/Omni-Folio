# G1.10 direct FX observation storage

Scope: credential-free, append-only storage and a sanitized exact-direction GET for direct FX observations. This gate does not calculate a portfolio valuation or expose mutation/UI authority.

## Acceptance

- [x] G1F1: schema v10 adds one `STRICT`, insert-only `fx_observations` table and preserves v1-v9 data.
- [x] G1F2: one observation records an opaque identity, `local_fixture` source, distinct uppercase base/quote currencies, a positive canonical decimal rate, canonical UTC `observed_at`, and durable `recorded_at`.
- [x] G1F3: `rate` means quote-currency units per one base-currency unit. No reciprocal, cross-rate, interpolation, or rate inferred from `FX_EXCHANGE` is created.
- [x] G1F4: exact identity replay is idempotent; changed identity payloads and conflicting source/pair/time slots fail closed without mutating prior observations.
- [x] G1F5: canonical replay detects malformed metadata, row/hash mismatch, update/delete weakening, and deterministic-series drift.
- [x] G1F6: backup v6/schema v10 records FX digest/count in the verification receipt. Legacy v8/v9 backup artifacts are verified only through an owned migrated copy and remain unchanged.
- [x] G1F7: the GET exposes only exact local-fixture sample/stale evidence; `PortfolioSnapshot.valuation_status` remains `unavailable`, and no mutation route, Flutter surface, provider call, credential, scheduler, or live authority is added.

## Evidence commands

- Focused: `cd services/core && go test ./... -run 'FXObservation|SchemaMigratesV1ToV10' -count=1`
- Regression: `make check && make smoke`

## Evidence

- RED: `go test ./... -run 'FXObservation|SchemaMigratesV1ToV10' -count=1` failed at compile time because the observation types, writer, replay and backup proof did not exist; checkpoint `26e285a` preserves the executable contract.
- GREEN: the same focused command and the full `go test ./... -count=1` pass after the schema/storage/read/recovery implementation.
- Root: `make check` passes Go vet/tests, Flutter analyze and 56 tests, Python compile and 13 tests, plus 15 JSON contract parses. `make smoke` passes the temporary-DB HTTP slice.
- Safety: `go test -race ./... -count=1` passes and `govulncheck ./...` reports no vulnerabilities.
- Review: independent review found and then verified fixes for route/gate scope, invalid-query 400 behavior and mixed legacy-manifest rejection.

## Explicitly open

- As-of selection and freshness policy, cash/holding base-currency valuation, prices, PnL, TWR/XIRR, inverse/cross FX, provider ingestion, correction, broker reconciliation, UI, and every live-money path.
