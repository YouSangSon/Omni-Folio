# Kiwoom Listing Ownership Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Require append-only owner-declared listing evidence before Kiwoom market data can be fetched or newly stored.

**Architecture:** Add a SQLite event log that replays active (venue, symbol, currency) to instrument identity, then bind G4T preflight and G4S transaction-time writes to that replay. Bump schema and backup contracts together so the registry has the same recovery proof as other financial state while legacy prices remain preserved but unowned.

**Tech Stack:** Go standard library, database/sql, SQLite STRICT tables and triggers, existing SHA-256 and canonical-time helpers, existing backup JSON contracts.

**Spec:** docs/superpowers/specs/2026-08-30-kiwoom-listing-ownership-design.md

## Global Constraints

- No credentials, external broker calls, public route, Flutter surface, scheduler, valuation promotion, order authority, deployment, PostgreSQL or Kubernetes resource.
- Migration 013 creates listing state only and never infers from ledger or price rows.
- Every behavioral production change follows RED, observed failure, minimal GREEN.
- Exact replay of legacy v12 Kiwoom price rows remains valid even when an old instrument ID differs from the current ledger convention.
- Tests clean owned files and processes on success, failure and interruption; never use global prune.

---

### Task 1: Durable listing registry and recovery proof

**Files:**
- Create: services/core/migrations/013_instrument_listing_events.sql
- Create: services/core/instrument_listing.go
- Create: services/core/instrument_listing_test.go
- Modify: services/core/core.go
- Modify: services/core/core_test.go
- Modify: services/core/order_state_test.go
- Modify: contracts/backup-manifest.schema.json
- Modify: contracts/fixtures/golden-backup-manifest.json

**Interfaces:**
- Produces: InstrumentListingInput and InstrumentListingEvent.
- Produces: instrumentListingRecoveryProof with SHA256, Events and Active fields.
- Produces: declareInstrumentListing(ctx, input) and revokeInstrumentListing(ctx, input) service methods.
- Produces: resolveInstrumentListing(ctx, q, venue, symbol, currency).
- Produces: replayInstrumentListingRegistry(ctx, q) and proveInstrumentListingRecovery(ctx, q).

- [ ] **Step 1: Write registry RED tests**

Add literal tests whose production mutations are accepting a conflicting active mapping, accepting a stale predecessor, resolving a revoked tuple, or trusting a corrupt row.

~~~go
func TestInstrumentListingDeclareResolveRevokeAndCorrect(t *testing.T) {
    svc, _ := testService(t, []time.Time{
        mustTime("2026-08-30T03:00:00Z"),
        mustTime("2026-08-30T03:01:00Z"),
        mustTime("2026-08-30T03:02:00Z"),
    }, nil)
    samsung := InstrumentListingInput{
        InstrumentID: "instrument_005930",
        Venue: "XKRX",
        Symbol: "005930",
        Currency: "KRW",
    }
    declared, err := svc.declareInstrumentListing(context.Background(), samsung)
    if err != nil { t.Fatal(err) }
    replayed, err := svc.declareInstrumentListing(context.Background(), samsung)
    if err != nil || !reflect.DeepEqual(declared, replayed) {
        t.Fatalf("idempotent declare=%+v err=%v", replayed, err)
    }
    conflicting := samsung
    conflicting.InstrumentID = "instrument_other"
    if _, err := svc.declareInstrumentListing(context.Background(), conflicting); err == nil {
        t.Fatal("active tuple rebound")
    }
    if _, err := svc.revokeInstrumentListing(context.Background(), samsung); err != nil {
        t.Fatal(err)
    }
    if listing, err := resolveInstrumentListing(context.Background(), svc.db, "XKRX", "005930", "KRW"); err == nil || listing != nil {
        t.Fatalf("revoked listing resolved: %+v", listing)
    }
    corrected, err := svc.declareInstrumentListing(context.Background(), conflicting)
    if err != nil || corrected.InstrumentID != "instrument_other" {
        t.Fatalf("corrected=%+v err=%v", corrected, err)
    }
}
~~~

Add separate corruption cases for record_sha256, expected_previous_event_id, event transition, tuple metadata, missing current index and each of the three triggers. Assert proof or restore failure, never source-text presence.

- [ ] **Step 2: Run registry RED**

~~~bash
cd services/core
go test . -run '^TestInstrumentListing' -count=1
~~~

Expected: build failure because the listing types and functions do not exist.

- [ ] **Step 3: Implement migration 013 and minimal registry**

Create the exact STRICT table, current index, no-update, no-delete and state-guard triggers from the spec. In instrument_listing.go reuse safeOrderID, currencyCodePattern, canonicalUTCString, orderJSONHash, s.id and s.now. Add no dependency or interface.

Replay rows in sequence order. Validate every predecessor and DECLARE/REVOKE transition, verify each record hash, build the active map, and hash sequence plus the complete row. Empty registry proof uses the standard empty SHA-256 digest.

- [ ] **Step 4: Expand schema and backup v8 atomically**

Set latestSchema to 13, backupFormat to omni-folio-backup.v8, and backupSchema to omni-folio.sqlite.v13. Retain v7 as a named legacy format.

Add manifest fields instrument_listing_state_sha256, instrument_listing_event_count, and active_instrument_listing_count. Add receipt fields instrument_listing_check and candidate_instrument_listing_state_sha256.

Compare source and candidate listing proofs in createBackup, include listing proof in verifyRestore, and pin the exact table, index and triggers. v8/schema13 requires the new fields. v7/schema11 or schema12 rejects v8-only fields, migrates only an owned copy, and requires an empty listing proof.

- [ ] **Step 5: Update contract fixtures and migration expectations**

Add the five v8 JSON keys to the schema and golden fixture. Change tests that literally assert twelve migrations or future schema 13 so they expect thirteen migrations and reject schema 14. Do not replace unrelated historical version strings.

- [ ] **Step 6: Verify Task 1 GREEN**

~~~bash
cd services/core
go test . -run '^(TestInstrumentListing|TestBackupManifestContractFieldsMatchRuntimeAndFixtures|TestMigrate)' -count=1
go test ./...
~~~

Expected: selected and full core tests pass; current backup proof includes listing digest and counts; legacy source files remain unchanged.

- [ ] **Step 7: Commit Task 1**

~~~bash
git add services/core/migrations/013_instrument_listing_events.sql services/core/instrument_listing.go services/core/instrument_listing_test.go services/core/core.go services/core/core_test.go services/core/order_state_test.go contracts/backup-manifest.schema.json contracts/fixtures/golden-backup-manifest.json
git commit -m "feat(core): 종목 상장 소유권을 영속화"
~~~

---

### Task 2: Enforce active listing ownership in G4S and G4T

**Files:**
- Modify: services/core/security_price_observation.go
- Modify: services/core/security_price_observation_test.go
- Modify: services/core/instrument_listing_test.go

**Interfaces:**
- Consumes: resolveInstrumentListing from Task 1.
- Changes: recordKiwoomLatestTradeObservation derives InstrumentID from the active XKRX/symbol/KRW declaration.
- Changes: captureKiwoomLatestTradeObservation resolves before LatestTrade and G4S resolves again in the write transaction.

- [ ] **Step 1: Write Kiwoom authority RED tests**

Name the production breaks: moving preflight after network, trusting instrumentIDForSymbol, omitting the transaction-time recheck, or making legacy evidence unreplayable.

~~~go
func TestKiwoomCaptureRequiresActiveListingBeforeNetwork(t *testing.T) {
    svc, _ := testService(t, nil, nil)
    calls := 0
    client := newSyntheticKiwoomClient(t, KiwoomMock, kiwoomRoundTripFunc(func(*http.Request) (*http.Response, error) {
        calls++
        return nil, errors.New("must not reach provider")
    }))
    row, err := svc.captureKiwoomLatestTradeObservation(context.Background(), client, "005930")
    if err == nil || row != nil { t.Fatalf("capture=%+v err=%v", row, err) }
    if calls != 0 { t.Fatalf("provider calls=%d", calls) }
}
~~~

Add an active declaration success case, a revoked zero-call case, direct generic-writer mismatch rejection, and a synthetic transport callback that revokes ownership after preflight so persistence fails without appending.

Build a schema-v12 database containing both instrument_005930 and krx_005930 Kiwoom rows before migration. Assert migration preserves the security-price proof, begins with an empty listing proof, and infers neither mapping.

- [ ] **Step 2: Run Kiwoom authority RED**

~~~bash
cd services/core
go test . -run '^(TestKiwoomCaptureRequiresActiveListingBeforeNetwork|TestKiwoomDirectWriterRequiresActiveListing|TestLegacyV12KiwoomRowsRemainUnowned)' -count=1
~~~

Expected: missing listing still reaches the provider or the listing-specific tests fail because G4S and G4T do not resolve the registry.

- [ ] **Step 3: Implement preflight and transaction-time authority**

Remove the instrumentIDForSymbol equality from structural Kiwoom replay validation so alternate-ID legacy rows remain valid evidence. Keep KRX symbol, XKRX venue, KRW currency, canonical price and timestamp, and deterministic source-observation ID validation.

Before G4T calls LatestTrade, resolve the active listing. In the price-write transaction, return exact existing replay first and require the exact active listing before a new Kiwoom insert. Use the resolved instrument ID to derive the source-observation ID and stored row.

Do not require registry ownership for local_fixture and do not fall back to instrumentIDForSymbol.

- [ ] **Step 4: Verify Task 2 GREEN and mutations**

~~~bash
cd services/core
go test . -run '^(TestKiwoom|TestLegacyV12KiwoomRowsRemainUnowned|TestSecurityPriceObservation)' -count=1
go test ./...
~~~

Temporarily move G4T resolve below LatestTrade; the zero-call test must fail. Restore it. Temporarily bypass the transaction-time resolve; the revoke-during-fetch test must fail. Restore production code and rerun GREEN.

- [ ] **Step 5: Commit Task 2**

~~~bash
git add services/core/security_price_observation.go services/core/security_price_observation_test.go services/core/instrument_listing_test.go
git commit -m "feat(core): 키움 가격에 상장 소유권을 강제"
~~~

---

### Task 3: Gate evidence and full verification

**Files:**
- Create: gates/g4u-kiwoom-listing-ownership.md
- Modify: PLAN.md
- Modify: GATES.md
- Modify: CONTEXT.md
- Modify: docs/omni-folio-plan.md
- Modify: the design spec only if implementation evidence changes the contract.

**Interfaces:**
- Consumes: Task 1 recovery proof and Task 2 Kiwoom authority.
- Produces: truthful G4U evidence without claiming provider verification, historical ownership, freshness or valuation authority.

- [ ] **Step 1: Update canonical evidence**

Add G4U below G4T. Record schema v13 and backup v8, owner-declared provenance, zero-network preflight, transaction-time recheck, legacy preservation, explicit non-goals, RED/GREEN commands and independent review.

- [ ] **Step 2: Run independent read-only review**

Review gates:
- registry replay and SQLite guard semantics agree;
- legacy v12 data is preserved without inferred authority;
- missing, revoked or corrupt listing blocks network or new writes;
- backup v8 cannot activate without exact listing proof;
- valuation remains local_fixture-only;
- no credential, route, UI or live path appears.

- [ ] **Step 3: Run full fresh verification**

~~~bash
make check
make smoke
cd services/core && go test -race -count=1 ./...
cd services/core && govulncheck ./...
git diff --check
~~~

Expected: every command exits 0. Verify no listener remains on port 18080, no omni-folio-smoke directory remains, and no owned Flutter or core test artifact remains after cleanup.

- [ ] **Step 4: Sync durable project memory**

Update the project wiki with the owner-declared and not-provider-verified distinction, schema v13/backup v8 proof, and remaining effective-date/provider-verification boundary.

- [ ] **Step 5: Commit Task 3**

~~~bash
git add PLAN.md GATES.md CONTEXT.md docs/omni-folio-plan.md gates/g4u-kiwoom-listing-ownership.md docs/superpowers/specs/2026-08-30-kiwoom-listing-ownership-design.md
git commit -m "docs(core): 상장 소유권 검증 근거 기록"
~~~
