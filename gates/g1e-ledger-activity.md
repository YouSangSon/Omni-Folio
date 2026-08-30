# G1.9 Sanitized Ledger Activity Gate

Scope: replay-verified, read-only local ledger activity API and accessible recent-activity Flutter surface without broker refresh, valuation, mutation, schema change or new dependency.

- [x] G1E1: latest-first activity, same-time sequence tie-break and revision-bound keyset continuation are exact across a later backdated import.
  CHECK: go test -count=1 -run '^TestG1ELedgerActivitiesArePagedNewestFirstAndRedacted$' ./...
  EXPECT: /ok\s+omni-folio\/services\/core/
  CWD: ../services/core

- [x] G1E2: invalid/forged query cursors, canonical corruption and ledger revision drift fail closed without identifiers or partial rows.
  CHECK: go test -count=1 -run '^TestG1ELedgerActivitiesEmptyValidationAndCorruption$' ./...
  EXPECT: /ok\s+omni-folio\/services\/core/
  CWD: ../services/core

- [x] G1E3: OpenAPI and Flutter parsers require the closed exact-money shape and reject identifiers, invalid event fields, wrong FX legs and correction mismatches.
  CHECK: make check
  EXPECT: validated
  CWD: ..

- [x] G1E4: Flutter renders local/not-current provenance, exact trade and FX rows, generic retry and retained-known-good failure at 320px/200% text with screen-reader labels.
  CHECK: flutter test test/vertical_slice_test.dart
  EXPECT: /All tests passed/
  CWD: ../apps/client

- [x] G1E5: root smoke reads the committed golden-import activity page and owned test/build resources are removed.
  CHECK: make smoke
  EXPECT: /smoke: health, status, preview, apply, snapshot, activity, market data OK/
  CWD: ..

This gate does not prove restart-stable or multi-replica cursors, current broker activity, broker reconciliation, valuation, exchange rates, full-history browsing, filters, export, correction/order mutation, physical-device accessibility, deployment or live-money readiness.

## Evidence (2026-08-28)

- Both focused G1E core checks passed, including encrypted/authenticated cursor tampering, malformed query, huge revision and non-contiguous sequence cases.
- `make check` passed Go format/vet/tests, Flutter format/analyze/56 tests, Python 13 tests and all 15 JSON contract parses.
- `go test -race -count=1 ./...` and `govulncheck ./...` passed in `services/core`; `make smoke` printed the expected activity-inclusive success line.
