# G4G / K2B1 Kiwoom Dated Execution Scan Gate

Scope: Go core internal-only, credential-free synthetic `kt00009` transport and normalization for one explicit order date. No credential, broker request, public route, Flutter order UI, persistence, order-event mutation, K2B0 reconciliation, unknown-submit correlation, timezone inference or live submit.

## Acceptance

- [x] **Fixed read boundary:** `scanDatedExecutions` issues only `POST /api/dostk/acnt` with API ID `kt00009` and the fixed stock/KRX/fills-only body; the caller must supply a canonical `YYYY-MM-DD` date before any network call.
  - CHECK: `cd services/core && go test -run '^TestK2B1DatedExecutionScanPaginatesAndNormalizesWithoutCompletenessClaim$|^TestK2B1DatedExecutionScanRejectsInvalidDatesBeforeNetwork$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **Strict synthetic normalization:** only `A`-prefixed KRX stock rows, cash buy/sell, non-empty provider order type, positive integer quantities, non-negative order price, positive fill price, non-zero seven-digit order/execution IDs and valid `HH:mm:ss` execution clocks are accepted.
  - CHECK: `cd services/core && go test -run '^TestK2B1DatedExecutionNormalizerRejectsUnsafeRows$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **Scoped opaque identity:** account, environment, requested date, provider order number and execution number qualify distinct `dated_order`/`dated_execution` aliases; these aliases cannot enter K2A/K2B0 durable order-event fields.
  - CHECK: `cd services/core && go test -run '^TestK2B1DatedExecutionAliasesAreDateAndEnvironmentScoped$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **All-or-nothing pages:** every page must have an explicit zero `return_code` and execution array. Duplicate executions, changed order tuples or provider order types, cumulative overfills and later-page transport failures return no scan.
  - CHECK: `cd services/core && go test -run '^TestK2B1DatedExecutionScan' -count=1 ./...`
  - EVIDENCE: PASS. Shared cursor, API-ID, page-cap, timeout and response-size behavior remains covered by the existing `TestKiwoom*` transport suite.
- [x] **No completeness or timezone fabrication:** `PaginationComplete=true` means only that the explicit request reached a terminal cursor. `ExecutionsComplete` stays false, and the requested date is never combined with provider-local `ExecutionClock` to manufacture UTC.
  - CHECK: `cd services/core && go test -run '^TestK2B1DatedExecutionScanPaginatesAndNormalizesWithoutCompletenessClaim$|^TestK2B1DatedExecutionScanAcceptsExplicitEmptyArray$' -count=1 ./...`
  - EVIDENCE: PASS. An explicit empty array means zero rows in this query only, not “order does not exist.”

## Official contract boundary

- The field names and fixed request come from the [official Kiwoom account specification](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/main/kiwoom_docs/%EA%B3%84%EC%A2%8C.md), reviewed at official repository commit [`6964258`](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/commit/69642586f7d84ba9fd8a6faf1f1537c7fda6568b).
- Official material does not define execution-clock timezone, date retention, row ordering, snapshot consistency across pages, ID uniqueness lifetime, client-order idempotency or lost-submit correlation. This leaf therefore preserves a naive clock and date-scoped, non-joinable aliases.

## Verification

- Code checkpoint: `d93cdee`.
- `make check`, `make smoke`, `cd services/core && go test -race ./... -count=1`, `git diff --check` and staged secret-pattern scan pass locally on 2026-08-24 KST.
- Independent architecture and test re-reviews report GO with no remaining P0/P1/P2 finding for this synthetic leaf.

## Deferred to K2B

- Credentialed Kiwoom mock observation of response shape, continuation stability, empty-result behavior, timezone, retention, order types, identifier scope and rate-limit behavior.
- A broker-guaranteed or empirically proven lost-submit correlation contract; tuple/time similarity and this dated scan are insufficient.
- Durable known-good persistence, scheduling, K2B0 mapping, public OpenAPI/Flutter order flow, production risk policy, broker-coupled reservation and fencing. K2C covers only credential-free internal BUY authority.
- Submit/cancel/amend transport, ledger mutation, market orders and every real-money gate.
