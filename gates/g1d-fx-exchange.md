# G1.8 Exact FX Exchange Gate

Scope: exact two-leg `FX_EXCHANGE` CSV preview/apply/replay, schema v9, v8 backup migration verification, and accessible import disclosure without a rate table or broker authority.

- [x] G1D1: valid FX exchange previews, applies, replays, deduplicates, and changes both cash currencies atomically.
  CHECK: go test -count=1 -run '^TestFXExchangePreviewApplyReplayAndBackupRestore$' ./...
  EXPECT: /ok\s+omni-folio\/services\/core/
  CWD: ../services/core
  EVIDENCE: exit=0; shell=/bin/sh; cwd=services/core; EXPECT=matched; output-sha256=b16e636634efabbccbb0039ec60a01c52d7fe583272381a13242e9133b743239; output-bytes=37

- [x] G1D2: application validation and schema v9 reject missing, same-currency, wrong-sign, malformed, trade-field, correction-field, and direct-write-invalid FX legs.
  CHECK: go test -count=1 -run '^TestFXExchange(RejectsInvalidLegs|SchemaGuard)$' ./...
  EXPECT: /ok\s+omni-folio\/services\/core/
  CWD: ../services/core
  EVIDENCE: exit=0; shell=/bin/sh; cwd=services/core; EXPECT=matched; output-sha256=ad5d13784e1968edd51ff81f95c9726209acf4500e40dceaded16d656424203e; output-bytes=37

- [x] G1D3: v1 and v8 ledger state migrate to v9, and a hash-bound v5/schema-v8 backup verifies only through an owned temporary v9 copy without mutating its source.
  CHECK: go test -count=1 -run '^(TestSchemaMigratesV1ToV9AndReadinessRequiresV9|TestVerifyManifestMigratesV8BackupCopy)$' ./...
  EXPECT: /ok\s+omni-folio\/services\/core/
  CWD: ../services/core
  EVIDENCE: exit=0; shell=/bin/sh; cwd=services/core; EXPECT=matched; output-sha256=ad8bebc97384e0c7c923162303d96c1144eb34e5ef70297adbc449da1f70e2b4; output-bytes=37

- [x] G1D4: failed backup verification discards newly-created candidate artifacts.
  CHECK: go test -count=1 -run '^TestCreateBackupDiscardsFailedCandidate$' ./...
  EXPECT: /ok\s+omni-folio\/services\/core/
  CWD: ../services/core
  EVIDENCE: exit=0; shell=/bin/sh; cwd=services/core; EXPECT=matched; output-sha256=d1137f9dcaebf940aa54d43271be1f0c5e8a9e976bf12f0b3f7b7eeb09a5c666; output-bytes=37

- [x] G1D5: OpenAPI and Flutter require the complete counter leg and disclose both legs accessibly at 320px and 200% text.
  CHECK: make check
  EXPECT: validated
  CWD: ..
  EVIDENCE: exit=0; shell=/bin/sh; cwd=.; EXPECT=matched; output-sha256=3c81df1566d5739433a8b70a0a927670ea7c26593acf8d0b352915a6d52e4731; output-bytes=6340

- [x] G1D6: root smoke, race, vulnerability, resource-cleanup, and security-boundary review evidence is current.
  EVIDENCE: 2026-08-27 KST `make smoke`, `go test -race -count=1 ./...`, and `govulncheck ./...` passed. `make clean` removed Flutter/Python/build artifacts; no port 18080 listener, owned smoke/restore temp directory, project-labelled Podman container, or Kind cluster remained. `git diff --check` and added-line credential/private-path scans were clean; review found and closed the SQLite `-0` source-leg bypass and removed absolute local paths from this ledger.

This gate does not prove exchange rates, base-currency valuation, FX correction, broker cash reconciliation, live FX data, tax classification, physical-device accessibility, deployment, or live-order readiness.
