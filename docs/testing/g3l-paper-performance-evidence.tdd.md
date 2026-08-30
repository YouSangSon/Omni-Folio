# G3.8C3 paper performance evidence — TDD record

Date: 2026-08-31 (Asia/Seoul). Scope is local, ex-post account-global paper
evidence only; no public UI, broker proof, profitability claim, threshold,
automatic halt/rollback, deployment, or live readiness is recorded.

## RED to GREEN

| Area | RED commit | GREEN commit |
| --- | --- | --- |
| Exact valuation and performance series | `8b747a6` | `2509986` |
| Cutoff-bounded accounting and marks | `040f886` | `bc4aa0d` |
| Atomic storage, recovery, and backup | `35fef99` | `aa6a595` |

The RED suites failed for the then-absent domain, cutoff/mark, and
performance-recovery symbols respectively. The Task 1 direct package coverage
run reported `internal/paperdomain` at 91.0% statements. The focused tests
cover exact money/scale-8 ratios, cutoff boundaries, complete marks, retry and
concurrency, corruption recovery, schema protection, backup proof, and v11
owned-copy migration.

## Fresh GREEN verification

All commands below exited 0 on 2026-08-31:

```text
cd services/core && go test -count=1 . -run '^(TestG38C3|TestG38C2|TestG38C1)'
ok  omni-folio/services/core  29.139s

cd services/core && go test -race -count=1 ./...
ok  omni-folio/services/core                       69.967s
ok  omni-folio/services/core/internal/exact        2.122s
ok  omni-folio/services/core/internal/orderdomain  1.695s
ok  omni-folio/services/core/internal/paperdomain  1.914s

make check
Go, Flutter (65 tests), Python (17 tests), format, vet, analyze, and 15 JSON contracts passed.

make smoke
smoke: health, status, preview, apply, snapshot, activity, market data OK

cd services/core && govulncheck ./...
No vulnerabilities found.

git diff --check
(no output)
```

Task 1 independent spec and quality reviews returned GO, as did the Task 2 and
Task 3 independent reviews. The Task 3 review confirmed the v11 source-first
owned-copy migration boundary and C3 fail-closed creation and recovery behavior.

## Owned-resource cleanup

`make clean-test-resources` exited 0. Post-cleanup inventory found no
`omni-folio-test.*` or `omni-folio-smoke.*` root, no listener on port 18080,
no `apps/client/build`, `apps/client/coverage`, `services/core/core`, or
research bytecode/cache, no Omni-Folio-labeled Podman container/network/volume,
and `kind get clusters` reported no clusters. No global prune was used.

## Current boundary

One schema-v18/backup-v12 insert-only evidence chain captures account-global
cutoffs, marks, cash, equity, returns, and drawdown. Threshold policy,
automatic halt/rollback provenance, UI, broker-backed evaluation, deployment,
and live behavior remain later-gate work.
