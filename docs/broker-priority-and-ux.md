# Broker Priority and Product UX

기준일: 2026-08-24 KST

## 결정

1. **키움 REST API를 첫 실행 브로커로 완성한다.** 국내주식 read-only 계좌·잔고·미체결·차트부터 시작하고, 키움 모의투자에서 주문 상태와 복구를 검증한 뒤에만 실전 주문을 검토한다.
2. **토스증권 Open API를 두 번째 브로커로 추가한다.** 키움 어댑터와 canonical model이 먼저 통과해야 하며, 토스 전용 분기를 원장·차트·주문 코어에 넣지 않는다.
3. **제품 경험은 토스증권처럼 쉽고 차분하게 만든다.** 토스 화면이나 상표를 복제하지 않고, 쉬운 용어·강한 정보 위계·점진적 상세 공개·한 화면 한 결정·접근성을 Omni Folio 고유 디자인으로 구현한다.

거래 공급자와 화면 언어를 분리한다. 키움 API 장애나 토스 API 추가가 Flutter 정보구조를 바꾸지 않고, Flutter가 어느 브로커의 secret이나 원본 응답도 알지 못하게 한다.

## 공식 API 기준선

| 항목 | 키움 | 토스증권 |
|---|---|---|
| 공식 문서 | [REST API 포털](https://openapi.kiwoom.com/guide/apiguide) | [Open API 가이드](https://developers.tossinvest.com/docs), [source index](https://developers.tossinvest.com/llms.txt) |
| REST | `https://api.kiwoom.com`; 모의 `https://mockapi.kiwoom.com` | `https://openapi.tossinvest.com` |
| WebSocket | `wss://api.kiwoom.com:10000`; 모의 도메인 별도 | `wss://openapi-ws.tossinvest.com/ws/v1`; [AsyncAPI](https://openapi.tossinvest.com/openapi-docs/latest/asyncapi.json) |
| 인증 | OAuth client credentials, `appkey`/`secretkey` | OAuth client credentials, 계좌 API는 `X-Tossinvest-Account` 추가 |
| 범위 | 국내·미국 계좌, 시세, 차트, 주문, 실시간 | 국내·미국 계좌, 시세, 차트, 주문·조건주문, 실시간 |
| 안전한 검증 환경 | 운영/모의 도메인과 키 분리; 국내 모의는 KRX만 지원 | 공식 [OpenAPI](https://openapi.tossinvest.com/openapi-docs/latest/openapi.json)에서 별도 주문 sandbox는 확인하지 못함 |
| 호출 제한 | 오류코드 `1700`~`1702`로 API·전체·그룹 유량 초과를 알리지만 공개 고정 quota는 재확인하지 못함 | 그룹별 제한과 응답의 `X-RateLimit-*`, 429의 `Retry-After`를 런타임 기준으로 사용 |

공식 명세는 바뀔 수 있다. 구현 시 저장한 문서 복사본을 진실로 두지 않고 위 포털, OpenAPI, AsyncAPI와 실제 응답 헤더를 다시 확인한다. 키움 공식 GitHub 자료는 참고만 하며 [키움 전용 제한 라이선스](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/main/LICENSE.md) 때문에 샘플 코드나 명세를 이 저장소로 복사·수정·재배포하지 않는다.

### 확인된 키움 K0 HTTP 계약

- OAuth는 운영/모의의 `POST /oauth2/token`에 `client_credentials`, `appkey`, `secretkey`를 보내고 `expires_dt`, `token_type`, `token`을 받는다. 공식 문서에는 token lifetime과 timezone, 권한 scope가 명시돼 있지 않다. ([au10001](https://openapi.kiwoom.com/m/guide/apiguide/a1/au10001))
- `ka00001`, `kt00018`, `ka10075`는 `POST /api/dostk/acnt`를 공유하며 `authorization: Bearer ...`, `api-id`를 요구한다. `cont-yn=Y`이면 응답의 `cont-yn`과 `next-key`를 다음 요청 header에 전달한다. ([ka10075](https://openapi.kiwoom.com/m/guide/apiguide/08/ka10075), [kt00018](https://openapi.kiwoom.com/m/guide/apiguide/08/kt00018))
- HTTP status만으로 성공을 판단하지 않고 body의 `return_code`도 확인한다. 공개 오류표는 인증·환경 불일치와 유량 초과 코드를 제공하지만 page size와 고정 quota 수치는 확인되지 않았다. ([오류코드](https://openapi.kiwoom.com/m/errorcode/errorCodeView))
- 국내 모의투자 계좌·주문은 KRX만 지원한다. ([모의투자 안내](https://openapi.kiwoom.com/m/intro/mockInvestInfo))
- 미확인 quota에서는 자동 retry/backoff를 추측하지 않는다. 읽기 호출은 안전한 오류로 끝내고, 실제 모의 응답과 제한을 측정한 뒤 bounded retry와 limiter를 추가한다.

## 구현 순서

### K0 — 계약과 secret 경계

- Go 서버만 OAuth secret과 access token을 읽는다. 로컬은 OS keychain, cloud는 secret manager를 사용한다.
- Flutter, Python, `.env`, Git, 로그, 오류 응답에는 app key, secret, token, 계좌 원문을 두지 않는다.
- 키움 OAuth의 read-only scope는 공식 문서에서 확인되지 않았으므로 credential 이름만 믿지 않는다. 현재 프로세스는 account read `ka00001`/`kt00018`/`ka10075`와 chart read `ka10080`/`ka10081`만 각각 고정 path로 허용하고 submit API와 route를 갖지 않는다.
- provider별 capability, rate-limit, pagination/continuation, 오류 envelope, symbol/market mapping은 공식 예제를 복사하지 않은 합성 contract fixture로 검증한다.
- 원본 금액·수량의 부호 규칙을 경계에서 canonical decimal string으로 변환한다.

### K1 — 키움 read-only

- 계좌번호 `ka00001`, 계좌평가잔고 `kt00018`, 미체결 `ka10075`를 canonical account/position/order-read model로 정규화한다.
- 일봉 `ka10081`, 분봉 `ka10080`과 실시간 체결 `0B`, 우선호가 `0C`, 호가잔량 `0D`에 freshness, 재연결, 재구독 상태를 붙인다.
- broker snapshot과 local ledger의 차이를 읽기 전용 reconciliation report로 보여 준다. 자동 보정하지 않는다.

#### K1 candle synthetic contract

- 구현 범위는 credential-free `POST /api/dostk/chart`의 `ka10080`/`ka10081` 합성 경계다. KRX 여섯 자리 symbol과 `1d`, `1/3/5/10/15/30/45/60m`만 받는다. 필드와 interval 기준은 현재 [키움 공식 chart 명세](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/main/kiwoom_docs/%EC%B0%A8%ED%8A%B8.md)에서 확인했다.
- signed price는 magnitude로, price/OHLC는 exact decimal로, volume은 nonnegative decimal로 정규화한다. descending page는 UTC ascending으로 바꾸고, 같은 timestamp·같은 값 overlap은 dedupe하며 값 충돌은 거절한다. 반환은 newest 500개로 제한하되 cap 경계 overlap 검증을 위해 다음 한 page까지만 확인한다.
- `upd_stkpc_tp=1`은 내부 `provider_adjusted` provenance이며 adjustment event의 정확성을 뜻하지 않는다. 공식 문서가 candle timestamp timezone을 정하지 않아 Asia/Seoul은 운영 가정일 뿐이다.
- 이 slice는 credential, broker request, live/current/freshness, public endpoint, persistence, realtime, adjustment event correctness, reconciliation, 또는 order capability를 증명하지 않는다. public market route는 계속 `local_fixture`/`sample`/`stale`다.
- 실행 증거는 [`../gates/g4c-kiwoom-candle-contract.md`](../gates/g4c-kiwoom-candle-contract.md)에 기록한다.

### K2 — 키움 모의주문

- 주문 intent, pre-trade risk, durable idempotency key, broker submit, ack/fill/cancel/reject, reconciliation 순서를 하나의 상태 머신으로 검증한다.
- timeout 뒤 결과가 불명확하면 같은 주문을 다시 보내지 않고 broker 조회로 상태를 확정한다.
- 실제 키와 실전 submit 경로는 owner 승인 전까지 꺼 둔다.

### T1 — 토스 read-only

- 키움 K1이 통과한 뒤 계좌 목록, 보유 주식, 시세·캔들, 실시간 체결·호가를 같은 canonical contract에 연결한다.
- 허용 IP, `X-Tossinvest-Account`, 동적 rate-limit header, WebSocket 연결·구독 한도를 provider 내부에서 처리한다.

### T2 — 토스 주문

- 공식 별도 sandbox가 확인되기 전에는 주문 계획, risk 결과, 예상 비용과 shadow intent까지만 자동 검증한다.
- 실제 주문 검증 방법과 rollback/reconciliation 절차를 별도로 승인한 뒤에만 토스 submit을 연다.

## 토스에서 가져올 UX 원칙

- **한 화면 한 결정:** 홈은 신뢰 상태와 자산 요약, 종목 화면은 가격과 차트, 주문 화면은 주문 확인만 우선한다.
- **쉬운 말 먼저:** `ledger_revision`, TR 코드, provider error는 기본 화면에서 숨기고 “마지막으로 확인한 시각”, “어느 계좌가 늦는지”, “다음 행동”으로 번역한다.
- **점진적 상세 공개:** 수수료·세금·lot·원본 이벤트·진단 ID는 `자세히`에서 보되 삭제하지 않는다.
- **국내·미국의 같은 흐름:** 통화, 시장 시간, 주문 capability 차이는 명시하되 화면 구조는 유지한다.
- **차트 가독성:** 가격·거래량·평균단가·체결 marker의 위계를 분명히 하고 동일 기간의 텍스트 요약과 표 대안을 제공한다. 토스증권도 공식 채용 설명에서 TradingView 기반 차트와 가격·지표·거래량 위계를 핵심으로 둔다.
- **주문은 안심 흐름:** 매수/매도 → 수량/가격 → 예상 금액/비용/위험 → 최종 확인 → 접수 상태를 짧고 되돌릴 수 있게 만든다.
- **접근성 기본값:** 48dp target, 200% text, VoiceOver/TalkBack, 키보드, 명암, reduced motion을 별도 모드가 아니라 기본 계약으로 둔다.

이 원칙은 토스증권 공식 제품이 강조하는 복잡성 축소와 직관적 투자 경험을 참고한다. ([토스증권 UX의 미래](https://toss.im/tmc-25/sessions/design/ui-design-20), [토스증권 공식 제품 소개](https://p.tossinvest.com/ko), [차트 Product Designer 역할](https://toss.im/career/job-detail?job_id=7659895003))

## 첫 어댑터 완료 조건

- 같은 키움 응답 페이지를 반복 동기화해도 원장 이벤트가 중복되지 않는다.
- 401/403/429/5xx, token 만료, pagination 중단, WebSocket 재연결 뒤에도 기존 snapshot이 보존된다.
- 계좌·잔고·미체결·차트 timestamp와 출처가 Flutter에 표시되고 stale/partial 상태를 재현할 수 있다.
- broker snapshot과 ledger 차이를 종목·현금 단위로 설명하며 자동으로 덮어쓰지 않는다.
- credential redaction, read API-ID allowlist, 모의/실전 분리와 로그 누출 방지 테스트가 통과한다. provider가 실제 OAuth scope를 제공할 때만 scope도 함께 강제한다.
- 실전 주문은 여전히 비활성이다.
