# G3AL — 연구 결과에서 모의 비교로 인계

## 상태와 범위

**구현·독립 리뷰 완료, 원격 CI 검증 대기.** 저장된 v1 연구 결과를 등록하고 [기존 Go 비교](g3aj-automatic-paper-promotion.md)로 넘기는 `paper-improve`를 추가한다. 연구 결과 SHA와 현재 선택 이벤트를 사용자가 복사하던 단계를 줄인다. schema v27/backup v21, 비교 journal과 실행 권한은 그대로 사용한다.

실제 RED는 head `0e8bfc6665c61eb848669298380237da206a08e2`, [Core40669802386 job122753398938](https://github.com/YouSangSon/Omni-Folio/actions/runs/40669802386/job/122753398938)에서 `unknown command "paper-improve"`로 실패했다. 이 실패를 구현 검증 통과로 세지 않는다. [PR #70](https://github.com/YouSangSon/Omni-Folio/pull/70)의 최종 head·CI·main 반영 여부는 후속 검증에서 기록한다.

## 연결 방식과 사용법

연구 파일은 후보를 담은 제안서이고 Go의 비교 기록은 그 제안을 검토한 결정이다. 파일 등록만으로 비교가 끝나거나 실행 권한이 생기지는 않는다. 기존 Python의 유한 탐색·검증 결과를 파일로 넘기고, Go가 현재 선택을 한 번 캡처해 비교와 선택을 맡는다.

| 선택 | 판단과 비용 |
| --- | --- |
| 기존 결과 파일을 Go에 전달 | 채택. 기존 입력 검증·registry·비교 journal을 재사용하며 Python 실행 환경을 새로 배포하지 않는다. |
| Go에서 Python을 직접 실행 | 현재 OCI에 없는 Python과 자식 프로세스·취소·환경 계약이 추가된다. 이번 인계에는 필요하지 않다. |
| 쉘에서 수동 명령을 연결 | 현재 선택 캡처, 재시도, 부분 저장 처리를 사용자에게 남긴다. |

먼저 기존 `improve_cli --bars … --config … --output …`으로 `strategy-improvement-result.v1` 파일을 만든다. 이후 기존 paper 계좌와 별도의 fresh forward CSV를 지정한다. 모든 플래그는 필수다.

```sh
(cd services/core && go run . paper-improve \
  -db ../../data/omni-folio.db -account '<EXISTING_ACCOUNT_REF>' \
  -artifact /absolute/path/to/research-result.json \
  -bars /absolute/path/to/fresh-forward-bars.csv \
  -attempt-id paper_improve_001)
```

명령은 두 파일을 크기가 제한된 일반 파일로 한 번씩 읽는다. artifact와 CSV 형식을 먼저 검증하고 v2는 기존처럼 거부한다. `paper_candidate`에는 기존 paper 전략·정수 수량 검증을 적용하며 `no_promotion`의 v1 등록 호환성은 유지한다. DB나 계좌를 생성·초기화하지 않는다.

## 저장·재시도 계약

[서비스](../services/core/paper_improvement.go)는 검증된 registry snapshot에서 기존 attempt ID를 먼저 찾는다. 이미 저장된 요청이면 계좌·결과 SHA·CSV byte를 대조하고 그 요청에 저장된 선택 이벤트를 사용한다. 새 요청만 현재 선택 이벤트를 한 번 캡처한다. 비교 전에 선택이 바뀌어도 같은 호출 안에서 새 선택으로 재시도하지 않는다.

연구 등록과 비교 claim은 별도 transaction이다. **attempt ID는 claim이 저장된 시점부터 입력에 묶인다.** 등록 후 claim 전에 실패하면 연구 등록만 남을 수 있으며, 다음 호출은 그때의 현재 선택을 다시 캡처할 수 있다. 명령 전체의 단 한 번 실행을 보장하지 않는다. 서로 다른 ID의 동시 요청은 양쪽 등록을 남겨도 기존 claim·구간·선택 조건을 통과한 요청만 비교한다.

성공 시 `paper-improvement.v1`은 항상 `result_sha256`, `registered`, `attempt_recorded`, `disposition`, `comparison`을 반환한다. `attempt_recorded`는 저장된 claim의 존재이며 이번 호출에서 새로 만들었다는 뜻이 아니다.

| `disposition` | 저장과 결과 |
| --- | --- |
| `research_not_candidate` | 연구가 보고한 `no_promotion`을 등록한다. Go가 재계산한 `HOLD`는 아니다. `attempt_recorded=false`, `comparison=null`이다. |
| `already_selected` | 캡처 시 같은 결과가 선택돼 있었다. 현재 상태가 계속 같다는 보장은 아니다. `attempt_recorded=false`, `comparison=null`이다. |
| `comparison` | 기존 `PaperPromotionReceipt`와 `attempt_recorded=true`를 반환한다. 정확한 재시도는 선택 변경 후에도 저장된 비교 결과를 재사용한다. |

두 비비교 결과는 ID를 예약하거나 비교 구간을 소비하지 않는다. 다만 등록한 연구의 구간은 기존 규칙대로 이후 비교의 freshness 경계에 포함된다. 기존 ID 충돌 검사는 비비교 판정·신규 등록보다 먼저 수행한다.

[CLI](../services/core/paper_improvement_cli.go)는 내부 DB 오류·파일 내용·경로를 출력하지 않는다. 저장 여부가 불확실하면 `strategy-status`로 등록을, 같은 attempt ID의 `paper-promotion-status`로 비교를 확인한 뒤 같은 파일·계좌·ID로 재시도한다. 취소 identity와 출력 writer 오류는 보존하며, 취소나 출력 실패가 이미 저장된 기록을 되돌리지는 않는다.

적용 확인: 자동 선택 후 rollback한 다음 같은 파일·계좌·ID로 다시 호출하면 이전 비교 결과를 반환해야 한다. `already_selected`를 새로 판단하거나 다시 선택하면 저장된 요청의 의미를 바꾼 것이다.

## 검증 근거

- 독립 읽기 전용 리뷰에서 P1/P2 결함은 발견되지 않았다. 이 결과는 컴파일·실행 통과를 대신하지 않는다.
- 실제 Python `improve_cli`가 만든 v1 결과를 Go CLI dispatch에 전달하는 RED를 먼저 확인했다. 구현 후 등록→fresh 비교→선택→기존 rollback과 정확한 재시도를 원격 CI에서 검증한다.
- 연구 부적격·이미 선택된 결과의 무소비, ID 충돌 전 등록 금지, 등록 후 권한·freshness 실패, pending 보존, 동일·상이 ID 동시 호출, 취소·출력 실패와 비공개 오류를 검사한다.
- 기존 `TestPaperPromotion` 집중 target과 전체 `make check DART=true FLUTTER=true CORE_TEST_PACKAGES='-timeout=30m ./...'`를 사용한다. Core가 바뀌므로 새 CI가 필요하다. Client는 소스·계약·검증 입력의 Git 객체가 같을 때만 이전 통과 근거를 재사용한다.
- 로컬 gofmt·diff, README 네 언어의 동일 shell32개(sh31·bash1), 기존 명령 보존·shell 문법과 변경 문서의 상대 링크309개를 정적으로 확인했다. 통합 테스트는 실제 Python CLI stdout JSON을 파일에 저장하므로 `--output` 옵션 자체를 검증한다고 세지 않는다. 로컬 앱·테스트·빌드·분석은 실행하지 않았다.

## 남은 범위

기존 flat·단일 계좌·중지된 실행 권한·runner·미완료 작업·pending·시각 cutoff·수량과 정책 일치·복구 검증을 그대로 따른다. 자동 abort·arm·계좌 초기화·실주문은 추가하지 않는다. Python 실행과 정기 탐색, 지속 운영은 별도다. 연구 CSV와 forward CSV의 이용 권한·가격 조정·provider·실제 지수 검증 및 VM·실기기 검증은 이번 근거에 포함하지 않는다.
