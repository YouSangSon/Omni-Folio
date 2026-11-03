# G4K Stored Broker Reconciliation Read Gate

## Pass when

- `GET /v1/broker-reconciliation/latest` returns only the newest stored G4H Kiwoom KRX position-quantity reconciliation, bound to its ledger revision.
- The response is a closed DTO with provider/environment/exchange, `freshness=unverified`, fetched/recorded times, ledger revision, overall match and exact decimal position differences. It omits account references, internal IDs, hashes and the raw snapshot.
- No stored snapshot returns stable 404. A corrupt record or newest raw snapshot without reconciliation returns generic 500 and never falls back to older evidence.
- Flutter Connections shows loading, empty, error/retry and success, retains a prior known-good result after refresh failure, and communicates stored/not-current provenance plus match/mismatch through text and screen-reader semantics.

## Evidence

- `TestG4KLatestBrokerReconciliationHTTPIsSanitized` proves exact JSON and forbidden-field absence by construction.
- `TestG4KLatestBrokerReconciliationHTTPDistinguishesMissingFromCorrupt` proves 404 for true absence and generic 500 for corrupt/orphaned newest evidence.
- `TestG4KOpenAPIExposesOnlyTheSanitizedReadModel` proves the route points to a closed schema without account/internal snapshot fields.
- Flutter parser/API/widget tests prove strict canonical DTO validation, fixed no-query route, 404-to-empty mapping, visible stale provenance, exact quantity strings, color-independent status, row semantics, retry and retained known-good state. The current combined client suite has 29 passing tests on 2026-08-26 KST.
- 2026-08-26 KST에 `make check`, `make smoke`, `go test -race ./... -count=1`, `govulncheck ./...`, `git diff --check`와 diff secret-pattern scan을 통과했다. Omni-Folio Podman/Kind/process/temp residue는 남지 않았다.

## Not proven

- credential or actual Kiwoom request, scheduled refresh, official freshness/timezone/retention, account switching
- current broker state, cash/valuation/fee/open-order/execution reconciliation, ledger correction
- physical-device/manual screen-reader evidence, broker submit, production risk, paper/live readiness
