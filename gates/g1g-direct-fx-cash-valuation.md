# G1.11 direct-FX cash valuation v1

Scope: a credential-free, read-only valuation of replay-verified ledger cash into one requested base currency. Whole-portfolio valuation remains unavailable.

## Acceptance

- [x] G1G1: `direct_fx_cash_v1` runs ledger and full FX replay in one read transaction and values only the returned ledger revision.
- [x] G1G2: identity and zero cash need no FX row; every other line uses only the exact stored `cash currency -> base currency` direction.
- [x] G1G3: observation eligibility requires `observed_at <= fetched_at <= recorded_at <= valuation_as_of`; the 24-hour observation-age boundary is inclusive.
- [x] G1G4: inverse, cross, interpolation, `FX_EXCHANGE` inference, future knowledge and rounding are never used.
- [x] G1G5: missing or older-than-policy direct rates return all native cash lines but no aggregate subtotal; local fixture results remain machine-labelled sample/stale.
- [x] G1G6: exact rational multiplication and addition emit canonical decimal strings with sanitized selected-observation provenance.
- [x] G1G7: malformed/duplicate/unknown query parameters fail with 400, a cutoff before the current ledger state fails with 409, and corrupt durable state fails with a generic 500.
- [x] G1G8: the closed GET-only contract exposes no account/source-private/hash/sequence data, mutation, holdings, PnL or live authority.
- [x] G1G9: `PortfolioSnapshot.valuation_status` stays `unavailable`; no Flutter valuation surface, provider call, scheduler, schema or backup-format change is added.

## Evidence commands

- Focused: `cd services/core && go test ./... -run 'CashValuation|FXObservationTemporal' -count=1`
- Regression: `make check && make smoke`

## Evidence

- RED `984ccee`: cash valuation contract did not exist and the focused target failed to compile.
- GREEN `4153b47`: direct-FX cash-only read model and closed OpenAPI route passed focused tests.
- Review RED `eaf8663`: corrupt `ledger_meta.revision` was incorrectly certified, proving the missing full ledger proof.
- Review GREEN `4373125`: reused `proveLedgerEvents` in the same read transaction and matched proof revision/recorded-at to the snapshot.
- Independent re-review: GO; no blocking findings remain.
- `go test ./... -run 'CashValuation|FXObservationTemporal' -count=1`: PASS; `cashValuation` focused statement coverage 89.1%.
- `make check`: PASS — Go vet/tests, Flutter analyze and 57 tests, Python compile and 13 tests, 15 JSON contracts.
- `make smoke`: PASS.
- `go test -race ./... -count=1`: PASS.
- `govulncheck ./...`: `No vulnerabilities found.`
- `go test -cover ./... -count=1`: repository aggregate 77.5%.

## Explicitly open

Provider FX ingestion, source priority, market-calendar policy, inverse/cross FX, display/minor-unit rounding, security prices, holding/PnL/performance valuation, historical ledger valuation, Flutter UI, broker cash reconciliation and every live-money path.
