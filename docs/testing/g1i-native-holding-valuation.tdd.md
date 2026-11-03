# G1.13 native-currency holding valuation TDD evidence

Source: [`../../gates/g1i-native-holding-valuation.md`](../../gates/g1i-native-holding-valuation.md). The bounded journey is: as the portfolio owner, I need reproducible native-currency holding value and unrealized PnL from proven ledger state and durable price evidence, without presenting local fixtures as live quotes or inventing an FX policy.

## RED -> GREEN

- Identity RED `2cc64e9`: `TestSecurityPriceObservationAcceptsCanonicalInternalInstrumentID` reproduced the mismatch between ledger ID `instrument_aapl` and the price writer's uppercase-only validation.
- Identity GREEN `0ad1dae`: the shared price-observation boundary accepts safe canonical internal IDs while symbol, venue and currency remain uppercase market identifiers.
- Valuation RED `b6f81de`: the holding-valuation tests failed to compile because the internal read model did not exist.
- Valuation GREEN `186cd2f`: `native_holding_valuation_v1` passed its focused native-math, provenance, availability and ledger-corruption contract.
- Historical accounting boundary `c1406bc`: `TestRecurringFIFOAllocationFailsClosedAtomically` proved that recurring rational allocation returned `invalid_ledger` and committed neither events nor revision before G1.14. That runtime behavior is superseded by the versioned policy in [`../../gates/g1j-fifo-allocation-policy.md`](../../gates/g1j-fifo-allocation-policy.md).
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
| Pre-G1.14 recurring FIFO rejection was atomic | `c1406bc` historical checkpoint; superseded by G1.14 | historical boundary | PASS at checkpoint |

## Commands and coverage

- `go test ./... -run 'HoldingValuation|RecurringFIFOAllocation|SecurityPriceObservationAcceptsCanonicalInternalInstrumentID' -count=1`: PASS.
- `make check`: PASS - Go vet/tests, Flutter analyze and 57 tests, Python compile and 13 tests, 15 JSON contracts.
- `make smoke`: PASS - health, status, preview, apply, snapshot, activity and market-data path.
- `go test -race ./...`: PASS.
- `govulncheck ./...`: `No vulnerabilities found.`
- `go test -cover ./... -count=1`: repository aggregate 77.9% statement coverage.
- Independent review: GO with no blocking findings; the promotion-only regressions below remain open.

## Required before public promotion

- Jurisdictional tax-basis rules and display/currency rounding remain separate follow-ups; the analytical recurring FIFO allocation prerequisite is closed by G1.14.
- Add a real durable price-ingestion path and a server-trusted public cutoff before Flutter consumes valuation. Device `DateTime.now()` is not valuation authority.
- Public route, OpenAPI, Flutter UI, provider ingestion and live authority remain out of scope; schema v11, backup v7 and snapshot valuation status remain unchanged.
