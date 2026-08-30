# Paper Performance Evidence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist one immutable, account-global paper equity curve from cutoff-bounded paper fills and complete daily-close fixture marks.

**Architecture:** Extend the existing pure `paperdomain` with exact valuation/performance functions, then let one Go application use case own current cutoffs, bounded accounting replay, mark selection, append-only persistence, and recovery. SQLite schema v18 and backup v12 carry one performance-event table and digest; no second accounting journal, mutable projection, public API/UI, scheduler, or automatic authority is added.

**Tech Stack:** Go 1.25, `math/big.Rat`, existing `internal/exact`, SQLite STRICT tables/triggers, existing backup/restore harness, root Make cleanup wrapper.

**Spec:** `docs/superpowers/specs/2026-08-31-paper-performance-evidence-design.md`

## Global Constraints

- Policy is exactly `paper-performance-account-v1` over `paper_fixture/KRX/KRW/1d/Asia-Seoul/unspecified` closes.
- Caller input is only `account_ref` and canonical UTC `as_of`; every financial field and cutoff is Go-derived.
- Domain imports only Go stdlib and `internal/exact`; add no dependency, interface, projection, cache, service, API, UI, scheduler, threshold, provider, credential, or live path.
- Use `paperdomain.Account` and the sole capitalized v3 `FILL_RECORDED` journal; never duplicate cash/FIFO/PnL rules.
- Ratios use exact rationals and one scale-8 half-even quantization; money remains exact canonical decimal.
- Run tests through owned temp roots (`t.TempDir` or root `make test/check/smoke`) and finish with owned-resource inventory.

---

### Task 1: Pure account valuation and performance series

**Files:**
- Create: `services/core/internal/paperdomain/performance.go`
- Create: `services/core/internal/paperdomain/performance_test.go`

**Interfaces:**
- Consumes: existing `paperdomain.AccountState`, `Lot`, `exact.ParseDecimal`, `exact.FormatDecimal`, `exact.QuantizeHalfEven`.
- Produces:

```go
type PositionValuation struct {
    Quantity, OpenCost, MarketValue, UnrealizedPnL string
}

type Valuation struct {
    Cash, OpenCost, MarketValue, RealizedPnL string
    UnrealizedPnL, TotalPnL, Equity           string
    Positions                                 map[string]PositionValuation
}

type PerformancePoint struct {
    Equity, PeakEquity, PeriodReturnState string
    PeriodReturn, CumulativeReturn         string
    Drawdown, MaxDrawdown                  string
}

func ValueAccount(startingCash string, state AccountState, closes map[string]string) (Valuation, error)
func CalculatePerformance(startingCash string, equities []string) ([]PerformancePoint, error)
```

- [x] **Step 1: Write domain examples first**

Add table-driven tests proving:

```go
func TestValueAccountReconcilesExactMoney(t *testing.T) {
    state := AccountState{Cash: "39.9", RealizedPnL: "0", Lots: map[string][]Lot{
        "005930": {{Quantity: "1", Cost: "60.1"}},
    }}
    got, err := ValueAccount("100", state, map[string]string{"005930": "70"})
    // Cash 39.9 + market 70 = equity 109.9; open cost 60.1;
    // unrealized/total PnL 9.9 and equity-starting cash reconcile exactly.
}

func TestCalculatePerformanceUsesRawPeakAndHalfEvenScale8(t *testing.T) {
    got, err := CalculatePerformance("100", []string{"120", "90", "110", "130", "100"})
    // period/cumulative returns, peaks 120/130, current drawdown and
    // max drawdown 0.25 are asserted as canonical strings.
}
```

Also assert cash-only success; multi-symbol sorting independence; missing/extra/non-positive/non-canonical mark rejection; negative quantity/cost/cash rejection; starting cash zero rejection; recurring one-third quantization; total loss; zero previous-equity period state; malformed equity failure; and no partial output on error.

- [x] **Step 2: Run RED**

Run:

```bash
cd services/core && go test -count=1 ./internal/paperdomain -run '^(TestValueAccount|TestCalculatePerformance)'
```

Expected: compile-time RED because `Valuation`, `PerformancePoint`, `ValueAccount`, and `CalculatePerformance` do not exist. Commit only the failing tests as `test(core): 페이퍼 성과 도메인 계약 추가`.

- [x] **Step 3: Implement the minimum pure functions**

`ValueAccount` parses every input before returning output, requires the close-key set to equal exactly the positive-lot symbol set, derives each `PositionValuation`, sums those values, and checks both PnL equalities before formatting. `CalculatePerformance` parses the complete series, keeps raw `big.Rat` previous/peak/max values, and formats ratios through:

```go
func performanceRatio(value *big.Rat) (string, error) {
    return exact.FormatDecimal(exact.QuantizeHalfEven(value, 8))
}
```

When previous equity is zero, set `PeriodReturnState="undefined_zero_denominator"` and `PeriodReturn=""`; otherwise use `defined`. Do not add a generic arithmetic abstraction.

- [x] **Step 4: Run GREEN and coverage**

Run:

```bash
cd services/core && go test -count=1 ./internal/exact ./internal/paperdomain
cd services/core && go test -cover ./internal/paperdomain
```

Expected: all direct tests pass and new functions each meet at least 80% statement coverage. Commit production code as `feat(core): exact 페이퍼 성과 계산 추가`.

### Task 2: Cutoff-bounded accounting and complete mark derivation

**Files:**
- Modify: `services/core/paper_accounting.go`
- Create: `services/core/paper_performance.go`
- Create: `services/core/paper_performance_test.go`

**Interfaces:**
- Consumes: complete recovery validation, `paperdomain.Account`, `PaperMarketBarObservation`, current order/market sequences.
- Produces:

```go
type paperAccountingCutoff struct { OrderSequence int64; AsOf string }
func replayPaperAccountingAt(ctx context.Context, q orderQuerier, cutoff paperAccountingCutoff) (map[string]paperAccountState, error)

type paperPerformanceMark struct {
    Symbol, Quantity, ObservationID, Close, OpenCost, MarketValue, UnrealizedPnL string
    ObservationSequence int64
}
func derivePaperPerformanceMarks(ctx context.Context, q orderQuerier, state paperAccountState, startingCash, asOf, recordedAt string, marketCutoff int64) ([]paperPerformanceMark, paperdomain.Valuation, error)
```

- [x] **Step 1: Write failing bounded-replay and mark tests**

Use existing G3.8C2 fixture helpers to create fills/bars and prove:

- a fill above the order cutoff is excluded;
- a fill whose bound bar closes after `as_of` is excluded even if its modeled occurrence is at bar open;
- all rows are still recovery-validated before the bounded result is returned;
- either legacy paper-signal v1 or v2 order on the account rejects bounded performance replay instead of appearing as cash-only;
- exactly one same-`as_of` close per held symbol produces symbol-sorted marks;
- missing/ambiguous/wrong-series/future-availability marks fail without returning a partial set;
- cash-only state returns empty marks only when at least one eligible bar has `close_at=as_of` under the cutoff;
- future/arbitrary cash-only `as_of` fails.

- [x] **Step 2: Run RED**

Run:

```bash
cd services/core && go test -count=1 . -run '^(TestG38C3BoundedAccounting|TestG38C3CompleteMarks|TestG38C3CashOnly)'
```

Expected: compile-time RED for missing cutoff/mark functions. Commit tests as `test(core): 성과 cutoff와 mark 계약 추가`.

- [x] **Step 3: Refactor replay once, then derive marks**

Keep `replayPaperAccounting(ctx,q)` behavior unchanged by making it call one internal replay implementation with no cutoff. The shared loop maintains the existing full validation account for every fill and an optional bounded account that receives the already-validated/calculated fill only when both cutoffs admit it. This preserves validation of later rows without calculating fills from truncated state. Do not add a second SQL/replay path.

`derivePaperPerformanceMarks` queries only observations `sequence<=marketCutoff`, validates each stored row with `loadPaperMarketBarByID`, requires complete exact keys, calls `paperdomain.ValueAccount` once, and combines its per-symbol values with the selected observation IDs to build canonical sorted marks. It returns both marks and that valuation so Task 3 never recomputes financial arithmetic. The cash-only path executes an `EXISTS` query for an eligible close-time anchor.

- [x] **Step 4: Run GREEN and regressions**

Run:

```bash
cd services/core && go test -count=1 . -run '^(TestG38C3BoundedAccounting|TestG38C3CompleteMarks|TestG38C3CashOnly|TestG38C2PaperAccounting|TestG38C2PaperRunner)'
cd services/core && go test -race -count=1 ./...
```

Expected: new tests and all C2 replay/runner regressions pass. Commit as `feat(core): cutoff 기반 페이퍼 평가 입력 도출`.

### Task 3: Atomic performance storage, recovery, and backup v12

**Files:**
- Create: `services/core/migrations/018_paper_performance_events.sql`
- Modify: `services/core/core.go`
- Modify: `services/core/core_test.go`
- Modify: `services/core/order_backup_test.go`
- Modify: `services/core/paper_performance.go`
- Modify: `services/core/paper_performance_test.go`
- Modify: `services/core/order_schema_restore_test.go`
- Modify: `contracts/backup-manifest.schema.json`
- Modify: `contracts/fixtures/golden-backup-manifest.json`
- Modify: directly coupled current-format assertions in `services/core/broker_snapshot_test.go`, `execution_authority_test.go`, `fx_exchange_test.go`, `fx_observation_test.go`, `paper_evaluation_test.go`, `security_price_observation_test.go`, `strategy_registry_test.go`, `paper_execution_authority_test.go`, `core_test.go`, and `order_state_test.go`

**Interfaces:**
- Consumes: Task 1 valuation/series, Task 2 cutoff replay/marks, current strategy registry and session.
- Produces: one atomic schema-v18/backup-v12 contract plus:

```go
type PaperPerformanceEvent struct {
    PerformanceID, SchemaVersion, PolicyVersion, AccountRef string
    PaperAccountingSessionID, StrategySelectionEventID      string
    SelectedStrategyResultRef, ExpectedPreviousPerformanceID string
    StrategySelectionSequenceCutoff, OrderEventSequenceCutoff int64
    PaperMarketSequenceCutoff int64
    AsOf, PaperAccountStateSHA256, MarksSHA256, MarksJSON string
    MarkCount int
    Cash, OpenCost, MarketValue, RealizedPnL, UnrealizedPnL string
    TotalPnL, Equity, PeakEquity, PeriodReturnState, PeriodReturn string
    CumulativeReturn, Drawdown, MaxDrawdown, RecordedAt string
}

func (s *Service) evaluatePaperPerformance(ctx context.Context, accountRef, asOf string) (*PaperPerformanceEvent, error)
func provePaperPerformanceRecovery(ctx context.Context, q orderQuerier) (paperPerformanceRecoveryProof, error)
```

- [x] **Step 1: Write failing persistence, concurrency, corruption, schema, backup, and legacy tests**

Prove cash-only baseline and an up/down/recovery series; exact-close `as_of` before or equal to `session.RecordedAt` rejection; exact retry after later evidence; retry of an older key fails when any later order/market row is corrupt; strictly increasing `as_of`; strategy change and rollback to `no_strategy`; same-key concurrent writers; different-`as_of` writers in both commit orders (earlier-first chains, later-first rejects backfill); transaction-current order/market/selection cutoffs; caller cannot supply totals; UPDATE/DELETE rejection; direct changed duplicate rejection; tampered cutoff/mark/value/ratio/predecessor/JSON/hash rejection; missing table/index/FK/trigger/state guard rejection.

Also assert source/candidate performance digests and event/mark counts match; manifest/receipt omit none of the new fields; v11/schema-v17 is verified before an owned copy migrates to an empty C3 log; source DB/manifest hashes and sizes remain unchanged; forged digest/count/status and performance schema drift block activation. Existing v5-v10 manifest classifications, field-shape checks, and owned-copy migrations must remain accepted exactly as before.

- [x] **Step 2: Run RED**

Run:

```bash
cd services/core && go test -count=1 . -run '^(TestG38C3PaperPerformance|TestG38C3Concurrent|TestG38C3Restore|TestG38C3Backup|TestG38C3LegacyV11)'
```

Expected: RED because migration 018, service method, event/recovery proof, and backup-v12 fields do not exist. Commit all failing tests together as `test(core): 불변 페이퍼 성과 복구 계약 추가`.

- [x] **Step 3: Add migration 018**

Create one STRICT `paper_performance_events` table with schema constant `paper-performance-evaluation.v1`, all event columns, record JSON/SHA, FK to session/current selection, unique performance ID and unique `(policy_version,account_ref,paper_accounting_session_id,as_of)`. The first predecessor sentinel is exactly `no_performance`. Add no-update/no-delete triggers and one state guard that requires transaction-current cutoffs, explicit current selected result or `no_strategy`, exact latest per-account predecessor, and strictly increasing `as_of`.

Bump `latestSchema` to 18 and append `018_paper_performance_events.sql` to the migration list only in the same production change that bumps backup format/schema to v12/v18. Pin the new table, indexes, foreign keys, and triggers in restore schema validation; no committed state may pair schema v18 with backup v11/schema v17.

- [x] **Step 4: Implement one transactional use case and recovery**

`evaluatePaperPerformance` starts the immediate transaction before reading current state and proves all prerequisite recovery before it may return even an existing key. It then loads the immutable session/current selection, rejects account-scoped legacy v1/v2 paper orders, returns an existing fully validated key if present, or requires `session.RecordedAt < as_of <= event.RecordedAt`, captures cutoffs, bounded-replays accounting, derives marks/valuation, replays prior account events to calculate the complete performance series, hashes canonical marks/event, inserts, proves the proposed performance log, and commits.

`provePaperPerformanceRecovery` independently repeats those derivations for every stored event in global sequence. It rejects a corrupt row even if the row is after an older event's cutoff. The simple implementation is potentially `O(P × (F+B+P))` for performance points P, fills F, and bars B; add a `ponytail:` comment naming measured event volume/latency as the upgrade trigger for a disposable projection, but add no cache now.

- [x] **Step 5: Extend the existing backup proof in the same GREEN change**

Add `paper_performance_state_sha256`, `paper_performance_event_count`, and `paper_performance_mark_count` to `BackupManifest`. Add `candidate_paper_performance_state_sha256` and `paper_performance_check` to the verification receipt. Compare source and candidate in `createBackup`, require these fields in current manifest validation, classify v11/schema-v17 as the direct legacy format, and verify empty performance proof after owned-copy migration. Preserve every v5-v10 classification and original field-shape rule. Rename schema title/ID/constants to v12/v18 and update the golden fixture. Do not add a second backup pipeline.

- [x] **Step 6: Run GREEN and race**

Run:

```bash
cd services/core && go test -count=1 . -run '^(TestG38C3PaperPerformance|TestG38C3Concurrent|TestG38C3Restore|TestG38C3Backup|TestG38C3LegacyV11)'
cd services/core && go test -race -count=1 ./...
```

Expected: all new persistence/recovery/concurrency/backup tests and full race suite pass. Commit the schema, application, recovery, backup, contract, and fixture changes atomically as `feat(core): 불변 페이퍼 성과 복구 증거 저장`.

### Task 4: Completion evidence and project state

**Files:**
- Modify: `goal.md`
- Modify: `PLAN.md`
- Modify: `GATES.md`
- Modify: `gates/g3l-paper-performance-evidence.md`
- Modify: `docs/README.md`
- Create: `docs/testing/g3l-paper-performance-evidence.tdd.md`

**Interfaces:**
- Consumes: Task 3 atomic schema/application/recovery/backup result.
- Produces: fresh root verification, resource inventory, TDD evidence, completed G3.8C3 project state.

- [x] **Step 1: Run focused GREEN and root verification**

Run fresh:

```bash
cd services/core && go test -count=1 . -run '^(TestG38C3|TestG38C2|TestG38C1)'
cd services/core && go test -race -count=1 ./...
make check
make smoke
cd services/core && govulncheck ./...
git diff --check
```

Expected: every command exits 0. Do not claim UI, broker, deployment, or live evidence.

- [x] **Step 2: Audit owned resource cleanup**

Run `make clean-test-resources`, then verify no `omni-folio-test.*`/`omni-folio-smoke.*` dead-owner roots, port `18080` listener, `apps/client/build`, `apps/client/coverage`, `services/core/core`, research bytecode/cache, Omni-Folio-labeled Podman container/network/volume, or Kind cluster remains. Never use global prune.

- [x] **Step 3: Record factual evidence and commit**

Update the G3.8C3 leaf gate checkboxes only for observed commands, add RED/GREEN/coverage/resource evidence to `docs/testing/g3l-paper-performance-evidence.tdd.md`, mark PLAN/GATES completion, and preserve deferred thresholds/UI/live scope. Commit as `feat(core): 페이퍼 성과 복구 증거 완성` after an independent spec and code review returns GO.
