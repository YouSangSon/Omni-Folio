# G3 Research Gate

## Pass when

- 동일 input snapshot과 manifest로 같은 결과를 재현한다.
- fee, tax, slippage, delay, partial fill과 lookahead 방지 fixture가 있다.
- 결과는 실제 기대수익이나 투자 추천으로 표시되지 않는다.
- Python process에 broker credential, order-submit permission, operational-table write 권한이 없음을 config와 test로 증명한다.
- promotion용 결과는 strategy/data/engine version과 parameter hash를 포함한다.
- 자동 개선은 선언된 유한 후보만 시계열 분할과 walk-forward로 평가하고 같은 입력에서 같은 winner와 artifact hash를 만든다.
- 부족한 표본, holdout 오염, 비용 후 성과·낙폭·거래 수 gate 실패에서는 승격하지 않는다.
- 자동 승격 결과는 `paper_candidate` 또는 `shadow`를 넘지 않으며 live order 권한을 포함하지 않는다.

## Evidence

- 2026-08-24 13 Python tests pass without third-party runtime dependencies, network access, broker credential surface, operational DB writes, or order-submit permission.
- The backtest golden manifest covers delayed next-eligible-bar fills, fee, tax, slippage, participation-based partial fills, canonical decimals, and zero lookahead violations.
- The improvement runner uses two expanding walk-forward folds and one final holdout, finite SMA candidates, a buy-and-hold baseline, deterministic selection/hash, and fails closed on short folds, zero delay, failed validation/holdout/baseline gates.
- Fixture result: `strategy-improvement-result.v1`, policy `sma-expanding-walk-forward.v1`, target `paper_candidate`, SHA-256 `bf00a8e0d6c59a58f53e7dbe772ad6d235f385ebf4341aa4f451774a9a935513`. This is research evidence, not expected return or live authorization.
