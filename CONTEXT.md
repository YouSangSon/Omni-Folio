# Omni Folio Domain Context

## Core language

- **Ledger authority**: 거래, 현금 이동, 수수료, 세금, 기업행사, 정정을 기록하는 유일한 재무 기준. 보유 수량과 손익은 여기서 계산한다.
- **Broker truth**: 외부 주문이 실제로 존재하고 어떤 상태인지에 대한 증권사의 사실. Omni Folio는 주문 의도와 위험 판단, 체결의 원장 반영 근거를 소유한다.
- **Broker snapshot**: 한 번의 완결된 pagination에서 읽은 계좌·잔고·미체결 사실. 원장과 별도이며 전체 성공한 snapshot만 이전 known-good를 대체한다.
- **Freshness**: source timestamp, 마지막 전체 성공 시각과 `fresh`, `stale`, `partial`, `error` 상태를 함께 나타내는 신뢰 정보. 서비스 readiness나 원장 검증 상태와 합치지 않는다.
- **Read model**: 원장과 주문 이벤트에서 결정적으로 다시 만들 수 있는 조회 결과. 모바일 캐시는 read model의 복제본일 뿐 권한자가 아니다.
- **Import preview**: 입력을 쓰지 않고 정규화·검증해 신규, 중복, 오류, 미해결 행과 예상 변화를 보여주는 단계.
- **Apply receipt**: import가 원자적으로 반영됐거나 전혀 반영되지 않았음을 증명하는 구조화된 결과.
- **Order intent**: 전략 또는 사용자가 원하는 주문을 표현하지만 아직 증권사에 전송되지 않은 명령.
- **Execution event**: 접수, 부분체결, 체결, 취소, 거절처럼 증권사에서 관찰한 append-only 주문 사실.
- **Reconciliation**: 내부 주문·원장 상태를 증권사의 주문·체결·잔고 사실과 비교해 차이를 설명하거나 차단하는 과정.
- **Risk reservation**: 주문 전송 전에 현금, 포지션, 익스포저 한도를 점유해 동시 주문이 한도를 넘지 못하게 하는 상태.
- **Fencing token**: 현재 실행 권한을 가진 runner 세대만 주문을 전송하게 하는 단조 증가 토큰.
- **Strategy manifest**: 전략 버전, 파라미터, 데이터 snapshot, 실행 환경을 재현 가능하게 묶는 식별 계약.
- **Promotion evidence**: paper, shadow, canary, limited-live 단계의 검증 결과와 승인 기록.
- **Live-disabled**: 어떤 UI 설정이나 프로세스 시작만으로도 실주문이 나갈 수 없는 기본 실행 상태.

## Invariants

- 금액, 가격, 수량, 환율은 JSON number나 이진 부동소수점으로 교환하거나 저장하지 않는다.
- 모바일·웹·Python 연구 프로세스는 broker credential과 주문 제출 권한을 갖지 않는다.
- 주문 timeout은 실패가 아니라 결과 미확정 상태다. 같은 식별자로 조회·reconcile하기 전 재주문하지 않는다.
- offline 상태에서 주문을 큐에 넣지 않는다.
- 삭제나 덮어쓰기 대신 correction/event append를 기본으로 한다.
