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
| 호출 제한 | 공식 FAQ 기준 TR·token별 운영 5회/초, 모의 1회/초. 전체·그룹 제한, burst window와 `Retry-After`는 미확인 | 그룹별 제한과 응답의 `X-RateLimit-*`, 429의 `Retry-After`를 런타임 기준으로 사용 |

공식 명세는 바뀔 수 있다. 구현 시 저장한 문서 복사본을 진실로 두지 않고 위 포털, OpenAPI, AsyncAPI와 실제 응답 헤더를 다시 확인한다. 키움 공식 GitHub 자료는 참고만 하며 [키움 전용 제한 라이선스](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/main/LICENSE.md) 때문에 샘플 코드나 명세를 이 저장소로 복사·수정·재배포하지 않는다.

### 확인된 키움 K0 HTTP 계약

- OAuth는 운영/모의의 `POST /oauth2/token`에 `client_credentials`, `appkey`, `secretkey`를 보내고 `expires_dt`, `token_type`, `token`을 받는다. 공식 문서에는 token lifetime과 timezone, 권한 scope가 명시돼 있지 않다. ([au10001](https://openapi.kiwoom.com/m/guide/apiguide/a1/au10001))
- `ka00001`, `kt00018`, `ka10075`, `kt00009`는 `POST /api/dostk/acnt`를 공유하며 `authorization: Bearer ...`, `api-id`를 요구한다. `cont-yn=Y`이면 응답의 `cont-yn`과 `next-key`를 다음 요청 header에 전달한다. ([ka10075](https://openapi.kiwoom.com/m/guide/apiguide/08/ka10075), [kt00009](https://openapi.kiwoom.com/m/guide/apiguide/08/kt00009), [kt00018](https://openapi.kiwoom.com/m/guide/apiguide/08/kt00018))
- HTTP status만으로 성공을 판단하지 않고 body의 `return_code`도 확인한다. 공개 오류표는 인증·환경 불일치와 유량 초과 코드를 제공하지만 page size, 전체·그룹 limit과 burst window는 확인되지 않았다. ([오류코드](https://openapi.kiwoom.com/m/errorcode/errorCodeView))
- 국내 모의투자 계좌·주문은 KRX만 지원한다. ([모의투자 안내](https://openapi.kiwoom.com/m/intro/mockInvestInfo))
- 공식 FAQ는 TR·token별 운영 초당 5건, 모의 초당 1건을 명시한다. 전체·그룹 limit, burst window와 `Retry-After`는 문서화되지 않았으므로 자동 retry/backoff를 추측하지 않고 실제 모의 응답을 관찰한 뒤 limiter를 추가한다. ([호출 횟수 FAQ](https://bbn.kiwoom.com/bbs/VBbsNoticeNOPAFPagingDetailView?seqid=41))

## 구현 순서

### K0 — 계약과 secret 경계

- Go 서버만 OAuth secret과 access token을 읽는다. 로컬은 OS keychain, cloud는 secret manager를 사용한다.
- Flutter, Python, `.env`, Git, 로그, 오류 응답에는 app key, secret, token, 계좌 원문을 두지 않는다.
- 키움 OAuth의 read-only scope는 공식 문서에서 확인되지 않았으므로 credential 이름만 믿지 않는다. 현재 프로세스는 account read `ka00001`/`kt00018`/`ka10075`/`kt00009`와 chart read `ka10080`/`ka10081`만 각각 고정 path로 허용하고 submit API와 route를 갖지 않는다. `kt00009`는 internal dated scan에서만 사용한다.
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

#### G4Q latest-trade synthetic contract

- 공식 예제 commit [`9180deb`](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/commit/9180debf7aea0074715dd8f7a15af432afbfc403)의 [`ka10079` sample](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/9180debf7aea0074715dd8f7a15af432afbfc403/examples/%EA%B5%AD%EB%82%B4%EC%A3%BC%EC%8B%9D/%EC%B0%A8%ED%8A%B8/get_domestic_stock_tick_chart.py)에 고정된 `/api/dostk/chart`, `stk_cd`, `tic_scope`, `upd_stkpc_tp`, `cur_prc`, `cntr_tm` 경계만 사용한다.
- 첫 page의 newest 1틱을 exact canonical 가격과 provider 체결 시각으로 정규화한다. 모든 받은 row가 유효하고 내림차순이어야 하며, 초 단위 시각이 같은데 가격이 다르거나 provider 시각이 fetch 시각보다 미래면 거절한다.
- 공식 sample이 timezone, durable execution identity와 adjustment provenance를 제공하지 않으므로 기존 Asia/Seoul 운영 가정만 재사용하고 persistence용 source ID나 adjustment 값을 만들지 않는다.
- credential, 외부 broker request, public API/UI, scheduler, DB mutation, live/current/freshness와 holding valuation 승격은 포함하지 않는다. 실행 증거는 [`../gates/g4q-kiwoom-latest-trade.md`](../gates/g4q-kiwoom-latest-trade.md)에 기록한다.

#### G4D price-adjustment consumer contract

- provider-neutral `MarketDataCandles`는 `price_adjustment`를 필수로 하고 `unspecified`와 `provider_adjusted`만 허용한다.
- 현재 공개 route는 local fixture 전용이므로 `price_adjustment=unspecified`만 반환하며 빈 값, `provider_adjusted`, 미지원 값은 500으로 fail-closed한다. Kiwoom K1을 공개 route에 연결했다는 뜻이 아니다.
- Flutter는 local fixture가 sample·stale이고 source timestamp와 provenance issue를 갖춘 경우만 받으며 가격 기준을 쉬운 말로 표시한다. `provider_adjusted`도 공급자 요청 provenance일 뿐 “수정주가 검증 완료”로 표현하지 않는다.
- 실행 증거는 [`../gates/g4d-market-data-price-adjustment.md`](../gates/g4d-market-data-price-adjustment.md)에 기록한다.

#### G4H known-good snapshot persistence

- 기존 credential-free 합성 `KiwoomSnapshot` 중 `complete=true`인 KRX/KRW 결과만 identity, canonical UTC/decimal, unique ascending position/open-order 계약을 다시 검증해 Go/SQLite에 저장한다.
- 한 transaction에서 현재 ledger revision과 `account-main`의 KRX/KRW 종목 수량을 읽고 raw snapshot과 `broker - ledger` exact quantity diff reconciliation을 분리 insert한다. 이는 비교 evidence이며 원장을 자동 보정하지 않는다.
- 같은 fetched-at와 같은 payload는 raw snapshot을 중복 저장하지 않는다. 같은 raw snapshot·같은 ledger revision replay는 같은 reconciliation을 반환하고, ledger revision이 바뀌면 새 reconciliation만 append한다. payload 충돌, 불완전 snapshot, 잘못된 generated ID는 전체 거절하고 최근 fetched-at의 known-good를 유지한다.
- 현재 schema v10/backup v6는 ledger event, direct FX observation, broker snapshot, broker reconciliation, execution-authority event, risk reservation과 synthetic/paper 주문을 insert-only로 보호하고 각 state digest/count, canonical row hash와 restore schema/trigger를 검증한다.
- credential, 실제 broker request, scheduling, freshness/timezone, 현금·평가금액 reconciliation, public API/UI 또는 risk authority는 증명하지 않는다. 실행 증거는 [`../gates/g4h-kiwoom-known-good-snapshot.md`](../gates/g4h-kiwoom-known-good-snapshot.md)에 기록한다.

### K2A — 내부 합성 주문 상태 로그

- Go 내부에서만 `kiwoom`/`synthetic`/`KRX`/`KRW` 지정가 매수·매도 intent를 기록한다. client-order ID, event ID와 provider execution alias의 동일 payload replay만 허용하고 충돌은 거절한다.
- risk verdict 순서 뒤 submit dispatch를 append하고 즉시 `SUBMIT_UNKNOWN`으로 보존한다. 같은 주문의 blind resubmit과 해당 계좌의 신규 submit을 차단하지만, 이미 알려진 open order의 cancel은 위험 축소 경로로 허용한다.
- explicit ack/reject/fill/cancel event만 상태를 확정한다. provider에서 찾지 못했다는 사실만으로 unknown을 성공·실패로 바꾸지 않는다.
- 현재 SQLite schema v10/backup v6는 append-only order log의 exact row hash/metadata, replay, STRICT/UNIQUE/FK/rowid sequence/trigger를 source와 restore 후보에서 계속 검증한다.
- K2A leaf 자체는 risk verdict의 **순서만** 증명했다. 이후 K2C가 별도 credential-free 고정 BUY policy와 lease/fencing을 추가했지만 broker network, credential, public route/UI, production risk, 시장가·정정 또는 ledger reconciliation 증거는 아니다. K2A 실행 증거는 [`../gates/g4e-kiwoom-synthetic-order-state.md`](../gates/g4e-kiwoom-synthetic-order-state.md)에 기록한다.

### G4I/K2C — 내부 합성 execution authority

- 권한 기록이 없으면 신규 submit을 차단한다. 수동 arm/halt는 account별 append-only full-state event를 남기며 halt는 fencing token을 증가시키고 현재 lease를 무효화한다.
- 각 Go service는 config나 DB에서 재사용하지 않는 crypto-random owner를 만들고, 30초 SQLite lease와 단조 증가 fencing token을 소유한 경우에만 신규 합성 BUY를 승인한다.
- 고정 `credential_free_buy_v1`은 `005930`·`000660`, `BUY/LIMIT/KRX/KRW`, 수량 10주, 주문 limit notional 100만 원, 계좌 active reservation 100만 원으로 제한한다. 이는 production exposure/cash 한도가 아니다.
- immutable reservation과 reservation-bound `RISK_APPROVED`·`SUBMIT_DISPATCHED`를 한 transaction에 연속 기록하고 DB trigger가 일반 event 경로와 직접 SQL 우회를 차단한다. backup v6는 authority/reservation digest/count와 replay/schema/trigger를 검증한다.
- 실제 broker request·credential·송신 증거, SELL, 현금·보유·수수료·손실·시장시간·freshness 한도, owner/strategy 승인, public route/UI, paper/live readiness는 없다. 실행 증거는 [`../gates/g4i-k2c-execution-authority.md`](../gates/g4i-k2c-execution-authority.md)에 기록한다.

### K2B0 — 알려진 주문의 내부 합성 체결 조정

- 명시적 `SUBMIT_ACKNOWLEDGED`로 opaque provider order ref가 이미 묶인 주문만 대상으로 한다. 전체 lookup과 전체 execution 목록이 완결되고 account·provider order ref·종목·방향·수량·가격·UTC 시간이 일치할 때만 식별 가능한 체결을 append한다.
- 체결은 `(occurred_at, provider_execution_ref)` 순서로 기존 K2A 이벤트 경로에 한 SQLite transaction으로 반영한다. 같은 체결 재관측은 idempotent하고, payload 변경·교차 주문 체결번호·중간 충돌은 전체 rollback한다.
- 미완결·미발견 조회는 known-good 상태를 보존한다. 주문번호를 잃은 `SUBMIT_UNKNOWN`은 동일 tuple/time의 단일 주문이 보여도 `UNCORRELATED`로 유지하고 계좌 신규 submit을 계속 차단한다.
- 이는 credential-free synthetic contract다. 공식 키움 계좌 조회의 시간대·보존 기간·주문/체결번호 유일성은 문서화되지 않았고 client-order idempotency 필드도 없으므로, credentialed mock 관찰 전에는 실제 조회 복구로 승격하지 않는다. 실행 증거는 [`../gates/g4f-kiwoom-known-order-reconciliation.md`](../gates/g4f-kiwoom-known-order-reconciliation.md)에 기록한다.

### K2B1 — 날짜 지정 체결 page의 내부 합성 스캔

- 현재 [공식 키움 계좌 명세](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/main/kiwoom_docs/%EA%B3%84%EC%A2%8C.md)의 `kt00009`만 사용한다. 요청 날짜, 주식, 전체 시장/방향, 체결만, KRX를 고정하고 terminal cursor까지 읽는다.
- `A`-prefix KRX 주식, cash buy/sell, provider `trde_tp`, exact decimal, non-zero 7자리 주문·체결번호와 `HH:mm:ss`만 보존한다. alias는 environment·account·요청 날짜를 포함한 `dated_order`/`dated_execution` namespace라 K2A/K2B0 event alias로 사용할 수 없다.
- `PaginationComplete=true`는 이 요청의 page traversal만 뜻한다. 공식 명세가 timezone, retention, page snapshot consistency, ID lifetime과 lost-submit correlation을 보장하지 않으므로 `ExecutionsComplete=false`를 고정하고 요청 날짜와 execution clock을 UTC로 결합하지 않는다.
- 빈 배열은 해당 날짜 요청에 row가 없다는 뜻일 뿐 주문 미존재 증거가 아니다. credential, 실제 broker request, DB mutation, K2B0 mapping, unknown-submit 복구, public route/UI는 추가하지 않았다. 실행 증거는 [`../gates/g4g-kiwoom-dated-execution-scan.md`](../gates/g4g-kiwoom-dated-execution-scan.md)에 기록한다.

### K2B — 키움 모의주문 transport와 조회 복구

- 현재 공식 주문 명세의 신규 매수 `kt10000`, 매도 `kt10001`, 정정 `kt10002`, 취소 `kt10003`과 계좌 명세의 미체결·체결 조회 `ka10075`, `ka10076`, `kt00007`, `kt00009`를 credentialed mock 관찰과 contract fixture로 검증한 뒤 연결한다. ([주문 명세](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/main/kiwoom_docs/%EC%A3%BC%EB%AC%B8.md), [계좌 명세](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/main/kiwoom_docs/%EA%B3%84%EC%A2%8C.md))
- 공식 주문 요청에 문서화된 client-order idempotency가 없으므로 내부 key를 dispatch 전에 저장하고 timeout 뒤에는 재전송하지 않는다. credentialed mock 관찰로 검증된 broker correlation 근거가 없으면 조회 tuple/time만으로 unknown을 확정하지 않는다.
- G4L/G4N의 public API/Flutter 화면은 검증된 로컬 lifecycle을 읽기만 한다. `pending_action=SUBMIT|CANCEL|none`을 함께 노출해 `FILLED` 상태에 남은 취소 결과 대기를 숨기지 않지만, production risk engine, broker-coupled runner lease/fencing, market/amend capability, credentialed broker-current 조회, public 주문 mutation/확인 UI와 체결-원장 reconciliation은 후속 단계에서 검증한다.
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
