# G4L Local Order Lifecycle Read Gate

## Pass when

- `GET /v1/orders` returns newest-first display fields only after the existing full order recovery proof validates canonical hashes, metadata and transition replay in one read transaction.
- The closed response fixes `source=local_order_log` and `broker_freshness=unverified`, omits account/client/provider/internal identifiers, and returns `orders=[]` for an empty log.
- Malformed events, mismatched hashes or invalid recorded timestamps return a generic 500 without partial history or sensitive details.
- Flutter Connections implements loading, empty, error/retry and retained-known-good states, states that no broker refresh occurred, and provides no order mutation control.
- `SUBMIT_UNKNOWN` visibly and semantically says `브로커 결과 미확정 · 재주문 금지` at 200% text scaling.

## Evidence

- `TestG4L*` covers exact sanitized HTTP, forbidden fields, empty history, malformed event/hash/timestamp rejection and the closed OpenAPI schemas.
- Flutter parser/API/widget tests cover strict DTO validation, fixed `/v1/orders`, unknown-state safety, raw-error redaction and retained known-good refresh behavior.
- Root checks, full Go race, smoke, diff/secret scan and residue checks are required before merge.

## Not proven

- credential or actual Kiwoom request, broker-current freshness, account switching, unknown-submit correlation
- submit/resubmit/cancel/amend mutation, broker-coupled production risk, fill-to-ledger reconciliation
- physical-device/manual VoiceOver or TalkBack evidence, paper/live performance or any real-money path
