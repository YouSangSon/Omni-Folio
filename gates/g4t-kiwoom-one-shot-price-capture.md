# G4T Kiwoom One-Shot Latest-Trade Capture Gate

Scope: credential-free internal composition of the existing G4Q `LatestTrade` read and G4S append-only price-observation write. No credential, external verification request, runtime caller, retry loop, scheduler, route, Flutter surface, valuation authority, order decision or live-money path is added.

## Acceptance

- [x] Invalid service, client or KRX six-digit symbol fails before network access.
- [x] Capture and direct Kiwoom writes derive `instrument_<lowercase symbol>` with the same helper as ledger import; callers cannot bind an alternate internal instrument ID.
- [x] One valid capture delegates to the existing Kiwoom read transport and then the existing durable observation writer without an additional capture-layer retry.
- [x] Re-fetching the same observed slot and price returns the first durable observation and does not append another row.
- [x] Provider failure and same-slot/different-price conflict return no observation and preserve the prior series and recovery proof.
- [x] Existing `kiwoom_mock`/`kiwoom_production` source mapping and `local_fixture`-only valuation boundary remain unchanged.
- [x] No schema or backup-format change is required.

## Evidence

- RED: `cd services/core && go test . -run '^TestKiwoomLatestTradeCapturePersistsOnceAndPreservesKnownGood$' -count=1` failed because `captureKiwoomLatestTradeObservation` did not exist.
- GREEN: the same focused command passes with synthetic token/chart responses and no broker credential or external request.
- Hardening RED: the focused G4T/G4S tests failed to compile after removing caller-provided instrument IDs from their expected contract.
- Hardening GREEN: `cd services/core && go test ./... -run 'TestKiwoomLatestTrade(CapturePersistsOnceAndPreservesKnownGood|RecordsDurablePriceObservation)'` passes and proves `005930 -> instrument_005930` plus direct-write rejection of `krx_005930`.
- 2026-08-30 KST: `make check`, `make smoke`, `go test -race -count=1 ./...` and `govulncheck ./...` pass; `govulncheck` reports no vulnerabilities.
- Independent read-only review returned GO with no blocking findings and zero edits.
- Executable coverage includes valid capture, later identical fetch, provider 500 without capture-layer retry, same-second changed-price conflict, proof preservation and invalid identity before network.

## Still Open

- A production runtime caller, credentialed observation and scheduled or realtime ingestion.
- A durable owner-declared instrument/listing registry for symbol changes, multiple venues and identifier corrections; the current rule only aligns existing local ledger and Kiwoom identities.
- Official timezone, freshness, retention, source-priority and market-calendar policy.
- Server-trusted cutoff and any use of Kiwoom observations in valuation, strategies, orders or public UI.
