# G1.14 Versioned FIFO Cost-Allocation Gate

Scope: deterministic analytical cost allocation for partial FIFO lots whose exact proportional cost is recurring. This is not a jurisdictional tax-basis or display-rounding policy.

## Pass evidence

- [x] `PortfolioSnapshot.cost_basis_policy` and OpenAPI pin `fifo_exact_else_half_even_residual_8_v1`; Flutter accepts only that version.
- [x] Full-lot closes consume the exact remaining cost, and partial allocations with a finite decimal representation remain exact for backward compatibility.
- [x] Only recurring partial allocations are rounded half-even at `max(8, current lot-cost decimal scale)`.
- [x] The rounded allocation is nonnegative and cannot exceed current lot cost; its exact residual stays with the open lot.
- [x] Repeated partial sells, split quantities and multi-lot sells conserve each lot's lifetime BUY cost when the lot closes.
- [x] A legacy golden snapshot with no policy field normalizes to v1 during restore; an explicit unknown version still fails comparison/parsing.
- [x] Existing schema v11 and backup v7 remain valid because every allocation that previously committed is numerically unchanged; recurring allocations previously failed before commit.

## Evidence

- RED `5a1ac93`: the recurring `1/3` allocation still failed the atomic apply.
- GREEN `d9fac4a`: exact-if-finite allocation, half-even recurring quantization, residual conservation, snapshot/OpenAPI/Flutter version pinning and legacy golden compatibility pass.
- Compatibility regressions preserve exact `1/2048`, bound a tiny recurring allocation, close repeated thirds, and conserve two split FIFO lots across a multi-lot sell.
- `make check`, `make smoke`, `go test -race -count=1 ./...`, `govulncheck ./...` and 78.1% Go statement coverage pass locally on 2026-08-29 KST.
- Independent review: always quantizing every partial allocation was NO-GO because it changed previously valid ledgers; corrected exact-if-finite arithmetic and compatibility are GO.

## Policy boundary

- Allocation is per canonical SELL event, per FIFO lot, in replay order. Temporary realized PnL can differ at the last quantized digit when one sell is split into multiple events; final lot closure consumes the residual exactly.
- The policy is analytical. The [IRS FIFO description](https://www.irs.gov/pub/irs-pdf/p550.pdf) is supporting evidence for oldest-lot ordering in its US scope, not a universal tax claim.
- Half-even follows the published [General Decimal Arithmetic Specification](https://speleotrove.com/decimal/decarith.pdf). Exact arithmetic and finite-decimal detection use Go [`math/big.Rat`](https://pkg.go.dev/math/big); `Rat.FloatString` is not used because it rounds ties away from zero.
- A future policy version must persist a selector and ship schema/backup migration plus old/new golden replay evidence before changing existing ledger results.

## Deliberately open

- Jurisdiction-specific tax-lot selection/reporting, broker-reported adjusted basis and correction reconciliation.
- Historical FX cost semantics, currency minor-unit/display rounding and public performance valuation.
- Corporate actions beyond the existing split contract, including merger/spinoff basis allocation.
