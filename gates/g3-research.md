# G3 Research Gate

## Pass when

- 동일 input snapshot과 manifest로 같은 결과를 재현한다.
- fee, tax, slippage, delay, partial fill과 lookahead 방지 fixture가 있다.
- 결과는 실제 기대수익이나 투자 추천으로 표시되지 않는다.
- Python process에 broker credential, order-submit permission, operational-table write 권한이 없음을 config와 test로 증명한다.
- promotion용 결과는 strategy/data/engine version과 parameter hash를 포함한다.

## Evidence

- Pending deterministic CLI test and manifest.
