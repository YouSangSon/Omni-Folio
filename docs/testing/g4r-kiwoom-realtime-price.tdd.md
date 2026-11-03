# G4R Kiwoom realtime-price TDD evidence

Source: [`../../gates/g4r-kiwoom-realtime-price.md`](../../gates/g4r-kiwoom-realtime-price.md).

## RED -> GREEN

- RED `2e80583`: the focused test failed to compile because the registration builder, realtime parser and DTO did not exist.
- GREEN `35116ea`: the stdlib-only internal contract preserves exact price, naive provider clock and separate receive time.
- Review RED `dac201e`: tests exposed an unproven fixed KRX label and missing byte/entry limits.
- Review GREEN `4da510f`: the DTO omits venue, frames are rejected above 1 MiB or 100 entries, and focused/full Go tests pass.

| Guarantee | Test | Result |
|---|---|---|
| Official single-symbol `0B` registration shape | `TestKiwoomRealtimePricesPreserveNaiveProviderClock` | PASS |
| Exact price, naive clock, UTC receive time and safe equal-price dedupe | `TestKiwoomRealtimePricesPreserveNaiveProviderClock` | PASS |
| Control/missing/empty/mixed/malformed/numeric/trailing frames fail closed | `TestKiwoomRealtimePricesFailClosed` | PASS |
| Same-clock different-price ambiguity and invalid registration/receive time fail closed | `TestKiwoomRealtimePricesFailClosed` | PASS |
| Unproven exchange FID cannot become a venue claim | `TestKiwoomRealtimePricesPreserveNaiveProviderClock` | PASS |
| Oversized byte and entry counts are rejected before partial output | `TestKiwoomRealtimePricesFailClosed` | PASS |

## Verification

- `go test -run '^TestKiwoomRealtimePrices' -count=1 ./...`: PASS.
- `make check`: PASS - Go vet/tests, Flutter analyze and 57 tests, Python compile and 13 tests, 15 JSON contracts.
- `make smoke`: PASS.
- `go test -race -count=1 ./...`: PASS.
- `govulncheck ./...`: `No vulnerabilities found.`
- `go test -cover -count=1 ./...`: 78.1% statement coverage.
- Independent review: initial NO-GO for unproven exchange and unbounded input; corrected re-review GO.

## Promotion boundary

This is a parser contract, not a connected stream. It intentionally does not infer a provider date, exchange, event identity or freshness and adds no WebSocket dependency, network loop, credential, persistence or consumer.
