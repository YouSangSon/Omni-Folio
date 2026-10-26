# G1.30 같은 관찰의 TWR 계산점 차트와 표

상태: 구현·독립 리뷰·필수 원격 검증 진행 중 ([PR #59](https://github.com/YouSangSon/Omni-Folio/pull/59)). 일별 성과 이력이나 전체 Goal 완료를 뜻하지 않는다.

## 문제와 작동 원리

기존 누적 TWR은 마지막 비율만 보여 주므로 입출금을 제외한 구간 성과를 확인하기 어렵다. 100원이110원이 된 뒤100원을 넣고 다시10% 늘면 누적 수익률은21%다. 입금 후210원을 기존100원과 바로 비교하면 입금 자체를 수익으로 오인한다. 기존 exact TWR의 연쇄 곱을 그대로 관찰해 외부 흐름 경계의10%와 기말21%를 제공한다.

`GET /v1/portfolio/twr/checkpoints`는 빈 `?`를 포함한 query를 거절하고, 서버 clock 한 번·같은 read transaction·원장/가격/환율 proof에서 기존 `PortfolioTWR`와 계산점 배열을 반환한다. 새 저장 이력·migration·의존성은 없다. 기존 `/twr`와 benchmark의 scalar 계산·128자 최종 비율 제한은 유지한다. 정책은 `twr_cash_flow_checkpoints.v1`이며 응답의 세 필드는 `policy_version`, `report`, `points`다.

- 계산 성공: `boundary_count`개의 `external_flow`와 마지막 `valuation` 한 점, 최대1001점이다. 첫 점은 시작 시각·0, 마지막 점은 기말 시각·기존 최종 비율과 정확히 같다.
- 외부 흐름 시각은 canonical UTC nanosecond로 엄격히 증가한다. 마지막 흐름과 기말만 같은 시각일 수 있으며 순서는 흐름→기말이다. 흐름점은 같은 시각의 내부 매매·배당·비용 전, 기말은 모든 사건 반영 후다.
- `at`은 평가 경계이며 시장 관측 시각이 아니다. 기존 최대24시간 가격/환율 정책을 바꾸지 않는다. 실제 흐름 직전 시장가를 인증하지 않는다.
- empty/unavailable이면 수집한 부분 경로를 버리고 `points: []`다. sample·직접 입력 가격/환율 선언은 전체 보고서에서 독립적으로 유지한다.

## 수치 표현과 대안

일별 평가를 추측해 선을 채우거나 별도 projection을 저장하는 대신, 기존 계산에서 이미 평가한 지점만 노출한다. 점 개수·점선 안내·같은 응답의 표로 간격을 설명한다. 입금이 한 번이면 두 점이며 중간 일별 움직임은 알 수 없다. 이는 실제 일별 성과 이력을 대신하는 완료 선언이 아니다.

exact factor는 기존8192bit 예산 안에서 유지하고 각 공개 prefix를 소수12자리 half-even으로 독립 반올림한다. 반올림한 prefix를 다음 곱셈의 입력으로 쓰지 않는다. 양수 factor가 매우 작으면 공개 비율이−1로 반올림될 수 있으며, 이것만으로 실제 중간 자본 소진을 추론하지 않는다.

중간 비율은 최종 비율보다 클 수 있다. factor가16번10³²배가 된 뒤 기말 전액 손실이면 중간 값은512자리지만 최종 값은−1이다. prefix는8192bit 분자의 약2467자리와 소수12자리·부호를 포함하는2500자로 제한하고 기존 scalar128자는 그대로 둔다. collector의 예상 밖 bound 오류는 일반500이며 scalar의 계산 한도 초과로 바꾸지 않는다. 서버는 실제 encoded body와 클라이언트는 실제 수신 stream을3MiB로 제한한다.

Flutter는 canonical decimal·필드 집합·개수·위상·시각 범위·첫/끝 일치를 검증한다. 비율과 nanosecond 시각을 BigInt로 비교하고, `(offset * 10^9 ~/ span) / 10^9`의0..1 좌표만 double로 바꾼다. 큰 원본을 먼저 double로 바꾸지 않아 overflow를 피한다. 동일 시각·평평한 값은 중앙 좌표로 처리하며 정렬/중복 제거로 사건을 합치지 않는다.

## 화면과 실패 흐름

TWR 상세→`입출금 구간별 성과 보기`→`같은 계산점 표로 보기`로 이동한다. 하나의 불변 응답을 차트·표가 공유하며 표 이동에 재조회하지 않는다. 표는 lazy이고 큰 값은 개별 확장한다. 차트에는 요약 semantics와 표 대안, 상승/하락 텍스트·기말 테두리를 제공한다. 48px target·200% 글자·light/dark·reduced-motion을 유지한다.

40초 대기 제한은 AbortableRequest를 종료 신호로 보낸다. 읽기 실패·잘못된 응답은 고정 공개 오류와 이전 전체 관찰·원래 cutoff를 유지한다. 성공한 unavailable은 이전 차트를 대체한다. dispose 이후 상태를 바꾸지 않으며 자동 polling은 없다.

## 구현과 검증

- Go: `services/core/portfolio_twr.go`, `portfolio_twr_checkpoints.go`, `portfolio_twr_checkpoints_http_test.go`, `portfolio_twr_math_test.go`.
- Flutter: `apps/client/lib/models.dart`, `api.dart`, `portfolio_twr_checkpoint_plot.dart`, `portfolio_twr_checkpoints_page.dart`, `portfolio_twr_page.dart` 및 `test/portfolio_twr_checkpoints_test.dart`. 모든 명시적 OmniApi fake와 integration profile fake를 갱신한다.
- 계약: `contracts/openapi.json`, `golden-portfolio-twr-checkpoints.json`의 기존7상태, `portfolio-twr-cases.json`의 독립 Fraction14사례·prefix 기대값.

실제 RED head `fb769884b0e1d89c042fef45f4a5fe80dc0a2abb`, [Core run40300745647](https://github.com/YouSangSon/Omni-Folio/actions/runs/40300745647), job121497719247에서 새 경로 HTTP404로 실패했다. full/source identity는 skipped다. 구현 후 필수 Core 전체·Python/JSON, Client 집중/전체·분석·web/ARM64·권한/소스 일치는 아직 실행 전이다. 로컬 runtime 검사는 실행하지 않는다.

수치 fixture는 Python 표준 라이브러리 Fraction과 정수 quotient/remainder half-even으로 기존 최종 값과 독립 prefix를 확인한다. 실제 HTTP producer 비교, 이전 scalar 동일성, 같은 시각/1ns, 늦은 과거 입력 실패의 부분 폐기, 반올림−1 후 회복,512자리 prefix,1000/1001경계와 strict consumer·stream bound·UI 회귀를 별도로 검사한다. 독립 설계/구현 리뷰에서 표 이동 시 출처·sample 경고가 사라지는 문제를 보완했다. 표 header에도 세 독립 경고를 표시하고 실제 이동 후 유지되는지 검사한다. 최종 수정 리뷰와 실제 CI 결과는 완료 후 기록한다.

## 출처와 한계

[Portfolio Performance General Method](https://help.portfolio-performance.info/en/concepts/performance/time-weighted/)의 외부 흐름별 기하 연결 개념을 참고했다. 해당 구현의 일별 평가·하루 시작 입금/끝 출금 관례는 도입하지 않았고 코드·자산은 차용하지 않았다. 기존 정책의 출처 owner는 [G1.24](g1t-portfolio-twr.md)다.

실제 시장 자료/사용권·제공사 정확성·일별 이력·새 iOS/실기기·수동 screen reader·VM/배포는 이 자동 합성 검증의 근거가 아니다. KRW·USD benchmark 지원과 실제 자료 확보의 구분은 [기존 source owner](g1u-benchmark-series.md#benchmark-source-policy)를 따른다. 예측 확인: 두 구간이 각각10%이고 중간에 입금했다면 최종21%가 유지되어야 한다.
