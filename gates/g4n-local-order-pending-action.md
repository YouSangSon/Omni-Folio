# G4N Local Order Pending Action and Overview Warning Gate

## Pass when

- `GET /v1/orders` keeps G4L's recovery proof, closed display-only shape and `broker_freshness=unverified`.
- Every `LocalOrderView` includes required `pending_action=SUBMIT|CANCEL|none`; missing or unsupported values fail client parsing.
- `pending_action` does not expose account, client-order, provider-order, execution, reservation, strategy-authority, fencing or internal identifiers.
- A replayed order with `status=FILLED` and unresolved `pending_action=CANCEL` is not shown as an ordinary complete order; Flutter visible and screen-reader text says the fill is local but cancel confirmation is still unknown and further action is prohibited.
- Overview and Connections reuse one retained local-order read. Home counts unresolved submit/cancel actions, labels them as local and not current broker state, links to the existing details screen, and never turns empty or unavailable history into a broker-safety claim.
- The optional local-order read does not block the portfolio snapshot, duplicate on navigation, hide retained refresh failure, or overflow at 375 logical pixels with 200% text.
- No submit, resubmit, cancel, amend, broker refresh, schema migration, new route or new projection is added.

## Evidence

- `TestG4LLocalOrderLifecycleHTTPKeepsPendingCancelOnFilledOrder` covers the real replay path `OPEN -> CANCEL_DISPATCHED -> FILL_RECORDED(full)` and proves the sanitized HTTP DTO retains `pending_action=CANCEL`.
- Existing G4L HTTP and OpenAPI tests require the field and continue to reject forbidden identifiers.
- Flutter parser and widget tests cover strict enum validation, `FILLED + CANCEL` copy, shared Overview/Connections state, slow/empty/error handling, no mutation controls and 200% text screen-reader semantics.
- Root checks, full Go race, smoke, diff/secret scan and residue checks are required before merge.

## Not proven

- credential or actual Kiwoom request, broker-current freshness, account switching, authoritative cancel/fill correlation
- submit/resubmit/cancel/amend mutation, broker-coupled production risk, fill-to-ledger reconciliation
- physical-device/manual VoiceOver or TalkBack evidence, paper/live performance or any real-money path
