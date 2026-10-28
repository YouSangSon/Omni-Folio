# G3AI — 모의 체결 계산과 호환되는 연구 v2

## 문제와 범위

기존 연구 v1은 잔여 현금과 거래량에서 소수 수량을 계산하지만 Go 모의 체결은 정수 주만 처리한다. 가격100·슬리피지10bps·체결 수수료1이면 1주에101.1이 필요하다. 현금101.09로 소수 주를 사는 연구 결과를 정수 주의 paper 성과로 사용할 수 없다.

v2는 기존 CLI와 유한 후보 탐색을 재사용하면서 `paper_bar_open_v1`의 체결 산술을 적용한다. 기존 v1의 소수 수량·보고서·수동 선택 계약은 유지한다. DB schema26/backup20, Go 체결기, registry, 신호 제출과 운영 권한은 바꾸지 않는다. v2 결과는 기존 registry와 signal 경로에서 거부된다.

## 계산 계약

| 항목 | v2 규칙 |
|---|---|
| 입력·결과 | `strategy-improvement.v2` / `strategy-improvement-result.v2` |
| 엔진·평가정책 | `omni-folio-paper-reference` `1.0.0` / `sma-paper-fill-walk-forward.v1` |
| 전략 | `long_only_sma_crossover` `1.0.0`, 양의 정수 수량≤4611686018427387903, 0<fast<slow≤365 |
| SMA | 종가<10¹², 소수 최대8자리. 평균 비교는 정확한 분수 계산 |
| 체결량 | `floor(volume × participation) − consumedCapacity`, 주문 잔여량과 SELL 보유량으로 제한 |
| 매수 | 수수료를 제외한 현금에서 불리한 체결 가격으로 살 수 있는 정수 주만 체결 |
| 비용 | 체결마다 고정 수수료, SELL에만 세금, 방향별 불리한 슬리피지. 무체결은 비용0, 음수 SELL 순수입은 오류 |
| 금액 | 표준 라이브러리 `Fraction`으로 정확히 계산하며 유한 소수 그대로 출력 |
| 비율 | 수익률·최대낙폭·회전율을 소수점 아래28자리 half-even으로 출력. 후보 순위와 gate는 출력된 값으로 판단 |
| 검증 집계 | 각 fold의 출력된 수익률 평균을 다시28자리로 반올림, 최대낙폭은 최댓값, 나머지 수치는 합계 |
| 입력 hash | CSV bytes를 한 번 읽고 같은 bytes를 파싱·hash. v1도 같은 캡처 원칙 적용 |

후보 선정은 각 validation fold 이전 자료로 다시 수행한다. 따라서 `walk_forward_metrics`는 fold별로 선택한 전략들의 집계이며, 최종 고정 challenger 하나의 과거 성과가 아니다. 마지막 holdout은 최종 후보 선정 뒤 평가하지만 실행 간 사용 이력을 보관하지 않으므로 재실행된 holdout을 미사용 자료로 간주할 수 없다.

연구 fold마다 자본을 초기화하고 마지막에 강제 청산한다. 미완결 주문이 있으면 새 신호를 건너뛰고, 종료까지 주문·청산을 완료하지 못하면 실패한다. 운영 paper runner의 지속 계좌·활성 목표 충돌 처리와 다르므로 체결·SMA 계산의 호환성을 전체 운영 동등성으로 확대하지 않는다.

## 선택과 구현

기존 v1을 정수 계산으로 바꾸는 방법은 같은 버전의 결과 재현성과 소수 수량 보고서 계약을 깨뜨린다. 독립 평가기를 통째로 복제하면 후보 선정·holdout 분리 로직이 중복된다. 기존 `improve.py`의 흐름을 재사용하고 실제 체결 산술만 `paper_fill.py`로 분리했다. 외부 의존성이나 서비스는 추가하지 않았다.

[Python Fraction 공식 문서](https://docs.python.org/3.14/library/fractions.html)는 Decimal에서 정확한 유리수로의 변환과 half-even 반올림을 정의한다. 이 표준 라이브러리의 원리를 사용했으며 외부 코드나 시장 자료를 가져오지 않았다. 체결 기준은 저장소의 `services/core/internal/paperdomain/paper.go:CalculateFill`, SMA 범위는 `internal/strategydomain/sma.go:VerifySMACrossover`다.

설정과 결과는 `contracts/strategy-improvement-config-v2.schema.json`, `strategy-improvement-result-v2.schema.json`에서 v1의 공통 필드를 참조하고 바뀐 버전·전략 계약만 지정한다. 같은 CLI의 `--config`에 `contracts/fixtures/strategy-improvement-paper-config.json`을 전달하면 로컬 합성 데이터의 v2 결과를 얻는다. `make run-improvement`와 기존 등록 예제는 v1을 유지한다.

## 검증 근거와 다음 작업

- 실제 RED: head `08114da98a2a8012fe4b6b7d7016257101fb8451`, [Core40640407394](https://github.com/YouSangSon/Omni-Folio/actions/runs/40640407394)의 새 평가 검사에서 `ValueError: config must be a strategy-improvement.v1 object`로 실패했다. 이후 전체 검사는 실행되지 않았다.
- 공통 `paper-fill-cases.json`은 실제 Python `calculate_fill`과 Go `CalculateFill`로 성공·무체결·오류와 모든 금액 필드를 비교한다. 수수료 포함 매수 경계, 거래량 내림·잔여/보유 제한, SELL 세금·음수 순수입, 28자리보다 긴 금액·수수료·세금·슬리피지, canonical 입력을 포함한다.
- 실제 fold 검사는 2주+2주 매수→4주 매도에서 종료 현금992.204·수수료3·세금3.996·슬리피지0.8, 지연 체결과 잔여 주문을 확인한다. holdout 변경 시 후보 선정 보존, Decimal context와 무관한 v2, 단일 입력 캡처, 기존 CLI, v2 registry 거절을 검사한다.
- 독립 리뷰에서 남은 차단 결함이 없음을 확인했고, 잘못된 schema version의 오류 형식 회귀도 보완했다. 최종 head `fa94c0978e9780458bd1e6074200393e892dc26f`와 [PR #63](https://github.com/YouSangSon/Omni-Folio/pull/63)의 main `33f4fc84371ffbb4c486643131ca7087b823fb13`는 전체 tree가 같고 head가 main의 조상이다. 뒤의 검증 기록 변경은 `PLAN.md`, `GATES.md`, 이 gate만 바꾸며 코드·계약·워크플로의 실행 근거를 재사용한다.
- [Core40641446740](https://github.com/YouSangSon/Omni-Folio/actions/runs/40641446740), job122660287537: `PYTHONPATH=services/research python -m unittest discover -s services/research/tests -p 'test_paper_improve.py' -v`의 집중9개와 `make check DART=true FLUTTER=true CORE_TEST_PACKAGES='-timeout=30m ./...'`의 Core 전체444.742초·내부5패키지·Python48·JSON43 파싱·Go vet/format·소유 자원 cleanup/source identity가 통과했다. 시간은 CI 실행 시간이며 성능 개선율이 아니다.
- [Client40641446700](https://github.com/YouSangSon/Omni-Folio/actions/runs/40641446700), job122660287672: 집중22/전체457·Dart format42파일 변경0·analyze·web/ARM64 release·권한 반례8·패키지/소스 일치가 통과했다. client tree는 `ff3d36ccec088bc3458ea1ef93e7401ffde1ec2e`다.
- APK는23,677,898bytes, SHA256 `e182241aa65f88d367173b5bb95bca232633e7909c4b858228e95af1f2619020`, registrant SHA256 `5d31e4d80984b4f5254d2e6629d2cc2b16760ab4774a94faa863c48897f18326`이다. 기존 INTERNET·signature-protected receiver 권한만 허용했다. cache/artifact 업로드는 없고 같은 client 소스라는 사실을 APK byte 재현성으로 확대하지 않는다.
- 로컬은 Python AST·JSON43 파싱·새 schema 참조 대상/JSON pointer 존재·README4개 shell29/문서 링크233·gofmt/diff만 확인했다. JSON 파싱과 참조 확인은 JSON Schema 의미 검증 실행이 아니다. 공유 호스트의 앱·테스트·빌드와 VM은 실행하지 않았다. RED·최종 로그는 `docs/_internal/skill-outputs/goal-20261028-paper-research-*`에 보존했다.
- 자동 승격은 후속이다. 신뢰된 재계산, 평가 전에 영속화한 holdout 사용권, 같은 입력·비용·기간의 incumbent 비교, 현재 선택 CAS·runner lease·미완결 주문·계좌 정책 검증과 원자적 결정/선택 기록이 필요하다. 실제 제공자·VM·실기기·실주문은 이 단위의 검증 범위가 아니다.

적용 확인: 현금101.09에서101.1로 바뀌면 매수 가능량은0주에서1주가 된다. 두 경우에 모두 소수 주가 나오거나 무체결 수수료가 붙으면 체결 계약과 다르다.
