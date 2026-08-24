# G4J / K2B2 Kiwoom Mock Submit Transport Gate

Scope: Go core internal-only, credential-free synthetic Kiwoom mock `kt10000` LIMIT BUY submit. The transport is in-memory; no real credential, external broker request, public route, Flutter order UI, runner or live authority is used.

## Acceptance

- [x] **Official request boundary:** mock-only `POST /api/dostk/ordr`, `api-id=kt10000`, KRX symbol, quantity, limit price and normal LIMIT trade type are emitted exactly after K2C authorization.
  - CHECK: `cd services/core && go test -run '^TestK2B2MockLimitSubmitAcknowledgesOnceAndRedactsProviderIdentity$' -count=1 ./...`
- [x] **Durable-before-write and one-shot submit:** token preflight precedes the atomic reservation/dispatch commit; the order write is never retried, including HTTP 401, and repeat calls cannot reach transport.
  - CHECK: `cd services/core && go test -run '^TestK2B2MockLimitSubmitDistinguishesRejectUnknownAndPreflightFailure$' -count=1 ./...`
- [x] **Conservative outcomes:** a valid ACK stores only an account-scoped HMAC order alias; explicit provider rejection becomes `REJECTED`; network/auth uncertainty remains `SUBMIT_UNKNOWN` for later lookup.
  - CHECK: `cd services/core && go test -run '^TestK2B2' -count=1 ./...`
- [x] **Secret and identity boundary:** raw provider order number, provider messages and access token are absent from durable order state/events and returned errors.
  - CHECK: `cd services/core && go test -run '^TestK2B2' -count=1 ./...`

The request and response fields are pinned to the [official Kiwoom order specification](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/5677b0efedda2525ba278787c24be66b57ef222b/kiwoom_docs/%EC%A3%BC%EB%AC%B8.md) at official repository commit [`5677b0e`](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/commit/5677b0efedda2525ba278787c24be66b57ef222b).

## Verification

- 2026-08-24 KST: focused K2B2 tests, full `make check`, `make smoke`, full Go race and `govulncheck ./...` passed locally.
- No real credential or broker call was made. Test artifacts and smoke processes were reclaimed by their owning commands.

## Deferred

- Credentialed Kiwoom mock observation, rate-limit behavior and authoritative provider semantics.
- Query/correlation for a lost submit response; tuple or time similarity still cannot resolve `SUBMIT_UNKNOWN`.
- SELL authorization, market/amend/cancel transport, public OpenAPI/Flutter flow, production risk, paper runner, fill-to-ledger reconciliation and every live-money path.
