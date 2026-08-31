# G3.8D — Current-selection paper strategy performance

Scope: local, internal-only strategy-window evidence derived from recovered G3.8C3 points. This is not an action policy, broker truth, profitability, deployment, or live readiness.

## Contract

- [x] Only current-selection C3 points after the selection event enter the window; `no_strategy`, stale selection, stale latest point, and cross-selection input write nothing.
- [x] The first point is a zero-return baseline. Later points recompute exact window-only peak, period/cumulative return, drawdown, and max drawdown.
- [x] Events are append-only, idempotent by policy/account/selection/latest point, predecessor-linked, atomic, and fail closed under concurrent or corrupted state.
- [x] Recovery independently validates every source point, exact calculation, canonical JSON, hash, sequence, and predecessor.
- [x] Schema v19 and backup v13 pin table/index/unique/FK/trigger protections plus digest, event count, and sample count; v12/schema v18 migrates through an unchanged owned copy to an empty D log.
- [x] Focused/full/race tests and independent implementation review pass locally.

## Fresh evidence

- `cd services/core && go test -count=1 ./... -run '^(TestG38D|TestG38C3|TestBackup|TestRestore|TestBackupManifestContractFieldsMatchRuntimeAndFixtures|TestK2ABackupProvesOrderRecoveryState)'` passed.
- `cd services/core && go test -count=1 ./...` passed.
- `cd services/core && go test -race -count=1 ./...` passed.
- `make check` passed: Go, Flutter 65 tests, Python 17 tests, and 15 JSON contracts.
- `make smoke` passed: health, status, preview, apply, snapshot, activity, and market data.
- `cd services/core && govulncheck ./...` reported `No vulnerabilities found.`
- `git diff --check` exited 0.
- `make clean-test-resources` exited 0. Final inventory found no port 18080 listener, `omni-folio-test.*`/`omni-folio-smoke.*` temp root, client build/coverage, core binary, research bytecode/cache, test-labelled Podman container/network/volume, or Kind cluster.
- An intentional `make check GO=false` failure with a nested Python cache fixture still removed every owned temp root, listener, and bytecode/cache artifact.
- Existing SIGINT/SIGTERM interruption and SIGKILL stale-owner recovery evidence remains pinned in [`g3k-paper-fill-accounting.md`](g3k-paper-fill-accounting.md); unrelated resources are preserved.

## Forbidden until later gates

- Versioned threshold or action decision with fewer than two same-window samples.
- Scheduler, automatic halt/rollback, promotion, public API/UI, broker call, credential access, deployment, or live-money path.
- Treating fixture performance as current, broker-backed, recommended, or profitable.

Design: [`docs/superpowers/specs/2026-08-31-paper-strategy-performance-design.md`](../docs/superpowers/specs/2026-08-31-paper-strategy-performance-design.md)

TDD: [`docs/testing/g3m-paper-strategy-performance.tdd.md`](../docs/testing/g3m-paper-strategy-performance.tdd.md)
