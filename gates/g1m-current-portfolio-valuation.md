# G1.17 Current KRW Whole-Portfolio Valuation

Scope: cash와 현재 holding 시장가를 한 시점의 KRW 합계로 공개한다. provider 호출·credential·DB write/migration·historical FX 원가·PnL·성과·주문 권한은 추가하지 않는다.

## 서버 계약

- [x] `GET /v1/portfolio/current-valuation`은 query를 받지 않고 기준통화를 KRW로 고정한다. transaction 안에서 서버 UTC now를 정확히 한 번 읽어 caller/client clock이 cutoff authority가 되지 않는다.
- [x] 같은 read transaction에서 ledger snapshot/proof, 전체 FX series와 security-price series를 replay 검증한 뒤 cash와 holdings을 계산한다. 기존 cash/holding 상위 use case를 따로 호출해 snapshot을 갈라놓지 않는다.
- [x] 동일통화와 0 cash 외에는 `currency -> KRW` 방향의 24시간 이내 `local_fixture` 직접 FX만 사용한다. 역산·교차·보간·`FX_EXCHANGE` inference는 하지 않는다.
- [x] holding은 원장의 internal instrument/symbol/currency와 일치하는 단일 venue의 24시간 이내 `local_fixture` 가격만 사용한다. 현재 가격 × 수량만 환산하고 historical cost basis나 PnL은 계산하지 않는다.
- [x] required price/FX 하나라도 missing/stale/ambiguous이면 `cash_value`, `holdings_value`, `total_value`를 모두 null로 반환한다. aggregate가 있으면 세 currency는 KRW이고 `cash + holdings = total`을 exact decimal로 만족한다.
- [x] HTTP 응답은 account/instrument/observation/source ID, hash, cost basis, PnL, performance를 노출하지 않는다. durable ledger/FX/price corruption과 서버 clock 역전은 상세를 숨긴 일반화된 500으로 fail-closed한다.
- [x] 기존 `PortfolioSnapshot.valuation_status=unavailable`, cash-only valuation, native holding valuation 계약은 변경하지 않는다.

## Flutter 계약

- [x] closed parser는 14개 필드, canonical UTC/revision, 상태별 all-or-none, KRW currency와 exact `cash + holdings = total`을 독립 검증한다. REST 요청은 query 없는 고정 경로만 사용한다.
- [x] valuation은 status/snapshot을 막지 않는 optional read다. transport/parser 실패만 마지막 정상 응답과 원래 cutoff/revision을 유지하고, 정상 `unavailable` 응답은 이전 합계를 제거한다. 동시 refresh는 single-flight다.
- [x] Overview는 total을 먼저, KRW cash/holdings를 다음에 표시한다. local fixture는 `저장된 로컬 샘플 기준 · 실시간·증권사 값 아님`, revision mismatch는 `원장 갱신 전 저장 평가`로 표시한다.
- [x] unavailable은 부분 합계를 보여주지 않고 coverage 문제 수와 복구 방향을 표시한다. 320 logical px·200% text에서 overflow 없이 의미 있는 하나의 screen-reader label을 제공한다.

## 검증 근거

- Backend RED: focused test가 `currentPortfolioValuation undefined` compile failure로 시작했다.
- Backend GREEN: `go test ./... -run '^TestCurrentPortfolioValuation' -count=1` 통과. exact KRW 합산, empty/KRW-only, price/holding-only FX missing·24h+1ns stale, aggregate 억제, server clock 1회, query/method/private-field/OpenAPI 경계와 ledger/FX/price corruption redaction을 포함한다.
- Flutter RED: focused parser/API test가 missing model/method로, controller test가 missing optional state/method로 실패했다. 첫 320px widget 시도는 lazy-build 위치를 드러내 실제 요소까지 스크롤하는 회귀로 교정했다.
- Flutter GREEN: focused model/API 3개, controller known-good 교체 규칙, 320px·200% text·sample/broker 비권위 semantics가 통과했고 `flutter analyze`는 issue 0개다.
- 독립 read-only 리뷰는 runtime blocker를 찾지 않았고, holding-only FX branch가 cash 오류에 가려진 fixture와 corruption 500 공백을 지적했다. USD cash를 0으로 상쇄한 missing/stale FX 사례와 ledger/FX/price/cutoff corruption HTTP 사례로 보강했다.
- 최종 repository `make check`는 2026-09-08 exit 0: format, Go vet/analyze, resource preflight, Go 전체(315.884초), Flutter 106개, Python 34개, cleanup self-test와 JSON 계약 17개가 통과했다. 별도 종료 inventory에서도 이 작업 소유 listener·테스트 프로세스·임시 root가 남지 않았다. 이는 배포·live readiness 증거가 아니다.

## 연구 근거와 범위 경계

- [GIPS standards](https://www.gipsstandards.org/standards/gips-standards-for-firms/gips-standards-handbook-for-firms/)는 현재 fair-value 관찰과 기간 성과·external flow·transaction cost 계산을 구분한다. 따라서 G1.17은 한 시점 시장가이고 성과 차트의 수익률 근거가 아니다.
- [ECB reference-rate framework](https://www.ecb.europa.eu/stats/pdf/exchange/Frameworkfortheeuroforeignexchangereferencerates.en.pdf)는 reference rate의 base/quote 방향, source와 시각을 명시해야 하며 거래 가능한 실시간 가격으로 보아서는 안 된다는 경계를 뒷받침한다. G1.17 local fixture도 같은 이유로 sample/stale다.
- [WCAG 2.2 status messages](https://www.w3.org/WAI/WCAG22/Understanding/status-messages.html)는 refresh·불완전 상태를 focus 이동 없이 programmatically 전달하는 UI 근거다.

남은 범위: provider-backed freshness/source priority, 사용자 선택 기준통화, historical FX cost basis, time-weighted/money-weighted performance, 표시·법정통화 rounding, broker cash/valuation reconciliation, 실제 screen reader·물리기기 profile, browser release QA, Kubernetes/배포와 live-money authority.
