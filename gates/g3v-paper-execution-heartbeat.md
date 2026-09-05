# G3.8G2E Joint paper execution renewal — local acceptance passed

## Contract and ownership

- Root owns implementation/tests/docs; `continuous_lease_review` provides read-only authority/recovery review. This is the lease prerequisite for continuous execution, used immediately by the existing manual step between fill, policy and admission phases. The continuous input loop remains open.
- `heartbeatLocalPaperExecution` requires the exact global runner tuple and execution owner/fence, unchanged current selection, matching immutable account session policy and an isolated paper account. Both leases must still be valid. It samples time once, rejects clock regression, appends an execution renewal and updates the global runner row in one SQLite transaction, publishing both tokens only after commit.
- Renewal never calls an arm/acquire helper. Execution renewal advances its fence; the global runner keeps its fence and changes its heartbeat tuple. Stale tokens fail. Lost, halted, expired or foreign authority cannot be revived. Cleanup uses the latest successfully committed execution fence and global tuple.
- Existing `lease_acquired` shape is retained. Replay now allows an unexpired successor only for the same owner, nonregressing recorded time, strictly extended expiry and fence +1. Foreign replacement still requires prior expiry. Existing schema v21 and backup v15 retain their fields, immutable historical authorizations and fill references.
- **Reader compatibility:** this deliberately extends the transition contract. Older binaries reject overlapping same-owner renewal histories even though the physical schema/backup format is unchanged. Once renewal records exist, keep a renewal-aware binary for reads/restore; do not claim binary downgrade compatibility or rewrite history to make an older reader accept it.
- The manual step checks renewal at phase boundaries after ten seconds of execution-lease age, not after ten seconds of global-heartbeat age. Policy stages can independently refresh the global row. There is no concurrent heartbeat goroutine. A single phase that runs past expiry still fails closed; this is not a guarantee that arbitrarily long work will complete.
- The global claim is refreshed after input preparation and before initial execution acquisition, so preparation age does not leave a materially older global deadline at the start of execution. A failed refresh retains the old cleanup tuple.

## Verification

- RED: missing heartbeat helper, then replay rejected the first same-owner renewal as unexpired replacement. A separate manual-step regression observed fence 3 (arm/acquire/halt) rather than the required fence 4 including renewal.
- Focused GREEN: repeated renewals survive the original 30-second expiry; stale tokens cannot fill, while the newest fence produces an actual full fill and retains its authority-event binding. Prior admission replay, full recovery and backup/manifest/restore verification pass.
- Rejection tests preserve both states and input tokens on halt, stale fence, foreign owner, each expiry, clock regression, duplicate event insertion and failure of the second write. The second-write case uses a TEMP abort trigger and asserts its distinctive error; adding a main-schema trigger only fails the earlier schema proof and is not rollback evidence.
- Caller integration uses an injected clock to pass ten seconds after the initial lease is sampled, then proves one renewal, one manual arm and cleanup of the latest fence. No real sleep or production test-only switch is added.
- `make check` passed 2026-09-05: Go core 208.096s and internal packages, Flutter 74, Python 33 (2.893s), JSON 17, formatting/vet/analyze and wrapper cleanup. The subsequent initial-global-claim alignment and concurrent-renewal test are covered by the final targeted race/regression run below, not retroactively by that earlier full run.
- Read-only review identified the ineffective first rollback injection, absence of a fresh post-expiry fill and an older initial-global deadline; all three were addressed. No remaining authority or recovery blocker was reported. `govulncheck ./...` found no vulnerabilities; dependencies were unchanged.
- Final `GOFLAGS='-race -run=TestLocalPaper|TestPaperBundle|TestK2CAuthorityLease -count=1' make test FLUTTER=true` passed Go targeted race/regression (87.487s), all 33 Python tests and wrapper cleanup. This covers final production code, real local CLI signal/restart cases and concurrent same-token renewal with one winner. Built child executables use the existing non-race build helper; the race instrumentation applies to the parent Go test binary. Flutter is intentionally skipped here and covered by the earlier full check.
- After making the concurrency test join both workers before any assertion, its focused race rerun passed (1.742s), with all 33 Python tests and wrapper cleanup. No Podman/Kind resources, persistent server or broker connection were created.

## Remaining work and ceiling

No continuous stream consumer, producer child ownership, idle heartbeat loop, delivery acknowledgement, new broker connection or live capability is added. G3.8G2 is not complete. The next consumer must serialize heartbeat with execution stages and stop on policy halt or ownership loss, never run repeated manual-arm commands.

Append-only execution renewals add 8,640 events/day if a future runner renews every ten seconds continuously. Account and recovery replay currently scale with history; sustained throughput has not been measured. Keep this ceiling explicit and measure before claiming always-on readiness rather than adding a second mutable authority projection speculatively.

## Focused source check

The [Go transaction guide](https://go.dev/doc/database/execute-transactions), queried and read via Exa on 2026-09-05, supports the single `sql.Tx` boundary and discarding results when commit fails. This was one focused transaction-source check, not a broad engine or concurrency benchmark. The same-owner transition and compatibility decisions come from this repository's replay/SQL consumers, not from the generic database guide.
