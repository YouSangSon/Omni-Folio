# G4 Broker, Chart, and Paper-Order Gate

## Sequence

1. Kiwoom domestic read-only account, holdings, open orders, daily/minute candles, and realtime quote/trade streams.
2. Kiwoom-to-ledger reconciliation and Flutter stale/partial/chart states.
3. K2A internal synthetic Kiwoom order-state log and unknown-submit durability.
4. K2B0 internal synthetic reconciliation for executions of already-bound provider orders.
5. K2B1 internal synthetic `kt00009` dated execution scan without reconciliation authority.
6. G4H credential-free complete snapshot persistence and ledger quantity diff.
7. G4I/K2C internal synthetic execution authority, fixed BUY policy, lease/fencing and backup v5 proof.
8. G4J/K2B2 credential-free Kiwoom mock LIMIT submit transport.
9. G4K sanitized stored position-reconciliation HTTP/Flutter read view.
10. K2B credentialed mock observation, query transport and lookup recovery.
11. Toss Securities read-only adapter using the same canonical contracts.

## Pass when

- OAuth secrets and account identifiers stay in the Go server secret boundary and are redacted from Flutter, Python, Git, logs, exports, and errors.
- Repeated pages/events are idempotent; pagination interruption, 401/403/429/5xx, token expiry, and WebSocket reconnect preserve known-good data and expose freshness.
- Broker balances are compared with, never silently substituted for, the authoritative ledger.
- Charts show source/as-of, price-adjustment basis, price/volume, text/table alternatives, empty/stale/partial/error states, and measured profile-mode frame timing.
- K2A proves risk-verdict ordering only, durable idempotency, append-only ack/partial-fill/fill/cancel/reject replay, unknown-submit restart recovery, and order-aware backup/restore.
- K2B0 proves atomic/idempotent execution reconciliation only for an already-bound provider order; lookup-only tuple/time similarity never resolves an unknown submit.
- K2B1 proves only strict, all-or-nothing normalization of one explicit-date `kt00009` page set; pagination completion never means complete execution history and dated aliases cannot enter durable order events.
- G4H proves only atomic retention of a complete synthetic raw snapshot and revision-bound KRX/KRW quantity reconciliation; failed inputs preserve last-known-good and never auto-correct the ledger.
- G4K proves only a read-only consumer of that stored G4H evidence. It exposes no account reference, internal ID, hash or raw snapshot; `freshness=unverified` and visible UI text prevent a current-state claim.
- K2C proves only credential-free internal execution authority: default-off kill switch, process owner, 30-second SQLite lease/fencing, fixed BUY limits, immutable reservation, DB bypass rejection and backup v5 recovery. It is not production risk and does not send broker orders.
- K2B2 proves only the official mock `kt10000` LIMIT BUY request shape on an in-memory transport, with token preflight, durable dispatch-before-write, no write retry, opaque ACK and conservative unknown/reject outcomes. It uses no real credential or external broker request.
- K2B must prove credentialed mock submit/query, broker-coupled runner fencing, public order flow, and broker/ledger reconciliation.
- Real-money submit remains disabled. Toss order work does not start until a safe test path is documented because a separate official sandbox has not been confirmed.

## Evidence

- G4A passes synthetic Kiwoom K0 transport/normalization tests only; no credential or broker request has been made.
- G4B has a provider-neutral local-fixture OHLCV API and Flutter asset-detail chart with explicit sample provenance, exact decimal strings, price/volume, non-color candle cues, state handling, screen-reader summary, and a lazy exact-data table. Automated checks and two metadata-complete Android-emulator profile runs pass after optimization.
- G4D carries the existing price-adjustment basis through HTTP, OpenAPI, and Flutter. The local fixture is pinned to `unspecified`; `provider_adjusted` is parsed and displayed conservatively for future provider responses but is not wired to the public route.
- G4E/K2A passes the internal-only Kiwoom synthetic `LIMIT`/`KRW`/`KRX` state log, durable unknown-submit block, risk-reducing cancel, replay/idempotency conflicts and the order portion of current schema v8/backup v5 recovery checks. No credential or broker request was used and no public route/UI was added.
- G4F/K2B0 passes the internal-only known-order execution reconciler: complete observations append in one transaction, conflicts rollback, incomplete/not-found preserve state, and unknown submits remain `UNCORRELATED`. No credential, broker request, public route/UI or ledger mutation was added.
- G4G/K2B1 passes the internal-only synthetic dated execution scan: fixed KRX stock/fills-only request, strict provider order/fill normalization, date/account/environment-scoped non-joinable aliases, no partial result after later-page failure, naive execution clock and `ExecutionsComplete=false`. No credential, broker request, persistence, K2B0 mapping or public route/UI was added.
- G4H passes credential-free complete snapshot validation, atomic raw snapshot plus ledger-revision reconciliation idempotency, quantity diff, last-known-good retention, insert-only ledger/broker rows and current backup v5 broker-state recovery. No credential, actual broker request, scheduler, authoritative freshness, public route/UI or auto-correction was added.
- G4I/K2C passes internal default-off execution authority, process owner, lease/fencing, fixed credential-free BUY limits, atomic reservation-bound approval/dispatch and backup v5 recovery. It adds no broker transmission, credential, public route/UI or production/live authority.
- G4J/K2B2 passes the credential-free Kiwoom mock LIMIT BUY submit contract. Synthetic transport proves the exact request, one-shot write, raw identifier/message/token redaction, definitive rejection and unresolved network/auth outcomes without enabling a route, runner or live authority.
- G4K passes a sanitized `GET /v1/broker-reconciliation/latest` contract and Flutter Connections read view. Missing evidence is empty/404, corrupt or orphaned newest evidence fails with a generic 500, and exact decimal strings plus text semantics show stored position differences without refreshing a broker.
- G4 remains open: real Kiwoom candle/realtime behavior, scheduled credentialed persistence, full broker/ledger reconciliation, physical-device profile, manual VoiceOver/TalkBack, K2B credentialed observation/unknown correlation/public UI/broker-coupled risk, and every live-order gate are unproven.
