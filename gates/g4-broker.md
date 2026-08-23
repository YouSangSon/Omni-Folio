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
- Charts show source/as-of, price/volume, text/table alternatives, empty/stale/partial/error states, and measured profile-mode frame timing.
- Kiwoom mock orders prove pre-trade risk, durable idempotency, ack/partial-fill/fill/cancel/reject, restart recovery, and reconciliation.
- Real-money submit remains disabled. Toss order work does not start until a safe test path is documented because a separate official sandbox has not been confirmed.

## Evidence

- Not active. Official contract baseline and UX boundary are recorded in `docs/broker-priority-and-ux.md`; no credential or broker request has been made.
