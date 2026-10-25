# G1.24 입출금 경계의 누적 TWR

상태: 구현·최종 금융 리뷰 및 원격 검사 대기. [PR #46](https://github.com/YouSangSon/Omni-Folio/pull/46)의 RED에서는 실제 원장 입력 후 API 부재(404)를 확인했다. 통과·병합·배포 완료를 뜻하지 않는다.

## 문제와 원리

100원이 110원이 된 뒤 100원을 더 넣어 210원이 되었다면 투자 수익률은 10%다. 자산 총액의 단순 증가는 입출금을 수익으로 오인한다. TWR은 외부 입출금 직전의 자산 가치를 이전 입출금 직후 자본으로 나눈 구간 비율을 이어 곱한다. 마지막에는 기말 자산을 마지막 입출금 직후 자본으로 나눈다. 초기 자본이 만들어지는 첫 입출금 자체는 수익이 아니다.

이 구현은 원장 시작 이후 전체 포트폴리오의 **누적 값**이다. XIRR처럼 날짜에 따라 할인하거나 연환산하지 않는다. 값은 KRW 기준이며 매매·배당·수수료·세금·환전은 외부 자본이 아닌 자산 내부의 변화다. 두 구간이 각각 10% 수익이면 총합20%가 아닌 `1.1×1.1−1=21%`다.

## 참고와 선택

[Portfolio Performance의 TWR 설명](https://help.portfolio-performance.info/en/concepts/performance/time-weighted/)에서 외부 흐름의 영향을 제거하는 구간 비율과 연쇄 곱의 원리를 참고했다. 문서의 General Method/Eq2와 현금 흐름 경계 평가의 조건을 사용한다. 원본 코드·문장·이미지·자산은 차용하지 않았고 새 의존성도 없다. PP의 하루 시작 입금·하루 끝 출금 관례나 동일 수치 재현을 주장하지 않는다.

| 선택지 | 필요한 자료·한계 | 이번 선택 |
|---|---|---|
| 기말 값의 단순 증가 | 과거 경계가 없으며 입출금을 수익으로 오인 | 사용하지 않음 |
| 일별 평가·일별 현금 흐름 관례 | 날짜별 평가와 하루 안의 시각 가정이 필요 | 별도 정책으로 남김 |
| 사건 경계의 저장 관측 평가 | 외부 흐름 경계·기말에 필요한 관측만 선택하되 해당 경계 자료가 없으면 계산 불가 | 현재 개인용 구조에서 채택 |

가설은 필요한 경계만 평가하면 일별 시계열을 새로 저장하지 않고 기존 검증 원장·가격·환율을 재사용할 수 있다는 것이다. 이를 처리량 개선이나 실제 시세 정확도 측정으로 표현하지 않는다. 선택한 가격은 경계 이전의 마지막 적격 관측이며 최대24시간까지 허용하므로, 실제 흐름 직전의 시장 가격과 같다는 보장은 없다.

## 시간·정정·가격 단위 계약

- `GET /v1/portfolio/twr`는 query를 받지 않는다. 서버 clock을 한 번 읽고 같은 read transaction에서 schema·전체 원장 정책 전환·정규 사건·관측 무결성을 증명한다. 계좌·원본/정정 링크·receipt·종목 내부 ID를 공개하지 않고 기존 `no-store`·고정 오류 redaction을 사용한다. broker·credential·write·migration 권한은 추가하지 않는다.
- 현재 원장의 `CASH_VOID`와 취소 대상 원본을 함께 제외해 외부 흐름과 내부 현금 정정을 과거 잔액에도 일관되게 반영한다. 이는 현재 알고 있는 사실로 과거를 다시 계산하는 회고 정책이다. 정정 시각에 새 출금을 만든 것으로 해석하지 않는다.
- 나노초를 유지한 동일 UTC 시각의 외부 입출금을 먼저 묶고 그 뒤 내부 사건을 기존 sequence 순서대로 적용한다. 같은 시각의 매매·비용이 초기 수익에서 사라지지 않는다. 서로 다른 소수초 사건은 같은 날짜여도 별도 경계다.
- 가격·직접 FX의 관측 시각은 경제 경계 이전, 수집·저장 시각은 보고서 cutoff 이전이어야 한다. 나중에 수집한 과거 자료를 쓸 수 있지만 거래 당시의 정보였다는 뜻은 아니다. 역환율·교차 환율·거래 현금 다리·현재 가격으로 누락을 보충하지 않는다. 과거 관측 하나라도 fixture이면 보고서 전체에 sample을 표시한다.
- `local_fixture`, 종목·symbol·currency·raw/unspecified 조정 기준과 적격 거래소의 모호성 검사를 기존 선택기에서 재사용한다. 적격 관측이 24시간을 초과하면 계산하지 않는다. 같은 경제 시각의 동률은 기존 첫 검증 append를 유지한다.
- 분할 뒤 수량에는 마지막 적용 `SPLIT`보다 **엄격히 나중**에 관측된 가격만 쓴다. 흐름과 분할 시각이 같으면 단위 해석을 추측하지 않는다. 현재 평가 API의 기존 의미는 바꾸지 않으며 TWR 기말에도 자체 분할 단위 검사를 추가한다.

## 자본·수치·무결성 계약

최초 흐름 이전의 통화별 현금과 보유 수량은 모두0이어야 한다. 첫 흐름 이후 자본과 이후 모든 중간 경계의 유입 전·후 자본은 양수여야 한다. 전액 출금 후0은 손실이 아니며 이번 정책에서는 계산 불가다. 재입금으로 새 투자 구간을 자동 연결하지 않는다. 직전 흐름 후 자본이 양수인 상태에서 **기말 자산만0**이면 실제 전액 손실 `−100%`를 계산할 수 있다. 음수 기말은 지원하지 않는다.

과거 projection은 통화별 현금과 종목/통화별 수량만 exact decimal로 계산한다. FIFO 원가·손익 재생을 별도로 만들지 않는다. 최종 통화별 잔액·보유 수량을 canonical snapshot과 0/누락 정규화 후 대조하고, 자체 기말 평가를 기존 current total과 다시 대조한다. 다른 잔액이 우연히 상쇄된 총액만으로 무결성을 인정하지 않는다. 불일치는 generic500이며 정상 계산 불가 상태로 숨기지 않는다.

구간 비율은 `big.Rat`으로 이어 곱하고 마지막 rate만 소수12자리 half-even으로 반올림한다. 큰 곱을 할당하기 전에 unreduced 분자·분모의 예상 bit 수를8192 이하로 제한한다. 출력은 canonical decimal128자 이내다. 1,000개 외부 흐름 경계 한도는 추가 계산의 범위이며 **필수 전체 원장 증명·읽기 메모리 한도**가 아니다. 취소·예산·자료 부족을0%로 바꾸지 않는다.

## 구현·화면·계약

- `services/core/portfolio_twr.go`: 같은 관찰의 증명·흐름 경계·연쇄 계산·읽기 전용 HTTP.
- `services/core/portfolio_twr_projection.go`: cash/quantity projection·두 시점 평가·분할 guard·잔액 대조·exact 연쇄 budget. `holding_valuation.go`의 기존 함수는 `(asOf, asOf)` wrapper로 유지한다.
- `contracts/openapi.json`과 `portfolio-twr-cases.json`, `golden-portfolio-twr*.json`: 닫힌 응답과 독립 수치·Go/Dart 공유 상태. 미계산 metadata는0이 아닌 null이다.
- Flutter는 기존 수익률 상세에서 별도 TWR 상세로 이동한다. XIRR의 연환산·후보 상태를 재사용하거나 섞지 않는다. 계산 가능·실제0·전액 손실·빈 기록·계산 불가·sample을 구분한다. 단일-flight·dispose guard와 새로고침 실패 시 이전 **전체 결과와 원래 cutoff/revision** 보존을 사용한다. 원시 오류 문장은 표시하지 않는다.
- 기존 Material/theme·한글 폰트·버튼·bounded scroll을 사용한다. light/dark·375px/가로·200% text/reduced motion을 자동 검사하며 물리 screen reader나 실제 브로커 동작의 증거로 확대하지 않는다.

## 검증과 미확인

- RED: 커밋 `2662bbb7d069cd5ca8bc86780769d24952957129`, [Core 40166668438](https://github.com/YouSangSon/Omni-Folio/actions/runs/40166668438), `TestPortfolioTWRNeutralizesAdditionalCapital`에서 실제 API404. 집중 실패 뒤 전체 검사는 건너뛰었다.
- Python 표준 `Fraction`의 exact 곱과 별도 Decimal100/half-even으로 수치11사례를 만들었다. PP의 두1% 구간·추가입금·부분출금·손실·실제0·비유한 소수·반올림 경계를 포함한다. 이는 가벼운 참조 데이터 계산이다.
- 집중 Go 회귀는 실제 SQLite 원장/HTTP·공유 응답, 정정·같은 시각/나노초 순서·두 시점 FX와24시간·분할 단위·중간 소진/기말 전액손실·잔액 대조·budget·손상/취소를 대상으로 한다. Flutter는 같은 실제 응답 골든과 닫힌 parser·GET 경로·실패 보존·dispose·상태 구분·화면 연결을 검사한다.
- 최종 원격 focused/full Core·Python/JSON·Dart format/analyze·Flutter 전체·새 web/Android release·package/source identity와 독립 금융 리뷰 결과는 아직 기입하지 않았다. 공유 호스트에서 Go/Flutter runtime·native build·DB stack은 실행하지 않는다.
- 네 언어 README의 구조·명령·참조 대응을 유지한다. 새 iOS binary·실기기·수동 screen reader·브라우저 runtime·VM·provider 시세·broker 성과·benchmark·소진 후 재투자 연결은 미검증 또는 범위 밖이다. 전체 Goal 완료를 뜻하지 않는다.
