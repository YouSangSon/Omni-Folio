# Paper Operational Evaluation Design

## Goal

Add append-only, replay-verifiable operational evidence for the currently selected paper strategy. The evaluator derives its decision from the existing Go order log and records whether paper execution has a completed sample, remains incomplete, or has an unresolved broker command.

This is a prerequisite for performance degradation automation. The current paper adapter is BUY-only and has no cash ledger, SELL, fee, tax, slippage, latency, or mark-to-market equity curve, so this gate must not claim return, drawdown, profitability, or investment performance.

## Scope and Authority

Go/SQLite owns evaluation and persistence. Python produces no paper metric and cannot write operational state. The evaluator reads the complete order recovery proof, the current strategy selection, and only paper orders bound to the exact account, result SHA, and selection event.

Evaluation is observational. It never changes strategy selection or execution authority. A later gate may consume a current degraded event only after paper accounting and a versioned performance policy exist.

No route, CLI, Flutter surface, scheduler, alert, broker request, credential, shadow/live promotion, or real-money path is added.

## Evaluation Contract

`paper-operational-evaluation.v1` records one immutable snapshot with:

- deterministic `evaluation_id` derived from account, exact strategy selection, scoped order-state hash, and policy version;
- `policy_version=paper-operational-safety.v1`;
- opaque Kiwoom `account_ref`;
- exact `strategy_result_sha256` and `strategy_selection_event_id`;
- `expected_previous_evaluation_id`, using `no_evaluation` for the first tuple event;
- SHA-256 of canonical replayed states for the tuple's paper orders;
- mutually exclusive `terminal_order_count`, `active_order_count`, and `pending_action_count`, whose sum equals `order_count`;
- `decision=INSUFFICIENT|PASS|DEGRADED` and its fixed reason code;
- canonical UTC `recorded_at` and canonical record SHA-256.

Orders are classified in this order:

1. any non-empty `pending_action` is pending;
2. `FILLED`, `CANCELED`, `REJECTED`, or `RISK_REJECTED` without a pending action is terminal;
3. every other valid state is active.

Policy v1 is intentionally operational rather than financial:

- `pending_action_count > 0` produces `DEGRADED/unresolved_action`;
- otherwise `terminal_order_count == 0` produces `INSUFFICIENT/no_terminal_sample`;
- otherwise it produces `PASS/operationally_complete`.

The evaluator first requires the complete order recovery proof to pass. It then verifies the current strategy registry still exactly matches the supplied result and selection event. Each scoped order intent must be `mode=paper` and carry the same account and strategy binding. The canonical scoped hash includes the immutable intent and replayed current state for every matching order in stable order-ID order.

Exact replay of the same scoped snapshot returns the existing evaluation. A changed snapshot appends a new event linked to the latest event for the same account and strategy-selection tuple. A stale selection, invalid account, corrupt order log, or corrupt registry fails before persistence.

## SQLite and Recovery

Migration 014 creates one STRICT `paper_evaluation_events` table, a tuple-current index, and no-update, no-delete, and predecessor-state triggers. It creates no backfill and does not rebuild order, strategy, or execution-authority tables.

The table constrains policy, decisions, reason codes, non-negative counts, count totals, lowercase SHA-256 values, and the decision/reason/count relationship. The predecessor trigger serializes events independently per `(account_ref, strategy_selection_event_id)` tuple.

Strategy-registry recovery reuses its existing proof and additionally validates every paper evaluation's canonical JSON/hash, predecessor chain, strategy evidence reference, selection-event/result binding, count invariants, and decision policy. The proof hash includes the complete evaluation rows and exposes an evaluation count.

The current database becomes schema v14 and backup format v9. The manifest adds only `paper_evaluation_event_count`; the existing strategy-registry digest and receipt cover the new log. Backup creation and restore compare the expanded strategy proof. Legacy v8/schema-v13 artifacts are verified through an owned temporary copy, migrated without rewriting existing rows, and must yield the empty evaluation count. Restore pins the exact table, index, and triggers.

## Failure Handling

- Invalid identity or stale current selection fails without an evaluation row.
- Corrupt order or strategy evidence fails the complete replay before evaluation.
- Direct update/delete, stale predecessor, inconsistent counts, and invalid decision tuples fail in SQLite and replay.
- Evaluation failure cannot halt an account, fence a lease, or roll back a strategy.
- Backup mismatch or missing evaluation guards fails activation.

## Verification

Executable evidence covers derived insufficient, pass, and degraded decisions; exact idempotency and changed-snapshot chaining; stale selection and wrong-account rejection; order and evaluation corruption; SQLite insert-only/state guards; no authority mutation; current backup round trip; and owned-copy schema-v13 migration.

The merge gate is focused RED/GREEN, full `make check`, `make smoke`, Go race, `govulncheck`, independent read-only review, and proof that owned processes and temporary resources were removed.

## Required Follow-up

Financial performance evidence requires explicit paper cash, SELL/down-rebalance, fees, taxes, slippage, price marks, and an equity curve before return or drawdown can be derived. Only then may a versioned degradation policy atomically append distinct automatic halt and rollback reasons. Scheduler, alerts, shadow promotion, broker-coupled fencing, and live execution remain later gates.
