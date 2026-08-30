# G3.8C3 — Immutable paper performance evidence

Scope: account-global `paper_fixture` daily-close marks and replay-derived equity, return, and drawdown. This is local ex-post paper evidence, not broker truth, profitability, threshold authority, production, or live readiness.

## Contract

- [ ] One schema-v18 append-only performance event owns transaction-current order and market cutoffs, current selection provenance, and an account-global predecessor.
- [ ] Bounded accounting reuses the sole capitalized `FILL_RECORDED` journal and excludes fill evidence whose sequence exceeds the order cutoff or whose bound bar closes after `as_of`.
- [ ] Every open KRX/KRW position has exactly one same-`as_of` immutable `paper_fixture` daily close at or below the price cutoff; cash-only points still require cutoff-bounded daily-close existence, and missing, ambiguous, arbitrary, or future `as_of` fails with zero writes.
- [ ] Exact cash, open cost, market value, realized/unrealized/total PnL, equity, scale-8 half-even returns, peak, drawdown, and max drawdown reconcile from unrounded rationals.
- [ ] Strategy selection or rollback never resets the session baseline, cash/lots, previous point, peak, or max drawdown; rollback to `no_strategy` remains markable with an explicit non-attribution provenance value.
- [ ] Exact retries return the first validated event; conflicting duplicate, out-of-order `as_of`, concurrent divergence, UPDATE, and DELETE fail closed.
- [ ] Recovery independently reconstructs every cutoff, mark, value, predecessor, JSON, and hash; backup v12/schema v18 proves digest/event/mark counts and v11 migrates only through an owned copy to an empty C3 log.
- [ ] Focused RED/GREEN, race, full `make check`, `make smoke`, `govulncheck ./...`, `git diff --check`, independent review, and owned-resource inventory pass.

## Forbidden until later gates

- Public API/UI or a claim that fixture performance is current, broker-backed, or profitable.
- Scheduler, performance threshold, automatic halt/rollback, promotion, credential, provider call, broker order, or live-money path.
- Alternate/fallback marks, FX, mixed currency, calendar/freshness inference, mutable projections, new dependency, or second accounting implementation.

Design: [`docs/superpowers/specs/2026-08-31-paper-performance-evidence-design.md`](../docs/superpowers/specs/2026-08-31-paper-performance-evidence-design.md)
