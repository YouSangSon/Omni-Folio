# Omni Folio Goal Prompt

Omni Folio를 개인이 실제로 오래 사용할 수 있고 증권사·시장·기능을 단계적으로 추가할 수 있는 멀티 증권사 주식 앱으로 완성하라.

이 문서는 Omni Folio의 제품 목표, 실행 권한, 안전 상한, 아키텍처 원칙과 완료 조건을 정의하는 단일 goal prompt다. 구현을 시작하기 전에 [`PLAN.md`](PLAN.md), [`GATES.md`](GATES.md), [`CONTEXT.md`](CONTEXT.md), [`DESIGN.md`](DESIGN.md), [`docs/omni-folio-plan.md`](docs/omni-folio-plan.md), [`docs/omni-folio-research-report.md`](docs/omni-folio-research-report.md)를 읽는다. 현재 구현 순서와 완료 상태는 `PLAN.md`, promotion 근거는 `GATES.md`와 [`gates/`](gates/)만 정본으로 삼고 이 문서에 진행 로그를 복제하지 않는다.

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
9. 제한된 후보 공간에서 전략·파라미터를 자동 탐색하고 검증·페이퍼 승격·롤백하는 지속 개선 루프
10. 충분히 검증된 전략만 별도 승인으로 활성화하는 제한적 실전 자동매매

첫 사용자는 앱 소유자 한 명이며 local-first를 우선한다. 여기서 local-first는 데이터 소유권과 로컬 단독 사용 가능성을 뜻하며 laptop-only를 뜻하지 않는다. 같은 코드와 데이터 계약으로 사용자가 관리하는 단일 노드 클라우드에도 배포할 수 있어야 한다. 옵션·선물·암호화폐, LLM 투자 추천, 다중 사용자 SaaS, 세금 신고서 생성은 제외한다. 자동매매는 핵심 확장 목표에 포함하되, 기본값은 연구·백테스트·paper trading이며 실전 자동매매는 명시적 승인 전까지 비활성화한다.

## 반드시 지킬 원칙

- 현재 작업공간과 `docs/omni-folio-research-report.md`, `docs/omni-folio-plan.md`를 먼저 읽고 현재 상태를 기준으로 작업한다.
- 최종 목표는 여러 증권사 지원이지만 첫 어댑터는 **키움 REST API**로 고정한다. 국내주식 read-only, 차트·실시간, 키움 모의주문을 순서대로 완성한 뒤 **토스증권 Open API**를 두 번째 어댑터로 추가한다.
- 브로커 어댑터와 시장 데이터 어댑터를 분리한다.
- 거래 원장을 source of truth로 삼고 잔고·손익·성과는 원장에서 결정적으로 계산한다.
- 금액·수량 계산은 부동소수점이 아닌 Decimal을 사용한다.
- import와 주문 요청은 멱등성을 보장하고, 정정은 원본 삭제보다 append-only correction을 우선한다.
- 환전 원장 event는 실제로 매도한 통화·금액과 매수한 통화·금액을 하나의 atomic record로 보존한다. 두 금액에서 환율을 역산해 현재 시세나 평가환율로 승격하지 않으며, 수수료가 있으면 별도 `FEE` event로 기록한다.
- 평가용 환율은 방향이 명시된 direct observation으로 별도 보존한다. `rate`는 기준 통화 1단위당 상대 통화 단위이며 source·source observation ID·observed/fetched/recorded 시각과 canonical hash를 포함한다. 역환율·교차환율·보간·오래된 값 대체는 버전된 별도 정책과 골든 검증 없이는 만들지 않는다.
- 클라이언트는 asdf로 stable 버전을 고정한 Flutter 하나로 iOS·Android·app-centric web을 제공한다. 공개 리서치·문서형 콘텐츠나 SEO 요구가 실제로 생길 때만 Next.js를 별도 web surface로 추가하며 Flutter 제품 화면을 중복 구현하지 않는다. 이전 React/PWA 우선 권장은 **superseded**다.
- Flutter UX는 영웅문을 복제하지 않는다. 토스증권에서 참고한 쉬운 용어, 한 화면 한 결정, 점진적 상세 공개, 국내·미국의 일관된 흐름, 읽기 쉬운 차트, 명확한 주문 확인과 접근성을 Omni Folio 고유 디자인으로 구현한다.
- Go 모듈러 모놀리스가 ledger, order, risk, broker credential과 broker submit authority를 소유한다. Python은 research/backtest와 재현 가능한 산출물만 담당하며 broker credential, 운영 DB 쓰기, order-submit 권한을 갖지 않는다.
- API 키와 계좌 식별자는 서버 측 안전한 저장소에만 두며 클라이언트, 로그, export, 오류 메시지에 노출하지 않는다.
- 배포 가능한 Go 모듈러 모놀리스와 SQLite single-writer local로 시작한다. 로컬과 단일 노드 클라우드는 같은 OCI image를 사용하고 설정·secret·영속 저장소만 실행 프로필으로 바꾼다. 측정된 필요가 생기기 전에는 microservice, Redis, message broker, 동적 plugin SDK를 추가하지 않는다.
- 신뢰할 수 있는 프레임워크·라이브러리는 회피하지 않는다. 직접 구현보다 정확성·성능·유지보수성이 나을 때 채택하되 공식 저장소의 활성도, license, 보안 이력, release 고정·lockfile, 공급망 검사와 Omni Folio 골든 fixture 교차검증을 통과해야 한다.
- 새 의존성은 기능별로 하나의 주 구현만 선택한다. 백테스트·차트·Decimal처럼 핵심 결과를 만드는 라이브러리는 입력 snapshot과 버전을 manifest에 남기고 reference fixture와 결과가 어긋나면 승격을 차단한다.
- 테스트가 만든 프로세스, listener, 임시 DB·디렉터리, bytecode/coverage/build 산출물, 컨테이너, Pod, volume, network와 Kind cluster는 성공·실패·SIGINT/SIGTERM 중단 경로 모두에서 소유 범위 안에서 회수한다. 각 test/check/smoke 실행은 고유 session ID·명시적 temp root·PID/PGID와 process start identity를 가지고 `t.Cleanup`·`defer`·shell `trap`으로 종료하며, 다음 실행은 죽은 owner가 남긴 명시적 Omni-Folio 자원만 선제 회수한다. 종료 후 pre/post inventory에서 owned 잔여물이 하나라도 있으면 테스트를 실패로 처리한다. 새 테스트·스모크·로컬 Kubernetes 검증은 이 cleanup contract에 묶인 wrapper나 같은 수준의 cleanup proof 없이는 완료로 보지 않는다. 현재 테스트는 Podman/Kind 자원을 만들지 않는다. 이를 도입할 때는 생성 직후 session temp root에 exact container/volume/network ID와 Kind cluster name을 기록하고 project·owner·session label을 함께 검증한 뒤 그 실행이 기록한 자원만 회수한다. 공용 label이나 이름 prefix만으로 다른 동시 실행의 자원을 삭제하거나 전역 prune·넓은 경로 삭제를 하지 않는다. SIGKILL·host crash처럼 trap이 실행될 수 없는 경우에도 다음 실행의 stale-owner 회수가 동일한 소유권 증거와 기록된 resource ID로 복구해야 한다.
- 실전 주문은 절대 자동 활성화하지 않는다. 모의투자 검증, 체결-원장 reconciliation, 실패 복구, 사용자 명시 승인 전에는 비활성 상태로 유지한다.
- 자동매매는 `Universe → Signal/Alpha → PortfolioTarget → RiskAdjustedTarget → OrderIntent → Execution` 단계로 분리한다. 전략은 브로커 주문을 직접 만들거나 전송하지 않는다.
- 백테스트 결과를 실전 기대수익으로 표시하지 않는다. 슬리피지, 수수료, 세금, 체결 지연, 데이터 지연, survivorship/lookahead bias를 검증 항목으로 둔다.
- sample·synthetic·fixture 시장 데이터는 API의 machine-readable provenance와 화면의 명시적 문구로 실시간이 아님을 표시한다. 이를 live/current 데이터와 조용히 혼합하거나 실제 broker·market-data 증거로 계산하지 않는다.
- 시장 데이터와 주문의 `(venue,symbol,currency) -> instrument` 관계는 종목 코드 관례나 과거 가격에서 추론하지 않는다. owner-declared append-only listing 원장을 네트워크 전과 신규 저장 transaction에서 검증하고, provider 확인·historical effective dating·valuation/order authority는 별도 gate로 둔다.
- 모든 OHLCV 응답은 `price_adjustment`를 포함한다. `unspecified`는 조정 여부 미확인, `provider_adjusted`는 공급자에게 조정 가격을 요청했다는 뜻으로만 사용하며 기업행사 반영 정확성·freshness·실시간성을 대신 증명하지 않는다.
- 자동 개선은 versioned 전략과 선언된 유한 파라미터 공간만 탐색한다. 실행 중인 전략 소스의 자기 수정, `eval`/동적 코드 실행, LLM이 만든 코드를 검증 없이 실행하는 방식은 사용하지 않는다.
- 각 실험은 불변 데이터 snapshot과 비용·지연 모델에서 시계열 순서를 보존한 train/validation/test 및 walk-forward 평가를 수행하고, 전략·파라미터·데이터·엔진·평가정책 버전과 산출물 hash를 남긴다.
- 후보 선택은 단일 최고 수익률이 아니라 비용 후 수익, 최대 낙폭, 거래 수, turnover/capacity, 구간·시장 국면별 안정성, 기존 champion 대비 개선을 함께 본다. 반복 탐색으로 test set에 과적합하지 않도록 실험 예산과 최종 holdout을 분리한다.
- champion/challenger 승격은 `research_candidate → paper_candidate → paper → shadow`까지만 자동화할 수 있다. 데이터 오류, 성능 저하, paper/backtest 괴리, 위험 한도 위반이 생기면 자동 중지하고 직전 champion으로 롤백한다.
- paper 주문의 완료·진행·결과 미확정 상태를 기록하는 운영 평가는 투자 성과와 분리한다. 자동 중지·롤백은 SELL, 현금, 수수료, 세금, slippage, 지연, durable price mark와 equity curve에서 결정적으로 계산한 versioned 성과 evidence가 준비된 뒤에만 운영 평가를 소비할 수 있다.
- local paper 성과 안전정책 `paper-strategy-performance-safety.v1`은 현재 selection에 귀속된 복구 검증 G3.8D 표본만 소비한다. 같은 selection 표본이 2개 미만이면 `INSUFFICIENT`, `max_drawdown >= 0.1`이면 우선 `HALT_AND_ROLLBACK`, 그 외 `cumulative_return <= -0.05`이면 `HALT_AND_ROLLBACK`, 나머지는 `HOLD`를 append-only로 기록한다. 자동 action은 현재 armed authority 전체의 결정적 halt와 정확한 one-pop strategy rollback을 하나의 transaction에서 수행하고 full replay, cutoff·action provenance, schema/backup/legacy migration proof를 통과해야 한다. 이 값은 실증된 최적값·수익 보장·투자 권유·live threshold가 아니며 scheduler, broker submit, promotion, public API/UI 또는 live authority를 만들지 않는다.
- scheduled paper evaluation/action은 G3.8F1 `paper-run-due` one-shot과 G3.8F2 `paper-run-loop` always-on local runner로 닫는다. F2는 현재 전역 strategy selection과 exact account를 schema v21 singleton lease에 묶고 10초 heartbeat/30초 TTL, 단조 fencing, stale-owner 회수, C3/D/E transaction 내부 exact claim 검증, success/failure/SIGINT/SIGTERM 조건부 release를 증명한다. selection이 account-scoped로 바뀌기 전에는 전역 직렬화한다. 이는 broker runner, credential, alerting, deployment, shadow/live promotion 또는 수익성 주장이 아니다.
- canary 또는 live로의 승격과 실제 자금 확대는 자동화하지 않는다. 별도 owner 승인, broker별 promotion evidence, reconciliation, healthy kill switch와 매 주문 risk gate를 요구한다.
- live 전략은 paper trading에서 일정 기간 검증한 동일한 전략 정의와 동일한 주문 상태 머신만 사용한다. paper/live 환경, API key, 계좌, feature flag를 물리적으로 분리한다.
- 자동매매 hot path는 p50/p95/p99 지연, 시장 데이터 freshness, queue depth, provider latency, 주문 접수/체결 지연, 실패·재시도 횟수를 측정한다.
- 갱신/복구 성능 측정은 이력 크기·표본 수·런타임·cache 상태와 실제 경과 시간을 남긴다. 논리 clock을 가속한 이력 측정은 실제 장기 운영 증거가 아니며, 성능 개선을 위해 소유권·fencing·전체 복구 검증을 생략하지 않는다.
- Flutter 성능 증거는 profile mode, Flutter 버전, 기기·OS 또는 browser, viewport, fixture와 표본 수를 함께 기록하고 build/raster p95를 분리한다. emulator나 web 측정을 physical iOS/Android release 증거로 대체하지 않는다.
- 주문 hot path는 `market data event → freshness check → strategy signal → pre-trade risk → idempotency key → broker submit → ack/execution ingest → ledger reconciliation → audit log`로 고정한다.
- 전략 승격은 `paper → shadow live market data → 소액 canary → limited live` 순서로만 허용한다. shadow mode는 실시간 데이터와 실계좌 상태를 읽되 실제 주문 대신 의도 주문과 위험 판단만 기록한다.
- 시스템 clock drift가 기준을 넘거나 시장 데이터가 stale이면 자동주문을 막는다. 큐가 밀리면 오래된 signal을 늦게 주문하지 않고 버리거나 최신 signal로 병합한다.
- 초기 latency tier는 EOD·분봉 전략이다. 고정된 밀리초 SLA를 먼저 약속하지 말고 실제 p95/p99 측정 뒤 전략별 `max_data_age`, `max_decision_time`, `max_ack_wait` 예산을 정한다. sub-ms HFT, co-location, exchange direct market access는 범위 밖이다.
- `live-enabled`는 앱 토글, 환경변수, runner 시작만으로 활성화할 수 없다. 서버는 만료 있는 owner 승인, broker/account/strategy allowlist, 해당 broker의 promotion evidence, healthy kill switch를 매 주문 직전에 모두 검증하고 하나라도 없으면 fail-closed한다.
- 휴대폰 background task는 opportunistic cache refresh와 push 보조만 한다. 주문 제출, token renewal, reconciliation, runner, kill switch는 always-on 서버 authority에서만 실행한다.

## 확장 가능한 아키텍처 기준

확장성은 처음부터 분산 시스템을 만드는 것이 아니라, 새 공급자를 추가할 때 검증된 코어를 수정하지 않는 것으로 정의한다.

### TDD + DDD + Clean Architecture 운영 원칙

- 기능은 실패하는 도메인 예제부터 시작해 `RED → 최소 GREEN → 리팩터링 → 회귀·race·복원 검증 → 독립 리뷰` 순서로 완성한다. 금액, 주문 권한, 체결, 원장, migration 변경은 실패 증거 없이 구현부터 추가하지 않는다.
- bounded context는 `instrument/listing`, `ledger/portfolio`, `market data`, `order/execution`, `strategy/portfolio construction`, `risk/automation`, `broker integration`으로 구분한다. 서로의 저장 테이블이나 공급자 DTO를 직접 읽지 않고 versioned command, event, query contract로 협력한다.
- 의존성 방향은 `domain → Go stdlib와 검증된 순수 shared kernel만`, `application/use case → domain과 port`, `adapter/infrastructure → port 구현`, `delivery/UI → versioned application contract`로 고정한다. shared kernel은 exact decimal·FIFO primitive처럼 둘 이상의 실제 domain 규칙이 이미 재사용하고 infrastructure를 전혀 참조하지 않을 때만 만들며 production import allowlist로 고정한다. 도메인 규칙은 다른 bounded-context application, Flutter, HTTP, SQLite, broker SDK, 환경변수와 credential을 참조하지 않는다.
- exact 금액·수량 계산, 주문 상태 전이, 목표 수량, 위험 한도, FIFO와 회계 불변식은 가능한 한 순수하고 결정적인 domain 함수로 둔다. transaction, lease/fencing, durable append, retry와 외부 호출 순서는 application use case가 조정하고 SQLite·Kiwoom·Toss 구현은 adapter가 담당한다.
- 테스트 피라미드는 domain 예제/속성 테스트, application use-case 테스트, port 공통 contract test, adapter 통합·migration/restore 테스트, 소수의 Flutter/API E2E로 구성한다. broker adapter는 같은 contract suite를 통과해야 하며 in-memory fake만 통과한 결과를 운영 증거로 승격하지 않는다.
- 현재 시작점은 배포 가능한 modular monolith다. 패키지와 프로세스는 실제 응집도·변경 빈도·성능·장애 격리 증거가 생길 때 경계별로 분리하되, DB transaction을 분산시키거나 network hop을 늘리는 microservice 분리는 측정과 운영 근거 없이는 하지 않는다.
- 인터페이스는 실제 교체 지점과 테스트 seam에만 둔다. 한 구현만 있는 내부 함수에 repository/service/factory 계층을 기계적으로 추가하거나 DDD 이름을 붙인 빈 wrapper를 만들지 않는다.
- 도메인 동작 변경과 대규모 패키지 이동을 한 커밋에 섞지 않는다. characterization/contract test로 현재 동작을 고정한 뒤 의존성 역전과 물리적 모듈 분리를 별도 리팩터링 커밋으로 수행하고, API·DB·backup 호환성을 각각 증명한다.

```text
Flutter client (iOS / Android / app-centric web)
       |
versioned HTTP/SSE contract; decimal strings
       |
Go modular monolith
  ├─ instruments
  ├─ ledger & portfolio
  ├─ market data
  ├─ orders & executions
  ├─ strategy & portfolio construction
  ├─ risk & automation
  └─ watchlists & alerts
       |
       +── research signal/target ingress ← Python research/backtest
       |                                  (no broker credential or order submit)
       |
Go ports
  ├─ BrokerPort
  └─ MarketDataPort
       |
Adapters
  ├─ Kiwoom first / Toss Securities second
  └─ later only when needed: Alpaca / IBKR
Market data adapters
  └─ broker feed / external market-data provider
```

### 실행 프로필과 클라우드 배포 계약

- 하나의 저장소와 pinned non-root OCI image에서 `api`, `migrate` 명령을 제공하고, 해당 단계가 시작될 때만 `worker`, `runner`, `execution-gateway` 역할을 같은 image에 추가한다. 이는 배포 시 프로세스 역할만 나누는 것이며 별도 microservice를 뜻하지 않는다.
- local 프로필은 loopback bind, 단일 SQLite, OS keychain을 사용하고 외부 인증 없이 인터넷에 공개하지 않는다.
- 첫 cloud 프로필은 TLS와 owner 인증, secret manager 주입, 단일 API replica, single-writer provider-managed block volume(RWO)의 SQLite, 암호화된 정기 backup과 restore drill을 요구한다. ephemeral filesystem이나 NFS형 공유·다중 writer volume에 SQLite를 두지 않는다. 이 프로필은 stateful single-node이며 무중단 교체, scale-out, 자동 failover를 보장하지 않는다.
- 애플리케이션의 영속 상태는 데이터베이스와 versioned backup에만 두고 container filesystem은 임시 파일 외에는 사용하지 않는다.
- SQLite backup은 online backup API 또는 동등한 transaction-consistent snapshot으로 off-volume에 저장한다. 실행 중 DB 파일의 단순 복사나 같은 volume의 복사본은 backup으로 인정하지 않으며, restore 후 `integrity_check`, ledger golden test, schema/order-log과 known-good broker-state hash·metadata·replay 증명을 통과해야 한다.
- 두 번째 API replica, API/worker 독립 확장, 무중단 교체, 다중 노드 failover, 높은 동시 쓰기, managed point-in-time recovery 또는 Kubernetes가 필요해지면 먼저 maintenance window에서 PostgreSQL로 승격한다. SQLite export → PostgreSQL import 후 row count, checksum, ledger invariant, order sequence와 restore를 검증하고 SQLite 상태에서 scale-out을 지원한다고 주장하지 않는다.
- schema migration은 startup side effect가 아니라 명시적인 `migrate` 단계로 실행한다. 배포 전 off-volume backup, schema compatibility check, migration 후 ledger golden test, restore 검증을 통과해야 한다.
- liveness는 프로세스 생존만, readiness는 DB 연결·schema version·필수 설정만 확인한다. 브로커 장애는 전체 API를 죽이지 않고 provider별 degraded/freshness 상태로 노출한다.
- HTTP 요청보다 오래 살거나 재시작 복구가 필요한 scheduled sync는 그 요구가 생기는 Phase B부터 `worker` 역할로 분리한다. unattended sync와 paper/shadow/live runner는 owner-managed always-on host에서만 실행한다. runner는 DB lease와 fencing token으로 계좌·전략별 단일 active instance만 허용하고 lease를 잃으면 신규 주문을 fail-closed한다.
- cloud rollout과 rollback 중에는 신규 주문을 먼저 차단하고, 미체결 주문과 broker 상태를 reconciliation한 뒤 runner lease를 넘긴다. 되돌릴 수 없는 migration은 자동 downgrade하지 않고 backup restore 또는 forward-fix 절차를 사용한다.
- PostgreSQL migration·restore drill, stateless API와 DB lease/fencing, OCI hardening/probe/resource 검증, 독립 scaling load evidence, rollout/rollback·secret/RBAC 경계가 모두 증명되기 전에는 Kubernetes manifest를 생성하지 않는다.
- 이 gate를 통과한 뒤 첫 로컬 Kubernetes 검증은 Kind의 Podman provider를 사용한다. 클러스터가 이미 있다고 가정하지 않고, production 배포판은 이 로컬 선택으로 고정하지 않는다.

- 도메인 모듈은 공급자 SDK, HTTP 응답 타입, UI 프레임워크를 직접 참조하지 않는다.
- 각 브로커 응답은 canonical account, instrument, transaction, position, order, execution 모델로 정규화한다.
- 브로커마다 가능한 주문 유형과 시장이 다르므로 `BrokerCapabilities`로 기능을 선언하고 UI도 이를 기준으로 노출한다.
- 공급자별 인증, rate limit, pagination, symbol mapping, 재시도는 해당 adapter 내부에 둔다.
- 주문 상태 머신과 거래 원장을 분리하고 체결 이벤트를 통해 reconciliation한다.
- 전략 정의는 브로커 SDK, credential, 주문 API에 접근하지 않고 `Signal`만 만든다. 포트폴리오 구성기가 여러 전략의 신호와 자금 배분을 `PortfolioTarget`으로 합치고, 공통 risk/execution pipeline만 주문을 만든다.
- 오프라인 신호 제안은 연구 artifact·파라미터·실제로 읽은 입력 snapshot hash를 보존하고 연구 표본 이후 데이터만 대상으로 한다. 신호 없음과 목표 수량 0을 구분한다. hash 일치는 승인·신선도·시장 데이터 완결성 증거가 아니며 Go가 현재 선택·session·closed-bar cutoff·lease/fencing·위험 한도를 독립 검증하기 전에는 실행할 수 없다. 성과 평가 scheduler만 있는 상태를 자동매매 완성으로 보고하지 않는다.
- 주문 admission은 재해시 가능한 제안의 방향을 그대로 신뢰하지 않는다. 동일한 저장 데이터와 등록 파라미터로 검증 가능한 결정만 소비하며, 독립 검산을 사용할 경우 reference 산술과의 동등성 범위를 명시하고 그 밖은 거절한다. 검증된 선택·receipt deadline·주문 기록은 같은 transaction에 묶고 호출자가 만료를 늘리거나 새 원금을 만드는 경로를 허용하지 않는다.
- 로컬 paper 입력도 실제 연구 CSV hash·종목·연구 종료 이후 시점과 신규 snapshot 전체를 Go에서 확인한다. 여러 단계 실행은 사전 검증 실패가 기존 주문 체결로 이어지지 않게 하고, 이미 확정된 단계와 미실행 단계를 구분해 재시도한다. 명시적 1회 arm을 자동 스케줄러의 재활성화 수단으로 사용하지 않는다. 종료 시 현재 owner/fence가 같은 만료 lease를 중지하는 것은 허용하되 takeover한 다른 owner를 중지하지 않는다.
- 입력 크기 제한만으로 파일 열기의 대기 시간을 제한했다고 간주하지 않는다. FIFO처럼 일반 파일이 아닌 입력은 작성자를 기다리지 않고 거절한다. 재시작 시 누락된 체결 가능 봉의 부분체결을 순서대로 따라잡되, 매 체결의 소유권·회계를 검증하고 체결량 증가가 없으면 반복을 종료한다. 체결 따라잡기는 누락 구간 전체의 성과 안전정책 재생을 대신하지 않는다.
- 지속 제안 생성의 출력·flush를 영속 접수나 exactly-once 실행으로 간주하지 않는다. 소비자는 제안 hash와 정확히 같은 원본 CSV byte를 검증하고 재시작·중복·부분 전달을 durable 기록으로 처리해야 한다. latest snapshot polling의 중간 입력 누락 가능성과 공급자의 원자적 파일 교체 전제를 명시하며, 제안 스트림을 단일 파일 수동 실행 경로에 억지로 연결하지 않는다.
- 원본 byte를 담은 전달 envelope는 실행 권한을 포함하지 않는다. Unicode·줄바꿈을 조용히 정규화하지 않고 decoded CSV와 전체 envelope에 각각 byte 상한을 적용한다. 수신 adapter는 기존 연구·선택·SMA·receipt·lease·risk 검증을 재사용하며, 별도 hash나 접수 상태를 추가해 두 번째 실행 정본을 만들지 않는다.
- 장기 paper 실행의 갱신은 현재 owner/fence와 아직 유효한 execution/global lease를 한 transaction에서 확인하고 둘 다 확정된 뒤 토큰을 교체한다. 만료·중지·소유권 상실을 갱신으로 복구하거나 자동 arm하지 않는다. 성과 단계의 global heartbeat만으로 execution lease가 갱신됐다고 판단하지 않는다. append-only 갱신 이력의 증가 비용과 이전 바이너리의 replay 호환성도 검증·문서화한다.
- 연속 paper 입력은 한 프로세스에서 최초 한 번만 명시적 arm하며 프레임 처리와 idle heartbeat를 직렬화한다. bounded LF 프레임·EOF 순서를 보존하고 취소 시 pipe read/channel send를 모두 해제하고 reader를 join한 뒤 최신 소유 토큰으로 정리한다. stdout 대기가 권한 반환을 막지 않게 하며 생산자는 새 입력 없이도 수신기 단절을 감지한다. pipe 전달을 durable 접수·exactly-once 또는 소스 누락 방지로 간주하지 않는다.
- 생산자·소비자 개별 성공만으로 연속 실행을 증명하지 않는다. 실제 pipe와 입력 갱신에서 양쪽 exit status, 확정된 주문·체결·정책 기록과 재연결을 확인한다. 취소된 읽기를 데이터 훼손으로 단정하지 않으며, 오류 redaction 후에도 취소 원인을 보존하고 별도 context로 복구·소유 권한 정리를 검증한다.
- Python 연구 산출물의 수수료·세금·slippage·지연·참여율·신호/체결 시점 계약은 hash 일치만으로 신뢰하지 않는다. Go가 exact field, canonical decimal과 허용 범위를 독립 검증한 산출물만 registry·복구·paper 실행 입력으로 인정한다.
- Capitalized paper 체결은 account-global session과 동일한 execution policy, transaction-owned market sequence cutoff, cutoff 뒤 exact eligible closed bar, current lease/fence를 함께 요구한다. Later-known final volume과 bar open을 쓰는 `paper_bar_open_v1`은 ex-post simulation이며 opening-auction이나 broker/live 체결 증거가 아니다.
- KRX paper target은 whole share로 제한하고 account/symbol당 active order를 하나만 허용한다. Fixed per-fill fee, SELL-only tax와 adverse slippage를 적용한 sole `FILL_RECORDED` journal에서 cash·FIFO lot·실현손익을 replay하며, general ledger나 mutable balance/lot projection을 두 번째 권한으로 만들지 않는다.
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
- 평가 화면은 같은 읽기 transaction에서 검증한 원장 revision·수량·원가·가격 provenance를 하나의 결과로 소비한다. 별도 snapshot과 종목 코드로 합치거나 클라이언트 시계로 평가 authority를 만들지 않는다. 원통화별 합계는 서버가 제공할 때만 표시하고, 누락·모호·stale 가격이면 합계를 숨기며 sample 결과를 현재 계좌 총액이나 live 시세로 승격하지 않는다.
- Java/Kotlin/JVM은 broker SDK 또는 팀·기존 JVM estate가 우세할 때만 Go의 대안으로 재평가한다. Rust는 profiling으로 Go CPU/GC tail bottleneck이 확인된 좁은 component에만 고려하며, Python-only runtime은 주문 authority를 분산하므로 채택하지 않는다.

## 실행 권한과 개선 원칙

- 이 계획을 고정된 체크리스트가 아닌 현재 최선의 기준선으로 취급한다.
- 합의된 제품 목표 안에서 되돌릴 수 있는 로컬 설계·구현·테스트·문서화·로컬 Git 체크포인트는 추천안을 선택해 재승인 없이 계속 진행한다.
- 여러 안전한 대안이 있으면 가장 단순하고 검증 가능한 안을 기본값으로 채택하고, 결정과 근거를 계획 문서에 남긴다.
- 구현 중 결함, 누락된 요구사항, 잘못된 가정, 더 단순하고 안전한 설계가 발견되면 최종 목표에 필요한 범위에서 계획과 구현을 함께 개선한다.
- 새 증권사 연결, 주문 정합성, 데이터 손실 방지, 보안, 접근성, 테스트, migration, 오류 복구에 필요한 작업은 초기 목록에 없더라도 추가한다.
- 증상별 임시 처방보다 모든 호출 경로가 공유하는 원인과 경계에서 수정한다.
- 기존 데이터와 API 계약을 깨는 변경에는 migration, 호환성 처리, rollback 또는 복구 절차를 포함한다.
- 변경할 때마다 관련 테스트와 문서를 함께 갱신하고, 중요한 판단과 남은 위험을 계획 문서에 기록한다.
- 추측성 기능, 관련 없는 리팩터링, 사용되지 않는 추상화는 추가하지 않는다.
- 실제 자금 주문과 live 활성화, 외부 비용, credential 변경, 외부 배포·push·publish, 파괴적·비가역 작업 또는 제품 방향을 바꾸는 큰 범위 확장은 사용자 승인 후 진행한다.
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

- 키움 REST API 국내주식 read-only 연결과 일·분봉 정규화
- 계좌번호, 계좌평가잔고, 미체결, 일·분봉, 실시간 체결·호가를 canonical contract로 정규화
- provider가 권한 scope를 제공하면 read-only 최소 scope만 허용한다. 키움은 공식 OAuth 문서에서 scope를 확인하지 못했으므로 account read `ka00001`/`kt00018`/`ka10075`/`kt00009`와 chart read `ka10080`/`ka10081`만 고정 API-ID·path allowlist로 허용하고, submit method·route 부재를 테스트로 강제한다. `kt00009`는 internal dated scan에서만 호출한다.
- 계좌, 거래, 잔고 pagination 동기화
- rate limit, retry/backoff, token 갱신, freshness 상태
- 브로커 잔고와 원장 잔고 reconciliation 화면

### Phase 3 — 핵심 UI와 차트

- Flutter Overview, Holdings, Asset Detail, Transactions, Import Review, Connections/Settings
- 포트폴리오 가치·손익·현금·성과 추이
- 종목 OHLCV, 거래량, 기간 선택, 평균단가와 매매 마커
- light/dark theme, 모바일 대응, 키보드 탐색, 200% 확대, reduced-motion
- loading, empty, error, partial, stale, success 상태
- 상승·하락을 색상만으로 표현하지 않고 부호와 텍스트를 함께 제공

### Phase 4 — 주문

- 이 단계는 키움 모의투자 주문만 먼저 구현한다. 실전 주문 credential과 live broker submit은 Phase 6의 broker별 promotion gate 전까지 코드 경로와 설정에서 비활성화한다.
- 시장가·지정가, 매수·매도, 수량·예상금액·수수료 확인을 provider capability와 함께 구현한다.
- 접수, 미체결, 부분체결, 체결, 취소, 정정, 거절의 실제 broker mapping과 broker-coupled runner fencing, production risk를 검증한다.
- 재시작·네트워크 단절 후 broker 상태 재조회와 원장 reconciliation을 구현한다. 공식 broker correlation 근거가 없으면 동일 tuple/time 휴리스틱으로 주문번호를 결합하지 않는다.

### Phase 5 — 전략 연구와 모의 자동매매

- LEAN과 NautilusTrader POC: KIS adapter 가능성, Python 전략 경험, backtest/live parity, 운영 복잡도, 라이선스를 비교하고 하나를 재사용할지 최소 내부 엔진을 만들지 기록
- 전략 정의: universe, signal/alpha, portfolio construction, allocation, risk limits, schedule
- 백테스트: 과거 OHLCV 재생, 기업행사, 수수료·세금·슬리피지·부분체결·지연 모델, survivorship/lookahead 방지, out-of-sample·walk-forward 검증
- 결과: CAGR, TWR/XIRR, max drawdown, Sharpe/Sortino, turnover, win rate, exposure, 거래별 감사 로그
- 재현성: strategy/version, parameter hash, data snapshot, engine version, random seed, 실행 환경을 run manifest로 저장
- 자동 개선: 선언된 유한 후보 공간을 정기적으로 평가하고 train/validation/test와 walk-forward gate를 통과한 하나의 challenger를 결정론적으로 선택
- champion registry: 후보·평가정책·데이터 snapshot·산출물 hash·승격/거절 이유를 append-only로 보존하고 paper 성능 저하 시 직전 champion으로 롤백
- 자동 승격 상한: research candidate에서 paper/shadow까지. canary/live 승격과 자금 확대는 owner 승인 없이 수행하지 않음
- paper automation: 전략 신호를 포트폴리오 목표와 risk-adjusted target으로 변환한 뒤 공통 주문 pipeline이 paper order만 실행
- G3.8C1은 계좌별 최초 선택 연구 산출물에서만 starting capital과 execution policy를 파생해 불변으로 보존한다. 이후 전략 변경은 이를 초기화하지 않으며, 이는 현금·체결·성과·자동 권한을 만들지 않는 선행 증거다.
- G3.8C3는 transaction-current order/market cutoff 아래의 account-global paper accounting과 완전한 `paper_fixture` daily-close mark로 cash·equity·return·drawdown을 exact하게 복구한다. G3.8D는 current non-`no_strategy` selection에 귀속되는 strategy-window performance evidence만 별도 append-only로 보존해 선택 전 account movement를 현재 전략 성과로 오인하지 않게 한다. 현재 schema v20/backup v14는 C3/D proof와 G3.8E의 versioned local paper policy·atomic halt/rollback provenance를 함께 검증하며, 이는 수익성, UI, broker truth, deployment 또는 live readiness를 뜻하지 않는다.
- owner-managed always-on host에서 DB lease/fencing으로 단일 runner만 활성화하고 중복 scheduler·중복 주문을 검증. G3.8F1 one-shot과 G3.8F2 always-on local paper policy runner는 C3/D/E idempotent journal과 전역 selection-bound lease를 검증했다. broker-coupled per-order fencing과 배포 환경의 중복 runner 증거는 별도 gate로 남긴다.
- kill switch: 수동 중지, 일일 손실, 연속 실패, stale data, reconciliation mismatch, provider 장애
- Strategy Lab, Backtest Report, Automation Monitor, Risk/Latency 화면: 전략 버전·모드·자금 배분·위험 한도·최근 신호/주문/체결·freshness·kill switch 표시
- buy-and-hold, 단순 리밸런싱, 이동평균 교차는 엔진 검증 fixture로만 제공하고 수익 보장이나 투자 추천으로 표시하지 않음
- 전략별 변경 이력과 실행 로그를 저장해 같은 입력에서 같은 결과가 재현되게 한다.

### Phase 6 — 제한적 실전 자동매매

- 실전 주문 권한은 기본 비활성이고 만료 있는 owner 승인 및 broker·계좌·전략·종목 allowlist를 별도 발급한다. 이 계약과 promotion evidence, healthy kill switch는 매 주문 서버 측에서 재검증한다.
- paper → shadow → 소액 canary → limited live 순서와 각 단계의 자동 중지 기준을 통과한 전략만 승격한다.
- paper/live parity 리포트, reconciliation 통과, 위험 한도, 롤백 절차, 독립 kill switch, 알림 채널이 준비된 전략만 활성화한다.
- 최초 실전 자동매매는 소액, 지정가, 허용 종목 목록, 장중 수동 모니터링과 fail-closed runner를 조건으로 한다.

### Phase 7 — 멀티 증권사 확장

- 토스증권 Open API read-only를 두 번째 어댑터로 추가하고, 공식 별도 sandbox가 확인되지 않으면 주문은 shadow intent까지만 검증
- 필요하면 Alpaca 또는 IBKR read-only/주문 연동
- 워치리스트, 가격 알림, 배당 캘린더, 벤치마크 비교

## 완료 조건

- 같은 거래 파일이나 API 페이지를 반복 적용해도 중복 거래가 생기지 않는다.
- 이전 backup을 현재 schema로 복원한 뒤 보유 수량·현금·손익이 동일하게 재계산된다.
- 입출금, 배당, 수수료, 세금, 환율, 분할이 포함된 골든 데이터에서 수량·현금·손익이 독립 계산과 일치한다.
- 브로커 잔고와 원장 잔고의 차이가 화면에서 설명 가능하다.
- 공급자 timeout, 429, token 만료, 부분 응답 후에도 기존 데이터가 보존되고 재시도가 안전하다.
- 차트는 실제 데이터와 일치하고 가격 조정 기준을 표시하며 모든 기간에서 빈 데이터·지연 데이터 상태를 처리한다.
- 모의주문에서 중복 요청, 부분체결, 취소, 거절, 재시작 복구가 검증된다.
- 실전 주문 경로는 모의주문 검증과 별개로 보안·오주문 방지 체크를 통과한다.
- 전략 백테스트는 수수료·슬리피지·지연·데이터 지연을 포함하고, lookahead bias를 막는 테스트가 있다.
- 자동 개선은 같은 입력에서 같은 후보·순위·산출물 hash를 만들고, 부족한 데이터·최종 holdout 열람·paper gate 실패에서는 승격하지 않는다.
- champion/challenger 이력은 재현 가능하며 candidate가 실패하면 신규 주문 없이 이전 champion 또는 `no_strategy`로 안전하게 롤백한다.
- 동일한 전략 코드·clock 계약·포트폴리오 구성·리스크 코어가 backtest, paper, shadow, live에서 사용된다.
- paper 자동매매는 신호 충돌과 자금 배분, 위험 차단, 주문 상태, 체결 reconciliation, restart recovery, kill switch가 검증된다.
- 각 주문은 market event부터 broker ack/fill까지 UTC timestamp와 monotonic duration을 남기며 구간별 p50/p95/p99, freshness, queue depth를 확인할 수 있다.
- 주문·체결 이벤트는 과부하와 재시작 상황에서도 유실되지 않는 테스트가 있다.
- submit timeout·ack 전후 crash·runner lease 상실·두 runner 동시 기동에서도 중복 주문이 발생하지 않고 신규 주문은 fail-closed한다.
- 두 번째 샘플 전략은 manifest, 전략 모듈, fixture 추가만으로 등록되고 공통 주문·리스크·원장 코드는 변경되지 않는다.
- 핵심 domain 테스트는 SQLite, HTTP server, Flutter, broker SDK 없이 실행되며 exact 회계·주문 상태·위험 불변식을 결정적으로 검증한다.
- application use case는 fake port로 오류·timeout·retry·lease 상실을 검증하고, 실제 SQLite와 broker adapter는 동일 contract suite 및 migration/restore 통합 테스트를 별도로 통과한다.
- 새 broker나 market-data provider는 adapter와 capability/contract test 추가만으로 연결되며 domain/application에 공급자 DTO·TR code·credential 분기가 유입되지 않는다.
- 실전 자동매매는 기본 비활성이고, paper/live parity와 사용자 승인 없이는 어떤 경로에서도 주문을 낼 수 없다.
- API 키 redaction 테스트와 핵심 원장·주문 테스트가 통과한다.
- read-only, paper, live 실행 profile과 secret binding을 분리한다. provider가 scope를 제공하면 최소 권한을 강제하고, 제공하지 않으면 허용 API·route·process authority를 고정해 fail-closed한다.
- Python research와 Flutter client가 broker credential, 운영 DB write, broker submit 경로를 갖지 않으며 live order gate가 매 주문 검증된다.
- 두 번째 브로커는 새 adapter와 공통 contract test 추가만으로 연결할 수 있고 원장·성과·차트·주문 코어의 공급자별 분기가 늘어나지 않는다.
- 지원 화면 크기와 키보드·접근성 검증이 통과한다.
- 동일 image의 local/cloud smoke test, health/readiness, migration, 암호화 backup/restore, 주문 차단형 rollback rehearsal이 통과한다.
- `make test`, `make check`, `make smoke`를 성공·의도적 실패·SIGINT/SIGTERM으로 각각 종료한 뒤 owned 프로세스/listener/temp/coverage/build 자원이 남지 않으며, SIGKILL로 trap을 건너뛴 stale owner·child process group fixture는 다음 실행의 scoped preflight가 회수하고 unrelated 자원은 보존한다. 현재 containerless suite는 Podman/Kind 생성이 0임을 확인하고, 이를 사용하는 테스트가 생기면 session registry에 기록된 exact ID의 성공·실패·중단·stale-owner 회수까지 같은 matrix에 포함한다. cleanup 또는 inventory 자체가 실패하면 해당 검증은 실패다.
- README에 로컬 실행, 단일 노드 cloud 배포, 데이터 백업/복원, API 연결, 모의주문 사용법, 실전 주문 활성화 위험과 절차가 기록된다.

각 단계에서 먼저 현재 코드를 조사하고 가장 작은 수직 슬라이스를 구현한 뒤 테스트로 증명하라. 부분 구현을 전체 완료로 보고하지 말고, 로컬 검증·모의투자 검증·실전 주문 준비·실제 운영 증거를 구분해서 보고하라.
