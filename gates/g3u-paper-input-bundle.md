# G3.8G2D Exact-byte paper input bundle — local acceptance passed

## Contract and ownership

- Python `signal_cli --bundle` emits `paper-input-bundle.v1`: the existing proposal plus `bars_csv` and `research_csv` captured once as UTF-8 text. JSON escaping preserves original CRLF and Unicode bytes; no path, account, credential, authority or second delivery cursor is carried. Default proposal output is unchanged; `--watch --bundle` remains generation-only NDJSON.
- Go `paper-execute -bundle FILE -arm-paper` accepts one regular-file envelope instead of the three separate input flags. Each decoded CSV is capped at 1 MiB; the complete envelope at 4 MiB. Python includes its terminating stdout newline in this cap. Schema `maxLength` counts characters, so runtime byte guards are additionally required; syntax/field tests are not a full JSON Schema validator run.
- The existing descriptor-first nonblocking file reader is reused with an explicit cap. Go rejects duplicate/unknown/missing keys, null/type mismatches, trailing JSON, invalid UTF-8 and unpaired UTF-16 escapes before opening the DB. Nested proposal bytes go to the existing closed decoder, preserving duplicate-key rejection.
- The adapter passes captured bytes into the existing `executeLocalPaper`; registered research, hash binding, current selection, stored-series SMA, receipt deadline, fill→policy→admission, execution authority and lease cleanup remain unchanged. A well-formed envelope is not authorization or evidence that its claimed research passed.
- Root owns Go/integration/docs; `paper_bundle_producer` owns the Python producer/schema slice and then read-only bridge review. No new dependency, database schema, background service, broker connection, credential, container or external publication is added.

## Evidence

- RED: focused Go bundle tests failed on missing decoder/cap; Python bundle tests failed on missing byte-based helpers.
- Initial GREEN: Python CLI→Go admission preserves CRLF bytes even after all original source paths are replaced; a second manual invocation returns without another admission and leaves execution authority halted. Focused wrapper passed Go bundle tests and all 33 Python tests.
- Boundary regressions cover duplicate outer/nested fields, absent/null/wrong-type fields, invalid Unicode, valid surrogate pairs, envelope/CSV caps, malformed input before DB creation, and original-hash mismatches without new admission or arm. Existing executable FIFO matrix adds bundle and symlink cases.
- `make check` passed 2026-09-05: Go core 136.122s and internal packages, Flutter 74, Python 33 (2.906s), JSON 17, formatting/vet/analyze and owned cleanup. The full Go suite includes real bundle/FIFO rejection and existing SIGINT/SIGTERM/SIGKILL recovery tests. The exact Python 4 MiB boundary including stdout newline also passed directly after review.
- Focused `GOFLAGS='-race -run=TestPaperBundle -count=1' make test FLUTTER=true` passed Go bundle race checks (3.340s), all 33 Python tests and wrapper cleanup. Flutter is deliberately skipped in that focused command; its evidence is the full check above.
- Read-only bridge review found no blocker; its exact-size coverage suggestion was added. An optional external review tool had no endpoint configured and was not counted as review evidence. `govulncheck ./...` reported no vulnerabilities; no third-party dependency was added. `pip-audit` is unavailable and the Python package remains stdlib-only; no Python vulnerability-scan result is claimed.

## Limits and source check

This is exact-byte **manual one-shot handoff**, not a durable stream consumer, exactly-once transport, continuous order runner, market-data certification, broker paper trading or live trading. Source writers still need atomic replacement rather than in-place mutation. `--watch` may skip intermediate snapshots and repeat on restart; a flushed line is not a DB acknowledgement. Do not schedule repeated `-arm-paper` calls. G3.8G2 remains open for bounded continuous consumption, replay semantics and long-lived process-owned lease renewal without automatic rearm.

The [Go encoding/json documentation](https://pkg.go.dev/encoding/json#Unmarshal), checked 2026-09-05, states that v1 replaces invalid UTF-8 and UTF-16 surrogate pairs. The bridge adds narrow rejection guards instead of silently changing a byte-bound input or adding another JSON dependency. Valid escaped pairs and literal Unicode must decode identically; an invalid pair must fail, not become U+FFFD.
