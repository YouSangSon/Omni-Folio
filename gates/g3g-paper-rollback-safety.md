# G3.7 Atomic Paper Halt/Rollback Safety

## Pass when

- 신규 paper intent 기록, 현재 process가 소유한 만료 전 execution lease·exact fencing 검증, K2C risk reservation과 durable dispatch는 한 transaction에서 모두 성공할 때만 생성된다.
- 수동 strategy rollback은 현재 선택 event를 확인하고 모든 활성 execution account에 token을 증가시킨 `manual_halt`를 append한 뒤 같은 transaction에 rollback event를 append한다.
- halt 또는 rollback event 중 하나라도 실패하면 execution authority와 strategy registry가 모두 이전 상태로 남는다.
- rollback 전에 이미 durable dispatch된 paper 주문은 신규 실행 권한 없이도 idempotent replay와 잔여 체결 복구를 계속한다.

## Evidence

- `TestG3PaperRunnerRequiresCurrentLeaseBeforeRecording`
- `TestG3PaperRunnerRollsBackIntentWhenDispatchAuthorizationFails`
- `TestG3PaperRollbackAtomicallyHaltsExecution`
- `TestG3PaperRunnerUsesSelectedStrategyRiskOrderReplayAndBackup`
- `cd services/core && go test ./...`

## Not proven

- paper 성능·저하 detector, 자동 rollback trigger 또는 scheduler
- 수수료, 세금, slippage, latency, quote stream과 실제 broker 관찰
- shadow/live promotion, broker write 또는 real-money readiness
