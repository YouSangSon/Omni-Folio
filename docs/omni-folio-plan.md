<!-- /autoplan restore point: /Users/yousang/.gstack/projects/Omni-Folio/unknown-autoplan-restore-20260823-201622.md -->
# Omni Folio 구현 계획 초안

상태: 제품 전제 부분 확정, D1 실행 게이트 위치 승인 대기
기준일: 2026-08-23

## 목표

한국·미국 주식과 ETF를 여러 계좌에서 통합해 성과를 분석하고, 시세 차트, 단계적으로 안전한 주문 기능, 백테스트와 모의 자동매매를 제공하는 개인용 local-first 투자 앱을 만든다.

## 전제

1. 첫 사용자는 앱 소유자 한 명이다.
2. 초기 릴리스는 조회·원장·차트까지 제공한다. 이후 paper 주문과 자동매매를 검증하고 실전 주문은 원장·체결 정합성과 위험 통제를 통과한 뒤 활성화한다.
3. 국내 주식·미국 주식·ETF만 다루고 옵션, 선물, 암호화폐는 제외한다.
4. 거래 원장이 기준 데이터이며 보유 수량과 성과는 파생한다.
5. 정확성, 개인정보 보호, 복구 가능성을 실시간성보다 우선한다.
6. 자동매매는 기본적으로 연구·백테스트·paper trading이며, 실전 자동매매는 별도 승인과 위험 한도 없이는 활성화하지 않는다.

## 성공 조건

- 같은 거래를 반복 import해도 중복이 생기지 않는다.
- 중간 입출금, 배당, 수수료, 세금, 분할, 환율이 있어도 보유 수량과 손익을 설명할 수 있다.
- TWR, XIRR, 벤치마크 대비 성과가 독립 구현과 골든 데이터로 검증된다.
- 한 공급자가 실패해도 이미 받은 데이터는 보존되고 실패 범위와 마지막 갱신 시각이 보인다.
- API 키와 계좌 식별자가 브라우저, 로그, export에 노출되지 않는다.
- 전략은 백테스트와 paper trading에서 동일한 정의로 실행되고, 위험 한도와 kill switch를 우회하지 못한다.

## 제품 범위

### Phase A: 계산 가능한 로컬 원장

- SQLite schema: accounts, instruments, transactions, lots, prices, fx_rates, corporate_actions, sync_runs
- Decimal money math와 기준 통화
- CSV/manual import: parse, normalize, preview, confirm, apply
- 중복 방지와 append-only correction
- FIFO와 이동평균 원가
- TWR, XIRR, realized/unrealized P&L, drawdown
- JSON/CSV export와 backup/restore

### Phase B: 첫 API 어댑터

- KIS 또는 키움 read-only adapter 하나
- 계좌, 거래, 잔고 동기화와 pagination/cursor
- 단일 시장 데이터 provider의 EOD 가격, FX, 배당, 분할
- provider rate limiter, retry/backoff, freshness state
- reconciliation report: broker positions vs ledger positions

### Phase C: 사용자 화면

- Overview
- Holdings와 Asset Detail
- 종목 상세 가격 차트: 기간 선택, OHLCV, 거래량, 평균단가·매수/매도 표시
- Transactions와 Import Review
- Connections/Settings
- light/dark theme, mobile-first, keyboard navigation
- loading/empty/error/partial/stale/success 상태
- 성과 차트의 텍스트 요약과 표 대안

### Phase D: 모의주문

- 종목 상세의 매수/매도 티켓
- 시장가·지정가, 수량·예상 금액·수수료 확인
- 주문 접수·부분체결·체결·취소·거절 상태
- Alpaca paper trading 또는 국내 증권사 모의투자 환경
- 주문 이벤트와 거래 원장 reconciliation

### Phase E: 전략 연구와 모의 자동매매

- LEAN/NautilusTrader POC 후 재사용 여부 결정; 맞지 않을 때만 최소 이벤트 백테스터 구현
- 전략 pipeline: universe -> signal/alpha -> portfolio target -> risk-adjusted target -> order intent -> execution
- OHLCV 기반 이벤트 백테스트
- 기업행사, 수수료, 세금, 슬리피지, 부분체결, 데이터·주문 지연 모델
- out-of-sample/walk-forward 검증과 survivorship/lookahead 방지 테스트
- 전략·파라미터·데이터·엔진 버전을 고정하는 재현 가능한 run manifest
- paper automation: signal -> portfolio construction -> risk guard -> paper order -> execution reconciliation
- shadow mode: live market data와 실계좌 상태를 읽되 실제 주문 대신 의도 주문과 위험 판단만 기록
- strategy signal -> pre-trade risk -> idempotency key -> broker submit -> execution ingest -> ledger reconciliation -> audit log
- market-data freshness, clock drift, queue depth, p50/p95/p99 latency 측정
- 오래된 market snapshot/signal은 병합할 수 있지만 order/ack/fill/cancel/reject 이벤트는 유실하지 않음
- 종목·가격·주문 금액·총/순 익스포저·포지션·미체결·일일 손실·주문 속도 한도
- 전략 runner와 독립된 fail-closed kill switch
- Strategy Lab, Backtest Report, Automation Monitor, Risk/Latency 화면
- buy-and-hold, 단순 리밸런싱, 이동평균 교차는 엔진 검증 fixture로만 제공하고 투자 추천으로 표시하지 않음

### Phase F: 제한된 실전 자동매매와 확장

- 두 번째 국내 브로커
- 워치리스트와 가격 알림
- 검증 완료 후 KIS/키움 실전 주문 또는 IBKR read-only
- paper -> shadow -> 소액 canary -> limited live 승격과 사용자 승인
- 실전 전략 runner를 UI/API와 별도 프로세스로 격리
- 배당 캘린더와 리밸런싱 편차

## 아키텍처

```text
Web/PWA
  |
Server API
  ├─ Ledger + read models
  ├─ Portfolio calculator
  ├─ Import pipeline
  ├─ Broker adapters
  ├─ Market-data adapters
  ├─ Research/data catalog + backtester
  ├─ Strategy + portfolio construction
  ├─ Risk + execution
  ├─ Automation runner
  └─ Scheduled sync
       |
     SQLite
```

초기에는 모듈러 모놀리스와 하나의 데이터베이스를 사용한다. broker adapter와 market-data adapter만 외부 인터페이스 경계로 두고 backtest/paper/shadow/live는 같은 전략·포트폴리오·리스크 코어와 교체 가능한 clock/data/execution adapter를 사용한다. 주문 command와 ack/fill/cancel/reject는 append-only execution log에 저장한다. 실전 자동매매 전에는 runner만 UI/API와 별도 프로세스로 격리하고 broker credential은 runner가 아닌 실행 gateway만 읽는다. plugin SDK, message broker, Redis, microservice, HFT용 분산 실행 엔진은 만들지 않는다.

## UI 구조

데스크톱은 sidebar + main content, 모바일은 4개 이하 top-level navigation을 사용한다. Overview의 첫 화면에는 total value, invested capital, cash, TWR, XIRR, benchmark delta, freshness를 보여준다. 자동매매 단계에는 Strategy Lab, Backtests, Automation/Risk를 추가하고 현재 실행 모드, 전략 버전, 위험 한도 사용량, 마지막 신호·주문·체결, data freshness, kill switch를 한 화면에서 확인하게 한다. 상승/하락은 색만 사용하지 않고 부호와 텍스트를 함께 쓴다.

React를 선택하면 shadcn/ui와 Tailwind semantic token을 재사용한다. 앱 전체를 dark-only로 만들지 않으며 Noto Sans KR/시스템 글꼴과 tabular numbers를 사용한다.

## 검증

- 원장 invariant: 수량, cash, lot 합계
- import idempotency: 같은 파일 2회 적용
- 계산 골든 케이스: no-flow, deposit, withdrawal, dividend/fee/split
- Portfolio Performance 및 Excel XIRR 교차 검증
- adapter contract test와 녹화 fixture
- API 키 redaction test
- 375/768/1024/1440px, keyboard-only, reduced-motion, 200% zoom
- provider timeout/429/partial response/stale data 복구
- 백테스트 재현성, lookahead 방지, 수수료·슬리피지·지연 모델
- 자동매매 위험 한도, stale data 차단, reconciliation mismatch 차단, kill switch
- paper/shadow/live 공통 전략 코어와 신호 충돌·자금 배분 검증
- 주문/ack/fill/cancel/reject 이벤트의 과부하·재시작 무손실 검증
- hot path 구간별 UTC timestamp, monotonic duration, freshness, queue depth, p50/p95/p99 검증

## 첫 릴리스에서 NOT in scope

- 실전 주문, 실전 자동매매, 주문 추천
- 세금 신고서 생성
- 옵션·선물·암호화폐
- 다중 사용자 SaaS
- LLM 투자 조언
- 고급 최적화, HFT, order book 시뮬레이션

## `/autoplan` Phase 1: CEO 전제 검토

### 외부 검토 상태

- 독립 reviewer CEO 검토: 완료 (`a37b30d`, read-only)
- Codex CLI CEO 검토: 완료 (`a37b30d`, read-only)
- 두 검토 모두 D1을 Phase A scaffold 선행조건이 아니라 무인 자동매매 진입조건으로 옮기라고 권고했다.

### 검토 결론

현재 방향인 개인용·원장 중심·정확성 우선은 타당하다. 차트는 핵심 조회 경험에 포함하고, 주문은 모의주문으로 체결 상태와 원장 정합성을 먼저 검증한 뒤 실전 주문으로 확장한다.

가장 작은 유효 MVP는 다음과 같다.

```text
CSV import -> 중복 검토 -> 거래 원장 -> 보유/현금/손익 -> JSON backup/restore
```

이 접근에서는 FIFO 하나만 먼저 지원하고, 브로커 API 자동 동기화·TWR·벤치마크·drawdown·이동평균 원가는 원장 검증 뒤로 미룬다. 반대로 자동 동기화가 제품의 필수 가치라면 Phase A에 KIS 또는 키움 read-only adapter 하나를 포함해야 한다.

### 사용자 확인 결과

후속 대화에서 제품 방향은 개인용이지만 확장 가능한 멀티 증권사 투자·퀀트 앱으로 확정됐다. Phase A에서 원장을 검증한 뒤 Phase B의 KIS 또는 키움 read-only 연동을 초기 제품 범위에 포함하며, 주문과 자동매매는 paper → shadow → canary → limited live 순서로 확장한다.

## `/autoplan` CEO 재검토: local-first 실행 경계

판정: **D1 미결정. 두 독립 검토는 D1을 무인 자동매매 진입 게이트로 옮기고 로컬 read-only Phase A는 별도로 승인하라고 권고했다.**

### 시스템 감사

- 현재 작업공간은 Git 저장소가 아니며 제품 코드는 없다.
- 현재 자산은 `docs/goal-prompt.md`, 이 계획, 조사 보고서뿐이다.
- 기존 코드, 마이그레이션, 테스트, `TODOS.md`, 디자인 시스템은 아직 없으므로 재사용 가능한 내부 구현은 없다.
- 따라서 지금 가장 값싼 수정은 잘못된 런타임 전제를 코드로 굳히지 않는 것이다.

### 전제 도전

풀어야 할 실제 문제는 “많은 API를 붙이는 것”이 아니라 여러 계좌의 거래와 주문을 한 원장에서 설명하고, 자동화가 위험 한도를 우회하지 못하게 만드는 것이다. 브로커 수나 전략 수는 이 결과를 측정하는 지표가 아니다.

현재 문서의 `local-first`는 데이터 소유권과 단일 사용자 경험을 뜻하지만 노트북 전용 실행을 뜻하는지는 정해지지 않았다. 노트북이 절전·종료되면 scheduled sync, market-data freshness 감시, broker reconnect, 주문 상태 추적, reconciliation, kill switch가 함께 멈춘다. 따라서 무인 자동매매를 최종 목표로 유지하려면 실행 경계를 명시해야 한다.

### 이미 존재하는 해법에서 재사용할 것

| 하위 문제 | 기존 근거 | Omni Folio의 사용 방식 |
|---|---|---|
| 로컬 자산 통합 UX | [Wealthfolio](https://wealthfolio.app/features/investments-tracking/) | 화면 계층과 local-first 신뢰 모델만 참고하고 코드는 복제하지 않는다. |
| KIS 전략 정의와 백테스트 연결 | [KIS 공식 Strategy Builder](https://github.com/koreainvestment/open-trading-api/blob/main/strategy_builder/README.md) | `.kis.yaml`, signal, backtest 흐름과 공식 fixture를 POC 입력으로 사용한다. 공식 샘플을 실전 안전성 증명으로 간주하지 않는다. |
| paper/live 분리 | [Alpaca 인증 문서](https://docs.alpaca.markets/us/v1.1/docs/authentication-1) | endpoint, credential, account, feature flag를 물리적으로 분리한다. |
| paper 한계 | [Alpaca Paper Trading](https://docs.alpaca.markets/us/v1.4.2/docs/paper-trading) | paper 결과만으로 live 승격하지 않고 shadow·canary·reconciliation을 유지한다. |

### 12개월 목표와 현재 계획의 차이

```text
CURRENT
  문서와 검증된 레퍼런스만 존재
       |
       v
THIS PLAN
  local ledger + read-only broker + charts
  -> paper order -> paper/shadow automation
       |
       v
12-MONTH IDEAL
  owner-controlled data
  + reproducible research
  + broker-neutral risk/execution
  + always-on private runner
  + live disabled unless every promotion gate passes
```

### D1 구현 접근 대안

#### A. 단일 Mac local-only

- Completeness: **6/10**
- 장점: localhost, SQLite, macOS Keychain만 사용하므로 인증·배포·운영 표면이 가장 작다.
- 단점: Mac이 잠들거나 꺼지면 무인 paper/shadow/live 자동화와 안전 감시도 중단된다.
- 적합 범위: 원장, 조회, 차트, 수동 연구, 앱을 열어 둔 동안의 paper 실행.

#### B. 단계적 hybrid — 권장

- Completeness: **10/10**
- 장점: 초기 원장·조회·연구는 개인 PC에서 단순하게 시작하고, 무인 자동화가 필요해질 때 동일 모듈을 owner-managed always-on private host에서 실행한다.
- 장점: runner는 주문 credential 없이 의도 주문만 만들고 execution gateway만 credential을 읽도록 분리할 수 있다.
- 단점: 자동매매 단계부터 로컬 UI와 private host 사이의 인증, 암호화 백업, 배포·패치·복구 책임이 생긴다.

#### C. 처음부터 server-first self-hosted PWA

- Completeness: **8/10**
- 장점: scheduled sync와 자동화가 처음부터 노트북 상태와 분리된다.
- 단점: 읽기 전용 MVP부터 TLS, 원격 인증, secret 관리, 백업, 패치, 장애 대응을 제품 범위에 넣어야 한다.

### 추천과 미결정 사항

추천은 **B. 단계적 hybrid**다. `local-first`를 “데이터 소유권과 기본 사용 경험”으로 정의하고, laptop-only와 동일시하지 않는다. 무인 paper, shadow, live 자동매매는 사용자가 관리하는 always-on private host가 있을 때만 활성화한다.

아직 결정하지 않아도 되는 항목은 home server 대 VPS, 구체적인 원격 접속 방식, 실전 계좌·종목·자본·손실 한도다. 이 항목들은 각각 자동매매와 live 승격 직전의 별도 보안 게이트에서 결정한다.

### CEO 독립 시각 합의표

| 검토 차원 | 독립 reviewer | Codex CLI | 합의 상태 |
|---|---|---|---|
| 제품 중심 | 설명 가능한 개인 원장 | owner-controlled reconciliation layer | **CONFIRMED** |
| D1 위치 | Phase A를 막지 말고 자동화만 차단 | unattended paper/shadow 직전으로 이동 | **CONFIRMED** |
| 첫 수직 슬라이스 | local import·원장·snapshot | import·차이 설명·수정·snapshot | **CONFIRMED** |
| 실제 데이터 검증 | 명시된 전제와 fixture 필요 | 실제 명세·CSV/API 적합성 선검증 | **CONFIRMED** |
| 자동매매 로드맵 | 단계적 목표로 유지 가능 | 별도 제품 결정으로 격리 권고 | **DISAGREE** |
| 오픈소스 활용 | fixture·UX acceptance로 변환 | 실제 데이터 bake-off를 자체 개발 선행 게이트로 요구 | **DISAGREE** |

### 합의된 보강 후보 — 아직 미반영

다음 항목은 두 검토가 모두 필요하다고 봤지만 사용자 승인 전에는 목표나 범위에 확정하지 않는다.

- 모든 평가액·손익·현금 숫자에서 원 거래, FX, 수수료, 세금, 기업행사, correction으로 drill-down하는 “왜 이 숫자인가” 흐름
- Phase B read-only credential만 허용하고 주문 scope가 있는 credential은 명시적으로 거절하는 capability 검사와 테스트
- `schema_migrations`, backup format version, 과거 backup restore 후 원장 재계산 golden test
- 두 번째 live adapter를 조기에 만들지 않되, 서로 다른 두 브로커 CSV/응답 fixture로 canonical model을 Phase A에서 검증
- Alpaca paper 결과는 KIS·키움 실전 안전성을 증명하지 않는다는 broker-specific promotion gate

### User Challenge — 자동매매를 같은 제품 로드맵에 둘 것인가

사용자가 정한 방향은 원장, 차트, 주문, 퀀트, paper/shadow/canary/live 자동매매를 하나의 확장 가능한 앱에서 단계적으로 제공하는 것이다. Codex CLI는 포트폴리오 기록 앱과 무인 자동매매 플랫폼의 행동·경쟁·실패 비용이 다르므로 자동매매를 별도 제품 결정으로 격리하라고 권고했다. 독립 reviewer는 공통 원장과 risk/execution 경계를 유지하면 같은 단계적 로드맵 안에 둘 수 있다고 봤다.

사용자의 원래 방향을 기본값으로 유지한다. 자동매매는 목표에서 제거하지 않으며, 별도 제품으로 분리하려면 사용자의 명시적 승인이 필요하다.

**UNRESOLVED D1:** 권장안은 실행 형태 결정을 Phase E의 무인 자동매매 진입 게이트로 옮기고, Phase A는 단일 로컬 SQLite·수동 실행·실제 주문 불가 조건으로 진행하는 것이다. 이 게이트 위치 변경과 local Phase A 착수에는 사용자 답변이 필요하다.
