# G4F / K2B0 Kiwoom Known-Order Reconciliation Gate

Scope: Go core internal-only synthetic execution reconciliation for a Kiwoom order whose opaque provider order ref was already bound by an explicit ACK. No network, credential, public route, Flutter order UI, unknown-submit correlation, risk engine, fencing, market/amend order, ledger mutation or live submit.

## Acceptance

- [x] **Unknown remains unknown:** a complete lookup with one matching order tuple cannot bind a provider order ref or append an ACK; it returns `UNCORRELATED` and the account-wide submit block remains.
  - CHECK: `cd services/core && go test -run '^TestK2B0LookupCannotAcknowledgeUnknownSubmitFromTupleMatch$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **Conservative known-good state:** incomplete lookup, incomplete execution history and missing known provider order return without appending events.
  - CHECK: `cd services/core && go test -run '^TestK2B0KnownOrderConservativeLookupOutcomesDoNotMutate$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **Atomic deterministic fills:** complete executions for the bound provider order sort by actual UTC time and execution ref, replay idempotently, accept cumulative follow-up lookups and reject changed execution payloads.
  - CHECK: `cd services/core && go test -run '^TestK2B0ReconcileCompleteExecutionsAreAtomicDeterministicAndIdempotent$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **Conflict rollback:** a provider execution collision discovered after an earlier fill insertion rolls back the entire lookup transaction.
  - CHECK: `cd services/core && go test -run '^TestK2B0ProviderExecutionConflictRollsBackEarlierFills$' -count=1 ./...`
  - EVIDENCE: PASS.
- [x] **Fail-closed boundary:** known provider ref tuple/time conflicts, raw aliases and cross-account lookup authority fail without state mutation or raw identifier disclosure.
  - CHECK: `cd services/core && go test -run '^TestK2B0KnownProviderObservationConflictFailsClosed$|^TestK2B0ReconcileFailsClosedWithoutLeakingRawOrCrossAccountIdentifiers$' -count=1 ./...`
  - EVIDENCE: PASS.

## Verification

- TDD/GREEN checkpoint: `4a2a6e4`.
- `make check`, `cd services/core && go test -race -count=1 ./...`, `git diff --check` and staged secret-pattern scan pass on 2026-08-24 KST.
- Independent safety re-review reports no remaining blocker after lookup-only ACK inference was removed.

## Deferred to K2B

- Credentialed Kiwoom mock submit/query observation and canonical adapter mapping.
- A broker-guaranteed or empirically proven correlation contract for a lost submit response; tuple/time similarity alone is insufficient.
- Public OpenAPI route and Flutter review/lifecycle/cancel UI.
- Real risk policy, reservations, reasons, DB lease and fencing.
- Market/amend orders, ledger fill reconciliation, portfolio mutation and every real-money gate.
