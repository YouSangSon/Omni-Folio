# Paper Accounting Session Design

## Goal

Add one immutable, account-global paper-accounting session that establishes authoritative starting capital before any future paper cash, cost, SELL, equity, or performance calculation. Starting cash and the complete execution-policy hash come only from the exact currently selected research artifact; callers cannot supply or override money or cost assumptions.

This is G3.8C1, a prerequisite rather than performance evidence. It adds no balance, fill accounting, PnL, equity, return, drawdown, or automatic authority mutation.

## Decision and Alternatives

The chosen order is:

1. open an immutable paper-accounting session;
2. add target reduction/SELL together with fee, tax, slippage, and position-safe accounting;
3. add immutable equity marks and versioned performance evaluation;
4. only then allow distinct automatic halt/rollback provenance.

SELL-first is rejected because the shared order machine can represent SELL, but the paper runner has no authoritative starting cash or cost context and its fixed risk policy is intentionally BUY-only. Cost-first is rejected because the current fixture ask is not yet a versioned next-eligible-open fill, so applying selected fields independently would create false parity.

The session is unique per `account_ref`, not per strategy selection. Existing paper target projection already spans all paper orders in an account, including orders created under earlier selections. Resetting starting cash on every strategy change would therefore double-count capital while positions remain continuous. Later strategy performance windows begin from immutable equity marks, not a fresh cash grant.

## Authority and Contract

Go/SQLite owns this authority. Python continues to own deterministic research generation and never writes operational state.

`paper-accounting-session.v1` stores:

- opaque Kiwoom `account_ref` and fixed `currency=KRW` for the current bounded paper runner;
- the initial exact `strategy_result_sha256` and `strategy_selection_event_id` used to authorize the account;
- canonical execution-policy JSON and its SHA-256;
- positive canonical `starting_cash` derived from that policy;
- deterministic session ID derived from account, initial selection, and policy hash;
- canonical record JSON/hash and UTC `recorded_at`.

`decodeStrategyExecutionContract` replaces validation-only decoding and returns a typed internal policy while preserving every G3.8B validation. `loadCurrentStrategyExecutionPolicy` first replays the registry, requires the exact current result/event pair, decodes the stored artifact, and returns its policy and canonical hash.

`openPaperAccountingSession` accepts only account, result SHA, and selection event. In one transaction it:

1. proves the complete order and strategy registries;
2. requires the supplied result/event to be exactly current;
3. loads the stored execution policy;
4. rejects any prior paper order for the account;
5. inserts the derived session, or returns the existing exact session idempotently.

An existing session belongs to its initial selection forever. A different attempt for the same account fails rather than resetting capital. Strategy changes later reuse this account session and load each current strategy's cost/fill policy separately.

There is no route, CLI, Flutter surface, scheduler, credential, broker call, or automatic session creation in G3.8C1. The next runner gate will require a session before new paper intents while retaining exact replay of already durable legacy orders.

## SQLite and Recovery

Migration 015 creates `paper_accounting_sessions` as a STRICT table with one row per account, foreign keys to research evidence and the initial selection event, exact SHA/canonical decimal checks, and fixed schema/currency values.

The table has:

- unique `session_id` and unique `account_ref`;
- no-update and no-delete triggers;
- an INSERT state guard that requires the latest strategy event/result pair, validates that the selection event chose that result, and rejects any existing `mode=paper` order for the account.

Application replay independently decodes canonical record/policy JSON, recomputes both hashes and the deterministic ID, validates metadata and UTC time, verifies the initial selection/result binding, and folds rows into a dedicated recovery digest/count. Direct SQL that bypasses Go still cannot create stale, mismatched, or post-order capital authority.

The current database becomes schema v15 and backup format v10. The manifest and verification receipt add paper-accounting session digest/count fields. Restore pins the exact table, unique indexes, state guard, and insert-only triggers.

Backup v9/schema v14 becomes a named legacy format. It is hash-checked, copied, migrated, and verified with an empty session proof. Existing legacy paper orders remain replayable but uncapitalized; they are never backfilled or treated as performance evidence.

## Failure Handling

- Invalid account, stale/no selection, mismatched result, corrupt strategy/order replay, invalid stored policy, or prior paper history fails with zero session writes.
- Concurrent exact opens return one session; conflicting opens fail the unique account/state guards.
- UPDATE, DELETE, record/policy hash drift, selection mismatch, missing table/index/trigger, or backup proof mismatch fails recovery or activation.
- Failure cannot create an order, fill, risk reservation, authority event, rollback, ledger event, or price observation.

## Verification

TDD evidence covers derived capital/policy, no caller override, exact idempotency, stale and corrupt rejection, prior-order rejection, concurrent conflict handling, direct-writer guards, mutation/corruption replay failure, zero side effects, current backup round trip, exact restore DDL, and owned-copy v9 migration with zero sessions.

The merge gate is focused RED/GREEN, full `make check`, `make smoke`, Go race, `govulncheck`, independent read-only review, and removal of owned temporary/process/Podman/Kind resources.

## Required Follow-up

G3.8C2 must make the paper runner require this session for new intents and land target-zero/SELL with bid-side eligible-bar fills, current selected policy loading, fee/tax/slippage application, exact cash/lots, and oversell/overdraft prevention as one accounting-eligible boundary. Existing order events remain the only fill journal.

G3.8C3 adds immutable price/order sequence cutoffs and complete no-lookahead equity marks. Return/drawdown thresholds and automatic halt/rollback remain forbidden until those proofs exist.
