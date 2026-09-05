# G3.8G2B Local Paper Workflow — in progress

## Implemented boundary

- Explicit `paper-init`, `paper-import-bars`, `paper-execute -arm-paper` commands use existing migrated SQLite only. No broker/network/credential, automatic initializer, daemon or live mode is added.
- Root owns CLI/lifecycle/tests/docs; `paper_snapshot_ingress` owns snapshot/research CSV validation; `paper_flow_gap` reviews authority and owns the stale-global-claim regression. Branch: `feat/g38g-local-paper-ingress`. Integration gate: full check, focused race, lifecycle evidence and diff review; no external push is included.
- Snapshot import hashes the bytes once, validates closed 13-column metadata/timing and reuses canonical OHLCV rules. The existing immediate transaction stores the entire batch or nothing. Rolling history preserves old receipts and rejects conflicts, retroactive inserts, omitted stored closes and changed bytes at the same last-bar anchor.
- Original research CSV hash, canonical values, symbol and last timestamp are independently checked by Go. The selected SMA warmup must fit in the supplied snapshot. Explicit fixture timestamps do not prove a provider trading calendar or actual availability.
- Before arm/fill, the same proposal validator checks current selection, session policy, stored series, decision and receipt deadline without granting authority. Actual admission repeats the checks with the current process lease in its write transaction.
- Sequence: import → preflight → explicit arm/acquire → old eligible fill → C3/D/E safety policy → new signal/order. A policy halt/rollback stops new admission. Each durable phase remains committed if a later phase fails; this is not one all-or-nothing run.
- Execution startup, fill, policy and proposal admission each validate the current global runner claim inside the relevant transaction. The global claim never substitutes for the account execution lease.
- Clean termination revokes only the exact current execution owner/fence, including its expired lease, then releases the global claim. Revocation is not permission to execute; a newer owner is never halted. Halt and explicit arm/acquire reuse existing immutable events, so there is no schema/backup version change.
- Mixed-mode accounts are rejected atomically at CLI initialization and explicit arm. Legacy internal session callers retain their existing behavior.

## Evidence

- RED: missing importer/local execution boundaries; mixed-mode CLI initialization incorrectly succeeded before the atomic isolation guard.
- Snapshot tests exercise real later-insert failure rollback, exact and rolling replay, malformed/timing/size/header/gap/conflict rejection, original receipt preservation and byte-level hash identity.
- Local workflow tests exercise actual OPEN admission → eligible filled BUY → `none` preservation, forged-direction rejection before prior fill, injected order failure with owned halt, explicit flag requirement, CLI initialization/import, and fresh-service CLI replay returning the original FILLED order.
- Python `run_experiment`/`generate_proposal` now feed the same extended CSV bytes through the real Go importer and local execution chain, not hand-populated historical bars.
- Global-claim takeover while A retains a valid account execution lease rejects A's startup, fill and admission without changing B's claim. Expired-but-still-owned cleanup succeeds; foreign takeover survives stale cleanup.
- Final independent read-only integration review found no blocker for this local checkpoint. It did not execute tests or certify the remaining acceptance below.
- 2026-09-05 final `make check` passed: Go all packages, Flutter 74 tests, Python 25 tests, JSON 16 syntax checks, formatting/vet/analyze and owned-resource cleanup self-tests. `go test -race -count=1 . -run 'Test(LocalPaper|PaperProposal|PaperSnapshotImport|ValidatePaperResearchInput|K2C)'` passed after the integrated research-input changes. `govulncheck ./...` reported no vulnerabilities. These do not prove the new executable's OS interruption matrix below.

## Remaining acceptance

- Prove this new executable's actual SIGINT/SIGTERM and SIGKILL/stale-owner process/resource matrix, not just the older policy runner's matrix or context wiring. Do not mark G3.8G2B or overall G3.8G2 complete before that evidence.
- Add explicit workflow-level policy halt/no-new-order and partial-fill/restart convergence evidence across missed input intervals. Existing lower-level policy/fill tests are not a substitute for these combined paths.
- Continuous proposal generation/ingestion and execution, calendar/provider truth, shadow/live parity, engine POCs, and profitability remain separate open requirements. Never schedule repeated `-arm-paper` calls as an automatic recovery policy.

Research: [Go encoding/csv](https://pkg.go.dev/encoding/csv) documents field-count checks and newline normalization. Therefore the raw file digest is taken before parsing; semantically identical CRLF/LF input is not the same immutable snapshot. Existing SQLite immediate transaction/replay contracts are reused; no new parser dependency or execution engine is introduced. This is a focused source check, not a new engine benchmark.

Operator instructions: [local paper workflow](../docs/local-paper-workflow.md).
