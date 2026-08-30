# G4Q Kiwoom Latest-Trade Gate

Scope: credential-free synthetic normalization of the official Kiwoom `ka10079` one-tick response. The transport is in memory; no real credential, external request, persistence, route, UI or live authority is used.

## Pass evidence

- [x] The closed read allowlist maps only `ka10079` to `POST /api/dostk/chart`, with fixed `stk_cd`, `tic_scope=1` and `upd_stkpc_tp=0` request fields.
- [x] KRX six-digit symbol, provider result, response symbol and every first-page tick row are validated before returning the first newest row.
- [x] Signed provider price is normalized as a positive exact decimal; provider `cntr_tm` and fetch time remain separate canonical UTC fields.
- [x] Empty, malformed, symbol-mismatched, unordered, same-second price-ambiguous and future-provider-time evidence fails closed without exposing provider messages.
- [x] The implementation reuses the existing OAuth/read transport, decimal parser, KST operating assumption and candle-row validator; no dependency or parallel client was added.

## Evidence

- RED `f00230e`: the focused contract failed because `LatestTrade` and `KiwoomLatestTrade` did not exist.
- GREEN `ea86544`: the focused latest-trade suite and the full Go core suite pass.
- Review regression RED `cb1acb3` and fix `cf5c15c`: second-level provider timestamps carrying different prices are rejected as ambiguous rather than selecting an arbitrary row.
- Independent re-review: GO after the ambiguity fix; no blocking code finding remains.
- `make check`, `make smoke`, `go test -race -count=1 ./...`, `govulncheck ./...` and 78.0% Go statement coverage pass locally on 2026-08-28 KST.
- Official source: Kiwoom repository commit [`9180deb`](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/commit/9180debf7aea0074715dd8f7a15af432afbfc403), [`get_domestic_stock_tick_chart.py`](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/9180debf7aea0074715dd8f7a15af432afbfc403/examples/%EA%B5%AD%EB%82%B4%EC%A3%BC%EC%8B%9D/%EC%B0%A8%ED%8A%B8/get_domestic_stock_tick_chart.py).

## Deliberately open

- Credentialed mock/production observation of timezone, ordering, retention, rate limits and response stability.
- A collision-safe provider event identity and explicit price-adjustment provenance before durable ingestion.
- Schema/source migration, scheduler, retry/known-good policy, public API/Flutter valuation and every live-money gate.
