# G1.15 Stored-Price Holding Valuation View

Scope: expose G1.13's existing exact native-currency read model through a sanitized GET and a separate Flutter detail. No migration, new valuation engine, provider call, quote freshness, whole-portfolio total or execution authority.

## Contract

- One read transaction owns ledger proof, quantity/cost, selected price and valuation. Flutter consumes that response without joining a second snapshot.
- Optional canonical UTC `as_of` defaults to server UTC now. Unknown/duplicate/empty/malformed queries fail 400 without reflecting supplied names; earlier-than-ledger fails 409, replay corruption is a redacted 500.
- Public lines omit instrument/observation/account IDs and hashes. Decimal values remain strings; selected local-fixture prices retain venue, adjustment and observed/fetched/recorded timestamps.
- Missing, ambiguous or over-24-hour prices suppress all totals. Fully valued results remain `stale_sample`; totals stay separated by native currency and `PortfolioSnapshot.valuation_status` stays `unavailable`.
- Flutter detail provides loading/retry, retained known-good refresh failure, empty/partial views and accessible sample/stale provenance. The retained response owns its original revision/time, never a newer snapshot's label.

## Verification

- Backend RED: `go test -count=1 . -run '^TestHoldingValuationHTTPStoredPriceView$'` failed 404 before route implementation on 2026-09-05.
- Backend GREEN: `go test -count=1 . -run '^TestHoldingValuationHTTP'` passes exact arithmetic, HTTP query/error boundaries, closed OpenAPI/runtime field matching and private-field omission.
- [x] Flutter RED: the new focused file failed on missing API/model/page; the later empty-Holdings navigation regression failed because the button was absent. GREEN: repository test run includes 9 valuation tests and 65 existing tests (74 total), including fixed GET/no query, exact decimal strings, invalid shape/status/provenance, 24h+1ns boundaries, empty/partial results, retained refresh errors, single-flight retry, navigation and semantics.
- [x] Light/dark widget states and 320px layout with actual 200% text-scale assertion. This is widget evidence, not browser screenshot or manual screen-reader signoff.
- [x] Repository `make check` passes on 2026-09-05: full Go suites, Flutter 74, Python 17, 15 JSON contracts and resource-cleanup self-tests. No new provider calls, data seeding, Podman/Kind or persistent servers were used.
- [x] `go test -race -count=1 . -run '^TestHoldingValuation'` and `govulncheck ./...` pass. No dependencies were added; Python research has no third-party runtime requirements and this is not an npm project.

Review: independent read-only backend audit found no blocking issue; its explicit ledger/price provenance assertion recommendation was added. Parent integration review corrected nanosecond truncation, hidden empty refresh errors and zero/negative valued-market parsing. Keep HTTP projection fields closed when extending the internal model.

Final parser follow-up: a RED mutation showed `86400.0` was accepted as the age policy integer; reusing `_count` closes that type boundary. All 9 valuation tests and final `make format-check lint` pass after this change; other runtime code is unchanged from the full check.

## Still open

- Provider-backed freshness/source selection, historical FX and whole-portfolio/performance valuation.
- Normal demo import seeds ledger rows, not stored security prices; no synthetic value is invented when price evidence is missing.
- Physical-device/profile and manual screen-reader evidence remain under G2. This slice does not establish broker connectivity, deployment or live readiness.
