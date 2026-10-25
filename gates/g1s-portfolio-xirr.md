# G1.23 입출금과 현재 자산을 연결하는 XIRR

상태: 구현·독립 리뷰 보완 중, 최종 원격 검증 전. [PR #44](https://github.com/YouSangSon/Omni-Folio/pull/44). 이 단위가 전체 Phase A나 Goal의 완료를 뜻하지 않는다.

## 문제와 사용자 가치

100원을 넣고 나중에 100원을 더 넣어 잔액이 200원이 된 것은 투자 수익 100%가 아니다. 입출금의 금액과 날짜를 따로 반영해야 한다. XIRR은 투자자 관점의 입출금과 마지막 자산 가치의 현재가치 합이 0이 되는 연율이다. 배당·매매·비용을 외부 입출금으로 다시 세면 성과가 이중 계산된다.

홈의 자산 요약 뒤 `입출금을 반영한 수익률 보기`에서 상세를 연다. 화면 진입 전에는 추가 성과 조회를 실행하지 않는다. 요청은 `GET /v1/portfolio/xirr`이며 query·writer·브로커 호출·credential·schema migration은 추가하지 않는다.

## 원리·대안·출처

- [Microsoft XIRR 공식 설명과 예제](https://support.microsoft.com/en-us/excel/functions/xirr-function)는 비정기 현금흐름, 날짜 정수화와 365일 기준, 기본 초기 추측 10%를 명시한다. 공개 예제의 다섯 흐름을 독립 고정밀 참조값 약 `0.373362533519`와 비교한다. Excel 전반의 계산 경로나 해 선택을 복제했다고 주장하지 않는다.
- [Portfolio Performance의 money-weighted 설명](https://help.portfolio-performance.info/en/concepts/performance/money-weighted/)에서 포트폴리오 경계의 외부 흐름과 내부 매매·배당의 구분을 참고했다. 3년·추가입금 예제는 연율 10%로 교차 확인한다. [같은 프로젝트의 TWR 설명](https://help.portfolio-performance.info/en/concepts/performance/time-weighted/)처럼 TWR에는 구간별 평가가 필요하므로 현재 자산만으로 TWR을 대신 만들지 않는다.
- 실제 [IRR.java](https://github.com/portfolio-performance/portfolio/blob/2a0ef52a93882c0845596b2c6b834e54f592b7ec/name.abuchen.portfolio/src/name/abuchen/portfolio/math/IRR.java)의 초기 추측·반분/수치 해법 구성을 확인했다. [EPL-1.0](https://github.com/portfolio-performance/portfolio/blob/2a0ef52a93882c0845596b2c6b834e54f592b7ec/LICENSE)이며 개념만 참고하고 코드를 차용하지 않았다. 이 앱의 solver와 아래 증거는 별도 구현이다.

단순 잔액 증가율은 입출금을 수익으로 오인한다. 추측 하나로 찾은 모든 값을 확정 수익률로 표시하면 복수 해 가능성이나 무근 곡선의 작은 잔차를 놓친다. 모든 지수다항식의 해를 인증하는 새 수학 프레임워크 대신, 일별 흐름의 부호 변화가 한 번이면 유일근을 반분법으로 찾고 나머지는 명시적으로 구분하는 최소 구조를 선택했다. 현재 구조·표준 라이브러리만 사용하며 새 의존성은 없다.

## 데이터와 시간 계약

`portfolio_xirr.go`는 한 read transaction에서 schema·replay transition·canonical ledger event·저장 관측 proof와 현재 KRW 평가를 확인한다. 서버 clock은 한 번만 읽으며 추출한 `currentPortfolioValuationTx`는 commit하거나 clock을 다시 읽지 않는다. 기존 현재 평가 API의 공개 응답은 유지한다. 기존 현재 평가 helper 자체를 전체 transition proof로 표현하지 않으며 새 XIRR가 그 검증을 추가한다.

현재 원장을 정정한 회고 보고서다. 유효한 `DEPOSIT`/`WITHDRAWAL`의 부호를 투자자 관점으로 뒤집는다. `CASH_VOID`는 같은 계좌의 원본 외부 흐름을 제외하고, 취소 행을 가상의 새 출금으로 만들지 않는다. 내부 거래·배당·비용·환전은 기말 자산에만 반영한다. 기말 자산과 흐름은 별도 API 결과를 클라이언트에서 합치지 않는다.

외화 흐름은 사건마다 정확한 KRW 금액으로 먼저 바꾼 뒤 UTC 날짜별로 합산한다. 같은 날짜의 외화 금액을 먼저 상계하면 서로 다른 시각의 환율 효과를 잃는다. 환율은 정확한 `currency -> KRW` 방향의 `local_fixture` 관측만 사용한다. 동률이면 검증된 최초 append를 유지한다. 관측 시각은 사건 발생 이전, fetched/recorded는 보고서 cutoff 이전이어야 하며 발생 시각 대비 관측 나이는 24시간을 포함한다. 나중에 수집한 과거 자료를 사용하는 회고 정책이므로 거래 시점의 no-lookahead 보증과 다르다. inverse·cross·현재 환율 대입이나 환전 cash leg 추론은 없다.

정확한 소수 금액을 유지하며 원장 시각은 변경하지 않는다. XIRR의 날짜 투영만 UTC calendar day와 actual/365를 사용한다. 하루 안의 시각 차이는 연율 계산에서 제외하며, 날짜·가격/환율 원문이나 원장 cutoff의 나노초 검증을 완화하지 않는다. 전 구간 가격 경로나 역사적 원가를 계산하는 API는 아니다.

## 수치 해법과 공개 상태

정확히 합산한 일별 0 흐름을 제거한 뒤 부호 변화를 센다. `r > -1`에서 한 번의 부호 변화는 유일한 해를 갖는다. 이 경로는 계산 가능한 log-rate 구간의 bracket/bisection을 사용한다. 그 밖에는 log-domain Newton의 10% 초기 추측을 위치 후보로만 사용하고, 주변의 신뢰 가능한 부호 변화 구간을 확인해 다시 반분한다. 접하는 해나 거의 근처럼 보이는 무근 함수를 인증하지 못하면 `numerical_limit`이며 “근 없음”을 뜻하지 않는다. 다른 해가 없음을 증명하지 않은 후보는 `candidate_rate`에만 제공한다.

항의 금액과 지수 크기를 함께 log로 정규화하고 보상 합산을 사용한다. 반복·finite·정규화 잔차·취소를 확인하며 최대 10,000개 비영 일별 흐름을 계산한다. 이 한도는 새 solver의 작업 예산이며 전체 ledger proof의 메모리/시간 상한이 아니다. 기본 통화 금액은 exact rational이고 수익률만 근삿값이다.

최종 ratio를 12자리 half-even으로 정규화한 뒤 **공개할 값 자체**의 상대 NPV 잔차도 확인한다. `1+rate`는 exact rational로 더한 뒤 log로 보내 −1 부근의 뺄셈 손실을 피한다. 공개 정밀도로 정규화 잔차 `1e-10`을 유지하지 못하면 값을 숨긴다. 예를 들어 연간 `-100, +0.00000000006`은 12자리로 표시하면 현재가치가 달라져 계산 불가로 남는다. 반대로 실제 0%는 문자열 `0`으로 제공한다. 기말 0이나 한 방향 흐름을 0%로 채우지 않는다.

- `calculated`: `annual_rate`, `uniqueness=unique`; 대표 숫자로 표시한다.
- `candidate`: `candidate_rate`, `uniqueness=not_proven`; 참고 상세에서만 표시하고 다른 해 가능성을 알린다. 부호 변화가 많다는 사실만으로 실제 복수 해가 존재한다고 단정하지 않는다.
- `empty`/`unavailable`: rate는 모두 `null`이다. 현재 평가나 과거 FX가 부족하면 미계산 순입금액·통계도 `null`로 남기며 0과 구분한다.

관측에 예시 자료가 하나라도 쓰이면 보고서 전체가 sample이다. 과거 FX만 sample이고 기말이 순수 KRW cash여도 표시를 잃지 않는다. 계좌·원본/관측/receipt/종목 내부 ID는 응답에 넣지 않는다. 기존 JSON writer의 `no-store`와 고정 오류 redaction을 사용한다.

## Flutter와 실패 보존

`PortfolioXirr` parser는 정책·closed fields·calendar·state 조합·finite canonical rate·미계산 통계를 검증한다. 상태가 맞지 않는 새 응답은 이전 전체 결과와 원래 revision/cutoff를 유지한다. 단일-flight 새로고침과 dispose guard를 사용한다. 확정 수익률·후보·실제 0·계산 불가를 서로 대체하지 않는다. 큰 연율도 exact string percent로 표시하며 기존 research 응답의 128자 제한은 별도로 유지한다.

기존 Material/token·52px 동작·양쪽 theme·375px/가로·200% text/reduced motion·키보드 접근을 사용한다. 값의 근거와 후보는 상세로 점진 공개한다. 위젯 fake는 실제 브로커·실기기·화면 읽기 기기 실행의 증거가 아니다.

## 검증과 미확인

- RED [40163504930](https://github.com/YouSangSon/Omni-Folio/actions/runs/40163504930): 실제 원장 seed 뒤 보고서 route가 404임을 확인했다. focused 실패 후 전체 검사는 실행하지 않았다.
- `portfolio-xirr-cases.json`: Python Decimal80의 직접 power NPV 참조와 공식 예제, 정확한 대수 사례를 포함한다. 이는 로컬 fixture 생성 계산이며 Go·DB·Flutter runtime을 로컬에서 실행한 것은 아니다. 상대 비교 허용치는 `1e-10`이다.
- `golden-portfolio-xirr.json`와 nullable-state goldens를 실제 Go HTTP 응답에 대조하고 같은 JSON을 Dart parser가 읽는다. 금액·정정·경제/지식 FX 시점·24시간 경계·사건별 환산·proof 손상·단일 clock·후보·nullable fields·표시/실패 보존을 검사한다.
- GREEN: 최종 원격 Core·Client 검사와 새 web/ARM64 release, package/source identity 확인 전이다. 공유 호스트의 로컬 runtime·native build·DB stack은 실행하지 않는다.
- TWR·benchmark·historical cost/PnL·provider feed·실제 브로커 수익률·새 iOS 빌드·실기기·수동 screen reader·브라우저 runtime·VM 검증은 완료하지 않았다. 전체 Goal은 계속 열려 있다.
