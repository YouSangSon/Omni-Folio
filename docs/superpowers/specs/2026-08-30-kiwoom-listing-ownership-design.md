# Kiwoom Listing Ownership Design

## Goal

Add a credential-free, owner-declared listing authority that proves which local instrument a Kiwoom KRX symbol belongs to before market data is fetched or stored. Preserve existing price evidence, keep valuation and public APIs unchanged, and provide append-only correction and backup recovery.

## Scope

This gate owns only the tuple `instrument_id + venue + symbol + currency` and its local declaration history. It is not a provider security master and does not claim that the broker or exchange verified the declaration.

The registry has no public route or Flutter surface. A later credentialed runtime may call the same internal declaration and resolution contracts, but this gate adds no credential, broker request, scheduler, worker, freshness policy, valuation promotion, order authority, or live-money path.

## Event Contract

`instrument_listing_events` is an append-only log with two event types:

- `DECLARE` makes one `(venue, symbol, currency)` tuple resolve to one `instrument_id`.
- `REVOKE` removes the matching active declaration. A correction is `REVOKE` followed by a new `DECLARE`.

Every event records:

- opaque `event_id`;
- `event_type=DECLARE|REVOKE`;
- `authority=owner_declared`;
- canonical `instrument_id`, uppercase `venue` and `symbol`, three-letter uppercase `currency`;
- `expected_previous_event_id`, using `no_event` for the first declaration;
- canonical UTC `recorded_at`;
- canonical SHA-256 over every authoritative field except the database sequence and the hash itself.

Exact declaration of an already active tuple is idempotent and returns the existing event. A conflicting declaration fails until the current tuple is revoked. Revocation requires the same active instrument and returns the existing revoke event only when replay proves that exact terminal state. The caller does not provide the predecessor; the service derives it inside the write transaction and SQLite rejects stale direct inserts.

Resolution replays the complete log, validates hashes, predecessor links, transitions and canonical metadata, then returns only the latest active declaration for the exact venue, symbol and currency. Full-log replay is intentionally linear for the current personal/local scale; measured registry volume or latency must justify a separate materialized view.

## SQLite Schema

Migration 013 creates only `instrument_listing_events`, its lookup index and insert-only/state-guard triggers. It does not mutate or infer from ledger events or `security_price_observations`.

```sql
CREATE TABLE instrument_listing_events (
    sequence INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL CHECK (event_type IN ('DECLARE', 'REVOKE')),
    authority TEXT NOT NULL CHECK (authority = 'owner_declared'),
    instrument_id TEXT NOT NULL,
    venue TEXT NOT NULL,
    symbol TEXT NOT NULL,
    currency TEXT NOT NULL,
    expected_previous_event_id TEXT NOT NULL,
    record_sha256 TEXT NOT NULL,
    recorded_at TEXT NOT NULL
) STRICT;
```

SQLite constraints enforce bounded non-empty IDs, uppercase venue/symbol, canonical currency, lowercase 64-character hashes and UTC-shaped timestamps. The application additionally enforces the repository's safe-ID and canonical-UTC validators.

The index is exactly:

```sql
CREATE INDEX instrument_listing_events_current_idx
ON instrument_listing_events(venue, symbol, currency, sequence DESC);
```

`no_update` and `no_delete` triggers keep the table insert-only. `state_guard` compares `expected_previous_event_id` to the latest tuple event and permits only `REVOKE -> DECLARE` or `DECLARE -> REVOKE`, with a first `DECLARE` from `no_event`. Restore verification pins the normalized table, index and all three triggers.

## Kiwoom Read and Write Boundary

G4T resolves `XKRX + six-digit symbol + KRW` before any token or market-data request. Missing, revoked or corrupt ownership produces zero provider calls.

After `LatestTrade` succeeds, G4S resolves again inside the price-write transaction. This prevents a declaration revoked during the provider call from authorizing storage. The stored instrument ID always comes from the active declaration, never `instrumentIDForSymbol` or a caller argument.

The generic price writer also requires the exact active declaration before inserting a new `kiwoom_mock` or `kiwoom_production` row, preventing direct internal bypass. Exact replay of an existing structurally valid row remains idempotent even when it predates the registry.

Price validation is split conceptually:

- record validation preserves source, source-observation ID, KRX market identity, exact decimal and timestamp integrity, including structurally valid legacy v12 Kiwoom rows with alternate internal IDs;
- write authority requires the current active declaration only for a new Kiwoom insert.

`local_fixture` behavior and `local_fixture`-only holding valuation remain unchanged. `instrumentIDForSymbol` remains only the current CSV ledger convention; it is not listing authority.

## Migration and Backup

The current schema becomes `omni-folio.sqlite.v13` and backup format becomes `omni-folio-backup.v8`.

The manifest adds:

- `instrument_listing_state_sha256`;
- `instrument_listing_event_count`;
- `active_instrument_listing_count`.

The verification receipt adds:

- `instrument_listing_check`;
- `candidate_instrument_listing_state_sha256`.

Backup creation compares source and candidate listing proofs. Restore proof validates the complete event log and exact DDL/index/triggers.

A v7/schema-v12 backup is accepted only as legacy after its original hash and size are verified. Verification copies it to an owned temporary directory, migrates the copy to v13, requires the empty listing proof, and leaves the source unchanged. Older supported v5/v6/v7 artifacts follow the existing owned-copy path and also produce an empty listing proof. A legacy manifest containing v8-only listing fields or a v8 manifest omitting them is rejected.

Migration preserves every structurally valid v12 Kiwoom observation without registering it. A later exact declaration may authorize matching future writes; no row is rewritten, deleted or automatically assigned. Historical/effective-dated ownership remains outside this gate, so this registry must not be presented as tax or historical-performance truth.

## Failure Handling

- Invalid declaration input fails before a transaction is committed.
- Hash, predecessor, transition or metadata corruption fails the complete registry replay.
- A missing or revoked Kiwoom tuple fails before network access.
- A revoke between G4T preflight and G4S persistence fails the write and preserves earlier price evidence.
- Backup proof mismatch, missing schema guard or mixed manifest version fails activation.
- No error exposes credentials, account identifiers or provider payloads.

## Verification

Executable evidence covers:

1. declaration, exact idempotency, conflict, revoke, redeclare and multi-venue resolution;
2. replay rejection for hash, predecessor, transition and metadata corruption;
3. missing, revoked and corrupt ownership before Kiwoom network access;
4. direct writer mismatch and revoke-during-fetch rejection without new price rows;
5. preservation of canonical and alternate-ID legacy v12 price rows without inferred ownership;
6. v8/schema-v13 backup round trip, exact DDL/index/trigger checks and v7/schema-v12 owned-copy migration;
7. readiness expectations for exactly 13 migrations and rejection of future schema 14;
8. unchanged local-fixture valuation and public API boundaries.

The merge gate is focused RED/GREEN, full `make check`, `make smoke`, Go race, `govulncheck`, independent read-only review, and proof that owned processes and temporary resources are removed.

## Non-goals

No public registration API or UI, provider lookup, ISIN/FIGI, company names, lot/tick sizes, trading calendar, timezone/freshness policy, effective dating, historical valuation, base-currency performance, credentialed call, scheduler, WebSocket lifecycle, order mutation, deployment, PostgreSQL or Kubernetes resource is added.
