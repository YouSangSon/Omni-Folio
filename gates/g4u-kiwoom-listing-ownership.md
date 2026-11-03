# G4U Kiwoom Instrument Listing Ownership Gate

Scope: owner-declared current listing ownership for internal Kiwoom latest-trade capture and durable price writes. This gate adds no credential, provider lookup, runtime scheduler, public route, Flutter surface, valuation authority, strategy input, order authority or live-money path.

## Acceptance

- [x] Schema v13 stores only append-only `DECLARE` and `REVOKE` events for `(venue,symbol,currency) -> instrument_id`; exact active redeclaration is idempotent and a different binding requires revoke then declare.
- [x] SQLite and replay validation reject stale predecessors, invalid transitions, hash drift, direct update and direct delete.
- [x] Backup v8 binds the registry state hash, total event count and active count to both source and restored candidate; exact table, index and all triggers are required.
- [x] Legacy schema v8-v12 backups are verified only through an owned temporary copy. Migration creates an empty registry without rewriting prices or inferring ownership.
- [x] G4T resolves active `XKRX/symbol/KRW` ownership before `LatestTrade`; missing, revoked or corrupt state produces zero transport calls.
- [x] G4S and the generic Kiwoom writer re-resolve the same ownership inside the insert transaction and reject a mismatched instrument.
- [x] Owner-declared instrument IDs, rather than `instrument_<symbol>` convention, determine new Kiwoom price identity.
- [x] Exact legacy v12 Kiwoom observations remain replayable, but cannot authorize a distinct new observation without an explicit matching declaration.
- [x] Existing `local_fixture` valuation and public API boundaries remain unchanged.

## Evidence

- Registry RED: `cd services/core && go test . -run '^TestInstrumentListingDeclareResolveRevokeAndCorrect$' -count=1` failed to compile before the listing types and functions existed.
- Registry GREEN: the same test passes after migration 013 and append-only replay were implemented.
- Enforcement RED: the focused G4U tests reported one network call for an unlisted symbol and stored `instrument_005930` instead of the owner-declared instrument.
- Enforcement GREEN: `cd services/core && go test . -run '^(TestInstrumentListing|TestKiwoomCaptureRequiresActiveListingBeforeNetwork|TestKiwoomListingControlsInstrumentAndWriteRace|TestLegacyV12KiwoomPriceRemainsReplayableButUnowned)$' -count=1` passes.
- Full core: `cd services/core && go test ./... -count=1` and `go test -race ./... -count=1` pass.
- Repository: `make check` passes, including Go format/vet/test, Flutter analyze and 65 tests, Python compile and 13 tests, and 15 JSON contracts.
- Security: `govulncheck ./...` reports `No vulnerabilities found.`
- Local checkpoints: registry/backup `8937bc4`; Kiwoom enforcement/legacy preservation `97071c2`.
- Two independent read-only reviews returned GO with zero edits and no correctness, security or bypass finding. One reviewer noted that revoke-during-fetch is rejected by the post-fetch resolution before the transaction check, so the transaction-time race itself is supported by code inspection and direct-writer mismatch coverage rather than a production test hook.

## Still Open

- A public or owner-authenticated declaration workflow, provider security-master verification, names, ISIN/FIGI, lot/tick sizes and exchange calendars.
- Effective-dated historical listing ownership. Current declarations must not be used as historical performance or tax truth.
- Credentialed Kiwoom observation, official timezone/freshness/retention, scheduled or realtime ingestion and source priority.
- Server-trusted valuation cutoff, strategy use, order allowlists and every live-money path.
- Full-log replay is intentionally linear for the current personal/single-node scope; add a verified projection only after measured volume makes it hot.
