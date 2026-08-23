# G4 Broker, Chart, and Paper-Order Gate

## Sequence

1. Kiwoom domestic read-only account, holdings, open orders, daily/minute candles, and realtime quote/trade streams.
2. Kiwoom-to-ledger reconciliation and Flutter stale/partial/chart states.
3. Kiwoom mock-order state machine and unknown-submit recovery.
4. Toss Securities read-only adapter using the same canonical contracts.

## Pass when

- OAuth secrets and account identifiers stay in the Go server secret boundary and are redacted from Flutter, Python, Git, logs, exports, and errors.
- Repeated pages/events are idempotent; pagination interruption, 401/403/429/5xx, token expiry, and WebSocket reconnect preserve known-good data and expose freshness.
- Broker balances are compared with, never silently substituted for, the authoritative ledger.
- Charts show source/as-of, price-adjustment basis, price/volume, text/table alternatives, empty/stale/partial/error states, and measured profile-mode frame timing.
- Kiwoom mock orders prove pre-trade risk, durable idempotency, ack/partial-fill/fill/cancel/reject, restart recovery, and reconciliation.
- Real-money submit remains disabled. Toss order work does not start until a safe test path is documented because a separate official sandbox has not been confirmed.

## Evidence

- G4A passes synthetic Kiwoom K0 transport/normalization tests only; no credential or broker request has been made.
- G4B has a provider-neutral local-fixture OHLCV API and Flutter asset-detail chart with explicit sample provenance, exact decimal strings, price/volume, non-color candle cues, state handling, screen-reader summary, and a lazy exact-data table. Automated checks and two metadata-complete Android-emulator profile runs pass after optimization.
- G4D carries the existing price-adjustment basis through HTTP, OpenAPI, and Flutter. The local fixture is pinned to `unspecified`; `provider_adjusted` is parsed and displayed conservatively for future provider responses but is not wired to the public route.
- G4 remains open: real Kiwoom candle/realtime behavior, known-good persistence, ledger reconciliation, physical-device profile, manual VoiceOver/TalkBack, mock orders, and every live-order gate are unproven.
