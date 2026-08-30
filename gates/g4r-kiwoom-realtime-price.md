# G4R Kiwoom Realtime-Price Gate

Scope: credential-free synthetic contract for one official Kiwoom `0B` registration packet and `REAL` price frame. No WebSocket connection, credential, external request, persistence, route, UI or live authority is used.

## Pass evidence

- [x] Registration is closed to one bare six-digit symbol with `REG`, group `1`, refresh `1` and type `0B`.
- [x] Frames are capped at 1 MiB before decode and 100 entries before normalization.
- [x] Every `REAL.data` entry must be `0B` with a bare six-digit item, string FID `10` positive price and valid string FID `20` clock.
- [x] Provider `HHMMSS` becomes naive `HH:mm:ss`; separately injected receive time becomes canonical UTC without inventing an observation date.
- [x] FID `9081` is ignored and the DTO has no exchange field until an official/observed mapping can prove it.
- [x] Same-frame equal symbol/clock/price duplicates collapse; different prices at the same symbol/clock fail closed without partial output.
- [x] Control/missing/empty/mixed/malformed/numeric/trailing frames and zero receive time fail closed; no provider name or unknown FID is exposed.
- [x] The implementation uses existing decimal/time/JSON helpers and Go stdlib only.

## Evidence

- RED `2e80583`: the registration, parser and internal DTO did not exist.
- GREEN `35116ea`: focused realtime tests and the full Go core suite pass.
- Review boundary RED `dac201e` and fix `4da510f`: unproven KRX labeling was removed, and byte/entry caps now reject oversized frames before normalization.
- Independent corrected re-review: GO; both exchange-provenance and external-input-bound blockers are closed.
- `make check`, `make smoke`, Go race, `govulncheck` and 78.1% Go statement coverage pass locally on 2026-08-28 KST.
- Official source: Kiwoom repository commit [`9180deb`](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/commit/9180debf7aea0074715dd8f7a15af432afbfc403), [`subscribe_domestic_stock_trade_async.py`](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/9180debf7aea0074715dd8f7a15af432afbfc403/examples/%EA%B5%AD%EB%82%B4%EC%A3%BC%EC%8B%9D/%EC%8B%A4%EC%8B%9C%EA%B0%84%EC%8B%9C%EC%84%B8/subscribe_domestic_stock_trade_async.py).

## Deliberately open

- Credentialed mock observation and WebSocket LOGIN/PING, timeout, reconnect, resubscribe, gap/duplicate and backpressure behavior.
- Official timezone/date, exchange-FID mapping, event ordering/identity and price-adjustment provenance.
- Durable ingestion/known-good policy, scheduler, public API/Flutter and every live-money gate.
