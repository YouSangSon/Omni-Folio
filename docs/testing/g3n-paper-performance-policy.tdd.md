# G3.8E paper performance safety policy — TDD record

Date: 2026-08-31 (Asia/Seoul). Scope is local, internal-only paper safety evidence and atomic automatic halt/rollback. Scheduler, public API/UI, broker access, credentials, deployment, promotion, live authority, advice, and profitability claims are excluded.

## RED to GREEN

| Area | RED evidence | GREEN evidence |
| --- | --- | --- |
| Exact policy | Undefined policy types/functions and inclusive boundary failures | Dependency-free exact-rational v1 policy covers sample floor, drawdown-first ties, inclusive thresholds, malformed decimals, `HOLD`, and `INSUFFICIENT` |
| Atomic action | Undefined application/event seams, forced stage failures, stale/cross-selection and concurrent retries | Recovery-first transaction records one policy, deterministic lexical all-armed halts, and exact one-pop rollback or writes nothing |
| Independent recovery | Canonical cutoff/action-time/action-link mutations, corrupt writer/dispatch and incomplete mutation matrix were accepted | Root replay recomputes policy, latest-at-global-cutoff evidence, one-clock provenance, fencing and forward/reverse coverage before retry/write/startup/backup/restore |
| Schema and compatibility | Weakened table/PK, noncanonical v19 FK drift, `_old` rebuild order and missing precommit proof were accepted | Schema v20 pins exact table/index/FK/trigger objects, canonical v19 FK digest, pre/post journal/FK/trigger proof, fixed create/copy/drop/rename order and post-enable FK check |
| Backup | v14 fields and v13 owned-copy policy proof were absent | Backup v14 carries policy digest/event/action/halt receipt; v13/schema v19 source remains unchanged and only an owned copy migrates to empty policy state |

Every RED above was observed before its corresponding production fix. The final canonical mutation matrix rewrites both stored scalar columns and canonical `record_json`/`record_sha256` for decision/reason, G3.8D source, count, fencing, halt coverage, rollback source and cutoff cases; recovery rejects all of them.

## Runnable evidence

```text
cd services/core && go test -count=1 -run '^TestG38E' .
cd services/core && go test -count=1 ./...
cd services/core && go test -race -count=1 ./...
cd services/core && govulncheck ./...
make check
make smoke
make test-resource-cleanup
git diff --check
```

Latest focused migration/restore, full, race, root check/smoke, vulnerability, contract, independent review, signal cleanup, and zero-residue inventory evidence is recorded in [`../../gates/g3n-paper-performance-policy.md`](../../gates/g3n-paper-performance-policy.md).

## Boundary

G3.8E can only remove local paper execution authority and roll back the current paper selection. It cannot schedule evaluation, submit a broker order, arm an account, promote a strategy, expose a client result, touch credentials, or enable live trading.
