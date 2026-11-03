# G4Q Kiwoom latest-trade TDD evidence

Source: [`../../gates/g4q-kiwoom-latest-trade.md`](../../gates/g4q-kiwoom-latest-trade.md).

## RED -> GREEN

- RED `f00230e`: `TestKiwoomLatestTradeUsesProviderTimeAndExistingReadTransport` failed to compile because the latest-trade contract did not exist.
- GREEN `ea86544`: `ka10079` reuses the closed read transport and returns canonical provider/fetch time plus exact price without adding persistence or a public surface.
- Review RED `cb1acb3`: two different prices at the same second incorrectly returned the first row as latest.
- Review GREEN `cf5c15c`: same-second equal prices remain deterministic; different prices fail with `ambiguous_trade_time`.

| Guarantee | Test | Result |
|---|---|---|
| Fixed official request and existing OAuth/API-ID/path boundary | `TestKiwoomLatestTradeUsesProviderTimeAndExistingReadTransport` | PASS |
| Exact signed-price magnitude and KST-to-UTC provider time | `TestKiwoomLatestTradeUsesProviderTimeAndExistingReadTransport` | PASS |
| Missing result, symbol mismatch, invalid row, wrong order, same-second ambiguity and future time fail closed | `TestKiwoomLatestTradeFailsClosed` | PASS |
| Empty response remains not-found and invalid input never reaches transport | `TestKiwoomLatestTradeFailsClosed` | PASS |
| `ka10079` stays inside the closed read allowlist | `TestKiwoomRejectsMockNXTAndNonReadAPIIDsBeforeNetwork` | PASS |

## Verification

- `go test -run '^TestKiwoomLatestTrade' -count=1 ./...`: PASS.
- `make check`: PASS - Go vet/tests, Flutter analyze and 57 tests, Python compile and 13 tests, 15 JSON contracts.
- `make smoke`: PASS.
- `go test -race -count=1 ./...`: PASS.
- `govulncheck ./...`: `No vulnerabilities found.`
- `go test -cover -count=1 ./...`: 78.0% statement coverage.
- Independent review: initial NO-GO for same-second ambiguity; corrected re-review GO.

## Promotion boundary

The official sample does not provide a collision-safe durable event ID or explicit timezone/price-adjustment provenance. G4Q therefore stops at internal normalization; adding a guessed source identity or wiring G1.12/G1.13 would weaken replay and no-lookahead guarantees.
