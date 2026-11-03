# G4M Overview Reconciliation Summary Gate

## Pass when

- Flutter Overview and Connections share the same stored G4K reconciliation result; opening either screen does not create another broker request or current-state claim.
- Overview shows `현재 상태 아님`, match or mismatch count, broker-fetched time and local-recorded time, then links to the existing Connections details.
- Missing and failed states remain explicit. A refresh failure retains the last known-good summary and raw errors or account/internal identifiers are not rendered.
- The summary and details action remain usable with semantics at a 375 logical-pixel viewport and 200% text scaling.

## Evidence

- `overview stored reconciliation stays usable at 200 percent text` proves the 375px summary, mismatch semantics, exact one-read navigation and route to stored differences.
- Focused tests prove that a slow optional reconciliation does not block the portfolio or its refresh at 320px/200% text, remains single-flight, completes safely after app disposal, announces retained-known-good refresh progress, and does not call an empty position set a successful match.
- Existing reconciliation API/parser/loading/404/error/retry/retained-known-good tests cover both Overview and Connections, together with Flutter analyze and the full client suite.
- Root checks, Go race, smoke, vulnerability, diff/secret and residue checks are required before merge.

## Not proven

- credential or actual Kiwoom request, broker-current freshness, account selection or scheduled refresh
- cash, valuation, fee, open-order or execution reconciliation and any correction/mutation
- physical-device/manual VoiceOver or TalkBack evidence, paper/live performance or any real-money path
