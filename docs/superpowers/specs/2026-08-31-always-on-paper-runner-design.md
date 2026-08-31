# G3.8F2 Always-On Local Paper Runner Design

Date: 2026-08-31
Status: approved by the repository goal's local-autonomy rule

## Problem

G3.8F1 closes one recovered C3/D/E paper-performance chain on demand, but two
processes still rely only on journal idempotency. There is no durable owner,
heartbeat, expiry, or fence to stop a stale process between the three existing
transactions. The next gate needs one local always-on runner without granting
broker or order authority and without replacing the proven C3/D/E state
machine.

## Decision

Add one global strategy-selection SQLite lease row and one explicit in-memory
claim. The row has a retained monotonic fencing token and, only while active, a
fresh cryptographic owner, account, heartbeat/expiry nanoseconds, exact
strategy-selection event, and selected result. Strategy selection and automatic
rollback are global in the current model, so a global lease is the smallest
fence that prevents two accounts from stranding each other's C3/D/E prefixes.

This intentionally serializes paper accounts. It still guarantees at most one
runner for every account/strategy pair. Per-account concurrency is opened only
after strategy selection itself becomes account-scoped; adding a second lock
layer before that would preserve the global rollback race.

Scheduler ownership remains separate from `execution_authority_events`.
Acquiring this lease cannot arm, reserve, dispatch, submit, access credentials,
or call a broker. C3/D/E business journals keep their current deterministic
identities and contain no scheduler token.

## Durable lease contract

Migration 021 creates `paper_runner_leases` as a `STRICT` table with:

- a fixed `paper_strategy_selection` scope primary key;
- positive retained `fencing_token`;
- an all-null released tuple or an all-present active tuple containing owner,
  account, heartbeat, expiry, selection event, and selected result;
- foreign keys for the exact strategy binding;
- canonical record JSON/hash, a no-delete trigger, and transition guards that
  allow only same-token heartbeat/release or exactly `token + 1` acquisition;
- integer Unix nanoseconds, avoiding variable-width timestamp comparison.

TTL is 30 seconds. Same-owner retry returns an unchanged unexpired claim.
Heartbeat keeps the token and requires exact scope/account/owner/token/selection,
non-regressing time, and `now < expiry`. A released row or an expired active row
may be acquired with exactly the next token and the then-current non-`no_strategy`
selection. Token overflow, backward clock movement, foreign live ownership, or
semantic corruption fails closed. Release clears only the active tuple and
retains the token; deleting a row is forbidden.

The recovery proof first proves the G3.8E root, then validates the singleton,
canonical record/hash, selection binding, fixed TTL span, transition-protected
schema, digest, row count, and active count. Expiry is valid recovered state,
not corruption.

## Application and transaction contract

The existing one-shot command and the new loop share one fenced application
path. A completed C3/D/E chain remains a recovery-proven read-only retry. Any
incomplete write requires an acquired claim.

For C3, D, and E separately:

1. renew the exact claim before entering the stage;
2. create a context deadline shorter than the lease TTL;
3. inside the existing immediate SQLite transaction, validate exact lease,
   fence, unexpired time, and current bound selection before replay/work;
4. preserve the existing idempotent lookup, computation, insert, and proposed
   recovery proof;
5. take a fresh clock reading and conditionally renew the exact lease to
   `now + 30s` as the final SQL write in the same transaction before commit.

The three commit boundaries remain. Lease loss rolls back only the current
stage; a higher-fence owner resumes an existing C3 or C3+D prefix through the
existing keys. No outer transaction or second policy state machine is added.

Because the lease is global, no other account can hold a live claim or advance
another prefix while E runs. E may change the selection only when that same
transaction recorded a
`HALT_AND_ROLLBACK` policy and the exact linked automatic rollback event. Its
final guard verifies the new current event, source selection, policy link, and
reason. A generic selection mismatch or decision flag is never accepted.

Manual `strategy-select` and manual rollback reject while the global runner
lease is live. The operator stops/releases the runner first; E's exact
policy-generated rollback is the only in-lease exception. A two-account test
proves account B cannot acquire or create a prefix while account A owns the
selection fence and rolls back.

## Runtime lifecycle

Add `paper-run-loop -db ... -account ...`. It acquires once, runs due work
immediately, then uses one serial, stopped/drained standard-library timer:
heartbeat every 10 seconds while idle and a completion-based due poll every 60
seconds. No heartbeat goroutine or second DB handle exists. Each stage renews
before entry and in its final transaction write, so heartbeat cannot compete
with an immediate writer transaction. A stage uses a 20-second context deadline;
expiry/cancellation observed before the final renewal rolls it back.

`sql.Tx.Commit` is not context-aware. A final renewal gives a full TTL and is
atomic with the business write; SQLite's immediate writer lock prevents any
takeover from committing before that transaction returns. The code does not
claim bounded shutdown under a hung filesystem or that wall-clock expiry after
the final SQL statement can cancel an in-progress commit. A lock-contention plus
SIGTERM process test proves the cancellable pre-commit path terminates without a
loser release.

No available close or incomplete local marks are typed not-due states and are
polled again. Corruption, schema/hash failure, lease loss, selection mismatch,
unknown work failure, or failed renewal is fatal and starts no later cycle. A
successful automatic rollback stops the old selection loop and never selects,
arms, or starts another runner.

SIGINT and SIGTERM cancel the active context. Shutdown waits for the one stage,
stops timers, then conditionally releases with a fresh bounded background
context. A loser never releases a winner's row. SIGKILL cannot clean up; the OS
rolls back an open transaction and the next process must wait until expiry,
take `token + 1`, and resume the durable prefix.

The runner owns no temp directory, PID file, listener, container, volume, or
cluster. Test processes remain covered by the repository's session-owned
Makefile cleanup contract on success, failure, SIGINT, SIGTERM, and next-run
stale-owner recovery.

## Backup and compatibility

Schema becomes v21 and current backup becomes v15. The manifest and receipt add
the lease digest, total/active counts, check status, and candidate digest.
Source and `VACUUM INTO` candidate are proved independently because a valid
heartbeat may occur between source proof and the transaction-consistent copy.
Active and released rows are preserved; silently clearing them could regress a
fence. A manifest with `active_paper_runner_lease_count > 0` records the exact
candidate but is not eligible for activation, and `verify-restore` rejects it.
Only a quiesced backup with a released lease can be activation-eligible.

V14/schema20 becomes the immediate legacy pair. Verification proves the
untouched v14 source with its original policy contract, explicitly rejects v15
lease fields, copies to an owned temporary candidate, migrates only that copy,
and requires canonical empty lease state. Older supported formats follow the
same owned-copy chain. Every exit removes only the owned candidate.

A restored process receives a new owner and cannot use a captured active lease.
SQLite fencing coordinates only processes sharing one database file, so expiry
alone cannot prove the source runner stopped after copying. V15 therefore
preserves active state for audit but refuses activation until a later explicit
quiesce/fence workflow exists.

## TDD and verification

RED precedes production edits. Tests cover exact acquisition/heartbeat/release,
two-owner and two-account races, exact-expiry takeover, overflow and clock
regression, manual selection blocking, C3/D/E entry and pre-commit expiry,
exact E exception, partial-prefix resume, heartbeat/write-lock SIGTERM, typed
not-due polling, current and legacy backup/restore, active-candidate activation
refusal, schema/record corruption, actual SIGINT/SIGTERM, SIGKILL-style expiry
takeover, race detection, and final owned-resource inventory.

## Explicit non-goals

No broker call, credential, order submission, execution arming, public API,
Flutter/UI change, alerting, retry framework, queue, Redis, microservice,
CronJob/Kubernetes object, deployment, strategy promotion, live-money authority,
or profitability claim is added.
