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
- `git diff 83facad -- apps/client` is empty. Existing release UI evidence from G4X is reused for the unchanged Flutter source; this unit did not run another release browser session. It does not claim a new browser import-flow test.
- Exact owned test roots and processes were cleaned; no smoke process or port 18080 listener remains.

## Remaining boundary

Local sample valuation is not a live quote or broker-confirmed balance. No credentialed broker, physical-device, remote CI or deployment validation was performed in this unit. Historical valuation and market-data authority remain separate contracts.
