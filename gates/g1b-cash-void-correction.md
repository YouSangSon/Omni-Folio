# G1.6 Append-Only Cash-Flow Correction Gate

## Passed scope

- CSV v1 keeps its existing required columns and accepts optional `corrects_source_event_id`; mapping v3 invalidates older previews.
- `CASH_VOID` appends a new event that exactly reverses one already committed, same-account and same-currency `DEPOSIT`, `WITHDRAWAL`, `DIVIDEND`, `FEE`, or `TAX` event. The original event and both import receipts remain in provenance.
- Missing, future, trade/split, different-currency, different-amount, already-voided, chained, same-preview-new and same-preview-double targets fail before apply with a sanitized error. Reusing one `(account_id, source_event_id)` with different normalized data is an error, not a silent duplicate.
- Apply reuses the existing preview fingerprint, ledger revision, idempotency and single SQLite transaction. Schema v8 independently enforces event shape, composite self-FK, one-void-per-target uniqueness, exact inverse/time/type/currency guard and insert-only triggers.
- Backup format remains v5 because its JSON shape did not change; `schema_version` advances to `omni-folio.sqlite.v8`, and restore rejects a missing or altered cash-void guard.
- Flutter Import review uses a closed correction target DTO, discloses the preserved original and reversing amount, avoids internal event/account IDs, and remains usable at 200% text with a dedicated semantics label.

## Evidence

- `TestCashVoidPreservesOriginalAndReplaysExactly`
- `TestCashVoidPreviewRejectsUnsafeTargets`
- `TestImportSourceEventConflictIsNotSilentlyDuplicate`
- `TestSchemaV8EnforcesCashVoidGuard`
- `TestOpenAPIExposesClosedCashVoidContract`
- `TestSchemaMigratesV1ToV8AndReadinessRequiresV8`
- Flutter parser test `cash void preview requires a closed correction target`
- Flutter widget test `cash void preview preserves the original disclosure at 200 percent text`
- 2026-08-26 KST: focused cash-void/OpenAPI tests, `make check`, `make smoke`, and `cd services/core && go test -race -count=1 ./...` pass locally. The root gate includes all 25 Flutter and 13 research tests and cleans generated test artifacts.

## Not proven

- FX conversion or base-currency restatement.
- BUY/SELL/SPLIT, lot, corporate-action, dividend-reinvestment or jurisdiction-specific tax correction.
- Broker-originated cash/fill correction, current broker truth, credentialed reconciliation or automatic correction.
- Physical-device or manual VoiceOver/TalkBack evidence for this row.
- Any broker submit, live authorization or real-money behavior.
