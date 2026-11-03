# G3.8E Paper Performance Safety Policy Design

Date: 2026-08-31
Status: approved by the repository goal's local-autonomy rule

## Problem

G3.8D now stores exact strategy-window return and drawdown evidence, while G3.7
can atomically fence paper execution and roll the selected strategy back. No
durable policy currently decides when one recovered performance window should
invoke that action. Reusing research thresholds or accepting caller-supplied
metrics would make the result mutable and unauditable; reusing manual reason
codes would hide automatic provenance.

## Decision and rejected alternatives

Add one fixed, paper-only v1 policy between the existing evidence and action
paths. The chosen design is the smallest path that preserves independent replay:

1. **Chosen:** one dependency-free `internal/riskdomain` function, one internal
   application use case, one append-only policy journal, and reason/link
   extensions to the two existing action journals.
2. **Rejected:** encode thresholds in the application service only. This writes
   less schema but cannot prove later which policy or evidence caused an action.
3. **Rejected:** add a configurable policy service, scheduler, or rules engine.
   G3.8E has one policy and no scheduling authority, so those abstractions add
   bypass paths without a current requirement.

The policy version is exactly `paper-strategy-performance-safety.v1`. It is a
local safety default, not an empirically calibrated optimum, investment advice,
or live-trading limit. External guidance supports preset, documented,
reviewable controls, independent testing, and a prompt disable mechanism, but
does not supply these numeric values: [SEC market-access FAQ](https://www.sec.gov/rules-regulations/staff-guidance/trading-markets-frequently-asked-questions/divisionsmarketregfaq-0),
[FINRA Regulatory Notice 15-09](https://www.finra.org/rules-guidance/notices/15-09),
and [CFTC/MFA automated-trading safeguards](https://www.cftc.gov/sites/default/files/idc/groups/public/@newsroom/documents/file/tac021014_mfa.pdf).

## Pure policy contract

```go
type PaperPerformanceInput struct {
    SampleCount      int
    CumulativeReturn string
    MaxDrawdown      string
}

type PaperPerformanceDecision struct {
    Decision   string
    ReasonCode string
}

func EvaluatePaperPerformancePolicy(PaperPerformanceInput) (PaperPerformanceDecision, error)
```

The function accepts only canonical decimal strings for recovered cumulative
return and maximum drawdown plus the recovered same-selection sample count. It
returns a closed decision/reason pair. Negative sample counts, non-canonical
decimals, and negative maximum drawdown fail before any decision; even an
insufficient sample must have structurally valid recovered metrics.

Rules run in this exact order:

1. `sample_count < 2` ->
   `INSUFFICIENT/minimum_same_selection_samples_not_met`.
2. `max_drawdown >= 0.1` ->
   `HALT_AND_ROLLBACK/max_drawdown_limit_reached`.
3. `cumulative_return <= -0.05` ->
   `HALT_AND_ROLLBACK/cumulative_return_floor_reached`.
4. Otherwise -> `HOLD/within_local_paper_safety_bounds`.

Both boundaries are inclusive and drawdown wins a tie. Exact rational
comparison reuses `internal/exact`; binary floating point and SQLite decimal
casts are forbidden. A threshold change creates a new policy version. G3.8A
operational status, period return, research promotion limits, and caller
configuration are not inputs.

## Application and atomicity contract

```go
func (s *Service) applyPaperPerformancePolicy(
    ctx context.Context,
    accountRef string,
    expectedSelectionEventID string,
    expectedStrategyPerformanceID string,
) (*PaperPerformancePolicyEvent, error)
```

The caller supplies identifiers only. In one existing immediate SQLite writer
transaction the use case:

1. proves full G3.8D, strategy-registry, execution-authority, and G3.8E
   recovery before an idempotent lookup;
2. returns an exact previously validated retry even if its own rollback later
   superseded the referenced selection;
3. otherwise verifies the exact current non-`no_strategy` selection and latest
   per-account G3.8D event, including session and selected-result bindings;
4. evaluates the pure fixed policy without pre-rejecting a valid one-sample
   G3.8D row; that row durably records `INSUFFICIENT`;
5. records `INSUFFICIENT` or `HOLD` without changing authority or selection; or
6. for `HALT_AND_ROLLBACK`, prederives the policy, halt, and rollback IDs,
   inserts the policy row first with a deferred rollback foreign key, appends one
   `automatic_performance_halt` for every authority armed at the captured
   cutoff and one `automatic_performance_rollback` for the exact selection;
7. independently replays the proposed cross-journal state before commit.

The existing G3.7 all-account halt and one-pop selection-stack implementation
is extracted into reason/link-aware transaction helpers; it is not duplicated.
Manual callers retain `manual_halt` and `manual_rollback`. Every automatic halt
increments its account's fencing token once and clears the lease. An action
with no armed accounts still records the exact selection rollback. No path
automatically arms, selects, or promotes a strategy.

Any stale identifier, corrupt prerequisite, malformed decimal, guard failure,
or failed action insert rolls back the policy row, all halts, and the selection
change. SQLite's immediate writer transaction serializes competing calls.
Identical calls converge on one event/action; different stale evidence fails
instead of being converted into a decision.

## Durable evidence and recovery

Migration 020 adds `paper_performance_policy_events` with canonical JSON/hash,
policy and schema versions, account/session/selection/result/G3.8D bindings,
per-selection predecessor and retry identity, selection/performance/authority
sequence cutoffs, decision/reason, rollback reference, halt count, and
append-only guards. Existing execution-authority and strategy-selection rows
gain a nullable policy-event link; only the two automatic reason codes require
that link, while all manual rows require it to be null.

Recovery independently recomputes the pure policy and reconstructs state at
each stored cutoff. It proves exact forward and reverse action coverage:

- no automatic action may be orphaned or linked twice;
- `INSUFFICIENT` and `HOLD` have no action rows;
- every account armed at the cutoff has exactly one next, fence-incrementing
  automatic halt, and no extra halt exists;
- linked halt rows occupy the exact global authority sequences
  `authority_cutoff+1..authority_cutoff+halt_count` in lexical account order,
  with no interleaved or unclaimed authority event;
- the rollback is cutoff + 1, references the exact source selection, and has
  the normal one-pop stack result;
- every stored row, predecessor, retry key, canonical JSON, hash, count, and
  digest is reproducible.

Recovery always runs before returning an old retry, so later corruption cannot
be hidden by cached success.

The dependency direction is non-recursive:

- base strategy and authority replays validate automatic-row shape, canonical
  content, transition rules, and a narrow linked-policy metadata proof, but
  never call G3.8E recovery;
- that narrow proof reads only the named policy row and validates its canonical
  hash/JSON, action decision/reason shape, stored rollback ID or halt count, and
  matching link identity; it does not traverse G3.8D, selection, or authority;
- `provePaperPerformancePolicyRecovery` is the root semantic proof. It calls
  the prerequisite base replays once, then an inner policy replay recomputes
  thresholds and cross-journal forward/reverse coverage without calling the
  prerequisites again.

For schema v20, the root semantic proof is mandatory before every public writer
that can extend either linked journal: strategy SELECT and ROLLBACK, authority
arm/halt/lease acquisition and batch halt, plus every policy retry/write. It is
also mandatory before source backup, restore candidate activation, and startup
acceptance. Shared tx-scoped halt/rollback/insert helpers do not call the root;
their public wrappers call it once, while `applyPaperPerformancePolicy` calls it
once before invoking those helpers in its transaction. This keeps the call
graph acyclic and prevents a corrupt automatic halt from being followed by a
manual re-arm or lease.

Synthetic order authorization continues to fail closed on the base authority
replay: an automatic reason can only represent a halt and its narrow policy
link must be valid. Tests instrument the proof call graph and corrupt a linked
policy row before each strategy/authority/policy writer, startup, backup, and
restore trust boundary to prove no recursion and no bypass.

Migration 020 is the only migration, besides the existing v7 exception, run
with foreign-key enforcement disabled outside its transaction. Its table
rebuild order is fixed:

1. create `execution_authority_events_new` and
   `strategy_selection_events_new` with every old column/constraint plus the
   nullable policy link and new reason rules;
2. copy every historical row and sequence exactly, setting the new link null;
3. drop the two old tables' own triggers/indexes and then the old tables;
4. rename the `_new` tables to their canonical names;
5. create `paper_performance_policy_events` against the now-final parent names;
6. recreate every original and new index/trigger verbatim;
7. compare row counts, sequences, canonical JSON/hash digests, and the complete
   `pragma_foreign_key_list` inventory for all child tables against the expected
   v20 contract; run `foreign_key_check`, commit, reenable enforcement, verify
   it is on, and run `foreign_key_check` again.

The `_new` tables may name the not-yet-created policy table because the cycle is
resolved before the transaction commits. Runtime policy writes keep foreign
keys enabled, insert the prederived policy row first, and use only the deferred
policy-to-rollback foreign key; action-to-policy links are immediate. A
non-empty schema-v19 fixture with strategy, authority, order/reservation,
evaluation, accounting, and G3.8D children proves no FK is silently retargeted
to `_new`/`_old` or lost.

## Backup and compatibility

The database becomes schema v20 and current backups become
`omni-folio-backup.v14` / `omni-folio.sqlite.v20`. Source and verification
receipts add policy digest, event count, action count, and automatic-halt count.

A v13/schema-v19 artifact is verified with its original G3.8D contract first.
Only an owned temporary copy is migrated to v20, with an empty policy log and
empty automatic links. Source database and manifest hashes must remain
unchanged. Failed candidate directories are removed by the owned-resource
cleanup path.

## TDD and cleanup evidence

The public test seams are the pure policy function and the application use
case. RED precedes production code. Tests cover sample floor, both inclusive
limits, tie priority, malformed decimals, no-action paths, exact retry after
rollback, stale/cross-selection inputs, two-handle convergence, all-account
fencing, zero-armed rollback, forced failures, replay mutations, schema drift,
current backup restore, and v13 owned-copy migration.

Focused and full runs use the repository cleanup wrapper. Success, intentional
failure, SIGINT, and SIGTERM must leave no owned listener, process, temporary
root, build/coverage/bytecode artifact, Podman resource, or Kind cluster.
SIGKILL/crash fixtures are recovered on the next scoped preflight. Global prune
or broad path deletion is forbidden.

## Explicit non-goals

No dependency, repository interface, mutable projection, second performance
calculator, scheduler, public API/CLI/UI, alert, broker call, credential access,
general-ledger write, deployment resource, Kubernetes manifest, live-money
authority, profitability claim, or strategy promotion is added.
