# G1.14 FIFO allocation policy TDD evidence

Source: [`../../gates/g1j-fifo-allocation-policy.md`](../../gates/g1j-fifo-allocation-policy.md).

## RED -> GREEN

- RED `5a1ac93`: `TestRecurringFIFOAllocationUsesVersionedResidualPolicy` failed because `1/3` had no finite decimal representation and apply rolled back.
- GREEN `d9fac4a`: `fifo_exact_else_half_even_residual_8_v1` accepts the recurring allocation, preserves exact finite predecessors and pins the version across Go, OpenAPI, fixtures and Flutter.

| Guarantee | Runnable check | Result |
|---|---|---|
| Repeated thirds preserve residual and close to exact total cost | `TestRecurringFIFOAllocationUsesVersionedResidualPolicy` | PASS |
| Half-even ties and tiny recurring cost are deterministic and bounded | `TestFIFOQuantizationUsesHalfEvenAndCannotExceedTinyLotCost` | PASS |
| Previously exact `1/2048` allocation is unchanged | `TestFIFOQuantizationUsesHalfEvenAndCannotExceedTinyLotCost` | PASS |
| Split and multi-lot closure conserve BUY cost | `TestFIFOResidualConservesCostAcrossSplitAndMultipleLots` | PASS |
| Snapshot/OpenAPI pin the analytical version | `TestOpenAPIPinsAnalyticalFIFOAllocationPolicy` | PASS |
| Legacy golden without the new field restores under compatible v1 | `TestGoldenVerticalSliceAndBackupRestore` | PASS |
| Flutter rejects an unknown policy version | `golden preview and snapshot parse canonical decimal strings` | PASS |

## Verification

- `make check`: PASS - Go vet/tests, Flutter analyze and 57 tests, Python compile and 13 tests, 15 JSON contracts.
- `make smoke`: PASS, including the exact snapshot policy version.
- `go test -race -count=1 ./...`: PASS.
- `govulncheck ./...`: `No vulnerabilities found.`
- `go test -cover -count=1 ./...`: 78.1% statement coverage.

## Promotion boundary

This policy makes analytical ledger replay deterministic; it does not certify tax filing basis, broker adjusted basis, historical FX, display rounding or public portfolio performance.
