# G3.8G1 Offline SMA Paper Signal Proposals

Scope: share the existing reference SMA crossover decision with a local-file-only CLI. No new strategy engine, dependency, database migration, network client, registry mutation or execution authorization. G3.8G as a whole remains open.

## Contract

- `python3 -m omni_research.signal_cli --bars <latest.csv> --research-bars <original.csv> --artifact <result.json>` writes one canonical JSON line to stdout only. Run with `PYTHONPATH=services/research` from the repository root.
- Accept only reference engine `0.1.0`, `long_only_sma_crossover/1.0.0`, expanding-walk-forward policy v1, valid execution contract and a self-hashed input claiming `paper_candidate` with all three promotion gates true. This is an untrusted local calculation input: full candidate/evaluation evidence is not validated or reproduced here, and a caller can rewrite and rehash it. Go must independently validate the registered artifact and selection before admission; a proposal must never serve as replacement research evidence.
- Read each CSV once for hashing and parsing. Original research bytes must match both artifact input hashes; proposal symbol must match that research series and be KRX six digits. Timestamps are UTC whole seconds, strictly increasing and unique. Duplicate CSV column names fail closed in the shared parser.
- Latest history needs slow-window + 1 bars; its last timestamp must follow the original research sample. Warm-up may overlap. This does not prove exchange sessions, completed daily bars, data freshness, or point-in-time provider availability; offline CSV assertions are not market truth.
- Output binds result SHA, parameter SHA, latest CSV SHA, symbol and data-as-of. `proposal_sha256` hashes canonical JSON excluding itself using the existing reference canonicalization.
- `golden_cross` proposes the positive whole-share configured target; `death_cross` proposes `"0"`; `none` emits `null`, which must never be treated as liquidation. No account, selection event, side, order quantity, limit price or execution lease is supplied.
- SMA retains the reference Decimal operation order/context and version. This is an existing bar-level model, not tick/HFT execution or arbitrary-precision return evidence.
- Artifact duplicate JSON keys, float/non-finite JSON numbers, oversized artifacts and invalid input fail with fixed stderr and no proposal. Neither input files nor operational state are written.

## Verification

- RED: missing `signal_cli` import; separate shared crossover helper import before extraction.
- RED: duplicate `symbol` CSV columns were silently resolved to the last field. Shared `parse_bars` now rejects duplicate names for every research consumer.
- GREEN: research 25 tests cover both crosses/no signal (including equality before the cross), future-suffix invariance, deterministic input/output hashes, rehashed calculation-contract drift, claimed promotion gates, symbol/history/time mismatch, redacted CLI failures and unchanged input files. Existing golden backtest and walk-forward outputs remain passing. Schema field/target-branch regression checks are not a full JSON Schema validator execution.
- [x] 2026-09-05 `make check`: format, Go vet, Flutter analyze, full Go suites (core 100.360s), all Flutter tests, Python 25, 16 JSON syntax checks and owned-resource cleanup self-tests pass. Final `make clean-test-resources` passes. No persistent server, Podman or Kind resource was created.
- Review: independent read-only review confirmed preserved SMA arithmetic/order and identified missing equality cases plus the risk of describing self-hashed input as trusted research. Equality cases are now tested; CLI comments, README, goal and this gate explicitly retain the untrusted-input boundary. No third-party Python dependencies were added; the research boundary import test remains green.

## Remaining G3.8G2

`paper-run-loop → runPaperPerformanceLoop → runDuePaperPerformancePolicyWithClaim` currently runs C3/D/E evaluation and safety policy, not trading. `openPaperAccountingSession`, `recordPaperMarketBar`, `admitPaperSignal`, and `runPaperOrder` lack non-test operational callers as of 2026-09-05.

Connect a bounded local-fixture execution path through existing Go validation/state machines: independently validate proposal, bind the current selection and session, ingest a completed bar, settle prior eligible orders, then admit the new signal with the persisted cutoff. Preserve restart/retry and no-lookahead invariants. A caller must not promote a rehashed proposal directly into an admitted signal.

The singleton performance-runner lease is not the per-account execution lease. `Service.executionOwner` changes per process, so pretending another process already armed a usable CLI lease is insufficient. Define explicit paper-only invocation ownership and conditional cleanup; do not expose or automatically toggle generic broker execution authority. No credential, live order, external deployment or profitability evidence is supplied by this gate.

Research rationale: [official-source addendum](../docs/omni-folio-research-report.md#2026-09-05-paper-신호실행-경계-추가-조사).
