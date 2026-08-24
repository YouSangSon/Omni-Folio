# G3.6 Credential-free Paper Execution Foundation

## Pass when

- 새 paper 주문은 현재 선택된 `paper_candidate`의 result SHA와 exact selection event를 가진 유효한 `paper-signal.v1`에서만 생성된다.
- 기존 K2C lease, fencing, kill switch와 risk reservation을 우회하지 않는다.
- 같은 `OrderIntent`/`OrderEvent` 상태 머신에서 결정적 ACK, 부분 체결, 완전 체결과 idempotent observation replay를 검증한다.
- selection rollback 또는 signal 만료는 새 주문을 차단하되 이미 durable dispatch된 주문의 잔여 체결 복구를 막지 않는다.
- paper 주문은 Kiwoom mock·production transport에 진입하지 않고 schema v7/backup v5 restore 뒤에도 같은 상태로 replay된다.

## Evidence

- `TestG3PaperRunnerUsesSelectedStrategyRiskOrderReplayAndBackup`
- `TestG3PaperRunnerRejectsExpiredStaleOrUnsafeInputsBeforeOrderCreation`
- `TestK2CMigrationPreservesLegacyNonAuthoritativeOrderEvents`
- `cd services/core && go test ./...`

## Not proven

- 자동 signal scheduler, quote stream, portfolio target/netting, 여러 전략의 자금 배분
- 수수료, 세금, slippage, latency와 paper 성능·저하 evidence
- shadow/live parity, broker-write fencing, credentialed broker 관찰 또는 실제 주문
