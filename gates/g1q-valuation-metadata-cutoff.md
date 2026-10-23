# G1.21 — Duplicate-import valuation and recorded-time cutoff

Date: 2026-10-23. Baseline `83facad`. Scope: cash, native holding and current KRW valuation reads.

## Contract and correction

A duplicate-only import intentionally updates `ledger_meta.recorded_at` without adding events. The three valuation readers incorrectly required that metadata timestamp to equal the last appended event's timestamp and returned 500 after a valid duplicate import.

Each reader still replays the snapshot and proves the full canonical event log in the same read-only transaction, then matches its revision. Only the invalid timestamp equality is removed. The response's `ledger_recorded_at` remains the snapshot metadata time, and the existing metadata/occurrence cutoff remains enforced. This preserves the valuation contract; G4X's last-event timestamp has a different documented meaning.

Removing equality alone would allow future knowledge after a clock rollback: an earlier sequence can have the latest recorded clock even when both the newest event and import metadata are older. A same-transaction `EXISTS` query checks every recorded event in the proved revision against the canonical cutoff, using the existing terminal-Z-free nanosecond ordering. A cutoff before any record returns the existing 409 for explicit cash/holding queries or generic 500 for server-clock current valuation. Equality is accepted.

No writer, schema, backup/transition receipt, economic calculation, response shape, policy version or Flutter source changes. OpenAPI clarifies the existing metadata field. The additional query preserves full proof and cutoff protection rather than inferring clock monotonicity from append order.

## Evidence

- The first focused command stopped at shell parsing because the Make recipe expanded an unquoted regex containing parentheses; it did not run Go tests. The corrected simple test-name prefix established RED: duplicate imports caused all three routes to return 500, including cases that should succeed or return 409. Logs: `goal-20261023-valuation-metadata-red{,-corrected}.log`.
- New regression tests compare the complete priced/FX HTTP responses before and after duplicate import: only metadata recorded time changes; revision and all economic values remain equal. Snapshot equality before/after the reads checks the read-only boundary.
- Cutoff regressions cover 1ns before metadata, an earlier append recorded at `.9Z` followed by a whole-second append and `.05Z` duplicate metadata, rejection at `.899999999Z`, acceptance at `.9Z`, and generic current-valuation error redaction. Existing revision/FX/price corruption and observation-cutoff regressions remain enabled.
- Focused `make test CORE_TEST_PACKAGES='-run Valuation -count=1 .' FLUTTER=true` passed: Go 8.937s, resource preflight and Python 34 tests. Flutter was explicitly skipped in this focused run.
- Final `make check CORE_TEST_PACKAGES='-timeout=30m ./...'` exited 0: Go core 337.471s and five internal packages, Flutter 120 tests, Python 34 tests, format/vet/analyze, resource preflight/cleanup and 17 JSON syntax checks. Log: `goal-20261023-valuation-metadata-check.log`. JSON syntax checks are not a complete JSON Schema validator.
- `make smoke` exited 0 for health/status/preview/apply/snapshot/activity/market data. The changed valuation routes were exercised through their production HTTP handlers in the focused/full regressions. Independent read-only review found no remaining blocker.
- `git diff 83facad -- apps/client` is empty. The implementation unit reused G4X release UI evidence for the unchanged Flutter source. The following supplement separately verifies the import flow on merged main.
- Exact owned test roots and processes were cleaned; no smoke process or port 18080 listener remains.

### Release browser supplement

On 2026-10-23, rebuilt main `d3059e26f94828dffdb6732990a9b89c1392e0e3` with the existing isolated browser harness, local API and golden CSV. In the actual 375×812 release UI, pasted the same CSV, created a preview and applied it: new 0, duplicate 3, applied 0, skipped 3, revision `rev_0000000003`.

- Before/after HTTP assertions passed for snapshot, cash valuation, holding valuation, current valuation and instrument ledger (all 200). Snapshot data is equal except import metadata time; the three valuations are equal except metadata and query time. The complete instrument-ledger response is unchanged, including its last-event timestamp. A final snapshot read confirms the valuation reads did not mutate it.
- Metadata advances from `2026-10-23T11:54:56.332188Z` to `2026-10-23T11:56:15.917606Z`; the instrument ledger retains the former. Cash USD 778, quantity 6, cost USD 300.6, average USD 50.1 and two original trades remain unchanged.
- Home refresh, Holdings → saved-price valuation and Holdings → AAPL → instrument ledger refresh all succeed. Missing price/FX correctly produce `unavailable` without a partial total; this fixture does not establish priced or live valuation. Those calculations remain covered by the priced/FX regression tests above.
- Visually inspected 375px receipt, Home, saved-price valuation and ledger screenshots: `output/playwright/valuation-duplicate-{receipt,home,holding,ledger}-375.png`. HTTP before/after/assertions JSON, UI snapshots and network/console captures are under `docs/_internal/skill-outputs/goal-20261023-valuation-duplicate-*`. Network records show successful preview/apply and valuation/ledger requests; the only console errors are the expected absent broker-reconciliation 404s, plus the CPU-only rendering warning.
- Application source, tests and contracts are unchanged, so the passing full check above remains applicable. The browser wrapper exited 0: actual Go API/release web build, resource preflight 1/1 and Python 34/34. It does not rerun the Go/Flutter unit suites. Owned tab/daemon, root `.62aPL1`, owner 14457, child group 14506 and core/web processes were removed; ports 18082/18083 have no listeners.

## Remaining boundary

Local sample valuation is not a live quote or broker-confirmed balance. No credentialed broker, physical-device, remote CI or deployment validation was performed in this unit. Historical valuation and market-data authority remain separate contracts.

The browser run also exposed a separate responsive-shell issue: crossing the 900px layout boundary resets an unsubmitted CSV and its preview. The shell changes the content's parent from `Row/Expanded` to direct content, disposing the page state. Re-entering the fixture at 375px completed the duplicate-flow verification; retaining import state across that boundary remains an open G2 follow-up.
