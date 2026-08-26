# Omni Folio Domain Context

## Core language

- **Ledger authority**: 거래, 현금 이동, 수수료, 세금, 기업행사, 정정을 기록하는 유일한 재무 기준. 보유 수량과 손익은 여기서 계산한다.
- **Broker truth**: 외부 주문이 실제로 존재하고 어떤 상태인지에 대한 증권사의 사실. Omni Folio는 주문 의도와 위험 판단, 체결의 원장 반영 근거를 소유한다.
- **Broker snapshot**: 한 번의 완결된 pagination에서 읽은 계좌·잔고·미체결 사실. 원장과 별도이며 전체 성공한 snapshot만 이전 known-good를 대체한다.
- **Freshness**: source timestamp, 마지막 전체 성공 시각과 `fresh`, `stale`, `partial`, `error` 상태를 함께 나타내는 신뢰 정보. 서비스 readiness나 원장 검증 상태와 합치지 않는다.
- **Market series**: 한 종목·거래소·시간대·interval에 속한 순서 보장 OHLCV bar와 source/as-of/freshness/price adjustment를 묶은 provider-neutral 조회 결과. 브로커 pagination·TR 필드는 포함하지 않는다.
- **Price adjustment**: OHLCV 가격의 조정 기준. `unspecified`는 조정 여부를 확인하지 못했다는 뜻이고, `provider_adjusted`는 공급자에게 조정 가격을 요청했다는 뜻일 뿐 기업행사 반영 정확성을 증명하지 않는다.
- **Sample market data**: 계약과 UI를 검증하기 위한 로컬 fixture. API의 machine-readable provenance와 화면의 명시적 문구로 실시간·투자 판단용 데이터가 아님을 항상 표시한다.
- **Synthetic Kiwoom candle contract**: credential·broker 요청 없이 `POST /api/dostk/chart`의 `ka10080`/`ka10081` 경계를 재현하는 K1 adapter 계약. KRX 여섯 자리 symbol, `1d` 및 `1/3/5/10/15/30/45/60m`, canonical OHLCV만 노출하며 public route가 아니다.
- **Synthetic Kiwoom order state**: broker 요청 없이 Go 내부에서만 실행하는 K2A `LIMIT`/`KRW`/`KRX` 주문 intent/event replay 계약. 실제 risk engine·broker submit/query·public route/UI·원장 반영은 포함하지 않는다.
- **Known-order execution reconciliation**: 명시적 broker ACK로 opaque provider order ref가 이미 묶인 주문에 대해, 완결된 조회의 식별 가능한 체결만 기존 append-only 주문 이벤트에 반영하는 K2B0 계약. 주문번호 없는 unknown submit을 속성 유사도로 결합하지 않는다.
- **Dated execution scan**: 명시한 날짜의 합성 `kt00009` 요청에서 terminal cursor까지 읽은 K2B1 provider-private 결과. `PaginationComplete`는 그 요청의 page 순회만 뜻하고 전체 주문 이력이나 체결 완결성을 뜻하지 않는다. `ExecutionsComplete`는 false이며 `ExecutionClock`은 timezone 없는 `HH:mm:ss`로 보존한다.
- **Mock submit transport**: 실제 credential이나 외부 broker 호출 없이 공식 `kt10000`/`POST /api/dostk/ordr` 요청·응답을 합성 transport로 검증하는 K2B2 내부 계약. public route/UI, credentialed 관찰, unknown-submit 조회 복구와 live 권한은 포함하지 않는다.
- **Read model**: 원장과 주문 이벤트에서 결정적으로 다시 만들 수 있는 조회 결과. 모바일 캐시는 read model의 복제본일 뿐 권한자가 아니다.
- **Import preview**: 입력을 쓰지 않고 정규화·검증해 신규, 중복, 오류, 미해결 행과 예상 변화를 보여주는 단계.
- **Cash-flow ledger event**: `DEPOSIT`·`DIVIDEND`는 양수, `WITHDRAWAL`·`FEE`·`TAX`는 음수 cash impact만 갖는 append-only event. `DIVIDEND`만 instrument provenance를 요구한다.
- **Stock-split ledger event**: 양수 분할 비율과 0 cash impact로 기존 열린 FIFO lot의 수량만 조정하고 총원가는 보존하는 append-only corporate action. 열린 lot가 없으면 전체 apply를 거절한다.
- **Apply receipt**: import가 원자적으로 반영됐거나 전혀 반영되지 않았음을 증명하는 구조화된 결과.
- **Order intent**: 전략 또는 사용자가 원하는 주문을 표현하지만 아직 증권사에 전송되지 않은 명령.
- **Execution event**: 접수, 부분체결, 체결, 취소, 거절처럼 증권사에서 관찰한 append-only 주문 사실.
- **Reconciliation**: 내부 주문·원장 상태를 증권사의 주문·체결·잔고 사실과 비교해 차이를 설명하거나 차단하는 과정.
- **Stored reconciliation read view**: G4H가 저장한 최신 Kiwoom KRX 보유수량과 특정 ledger revision의 exact diff를 account/internal ID/hash/raw snapshot 없이 조회하는 G4K 결과. `freshness=unverified`이며 broker refresh, 현재 상태, 현금·평가금액·주문·체결 대조를 뜻하지 않는다.
- **Risk reservation**: 주문 전송 전에 현금, 포지션, 익스포저 한도를 점유해 동시 주문이 한도를 넘지 못하게 하는 상태.
- **Fencing token**: 현재 실행 권한을 가진 runner 세대만 주문을 전송하게 하는 단조 증가 토큰.
- **Strategy manifest**: 전략 버전, 파라미터, 데이터 snapshot, 실행 환경을 재현 가능하게 묶는 식별 계약.
- **Promotion evidence**: paper, shadow, canary, limited-live 단계의 검증 결과와 승인 기록.
- **Research evidence**: Python이 만든 immutable `strategy-improvement-result.v1`. 재현 가능한 평가 산출물이지 실행 권한이 아니다.
- **Selected paper candidate**: Go registry replay가 가리키는 최신 `paper_candidate`; paper에서 실행 중인 champion이나 주문 승인과 동일하지 않다.
- **Strategy order binding**: 전략이 만든 주문 intent에 선택 result SHA와 exact selection event ID를 함께 보존하는 G3.5 fencing 계약. 신규 intent 기록과 durable dispatch 시점 모두 현재 registry replay와 일치해야 한다.
- **Paper signal**: 선택된 전략 result·exact selection event, 입력 data hash, 생성·만료 시각, 종목과 목표 수량을 묶은 `paper-signal.v2` 내부 명령. 계좌·방향·주문 수량·가격이나 broker 주문 권한을 갖지 않는다.
- **Paper execution adapter**: 실제 broker 호출 없이 한 시점의 local fixture ask와 가용 수량을 공통 주문 상태 머신의 결정적 ACK·부분/완전 체결 event로 바꾸는 G3.6 adapter.
- **`no_promotion` / `no_strategy`**: 전자는 한 실험의 gate 실패 결과, 후자는 현재 선택이 없다는 registry sentinel이다.
- **Live-disabled**: 어떤 UI 설정이나 프로세스 시작만으로도 실주문이 나갈 수 없는 기본 실행 상태.

## Invariants

- 금액, 가격, 수량, 환율은 JSON number나 이진 부동소수점으로 교환하거나 저장하지 않는다.
- 모바일·웹·Python 연구 프로세스는 broker credential과 주문 제출 권한을 갖지 않는다.
- strategy registry는 evidence와 선택 이력만 소유한다. 선택 상태만으로 paper/live runner 또는 주문 dispatch를 허용하지 않으며, 전략 주문은 exact current selection에 묶인 경우에만 기록·durable dispatch할 수 있다.
- 새 paper 주문은 현재 selection, 유효한 signal, K2C lease/fencing과 risk reservation을 모두 요구한다. 이미 durable dispatch된 paper 주문은 selection rollback이나 signal 만료 뒤에도 같은 관찰의 idempotent replay와 잔여 체결 복구를 계속한다.
- 신규 paper intent 기록은 현재 process가 소유한 만료 전 lease와 exact fencing token을 검증하고 K2C 승인·durable dispatch까지 같은 transaction에 append한다. 수동 strategy rollback은 모든 활성 execution authority를 fencing halt하고 rollback event를 같은 transaction에 append하며, 어느 한 기록이라도 실패하면 둘 다 남기지 않는다.
- Go는 같은 paper 계좌·종목의 체결 수량과 미완결 BUY 전체 수량을 목표에서 원자적으로 차감해 양수 delta만 `OrderIntent`로 만든다. 동시·반복 신호는 같은 목표를 중복 주문하지 않으며 `paper-signal.v1`은 복구만 허용한다.
- paper mode는 Kiwoom mock/production transport에 진입하지 않는다. G3.6의 local fixture 체결은 수수료·세금·slippage·quote stream·실제 성능 또는 live parity를 증명하지 않는다.
- 주문 timeout은 실패가 아니라 결과 미확정 상태다. 같은 식별자로 조회·reconcile하기 전 재주문하지 않는다.
- offline 상태에서 주문을 큐에 넣지 않는다.
- 삭제나 덮어쓰기 대신 correction/event append를 기본으로 한다.
- G1.6 cash-flow correction은 같은 계좌·통화의 이미 적용된 `DEPOSIT`, `WITHDRAWAL`, `DIVIDEND`, `FEE`, `TAX`만 `CASH_VOID`로 exact 반전한다. 원본과 두 receipt의 provenance를 유지하고 동일 target 중복, chain, trade/split target, 미래 target, 다른 금액·통화를 preview와 schema v8에서 모두 거절한다. replacement 값은 별도 정상 event로 같은 atomic apply에 넣는다.
- 현금 event의 부호와 분할의 0 cash impact를 신뢰하지 않고 import 경계와 SQLite CHECK에서 검증한다. 분할은 실현손익을 만들거나 기존 FIFO 총원가를 바꾸지 않는다.
- sample market data는 live/current 상태로 승격하거나 실제 market source와 조용히 혼합하지 않는다.
- public market series는 `price_adjustment`를 생략하지 않는다. local fixture는 `unspecified`로 고정하고 화면에서도 조정 여부를 확인하지 못했다고 표시한다.
- synthetic Kiwoom candle 결과는 `provider_adjusted`를 내부 provenance로 보존한다. 공식 문서가 timestamp timezone을 명시하지 않아 Asia/Seoul을 운영 가정으로 해석하며, adjustment event·freshness·실시간성을 주장하지 않는다.
- `SUBMIT_UNKNOWN`은 실패가 아니다. 같은 주문과 해당 계좌의 신규 submit은 차단하되 이미 알려진 open order의 cancel은 위험 축소 경로로 허용한다. K2B0는 이미 ACK된 주문의 체결만 조정하며, 주문번호 없는 timeout은 종목·방향·수량·가격·시간이 같아도 결합하지 않고 `UNCORRELATED`로 유지한다.
- K2B2 mock 주문은 token preflight가 성공한 뒤 reservation과 `SUBMIT_DISPATCHED`를 먼저 durable commit하고 broker write를 한 번만 시도한다. write의 401·provider auth code·timeout·network·5xx는 자동 재시도하지 않고 `SUBMIT_UNKNOWN`을 유지한다. 명시적 provider reject만 `REJECTED`로 끝내며 ACK와 account snapshot은 동일한 `opaque account_ref + raw order number` HMAC namespace를 사용한다.
- K2B1 dated aliases는 environment·account·요청 날짜를 포함한 별도 namespace이며 K2A/K2B0 order event alias로 사용할 수 없다. 요청 날짜와 provider-local execution clock을 결합해 UTC timestamp를 만들거나 terminal pagination을 complete execution history로 승격하지 않는다.
- G4K는 가장 최신 raw broker snapshot에 대조 record가 없거나 canonical hash·metadata 검증이 실패하면 과거 known-good로 조용히 후퇴하지 않고 fail-closed한다. 응답과 Flutter는 account reference를 포함하지 않는다.
