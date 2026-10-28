# G3AJ — 고정 후보의 로컬 paper 자동 비교

## 상태와 범위

**검증 대기.** Go core의 로컬 구현은 등록된 v1 `paper_candidate`를 현재 선택 전략과 같은 입력의 buy-and-hold에 비교한다. 세 구간의 조건을 통과하고 현재 상태도 유지될 때만 paper 선택을 바꾼다. Python의 적응형 후보 탐색이나 그 결과의 `paper_candidate` 표시는 이 고정 후보 비교의 증명이 아니다. v2 결과는 기존 registry와 신호 제출에서 계속 거부된다.

- 실제 RED: head `b8dec58517c98306316da1853c9e63ac9680e61e`, [Core40645288609 job122672570413](https://github.com/YouSangSon/Omni-Folio/actions/runs/40645288609/job/122672570413)에서 `paper-promote`가 `unknown command`로 실패했다.
- 구현 head `54090be5a90bde908dd3c8e80cd2e3b0d52cb665`의 Core40648451434는 집중 검사 전 Go 컴파일에서 `strings` import 누락으로 실패했다. 누락을 수정하고 리뷰에서 확인한 시간 경계를 보완 중이다. 전체 검사 통과는 아직 확인하지 않았다.

## 입력과 비교

1. DB에는 기존 paper 계좌 세션이 정확히 하나 있어야 한다. 해당 계좌는 보유 lot과 진행 중이거나 미확정인 주문이 없어야 한다. 모든 계좌의 실행 권한은 중지되어야 하고, 전역 runner의 활성 임대 및 미완결 체인 검사를 통과해야 한다. 현재 선택과 다른 등록된 v1 후보가 필요하며 두 전략의 수량과 세션의 체결 정책이 같아야 한다.
2. `-bars`는 한 번 읽는 최대 1 MiB의 UTF-8 일반 파일이다. 헤더는 `bar_at,symbol,open,high,low,close,volume`이며 한 개의 6자리 종목, 정규 UTC 초 단위의 증가하는 시각, 유효한 OHLCV가 필요하다. 행 수는 두 전략 중 더 긴 slow window의 공통 warmup과 정확히 90개 비교 bar다. 비교 종료 시각은 실행 시각을 넘을 수 없다.
3. 비교 구간의 시작은 claim 시점까지 등록된 모든 연구의 train·validation·final holdout 종료와 이 DB의 이전 claim 종료보다 엄격히 뒤여야 한다. 판정 대상은 warmup 뒤 90개 bar의 구간이다. 구간 검사는 실제 입수 시점이나 다른 DB에서의 사용 이력을 증명하지 않는다.
4. Go는 고정 후보·현재 전략·buy-and-hold를 공통 warmup과 30개 bar의 세 구간에 대해 각각 재계산한다. 각 구간은 같은 계좌 현금·정수 수량·수수료·세금·슬리피지·참여율·체결 지연을 사용하며 별도 계좌로 시작해 종료 시 청산한다. 미완료 주문이나 청산을 완료할 수 없으면 비교는 실패한다. buy-and-hold는 동일 bar의 모델 기준선이며 외부 지수 series가 아니다.

## 결과와 기록

각 구간에서 후보와 현재 전략의 완료된 왕복 거래가 각각 1회 이상이어야 한다. 후보의 비용 후 수익률은 0 이상이면서 현재 전략 이상이어야 하고, 최대 낙폭과 회전율은 현재 전략보다 높을 수 없다. 마지막 구간의 후보 수익률은 현재 전략과 buy-and-hold보다 각각 엄격히 높아야 한다. 조건에 처음 실패하면 `HOLD`와 해당 reason을 기록한다.

| `decision` | 의미 및 `reason` |
|---|---|
| `PENDING` | claim은 저장됐지만 결정은 아직 없다. `comparison_claimed` |
| `PROMOTE` | 모든 비교 조건과 최종 상태 검사를 통과했다. `all_forward_gates_passed` |
| `HOLD` | `insufficient_round_trips`, `candidate_negative_return`, `candidate_return_below_incumbent`, `candidate_drawdown_above_incumbent`, `candidate_turnover_above_incumbent`, `holdout_not_better_than_incumbent`, `holdout_not_better_than_buy_and_hold` 중 첫 실패 |
| `STALE` | 평가 후 선택·계좌·권한 등 캡처한 상태가 달라져 자동 선택하지 않았다. `account_or_selection_changed` |
| `ERROR` | 평가가 실패하거나 취소됐다. `comparison_failed` |
| `ABORTED` | 사용자가 미완료 claim을 명시적으로 중단했다. `explicit_abort` |

영수증 JSON에는 `attempt_id`, `input_data_sha256`, `comparison_start`, `comparison_end`, `decision`, `reason`이 있다. 비교가 완료된 경우 `comparison`에 `policy_version`, 세 `windows`, 비교 자체의 `decision`과 `reason`이 포함된다. 각 window는 `start`, `end`와 `candidate`, `incumbent`, `buy_and_hold`의 비용 후 수익률·낙폭·거래·체결·회전율·비용 지표를 갖는다. 최종 상태가 `STALE`이면 내부 비교 결과와 최종 `decision`은 다를 수 있다. `PROMOTE`에는 `selection_event_id`가 추가된다. 영수증에 원본 CSV는 없지만 claim의 원본 bytes는 DB와 백업에 저장되므로 접근을 제한해야 한다.

claim과 결정은 insert-only journal에 남는다. 평가 후 현재 선택과 계좌·권한·runner 등 상태를 다시 확인하며, `PROMOTE` 결정과 선택 이벤트는 같은 transaction에 기록한다. 등록 결과의 self-hash만으로는 비교를 증명하지 않는다. registry 복구는 claim과 입력 결합, 비용을 포함한 비교 결과, 자동 선택 이벤트의 연결을 다시 검증한다. 구현은 [promotion](../services/core/paper_promotion.go), [comparison](../services/core/paper_promotion_comparison.go), [recovery](../services/core/paper_promotion_recovery.go)에 있다.

## 명령과 실패 복구

아래 경로와 ID는 예시다. 기존 DB와 계좌, 등록된 후보 SHA, `strategy-status`의 현재 event ID, 조건을 충족하는 CSV로 바꾼다. 명령은 DB를 새로 만들거나 migration하지 않는다.

```sh
(cd services/core && go run . paper-promote \
  -db ../../data/omni-folio.db \
  -account '<EXISTING_PAPER_ACCOUNT>' \
  -candidate-result-sha '<REGISTERED_RESULT_SHA256>' \
  -expected-selection-event '<CURRENT_EVENT_ID>' \
  -attempt-id paper_compare_001 \
  -bars /absolute/path/to/fresh-forward-bars.csv)

(cd services/core && go run . paper-promotion-status \
  -db ../../data/omni-folio.db -attempt-id paper_compare_001)

# Only if the pending attempt should be aborted:
(cd services/core && go run . paper-promotion-abort \
  -db ../../data/omni-folio.db -attempt-id paper_compare_001)
```

오류나 출력 실패가 나도 이미 claim 또는 결정이 저장됐을 수 있다. 같은 attempt ID로 읽기 전용 `status`를 먼저 조회한다. claim 자체가 저장되지 않았다면 해당 기록이 없을 수 있다. 같은 계좌·후보 SHA·예상 이벤트·CSV bytes와 attempt ID를 다시 제출하면 저장된 `PENDING` 또는 완료 영수증을 돌려주며 재평가하지 않는다. 같은 ID의 다른 요청은 충돌이다.

claim 뒤의 `HOLD`, `ERROR`, `ABORTED`, `STALE`와 프로세스 중단으로 남은 `PENDING` 모두 비교 구간을 소비한다. `PENDING`은 새 비교를 막으며 시간만으로 자동 만료되거나 재개되지 않는다. 필요한 경우 `abort`로 명시적으로 닫고 앞선 평가보다 뒤의 새 구간을 준비한다. 이미 완료된 attempt의 abort는 저장된 결과를 반환한다. claim 전 오류에는 이 소비 규칙을 적용하지 않는다.

claim의 주문·연구 cutoff는 해당 시각까지 기록된 실제 prefix와 일치해야 한다. 이후 새 주문 이벤트와 연구 등록은 가장 최근 claim보다 엄격히 뒤의 시각만 허용하며, 같은 시각이나 시계 역행은 거절한다. 이미 저장된 요청의 정확한 재시도는 가능하다. 다음 claim도 이전 결정 시각보다 앞설 수 없다.

자동 선택된 결과의 신호 `data_as_of`는 비교 종료 시각보다 엄격히 뒤여야 한다. rollback 후 같은 결과를 수동으로 다시 선택해도 이전 자동 비교의 종료 경계는 유지된다. 이 기능은 기존 계좌를 생성·초기화하거나 execution authority를 자동으로 arm하지 않고, 브로커·실거래 요청을 보내지 않는다.

## 백업과 남은 한계

schema v27/backup v21 구현은 claim·decision hash/count를 기존 복구 증명과 함께 보관하고 복구 후보에서 다시 계산해 비교한다. v20/schema v26 등 지원하는 이전 백업은 원본을 보존하고 소유한 복사본에서 이력을 비운 채 migration한다. 현재 변경의 비어 있지 않은 journal 백업·복구를 포함한 CI 검증은 대기 중이다. 상세 스키마는 [migration 027](../services/core/migrations/027_paper_promotion.sql)과 [backup manifest](../contracts/backup-manifest.schema.json)에 있다.

CSV의 사용 권한, provider, 종목 식별·거래소·corporate action 등 metadata의 실제 정확성은 검증하지 않는다. 입력 시각과 중복 구간 검사는 데이터 출처나 실시간성을 보장하지 않는다. 연구 fold는 각 구간의 계좌를 초기화·청산하므로 지속 paper 계좌나 실제 시장 체결과 동일하다고 볼 수 없다. 수익·실전 성과, 자동 재시작, 배포 준비 또는 실거래 권한을 의미하지 않는다.
