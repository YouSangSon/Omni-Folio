# Paper Fill Accounting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete G3.8C2 with causally cutoff-bound BUY/SELL paper fills and exact replay-derived KRW cash, FIFO lots, costs, oversell, and overdraft protection.

**Architecture:** Persist closed paper bars before capitalized signals, capture the database-owned observation cutoff with each v3 signal, and authorize one paper model order through a paper-specific lease-bound record. Keep `order_events.FILL_RECORDED` as the sole fill journal and independently recalculate all fill/accounting fields during recovery and backup.

**Tech Stack:** Go standard library, `database/sql`, `math/big`, SQLite STRICT tables/triggers, canonical JSON/SHA helpers, existing order state/FIFO/backup machinery, Python standard-library research tests.

**Spec:** `docs/superpowers/specs/2026-08-30-paper-fill-accounting-design.md`

## Global Constraints

- `order_events.FILL_RECORDED` remains the only durable fill journal; never write a capitalized paper fill to `events` or a new fill table.
- New capitalized writes use `paper-signal.v3`, `paper_accounting_v1`, `paper_bar_open_v1`, and `OrderType=PAPER_MARKET`; v1/v2 remain replay-only and uncapitalized.
- A signal cutoff is transaction-owned: `signal_bar.sequence <= cutoff < eligible_bar.sequence`; timestamps alone never authorize an eligible fill.
- The account session policy SHA must equal the current selected strategy policy SHA when admitting a signal; later fills use the immutable order/session policy.
- KRX quantity is a whole canonical integer and capacity is `floor(volume * max_participation)`.
- Fee is fixed KRW per non-zero fill; tax is SELL notional rate only; adverse slippage uses eligible-bar open.
- `0 <= tax <= 1`, `0 <= slippage_bps < 10000`; no clamping.
- One active capitalized paper order per account and symbol; a changed target fails closed until terminal.
- Locally modeled order admission and every fill require the current execution lease owner and exact fencing token; no Kiwoom transport.
- No mutable cash/lot projection, public API/CLI/UI, scheduler, quote inference, equity, return/drawdown, automatic halt/rollback, credential, broker, or live action.
- Schema becomes v17 and backup becomes v11/schema-v17; v10/schema-v15 is a supported owned-copy legacy input and no legacy paper order is capitalized.
- Every production behavior begins with an observed RED test using hand-derived literal expectations.

---

### Task 1: Exact paper fill policy arithmetic

**Files:**
- Create: `services/core/paper_fill_policy.go`
- Create: `services/core/paper_fill_policy_test.go`
- Modify: `services/core/strategy_registry.go`
- Modify: `services/core/strategy_registry_test.go`
- Modify: `contracts/backtest-run.schema.json`
- Modify: `contracts/strategy-improvement-result.schema.json`
- Modify: `contracts/strategy-improvement-config.schema.json`
- Modify: `services/research/omni_research/engine.py`
- Modify: `services/research/tests/test_engine.py`
- Modify: `services/research/omni_research/improve.py`
- Modify: `services/research/tests/test_improve.py`

**Interfaces:**
- Consumes: `strategyExecutionPolicy`, `parseDecimal`, `formatDecimal`, `math/big`.
- Produces: `paperFillInput`, `paperCalculatedFill`, `calculatePaperFill(strategyExecutionPolicy, paperFillInput) (paperCalculatedFill, bool, error)`, and `floorPositiveRat(*big.Rat) *big.Int`.

- [ ] **Step 1: Write RED tests for exact BUY and SELL math**

Use this exact private API:

```go
type paperFillInput struct {
    Side, Open, Volume, RemainingQuantity string
    Cash, PositionQuantity, ConsumedCapacity string
}

type paperCalculatedFill struct {
    Quantity, ReferencePrice, Price, Notional string
    Fee, Tax, Slippage, CashDelta string
}
```

Add table-driven tests with a policy `{Fee:"1", Tax:"0.001", SlippageBPS:"10", MaxParticipation:"0.5"}`.

- BUY open `100`, volume `5`, remaining `10`, cash `10000`, consumed `0` must fill `2` shares at `100.1`, notional `200.2`, fee `1`, tax `0`, slippage `0.2`, cash delta `-201.2`.
- SELL open `120`, volume `5`, remaining `10`, position `2`, consumed `0` must fill `2` shares at `119.88`, notional `239.76`, fee `1`, tax `0.23976`, slippage `0.24`, cash delta `238.52024`.
- BUY cash `101.1` fills exactly `1`; cash below `101.1`, zero volume, or exhausted capacity returns `ok=false` with an all-zero value and no error.
- SELL position `1` caps quantity to `1`.
- invalid side, non-integer remaining/consumed quantity, consumed capacity above capacity, non-positive computed SELL price, or a negative resulting SELL cash delta returns an error.

Before each body, record the mutation it catches in a test comment: wrong direction, missing floor, missing fee, BUY tax, missing affordability cap, or negative price/proceeds admission.

- [ ] **Step 2: Verify RED**

Run: `cd services/core && go test . -run '^TestG38C2PaperFillPolicy' -count=1`

Expected: compile failure because `paperFillInput` and `calculatePaperFill` do not exist.

- [ ] **Step 3: Implement the minimum exact calculator**

Use only `big.Rat`/`big.Int`. Compute whole-share capacity by flooring the non-negative rational, subtract consumed capacity, then cap by remaining quantity and side-specific cash/position. Return no fill before applying the fixed fee when capacity is zero. Never convert through float64 and never round price, notional, tax, fee, slippage, or cash delta.

`floorPositiveRat` divides numerator by denominator with `big.Int.Quo`; it accepts only non-negative values from already validated callers.

- [ ] **Step 4: Tighten the shared policy boundary in RED/GREEN**

Add Go registry tests that reject `tax="1.0001"` and `slippage_bps="10000"` while preserving the exact canonical SHA for the existing valid fixture. Update `decodeStrategyExecutionContract` to enforce tax at most one and slippage strictly below 10000.

Add dedicated canonical string definitions. Use `^(?:0|1|0\.[0-9]*[1-9])$` for tax in `[0,1]` and `^(?:0|0\.[0-9]*[1-9]|[1-9][0-9]{0,3}(?:\.[0-9]*[1-9])?)$` for slippage in `[0,10000)`. Do not put numeric `maximum` keywords on string schemas. Apply the definitions in backtest request, strategy-improvement config, and strategy-improvement result schemas. Update both Python engine and improvement-config validation to reject the same values before execution, and add literal tests in both suites. Preserve the existing valid golden artifact/hash.

- [ ] **Step 5: Verify GREEN**

Run:

```bash
cd services/core
go test . -run '^(TestG38C2PaperFillPolicy|TestG38B|TestG38C1Loads)' -count=1
cd ../research
python3 -m unittest tests.test_engine tests.test_improve
```

Expected: PASS; existing valid artifacts are byte-for-byte unchanged.

- [ ] **Step 6: Commit**

```bash
git add services/core/paper_fill_policy.go services/core/paper_fill_policy_test.go services/core/strategy_registry.go services/core/strategy_registry_test.go contracts/backtest-run.schema.json contracts/strategy-improvement-result.schema.json contracts/strategy-improvement-config.schema.json services/research/omni_research/engine.py services/research/tests/test_engine.py services/research/omni_research/improve.py services/research/tests/test_improve.py
git commit -m "feat(core): 페이퍼 체결 정책 계산기 추가"
```

### Task 2: Immutable closed bars and signal cutoffs

**Files:**
- Create: `services/core/migrations/016_paper_market_signals.sql`
- Create: `services/core/paper_market_data.go`
- Create: `services/core/paper_market_data_test.go`
- Modify: `services/core/paper_runner.go`
- Modify: `services/core/paper_runner_test.go`
- Modify: `services/core/paper_accounting.go`
- Modify: `services/core/core.go`
- Modify: `services/core/order_backup_test.go`
- Modify: `services/core/order_schema_restore_test.go`
- Modify: `contracts/backup-manifest.schema.json`
- Modify: `contracts/fixtures/golden-backup-manifest.json`

**Interfaces:**
- Consumes: Task 1 policy, `PaperAccountingSession`, current strategy loader, `orderJSONHash`, `canonicalUTCTime`, `orderQuerier`.
- Produces: `PaperMarketBarObservation`, `recordPaperMarketBar(context.Context, PaperMarketBarObservation) (*PaperMarketBarObservation, error)`, `PaperSignalEvent`, `recordPaperSignalEventTx(context.Context, *sql.Tx, string, PaperSignal) (*PaperSignalEvent, error)`, `provePaperMarketRecovery(context.Context, orderQuerier)`, schema v16, and the stable v11 manifest count fields.

- [ ] **Step 1: Write RED tests for immutable closed bars**

Use this exact public-in-package input/result shape:

```go
type PaperMarketBarObservation struct {
    ObservationID, SchemaVersion, Source, SourceObservationID string
    InputDataSHA256, Symbol, Venue, Currency, Interval, Timezone, PriceAdjustment string
    Open, High, Low, Close, Volume string
    OpenAt, CloseAt, SourceAvailableAt, FetchedAt, RecordedAt string
}
```

`recordPaperMarketBar(ctx, observation)` ignores caller `ObservationID`, `SchemaVersion`, and `RecordedAt`; it derives schema `paper-market-bar.v1`, transaction time, and deterministic ID from source plus source observation ID. Tests must prove:

- exact insert/retry returns one identical row;
- changed retry under the same source observation ID fails;
- only `paper_fixture/KRX/KRW/1d/Asia/Seoul/unspecified` is accepted;
- `open_at < close_at <= source_available_at <= fetched_at <= now`;
- canonical positive OHLC, valid range, non-negative volume, lowercase 64-char input SHA;
- UPDATE/DELETE and direct malformed inserts fail.

- [ ] **Step 2: Write RED tests for cutoff-owned v3 signals**

Extend `PaperSignal` with `SignalBarObservationID`. Change v3 target validation to accept canonical `0` or a positive canonical integer. Keep v1/v2 replay validation separate.

Use:

```go
type PaperSignalEvent struct {
    EventID, SchemaVersion, AccountRef, PaperAccountingSessionID string
    StrategyResultSHA256, StrategySelectionEventID, ExecutionPolicySHA256 string
    SignalID, SignalBarObservationID, DataSHA256, Symbol, TargetQuantity string
    DataAsOf, GeneratedAt, ExpiresAt string
    MarketObservationSequenceCutoff int64
    RecordedAt string
}
```

Create a session and signal bar, call `recordPaperSignalEventTx` inside a transaction, and assert the cutoff equals the current maximum bar sequence. Insert the next same-series bar first and prove a retroactive signal fails because the signal bar is no longer latest at cutoff. Also reject missing session, policy mismatch, stale selection, `DataSHA256` mismatch, `DataAsOf != signal_bar.close_at`, generation before source availability, future generation, expired signal, and any account with a legacy v1/v2 paper order. Assert zero signal/order/reservation/fill side effects for each failure.

- [ ] **Step 3: Verify RED**

Run: `cd services/core && go test . -run '^TestG38C2Paper(MarketBar|SignalCutoff)' -count=1`

Expected: compile failure because the durable bar/signal types and functions do not exist.

- [ ] **Step 4: Add migration 016 and minimal replay**

Create STRICT, insert-only `paper_market_bar_observations` and `paper_signal_events` tables matching the spec and types. Required uniqueness:

- bar `observation_id`;
- `(source, source_observation_id)`;
- `(source, symbol, venue, interval, timezone, price_adjustment, open_at)`;
- signal `event_id`;
- `(account_ref, signal_id)`.

Add foreign keys from a signal to session, strategy result/selection, and signal bar. Add exact no-update/no-delete triggers. The signal INSERT state guard must verify the latest selection, session/account/policy equality, exact signal bar metadata/data SHA/time, latest same-series bar at `market_observation_sequence_cutoff`, no bar sequence above the cutoff at transaction capture, and no account legacy paper order.

Application replay recomputes canonical records/IDs/hashes, validates contiguous sequences and every cross-binding, and emits a digest plus bar/signal counts. `provePaperAccountingRecovery` incorporates this proof after sessions.

- [ ] **Step 5: Introduce schema v16 and stable backup v11 fields**

Set `latestSchema=16`, append migration 016, set `backupFormat="omni-folio-backup.v11"` and temporary current `backupSchema="omni-folio.sqlite.v16"`. Name v10/schema-v15 as `legacyPaperFillBackupFormat/Schema`.

Add these required manifest counts now so their v11 JSON shape remains stable through Tasks 3-4:

```go
PaperMarketBarObservationCount int `json:"paper_market_bar_observation_count"`
PaperSignalEventCount int `json:"paper_signal_event_count"`
PaperExecutionAuthorizationCount int `json:"paper_execution_authorization_count"`
PaperCapitalizedFillCount int `json:"paper_capitalized_fill_count"`
```

The last two are zero on schema v16. Keep `paper_accounting_state_sha256` and the receipt candidate digest as the single financial proof hash. Backup/restore must compare the expanded session+market+signal digest and counts. Update the JSON schema and golden fixture.

- [ ] **Step 6: Prove exact restore and owned-copy v10 migration**

Add tests that independently remove or drift each table, unique index, foreign key, state guard, and insert-only trigger and assert activation failure. Create a genuine v10/schema-v15 backup with a session and legacy paper order, verify source DB hash/size stay unchanged, migrate the owned copy to v16, preserve session/order proofs, and get zero bar/signal/authorization/capitalized-fill counts.

- [ ] **Step 7: Verify GREEN**

Run:

```bash
cd services/core
go test . -run '^(TestG38C2Paper(MarketBar|SignalCutoff)|TestG38C2PaperMarketBackup|TestRestore)' -count=1
go test -count=1 ./...
```

Expected: PASS with v16 current and every named legacy format.

- [ ] **Step 8: Commit**

```bash
git add services/core contracts/backup-manifest.schema.json contracts/fixtures/golden-backup-manifest.json
git commit -m "feat(core): 페이퍼 바와 신호 컷오프 원장 추가"
```

### Task 3: Capitalized target orders and paper authorization

**Files:**
- Create: `services/core/migrations/017_paper_execution_authorizations.sql`
- Create: `services/core/paper_execution_authority.go`
- Create: `services/core/paper_execution_authority_test.go`
- Modify: `services/core/order_state.go`
- Modify: `services/core/order_state_test.go`
- Modify: `services/core/paper_runner.go`
- Modify: `services/core/paper_runner_test.go`
- Modify: `services/core/paper_accounting.go`
- Modify: `services/core/core.go`
- Modify: `services/core/order_backup_test.go`
- Modify: `services/core/order_schema_restore_test.go`

**Interfaces:**
- Consumes: Task 2 signal event/session, current lease, existing order state machine, `recordOrderIntentTx`, signed replay of capitalized intents/fills.
- Produces: `paperExecutionAuthorization`, `authorizePaperDispatchOnceTx`, `admitPaperSignal`, `paperProjectedQuantityFrom`, schema v17, and paper authorization recovery/count.

- [ ] **Step 1: Write RED tests for v3 intent binding**

Extend `OrderIntent` with these JSON fields:

```go
PaperAccountingSessionID string `json:"paper_accounting_session_id,omitempty"`
PaperAccountingPolicyVersion string `json:"paper_accounting_policy_version,omitempty"`
PaperSignalEventID string `json:"paper_signal_event_id,omitempty"`
ExecutionPolicySHA256 string `json:"execution_policy_sha256,omitempty"`
```

New paper writes require v3, all four fields, `PAPER_MARKET`, and empty `LimitPrice`. Synthetic writes remain `LIMIT` with positive price and no paper fields. Stored v1/v2 paper intents remain valid only through recovery; `recordOrderIntentTx` rejects them as new writes.

Extend `OrderEvent` in this task with `PaperAuthorizationID string` using JSON key `paper_authorization_id,omitempty`. Risk/dispatch validation admits `paper_accounting_v1` only when this ID is present and `RiskReservationID` is absent; every legacy/synthetic event keeps its existing shape.

Tests must show direct or application writes with missing/mismatched session, signal, account, symbol, target, selection, side, quantity, or policy fail with zero rows.

- [ ] **Step 2: Write RED tests for target and authorization**

Use:

```go
func (s *Service) admitPaperSignal(
    ctx context.Context,
    accountRef string,
    signal PaperSignal,
    fencingToken int64,
) (*PaperSignalEvent, *OrderState, error)
```

In one immediate transaction it records/replays the signal event, computes signed target delta, creates a v3 order if non-zero, writes a paper authorization plus `RISK_APPROVED`, `SUBMIT_DISPATCHED`, and local `SUBMIT_ACKNOWLEDGED`, and commits an `OPEN` order. Literal tests:

- target `10` from zero creates BUY 10; exact retry returns the same signal/order and row counts;
- target `0` from zero records the signal but returns nil order;
- same target while BUY 10 is active is a no-op;
- a different target while that order is active fails and rolls back the new signal;
- stale, expired, foreign-owner, or wrong fencing token leaves zero new signal/order/auth/events;
- paper order never reaches `submitAuthorizedKiwoomMockOrder`.

- [ ] **Step 3: Verify RED**

Run: `cd services/core && go test . -run '^TestG38C2Paper(Intent|Target|Authorization)' -count=1`

Expected: compile failure because the v3 binding and paper authorization path do not exist.

- [ ] **Step 4: Add paper-specific authorization without weakening K2C**

Use this exact record:

```go
type paperExecutionAuthorization struct {
    AuthorizationID, SchemaVersion, OrderID, AccountRef string
    PaperAccountingSessionID, ExecutionPolicySHA256 string
    PolicyVersion, Side, Quantity string
    AuthorityEventID string
    FencingToken int64
    RiskEventID, DispatchEventID, AuthorizedAt string
}
```

Policy/schema constants are `paper_accounting_v1` and `paper-execution-authorization.v1`. `authorizePaperDispatchOnceTx` loads a recorded v3 order, session and signal, proves bindings, requires the current lease, inserts the immutable authorization, and appends consecutive risk/dispatch events. It does not call `validateSyntheticBuyPolicy`, apply synthetic quantity/notional caps, or modify the old reservation validator.

Append local ACK in the same transaction because there is no broker unknown state. Preserve the shared order state machine and deterministic aliases.

- [ ] **Step 5: Add migration 017 and direct-writer guards**

Create STRICT, insert-only `paper_execution_authorizations` with unique authorization/order/risk/dispatch IDs and foreign keys to order, session, and execution-authority event. Add `paper_authorization_id` to `order_events` with a foreign key.

Replace the three authority triggers with mode-aware guards:

- synthetic risk/dispatch requires exactly `risk_reservations` and no paper authorization;
- capitalized paper risk/dispatch requires exactly `paper_execution_authorizations` and no synthetic reservation;
- every other event rejects either authorization column except a capitalized fill carrying the paper authorization and complete fill metadata reserved for Task 4.

Add an order INSERT guard that parses v3 intent JSON and matches session, signal, account, strategy, symbol, side/quantity, and policy. It must reject direct v1/v2 new paper rows without changing old rows.

- [ ] **Step 6: Implement signed projection with the active-order ceiling**

Replay v3 intents in order. Filled BUY adds, filled SELL subtracts; the one active order contributes signed remaining quantity. Legacy orders cause `admitPaperSignal` to fail before recording a v3 signal. If an active order exists for the account/symbol, exact same projected target is a no-op and every different target fails.

At SELL order creation, reject quantity above filled holdings after active commitments. BUY cash is capped only at fill time, matching the selected engine.

- [ ] **Step 7: Advance schema and backup proof**

Set `latestSchema=17`, append migration 017, and set current `backupSchema="omni-folio.sqlite.v17"`. Extend the existing v11 accounting digest/count wiring with paper authorizations. Restore pins exact table, FK, unique indexes, intent/authority guards, and insert-only triggers. The v10/schema-v15 owned-copy migration must produce zero authorizations.

- [ ] **Step 8: Verify GREEN**

Run:

```bash
cd services/core
go test . -run '^(TestG38C2Paper(Intent|Target|Authorization)|TestK2C|TestG3PaperRunner|TestG38C2PaperAuthorizationBackup|TestRestore)' -count=1
go test -count=1 ./...
```

Expected: PASS; synthetic K2C reservations and caps are unchanged.

- [ ] **Step 9: Commit**

```bash
git add services/core
git commit -m "feat(core): 자본화 페이퍼 주문 권한 추가"
```

### Task 4: Eligible fills and replay-derived accounting

**Files:**
- Create: `services/core/paper_fill_accounting_test.go`
- Modify: `services/core/paper_accounting.go`
- Modify: `services/core/paper_fill_policy.go`
- Modify: `services/core/paper_runner.go`
- Modify: `services/core/paper_runner_test.go`
- Modify: `services/core/order_state.go`
- Modify: `services/core/order_state_test.go`
- Modify: `services/core/core.go`
- Modify: `services/core/order_backup_test.go`

**Interfaces:**
- Consumes: Tasks 1-3 calculator, closed bars/signals, paper authorization, `fifoCostAllocation`, `quantizeHalfEven`, shared order replay.
- Produces: enriched capitalized fill events, `paperAccountState`, `replayPaperAccounting`, and `runPaperOrder(context.Context, string, int64)`.

- [ ] **Step 1: Write RED accounting replay tests**

Use internal replay results with canonical string fields:

```go
type paperLotState struct { Quantity, Cost string }
type paperAccountState struct {
    AccountRef, PaperAccountingSessionID, Cash string
    Lots map[string][]paperLotState
    Fees, Taxes, Slippage, RealizedPnL string
    CapitalizedFills int
}
```

Build durable session/signal/order/bar fixtures through production methods. Verify by literal values:

- BUY 2 at Task 1 values leaves cash `9798.8`, one `005930` lot quantity `2` cost `201.2`, fees `1`, taxes `0`, slippage `0.2`.
- SELL those 2 at Task 1 values leaves cash `10037.32024`, no lot, fees `2`, taxes `0.23976`, slippage `0.44`, realized PnL `37.32024`.
- partial recurring FIFO allocations use `fifo_exact_else_half_even_residual_8_v1`; the final sale consumes the exact residual and lifetime allocated cost equals the original lot cost.
- legacy paper fills change none of these fields.
- mutation of fee, tax, price, quantity, bar, cutoff, session, policy, authorization, event hash, or event order makes replay fail.

- [ ] **Step 2: Write RED eligible-bar and fencing tests**

Extend `OrderEvent` with:

```go
PaperAccountingSessionID string `json:"paper_accounting_session_id,omitempty"`
PaperSignalEventID string `json:"paper_signal_event_id,omitempty"`
PaperBarObservationID string `json:"paper_bar_observation_id,omitempty"`
PaperFillPolicyVersion string `json:"paper_fill_policy_version,omitempty"`
ExecutionAuthorityEventID string `json:"execution_authority_event_id,omitempty"`
ReferencePrice string `json:"reference_price,omitempty"`
Fee string `json:"fee,omitempty"`
Tax string `json:"tax,omitempty"`
Slippage string `json:"slippage,omitempty"`
```

Use:

```go
func (s *Service) runPaperOrder(ctx context.Context, orderID string, fencingToken int64) (*OrderState, error)
```

Tests must prove:

- no post-cutoff bar or fewer than `delay_bars` returns unchanged OPEN state and no event;
- the exact Nth later closed bar uses open, not close, and volume participation floor;
- a partial order consumes one fill per later bar and charges the fixed fee each time;
- exact retry on the same latest bar adds no event;
- target 10 BUY fill followed by terminal target 4 creates SELL 6; target zero later sells the final 4;
- BUY is capped to affordable whole shares without negative cash;
- SELL is capped/rejected before oversell and cannot make cash negative;
- concurrent BUYs on different symbols serialize on shared cash; concurrent SELL attempts cannot consume one lot twice;
- stale/expired/foreign lease cannot append a local fill; explicit re-arm/re-lease can continue an OPEN order;
- no general-ledger event, new fill table, Kiwoom request, Podman, or network side effect occurs.

- [ ] **Step 3: Verify RED**

Run: `cd services/core && go test . -run '^TestG38C2Paper(Fill|Accounting|Reduction|Concurrency|Fence)' -count=1`

Expected: compile or assertion failure because capitalized fill replay and `runPaperOrder` do not exist.

- [ ] **Step 4: Implement one atomic fill path**

Inside one immediate transaction:

1. load and validate the v3 intent, paper authorization, session, signal cutoff, and current order state;
2. require current execution lease owner/fencing token and capture its event ID;
3. replay the complete account cash/lots and consumed account/symbol/bar capacity;
4. select the earliest unconsumed same-series bar whose ordinal is at least `delay_bars` and sequence is above cutoff;
5. call `calculatePaperFill` with remaining quantity, cash/position, and consumed capacity;
6. return unchanged state when no whole share can fill;
7. derive deterministic event/execution IDs from order, bar observation ID, and `paper_bar_open_v1`;
8. validate/apply the proposed event and proposed account state;
9. insert the single `FILL_RECORDED` event and commit.

Do not pre-check accounting outside the transaction. Keep generic reconciliation unable to append a capitalized paper fill without the complete provenance.

- [ ] **Step 5: Implement independent recovery**

`replayPaperAccounting` must rescan sessions, v3 intents, signal/bar evidence, authorizations, and global fill-event sequence. For every capitalized fill, recalculate eligibility, capacity, quantity, price, fee, tax, slippage, authority binding, cash, lots, and realized PnL. Stored calculated fields must match exactly.

Extend `provePaperAccountingRecovery` to encode the canonical derived account states and set `paper_capitalized_fill_count`. `proveOrderRecovery` continues to prove raw order bytes; neither digest replaces the other.

- [ ] **Step 6: Complete target reduction behavior**

After an order becomes terminal, `admitPaperSignal` uses actual replayed holdings and signed target delta. Prove BUY 10, SELL 6, SELL 4 to target zero, exact retries, and no crossing active order. Paper SELL never calls or weakens the synthetic BUY risk policy.

- [ ] **Step 7: Verify GREEN and recovery/backup**

Run:

```bash
cd services/core
go test . -run '^TestG38C2Paper(Fill|Accounting|Reduction|Concurrency|Fence)' -count=1
go test . -run '^(TestG38C2|TestBackup|TestRestore|TestK2C|TestG3Paper)' -count=1
go test -race -count=1 ./...
```

Create a v11/schema-v17 backup after BUY and SELL fills. Assert exact cash/lots/cost totals, all four manifest counts, candidate accounting digest, and restored replay. Tampering with any capitalized fill input or deleting a required schema object must reject activation. The v10 source DB hash/size must remain unchanged during owned-copy migration.

- [ ] **Step 8: Commit**

```bash
git add services/core
git commit -m "feat(core): 페이퍼 SELL과 회계 체결 추가"
```

### Task 5: Gate evidence, full verification, and durable memory

**Files:**
- Create: `gates/g3k-paper-fill-accounting.md`
- Modify: `goal.md`
- Modify: `PLAN.md`
- Modify: `GATES.md`
- Modify: `CONTEXT.md`
- Modify: `DESIGN.md`
- Modify: `README.md`
- Modify: `docs/omni-folio-plan.md`
- Modify: relevant Omni-Folio Obsidian project pages through the `obsidian-memory` workflow

**Interfaces:**
- Consumes: Tasks 1-4 implementation, SDD ledger, task reviews, backup evidence, and cleanup checks.
- Produces: an evidence-backed G3.8C2 closure and explicit G3.8C3 boundary.

- [ ] **Step 1: Record the exact product boundary**

Document ex-post closed-bar simulation, cutoff causality, whole-share specialization, session policy equality, one-active-order ceiling, fixed per-fill fee, SELL tax, directional slippage, replay-derived cash/FIFO lots, and one fill journal. Remove or replace every statement that still describes the current ask-based BUY-only runner as authoritative.

Keep durable marks, instrument/order price cutoffs for equity, valuation, return/drawdown, performance thresholds, scheduler, broker/live paths, and automatic halt/rollback explicitly open under G3.8C3+.

- [ ] **Step 2: Independent task and whole-branch review**

Require task review after every implementation task and one final read-only Sol review over the complete plan diff. Review must cover causal cutoff, legacy exclusion, session/policy ownership, lease/fencing on fills, synthetic K2C isolation, direct-writer guards, exact decimal/FIFO math, concurrency, backup/restore, and no second fill journal. Every Critical/Important finding enters the bounded SDD fix/re-review loop with a new RED test where behavioral.

- [ ] **Step 3: Full verification**

Run:

```bash
make check
make smoke
cd services/core && go test -race -count=1 ./...
cd services/core && govulncheck ./...
git diff --check
```

Expected: all commands PASS with no warning treated as evidence of live/broker behavior.

- [ ] **Step 4: Resource cleanup and proof**

Remove only test-owned Python bytecode/cache and explicit Omni-Folio temporary artifacts. Verify no listener on port `18080`, no Omni-Folio-owned `/tmp` path, no labeled Omni-Folio Podman container, and no Kind cluster created by this plan. Preserve unrelated/unlabeled resources and ignored `/api-key/` without reading, staging, modifying, or deleting it.

- [ ] **Step 5: Sync durable memory**

Update the Omni-Folio project hub, staged architecture, and index so the current gate is G3.8C2 complete and G3.8C3 next. Record the durable decisions, not a worklog-only summary: ex-post model boundary, cutoff invariant, one fill journal, session policy equality, whole shares, one active order, and automatic authority prohibition.

- [ ] **Step 6: Commit evidence**

```bash
git add goal.md PLAN.md GATES.md CONTEXT.md DESIGN.md README.md docs/omni-folio-plan.md gates/g3k-paper-fill-accounting.md
git commit -m "docs(core): 페이퍼 체결 회계 증거 기록"
```

Push, PR, merge, deployment, credentials, broker/live activation, and paid resources remain unperformed without explicit approval.
