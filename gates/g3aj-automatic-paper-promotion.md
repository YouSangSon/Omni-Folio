# G3AJ — 고정 후보의 로컬 paper 자동 비교

## 상태와 범위

**원격 CI 통과·main 병합.** Go core는 등록된 v1 `paper_candidate`를 현재 선택 전략과 같은 입력의 buy-and-hold에 비교한다. 세 구간의 조건을 통과하고 현재 상태도 유지될 때만 paper 선택을 바꾼다. Python의 적응형 후보 탐색이나 그 결과의 `paper_candidate` 표시는 이 고정 후보 비교의 증명이 아니다. v2 결과는 기존 registry와 신호 제출에서 계속 거부된다.

테스트 head는 `4a9b7e5fc3ef306cc05b08a07e1db92ce5c0c7f3`, [PR #65](https://github.com/YouSangSon/Omni-Folio/pull/65)의 main은 `ae47a51e11bc722dd38d05a04c3d0e9f834c7130`이다. head가 main의 조상이며 전체 Git tree가 같다. 뒤의 검증 기록 변경은 `PLAN.md`, `GATES.md`, 이 gate만 바꾸므로 실행 근거를 재사용한다.

## 선택 이유와 재사용

핵심은 평가할 기간을 먼저 예약하고, 그 기록에만 결정을 붙이는 것이다. 성공한 결과만 저장하면 실패한 비교를 숨기고 같은 기간을 반복 사용할 수 있다. claim을 먼저 영속화하면 취소·프로세스 중단도 소비한 기간으로 남는다. 재평가를 자동으로 시작하는 대신 저장된 결과 조회와 명시적 abort를 제공하는 이유다.

기존 Python은 후보 제안을 맡고 Go가 고정 후보를 독립적으로 재계산하도록 분리했다. Python의 보고 지표를 그대로 승인 근거로 사용하면 적응형 후보 탐색의 집계와 고정 후보의 전방 성과를 혼동하게 된다. 후보 탐색을 Go에 통째로 복제하는 대신 [공유 체결 함수](../services/core/internal/paperdomain/paper.go)의 `CalculateFill`, [공유 SMA](../services/core/internal/strategydomain/sma.go), [고정 fold 계산](../services/core/internal/researchdomain/fold.go)을 연결했다. 새 의존성·서비스는 추가하지 않았다. 계산 정책의 배경은 [G3AI](g3ai-paper-compatible-research.md)가 소유한다.

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

schema v27/backup v21은 claim·decision hash/count를 기존 복구 증명과 함께 보관하고 복구 후보에서 다시 계산해 비교한다. v20/schema v26 등 지원하는 이전 백업은 원본을 보존하고 소유한 복사본에서 migration한다. 기존 journal은 유지하며 새 promotion claim·decision만 빈 상태로 추가한다. 비어 있지 않은 journal의 백업·복구와 이전 hash·행 수·외래키 보존을 CI에서 확인했다. 상세 스키마는 [migration 027](../services/core/migrations/027_paper_promotion.sql)과 [backup manifest](../contracts/backup-manifest.schema.json)에 있다.

현재 상태 hash는 평가 전후의 선택·계좌·권한 변경을 감지하는 비교용 값이다. 변경 가능한 runner lease와 실행 권한이 과거에도 중지 상태였다는 사실을 백업만으로 다시 증명하지는 않는다. 복구는 영속 journal의 입력·현금 cutoff·계산·선택 연결을 검증하고, 현재 실행 조건은 명령 시점의 transaction이 검사한다. 이후 자동 선택을 추가해도 그보다 앞선 선택에 연결된 과거 신호의 유효성은 바꾸지 않는다.

CSV의 사용 권한, provider, 종목 식별·거래소·corporate action 등 metadata의 실제 정확성은 검증하지 않는다. 입력 시각과 중복 구간 검사는 데이터 출처나 실시간성을 보장하지 않는다. 연구 fold는 각 구간의 계좌를 초기화·청산하므로 지속 paper 계좌나 실제 시장 체결과 동일하다고 볼 수 없다. 수익·실전 성과, 자동 재시작, 배포 준비 또는 실거래 권한을 의미하지 않는다.

## 검증 근거

- 실제 RED: head `b8dec58517c98306316da1853c9e63ac9680e61e`, [Core40645288609 job122672570413](https://github.com/YouSangSon/Omni-Folio/actions/runs/40645288609/job/122672570413)에서 `paper-promote`가 `unknown command`로 실패했다. 이후 import 누락, restore의 외래키/trigger 계약과 시각 표현 차이, 기존 migration 테스트의 외래키 예상 목록을 수정했다. 실패·취소한 실행을 통과 근거로 세지 않는다.
- 독립 리뷰의 과거 구간 재사용, 현금·연구 cutoff 위조, 알 수 없는 migration trigger 삭제 지적을 보완하고 재검토했다. 실제 계좌의 손실 후 현금9977.44018, 상태 변경 시 선택 보류, 취소/abort/정확한 재시도, 입력 변조, backup 필수 proof, rollback 뒤 같은 후보의 재선택과 과거 신호 보존을 확인했다. Go/Python의 실제 fold 함수도 부분체결·고정 후보·holdout의 후보/현재 전략/기준선 다섯 사례로 대조했다.
- [Core40652322333](https://github.com/YouSangSon/Omni-Folio/actions/runs/40652322333), job122695055782: 자동 비교32개와 기존 journal migration1개의 집중 검사가18.198초에 통과했다. 이어 `make check DART=true FLUTTER=true CORE_TEST_PACKAGES='-timeout=30m ./...'`의 Core 전체444.051초·내부6패키지·Python48·JSON43 파싱·Go vet/format·소유 자원 cleanup·소스 일치가 통과했다. Ubuntu24.04, Go1.24.5, Python3.14.5, Go 병렬도2/빌드 병렬도1의 기존 무료 CI다. 시간은 CI 실행 시간이며 성능 개선율이 아니다.
- [Client40652322242](https://github.com/YouSangSon/Omni-Folio/actions/runs/40652322242), job122695055796: 집중22/전체457·Dart format42파일 변경0·analyze·web/ARM64 release·권한 반례8·패키지/소스 일치가 통과했다. client tree는 `ff3d36ccec088bc3458ea1ef93e7401ffde1ec2e`다.
- APK는23,677,898bytes, SHA256 `d59b279fbad80f91661e0dc6fee8488827adba81df658e626a11c7c7d15cd8ae`, registrant SHA256 `5d31e4d80984b4f5254d2e6629d2cc2b16760ab4774a94faa863c48897f18326`이다. 기존 INTERNET·signature-protected receiver 권한만 허용했다. cache/artifact 업로드는 없으며 같은 client 소스라는 사실을 APK byte 재현성으로 확대하지 않는다.
- 로컬은 gofmt·diff·JSON 파싱·README 네 언어의 동일 shell30개(sh29·bash1)와 상대 링크 확인만 수행했다. JSON 파싱은 JSON Schema 의미 검증 실행이 아니다. 공유 호스트의 앱·테스트·빌드, 실제 제공자·VM·새 iOS·실기기·실주문은 실행하지 않았다. RED부터 최종 CI까지의 로그는 `docs/_internal/skill-outputs/goal-20261028-promotion-*.log`에 보존했다.

적용 확인: 실패한 비교의 CSV 값을 바꾸거나 attempt ID를 새로 붙여도 이미 소비한 시간 구간을 재사용할 수 없다. 파일 hash만 달라졌다는 이유로 통과하면 holdout 사용 이력 계약을 지키지 못한 것이다.
