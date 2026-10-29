# G3AL — 연구 결과에서 모의 비교로 인계

## 상태와 범위

**구현·독립 리뷰·Core CI와 고정 버전 로컬 검증을 통과해 main에 반영했다.** 저장된 v1 연구 결과를 등록하고 [기존 Go 비교](g3aj-automatic-paper-promotion.md)로 넘기는 `paper-improve`를 추가했다. 연구 결과 SHA와 현재 선택 이벤트를 사용자가 복사하던 단계를 줄인다. schema v27/backup v21, 비교 journal과 실행 권한은 그대로 사용한다.

검증 head는 `c53f357067ae8bd73cd1594d7591fb6d2cf7d3fd`, [PR #70](https://github.com/YouSangSon/Omni-Folio/pull/70)의 main은 `09074a73bcfdc742b04403ed6a986515b374b697`이다. 원격 main을 fetch한 뒤 head의 ancestry와 전체 Git tree 일치를 확인했다. 후속 기록 변경은 이 gate·`PLAN.md`·`GATES.md`만 바꾸므로 실행 입력이 같은 통과 근거를 재사용한다.

실제 RED는 head `0e8bfc6665c61eb848669298380237da206a08e2`, [Core40669802386 job122753398938](https://github.com/YouSangSon/Omni-Folio/actions/runs/40669802386/job/122753398938)에서 `unknown command "paper-improve"`로 실패했다. 이 실패를 구현 검증 통과로 세지 않는다.

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

- 독립 읽기 전용 리뷰는 지정 head/base의 변경13파일과 연결된 registry·promotion·복구 경로에서 P1/P2 결함을 발견하지 못했다. 독립 실행 검증과 함께 `PASS+NOTES`로 기록했다. 새 동시 호출 테스트는 이미 등록된 후보를 사용하고, 취소 테스트는 사전 취소이므로 최초 등록끼리의 경합이나 새 명령 실행 도중의 native SIGINT/SIGTERM까지 검증했다고 세지 않는다.
- [Core40672222560](https://github.com/YouSangSon/Omni-Folio/actions/runs/40672222560), job122761600282가 검증 head에서 통과했다. 기존 `TestPaperPromotion`·migration 집중 target과 `make check DART=true FLUTTER=true CORE_TEST_PACKAGES='-timeout=30m ./...'`를 실행했다. Go core 전체503.019초·내부6패키지, Python48·JSON44 파싱, Go format/vet·Python 문법 검사·소유 자원 cleanup·소스 일치를 확인했다. 환경은 Ubuntu24.04, Go1.24.5, Python3.14.5, Go 실행 병렬도2·빌드 병렬도1이다. JSON 파싱을 전체 JSON Schema 검증으로 확대하지 않는다.
- 실제 Python `improve_cli` stdout을 파일로 저장해 Go CLI dispatch에 전달하고 등록→fresh 비교→선택→기존 rollback과 정확한 재시도를 검증했다. 연구 부적격·이미 선택된 결과의 무소비, ID 충돌 전 등록 금지, 등록 후 권한·freshness 실패, pending 보존, 동일·상이 ID 동시 호출, 사전 취소·출력 실패와 비공개 오류도 포함한다. 이 경로는 Python의 `--output` 옵션 자체를 검증하지 않는다.
- 2026-10-29의 독립 detached checkout에서 Go1.24.5·Python3.14.5 실행 경로와 PATH를 고정했다. `make test FLUTTER=true CORE_TEST_PACKAGES="-race -run='^TestPaperPromotionImprovement' -count=1 -timeout=10m -v ./..."`가 Go 집중10개·하위16개·Python48과 cleanup을 exit0으로 통과했다. core15.528초·Python5.838초는 검사 소요시간이며 성능 개선 측정이 아니다. 같은 환경의 `make smoke`도 exit0으로 health·status·preview·apply·snapshot·activity·sample market data를 검증했다. 임시 root와 프로세스가 종료됐고18080 listener는 없었다. checkout은16,888KiB로 전후 동일했다. 로그와 정확한 명령·종료 코드는 Git 제외 경로 `docs/_internal/skill-outputs/goal-20261029-paper-handoff/`의 `validation.json`과 `*-pinned.log`에 보관했다.
- [Client40662911694](https://github.com/YouSangSon/Omni-Folio/actions/runs/40662911694), job122730096020의 집중15·전체472, format/analyze·web/ARM64 release·권한 반례8·패키지/소스 일치를 재사용했다. 해당 head `06e76dd8589566b8925d33e0dee38060fac87e4f`와 이번 head의 `apps/client`·전체 `contracts`·`.tool-versions`·Client workflow·`Makefile`·`tools`·`tests` Git 객체가 모두 같다. Client tree는 `d8f8ad1e62cf2e11e5c7f8cba843c62087577740`이며 새 Client 전체 검사·빌드는 반복하지 않았다.
- 앞선 로컬 gofmt·diff, README 네 언어의 동일 shell32개(sh31·bash1), 기존 명령 보존·shell 문법과 상대 링크309개 확인은 당시 범위의 정적 근거다. 후속 기록 변경은 변경 문서의 링크·내용과 diff를 다시 검사한다.

### 실제 실행 디렉터리에서 도구 버전 확인

처음 로컬 검사는 exit0이었으나 고정 버전의 근거에서 제외했다. 현재 asdf는 저장소 루트의 `.tool-versions`에서는 Go1.24.5를, `services/core`의 `go.mod`에서는 Go1.27.3을 선택했다. `asdf current golang`과 `go version`으로 차이를 재현했고 실제 smoke compiler 경로도1.27.3이었다. Python 자체의 버전 문제로 단정하지 않는다.

전역 설정을 바꾸지 않고 `GO`·`PYTHON`에 검증한 절대 실행 경로를, PATH 앞부분에 같은 bin 디렉터리를 지정해 집중 검사와 smoke만 다시 실행했다. `GOTOOLCHAIN=local`을 유지하고 `services/core` 안의 버전과 실제 smoke compiler가1.24.5임을 확인했다. 이전 로그는 추가 호환성 관찰로 보존하며 위의 고정 버전 통과 근거와 구분한다. 실행 디렉터리나 asdf 설정이 바뀌면 이 확인부터 다시 한다.

## 남은 범위

기존 flat·단일 계좌·중지된 실행 권한·runner·미완료 작업·pending·시각 cutoff·수량과 정책 일치·복구 검증을 그대로 따른다. 자동 abort·arm·계좌 초기화·실주문은 추가하지 않는다. Python 실행과 정기 탐색, 지속 운영은 별도다. 연구 CSV와 forward CSV의 이용 권한·가격 조정·provider·실제 지수 검증 및 VM·실기기 검증은 이번 근거에 포함하지 않는다.
