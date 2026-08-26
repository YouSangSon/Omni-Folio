# G3.6 Credential-free Paper Execution Foundation

## Pass when

- 새 paper 주문은 현재 선택된 `paper_candidate`의 result SHA와 exact selection event를 가진 유효한 `paper-signal.v2` target에서만 생성된다. signal은 계좌·방향·주문 수량·가격을 소유하지 않는다.
- Go가 같은 계좌·종목의 체결 수량과 미완결 BUY 전체 수량을 목표에서 한 transaction으로 차감하고, 양수 delta만 OrderIntent로 만든다. 같은 목표의 반복·동시 신호는 주문을 중복 생성하지 않는다.
- 신규 intent 기록, 현재 process의 만료 전 K2C lease·exact fencing 검증, risk reservation과 durable dispatch를 한 transaction으로 묶는다.
- 같은 `OrderIntent`/`OrderEvent` 상태 머신에서 결정적 ACK, 부분 체결, 완전 체결과 idempotent observation replay를 검증한다.
- selection rollback 또는 signal 만료는 새 주문을 차단하되 이미 durable dispatch된 주문의 잔여 체결 복구를 막지 않는다.
- paper 주문은 Kiwoom mock·production transport에 진입하지 않고 schema v7/backup v5 restore 뒤에도 같은 상태로 replay된다.

## Evidence

- `TestG3PaperRunnerUsesSelectedStrategyRiskOrderReplayAndBackup`
- `TestG3PaperRunnerRejectsExpiredStaleOrUnsafeInputsBeforeOrderCreation`
- `TestG3PaperRunnerNetsTargetsAgainstFilledAndOutstandingOrders`
- `TestG3PaperRunnerSerializesConcurrentTargets`
- `TestG3PaperRunnerRequiresCurrentLeaseBeforeRecording`
- `TestG3PaperRunnerRollsBackIntentWhenDispatchAuthorizationFails`
- `TestK2CMigrationPreservesLegacyNonAuthoritativeOrderEvents`
- `cd services/core && go test ./...`

## Not proven

- 자동 signal scheduler, quote stream, 외부 보유·현금 통합과 여러 전략의 자금 배분
- 목표 감소를 위한 SELL/down-rebalance와 미체결 취소·대체
- 수수료, 세금, slippage, latency와 paper 성능·저하 evidence
- shadow/live parity, broker-write fencing, credentialed broker 관찰 또는 실제 주문
