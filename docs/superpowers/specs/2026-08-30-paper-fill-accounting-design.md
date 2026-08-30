# Paper Fill Accounting Design

## Goal

Complete G3.8C2 as one causally honest paper-execution boundary: a persisted target may create BUY or SELL model orders, and every modeled fill is derived from an immutable closed-bar cutoff, the account's immutable paper-accounting session, and exact fee, tax, slippage, cash, and FIFO-lot rules.

This gate does not claim broker or opening-auction execution. A bar's final volume is known only after the bar closes, so `paper_bar_open_v1` is an ex-post simulation recorded after `source_available_at`, with modeled `occurred_at=open_at`. The record must say that plainly.

## Decision and Alternatives

The selected approach is append-only market/signal evidence plus replay-derived accounting over the existing order journal.

- Keep `order_events.FILL_RECORDED` as the only durable fill journal.
- Add immutable paper closed bars and signal events with a transaction-owned observation cutoff.
- Add a paper-only execution authorization instead of weakening `credential_free_buy_v1`.
- Derive cash, FIFO lots, costs, and realized PnL by replay; add no mutable balance, lot, or paper-fill table.

Copying fills into the imported portfolio ledger is rejected because it creates two authorities and mixes paper simulation with user-owned broker/import history. An expanded in-memory ask tuple is rejected because it cannot prove delay, a signal-time cutoff, or bar capacity. Opening quote replay is deferred until a durable provider bid/ask/size contract exists.

## Durable Contracts

### Closed paper bars

`paper-market-bar.v1` is an append-only observation admitted only from `paper_fixture` in this gate. It stores:

- deterministic `observation_id` and unique `source_observation_id`;
- `symbol`, `venue=KRX`, `currency=KRW`, `interval=1d`, `timezone=Asia/Seoul`, and `price_adjustment=unspecified`;
- canonical `open`, `high`, `low`, `close`, and non-negative `volume`;
- `open_at`, `close_at`, `source_available_at`, `fetched_at`, and transaction-owned `recorded_at`;
- canonical record JSON and SHA-256.

Time must satisfy `open_at < close_at <= source_available_at <= fetched_at <= recorded_at`. OHLC ranges must be internally valid. Sequence order is the only observation cutoff authority; wall-clock timestamps alone are not sufficient.

### Capitalized paper signals

`paper-signal.v3` permits a canonical non-negative integer `target_quantity`, including zero. New writes require:

- exact current strategy result and selection event;
- an existing account-global `paper-accounting-session.v1`;
- the current strategy execution-policy SHA to equal the session policy SHA;
- an exact signal-bar observation in the same symbol/series;
- `data_as_of=signal_bar.close_at` and `generated_at >= signal_bar.source_available_at`;
- no uncapitalized legacy paper order in the account.

Go records `paper_signal_events` before later bars can become eligible. It owns `market_observation_sequence_cutoff=MAX(paper_market_bar_observations.sequence)` inside the same immediate transaction and requires the signal bar to be the latest same-series bar at that cutoff. This establishes:

`signal_bar.sequence <= cutoff < eligible_bar.sequence`.

The caller cannot supply a cutoff, session ID, policy hash, fee, tax, slippage, delay, participation, cash, or side. Legacy signal v1/v2 and their orders remain byte-for-byte replayable but uncapitalized and cannot enter accounting or performance evidence.

### Paper model orders and authorization

Capitalized orders use `OrderType=PAPER_MARKET` and no caller price. Go derives side and quantity from signed target delta. Synthetic/Kiwoom mode remains `LIMIT` and continues to use only `credential_free_buy_v1`.

Each paper order is bound to:

- `paper_accounting_session_id`;
- `paper_signal_event_id`;
- `execution_policy_sha256`;
- `paper_accounting_policy_version=paper_accounting_v1`.

`paper_execution_authorizations` stores one immutable authorization per order, the current lease event/fencing token, session and policy bindings, side/quantity, and the exact risk/dispatch event IDs. SQL guards distinguish synthetic reservations from paper authorizations. New paper intents, risk approvals, and dispatch events cannot be inserted directly without matching session, signal, and authorization rows.

Intent, paper authorization, `RISK_APPROVED`, `SUBMIT_DISPATCHED`, and local `SUBMIT_ACKNOWLEDGED` are appended in one immediate transaction. No Kiwoom transport is called.

## Target and Active-Order Policy

Actual paper position is filled BUY quantity minus filled SELL quantity. Projected position also includes the signed remaining quantity of the one active order for the account and symbol.

- positive delta creates BUY;
- negative delta creates SELL;
- zero delta creates no order;
- target zero is a full-reduction request;
- exact signal retries are idempotent;
- a different target while an account/symbol order is active fails closed.

The one-active-order ceiling avoids crossing BUY/SELL orders and cancel/amend semantics. It is deliberate for G3.8C2 and is removed only with a versioned cancel/replace and shared-bar allocation policy.

## Eligible-Bar and Fill Policy

An order may use only closed bars from the exact signal series whose sequence is greater than the signal cutoff. The first eligible bar is exactly the `delay_bars`-th later bar; a partially filled order may consume each subsequent bar at most once.

For `paper_bar_open_v1` and KRX whole shares:

- `capacity = floor(volume * max_participation)`;
- capacity is reduced by prior capitalized fills for the same account, symbol, and bar;
- BUY price is `open * (1 + slippage_bps / 10000)`;
- SELL price is `open * (1 - slippage_bps / 10000)`;
- fee is the policy's fixed KRW amount for each non-zero fill;
- BUY tax is zero;
- SELL tax is `notional * tax`;
- BUY quantity is capped by `floor((cash - fee) / price)`;
- SELL quantity is capped by replayed holdings;
- zero capacity or affordability records no fill.

KRX quantities are whole shares even though the generic Python research engine uses Decimal capacity. This is an intentional adapter specialization, not bit-for-bit quantity parity. Shared policy validation tightens to `0 <= tax <= 1` and `0 <= slippage_bps < 10000`; values are never clamped.

Each capitalized `FILL_RECORDED` event stores its session, signal, bar, current execution-authority event/fence, policy versions, reference open, exact quantity, price, fee, tax, slippage, and modeled occurrence time. Event and provider-execution identities are deterministic from order, bar, and fill-policy version. Recovery recalculates these values; stored calculated fields are audit data, not trusted inputs.

## Accounting Replay

`replayPaperAccounting` starts with the account session's exact starting KRW cash and scans capitalized fills in global order-event sequence.

- BUY subtracts `notional + fee` and appends a FIFO lot with that exact cost.
- SELL adds `notional - fee - tax`, consumes FIFO lots, and records realized PnL as net proceeds minus allocated cost.
- Partial recurring allocations reuse `fifo_exact_else_half_even_residual_8_v1`, `fifoCostAllocation`, and `quantizeHalfEven`.
- Cash and positions may equal zero but never become negative.
- Legacy v1/v2 paper events contribute nothing.

The internal result includes account/session identity, cash, per-symbol FIFO quantities and costs, cumulative fee/tax/slippage, realized PnL, and capitalized fill count. It is accounting evidence, not equity or strategy performance.

The paper fill path begins one immediate SQLite transaction, proves/replays the session, strategy, order, signal, bar, authorization, and current account state, requires the current local execution lease/fence, calculates the next fill, validates the proposed order and accounting state, appends the one fill event, replays the proposed state, and commits. Any failure leaves no fill or accounting mutation.

Locally synthesized ACK/fills require the current lease/fence on every write. External reconciliation fills are not admitted for capitalized paper orders until they carry an independently specified accounting provenance contract.

## SQLite, Recovery, and Backup

Migration 016 owns closed bars and signal cutoffs. Migration 017 owns paper execution authorization, the paper event column/guards, and capitalized intent/fill direct-writer guards. The database becomes schema v17.

All new tables are STRICT, insert-only, hash-checked, and independently replayed. Restore validation pins exact table definitions, unique indexes, foreign keys, state guards, and no-update/no-delete triggers, including empty-table candidates.

Backup becomes `omni-folio-backup.v11` / `omni-folio.sqlite.v17`. Its paper-accounting digest covers sessions, bar observations, signal cutoffs, paper authorizations, every capitalized intent/fill, and the canonical derived account state. Manifest counts add paper bar observations, signal events, authorizations, and capitalized fills; the candidate receipt carries the same accounting digest.

Backup v10/schema-v15 is a named legacy input. Verification checks its original proof, copies it, migrates only the owned copy through v16/v17, and proves zero new bar/signal/authorization/fill evidence. Existing v15 sessions survive exactly; existing paper orders remain uncapitalized. Every older supported backup retains the same no-source-mutation rule.

## Failure Handling

- Missing/corrupt session, policy mismatch, stale selection, legacy paper history, retroactive signal, future/incomplete/mismatched bar, or stale lease fails before new financial writes.
- Active target replacement, oversell, overdraft, non-positive computed price, net-negative SELL proceeds, exhausted capacity, and conflicting retry fail closed.
- Exact retry of a signal, authorization, or bar/fill identity is idempotent; same identity with changed content fails.
- Tampering with bar, cutoff, session, policy, authorization, fill costs, event hash, schema objects, or backup proof makes replay or activation fail.
- No paper execution writes a general ledger event, mutable cash/lot row, broker request, credential, or external resource.

## Verification

TDD must demonstrate the RED failure before each production behavior. Focused evidence covers signal cutoff, policy/session binding, target zero/reduction, active-order rejection, exact BUY/SELL math, whole-share participation, multi-fill fixed fees, FIFO residual conservation, oversell/overdraft, current fencing, concurrency, idempotency, corruption, direct writers, backup v11, owned-copy v10 migration, and absence of Kiwoom/general-ledger writes.

The merge gate is focused RED/GREEN, full `make check`, `make smoke`, Go race, `govulncheck`, independent task and whole-branch review, `git diff --check`, and cleanup/proof of owned Python/process/tmp/Podman/Kind resources.

## Non-goals

- no live or credentialed broker call, scheduler, quote stream, bid/ask inference, or opening-auction claim;
- no API, CLI, Flutter, React, or public accounting surface;
- no mutable balance/lot projection, second fill journal, or imported-ledger paper entries;
- no equity marks, valuation, return, drawdown, performance window, threshold, automatic halt, rollback, or promotion;
- no cancel/amend transport, multi-order bar allocation, shorting, margin, borrow, fractional KRX shares, FX funding, corporate actions, or jurisdictional tax basis.

G3.8C3 remains responsible for immutable order/price sequence cutoffs, durable marks, equity, return/drawdown, and versioned performance evidence. Automatic halt/rollback remains forbidden until that proof exists.
