# G1 Ledger Gate

## Pass when

- CSV parse/normalize/preview는 authoritative ledger를 변경하지 않고 행별 신규·중복·오류를 반환한다. 재시작 안전성을 위한 만료 가능한 preview record는 허용한다.
- preview token은 file hash, schema, mapping, ledger revision을 묶는다.
- apply는 한 transaction이고 같은 idempotency key와 payload에는 같은 receipt를 반환한다.
- key 재사용과 다른 payload는 conflict이며 부분 mutation이 없다.
- FIFO snapshot에서 보유 수량, 현금, 실현손익, provenance가 golden fixture와 일치한다.
- exact-decimal cash-flow는 입금·출금·현금배당·수수료·세금의 부호를 fail-closed하고, stock split은 열린 FIFO lot 수량만 조정하며 총원가와 cash를 보존한다.
- `CASH_VOID`는 이미 적용된 같은 계좌·통화의 입금·출금·현금배당·수수료·세금 하나만 exact 반전한다. 원본 event는 보존하고 거래·분할·같은-preview target·중복 void·chain을 차단한다.
- `FX_EXCHANGE`는 매도 통화의 음수 cash leg와 다른 매수 통화의 양수 cash leg를 하나의 event로 원자 적용한다. 두 금액에서 환율이나 현재 시세를 주장하지 않는다.
- backup candidate를 별도 DB에 복원해 integrity와 golden snapshot을 검증하기 전 active DB를 바꾸지 않는다. 현재 schema v9/backup v5는 cash-void와 FX guard, 주문 schema/hash/replay, known-good broker snapshot, execution-authority와 risk-reservation digest/count도 함께 검증한다. schema v8 candidate는 원본을 수정하지 않는 임시 copy를 v9로 migration한 뒤 검증한다.

## Evidence

- 2026-08-24 `go test ./...` and `go test -race ./...` pass.
- `make smoke` passes health, status, CSV preview, atomic apply, receipt, and resulting snapshot against a temporary DB.
- `TestGoldenVerticalSliceAndBackupRestore` verifies FIFO values, a transaction-consistent backup candidate, manifest, integrity check, golden snapshot, invalid manifest rejection, and restored DB without replacing the active DB. Current backup v5 evidence is in [`g4e-kiwoom-synthetic-order-state.md`](g4e-kiwoom-synthetic-order-state.md), [`g4h-kiwoom-known-good-snapshot.md`](g4h-kiwoom-known-good-snapshot.md), and [`g4i-k2c-execution-authority.md`](g4i-k2c-execution-authority.md).
- Idempotency replay/conflict, stale preview, rollback, oversell, canonical decimal, request-size, CORS, readiness, and non-leaking error paths have runnable tests.
- `TestCashFlowsAndSplitReplayExactly`, `TestCashVoid*`, `TestImportSourceEventConflictIsNotSilentlyDuplicate`, `TestSchemaV9EnforcesCashVoidGuard`, and `TestOpenAPIExposesClosedCashVoidContract` cover exact replay, API/DB trust-boundary rejection, append-only provenance and weakened-restore rejection. 세부 G1.6 증거는 [`g1b-cash-void-correction.md`](g1b-cash-void-correction.md)에 있다.
- `TestFXExchange*`, `TestOpenAPIExposesClosedFXExchangeContract`, `TestVerifyManifestMigratesV8BackupCopy`, and `TestSchemaMigratesV1ToV9AndReadinessRequiresV9` cover exact two-leg replay, API/DB rejection, schema v9 preservation, legacy-v8 copy migration and weakened-restore rejection. 세부 증거는 [`g1d-fx-exchange.md`](g1d-fx-exchange.md)에 있다.

## Still open

- FX rate series and base-currency valuation, FX/trade/split/corporate-action correction, dividend reinvestment, jurisdiction-specific tax classification, and credentialed broker execution/cash reconciliation.
