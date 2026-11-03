# G1.12 durable security price observations v1

Scope: credential-free, local-fixture-only security-price evidence. This gate stores and replays prices; it does not value holdings or expose a market-data API/UI.

## Acceptance

- [x] G1H1: schema v11 adds a `STRICT`, insert-only `security_price_observations` table while preserving v1-v10 data.
- [x] G1H2: a row is local-fixture-only and binds source identity, instrument ID, symbol, venue, currency, positive canonical price, explicit `unspecified` adjustment, and canonical UTC observed/fetched/recorded times.
- [x] G1H3: identical source replay is idempotent; conflicting source identity or source/instrument/venue/currency/observed/adjustment slot fails closed.
- [x] G1H4: full replay validates row hashes and metadata; update/delete weakening or corruption fails closed.
- [x] G1H5: the internal exact-as-of helper accepts no substituted instrument/symbol/venue/currency/adjustment and excludes future observed, fetched, or recorded state.
- [x] G1H6: backup v7/schema v11 proves security-price digest/count; legacy v10/v6 artifacts are verified through an owned migrated copy and remain unchanged.
- [x] G1H7: `PortfolioSnapshot.valuation_status` remains `unavailable`; no holding/PnL/performance valuation, public route, Flutter UI, provider request, credential, scheduler, or live authority is added.

## Evidence commands

- RED/Focused: `cd services/core && go test ./... -run 'SecurityPriceObservation' -count=1`
- Regression after GREEN: `make check && make smoke`

Result: RED checkpoint `cb35559`, GREEN checkpoint `5740212`, independent re-review GO after `TestSecurityPriceObservationRestoreRejectsWeakAdjustmentConstraint` closed the weakened-empty-table restore gap. Full commands and coverage are recorded in [`../docs/testing/g1h-security-price-observation.tdd.md`](../docs/testing/g1h-security-price-observation.tdd.md).

## RED contract

`security_price_observation_test.go` defines the minimal internal writer, replay, exact-as-of helper, strict mutation guard, backup/restore proof, and legacy-copy behavior. It intentionally references no public endpoint or valuation surface.
