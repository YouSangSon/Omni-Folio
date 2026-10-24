# G4R Kiwoom Realtime-Price Gate

Scope: credential-free synthetic contract for one official Kiwoom `0B` registration packet and `REAL` price frame. No WebSocket connection, credential, external request, persistence, route, UI or live authority is used.

## Pass evidence

- [x] Registration is closed to one bare six-digit symbol with `REG`, group `1`, refresh `1` and type `0B`.
- [x] Frames are capped at 1 MiB before decode and 100 entries before normalization.
- [x] Every `REAL.data` entry must be `0B` with a bare six-digit item, string FID `10` positive price and valid string FID `20` clock.
- [x] Provider `HHMMSS` becomes naive `HH:mm:ss`; separately injected receive time becomes canonical UTC without inventing an observation date.
- [x] FID `9081` is ignored and the DTO has no exchange field until an official/observed mapping can prove it.
- [x] Same-frame equal symbol/clock/price duplicates collapse; different prices at the same symbol/clock fail closed without partial output.
- [x] Control/missing/empty/mixed/malformed/numeric/trailing frames and zero receive time fail closed; no provider name or unknown FID is exposed.
- [x] The implementation uses existing decimal/time/JSON helpers and Go stdlib only.

## Evidence

- RED `2e80583`: the registration, parser and internal DTO did not exist.
- GREEN `35116ea`: focused realtime tests and the full Go core suite pass.
- Review boundary RED `dac201e` and fix `4da510f`: unproven KRX labeling was removed, and byte/entry caps now reject oversized frames before normalization.
- Independent corrected re-review: GO; both exchange-provenance and external-input-bound blockers are closed.
- `make check`, `make smoke`, Go race, `govulncheck` and 78.1% Go statement coverage pass locally on 2026-08-28 KST.
- Official source: Kiwoom repository commit [`9180deb`](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/commit/9180debf7aea0074715dd8f7a15af432afbfc403), [`subscribe_domestic_stock_trade_async.py`](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/9180debf7aea0074715dd8f7a15af432afbfc403/examples/%EA%B5%AD%EB%82%B4%EC%A3%BC%EC%8B%9D/%EC%8B%A4%EC%8B%9C%EA%B0%84%EC%8B%9C%EC%84%B8/subscribe_domestic_stock_trade_async.py).

## Deliberately open

- Credentialed mock observation and WebSocket LOGIN/PING, timeout, reconnect, resubscribe, gap/duplicate and backpressure behavior.
- Official timezone/date, exchange-FID mapping, event ordering/identity and price-adjustment provenance.
- Durable ingestion/known-good policy, scheduler, public API/Flutter and every live-money gate.


<a id="realtime-connection-readiness-2026-10-25"></a>

## 실제 연결 전의 재개 조건 (2026-10-25)

현재 production Go source에서 `NewKiwoomClient`, `kiwoomRealtimeTradeRegistration`, `normalizeRealtimePrices`는 선언 외 참조가 없다. `KiwoomClient`를 만드는 production literal도 생성자 안에만 있다. 따라서 이 gate의 synthetic parser 통과는 CLI·서버에서 동작하는 실제 연결을 증명하지 않는다. 이번 세션의 `KIWOOM` 계열 환경변수 이름은 없었으며, 사용자 전체의 비밀 저장소 준비 여부는 미확인이다. 다음 구현은 기존 계획대로 credentialed mock 관측을 먼저 확보한다.

공식 SDK의 조회 시점 main commit [`953e5db`](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/commit/953e5dbff123f437ab4d11a78a95191a685eb51f)의 [`ws_client.py`](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/953e5dbff123f437ab4d11a78a95191a685eb51f/kiwoom/core/ws_client.py)와 [`auth.py`](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/953e5dbff123f437ab4d11a78a95191a685eb51f/kiwoom/core/auth.py)에서 확인한 범위는 다음과 같다. SDK를 설치·실행한 결과나 제공사 서버의 관측은 아니다.

- SDK 기본 모의 주소는 `wss://mockapi.kiwoom.com:10000`, 운영 주소는 `wss://api.kiwoom.com:10000`이다. 기존 `0B` sample의 path는 `/api/dostk/websocket`이다. mode와 실제 접속 대상을 함께 확인해야 한다.
- SDK는 연결 후 token을 포함한 `LOGIN`을 보내고 로그인 응답을 기다린 뒤 등록 요청을 보낸다. application message의 문자열/JSON `PING`도 처리해 echo한다. 이는 라이브러리의 WebSocket control ping/pong과 구분한다.
- `subscribe()`는 로그인 후 요청을 송신하고 반환한다. 그 반환만으로 등록 승인·유효 가격 수신이 입증되지 않는다. 수신 중 연결 종료를 발견하면 iterator를 끝내므로 자동 reconnect·resubscribe 보장도 따로 확인해야 한다.
- 예제의 `open_timeout`은 연결을 여는 범위의 설정이다. [라이브러리 공식 정의](https://websockets.readthedocs.io/en/stable/reference/asyncio/client.html#websockets.asyncio.client.connect)와도 일치하며, 이 값 하나를 LOGIN/등록 응답 대기의 전체 deadline으로 간주하지 않는다. 우리 연결 경로가 생기면 대기 단계·취소·종료의 경계를 별도로 증명해야 한다.

실제 관측도 broker credential을 Go가 소유하는 기존 경계와 [my-cloud 실행 진입 조건](g6-deployment.md#single-node-resource-budget)을 따른다. 최소 관측의 순서는 준비된 **모의** secret binding과 접속 대상 확인 → 소유한 세션의 LOGIN 응답과 PING 처리 → 한 종목의 등록 응답·REAL 수신 순서 → 실패/취소 뒤 소켓과 소유 자원 종료다. token·계좌·원본 `return_msg`를 증거 로그로 옮기지 않고 기존 Go 오류 redaction과 byte/entry cap을 유지한다. 응답이 없는 구간은 구독 실패와 가격 미관측을 구분한다. 이 확인만으로 provider clock에 날짜/거래소를 만들거나 현재 가격·영속화·주문 권한으로 승격하지 않는다.

[키움 SDK 라이선스](https://github.com/Kiwoom-Securities/Kiwoom-REST-API/blob/953e5dbff123f437ab4d11a78a95191a685eb51f/LICENSE.md)와 기존 저장소 정책에 따라 샘플·공통 클라이언트 코드는 차용하지 않았다. 이번 변경은 소스 확인과 재개 조건 기록이며 실제 연결, 새 의존성, credential 변경, broker 요청, local 통합 검사 또는 추가 runtime 검증을 포함하지 않는다. LOGIN/PING의 실제 응답과 안전한 접속 환경이 확인되기 전까지 위 open gate를 유지한다.
