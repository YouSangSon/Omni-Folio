# G3.8G2L — observed paper input capture

## Contract

The optional Python `signal_cli --bundle --capture-dir DIR` archives the exact serialized UTF-8 bundle, including its terminal LF, before stdout. The existing private, current-user-owned directory is pinned by descriptor. A random exclusive `0600` temporary file is flushed and synced, published with a no-overwrite hardlink, removed, and followed by directory sync. Same-name retries accept only identical bounded regular-file bytes. Conflict, symlink/FIFO, directory and storage failures produce a redacted error without emitting the bundle; unrelated files remain intact.

The filename combines the latest-close timestamp and proposal hash. Existing inputs, proposal/bundle contracts, default stdout behavior, watch ordering and Go execution authority are unchanged. No dependencies, schema, backup format, API or UI changes are introduced. [Usage](../docs/local-paper-workflow.md#출력-전에-입력-보관하기)

## Verification

- RED: the new capture-before-output test failed against the old CLI with argparse `SystemExit: 2` for the unsupported option; the existing CRLF captured-read test passed. Root preflight and cleanup completed, exit 2.
- Focused GREEN: root `make test FLUTTER=true` with `GOFLAGS='-run=TestPaperBundle|TestPaperProposalFailureAtomicityAndOwnedDeadline|TestPaperSnapshotImportIsAtomicIdempotentAndAppendOnly -count=1 -v'` and a Python unittest `-k capture` wrapper passed. Core 6.710s; seven Python tests/1.115s; preflight and cleanup passed with no owned test roots left.
- Python coverage includes capture-before-stdout, CRLF snapshots, `0600`, same-input retry/watch deduplication, conflicting or unsafe targets, invalid directory/option combinations, file/directory sync failure, two real concurrent producer processes and a closed stdout pipe. Complete captured bytes remain available after output failure.
- The Go integration test runs the actual Python CLI, reads its capture file, replaces the original source files, then executes and retries through the Go CLI handler. It checks exact source bytes, the expected OPEN order, unchanged admission counts and lease cleanup. Separate snapshot/deadline tests preserve first-receipt and expiry behavior. The Go handler is in-process in this test; it is not a new native-Go-process termination proof.
- Final `make check CORE_TEST_PACKAGES='-timeout=30m ./...' FLUTTER=true` passed with Go 1.24.5, Flutter/Dart 3.47.1 and Python 3.14.5 on 2026-10-23: core 483.812s; five internal packages cached; Python 39 tests/7.481s; format/vet, Python compilation, resource preflight/cleanup and 17 JSON syntax checks. Flutter test/analyze were explicitly skipped: `apps/client` is unchanged from base `aa32c9e`, so PR #26's 123-test/analyze passing evidence is reused. No new browser, smoke, opt-in soak or Go race run is claimed; Go production and UI paths are unchanged.
- Independent read-only design and final production review found no blocking defect. The reviewer did not execute tests.

Final check/test roots `omni-folio-test.LVWZEu` and `omni-folio-test.HDtTu9` were removed; no owned test/smoke roots remained after cleanup. Logs: local ignored `docs/_internal/skill-outputs/goal-20261023-paper-capture-{red,focused,check}.log`. GitHub Actions had zero workflows at merge preparation; these are local checks, not remote CI evidence.

## Boundaries and next work

Capture preserves only observed bundles. Latest-snapshot polling may coalesce inputs; it does not establish complete source delivery. A later cumulative CSV may still contain an earlier close, so an unobserved proposal is not necessarily a missing bar. Restart can emit the same proposal again, and archive filename ordering is not replay-order proof.

This archive is not a queue, execution receipt, completion ACK or automatic rearm. Explicit `paper-execute -bundle ... -arm-paper` still revalidates input and authority. A successful handler return alone cannot safely authorize a filesystem ACK: `signal=none` does not write a signal journal, and policy HALT can return before proposal admission. A future completion protocol needs DB-rooted completion evidence and restore binding; a separate ACK can outlive a restored database.

The existing proposal deadline begins at the first **Go import receipt**, not capture time. An old, never-imported file may receive a new Go receipt; this adds no freshness/capture-age check. Reimport preserves an existing receipt and deadline. The archive is user-managed input material, separate from DB backup.

A completed final file may remain after directory-sync or output failure. SIGKILL/SIGTERM may leave an invocation's temporary file. No automatic retention or global temporary cleanup is added. Filesystem sync calls and fault injection are not physical power-loss proof. Broader G3.8G2, complete source delivery, long operational evidence and broker/live readiness remain open.
