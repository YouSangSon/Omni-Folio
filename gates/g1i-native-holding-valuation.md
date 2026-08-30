# G1.13 native-currency holding valuation v1

Scope: credential-free, internal-only holding valuation from replay-verified ledger and durable local-fixture security prices. This gate does not promote valuation to a public API or Flutter surface.

## Acceptance

- [x] G1I1: `native_holding_valuation_v1` calculates exact native-currency market value, unrealized PnL and per-currency totals without FX conversion or rounding.
- [x] G1I2: one read-only transaction reconstructs the portfolio snapshot, proves the full ledger revision and replays the complete durable security-price log.
- [x] G1I3: price selection matches the holding's exact instrument ID, symbol and currency, then fails closed unless exactly one eligible venue exists as of the valuation cutoff.
- [x] G1I4: eligible observations require observed, fetched and recorded timestamps at or before `as_of`; observation age of exactly 24 hours is accepted.
- [x] G1I5: missing, ambiguous, stale or future-only price evidence leaves the affected line unavailable and suppresses every aggregate total.
- [x] G1I6: selected price provenance remains explicit `local_fixture` sample/stale data; it is never represented as a current or live quote.
- [x] G1I7: corrupt ledger proof returns a sanitized internal error rather than certifying a valuation.
- [x] G1I8: schema v11 and backup v7 remain unchanged, and `PortfolioSnapshot.valuation_status` remains `unavailable`.
- [x] G1I9: no route, OpenAPI contract, Flutter UI, provider integration, credential, scheduler or live-money authority is added.
- [x] G1I10: recurring rational FIFO allocation remains fail-closed and rolls the import back atomically while a versioned rounding/quantization policy is still open.

## Evidence commands

- Focused: `cd services/core && go test ./... -run 'HoldingValuation|RecurringFIFOAllocation|SecurityPriceObservationAcceptsCanonicalInternalInstrumentID' -count=1`
- Full repository: `make check && make smoke`

## Evidence

- Price identity prerequisite RED `2cc64e9`: the durable writer rejected the ledger's canonical internal instrument ID.
- Price identity prerequisite GREEN `0ad1dae`: price observations now accept the same safe internal instrument ID emitted by ledger replay while retaining strict symbol, venue and currency validation.
- Valuation RED `b6f81de`: the focused contract failed because the internal native-currency holding read model did not exist.
- Valuation GREEN `186cd2f`: exact native market value, unrealized PnL, per-currency totals, ledger proof and fail-closed durable-price selection were implemented without changing public authority.
- FIFO boundary `c1406bc`: finite inputs that imply a recurring rational cost allocation fail with `invalid_ledger` and leave event count and revision unchanged.
- Promotion regression `10d4db8`: the full read model selects the newest eligible row within one venue and excludes future observed, fetched and recorded timestamps.
- Independent review: GO with no blocking findings; public promotion tests remain listed below.
- Full verification: `make check`, `make smoke`, Go race, `govulncheck` and 77.9% Go statement coverage pass.

## Promotion gates still open

- Define and version a rounding/quantization plus residual-allocation policy before accepting recurring rational FIFO allocations.
- Keep the model internal until a real price-ingestion path exists and the public route owns a server-trusted cutoff; do not make Flutter depend on the mobile device clock.
- Add the closed read-only route, OpenAPI and accessible retained-state Flutter UX together only when the endpoint can return useful durable price evidence.
