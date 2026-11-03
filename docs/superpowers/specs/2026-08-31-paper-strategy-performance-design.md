# G3.8D Paper Strategy-Window Performance Design

Date: 2026-08-31
Status: approved by the repository goal's local-autonomy rule

## Problem

G3.8C3 stores exact, immutable account/session performance points. Its starting cash, predecessor chain, peak equity, and maximum drawdown intentionally continue across strategy selections. Those values are truthful account health evidence, but they cannot decide that the currently selected strategy caused a loss.

Automatic strategy rollback therefore remains unsafe until the system can attribute a measurement window to one selection event without including movement that happened before the selection.

## Decision

G3.8D adds an internal, append-only `paper-strategy-window-performance.v1` evidence log. It consumes only recovered G3.8C3 rows and never recalculates orders, fills, marks, cash, or account valuation.

The window key is `(account_ref, paper_accounting_session_id, strategy_selection_event_id)`. Only the current non-`no_strategy` selection may append a new window event.

The first C3 point carrying that exact selection event is the zero-return attribution anchor. It is not compared with an earlier account point because the interval before selection is not owned by the new strategy. Its equity becomes baseline and peak; period return, cumulative return, drawdown, and maximum drawdown are zero. A later automatic policy must require at least two points before reading strategy-window return or drawdown.

Account-global C3 continues to represent the skipped transition interval. A future account-health halt may consume it, but a strategy rollback must not.

## Internal contract

```go
func (s *Service) evaluatePaperStrategyPerformance(
    ctx context.Context,
    accountRef string,
    expectedSelectionEventID string,
    expectedLatestPerformanceID string,
) (*PaperStrategyPerformanceEvent, error)
```

The caller supplies identifiers, never metrics. In one immediate SQLite transaction the use case:

1. proves complete G3.8C3 and G3.8D recovery before trusting an existing row;
2. replays the current strategy registry and verifies the expected current selection;
3. rejects `no_strategy` without writing;
4. loads the transaction-current latest C3 point for the account/session and verifies its expected ID and selection binding;
5. selects every C3 point for the same selection from the first same-selection anchor through that latest point;
6. rejects a zero baseline because ratio semantics would be undefined;
7. calls the existing exact `paperdomain.CalculatePerformance` with the anchor equity as starting equity;
8. appends or returns the deterministic evidence row;
9. proves proposed G3.8D recovery before commit.

The deterministic ID binds policy, account, session, selection event, baseline performance ID, and latest performance ID. Exact retries return the first validated row. A newer C3 point creates a new row chained by `expected_previous_strategy_performance_id`. Stale selection or latest-performance identifiers fail without a write.

## Persisted evidence

Migration 019 creates `paper_strategy_performance_events` with:

- schema and policy versions;
- account/session/selection/result provenance;
- baseline and latest C3 performance IDs and their `as_of` values;
- expected previous G3.8D event within the selection window;
- sample count and exact baseline/latest/peak equity;
- exact scale-8 half-even period return, cumulative return, drawdown, and maximum drawdown;
- canonical record JSON and SHA-256;
- append-only update/delete guards and a current-state insert guard.

Recovery first proves every prerequisite log, then reconstructs each selection window from referenced C3 rows, recomputes exact ratios, validates the predecessor chain, canonical JSON, hash, counts, and digest, and rejects any mismatch. Historical rows remain recoverable after a later selection change; only new writes require the selection to be current.

## Backup and migration

Database schema becomes v19 and backup format becomes v13. The manifest adds G3.8D digest, event count, and sample count to both source and verification receipt.

A v12/schema-v18 backup is verified using its own C3 contract before an owned copy is migrated. Migration creates an empty G3.8D log; it never synthesizes strategy-window history. The source backup is never modified.

## TDD and failure evidence

RED must fail for missing strategy-window symbols and migration contract. GREEN covers:

- first-point zero baseline and later exact return/drawdown reset;
- strategy change opening a new window without inheriting the old peak;
- `no_strategy`, missing sample, zero baseline, stale selection, and stale latest point with zero writes;
- exact retry, new-point append, concurrent convergence, and forced insert failure;
- corruption in D or prerequisite C3/order/market evidence blocking recovery and old retries;
- schema objects, backup v13 restore, v12 owned-copy migration, and failed-candidate cleanup.

Root verification uses existing Make traps and scoped cleanup. Success, failure, SIGINT/SIGTERM, and stale-owner recovery must leave no owned process, listener, temp root, build/coverage/bytecode artifact, test-labelled Podman resource, or Omni-Folio Kind cluster. No global prune is permitted.

## Explicit non-goals

G3.8D adds no threshold, decision, halt, rollback, authority reason, scheduler, alert, promotion, API, UI, broker call, credential access, general-ledger write, deployment, Kubernetes manifest, live-money path, or profitability claim. Those require later gates. No dependency, repository interface, projection, cache, or second accounting implementation is added.
