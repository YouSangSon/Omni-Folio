# G3.8A Append-only Paper Operational Evaluation Evidence

Scope: credential-free operational evidence for the exact current paper strategy and account. This gate derives only execution-state completeness from replayed local order events. It does not claim return, drawdown, profitability, financial performance, broker truth, or live readiness.

## Acceptance

- [x] `evaluatePaperOperations` first proves the complete append-only order state and exact current strategy selection, then derives a tuple-scoped SHA-256 and counts from matching `mode=paper` orders.
- [x] Orders with a pending submit/cancel action produce `DEGRADED/unresolved_action`; no pending action and no terminal sample produces `INSUFFICIENT/no_terminal_sample`; at least one terminal sample produces `PASS/operationally_complete`.
- [x] Callers cannot submit metrics, decisions, thresholds, or performance claims.
- [x] Exact repeated evaluation is idempotent. Changed scoped state appends against the latest evaluation for the exact account and strategy-selection tuple.
- [x] Schema v14 stores the canonical event JSON/hash in a STRICT insert-only log. Each strategy selection seals the global paper-evaluation sequence, so SQLite and replay reject stale predecessors, inconsistent counts, invalid or superseded strategy binding even at tied timestamps, direct mutation, and corruption.
- [x] Strategy-registry recovery and backup v9 include the paper evaluation rows and count. A non-empty legacy v8/schema-v13 strategy registry is verified only through an owned migrated copy, keeps its legacy hash representation, gains an empty evaluation proof, and leaves the source unchanged.
- [x] A degraded evaluation does not halt/fence execution authority or append/select/rollback strategy events.
- [x] No route, CLI, UI, Python DB write, scheduler, alert, broker call, credential, shadow/live promotion, deployment resource, or real-money path was added.

## Evidence

- RED: `cd services/core && go test . -run '^TestG38PaperEvaluation' -count=1` failed to compile because `evaluatePaperOperations` did not exist.
- GREEN: the same focused command passes with insufficient, pass, degraded, idempotency, stale-selection, no-authority-mutation, corruption, state-guard, backup, and legacy migration coverage.
- Core: `cd services/core && go test ./... -count=1` passes.
- Race: `cd services/core && go test -race . -run '^TestG38PaperEvaluation' -count=1` passes.
- Repository: `make check` passes Go format/vet/test, Flutter analyze and 65 tests, Python compile and 13 tests, and 15 JSON contracts.
- Implementation checkpoints: evaluation ledger `f88eb71`; stale-selection time guard `82cb856`; causal sequence and legacy hash compatibility `2446755`.
- Independent review first rejected historical selection evaluation, then the tied-timestamp bypass, then non-empty schema-v13 hash drift. The causal sequence, zero-backfill hash compatibility, and regressions received final GO with zero reviewer edits.

## Still Open

- Paper cash accounting, SELL/down-rebalance, fees, taxes, slippage, latency, durable price marks, and an equity curve.
- Versioned return/drawdown degradation thresholds and distinct automatic halt/rollback provenance.
- Scheduler, alerts, shadow/canary promotion, credentialed broker observation, broker-coupled fencing, and every live-money path.
