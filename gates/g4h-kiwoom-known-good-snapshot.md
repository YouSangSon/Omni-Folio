# G4H Kiwoom Known-Good Snapshot Gate

## Pass when

- `complete=true`인 KRX/KRW Kiwoom snapshot만 identity, canonical UTC, exact decimal, unique ascending position과 open-order 계약을 다시 검증한 뒤 저장한다.
- 한 SQLite transaction이 현재 ledger revision과 KRX/KRW 종목 수량을 읽고 raw snapshot, canonical hashes, 종목별 `broker - ledger` 수량 diff reconciliation을 분리 insert한다.
- 같은 provider/environment/exchange/account/fetched-at와 같은 payload는 raw snapshot을 중복 저장하지 않는다. 같은 raw snapshot·같은 ledger revision replay는 같은 reconciliation을 반환하고, ledger revision이 바뀌면 새 reconciliation만 append한다. payload 충돌·불완전 snapshot·잘못된 generated ID는 전체 거절한다.
- 실패한 입력과 더 오래된 snapshot은 최근 fetched-at의 known-good record를 덮어쓰지 않는다.
- ledger event, broker snapshot, broker reconciliation은 insert-only이고, backup/restore가 broker state digest/count, canonical record hash와 필수 schema/trigger를 검증한다.

## Evidence

- `TestG4HKnownGoodSnapshotPersistsAndDiffsLedger`는 exact quantity diff, ledger revision binding, idempotent replay, fetched-at conflict, incomplete/invalid-ID 거절과 last-known-good 보존을 검증한다.
- `TestG4HConcurrentReplayAcrossSQLiteHandles`는 두 SQLite handle의 동시 replay가 raw snapshot/reconciliation을 중복 생성하지 않음을 검증한다.
- `TestG4HSnapshotRowsAndLedgerEventsAreInsertOnly`는 DB mutation 차단을 검증한다.
- `TestG4HBackupRestoresBrokerSnapshotProof`는 schema/backup v3 broker digest/count, restored projection, manifest tampering과 약한 trigger rejection을 검증한다.
- `TestG4HBrokerRecoveryRejectsCorruptRows`는 runtime/recovery hash mismatch 거절을 검증한다.
- 2026-08-24 KST에 `go test -run 'TestG4H|TestK2ABackup|TestSchema|TestHealth|TestRestore' -count=1 -v`, `make check`, `make smoke`, `cd services/core && go test -race ./... -count=1`, `git diff --check`, secret-pattern scan을 통과했다.

## Not proven

- credential, 실제 Kiwoom request, 자동 scheduling, provider freshness/timezone/retention 또는 장애 상태
- 현금·평가금액·미체결·체결의 authoritative reconciliation이나 자동 원장 보정
- public API, Flutter 화면, risk approval, broker submit, paper/live readiness
