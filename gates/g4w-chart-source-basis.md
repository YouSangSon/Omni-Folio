# G4W — Chart source identity and basis

Date: 2026-10-23. Baseline `c027e1e`. Scope: disclose the candle source's identity/currency and unknown time basis before joining any ledger overlay.

## Contract

- Preserve the selected holding's existing server `instrument_id` through Flutter navigation. Before replacing the chart, compare symbol plus any source-declared instrument/currency pair. A different instrument or currency fails the load; refresh failure retains the whole prior series, provenance and original timestamps.
- The existing candle query remains `symbol/interval`, reading one configured fixture independently of the ledger. It never copies request/holding/current-listing identity into a response.
- Preserve the original ten-column CSV. The second exact header appends `instrument_id,currency`; both are required, validated and identical across every row. The new `market-bars-identified.csv` is an explicitly identified **sample**, not broker evidence. Legacy input returns `null/null`; malformed or partial metadata fails closed.
- The response includes `bar_time_basis=unspecified`. `at` is an ordered timestamp label, not a proven session date, candle open/close or trade membership. Local fixture prices remain `price_adjustment=unspecified`, `sample=true`, `state=stale`.
- Flutter shows price currency, declared identity match or unknown, price adjustment and time uncertainty with the existing source/as-of/fetched-at details. Missing metadata from an older server remains explicitly unknown. No average-cost line or trade marker is enabled.

## Verification

- RED: existing server omitted identity and rejected the extended header; the two new widget cases failed on missing currency/unknown disclosure.
- First focused GREEN attempt: Go passed; Flutter 108 passed / 3 failed because chart assertions assumed the longer metadata still left the chart eagerly built before scrolling. Tests now reach it through the actual scroll control; no assertion was removed. Initial wrong-identity failure/retry is also exercised.
- Updated focused run: Go market-data tests and 111 Flutter tests pass, including malformed/partial/mixed input, both identity and currency mismatch, retention, initial retry, legacy unknown, chart/table range synchronization, 320×480 at 200% text and accessibility semantics. Wrapper exit 0; Python 34 and resource cleanup also passed.
- Final `make check CORE_TEST_PACKAGES='-timeout=30m ./...'` exits 0: core 474.280s, five internal packages cached, Flutter 111, Python 34/5.689s, formatting/vet/analyze/Python compilation/JSON syntax and resource checks. `make smoke` exits 0 for health/status/preview/apply/snapshot/activity/market data. These are local results, not remote CI.
- Release web build (141.7s), real API demo import (3 rows), and native Chrome UI verified Holdings → AAPL, declared USD/identity, 30-day range, exact six-row OHLCV table, scrolling and 375×812 responsive layout. Stopping only the owned API and refreshing retained USD, original fetched time `2026-10-23T07:09:13.646379Z`, source-as-of and selected range. Screenshot `output/playwright/chart-basis-375.png` was inspected; the failure state is session observation evidence, not a separately saved image. Browser's optional reconciliation GET returned the expected 404; intentional offline GET then returned connection refused. No universal zero-console-error claim.
- Browser CLI was unavailable from local npm cache; native browser control completed UI checks. Initial browser wrapper hit a concurrent harness-edit parse error and is excluded despite a misleading wrapper exit. The stabilized immutable retry reached explicit readiness, completed UI verification and exited 0 after shutdown. No application change was needed. Owned browser API/web PIDs, ports and temp root were checked after cleanup; pre-existing browser tabs and unrelated project processes were preserved.
- Independent read-only Astra review found no blocking defect in the API/UI trust boundary. Parent inspected the production diff and failure logs.
- Original `market-bars.csv` bytes match baseline; SHA-256 `42e5c1e15b2eb2f066e7d8c582a6871365997d5ee57df1b2720540a6600d93a6`. Research inputs/artifacts, ledger/schema/backup, default fixture and Kiwoom adapter are unchanged.

## Remaining boundary

Average cost and markers remain open in PLAN. The next combined view must use exact instrument+currency and one ledger revision; current remaining FIFO cost / quantity needs declared display rounding. Local BUY/SELL records are not broker-confirmed executions. Trade placement needs explicit source session/interval membership, comparable price units and split-aware treatment. A timezone, current listing or nearest candle is insufficient.

Physical-device frame profiles, manual screen-reader operation, actual broker data, remote CI and deployment are separate evidence; this gate does not claim them. Local ignored logs: `docs/_internal/skill-outputs/goal-20261023-chart-basis-*.log`.
