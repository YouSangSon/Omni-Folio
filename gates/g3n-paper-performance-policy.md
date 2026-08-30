# G3.8E — Versioned paper performance safety policy

Scope: local, internal-only policy evidence and atomic paper halt/rollback derived from recovered G3.8D strategy-window performance. This is not broker truth, advice, profitability, deployment, promotion, scheduler, or live readiness.

## Contract

- [x] `paper-strategy-performance-safety.v1` accepts recovered canonical decimals only: fewer than two same-selection samples is `INSUFFICIENT`; `max_drawdown >= 0.1` wins; otherwise `cumulative_return <= -0.05` acts; otherwise `HOLD`.
- [x] Callers provide only account, current selection, and expected latest strategy-performance IDs. Metrics, threshold, version, decision, reason, and action IDs are derived internally.
- [x] One immediate transaction proves recovery before retry, records the policy, deterministically halts every authority armed at the global cutoff in lexical account order, appends one exact-source one-pop rollback, reproves the proposed state, and commits once.
- [x] Full non-recursive recovery verifies canonical row/hash, three cutoff bindings, latest account evidence through the global G3.8D cutoff, deterministic halt identity, one-clock action provenance, fencing, exact forward/reverse coverage, retry, stale and concurrent behavior.
- [x] Every schema-v20 linked writer, synthetic dispatch/target, policy call, server startup, source backup and restore activation fails closed on the required root/base proof.
- [x] Migration 020 uses create/copy/drop/rename/create-policy order, pins the canonical v19 full FK digest, and compares authority/strategy proof, rows/sequences, complete FK and dependent-trigger inventory before recording v20. FK enforcement and `foreign_key_check` pass again after commit.
- [x] Schema v20/backup v14 pins exact policy/authority/selection objects and policy proof receipt. v13/schema v19 is verified at source and only an owned temporary copy migrates to an empty policy journal.
- [x] Independent final review is clean: Critical 0, Major 0, Minor 0. No UI, public API/CLI, scheduler, broker, credential, deployment, live, new dependency, general-ledger, or profitability surface was added.

## Fresh evidence

- Focused migration/restore and canonical v19 FK baseline tests passed; the final focused migration/restore run completed in 1.994s.
- `cd services/core && go test -count=1 ./...` passed in 66.279s after the final FK-baseline change.
- `cd services/core && go test -race -count=1 ./...` passed in 154.487s with no race report.
- Independent review reran `go test -count=1 -run '^TestG38E' .`, the pure `internal/riskdomain` suite, `git diff --check`, and process inventory, then reported Critical 0 / Major 0 / Minor 0.
- `make check` passed Go vet/tests, Flutter analyze/tests, Python 17 tests, and validation of 15 JSON contracts.
- `make smoke` passed health, readiness, local status, preview/apply/idempotent replay, snapshot, sanitized activity, and local sample market data.
- `cd services/core && govulncheck ./...` reported `No vulnerabilities found.`
- Intentional failure runs `make test GO=false`, `make check DART=false`, and `make smoke GO=false` returned nonzero and each left an empty owned inventory.
- Actual PTY SIGINT runs of `make test`, `make check`, and a bounded FIFO-startup `make smoke` returned 130 and left no owned root, process, listener, or artifact.
- Actual process-group SIGTERM runs of the same three targets returned 143 and left no owned root, process, listener, or artifact.
- `make test-resource-cleanup` passed stale-owner, dead-server and PID-reuse cleanup while preserving the active fixture.
- Final inventory found no `omni-folio-test.*`, `omni-folio-smoke.*`, `omni-folio-restore-legacy.*`, or signal-harness temp root; port 18080 listener; `omni-core`/Go test process; client build/coverage; core binary; research bytecode/cache; test-labelled Podman resource; or `omni-folio-*` Kind cluster.

## Forbidden until later gates

- Scheduled/unattended policy evaluation or automatic strategy promotion.
- Public API/UI presentation, alerts, broker request, credential use, deployment mutation, shadow/live authority, or real-money action.
- Treating the fixed local thresholds or fixture-derived evidence as optimal, current, recommended, broker-backed, or profitable.

Design: [`docs/superpowers/specs/2026-08-31-paper-performance-policy-design.md`](../docs/superpowers/specs/2026-08-31-paper-performance-policy-design.md)

TDD: [`docs/testing/g3n-paper-performance-policy.tdd.md`](../docs/testing/g3n-paper-performance-policy.tdd.md)
