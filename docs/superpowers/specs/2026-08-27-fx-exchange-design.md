# FX Exchange Ledger Design

## Goal

Add one append-only, atomic foreign-exchange event to the existing CSV preview/apply ledger so a user can record an exact amount sold in one currency and an exact amount bought in another. Preserve v8 data and prove that a signed v5/schema-v8 backup can be migrated and verified on schema v9 without modifying the backup artifact.

## Chosen contract

`FX_EXCHANGE` is one event and one import row:

- `currency` / `amount`: sold cash leg; `amount` is a canonical decimal smaller than zero.
- `counter_currency` / `counter_amount`: bought cash leg; `counter_amount` is a canonical decimal greater than zero.
- the currencies are distinct three-letter uppercase codes.
- `symbol`, `quantity`, `price`, `fee`, and `corrects_source_event_id` are absent.

One row keeps both legs inside the existing preview fingerprint, source-event idempotency key, SQLite transaction, receipt, ledger revision, and provenance. The event does not store or derive an exchange rate. A fee remains a separate `FEE` event so its currency and provenance stay explicit. `CASH_VOID` does not target FX events in this slice.

## Storage and replay

Migration `009_fx_exchange.sql` rebuilds the insert-only `events` table with nullable `counter_currency` and `counter_amount` columns. A table `CHECK` requires both columns only for `FX_EXCHANGE`, enforces opposite signs and distinct currencies, and forbids them on every other event. Existing v8 rows copy with both columns null; the self foreign key, one-void-per-target uniqueness, cash-void guard, and insert-only triggers remain unchanged.

Snapshot replay adds the sold amount to `cash[currency]` and the bought amount to `cash[counter_currency]`. It does not create a security position or realized PnL. Both decimal strings are parsed independently, so malformed stored data fails closed.

## Compatibility and recovery

The backup JSON shape remains `omni-folio-backup.v5`; new backups declare `omni-folio.sqlite.v9`. Verification accepts only schema v8 or v9 manifests. A v8 artifact is hash-checked in place, copied to an owned temporary directory, migrated to v9, then subjected to the same integrity, schema, ledger golden, order, broker, strategy, and revision proof as a native v9 candidate. The original backup and manifest are never modified, and the temporary copy is removed on success or failure.

`createBackup` removes only the output and manifest paths it established as absent when any later candidate verification or manifest write fails. It never removes a pre-existing target.

## API and Flutter

CSV keeps the existing required header and adds optional `counter_currency` and `counter_amount` columns that become required together only for `FX_EXCHANGE`. Mapping advances to `canonical-transaction.v4`, invalidating stored v3 previews.

OpenAPI adds the two optional fields to its closed normalized-transaction schema and conditionally requires them only for FX. Flutter parsing rejects a missing or partial counter leg. Import review presents `USD 100 매도 -> KRW 137000 매수` as two explicit cash legs at 200% text with a single semantics label; it does not claim a quoted rate, broker execution, tax result, current valuation, or investment performance.

## Verification boundary

Evidence must cover valid preview/apply/replay/idempotency, invalid legs, source-event conflicts across the counter leg, SQLite direct-write rejection, v1/v8-to-v9 migration, v9 backup restore, v8 backup copy-migrate-verify, failed-candidate cleanup, closed OpenAPI, Flutter parser and 320px/200% disclosure, full regression, race, and owned-resource cleanup.

This slice does not add `fx_rates`, base-currency valuation, FX corrections, broker cash reconciliation, live rates, live orders, or tax-lot conversion.
