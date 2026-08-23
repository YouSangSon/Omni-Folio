# Omni Folio Goal Prompt

Omni Folio를 개인이 실제로 오래 사용할 수 있고 증권사·시장·기능을 단계적으로 추가할 수 있는 멀티 증권사 주식 앱으로 완성하라.

## 최종 목표

한국·미국 주식과 ETF를 여러 증권 계좌에서 통합하여 다음 기능을 제공한다.

1. 계좌·거래·잔고 자동 동기화와 CSV 수동 가져오기
2. 정확한 거래 원장 기반 보유 수량, 현금, 평가손익, 실현손익, 배당, 수수료, 세금, 환율 계산
3. 포트폴리오 성과 차트와 종목별 OHLCV·거래량 차트
4. 차트 위 평균단가와 실제 매수·매도 체결 표시
5. 워치리스트, 가격 갱신 상태, 지연·오류·부분 실패 표시
6. 시장가·지정가 매수/매도, 주문 확인, 취소, 부분체결·체결·거절 추적
7. 모의주문에서 검증한 뒤 사용자가 명시적으로 활성화하는 실전 주문
8. 전략 연구, 백테스트, 모의 자동매매, 위험 한도, 지연·체결 품질 분석
9. 충분히 검증된 전략만 별도 승인으로 활성화하는 제한적 실전 자동매매

첫 사용자는 앱 소유자 한 명이며 local-first를 우선한다. 여기서 local-first는 데이터 소유권과 로컬 단독 사용 가능성을 뜻하며 laptop-only를 뜻하지 않는다. 같은 코드와 데이터 계약으로 사용자가 관리하는 단일 노드 클라우드에도 배포할 수 있어야 한다. 옵션·선물·암호화폐, LLM 투자 추천, 다중 사용자 SaaS, 세금 신고서 생성은 제외한다. 자동매매는 핵심 확장 목표에 포함하되, 기본값은 연구·백테스트·paper trading이며 실전 자동매매는 명시적 승인 전까지 비활성화한다.

## 반드시 지킬 원칙

- 현재 작업공간과 `docs/omni-folio-research-report.md`, `docs/omni-folio-plan.md`를 먼저 읽고 현재 상태를 기준으로 작업한다.
- 최종 목표는 여러 증권사 지원이지만 첫 어댑터는 KIS 또는 키움 REST API 하나만 끝까지 완성한다. 공통 원장과 첫 어댑터가 검증된 뒤 두 번째 증권사를 추가한다.
- 브로커 어댑터와 시장 데이터 어댑터를 분리한다.
- 거래 원장을 source of truth로 삼고 잔고·손익·성과는 원장에서 결정적으로 계산한다.
- 금액·수량 계산은 부동소수점이 아닌 Decimal을 사용한다.
- import와 주문 요청은 멱등성을 보장하고, 정정은 원본 삭제보다 append-only correction을 우선한다.
- API 키와 계좌 식별자는 서버 측 안전한 저장소에만 두며 브라우저, 로그, export, 오류 메시지에 노출하지 않는다.
- 배포 가능한 모듈러 모놀리스와 SQLite로 시작한다. 로컬과 클라우드는 같은 코드와 OCI image를 사용하고 설정·secret·영속 저장소만 실행 프로필로 바꾼다. 측정된 필요가 생기기 전에는 microservice, Redis, message broker, 동적 plugin SDK를 추가하지 않는다.
- 실전 주문은 절대 자동 활성화하지 않는다. 모의투자 검증, 체결-원장 reconciliation, 실패 복구, 사용자 명시 승인 전에는 비활성 상태로 유지한다.
- 자동매매는 `Universe → Signal/Alpha → PortfolioTarget → RiskAdjustedTarget → OrderIntent → Execution` 단계로 분리한다. 전략은 브로커 주문을 직접 만들거나 전송하지 않는다.
- 백테스트 결과를 실전 기대수익으로 표시하지 않는다. 슬리피지, 수수료, 세금, 체결 지연, 데이터 지연, survivorship/lookahead bias를 검증 항목으로 둔다.
- live 전략은 paper trading에서 일정 기간 검증한 동일한 전략 정의와 동일한 주문 상태 머신만 사용한다. paper/live 환경, API key, 계좌, feature flag를 물리적으로 분리한다.
- 자동매매 hot path는 p50/p95/p99 지연, 시장 데이터 freshness, queue depth, provider latency, 주문 접수/체결 지연, 실패·재시도 횟수를 측정한다.
- 주문 hot path는 `market data event → freshness check → strategy signal → pre-trade risk → idempotency key → broker submit → ack/execution ingest → ledger reconciliation → audit log`로 고정한다.
- 전략 승격은 `paper → shadow live market data → 소액 canary → limited live` 순서로만 허용한다. shadow mode는 실시간 데이터와 실계좌 상태를 읽되 실제 주문 대신 의도 주문과 위험 판단만 기록한다.
- 시스템 clock drift가 기준을 넘거나 시장 데이터가 stale이면 자동주문을 막는다. 큐가 밀리면 오래된 signal을 늦게 주문하지 않고 버리거나 최신 signal로 병합한다.
- 초기 latency tier는 EOD·분봉 전략이다. 고정된 밀리초 SLA를 먼저 약속하지 말고 실제 p95/p99 측정 뒤 전략별 `max_data_age`, `max_decision_time`, `max_ack_wait` 예산을 정한다. sub-ms HFT, co-location, exchange direct market access는 범위 밖이다.
- 외부 계좌 주문, 배포, credential 변경, push는 사용자의 명시적 승인 없이 실행하지 않는다.

## 확장 가능한 아키텍처 기준

확장성은 처음부터 분산 시스템을 만드는 것이 아니라, 새 공급자를 추가할 때 검증된 코어를 수정하지 않는 것으로 정의한다.

```text
Web / Mobile UI
       |
Application use cases
       |
Domain modules
  ├─ instruments
  ├─ ledger & portfolio
  ├─ market data
  ├─ orders & executions
  ├─ research & data catalog
  ├─ strategy & portfolio construction
  ├─ backtesting & simulation
  ├─ risk & automation
  └─ watchlists & alerts
       |
Ports
  ├─ BrokerPort
  └─ MarketDataPort
       |
Adapters
  ├─ KIS / Kiwoom / Alpaca / IBKR
  └─ broker feed / external market-data provider
```

### 실행 프로필과 클라우드 배포 계약

- 하나의 저장소와 pinned non-root OCI image에서 `api`, `migrate` 명령을 제공하고, 해당 단계가 시작될 때만 `worker`, `runner`, `execution-gateway` 역할을 같은 image에 추가한다. 이는 배포 시 프로세스 역할만 나누는 것이며 별도 microservice를 뜻하지 않는다.
- local 프로필은 loopback bind, 단일 SQLite, OS keychain을 사용하고 외부 인증 없이 인터넷에 공개하지 않는다.
- 첫 cloud 프로필은 TLS와 owner 인증, secret manager 주입, 단일 API replica, single-writer provider-managed block volume(RWO)의 SQLite, 암호화된 정기 backup과 restore drill을 요구한다. ephemeral filesystem이나 NFS형 공유·다중 writer volume에 SQLite를 두지 않는다. 이 프로필은 stateful single-node이며 무중단 교체, scale-out, 자동 failover를 보장하지 않는다.
- 애플리케이션의 영속 상태는 데이터베이스와 versioned backup에만 두고 container filesystem은 임시 파일 외에는 사용하지 않는다.
- SQLite backup은 online backup API 또는 동등한 transaction-consistent snapshot으로 off-volume에 저장한다. 실행 중 DB 파일의 단순 복사나 같은 volume의 복사본은 backup으로 인정하지 않으며, restore 후 `integrity_check`와 ledger golden test를 통과해야 한다.
- 두 번째 API replica, API/worker 독립 확장, 무중단 교체, 다중 노드 failover, 높은 동시 쓰기 또는 managed point-in-time recovery가 필요해지면 먼저 PostgreSQL로 승격한다. SQLite export → PostgreSQL import 후 row count, checksum, ledger invariant를 검증하고 SQLite 상태에서 scale-out을 지원한다고 주장하지 않는다.
- schema migration은 startup side effect가 아니라 명시적인 `migrate` 단계로 실행한다. 배포 전 off-volume backup, schema compatibility check, migration 후 ledger golden test, restore 검증을 통과해야 한다.
- liveness는 프로세스 생존만, readiness는 DB 연결·schema version·필수 설정만 확인한다. 브로커 장애는 전체 API를 죽이지 않고 provider별 degraded/freshness 상태로 노출한다.
- HTTP 요청보다 오래 살거나 재시작 복구가 필요한 scheduled sync는 그 요구가 생기는 Phase B부터 `worker` 역할로 분리한다. unattended sync와 paper/shadow/live runner는 owner-managed always-on host에서만 실행한다. runner는 DB lease와 fencing token으로 계좌·전략별 단일 active instance만 허용하고 lease를 잃으면 신규 주문을 fail-closed한다.
- cloud rollout과 rollback 중에는 신규 주문을 먼저 차단하고, 미체결 주문과 broker 상태를 reconciliation한 뒤 runner lease를 넘긴다. 되돌릴 수 없는 migration은 자동 downgrade하지 않고 backup restore 또는 forward-fix 절차를 사용한다.

- 도메인 모듈은 공급자 SDK, HTTP 응답 타입, UI 프레임워크를 직접 참조하지 않는다.
- 각 브로커 응답은 canonical account, instrument, transaction, position, order, execution 모델로 정규화한다.
- 브로커마다 가능한 주문 유형과 시장이 다르므로 `BrokerCapabilities`로 기능을 선언하고 UI도 이를 기준으로 노출한다.
- 공급자별 인증, rate limit, pagination, symbol mapping, 재시도는 해당 adapter 내부에 둔다.
- 주문 상태 머신과 거래 원장을 분리하고 체결 이벤트를 통해 reconciliation한다.
- 전략 정의는 브로커 SDK, credential, 주문 API에 접근하지 않고 `Signal`만 만든다. 포트폴리오 구성기가 여러 전략의 신호와 자금 배분을 `PortfolioTarget`으로 합치고, 공통 risk/execution pipeline만 주문을 만든다.
- 실행 모드는 `backtest`, `paper`, `shadow`, `live-disabled`, `live-enabled`로 구분하며 UI·로그·credential·계좌를 섞지 않는다.
- 위험 제어는 전략이 우회할 수 없는 공통 레이어에 둔다: 종목/시장 허용 목록, 가격 collar, 1회 주문 수량·금액, 총/순 익스포저, 포지션·미체결 주문, 일일 손실, 주문 속도, 거래 시간, stale data, clock drift, provider/reconciliation 장애.
- kill switch는 전략 프로세스와 독립적으로 동작하고 기본적으로 신규 주문을 차단하며, 필요하면 미체결 취소와 위험 축소 주문만 허용한다.
- backtest, paper, shadow, live는 같은 전략·포트폴리오·리스크 코어와 교체 가능한 clock/data/execution adapter를 사용한다.
- 일반 목적 백테스트·실행 엔진을 새로 만들기 전에 LEAN과 NautilusTrader를 짧은 POC로 평가한다. 요구를 충족하는 기존 엔진이 있으면 재사용하고, 둘 다 맞지 않을 때만 일봉/분봉 이벤트 재생과 deterministic fill model의 최소 내부 엔진을 만든다.
- 시장 데이터 큐는 stale snapshot이나 superseded signal을 병합할 수 있지만 주문 command, ack, fill, cancel, reject 이벤트는 유실하거나 덮어쓰지 않는다.
- 주문 command와 ack/fill/cancel/reject는 provider ID와 idempotency key를 포함한 append-only execution log에 저장한다.
- 주문 submit 전 `client_order_id`/idempotency key를 unique constraint와 함께 durable하게 기록한다. execution gateway는 모든 order intent의 fencing token을 DB의 현재 owner token과 비교하고 불일치하면 broker submit 전에 거절한다. timeout이나 crash로 결과가 불명확하면 재주문하지 않고 같은 키로 broker 상태를 조회·reconcile한 뒤에만 다음 상태로 진행한다.
- live 전략 runner는 실전 활성화 전에 UI/API와 별도 프로세스로 격리하고 broker credential이나 외부 주문 API에 접근하지 못하게 한다. credential은 공통 execution gateway만 읽는다. 같은 저장소와 모듈 경계를 유지하므로 이를 별도 microservice로 확장하지 않는다.
- 새 알고리즘은 versioned strategy manifest, 전략 모듈, contract/backtest fixture만 추가해 등록할 수 있어야 하며 주문·원장·브로커 코어에 전략별 분기를 추가하지 않는다.
- 인터페이스는 `BrokerPort`와 `MarketDataPort`처럼 실제 교체 지점에만 만든다. 단일 구현 내부에는 불필요한 factory나 추상화를 만들지 않는다.
- SQLite schema, backup format, API 계약에는 명시적인 migration/version 정책을 둬 데이터와 클라이언트를 깨지 않고 확장한다.
- PostgreSQL 승격, 별도 runner process, 모바일 클라이언트가 실제로 필요해질 때 현재 모듈 경계와 골든 테스트를 유지한 채 분리할 수 있어야 한다.

## 진행 중 개선 원칙

- 이 계획을 고정된 체크리스트가 아닌 현재 최선의 기준선으로 취급한다.
- 구현 중 결함, 누락된 요구사항, 잘못된 가정, 더 단순하고 안전한 설계가 발견되면 최종 목표에 필요한 범위에서 계획과 구현을 함께 개선한다.
- 새 증권사 연결, 주문 정합성, 데이터 손실 방지, 보안, 접근성, 테스트, migration, 오류 복구에 필요한 작업은 초기 목록에 없더라도 추가한다.
- 증상별 임시 처방보다 모든 호출 경로가 공유하는 원인과 경계에서 수정한다.
- 기존 데이터와 API 계약을 깨는 변경에는 migration, 호환성 처리, rollback 또는 복구 절차를 포함한다.
- 변경할 때마다 관련 테스트와 문서를 함께 갱신하고, 중요한 판단과 남은 위험을 계획 문서에 기록한다.
- 추측성 기능, 관련 없는 리팩터링, 사용되지 않는 추상화는 추가하지 않는다.
- 외부 비용, credential 변경, 실제 주문 실행, 배포, push 또는 제품 방향을 바꾸는 큰 범위 확장은 사용자 승인 후 진행한다.
- 완료 여부는 최초 계획의 항목 수가 아니라 실제 최종 목표와 현재 구현을 다시 대조한 증거로 판단한다.

## 구현 순서

### Phase 1 — 원장과 복구

- accounts, instruments, transactions, lots, prices, fx_rates, corporate_actions, sync_runs 모델
- CSV parse → normalize → preview → confirm → apply
- 중복 import 방지와 JSON backup/restore
- `schema_migrations`, backup format version, 이전 backup을 현재 schema로 복원하는 golden test
- FIFO 기준 보유 수량, 현금, 실현·미실현 손익
- 배당, 수수료, 세금, 입출금, 주식 분할, 다중 통화 처리

### Phase 2 — 첫 증권 API

- KIS 또는 키움 read-only 연결
- read-only scope credential만 허용하고 주문 scope credential은 이 단계에서 명시적으로 거절
- 계좌, 거래, 잔고 pagination 동기화
- rate limit, retry/backoff, token 갱신, freshness 상태
- 브로커 잔고와 원장 잔고 reconciliation 화면

### Phase 3 — 핵심 UI와 차트

- Overview, Holdings, Asset Detail, Transactions, Import Review, Connections/Settings
- 포트폴리오 가치·손익·현금·성과 추이
- 종목 OHLCV, 거래량, 기간 선택, 평균단가와 매매 마커
- light/dark theme, 모바일 대응, 키보드 탐색, 200% 확대, reduced-motion
- loading, empty, error, partial, stale, success 상태
- 상승·하락을 색상만으로 표현하지 않고 부호와 텍스트를 함께 제공

### Phase 4 — 주문

- 이 단계는 증권사 모의투자 또는 Alpaca paper trading만 구현한다. 실전 주문 credential과 live broker submit은 Phase 6의 broker별 promotion gate 전까지 코드 경로와 설정에서 비활성화한다.
- 시장가·지정가, 매수·매도, 수량·예상금액·수수료 확인
- idempotency key와 중복 주문 방지
- 접수, 미체결, 부분체결, 체결, 취소, 정정, 거절 상태 머신
- 재시작·네트워크 단절 후 주문 상태 재조회와 원장 reconciliation

### Phase 5 — 전략 연구와 모의 자동매매

- LEAN과 NautilusTrader POC: KIS adapter 가능성, Python 전략 경험, backtest/live parity, 운영 복잡도, 라이선스를 비교하고 하나를 재사용할지 최소 내부 엔진을 만들지 기록
- 전략 정의: universe, signal/alpha, portfolio construction, allocation, risk limits, schedule
- 백테스트: 과거 OHLCV 재생, 기업행사, 수수료·세금·슬리피지·부분체결·지연 모델, survivorship/lookahead 방지, out-of-sample·walk-forward 검증
- 결과: CAGR, TWR/XIRR, max drawdown, Sharpe/Sortino, turnover, win rate, exposure, 거래별 감사 로그
- 재현성: strategy/version, parameter hash, data snapshot, engine version, random seed, 실행 환경을 run manifest로 저장
- paper automation: 전략 신호를 포트폴리오 목표와 risk-adjusted target으로 변환한 뒤 공통 주문 pipeline이 paper order만 실행
- owner-managed always-on host에서 DB lease/fencing으로 단일 runner만 활성화하고 중복 scheduler·중복 주문을 검증
- kill switch: 수동 중지, 일일 손실, 연속 실패, stale data, reconciliation mismatch, provider 장애
- Strategy Lab, Backtest Report, Automation Monitor, Risk/Latency 화면: 전략 버전·모드·자금 배분·위험 한도·최근 신호/주문/체결·freshness·kill switch 표시
- buy-and-hold, 단순 리밸런싱, 이동평균 교차는 엔진 검증 fixture로만 제공하고 수익 보장이나 투자 추천으로 표시하지 않음
- 전략별 변경 이력과 실행 로그를 저장해 같은 입력에서 같은 결과가 재현되게 한다.

### Phase 6 — 제한적 실전 자동매매

- 실전 주문 권한은 기본 비활성이고 전략별·계좌별·종목별로 별도 승인한다.
- paper → shadow → 소액 canary → limited live 순서와 각 단계의 자동 중지 기준을 통과한 전략만 승격한다.
- paper/live parity 리포트, reconciliation 통과, 위험 한도, 롤백 절차, 독립 kill switch, 알림 채널이 준비된 전략만 활성화한다.
- 최초 실전 자동매매는 소액, 지정가, 허용 종목 목록, 장중 수동 모니터링과 fail-closed runner를 조건으로 한다.

### Phase 7 — 멀티 증권사 확장

- 두 번째 국내 증권사 어댑터
- 필요하면 Alpaca 또는 IBKR read-only/주문 연동
- 워치리스트, 가격 알림, 배당 캘린더, 벤치마크 비교

## 완료 조건

- 같은 거래 파일이나 API 페이지를 반복 적용해도 중복 거래가 생기지 않는다.
- 이전 backup을 현재 schema로 복원한 뒤 보유 수량·현금·손익이 동일하게 재계산된다.
- 입출금, 배당, 수수료, 세금, 환율, 분할이 포함된 골든 데이터에서 수량·현금·손익이 독립 계산과 일치한다.
- 브로커 잔고와 원장 잔고의 차이가 화면에서 설명 가능하다.
- 공급자 timeout, 429, token 만료, 부분 응답 후에도 기존 데이터가 보존되고 재시도가 안전하다.
- 차트는 실제 데이터와 일치하며 모든 기간에서 빈 데이터·지연 데이터 상태를 처리한다.
- 모의주문에서 중복 요청, 부분체결, 취소, 거절, 재시작 복구가 검증된다.
- 실전 주문 경로는 모의주문 검증과 별개로 보안·오주문 방지 체크를 통과한다.
- 전략 백테스트는 수수료·슬리피지·지연·데이터 지연을 포함하고, lookahead bias를 막는 테스트가 있다.
- 동일한 전략 코드·clock 계약·포트폴리오 구성·리스크 코어가 backtest, paper, shadow, live에서 사용된다.
- paper 자동매매는 신호 충돌과 자금 배분, 위험 차단, 주문 상태, 체결 reconciliation, restart recovery, kill switch가 검증된다.
- 각 주문은 market event부터 broker ack/fill까지 UTC timestamp와 monotonic duration을 남기며 구간별 p50/p95/p99, freshness, queue depth를 확인할 수 있다.
- 주문·체결 이벤트는 과부하와 재시작 상황에서도 유실되지 않는 테스트가 있다.
- submit timeout·ack 전후 crash·runner lease 상실·두 runner 동시 기동에서도 중복 주문이 발생하지 않고 신규 주문은 fail-closed한다.
- 두 번째 샘플 전략은 manifest, 전략 모듈, fixture 추가만으로 등록되고 공통 주문·리스크·원장 코드는 변경되지 않는다.
- 실전 자동매매는 기본 비활성이고, paper/live parity와 사용자 승인 없이는 어떤 경로에서도 주문을 낼 수 없다.
- API 키 redaction 테스트와 핵심 원장·주문 테스트가 통과한다.
- read-only, paper, live credential이 별도 secret과 권한으로 분리되고 허용되지 않은 scope가 fail-closed한다.
- 두 번째 브로커는 새 adapter와 공통 contract test 추가만으로 연결할 수 있고 원장·성과·차트·주문 코어의 공급자별 분기가 늘어나지 않는다.
- 지원 화면 크기와 키보드·접근성 검증이 통과한다.
- 동일 image의 local/cloud smoke test, health/readiness, migration, 암호화 backup/restore, 주문 차단형 rollback rehearsal이 통과한다.
- README에 로컬 실행, 단일 노드 cloud 배포, 데이터 백업/복원, API 연결, 모의주문 사용법, 실전 주문 활성화 위험과 절차가 기록된다.

각 단계에서 먼저 현재 코드를 조사하고 가장 작은 수직 슬라이스를 구현한 뒤 테스트로 증명하라. 부분 구현을 전체 완료로 보고하지 말고, 로컬 검증·모의투자 검증·실전 주문 준비·실제 운영 증거를 구분해서 보고하라.
