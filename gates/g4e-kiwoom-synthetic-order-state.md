# Gates: G4E / K2A Kiwoom Synthetic Order-State Log

Scope: Go core internal-only synthetic Kiwoom `LIMIT`/`KRW`/`KRX` order state. No network, credential, public route, Flutter order UI, broker lookup, market/amend order, ledger fill reconciliation, or live submit. G4I/K2C later supersedes the direct synthetic risk/dispatch setup with a credential-free authority; this file remains K2A historical evidence.

## Acceptance and evidence

- [x] **K2A1 client-order idempotency:** one opaque intent is recorded once; the same payload replays and the same key with a changed payload is rejected.
  - CHECK: `cd services/core && go test -run '^TestK2AIntentValidationAndClientOrderIdempotency$' -count=1 ./...`
  - EVIDENCE: PASS. Exact canonical integer/decimal, KRX/KRW/LIMIT and opaque Kiwoom alias boundaries are enforced.
- [x] **K2A2 unknown-submit safety:** risk verdict ordering is required; dispatch becomes durable `SUBMIT_UNKNOWN`; blind resubmit and account-wide new submit are blocked while a command is unresolved; cancel of a known open order remains allowed.
  - CHECK: `cd services/core && go test -run '^TestK2ARiskGateAndExplicitUnknownRecovery$' -count=1 ./...`
  - EVIDENCE: PASS. Risk policy/version/reason is not implemented; only verdict ordering is proven.
- [x] **K2A3 lifecycle replay:** partial fill, cancel unknown, cancel ack/reject, late fill, full fill and overfill rejection replay from append-only events.
  - CHECK: `cd services/core && go test -run '^TestK2APartialFillCancelRaceLateFillAndOverfill$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **K2A4 event identity:** raw provider IDs, invalid provenance, changed/cross-order provider refs, conflicting event IDs and provider execution aliases, malformed fills and overfills fail closed.
  - CHECK: `cd services/core && go test -run '^TestK2AEventAndProviderExecutionIdempotencyConflicts$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **K2A5 schema and restart:** migration v1→v4 preserves v1 data, readiness requires the exact contiguous migration history, and order/authority tables are insert-only.
  - CHECK: `cd services/core && go test -run '^TestK2AOrderTablesAreInsertOnly$|^TestSchemaMigratesV1ToV4AndReadinessRequiresV4$|^TestHealthAndReadinessAreSeparate$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **K2A6 order-aware backup:** originally passed as backup v2; current backup v4 retains source/candidate order digests/counts, row hash/metadata/full replay and schema protections while adding broker-state and K2C authority/reservation recovery evidence.
  - CHECK: `cd services/core && go test -run '^TestK2ABackup|^TestK2ARestore' -count=1 ./...`
  - EVIDENCE: PASS. Earlier backup formats are not eligible for automatic v4 activation.

## Verification

- TDD checkpoints: `35ae80e`, `69f6f86`, `37bbff4` RED; `132de8e` GREEN.
- `make check` passes Go vet/tests, 17 Flutter tests, 13 Python tests and 15 JSON contract files.
- `make smoke`, `cd services/core && go test -race -count=1 ./...`, `git diff --check` and staged secret-pattern scan pass.
- Independent final review reports no P0/P1 findings after the backup-schema corrections.

## Deferred to K2B

- Kiwoom mock credentials and real broker submit/query observations.
- Broker lookup/reconciliation that resolves `SUBMIT_UNKNOWN`.
- Public OpenAPI route and Flutter order review/lifecycle UI.
- Production risk beyond K2C's fixed credential-free BUY policy.
- Broker-coupled runner lease/fencing beyond K2C's internal 30-second account lease.
- Market orders, amend/correction and provider-specific capability handling.
- Ledger fill reconciliation and portfolio mutation.
- Production/live submit and every real-money gate.
