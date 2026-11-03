# G3.8B Go-trusted Strategy Execution-policy Contract

Scope: independently validate the execution assumptions inside immutable Python research evidence before Go admits or restores it. This gate adds no paper accounting, strategy execution, authority, or broker access.

## Acceptance

- [x] The shared Go evidence decoder used by registration and registry recovery requires exactly `starting_cash`, `fee`, `tax`, `slippage_bps`, `delay_bars`, `max_participation`, `signal_price`, and `fill_price`.
- [x] Every numeric input is a canonical decimal string. Starting cash is positive; fee, tax, and slippage are non-negative; delay is a whole number of at least one; participation is greater than zero and at most one.
- [x] Signal and fill semantics remain `bar_close` and `next_eligible_bar_open`.
- [x] A malicious producer cannot bypass validation by recomputing `result_sha256`; rejection leaves no registered evidence.
- [x] The public result JSON Schema declares the same exact execution object and canonical decimal shape.
- [x] Valid existing artifacts keep their result and canonical artifact hashes. Schema remains v14 and backup remains v9 because no durable row or backup shape changed.
- [x] No typed policy loader, duplicated fill journal, paper order, SELL, capital allocation, fee/tax/slippage application, equity/PnL, threshold, scheduler, UI, broker call, credential, authority mutation, or live path was added.

## Evidence

- RED: `cd services/core && go test . -run '^TestG38BRegistryRejectsUnsafeExecutionContract$' -count=1` admitted every correctly rehashed unsafe execution object.
- GREEN: the same focused command and the G3 registry suite pass after validating the common decoder.
- Schema RED/GREEN: focused research tests first failed because result `execution` had no exact-object contract and later because zero starting cash matched the shared decimal pattern. Field-specific patterns now reject runtime-invalid ranges.
- Python parity RED/GREEN: `-0` cost inputs were accepted by Python but rejected by Go and the public schema; the shared Python decimal boundary now rejects signed zero and all 15 research tests pass.
- Repository: `make check` passes Go format/vet/test, Flutter analyze and 65 tests, Python compile and 15 tests, and 15 JSON contracts.
- Runtime and safety: `make smoke`, `go test -race -count=1 ./...`, and `govulncheck ./...` pass.
- Independent review rejected signed-zero parity and then loose schema ranges; both received focused regressions before final GO with zero reviewer edits.
- Cleanup left no Python bytecode/cache, port `18080` listener, Omni-Folio `/tmp` artifact, labeled Podman container, or Kind cluster.

## Still Open

- Consume the selected policy in a current-selection-bound paper accounting session.
- Target reduction/SELL, capital allocation, bid-side fills, exact cash/fee/tax/slippage accounting, and immutable equity marks with order/price sequence cutoffs.
- Versioned return/drawdown thresholds and distinct automatic halt/rollback provenance.
- Scheduler, alerts, shadow/canary promotion, credentialed broker observation, and every live-money path.
