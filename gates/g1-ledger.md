# G1 Ledger Gate

## Pass when

- CSV parse/normalize/preview는 authoritative ledger를 변경하지 않고 행별 신규·중복·오류를 반환한다. 재시작 안전성을 위한 만료 가능한 preview record는 허용한다.
- preview token은 file hash, schema, mapping, ledger revision을 묶는다.
- apply는 한 transaction이고 같은 idempotency key와 payload에는 같은 receipt를 반환한다.
- key 재사용과 다른 payload는 conflict이며 부분 mutation이 없다.
- FIFO snapshot에서 보유 수량, 현금, 실현손익, provenance가 golden fixture와 일치한다.
- backup candidate를 별도 DB에 복원해 integrity와 golden snapshot을 검증하기 전 active DB를 바꾸지 않는다. G4E 이후 backup v2는 주문 schema/hash/replay도 함께 검증한다.

## Evidence

- 2026-08-24 `go test ./...` and `go test -race ./...` pass.
- `make smoke` passes health, status, CSV preview, atomic apply, receipt, and resulting snapshot against a temporary DB.
- `TestGoldenVerticalSliceAndBackupRestore` verifies FIFO values, a transaction-consistent backup candidate, manifest, integrity check, golden snapshot, invalid manifest rejection, and restored DB without replacing the active DB. Order-aware v2 extension evidence is in [`g4e-kiwoom-synthetic-order-state.md`](g4e-kiwoom-synthetic-order-state.md).
- Idempotency replay/conflict, stale preview, rollback, oversell, canonical decimal, request-size, CORS, readiness, and non-leaking error paths have runnable tests.
