# G4I / K2C Kiwoom Internal Synthetic Execution Authority Gate

Scope: Go core internal-only synthetic execution authority for credential-free Kiwoom LIMIT orders. No public route, Flutter order UI, credential, broker request, paper submit or live submit.

## Acceptance

- [x] **Fail-closed authority:** no authority state, armed-without-lease, stale token, foreign owner, expired lease and halted state all reject dispatch.
  - CHECK: `cd services/core && go test -run '^TestK2CExecutionAuthorityFailsClosedAndBlocksDirectBypass$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **Fixed credential-free BUY policy:** only synthetic Kiwoom `BUY/LIMIT/KRX/KRW` for `005930` and `000660` is allowed, with 10-share, 1,000,000 KRW order notional and 1,000,000 KRW active account reservation caps.
  - CHECK: `cd services/core && go test -run '^TestK2CFixedBuyPolicyAndReservationRace$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **Lease and fencing race:** two SQLite handles cannot both acquire one active lease, and failed authorization rolls back reservation and order events.
  - CHECK: `cd services/core && go test -run '^TestK2CAuthorityLeaseRaceAndAtomicRollback$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **Durable recovery:** backup/restore v5 includes execution authority and risk reservation digest/count, and a restored process cannot reuse another process owner lease.
  - CHECK: `cd services/core && go test -run '^TestK2CBackupRestoresAuthorityAndStartsWithNoOwnedLease$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **Bypass guard:** direct order-event insertion, forged reservation metadata and weakened restore trigger DDL are rejected.
  - CHECK: `cd services/core && go test -run '^TestK2CDatabaseBypassAndWeakRestoreGuardAreRejected$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **Migration compatibility:** old schema v3 metadata-less synthetic risk/dispatch rows remain replayable but are not promoted to authority evidence.
  - CHECK: `cd services/core && go test -run '^TestK2CMigrationPreservesLegacyNonAuthoritativeOrderEvents$' -count=1 ./...`
  - EVIDENCE: PASS.

## Verification

- 2026-08-24 KST: `cd services/core && go test -run '^TestK2C' -count=1 -v` passed.
- 2026-08-24 KST: `make check`, `make smoke`, `cd services/core && go test -race ./...`, `git diff --check`, public-repo check and secret-pattern scan passed.

## Deferred

- Credentialed Kiwoom mock-order observation and query recovery. G4J/K2B2 covers only credential-free synthetic submit transport.
- Public OpenAPI route and Flutter order review/confirm/cancel UI.
- Production risk policy: cash, holdings, fees, max loss, market hours, stale market data, strategy approval and owner approval.
- Paper/shadow/live promotion and broker/ledger reconciliation.
