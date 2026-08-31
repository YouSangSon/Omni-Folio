# G1.11 direct-FX cash valuation TDD evidence

Source: [`../../gates/g1g-direct-fx-cash-valuation.md`](../../gates/g1g-direct-fx-cash-valuation.md). The bounded journey is: as the portfolio owner, I need replay-verified current cash converted reproducibly into one requested base currency without pretending that sample FX is current or that holdings are valued.

## RED → GREEN

- RED `984ccee`: the focused target failed at compile time because the cash valuation service and route did not exist.
- GREEN `4153b47`: the minimal direct-pair, cash-only read model passed focused tests.
- Review RED `eaf8663`: a regression test showed that one event with `ledger_meta.revision=99` was still returned as a valid KRW 1000 valuation.
- Review GREEN `4373125`: the implementation reuses the existing full ledger proof in the same read transaction and checks revision/recorded-at against the snapshot. Independent re-review verdict: GO.

| Guarantee | Test/evidence | Type | Result |
|---|---|---|---|
| Exact direct pairs produce deterministic canonical totals; inverse/cross are not used | `TestCashValuationExactDirectAndNoInverseCross` | integration | PASS |
| Latest eligible canonical instant is selected and backup restore reproduces the result | `TestCashValuationSelectsLatestCanonicalInstantAndRestoresExactly` | recovery | PASS |
| 24-hour freshness is inclusive and missing/stale coverage suppresses the aggregate | `TestCashValuationAsOfFreshnessAndCoverage` | policy boundary | PASS |
| Invalid/future/ledger-preceding cutoffs and corrupt FX state fail closed | `TestCashValuationRejectsCutoffAndFailsClosed` | trust boundary | PASS |
| Corrupt ledger revision cannot be certified as a valuation | `TestCashValuationFailsClosedWhenLedgerRevisionIsCorrupt` | recovery | PASS |
| Impossible FX temporal order fails with a sanitized error | `TestFXObservationTemporalOrderFailsCashValuationClosed` | no-lookahead | PASS |
| GET/query/OpenAPI are closed, read-only and sanitized | `TestCashValuationHTTPContractIsClosedReadOnlyAndSanitized` | API contract | PASS |

## Commands and coverage

- `go test ./... -run 'CashValuation|FXObservationTemporal' -count=1`: PASS.
- Focused statement coverage: `cashValuation` 89.1%, selection/provenance helpers 100%, handler 88.2%.
- `make check`: PASS — Go vet/tests, Flutter analyze and 57 tests, Python compile and 13 tests, 15 JSON contracts.
- `make smoke`: PASS.
- `go test -race ./... -count=1`: PASS.
- `govulncheck ./...`: `No vulnerabilities found.`
- `go test -cover ./... -count=1`: repository aggregate 77.5%.

Known gaps are deliberate later leaves: provider FX ingestion/source priority/calendar policy, inverse/cross and display rounding, security prices, holding/PnL/performance valuation, historical ledger valuation, Flutter UI, broker cash reconciliation and live authority.
