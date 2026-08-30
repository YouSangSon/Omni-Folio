# G1.13 native-currency holding valuation TDD evidence

Source: [`../../gates/g1i-native-holding-valuation.md`](../../gates/g1i-native-holding-valuation.md). The bounded journey is: as the portfolio owner, I need reproducible native-currency holding value and unrealized PnL from proven ledger state and durable price evidence, without presenting local fixtures as live quotes or inventing an FX/rounding policy.

## RED -> GREEN

- Identity RED `2cc64e9`: `TestSecurityPriceObservationAcceptsCanonicalInternalInstrumentID` reproduced the mismatch between ledger ID `instrument_aapl` and the price writer's uppercase-only validation.
- Identity GREEN `0ad1dae`: the shared price-observation boundary accepts safe canonical internal IDs while symbol, venue and currency remain uppercase market identifiers.
- Valuation RED `b6f81de`: the holding-valuation tests failed to compile because the internal read model did not exist.
- Valuation GREEN `186cd2f`: `native_holding_valuation_v1` passed its focused native-math, provenance, availability and ledger-corruption contract.
- Accounting boundary `c1406bc`: `TestRecurringFIFOAllocationFailsClosedAtomically` locks the current behavior until a versioned quantization policy exists: recurring rational allocation returns `invalid_ledger` and commits neither events nor revision.
- Promotion regression `10d4db8`: the full read model chooses the newest same-venue observation and separately excludes future observed, fetched and recorded times.

| Guarantee | Test/evidence | Type | Result |
|---|---|---|---|
| Ledger and durable-price state are reconstructed and proved inside one read-only transaction | `TestHoldingValuationUsesExactNativePriceAndKeepsSnapshotUnavailable` plus implementation trace | integration | PASS |
| Exact native quantity x price, unrealized PnL and per-currency totals retain sample/stale provenance | `TestHoldingValuationUsesExactNativePriceAndKeepsSnapshotUnavailable` | accounting/provenance | PASS |
| Exact instrument, symbol and currency matching requires one unique eligible as-of venue | `TestHoldingValuationSuppressesTotalsAndFailsClosed` | identity boundary | PASS |
| Missing, ambiguous, older-than-24h and future-recorded evidence suppress totals | `TestHoldingValuationSuppressesTotalsAndFailsClosed` | availability/no-lookahead | PASS |
| Same-venue selection uses the newest eligible observation | `TestHoldingValuationUsesExactNativePriceAndKeepsSnapshotUnavailable` | deterministic selection | PASS |
| Future observed, fetched and recorded evidence cannot enter valuation | `TestHoldingValuationSuppressesTotalsAndFailsClosed` | no-lookahead | PASS |
| The 24-hour observation-age boundary is inclusive | `TestHoldingValuationUsesExactNativePriceAndKeepsSnapshotUnavailable` | freshness policy | PASS |
| Corrupt ledger revision returns a sanitized internal error | `TestHoldingValuationSuppressesTotalsAndFailsClosed/corrupt_ledger_revision` | recovery/trust boundary | PASS |
| Internal ledger IDs are accepted by durable price storage | `TestSecurityPriceObservationAcceptsCanonicalInternalInstrumentID` | prerequisite regression | PASS |
| Recurring rational FIFO allocation cannot partially mutate the ledger | `TestRecurringFIFOAllocationFailsClosedAtomically` | atomicity boundary | PASS |

## Commands and coverage

- `go test ./... -run 'HoldingValuation|RecurringFIFOAllocation|SecurityPriceObservationAcceptsCanonicalInternalInstrumentID' -count=1`: PASS.
- `make check`: PASS - Go vet/tests, Flutter analyze and 57 tests, Python compile and 13 tests, 15 JSON contracts.
- `make smoke`: PASS - health, status, preview, apply, snapshot, activity and market-data path.
- `go test -race ./...`: PASS.
- `govulncheck ./...`: `No vulnerabilities found.`
- `go test -cover ./... -count=1`: repository aggregate 77.9% statement coverage.
- Independent review: GO with no blocking findings; the promotion-only regressions below remain open.

## Required before public promotion

- Define a versioned rounding/quantization and residual-allocation policy. Until then, recurring rational FIFO allocations deliberately roll back atomically.
- Add a real durable price-ingestion path and a server-trusted public cutoff before Flutter consumes valuation. Device `DateTime.now()` is not valuation authority.
- Public route, OpenAPI, Flutter UI, provider ingestion and live authority remain out of scope; schema v11, backup v7 and snapshot valuation status remain unchanged.
