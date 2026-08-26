# G3 Research Gate

## Pass when

- 동일 input snapshot과 manifest로 같은 결과를 재현한다.
- fee, tax, slippage, delay, partial fill과 lookahead 방지 fixture가 있다.
- 결과는 실제 기대수익이나 투자 추천으로 표시되지 않는다.
- Python process에 broker credential, order-submit permission, operational-table write 권한이 없음을 config와 test로 증명한다.
- promotion용 결과는 strategy/data/engine version과 parameter hash를 포함한다.
- 자동 개선은 선언된 유한 후보만 시계열 분할과 walk-forward로 평가하고 같은 입력에서 같은 winner와 artifact hash를 만든다.
- Go가 result·parameter hash를 재계산하고 input/config hash의 형식과 manifest 내부 input binding, manifest/gate 계약을 검증해 SQLite에 append-only로 등록한다. 원본 bars/config를 받지 않으므로 그 두 hash의 원본 재계산을 주장하지 않는다.
- `no_promotion` evidence는 보존하되 선택하지 못하고, `paper_candidate` 선택은 expected current event가 일치할 때만 append한다.
- rollback은 현재 event를 source로 요구하고, 모든 활성 execution authority를 halt/fence한 뒤 같은 transaction에서 직전 선택 또는 `no_strategy`만 append하며 기존 행을 수정·삭제하지 않는다.
- 전략 주문 intent는 선택 result SHA와 exact selection event ID를 함께 보존한다. 신규 기록과 durable dispatch는 둘 다 현재 registry replay와 일치해야 하며 rollback·reselection 뒤 stale event는 fail-closed한다.
- `paper-signal.v2`는 선택 전략·입력 data hash·유효 시간·종목·목표 수량만 보존한다. Go가 체결+미완결 BUY를 원자 netting해 delta OrderIntent를 만들고 K2C risk를 통과한 뒤 공통 주문 상태 머신에서만 결정적 부분/완전 체결한다. 실제 broker transport에는 진입하지 않는다.
- 부족한 표본, holdout 오염, 비용 후 성과·낙폭·거래 수 gate 실패에서는 승격하지 않는다.
- 자동 승격 결과는 `paper_candidate` 또는 `shadow`를 넘지 않으며 live order 권한을 포함하지 않는다.

## Evidence

- 2026-08-24 13 Python tests pass without third-party runtime dependencies, network access, broker credential surface, operational DB writes, or order-submit permission.
- The backtest golden manifest covers delayed next-eligible-bar fills, fee, tax, slippage, participation-based partial fills, canonical decimals, and zero lookahead violations.
- The improvement runner uses two expanding walk-forward folds and one final holdout, finite SMA candidates, a buy-and-hold baseline, deterministic selection/hash, and fails closed on short folds, zero delay, failed validation/holdout/baseline gates.
- Fixture result: `strategy-improvement-result.v1`, policy `sma-expanding-walk-forward.v1`, target `paper_candidate`, SHA-256 `bf00a8e0d6c59a58f53e7dbe772ad6d235f385ebf4341aa4f451774a9a935513`. This is research evidence, not expected return or live authorization.
- `TestG3Registry*`는 실제 Python CLI→Go ingest의 cross-runtime hash, idempotent registration, stale selection, rejected-candidate 차단, 이전 선택/`no_strategy` rollback, insert-only/replay와 schema v8/backup v5 복구를 검증한다.
- `TestG3StrategyOrderRequiresCurrentSelectionAtRecordAndDispatch`는 이미 기록된 intent의 idempotent retry를 보존하면서 stale 신규 기록과 rollback된 전략의 durable dispatch를 차단하고, 새 selection event에 묶인 주문만 허용함을 검증한다.
- `TestG3PaperRunner*`는 선택 target→원자 delta/netting→record-time lease/fencing→같은 transaction의 K2C reservation·durable dispatch→공통 order replay→부분/완전 체결→backup/restore와 만료·rollback·동시 중복·Kiwoom transport 차단을 검증한다.
- 현재 paper adapter는 local fixture 한 건만 처리한다. 수수료·세금·slippage·quote stream, 자동 scheduling, 실제 paper 성능 관찰·성능저하 rollback 또는 broker-write 순간까지의 selection/lease race 차단은 제공하지 않는다.
