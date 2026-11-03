# G3.8G2C Continuous proposal generation — local acceptance passed

## Contract and ownership

- Root owns Python CLI/tests and integration docs on `feat/g38g-local-paper-ingress`; `continuous_ingress_review` owns read-only design/diff review. Integration requires focused RED/GREEN, actual subprocess termination, full check and owned-resource cleanup. No external push or deployment is included.
- `python3 -m omni_research.signal_cli --watch` reuses the current generator and emits the existing `paper-signal-proposal.v1` as flushed NDJSON. It checks the local input again one second after each completed iteration, with no overlapping calculations, new dependency, thread, child process, persistent output file or operational DB access.
- The initial artifact object stays fixed; later artifact bytes must match it. Research bytes must continue matching its input SHA. An unchanged proposal emits nothing; changed bytes with the same or older last-bar anchor stop with an error. A strictly newer valid last close emits another proposal. `none/null` and liquidation target `0` stay distinct.
- Each input uses one bounded read of its opened regular-file descriptor. O_NONBLOCK prevents FIFO writer waits; the 1 MiB cap does not promise a general filesystem deadline. The same bars bytes determine parsing and input SHA. CSV parser errors are redacted.
- Source owners must publish complete CSV snapshots by atomic replacement, not in-place writes. A descriptor/read/hash does not establish producer atomicity. History overlap, 13-column timing metadata, receipt deadlines, current selection and actual order admission remain Go validations.
- SIGINT exits 130; SIGTERM/SIGKILL retain native process termination. There is no persistent producer state to recover. A broken stdout exits 1 with a fixed message, redirecting only that descriptor to devnull before Python's final flush. Tests reap every owned child and close pipe descriptors.

## Evidence

- RED: missing `--watch`; rewritten same/older anchor and changed artifact continued instead of stopping; all six FIFO/caller-link cases timed out; broken stdout ended with Python shutdown code 120 instead of a clean error.
- GREEN: the CLI tests prove initial and changed-input emission, unchanged-input silence, no new record on invalid/regressed inputs, preserved inputs, all three real OS signals, invalid update, FIFO/symlink/directory/oversize/large CSV-field rejection and closed-output termination. Existing one-shot schema/hash/golden and research authority-boundary tests remain in the suite.
- Focused Python verification under `make test GO=true FLUTTER=true` passed 30 tests (11.481s) and wrapper cleanup. That command deliberately skips Go/Flutter and is not full-check evidence.
- Final `make check` passed on 2026-09-05: Go core 129.236s and internal packages, Flutter 74, Python 30 (3.002s), JSON 16, formatting/vet/analyze and owned cleanup. The Go suite includes the existing Python-producer-to-Go-admission integration. No broker, credential, Podman/Kind resource or persistent listener was created.
- Independent read-only review found no remaining blocker. Its initial mixed-symbol concern was retracted after checking the shared `parse_bars` single-symbol invariant and the generator's cross-file symbol match; a mixed-history public-seam regression preserves that inherited rule without adding a duplicate production guard.
- `services/research/pyproject.toml` declares no third-party dependencies and this change uses only stdlib. `pip-audit` is not installed in the current environment; no Python package vulnerability-scan result is claimed.

## Remaining integration

This is automatic **proposal generation only**, not automated trading, ingestion, atomic publication, durable delivery or exactly-once processing. Polling observes only the latest available close; it can miss intermediate snapshots. Restart and parallel producers can repeat records. A missing trailing newline or a partial record is not usable input.

Do not pipe this stream into the current regular-file-only `paper-execute`, redirect it over its single-proposal input, or schedule repeated `-arm-paper` invocations. Continuous Go consumption still needs the exact CSV-byte handoff, durable replay/cursor semantics, strategy/calendar/freshness rules and a long-lived process-owned lease that stops on policy halt or ownership loss without automatic rearm. G3.8G2 remains open.

## Focused source check

The [Python signal documentation](https://docs.python.org/3/library/signal.html) distinguishes SIGINT's KeyboardInterrupt, uncatchable SIGKILL, and broken-pipe errors including the interpreter's final stdout flush. The [POSIX open contract](https://pubs.opengroup.org/onlinepubs/9799919799/functions/open.html) explains nonblocking FIFO open; the existing Go input reader applies the same descriptor-first rule. One Exa query and the Python primary-source read informed this lifecycle boundary; this is not an engine comparison or performance benchmark.
