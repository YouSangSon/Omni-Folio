# Kiwoom Durable Price Observation Design

## Goal

Persist the existing credential-free `KiwoomLatestTrade` result in the append-only security-price series without inventing a provider trade ID, enabling public valuation, adding a scheduler, or changing any live-money path.

## Chosen contract

The durable identity is a versioned **price-observation slot**, not a trade-tape event. Its SHA-256 source ID binds the Kiwoom environment, `ka10079`, canonical instrument ID, KRX symbol and venue, currency, price-adjustment policy, and provider-observed second. Price and fetch time are deliberately excluded from the slot ID:

- the first valid observation fixes the slot's exact price and first fetched time;
- a later fetch of the same slot and price is an idempotent no-op that returns the first durable observation;
- a different price in the same slot fails closed instead of pretending the second-level provider time identifies distinct trades.

The source namespace is `kiwoom_mock` or `kiwoom_production`. Price adjustment remains explicitly `unspecified`; the official request does not prove adjusted-price semantics. Kiwoom writes derive `instrument_<lowercase symbol>` through the same helper used by ledger import and reject caller overrides. This aligns the two existing local domains but does not prove exchange identity or replace a future durable instrument/listing registry.

## Storage and recovery

Schema v12 rebuilds only `security_price_observations` to admit the two Kiwoom source namespaces. It preserves the existing strict positive-price checks, insert-only triggers, canonical row hash, unique source identity, and one observation per source/instrument/symbol/venue/currency/observed-time/adjustment slot.

Backup JSON remains `omni-folio-backup.v7` and new artifacts declare schema v12. A signed v7/schema-v11 artifact retains its security-price proof, is hash-checked in place, copied to an owned temporary directory, migrated to v12, and verified without modifying the source artifact.

## Deliberate boundary

The existing holding valuation and internal latest-price query continue to accept only `local_fixture`, so durable Kiwoom observations cannot silently become valuation authority. No route, Flutter surface, credential, external request, retry loop, scheduler, source-priority rule, freshness/calendar policy, realtime frame ingestion, order decision, or live authority is added.

## Verification

Executable evidence must cover exact slot derivation, mock/production separation, repeated-fetch no-op, same-slot changed-price rejection, malformed/tampered trade rejection, v11-to-v12 data preservation, schema/trigger/index restore proof, current backup restore, legacy v11 copy-migration, and the unchanged public/holding-valuation boundary.
