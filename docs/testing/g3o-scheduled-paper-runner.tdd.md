# G3.8F1 scheduled one-shot paper runner — TDD record

## User Journey

As the owner, I want an external scheduler to call one local command that closes due paper performance and safety policy evidence, so that unattended local paper evaluation can proceed without gaining broker, credential, or live-money authority.

## RED

```sh
cd services/core
go test -count=1 -run '^TestG38FScheduled' .
```

Failed because `runDuePaperPerformancePolicy` and `PaperScheduledRunResult` did not exist.

```sh
cd services/core
go test -count=1 -run '^TestG38FPaperRunDueCLI$' .
```

Failed with `unknown command "paper-run-due"`.

```sh
cd services/core
go test -count=1 -run '^TestG38FScheduledPaperRunRetryRejectsPrerequisiteCorruption$' .
```

Failed when cached retry returned a completed chain after an older prerequisite journal row was corrupted.

## GREEN

```sh
cd services/core
go test -count=1 -run '^TestG38F' .
```

Passed after adding `runDuePaperPerformancePolicy`, latest-available local close derivation, recovery-checked completed-chain retry, and the `paper-run-due` CLI entrypoint.

```sh
make test-resource-cleanup
```

Passed after proving active-owner preservation, PID/command/start-time reuse rejection, stale child process-group cleanup, and cleanup of owned descendants after their group leader exits. The Python subprocess used by Go tests also disables bytecode writes.

The intentional-failure matrix for `make test`, `make check`, and `make smoke`, the SIGINT/SIGTERM matrix for all three targets, and an actual SIGKILL stale-owner recovery run all exited non-zero and left the owned temp/build/coverage/bytecode inventory clean.

```sh
make check
make smoke
go test -race -count=1 -run '^TestG38F' .
```

Passed with final owned local resource inventory clean.

## Non-Goals

No scheduler table, daemon, public API, Flutter screen, broker call, credential access, deployment artifact, alerting, shadow/live promotion, or profitability claim was added at the F1 checkpoint. The later local G3.8F2 runner evidence is recorded separately in [`g3p-always-on-paper-runner.tdd.md`](g3p-always-on-paper-runner.tdd.md).
