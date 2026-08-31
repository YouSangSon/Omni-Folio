# G3.8D paper strategy performance — TDD record

Date: 2026-08-31 (Asia/Seoul). Scope is local, internal-only current-selection performance evidence. Thresholds, automatic actions, API/UI, broker access, deployment, live authority, and profitability claims are excluded.

## RED to GREEN

| Area | RED commit | GREEN commit |
| --- | --- | --- |
| Strategy-window behavior | `710e231` | `1489769` |
| Backup v13/schema v19 recovery | focused failures after the version bump | `ea3aeda` |

The RED suite failed because the strategy-window use case did not exist. GREEN fixes the first same-selection point as a zero-return baseline, resets only on a new selection event, and records no evidence for stale or unattributable input. Backup tests then failed on the old v12/schema-v18 contract until v13 fields, source-first legacy migration, restore protections, and current expectations were completed.

## Runnable evidence

```text
cd services/core && go test -count=1 ./... -run '^TestG38D'
cd services/core && go test ./...
cd services/core && go test -race ./...
make check
make smoke
make clean-test-resources
```

Focused, full, and race Go tests passed on 2026-08-31 KST. Root check/smoke,
`govulncheck`, `git diff --check`, and the final zero-residue inventory are
recorded in [`../../gates/g3m-paper-strategy-performance.md`](../../gates/g3m-paper-strategy-performance.md).

## Boundary

Schema v19/backup v13 contains recoverable strategy-window evidence only. A later automatic policy must require at least two same-window points and add separately versioned thresholds and halt/rollback provenance.
