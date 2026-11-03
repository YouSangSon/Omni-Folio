# G3.8F2 — DB-leased/fenced always-on local paper runner

Status: local gate passed on 2026-08-31.

## Closed scope

- `paper-run-loop` acquires one schema v21 `paper_runner_leases` claim bound to the exact account, current global strategy-selection event, selected result, owner, and monotonic fencing token.
- The loop runs due work immediately, heartbeats every 10 seconds, polls again after six completed heartbeats, and uses a 30-second TTL. Only an expired owner can be replaced.
- C3 account performance, D strategy performance, and E policy action revalidate the exact claim inside each write transaction and finish with an in-transaction renewal. Lease loss or selection/account mismatch rolls the transaction back.
- Manual select/rollback rejects a live lease. E's exact automatic halt/one-pop rollback is the only selection mutation permitted by a claim.
- An expired but unreleased claim still blocks manual selection while its C3/D/E prefix is incomplete. After an exact release, a manual selection change is allowed and the old cached close becomes a typed idle result until a newer close is due.
- Normal HALT, fatal failure, cancellation, SIGINT, SIGTERM, stale takeover, and SQLite writer contention release only the exact owned claim; the retained fence never decreases.
- Backup v15/schema v21 proves the singleton row, record hash, transition triggers, selection-binding index, and active-lease receipt. V14/schema v20 is migrated only in an owned candidate; a captured active lease cannot be activated.

The selection registry is global today, so this gate deliberately uses one global lease. Per-account concurrency is deferred until strategy selection itself becomes account-scoped.

## Fresh verification

```text
go test -count=1 . -run '^TestG38F'                         PASS (28.382s)
go test -count=1 ./...                                      PASS (core 81.699s; all internal packages pass)
go test -race -count=1 ./...                                PASS (core 266.436s; all internal packages pass)
make check                                                   PASS (Go, 65 Flutter, 17 Python, cleanup, 15 JSON contracts)
make smoke                                                   PASS
make test-resource-cleanup && make clean-test-resources      PASS
govulncheck ./...                                            PASS (no vulnerabilities found)
git diff --check                                             PASS
owned-resource inventory                                    PASS (no temp root, generated cache/build/coverage, port 18080 listener, labeled Podman resource, or Omni-Folio Kind cluster)
```

The focused suite includes real child-process SIGINT/SIGTERM tests, stale-owner takeover, exact fence expiry at C3/D/E entry and pre-commit boundaries, writer-lock cancellation, backup corruption, legacy migration, active-candidate rejection, expired partial-prefix rejection, and released-prefix manual selection.

## Explicitly open

- Broker calls, credentials, public API/Flutter controls, alerting, deployment/CronJob packaging, multi-node production operations, and official exchange calendar/freshness proof.
- Broker-coupled order-submit fencing, shadow/live promotion authority, live-money readiness, and any profitability claim.
