# G4S Kiwoom Durable Latest-Trade Observation Gate

Scope: credential-free Go internal persistence of the existing G4Q `KiwoomLatestTrade` DTO into the append-only security-price observation series. No credential, external request, public route, Flutter surface, scheduler, valuation authority, order decision or live-money path is added.

## Acceptance

- [x] `KiwoomLatestTrade` from `kiwoom` mock or production maps to `kiwoom_mock`/`kiwoom_production` source namespaces only.
- [x] The durable identity is a versioned `ka10079` observation slot over environment/source, internal instrument ID, `XKRX`, KRX 6-digit symbol, `KRW`, `price_adjustment=unspecified` and provider observed second.
- [x] Replaying the same slot and price is idempotent even when fetched again later; a different price for the same second fails closed.
- [x] Direct internal writes reject tampered source IDs and non-KRX/KRW Kiwoom identities.
- [x] Schema v12 preserves the strict insert-only price series and adds only the two Kiwoom source namespaces.
- [x] Backup v7 now declares schema v12; v7/schema-v11 artifacts are verified in place, copied to an owned temporary candidate, migrated and checked without changing the source artifact.
- [x] Existing holding valuation and public portfolio snapshot continue to read only `local_fixture`.

## Evidence

- RED: the focused test showed that a later fetch of the same slot conflicted in the atomic writer and that the draft source ID truncated SHA-256.
- GREEN: `cd services/core && go test -count=1 -run '^(TestKiwoomLatestTradeRecordsDurablePriceObservation|TestSecurityPriceObservationBackupProofAndLegacyCopyMigrations)$' ./...` passes.
- 2026-08-30 KST: `make check`, `make smoke`, `go test -race -count=1 ./...` and `govulncheck ./...` pass; `govulncheck` reports no vulnerabilities.
- Executable coverage includes exact slot derivation, mock/production separation, repeated-fetch no-op through the atomic writer, same-slot changed-price rejection, tampered ID rejection, malformed/non-KRX/KRW rejection, schema v12 DDL/trigger/index proof, v11 copy migration and unchanged valuation boundary.

## Still Open

- Credentialed Kiwoom observation of timezone, freshness, retention, duplicate-tick behavior and official identity semantics.
- Server-trusted cutoff and freshness/calendar/source-priority policy before using Kiwoom observations in public valuation.
- Any scheduled refresh, realtime ingestion, order decision, Flutter valuation surface or live trading authority.
