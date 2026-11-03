# Paper Performance Evidence Design

Date: 2026-08-31
Gate: G3.8C3

## Goal

Persist one account-global, replay-verifiable paper equity curve from immutable order and price evidence. This gate produces exact marks, equity, simple returns, and drawdown; it does not authorize thresholds, automatic halt/rollback, promotion, public API/UI, broker access, credentials, or live trading.

## Chosen approach

Add one append-only `paper_performance_events` table in schema v18 and backup v12. Each event is both the valuation snapshot and one equity-curve point. Canonical `marks_json` holds the exact selected bar evidence, so a second mark table or mutable balance/equity projection is unnecessary.

Do not reuse `paper_evaluation_events`: that log proves order-operation completeness for one strategy selection and has no reconstructable order or price cutoff. Do not copy the paper accounting algorithm: bounded replay reuses `paperdomain.Account` and the sole `FILL_RECORDED` journal.

## Fixed v1 authority

The policy is `paper-performance-account-v1`:

- The series belongs to the immutable paper accounting session and account, not a strategy. Selection changes are provenance only and never reset starting cash, cash, lots, previous equity, peak, or max drawdown.
- The caller supplies only `account_ref` and canonical UTC `as_of`. `as_of` must be after the session opened, no later than service-owned `recorded_at`, and equal `close_at` of at least one eligible `paper_fixture` bar under the stored market cutoff even when the account is cash-only. The service owns cutoffs, selection provenance, marks, quantities, totals, ratios, predecessor, identity, and `recorded_at`.
- One immediate SQLite transaction proves current recovery state, captures `MAX(order_events.sequence)` and `MAX(paper_market_bar_observations.sequence)`, derives the complete event, validates its predecessor, inserts, replays, and commits.
- Exact retry of an existing `(policy_version, account_ref, session_id, as_of)` returns that fully validated first event even after later evidence. A new event must have a strictly later `as_of`.
- Legacy paper v1/v2 orders remain recoverable but cannot enter a new performance series.

## Causal accounting cutoff

Bounded accounting includes only capitalized v3 fills whose global order-event sequence is at or below the stored order cutoff and whose bound ex-post fill bar has `close_at <= as_of`. The close-time condition is stronger than the modeled bar-open occurrence time because final volume and the modeled fill are not knowable until the bar closes.

Every durable row encountered during recovery is still validated. A corrupt row after an older event's cutoff cannot be hidden by historical replay.

## Complete price marks

For every positive open position, select exactly one stored observation satisfying:

- `source=paper_fixture`, `venue=KRX`, `currency=KRW`, `interval=1d`, `timezone=Asia/Seoul`, `price_adjustment=unspecified`;
- matching symbol and `close_at=as_of`;
- observation sequence at or below the event's market cutoff;
- `source_available_at <= fetched_at <= recorded_at <= event.recorded_at`;
- canonical positive close.

The mark is the exact close. Missing or ambiguous marks reject the whole event. No carry-forward, interpolation, latest-trade fallback, averaging, mixed source/currency/adjustment, or partial aggregate is allowed. A cash-only account uses an empty mark array but still requires the same cutoff-bounded daily-close existence check, preventing arbitrary or future-dated curve points without inventing a calendar.

Marks are sorted by symbol and contain symbol, quantity, observation ID, observation sequence, close, open cost, market value, and unrealized PnL. Their canonical JSON and SHA are stored and independently reconstructed.

## Exact performance math

All money and quantities use `math/big.Rat` through the existing `internal/exact` kernel. No float or new dependency is introduced.

For event `i`, with session starting cash `E0`:

```text
market_value_i  = SUM(quantity_s * close_s)
open_cost_i     = SUM(open FIFO lot cost_s)
equity_i        = cash_i + market_value_i
unrealized_pnl  = market_value_i - open_cost_i
total_pnl       = realized_pnl_i + unrealized_pnl_i
                = equity_i - E0
period_return   = (equity_i - previous_equity) / previous_equity
cumulative      = (equity_i - E0) / E0
peak_i          = max(E0, equity_1 ... equity_i)
drawdown_i      = (peak_i - equity_i) / peak_i
max_drawdown_i  = max(drawdown_1 ... drawdown_i)
```

Money and peak remain unrounded canonical finite decimals. Returns and drawdowns are calculated from unrounded exact rationals, quantized once to scale 8 with half-even rounding, then canonically trimmed. The first period compares with `E0`. If a previous actual equity is zero, the equity point remains valid with `period_return_state=undefined_zero_denominator` and no numeric period return; NaN and infinity are forbidden.

## Durable event

`paper_performance_events` stores:

- identity/schema/policy, account/session, current selection ID, selected result reference, and selection sequence cutoff;
- predecessor, `as_of`, order-event cutoff, market-observation cutoff;
- account-state SHA, canonical marks JSON/SHA/count;
- cash, open cost, market value, realized/unrealized/total PnL, equity, peak equity;
- period-return state/value, cumulative return, drawdown, max drawdown;
- canonical record JSON/SHA and service-owned recorded time.

The selected result reference is either the exact current result SHA or the explicit `no_strategy` sentinel produced by a rollback. A `no_strategy` state may still append account-global performance; the field is provenance and must never be exposed as strategy attribution.

The deterministic ID uses policy version, account, session, and `as_of`; it deliberately excludes current cutoffs so a retry after later evidence resolves to the first immutable row. Unique account/policy/as-of identity, append-only triggers, current-cutoff guards, current-selection provenance, and per-account predecessor/as-of guards provide the storage boundary. Go independently validates nested JSON and arithmetic.

## Recovery and backup

Recovery scans the performance table in sequence order and, for every event, reconstructs bounded accounting, exact marks, all arithmetic, identity, predecessor, and canonical hashes. It rejects forged cutoffs, missing or later-substituted marks, sequence gaps, broken session/selection bindings, changed totals/ratios, or schema protection drift.

Backup v12/schema v18 adds performance digest, event count, mark count, candidate digest, and performance verification status. v11/schema v17 and every already-supported older backup are verified in their original form, copied to an owned temporary candidate, migrated to an empty C3 log, and reverified without modifying the source artifact. Historical performance is never synthesized.

## TDD and resource boundary

Implementation order is pure domain RED/GREEN, bounded application replay, append-only migration, recovery/backup, then full regression and independent review. Every test uses `t.TempDir` or the root-owned test/smoke session. No test creates an unmanaged listener, container, Podman/Kind resource, or persistent database. Root verification must end with no owned temp roots, processes, build/coverage/bytecode artifacts, containers, or Kind cluster.

## Deferred by design

- Kiwoom/Toss/security-price marks, latest-price carry-forward, calendar/freshness policy, mixed currency and FX;
- public API, Flutter UI, chart overlays, scheduler and cadence automation;
- performance thresholds, automatic halt/rollback, paper/shadow promotion and every live-money action;
- TWR/XIRR, deposits/withdrawals, annualized metrics, Sharpe/Sortino, benchmark attribution, drawdown duration;
- mutable projections, caches, new services, interfaces, dependencies, Kubernetes resources.
