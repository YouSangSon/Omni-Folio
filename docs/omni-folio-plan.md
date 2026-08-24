# Omni Folio 구현 계획

상태: G0·G1·G3 로컬 통과, G2 build/widget/browser·자동 accessibility/reduced-motion 증거 확보 및 physical profile/screen-reader 증거 보강 중
기준일: 2026-08-24

## 목표

한국·미국 주식과 ETF를 여러 계좌에서 통합해 성과를 분석하고, 시세 차트, 단계적으로 안전한 주문 기능, 백테스트와 자동 개선되는 모의 자동매매를 제공하는 개인용 local-first 투자 앱을 만든다. 하나의 Flutter 코드베이스로 iOS·Android·app-centric web을 제공하고, 서버 artifact는 로컬 단독 실행과 사용자가 관리하는 단일 노드 클라우드 배포를 모두 지원한다.

## 전제

1. 첫 사용자는 앱 소유자 한 명이다.
2. 초기 릴리스는 조회·원장·차트까지 제공한다. 이후 paper 주문과 자동매매를 검증하고 실전 주문은 원장·체결 정합성과 위험 통제를 통과한 뒤 활성화한다.
3. 국내 주식·미국 주식·ETF만 다루고 옵션, 선물, 암호화폐는 제외한다.
4. 거래 원장이 기준 데이터이며 보유 수량과 성과는 파생한다.
5. 정확성, 개인정보 보호, 복구 가능성을 실시간성보다 우선한다.
6. 자동매매는 기본적으로 연구·백테스트·paper trading이며, 실전 자동매매는 별도 승인과 위험 한도 없이는 활성화하지 않는다.
7. `local-first`는 데이터 소유권과 로컬 실행 가능성을 뜻하며 laptop-only를 뜻하지 않는다.
8. 첫 구현 lake는 로컬 SQLite와 수동 실행으로 시작하고, unattended paper/shadow/live는 owner-managed always-on host에서만 활성화한다.
9. 클라이언트와 Python 연구 프로세스는 원장·주문 권한자가 아니다. broker credential과 order-submit 권한은 Go 실행 경계에만 둔다.
10. 자동 개선은 versioned 후보와 파라미터를 평가하는 연구·페이퍼 루프이며, 실행 코드 자기 수정이나 자동 live 승격을 뜻하지 않는다.
11. 신뢰할 수 있는 라이브러리는 검증 후 사용한다. 의존성 수를 목표로 줄이지 않고 핵심 결과의 재현성·license·보안·업데이트 비용을 기준으로 선택한다.
12. 첫 브로커는 키움, 두 번째 브로커는 토스증권이다. 제품 UX는 토스의 쉬운 흐름을 원칙으로 참고하되 화면·상표를 복제하지 않는다.

## 성공 조건

- 같은 거래를 반복 import해도 중복이 생기지 않는다.
- 중간 입출금, 배당, 수수료, 세금, 분할, 환율이 있어도 보유 수량과 손익을 설명할 수 있다.
- TWR, XIRR, 벤치마크 대비 성과가 독립 구현과 골든 데이터로 검증된다.
- 한 공급자가 실패해도 이미 받은 데이터는 보존되고 실패 범위와 마지막 갱신 시각이 보인다.
- API 키와 계좌 식별자가 브라우저, 로그, export에 노출되지 않는다.
- 전략은 백테스트와 paper trading에서 동일한 정의로 실행되고, 위험 한도와 kill switch를 우회하지 못한다.
- 자동 개선 run은 재현 가능하고 시계열 holdout을 오염시키지 않으며, 검증 실패 후보가 champion이나 live 전략을 덮어쓰지 못한다.
- 같은 Go OCI artifact가 local과 단일 노드 cloud 프로필에서 실행되고, Flutter iOS·Android·web 산출물이 같은 versioned contract를 사용한다.
- backup/restore와 주문 차단형 rollback이 검증된다.

## 현재 구현 lake: G0-G3

기존 Phase A-C의 순차 UI 가정은 모바일 요구가 확정되기 전 기록이다. 현재는 전체 제품을 미리 scaffold하지 않고 다음 한 흐름만 end-to-end로 만든다.

```text
contracts fixture
  -> Go CSV preview
  -> idempotent atomic apply
  -> ledger snapshot + receipt
  -> Flutter trust/import 화면

same market fixture
  -> Python deterministic backtest manifest
```

- `apps/client`: Flutter iOS·Android·web 최소 앱
- `services/core`: Go API와 SQLite 원장
- `services/research`: Python CLI 백테스트
- `contracts`: OpenAPI·JSON Schema·공통 fixture
- `infra`: 로컬 process와 OCI/Compose만, Kubernetes manifest는 만들지 않음

상세 gate와 현재 상태는 [`GATES.md`](../GATES.md), [`PLAN.md`](../PLAN.md), [`docs/adr/0001-runtime-and-monorepo.md`](adr/0001-runtime-and-monorepo.md)를 따른다.

키움·토스의 공식 API 기준선, 구현 순서, Toss-inspired UX 경계는 [`docs/broker-priority-and-ux.md`](broker-priority-and-ux.md)를 따른다.

Parallax, Mimir, akasha에서 가져올 패턴과 제외한 범위는 [`docs/reuse-audit.md`](reuse-audit.md)에 revision·license와 함께 기록한다. 현재 slice에는 기존 요구를 직접 닫는 loopback/readiness/no-lookahead 패턴만 적용하고 세 프로젝트의 프레임워크는 복제하지 않는다.

## 제품 범위

### Phase A: 계산 가능한 로컬 원장

- SQLite schema: accounts, instruments, transactions, import_runs, schema_migrations
- Decimal money math, KRW 기본 기준 통화, 거래 원 통화 보존
- CSV import: parse, normalize, preview token, confirm, atomic apply receipt
- 중복 방지와 append-only correction
- FIFO 보유 수량, 현금, 실현 손익과 구조화된 계산 provenance
- versioned JSON backup, temp DB restore, 이전 backup restore golden test
- 서로 다른 두 브로커 CSV/응답 fixture로 canonical model 검증; 두 번째 live adapter는 구현하지 않음

Phase A 코어가 green이 된 뒤 같은 단계의 후속 slice에서 수동 입력, prices/fx_rates/corporate_actions, 미실현 손익, TWR/XIRR, benchmark, drawdown, 이동평균 원가, CSV export를 하나씩 추가한다. 이 후속 항목을 현재 코어 구현에 미리 scaffold하지 않는다.

### Phase B: 첫 API 어댑터

- 키움 REST API 국내주식 read-only adapter
- `ka00001`, `kt00018`, `ka10075`, 일·분봉과 실시간 체결·호가의 canonical mapping
- K1 synthetic candle boundary: `POST /api/dostk/chart`의 `ka10080`/`ka10081`, KRX 6자리 symbol, `1d`와 `1/3/5/10/15/30/45/60m`; 가격 부호는 magnitude로 정규화하고 decimal OHLCV와 nonnegative volume을 보존한다. page는 UTC ascending으로 정렬하고 동일 overlap은 dedupe, 값 충돌은 거절하며 newest 500개로 제한한다. `upd_stkpc_tp=1`은 내부 `provider_adjusted` provenance로만 노출한다.
- official candle timestamp timezone은 명시되지 않아 Asia/Seoul을 운영 가정으로 사용한다. 이 합성 계약은 credential, broker request, current/fresh data, public route, persistence, adjustment-event correctness, realtime 또는 주문 capability를 증명하지 않는다.
- 계좌, 거래, 잔고 동기화와 pagination/cursor
- 단일 시장 데이터 provider의 EOD 가격, FX, 배당, 분할
- provider rate limiter, retry/backoff, freshness state
- reconciliation report: broker positions vs ledger positions
- read-only scope credential만 허용하고 주문 scope credential은 이 단계에서 거절

### Phase C: 사용자 화면

- Overview
- Holdings와 Asset Detail
- 종목 상세 가격 차트: 기간 선택, OHLCV, 거래량, 평균단가·매수/매도 표시
- Transactions와 Import Review
- Connections/Settings
- light/dark theme, mobile-first, keyboard navigation
- loading/empty/error/partial/stale/success 상태
- 성과 차트의 텍스트 요약과 표 대안
- 모든 평가액·손익·현금 숫자에서 원 거래, FX, 수수료, 세금, 기업행사, correction으로 내려가는 `왜 이 숫자인가` 흐름

### Phase D: 모의주문

- 현재 K2A 통과 범위는 Go 내부 합성 `LIMIT`/`KRW`/`KRX` intent/event log와 unknown-submit replay다. 현재 backup v4는 이 order-state proof와 K2C authority/reservation proof를 함께 검증한다. 아래 제품·broker 항목은 K2B 이후 범위다.
- 종목 상세의 매수/매도 티켓
- 시장가·지정가, 수량·예상 금액·수수료 확인
- 주문 접수·부분체결·체결·취소·거절 상태
- 키움 모의투자 환경
- 주문 이벤트와 거래 원장 reconciliation

### Phase E: 전략 연구와 모의 자동매매

- LEAN/NautilusTrader POC 후 재사용 여부 결정; 맞지 않을 때만 최소 이벤트 백테스터 구현
- 전략 pipeline: universe -> signal/alpha -> portfolio target -> risk-adjusted target -> order intent -> execution
- OHLCV 기반 이벤트 백테스트
- 기업행사, 수수료, 세금, 슬리피지, 부분체결, 데이터·주문 지연 모델
- out-of-sample/walk-forward 검증과 survivorship/lookahead 방지 테스트
- 전략·파라미터·데이터·엔진 버전을 고정하는 재현 가능한 run manifest
- 유한한 후보 공간의 자동 탐색과 결정론적 champion/challenger 선택
- 비용 후 수익·최대 낙폭·거래 수·turnover/capacity·구간 안정성을 함께 보는 versioned promotion policy
- `research_candidate -> paper_candidate -> paper -> shadow` 자동 승격과 실패 시 이전 champion 또는 `no_strategy` 롤백
- 최종 holdout과 실험 예산 분리; 동적 코드 실행·자기 수정·자동 canary/live 승격 금지
- paper automation: signal -> portfolio construction -> risk guard -> paper order -> execution reconciliation
- shadow mode: live market data와 실계좌 상태를 읽되 실제 주문 대신 의도 주문과 위험 판단만 기록
- strategy signal -> pre-trade risk -> idempotency key -> broker submit -> execution ingest -> ledger reconciliation -> audit log
- market-data freshness, clock drift, queue depth, p50/p95/p99 latency 측정
- 오래된 market snapshot/signal은 병합할 수 있지만 order/ack/fill/cancel/reject 이벤트는 유실하지 않음
- 종목·가격·주문 금액·총/순 익스포저·포지션·미체결·일일 손실·주문 속도 한도
- 전략 runner와 독립된 fail-closed kill switch
- Strategy Lab, Backtest Report, Automation Monitor, Risk/Latency 화면
- buy-and-hold, 단순 리밸런싱, 이동평균 교차는 엔진 검증 fixture로만 제공하고 투자 추천으로 표시하지 않음
- owner-managed always-on host에서 DB lease와 fencing token으로 계좌·전략별 단일 active runner만 허용

### Phase F: 제한된 실전 자동매매와 확장

- 토스증권 Open API read-only를 두 번째 브로커로 추가
- 워치리스트와 가격 알림
- 검증 완료 후 키움 제한적 실전 주문 또는 IBKR read-only
- paper -> shadow -> 소액 canary -> limited live 승격과 사용자 승인
- 실전 전략 runner를 UI/API와 별도 프로세스로 격리
- broker별 credential·계좌·capability·promotion gate를 분리하고 다른 broker의 paper 결과를 실전 안전성 증명으로 재사용하지 않음
- 배당 캘린더와 리밸런싱 편차

## 아키텍처

```text
apps/client (Flutter: iOS / Android / web)
       |
       | versioned HTTP/SSE; canonical decimal strings
       v
services/core (Go modular monolith)
  ledger | import | portfolio | broker | order | risk
       |
       +-- SQLite single writer: local/single-node
       +-- PostgreSQL: before multi-replica/Kubernetes

services/research (Python batch/CLI)
  backtest | analysis | reproducible artifacts
  no broker credential | no order submit | no operational DB writes

Later roles from the same Go codebase
  worker | runner | execution-gateway
  DB claim/lease/fencing remains authoritative
```

초기에는 Go 모듈러 모놀리스와 하나의 데이터베이스를 사용한다. Flutter와 Python은 wire contract만 공유하고 Go 내부 package나 운영 DB에 직접 의존하지 않는다. broker adapter와 market-data adapter만 외부 인터페이스 경계로 두며 주문 command와 ack/fill/cancel/reject는 append-only execution log에 저장한다. Python 전략은 versioned signal/target만 만들고 모든 pre-trade risk와 broker submit은 Go 경계를 통과한다. 실전 자동매매 전에는 execution gateway만 live credential을 읽는다. plugin SDK, message broker, Redis, microservice, HFT용 분산 실행 엔진은 만들지 않는다.

### 클라우드 준비 계약

- local과 cloud는 같은 저장소와 pinned non-root Go OCI image를 사용한다. Flutter native/web은 별도 서명·배포 산출물이다. 첫 lake는 `api`와 `migrate`만 사용하고, 해당 단계가 시작될 때 `worker`, `runner`, `execution-gateway` command를 같은 Go image에 추가한다.
- local 프로필은 loopback + SQLite + OS keychain이다. 첫 cloud 프로필은 TLS + owner 인증 + secret manager + 단일 API replica + single-writer provider-managed block volume(RWO)의 SQLite다. 이는 stateful single-node 프로필이며 무중단 교체·scale-out·자동 failover를 보장하지 않는다.
- container filesystem은 임시 파일만 허용한다. SQLite를 ephemeral disk, NFS형 공유 volume, 다중 writer에 두지 않는다. backup은 SQLite online backup API 또는 동등한 일관된 snapshot으로 off-volume에 암호화 저장하고 restore 후 `integrity_check`, ledger golden test와 schema/order-log hash·replay 증명을 통과한다.
- 두 번째 API replica, API/worker 독립 확장, 무중단 교체, 다중 노드 failover, 높은 동시 쓰기, managed point-in-time recovery가 필요해지면 PostgreSQL로 먼저 승격한다. SQLite export → PostgreSQL import 후 row count, checksum, ledger invariant를 검증하고 SQLite 상태에서는 scale-out을 지원한다고 주장하지 않는다.
- migration은 `migrate` one-shot 단계로 실행하고 readiness는 DB 연결과 schema version 불일치를 fail-closed한다. liveness는 외부 broker 장애와 분리한다.
- HTTP 요청보다 오래 살거나 재시작 복구가 필요한 scheduled sync는 Phase B부터 `worker` 역할로 분리한다. Phase E부터 automation runner는 DB lease/fencing으로 계좌·전략별 단일 active owner만 허용한다. lease를 잃은 runner는 신규 주문을 만들지 못한다.
- execution gateway는 submit 전에 unique `client_order_id`/idempotency key를 durable하게 기록하고 모든 order intent의 fencing token을 DB의 현재 owner token과 비교한다. token이 다르면 broker submit 전에 거절한다. timeout·crash 후 결과가 불명확하면 재주문하지 않고 broker 조회와 reconciliation으로 상태를 확정한다.
- 배포·rollback은 신규 주문 차단 → 미체결/reconciliation 확인 → runner lease 이전 → migration/app 전환 순서로 수행한다. 되돌릴 수 없는 migration은 자동 downgrade하지 않는다.
- Kubernetes, Redis, Kafka, service mesh는 이 계약을 만족하는 데 필요하지 않으므로 측정된 요구가 생길 때까지 추가하지 않는다.

## UI 구조

데스크톱 sidebar와 모바일 navigation은 `Home / Holdings / History / Connections` 네 영역을 공유한다. History 아래에 Transactions와 Import Review를, Connections 아래에 broker 연결, Export, Backup/Restore를 둔다. Asset Detail과 `계산 근거 보기`는 contextual route이며 Settings는 보조 경로다. 아직 활성화되지 않은 broker, 성과 지표, 주문, 자동화 메뉴는 placeholder로 노출하지 않는다.

Home의 첫 화면은 “얼마 벌었나?”보다 “현재 데이터가 믿을 만한가?”에 먼저 답한다. 검증·freshness 상태, 해결할 문제, 보유/현금 snapshot, 최근 import, 최근 verified backup 순서로 보여주고 TWR/XIRR/benchmark는 해당 데이터와 계산이 실제로 준비된 뒤 추가한다. 자동매매 단계에는 Strategy Lab, Backtests, Automation/Risk를 capability gate 뒤에 추가하고 현재 실행 모드, 전략 버전, 위험 한도 사용량, 마지막 신호·주문·체결, data freshness, kill switch를 한 화면에서 확인하게 한다. 상승/하락은 색만 사용하지 않고 부호와 텍스트를 함께 쓴다.

Flutter Material primitive와 semantic design token을 사용한다. 토스증권에서 쉬운 용어, 한 화면 한 결정, 점진적 상세 공개, 강한 숫자 위계, 국내·미국의 일관된 흐름을 참고하되 trade dress·브랜드 자산·정확한 layout과 motion은 복제하지 않는다. 앱 전체를 dark-only로 만들지 않으며 Noto Sans KR/시스템 글꼴, tabular numbers, 48dp touch target, 명시적인 focus/semantic label을 사용한다. 대량 CSV 변환과 시계열 downsampling은 서버가 수행하고 Flutter web main thread에 올리지 않는다.

## 검증

- 원장 invariant: 수량, cash, lot 합계
- import idempotency: 같은 파일 2회 적용
- 계산 골든 케이스: no-flow, deposit, withdrawal, dividend/fee/split
- Portfolio Performance 및 Excel XIRR 교차 검증
- adapter contract test와 녹화 fixture
- API 키 redaction, credential scope 거절, read-only/paper/live secret 분리 test
- Flutter iOS·Android·web build, compact/medium/expanded layout, keyboard-only, screen reader, reduced-motion, 200% text scale
- provider timeout/429/partial response/stale data 복구
- 백테스트 재현성, lookahead 방지, 수수료·슬리피지·지연 모델
- 자동 후보 순위의 결정성, holdout 격리, 최소 데이터·거래 수, 과적합/성능 저하 시 승격 거절과 champion 롤백
- 자동매매 위험 한도, stale data 차단, reconciliation mismatch 차단, kill switch
- 두 runner 동시 기동, lease 상실, submit timeout, ack 전후 crash에서 중복 주문 방지와 fail-closed 복구
- SQLite online backup의 off-volume restore, `integrity_check`, 이전 schema ledger golden test와 order-log hash·replay
- PostgreSQL 승격 시 SQLite export/import row count·checksum·ledger invariant 일치
- paper/shadow/live 공통 전략 코어와 신호 충돌·자금 배분 검증
- 주문/ack/fill/cancel/reject 이벤트의 과부하·재시작 무손실 검증
- hot path 구간별 UTC timestamp, monotonic duration, freshness, queue depth, p50/p95/p99 검증

## 첫 릴리스에서 NOT in scope

- 실전 주문, 실전 자동매매, 주문 추천
- 세금 신고서 생성
- 옵션·선물·암호화폐
- 다중 사용자 SaaS
- LLM 투자 조언
- 무제한 black-box 최적화, 전략 코드 자기 수정, 자동 canary/live 승격, HFT, order book 시뮬레이션

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

이 접근에서는 FIFO 하나만 먼저 지원하고, 브로커 API 자동 동기화·TWR·벤치마크·drawdown·이동평균 원가는 원장 검증 뒤로 미룬다. 검토 당시 첫 브로커 후보는 KIS/키움으로 열어 뒀으며, 2026-08-24 사용자 결정으로 키움이 첫 어댑터가 됐다.

### 사용자 확인 결과

후속 대화에서 제품 방향은 개인용이지만 확장 가능한 멀티 증권사 투자·퀀트 앱으로 확정됐다. Phase A에서 원장을 검증한 뒤 Phase B는 키움 read-only와 키움 모의주문을 진행하고, 그 다음 토스증권 Open API를 두 번째 어댑터로 추가한다. 주문과 자동매매는 paper → shadow → canary → limited live 순서로 확장한다.

## `/autoplan` CEO 재검토: local-first 실행 경계

판정: **D1 제품 전제와 권장 아키텍처 승인 완료. Phase A는 로컬 read-only로 진행하고, cloud/always-on 실행은 무인 자동매매 진입 게이트로 둔다. 합의된 범위의 가역적인 로컬 작업은 추천안을 자동 채택해 진행한다.**

### 시스템 감사

- 이 CEO 검토 당시 작업공간은 `main` 브랜치의 Git 저장소이고 제품 코드는 없었다. 현재는 첫 수직 슬라이스가 구현되어 이 상태 기록을 supersede한다.
- 이 CEO 검토 시점의 제품 자산은 `docs/goal-prompt.md`, 이 계획, 조사 보고서뿐이었다.
- 검토 시점에는 코드, 마이그레이션, 테스트, `TODOS.md`, 디자인 시스템이 없어 재사용 가능한 내부 구현도 없었다. 현재 첫 수직 슬라이스 상태는 문서 상단과 Phase 3 evidence가 우선한다.
- 따라서 지금 가장 값싼 수정은 잘못된 런타임 전제를 코드로 굳히지 않는 것이다.

### 전제 도전

풀어야 할 실제 문제는 “많은 API를 붙이는 것”이 아니라 여러 계좌의 거래와 주문을 한 원장에서 설명하고, 자동화가 위험 한도를 우회하지 못하게 만드는 것이다. 브로커 수나 전략 수는 이 결과를 측정하는 지표가 아니다.

초기 검토 당시 `local-first`는 데이터 소유권과 단일 사용자 경험을 뜻했지만 노트북 전용 실행인지가 불명확했다. 노트북이 절전·종료되면 scheduled sync, market-data freshness 감시, broker reconnect, 주문 상태 추적, reconciliation, kill switch가 함께 멈추므로 D1에서 단계적 hybrid 실행 경계를 확정했다.

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

### 합의된 보강 사항

다음 항목은 두 검토의 합의와 사용자의 지속 개선 요청에 따라 계획 본문과 완료 조건에 반영한다.

- 모든 평가액·손익·현금 숫자에서 원 거래, FX, 수수료, 세금, 기업행사, correction으로 drill-down하는 “왜 이 숫자인가” 흐름
- Phase B read-only credential만 허용하고 주문 scope가 있는 credential은 명시적으로 거절하는 capability 검사와 테스트
- `schema_migrations`, backup format version, 과거 backup restore 후 원장 재계산 golden test
- 두 번째 live adapter를 조기에 만들지 않되, 서로 다른 두 브로커 CSV/응답 fixture로 canonical model을 Phase A에서 검증
- Alpaca paper 결과는 KIS·키움 실전 안전성을 증명하지 않는다는 broker-specific promotion gate

### User Challenge — 자동매매를 같은 제품 로드맵에 둘 것인가

사용자가 정한 방향은 원장, 차트, 주문, 퀀트, paper/shadow/canary/live 자동매매를 하나의 확장 가능한 앱에서 단계적으로 제공하는 것이다. Codex CLI는 포트폴리오 기록 앱과 무인 자동매매 플랫폼의 행동·경쟁·실패 비용이 다르므로 자동매매를 별도 제품 결정으로 격리하라고 권고했다. 독립 reviewer는 공통 원장과 risk/execution 경계를 유지하면 같은 단계적 로드맵 안에 둘 수 있다고 봤다.

사용자의 원래 방향을 기본값으로 유지한다. 자동매매는 목표에서 제거하지 않으며, 별도 제품으로 분리하려면 사용자의 명시적 승인이 필요하다.

**CONFIRMED D1:** 단계적 hybrid를 사용한다. Phase A는 단일 로컬 SQLite·수동 실행·실제 주문 및 live-capable credential 불가 조건으로 진행한다. 동일 artifact의 단일 노드 cloud 배포 경로는 유지하되, unattended paper/shadow/live는 owner-managed always-on host, 인증·TLS·secret manager·durable backup·singleton runner gate를 통과해야 한다. 구체적인 cloud vendor와 home server/VPS 선택은 배포 직전 결정한다.

## `/autoplan` Phase 1 최종 종합: 현재 구현 lake

모드: **SELECTIVE_EXPANSION**. 앞선 Codex CLI 검토와 최신 독립 reviewer 검토를 종합한다. 사용자의 제품 방향 승인과 자동 진행 지시로 Phase A 전제는 모두 닫혔다.

### 0A-0F 전제·대안·시간축 결정

| 항목 | 검토 결과 | 결정 |
|---|---|---|
| 실제 문제 | 여러 API 연결 자체가 아니라 거래·현금·손익의 설명 가능성과 안전한 자동화 경계 | 거래 원장을 source of truth로 유지 |
| 현재 자산 | 목표·계획·조사 문서만 있고 제품 코드는 없음 | 내부 코드를 재사용한다고 가정하지 않음 |
| 12개월 목표 | 개인 원장 → read-only 연동·차트 → paper/shadow → gated live | 로드맵은 유지하되 지금은 Phase A만 구현 |
| 구현 대안 | 원장 코어만, 최소 UI 포함, cloud scaffold 우선 | 원장 코어를 먼저 완성하고 UI는 그 위에 얹음 |
| Hour 1-3 | schema, 거래 의미, Decimal, import/apply가 가장 큰 모호성 | FIFO와 단일 기준 통화 계약부터 고정 |
| Hour 4+ | restore·오류·중복·골든 fixture가 구현 완료를 좌우 | 원자적 apply와 검증 후 restore를 ship gate로 둠 |

```text
CURRENT                 PHASE A                         12-MONTH IDEAL
docs only  ──>  CSV preview/apply ──> ledger  ──>  broker-neutral private app
                 │                    │               + reproducible research
                 └─ backup/restore    └─ explainable  + gated automation
```

현재 lake는 아래 한 줄이다.

```text
CSV import -> normalize/preview -> idempotent apply -> append-only ledger
           -> FIFO holdings/cash/P&L -> versioned backup/verified restore
```

Phase A에서는 `worker`, `runner`, execution gateway, broker credential, cloud runtime을 만들지 않는다. cloud-ready는 영속 상태·migration·backup 경계를 잘못 굳히지 않기 위한 계약으로만 남긴다.

### 시스템·데이터·상태·오류·배포·복구 흐름

```text
[CSV/manual input]
        |
  parse + validate -- nil/empty/malformed --> visible preview error, no write
        |
    normalize ------ unknown instrument ---> unresolved row, no write
        |
      preview ------- cancel/duplicate -----> no write
        |
 atomic SQLite apply -- lock/invariant -----> bounded failure + rollback
        |
 deterministic replay
        |
 holdings + cash + realized P&L + backup
```

```text
ImportRun
NEW -> PARSED -> PREVIEWED -> APPLYING -> APPLIED -> RECONCILED
 |        |          |          |
 +------> FAILED <---+----------+
                     +-> CANCELLED

FAILED/CANCELLED -> APPLYING 은 새 run 없이 금지한다.
APPLIED transaction은 수정하지 않고 correction transaction을 append한다.
```

```text
local rollout:
migrate -> tests -> import fixture -> invariant check -> backup
        -> restore into temp DB -> integrity_check -> snapshot compare

rollback:
stop writes -> preserve current backup -> verify last good backup in temp DB
            -> replace only after integrity + ledger checks -> restart
```

### Sections 1-11 결정

| 검토 차원 | 결론 | Phase A 조치 |
|---|---|---|
| 1 Architecture | 단일 프로세스·SQLite가 한 사용자에게 충분함 | 미래 역할과 provider interface를 scaffold하지 않음 |
| 2 Error/rescue | 부분 원장 반영이 최악의 실패 | import 전체를 한 transaction으로 적용 |
| 3 Security | 외부 credential은 없지만 import/restore가 trust boundary | 입력 크기·형식·Decimal 범위·backup version 검증 |
| 4 Data/interaction | empty, invalid, duplicate, correction, double submit을 구분해야 함 | preview 결과와 idempotency key를 명시 |
| 5 Code quality | 두 번째 구현 전 추상화가 가장 큰 코드 부채 | stdlib와 실제 함수 경계만 사용 |
| 6 Tests | happy path만으로 원장 신뢰를 증명할 수 없음 | duplicate, rollback, golden math, restore test 필수 |
| 7 Performance | 현재 위험은 지연보다 불필요한 복잡성 | 전체 replay로 시작하고 거래 수가 병목일 때 측정 |
| 8 Observability | 잘못된 숫자를 재구성할 수 있어야 함 | import run 상태·건수·오류 종류를 저장 |
| 9 Deployment | Phase A 배포는 local migrate/run뿐 | cloud image·Kubernetes는 구현하지 않음 |
| 10 Trajectory | 핵심 플랫폼 자산은 adapter가 아니라 deterministic ledger | canonical transaction과 snapshot을 안정화 |
| 11 UX | 고급 차트보다 import 신뢰와 숫자 설명이 선행 | Phase C 설계에 preview·오류·drill-down 계약 전달 |

### Error & Rescue Registry

| Codepath | 실패 | 구조화된 결과 | 복구 |
|---|---|---|---|
| CSV parse | 빈 파일, encoding, 필수 열 누락 | import validation error | mutation 없음, 행/열 표시 |
| Decimal/date normalize | 잘못된 값·범위 | row error | preview 차단 |
| Reference normalize | 알 수 없는 account/instrument | unresolved row | mapping 후 새 preview |
| Import apply | 같은 run 재적용 | duplicate/no-op | unique key로 멱등 처리 |
| Import apply | DB lock·constraint·invariant | failed run | 전체 rollback |
| Ledger replay | 음수 lot·SELL 초과 | invariant error | snapshot 생성 차단 |
| Backup | write 실패 | backup failure | 기존 backup을 덮어쓰지 않음 |
| Restore | checksum/version/integrity 불일치 | restore validation error | active DB 교체 금지 |
| Broker/order | timeout·unknown ack | Phase B/D 이후 | 현재 코드 경로 없음 |

### Failure Modes Registry

| Failure mode | Phase A rescue | Test | Gap |
|---|---|---|---|
| malformed/empty CSV | no write + diagnostic | required | 없음 |
| double submit/duplicate row | unique idempotency key | required | 없음 |
| crash/invariant during apply | transaction rollback | required | 없음 |
| precision/rounding drift | `Decimal`, canonical text storage | required | 없음 |
| SELL exceeds FIFO lots | reject whole import | required | 없음 |
| corrupt/old backup | temp restore + version/integrity check | required | 없음 |
| destructive active-DB restore | verified atomic replacement only | required | 없음 |
| broker timeout/order unknown | later reconciliation state | deferred | Phase B/D |
| dual automation runner | fencing token | deferred | Phase E |

### Phase A NOT in scope

- 브로커 API, market data, credential 저장
- Flutter 화면 이후의 차트, 주문 티켓
- TWR/XIRR/benchmark/drawdown과 이동평균 원가
- paper/shadow/live, 전략 엔진, scheduled worker
- cloud 배포·image publish, PostgreSQL, Redis/Kafka/Kubernetes. 이 초기 기록의 OCI blanket 제외는 현재 G0의 단일 local Go image smoke로 superseded됐다.
- CSV spreadsheet export; 이 기능을 추가할 때 formula-injection 방어도 함께 추가

### Phase A 구현 작업

- **P1 Ledger schema:** versioned SQLite schema와 canonical transaction 계약
- **P1 Import:** parse/normalize/preview/apply와 file·row 멱등성
- **P1 Calculator:** FIFO holdings, cash, realized P&L, invariant
- **P1 Recovery:** versioned JSON backup, temp DB restore, integrity/snapshot comparison
- **P2 Diagnostics:** import run의 건수·상태·구조화된 오류

### CEO dual voices — consensus

| Dimension | 독립 reviewer | Codex CLI | Consensus |
|---|---|---|---|
| Premises valid? | 원장 중심·단계적 hybrid 타당 | 타당 | CONFIRMED |
| Right problem? | 설명 가능한 reconciliation layer | 같은 판단 | CONFIRMED |
| Scope calibrated? | 같은 앱의 후속 automation 허용 | 별도 제품 결정 권고 | DISAGREE → 사용자 방향으로 해결 |
| Alternatives explored? | core-first/minimal UI/cloud-first 비교 | local/hybrid/server-first 비교 | CONFIRMED |
| Market/competitive risks? | fixture와 검증 계약 우선 | 실제 데이터 bake-off 우선 | CONFIRMED |
| Six-month trajectory? | deterministic ledger 선행 | ledger 선행 | CONFIRMED |

### CEO 완료 요약

| 항목 | 결과 |
|---|---|
| 모드 | SELECTIVE_EXPANSION |
| 독립 시각 | 이전 Codex CLI + 최신 독립 reviewer 완료 |
| 합의 | 5/6 cross-voice 확인; 1개 범위 이견은 사용자 방향으로 종결 |
| 현재 lake | Phase A 원장·import·계산·복구 |
| critical gaps | 위 구현 작업과 test gate로 전환, 미해결 결정 0 |
| 범위 추가 | 설명 가능성·과거 backup 복구·두 provider fixture 계약만 수용 |
| 범위 유예 | 모든 broker/cloud/order/automation 구현 |

> **Phase 1 plan review complete.** Codex: 5 concerns. Independent reviewer: 7 implementation findings. Consensus: 5/6 confirmed, 1 disagreement resolved by the user, 0 unresolved. Passing to Phase 2 review.

## `/autoplan` Phase 2: Design 검토

분류: **APP UI**. 이 검토를 시작한 시점에는 docs-only였고 `DESIGN.md`와 UI component가 없었다. 현재는 `DESIGN.md`, semantic token, Flutter trust/import 화면과 실제 mobile/desktop browser screenshot이 존재한다. 아래 점수는 사전 설계 검토 기록이며 현재 구현 증거는 완료 요약에서 별도로 갱신한다.

### 이미 존재하는 디자인 계약

- Phase C 화면 목록, desktop sidebar와 mobile 4개 이하 navigation
- loading/empty/error/partial/stale/success vocabulary
- keyboard, reduced-motion, 200% zoom, light/dark, 비색상 손익 표시 원칙
- import/restore의 안전한 domain 상태와 `왜 이 숫자인가` 목표
- Minimal/Swiss 계열의 차분한 APP UI 방향과 Noto Sans KR/tabular number 기준

### Pass 1 — Information Architecture: 6/10 → 10/10

```text
Home              Holdings           History               Connections
├─ trust status   └─ Asset Detail    ├─ Transactions       ├─ Connections
├─ next problem       └─ 근거 보기   └─ Import Review      ├─ Export
├─ snapshot                                                   └─ Backup / Restore
├─ recent import
└─ verified backup

Settings = 보조 경로
Research / Orders / Automation = 해당 capability가 시작될 때만 추가
```

첫 viewport의 우선순위는 (1) 검증·freshness, (2) 해결할 문제, (3) 보유/현금 snapshot이다. metric card 7개나 미래 기능을 먼저 노출하지 않는다. 모든 재무 숫자의 `계산 근거 보기`는 desktop side panel, mobile full route로 연다.

### Pass 2 — Interaction State Coverage: 5/10 → 10/10

| Feature | Loading | Empty | Error | Success | Partial | Stale |
|---|---|---|---|---|---|---|
| Home | 최초에만 layout-stable skeleton | import 한 가지 CTA와 preview 무변경 안내 | 안전하게 남은 데이터와 복구 행동 | 검증 시각·snapshot·최근 receipt | 알려진 값과 누락 범위를 함께 표시 | price/FX `as_of`와 영향 범위 |
| Import preview | filename, parse progress, `aria-busy`; 아직 write 없음 | 빈 파일과 새 거래 0건을 구분 | 행·열 원인, confirm disabled | 신규·중복·차단 건수와 before/after | preview에서는 허용: 유효·중복·오류·미해결 분류 | file hash/schema/mapping/ledger revision이 바뀌면 재-preview |
| Import apply | submit lock, run ID, atomic 안내 | duplicate-only면 `변경 없음` | `아무것도 기록되지 않음`, 새 run으로 재시도 | 적용·제외 건수, invariant, 영향 거래 receipt | **금지**: 부분 mutation은 defect | preview token 불일치 시 apply 거절 |
| Backup | snapshot→verify 단계, 기존 backup 유지 | verified backup 없음 + 생성 CTA | 실패 candidate 폐기, 기존 backup 유지 | 시각·version·checksum·size·검증 receipt | **금지** | 마지막 backup 이후 변경 수와 age |
| Restore | temp DB의 checksum→version→integrity→snapshot | 호환 backup 없음 | active DB를 교체하지 않았음을 명시 | 복원 거래/계정 수와 검증 receipt | **금지** | 현재 변경보다 오래된 backup임을 경고 |
| 계산 근거 | 현재 값은 유지하고 provenance만 load | 0은 contributor 0 허용; non-zero 무근거는 trust error | 값 유지, 근거 unavailable과 diagnostic ID | 시점·범위·통화·수식·event·FX·수수료·세금·rounding | known contribution과 unresolved residual 분리 | provider와 `as_of` 표시 |

`partial preview`는 유효하지만 `partial apply/backup/restore`는 허용하지 않는다. 오류는 원인뿐 아니라 무엇이 보존됐고 다음에 무엇을 하면 되는지 말한다. 완료는 toast 하나가 아니라 감사 가능한 receipt다.

### Pass 3 — User Journey & Emotional Arc: 5/10 → 9/10

| Step | User action | 감정 목표 | 제품의 답 |
|---|---|---|---|
| 1 | 빈 앱 진입 | 막막함 → 방향 | `거래 가져오기` 하나와 preview는 안전하다는 안내 |
| 2 | 파일 선택 | 경계 | 파일·계좌·기간과 read-only parse 표시 |
| 3 | preview 검토 | 의심 | 반영/중복/매핑/오류/충돌 및 숫자 변화 |
| 4 | 문제 해결 | 통제 | 행별 원인·행동, 진행 상태 보존 |
| 5 | apply | 불안 | stale guard, double-submit 차단, atomicity |
| 6 | receipt 확인 | 안도 | exact counts, invariant, affected records |
| 7 | backup/restore | 높은 불안 | active data 보존과 검증 단계, 복구 receipt |
| 8 | 숫자 검증 | 회의 | 생성 문구가 아닌 deterministic provenance |

5초에는 `내 데이터의 상태와 live 비활성`을, 5분에는 import/apply와 verified backup 한 번을, 5년에는 correction/migration까지 감사 가능한 이력을 제공한다.

### Pass 4 — AI Slop Risk: 7/10 → 9/10

- table-led personal ledger를 사용하고 stacked card mosaic를 만들지 않는다.
- Home은 balance/trust anchor 하나, compact metric row 하나, 준비됐을 때 performance chart 하나만 둔다.
- thin divider와 restrained elevation을 사용한다. decorative gradient, glass, colored icon circle, bubbly radius, ornamental icon, confidence score를 만들지 않는다.
- blue는 action, green/red는 의미 상태에만 사용하며 항상 부호·텍스트를 병기한다.
- 조회는 mobile-first지만 대량 import review는 desktop-optimized라고 정직하게 선언한다.

#### Dual-voice litmus scorecard

| Litmus | 독립 reviewer | Codex CLI | Consensus / fix |
|---|---|---|---|
| 첫 화면에서 제품이 명확한가 | Partial | No | trust-and-action-first로 수정 |
| 강한 visual anchor가 하나인가 | No | No | validation status + snapshot anchor |
| 제목만 훑어도 이해되는가 | Partial | Partial | utility heading 사용 |
| 각 section의 일이 하나인가 | Yes | 개선 필요 | one-job sections 유지 |
| card가 실제로 필요한가 | 대부분 No | No | table/layout 우선 |
| motion이 hierarchy를 돕는가 | 불명확 | 최소화 | 상태 변화에만 사용 |
| shadow 없이도 premium인가 | Yes | Yes | type·spacing·data clarity로 해결 |

7/7 방향 합의. navigation에서 연결·복구를 top-level로 둘지 Settings 아래에 둘지 taste 차이가 있었고, 현재 제품의 import/recovery 비중을 근거로 `Connections` top-level을 채택했다.

### Pass 5 — Design System Alignment: 3/10 → 8/10

`DESIGN.md`, `design-system/omni-folio/MASTER.md`, Flutter light/dark theme에 `surface`, `text`, `muted`, `action`, `success`, `warning`, `danger`, `focus`, `stale` 의미를 반영했다. Noto Sans KR/시스템 글꼴과 tabular number를 사용하며 React/shadcn은 설치하지 않았다.

로컬 UI 검색이 제안한 marketing pattern과 dark-only 금융 palette는 개인 원장 APP UI와 맞지 않아 채택하지 않았다. 그 결과 중 Minimal/Swiss, 낮은 motion, data density 원칙만 사용한다.

### Pass 6 — Responsive & Accessibility: 6/10 → 9/10

- 375px: `Home/Holdings/History/Connections` bottom nav, 단일 열, import/restore와 근거 보기는 full route
- 768px: navigation rail/drawer; 두 pane의 label과 focus가 유지될 때만 split view
- 1024px+: persistent sidebar와 main workspace
- 1440px+: financial table 폭을 확장하고 marketing-style narrow container를 피함
- mobile transaction/import row는 symbol/name, value, status/action을 먼저 보여주고 세부 필드는 row detail로 이동
- semantic table, caption, row/column header, `aria-sort`, visible focus, skip/main landmark, dialog focus return
- 최소 44×44 CSS px touch target, body 4.5:1, UI component 3:1, 200% zoom에서 기능 손실 없음
- failed form은 focusable error summary와 field-linked inline error를 함께 사용
- chart는 같은 기간·범위의 text summary와 table을 제공하고 hover-only disclosure를 금지
- 완료·차단처럼 중요한 상태만 live region으로 알리고 계속 변하는 가격을 읽어주지 않음

### Pass 7 — Design Decisions

| 결정 | 결과 |
|---|---|
| preview invalidation | file hash + schema + mapping/config + ledger revision을 token에 포함 |
| restore safety | temp DB 검증 + pre-restore recovery point + 명시적 replace confirmation |
| number explanation | 생성 문자열이 아니라 structured provenance/equation |
| navigation | `Home/Holdings/History/Connections`; contextual detail; Settings 보조 |
| exact tokens/components | semantic theme와 최소 Flutter components로 구현 |
| visual evidence | 390×844 mobile 및 desktop browser screenshot 생성 |

미해결 디자인 결정은 없다. semantics·touch target·light/dark contrast·reduced motion은 자동 검증되며, strict physical-device profile p95와 수동 assistive-technology 검증은 G2 release evidence로 남아 있다.

### Design NOT in scope

- Phase A 밖의 chart, 주문, 자동화 UI 구현
- frontend stack이 생기기 전 component 설치
- broker/order/automation UI와 disabled placeholder
- marketing page, onboarding tour, 장식 animation
- 실제 fixture가 없는 조기 mockup과 최종 palette 고정

### Design 구현 작업

- **P1 Import contract:** preview token, row classification, before/after, apply receipt를 domain output으로 제공
- **P1 Recovery contract:** 검증 단계와 backup/restore receipt를 구조화
- **P1 Explainability:** snapshot 각 값의 structured provenance를 보존
- **P2 Phase C entry gate:** IA, viewport, keyboard, screen-reader acceptance를 적용; physical profile과 수동 screen-reader 증거는 G2 잔여 항목
- **P3 Visual system:** `DESIGN.md`, semantic tokens와 실제 fixture 기반 Flutter variant 생성 완료

### Design 완료 요약

| 항목 | 결과 |
|---|---|
| System audit | 사전에는 docs-only; 현재 `DESIGN.md`, token, Flutter components 존재 |
| Initial → reviewed | 5.5/10 → 9/10 contract completeness |
| Pass 1 IA | 6 → 10 |
| Pass 2 States | 5 → 10 |
| Pass 3 Journey | 5 → 9 |
| Pass 4 AI slop | 7 → 9 |
| Pass 5 Design system | 3 → 8; semantic theme와 실제 Flutter 화면 구현 |
| Pass 6 Responsive/a11y | 6 → 9 |
| Pass 7 Decisions | 4 resolved, 2 intentionally deferred, 0 unresolved |
| Dual voices | Codex 8 findings, independent reviewer 5 tasks; 7/7 litmus direction aligned |
| Visual evidence | mobile/desktop browser screenshot, widget tests, iOS/Android/web builds |

> **Phase 2 design review complete.** Codex: 8 concerns. Independent design reviewer: 5 implementation findings. Consensus: 7/7 litmus directions confirmed, 1 navigation taste difference auto-resolved, 0 unresolved. Current UI implements the reviewed first slice; G2 profile/screen-reader evidence remains a release gate, not a design decision.

## `/autoplan` Phase 3: Engineering 검토

모드: **SCOPE_REDUCED**. 사용자가 이미 “추천대로 진행하되 확장 가능한 mobile/backend/backtest/cloud 하위 프로젝트를 실제로 만들라”고 승인했으므로 가역적인 권고는 자동 채택했다. 구현 lake는 네 runtime을 늘어놓는 대신 다음 한 수직 흐름으로 제한한다.

```text
CSV fixture
  -> Go preview/apply transaction
  -> SQLite ledger/FIFO snapshot/receipt
  -> Flutter trust/import presentation

market fixture
  -> Python deterministic run manifest

Go binary
  -> local process + one non-root OCI smoke
```

Python G3와 local OCI를 이번 lake에서 자르자는 독립 검토 의견은 채택하지 않았다. 둘 다 사용자가 명시한 하위 프로젝트와 cloud-ready 구조의 최소 실행 증거이고, Python은 stdlib CLI 하나, OCI는 Go image 하나로 상한을 고정했다. broker SDK, live order, Kubernetes, queue, 여러 서비스는 포함하지 않는다.

### What already exists

- 제품·안전 목표와 단계적 trading promotion 계약
- Flutter/Go/Python state-authority ADR와 semantic design contract
- OpenAPI, import/FIFO golden fixture, backtest manifest fixture
- gate tree와 첫 lake acceptance

재사용 대상은 SQLite transaction, Go standard HTTP/profiling, Python `Decimal`/stdlib, Flutter Material/accessibility primitive다. framework-level event store, plugin SDK, shared domain library는 새로 만들지 않는다.

### Architecture findings and resolutions

| # | Finding | Confidence | Resolution |
|---|---|---:|---|
| 1 | preview가 file hash와 ledger revision만 묶어 schema/mapping 변경을 놓침 | 9/10 | `schema_version`, `mapping_version`, `preview_fingerprint`를 required contract로 추가 |
| 2 | unknown account/instrument를 표현할 `unresolved` row가 없음 | 9/10 | structured resolution과 `unresolved` status 추가 |
| 3 | backup/restore가 G1 gate지만 manifest/receipt fixture가 없음 | 9/10 | API endpoint 대신 versioned CLI manifest와 invalid candidate fixture 추가 |
| 4 | local OCI가 current lake와 과거 Phase A NOT-in-scope에 동시에 존재 | 9/10 | current G0의 단일 local image smoke가 과거 blanket 제외를 supersede |
| 5 | G3에 shared market fixture/run schema가 없음 | 9/10 | 하나의 deterministic partial-fill fixture와 JSON Schema 추가 |
| 6 | first launch가 검증 timestamp를 거짓으로 만들어야 함 | 7/10 | `never_verified`와 nullable `last_verified_at` 추가 |
| 7 | CSV request 상한이 없음 | 7/10 | 1 MiB/10,000 row hard limit을 계약과 서버에 동일 적용 |
| 8 | chart 없는 단계에서 chart frame budget을 요구 | 7/10 | G2는 import/list budget만, chart budget은 G4로 이동 |
| 9 | generated Flutter counter는 제품 gate 증거가 아님 | 10/10 | scaffold는 중간 상태로만 취급하고 trust/import widget과 tests로 교체 |

### Code-quality decision

- Go는 하나의 binary와 한 DB connection owner로 시작한다. 아직 없는 broker/order interface, repository factory, event bus는 만들지 않는다.
- Decimal은 JSON/SQLite canonical text와 exact arithmetic만 허용하고 `float64` 경로를 테스트로 차단한다.
- Flutter는 API client 한 개와 화면에 필요한 immutable model만 둔다. generic state framework, router package, chart package는 추가하지 않는다.
- Python은 CLI/library 같은 함수 경계만 두고 network/service/queue/DB abstraction을 만들지 않는다.
- 모든 오류는 stable code, 원인, 사용자가 취할 다음 행동을 제공하고 ledger mutation은 transaction 밖으로 새지 않는다.

### Test coverage diagram

```text
CODE PATHS                                      USER FLOWS
[x] CSV body limit/header parse                 [x] status -> trust banner
 ├─ [x] canonical decimal/RFC3339               [x] paste/select CSV -> preview
 ├─ [x] new/duplicate/error/unresolved           ├─ [x] invalid rows -> repair guidance
 └─ [x] fingerprint + revision                  └─ [x] can_apply -> explicit confirm
[x] apply transaction [-> integration]             ├─ [x] receipt success
 ├─ [x] replay same key                            ├─ [x] key conflict/stale preview
 ├─ [x] key different payload                      └─ [x] refresh failure retains known data
 ├─ [x] stale preview
 └─ [x] rollback on invariant
[x] FIFO projection
 ├─ [x] deposit/buy/sell/fee allocation
 ├─ [x] oversell rejection
 └─ [x] cash/holding/P&L/provenance
[x] backup candidate [-> integration]
 ├─ [x] consistent snapshot + hash
 ├─ [x] version/integrity/golden verify
 └─ [x] corrupt/old candidate never replaces active DB
[x] Python simulation
 ├─ [x] no-lookahead delayed fill
 ├─ [x] participation partial fill
 └─ [x] fee/tax/slippage + deterministic manifest
```

최소 테스트 계획은 아래 Implementation Tasks와 executable check에 통합했으며, 완료 항목은 실제 test file과 command 증거로 갱신했다.

### Failure modes

| Path | Production failure | Required rescue | User result |
|---|---|---|---|
| preview | mapping/schema 변경 뒤 오래된 preview apply | fingerprint/revision conflict | 새 preview 안내, no write |
| apply | double tap/concurrent retry | DB unique receipt + one transaction | 원 receipt 또는 409, duplicate 없음 |
| ledger | oversell/decimal drift | exact arithmetic + invariant rollback | 행 오류와 no partial mutation |
| backup | corrupt candidate/중단된 copy | temp restore + hash/integrity/golden verify | active DB 유지 |
| client | refresh timeout | known-good snapshot 유지 + stale timestamp | retry 가능, blank screen 없음 |
| research | 미래 bar 사용 또는 delayed fill 누락 | event-time test와 golden manifest | run 실패, 결과 publish 안 함 |
| OCI | volume 누락/read-only path | startup fail-fast와 health smoke | ledger를 ephemeral disk에 만들지 않음 |

critical silent gap은 contract 보강 뒤 0개다.

### Performance review

- import 기준: 1 MiB/10,000 rows hard cap 안에서 preview/apply p50/p95, peak RSS, DB growth를 기록한다.
- read 기준: 100/1,000/10,000 ledger events에서 snapshot p50/p95를 측정한다.
- Flutter 기준: representative import/list fixture의 60 Hz p95 frame budget을 profile mode에서 기록한다. chart는 아직 측정하지 않는다.
- 2026-08-24 첫 로컬 Android emulator profile은 614 frames에서 build/raster/total-span p95 1.026/16.674/22.222 ms를 기록했지만 환경 메타데이터가 부족했다. Flutter 3.47.1, `Medium_Phone_API_36.1_emulator`, Android 16/API 36, 411.43×914.29 logical viewport@2.625×를 함께 기록한 두 번째 595-frame run은 2.745/31.851/54.008 ms였다. strict 16.67 ms raster budget을 실패했고 emulator 간 변동도 크므로, physical Android/iOS에서 다시 측정한다.
- Python 기준: 같은 bars/request에서 deterministic output을 우선하고 벡터화 dependency는 stdlib가 측정상 한계일 때만 추가한다.
- 고정 latency SLA나 cache/Redis를 먼저 약속하지 않는다. broker/order hot path가 생긴 뒤 구간별 p50/p95/p99 예산을 둔다.

### Engineering NOT in scope

- broker credential, adapter, market feed, order submit과 live capability
- chart package와 대량 tick pipeline
- PostgreSQL dual-write/migration 구현, Kubernetes/Helm
- Redis, Kafka/NATS, Temporal, service mesh, microservice 분리
- JVM/Rust subproject. JVM-only SDK 또는 measured Go CPU/GC p99 failure가 생길 때 ADR을 재검토
- app-store signing/publish와 외부 cloud deploy

### Parallelization

| Lane | Modules | Depends on |
|---|---|---|
| A | `contracts/` | ADR/gates |
| B | `services/core/` | A |
| C | `apps/client/` | A |
| D | `services/research/` | A G3 fixture |
| E | root commands + `infra/` | B-D runnable commands |

A를 먼저 고정한 뒤 B/C/D를 병렬로 실행하고 E가 실제 명령을 통합한다. 각 lane은 다른 directory를 소유하며 root integration만 마지막에 수행한다.

### Implementation Tasks

- [x] **T1 (P1, human: ~4h / CC: ~25min)** — contracts — preview/recovery/backtest 계약 보강
  - Surfaced by: architecture findings 1-8
  - Files: `contracts/**`, `gates/**`
  - Verify: JSON parse, fixture hash/Decimal assertions, `git diff --check`
- [x] **T2 (P1, human: ~3d / CC: ~90min)** — Go core — atomic import와 exact FIFO/restore 구현
  - Surfaced by: code/test/failure-mode review
  - Files: `services/core/**`
  - Verify: `go test ./... && go vet ./...`
- [x] **T3 (P1, human: ~2d / CC: ~60min)** — Flutter — counter scaffold를 trust/import vertical slice로 교체
  - Surfaced by: architecture finding 9 and G2
  - Files: `apps/client/**`
  - Verify: `flutter analyze && flutter test && flutter build web --release`
- [x] **T4 (P2, human: ~1d / CC: ~45min)** — Python research — deterministic delayed/partial-fill manifest 구현
  - Surfaced by: architecture finding 5 and G3
  - Files: `services/research/**`
  - Verify: `python -m unittest discover -s tests`
- [x] **T5 (P2, human: ~1d / CC: ~40min)** — local delivery — root check와 non-root OCI smoke
  - Surfaced by: scope/distribution review
  - Files: `README.md`, `Makefile`, `.gitignore`, `.env.example`, `infra/**`
  - Verify: `make check && make smoke`

### Engineering completion summary

- Step 0 Scope Challenge: first vertical slice로 축소, explicit subprojects는 최소 runnable proof로 유지
- Architecture Review: 7 contract/scope issues accepted and resolved in the plan
- Code Quality Review: 4 runtime-specific simplicity rules
- Test Review: diagram produced, 22 paths/flows scheduled
- Performance Review: 4 bounded budgets, premature cache/distribution cut
- NOT in scope: written
- What already exists: written
- Failure modes: 0 accepted silent critical gaps
- Outside voice: independent reviewer ran; 9 findings, 8 durable plan changes and 1 transient scaffold finding
- Parallelization: 5 lanes, B/C/D parallel after A, E sequential
- Unresolved decisions: 0

> **Phase 3 plan and first-slice implementation complete.** `make check`, `make smoke`, Go race/build, Flutter web/iOS/Android release builds, deterministic improvement run, Playwright API/UI flow, and isolated non-root/read-only Compose readiness all passed on 2026-08-24. G2 automated semantics/touch/contrast/reduced-motion checks pass; strict physical profile p95 and manual assistive-technology evidence remain open.

## `/autoplan` Phase 4: Developer Experience 검토

새 사용자가 README만 따라가도 동일한 수직 슬라이스를 실행할 수 있는지, 명령·버전·환경변수·컨테이너 설명이 실제 파일과 일치하는지 검토했다. 독립 reviewer가 발견한 custom API port 불일치와 중복 asdf pin을 수정했고, Flutter 기본 scaffold 이름과 사용하지 않는 아이콘 의존성을 제거했다.

- `README.md`의 custom-port 예시는 core bind address와 client `API_URL`을 같은 값으로 사용한다.
- `.tool-versions`는 Flutter 3.47.1 stable, Go 1.24.5, Python 3.14.5를 한 번씩 고정한다.
- `.env.example`은 secret이 아닌 로컬 reference만 포함하며 Make가 암묵적으로 source하지 않음을 설명한다.
- Compose는 root context를 `.dockerignore`로 제한하고 non-root, read-only, capability drop, loopback port, one-shot migration과 readiness를 실제로 통과했다.
- 독립 DX reviewer 재검증에서 나온 3개 finding을 2026-08-24에 모두 수정했다.

> **Phase 4 DX review complete.** 독립 reviewer 3개 실질 finding을 모두 수정했고 명령 진실성·secret 경계·gate 정직성에 미해결 blocker가 없다.

## GSTACK REVIEW REPORT

| Phase | Result | Durable decision or evidence |
|---|---|---|
| CEO | PASS | 개인용 local-first 원장 앱에서 시작해 paper/shadow를 자동화 상한으로 두고 live는 owner 승인으로 제한 |
| Design | PASS WITH RELEASE EVIDENCE OPEN | Flutter 단일 client, Toss-inspired plain-language UX, 실제 mobile/desktop 화면과 200% text·semantics·contrast·reduced-motion test; physical profile/screen-reader 증거는 G2에 남김 |
| Engineering | PASS | Go exact ledger/import/restore, Flutter trust/import, Python deterministic backtest와 expanding walk-forward, versioned contracts가 root check/smoke 통과 |
| DX | PASS | pinned asdf, five-minute README, isolated hardened Compose readiness, custom-port 명령 일치 |
| Broker decision | PASS | 키움 read-only·차트·실시간·모의주문을 첫 gate로, 토스증권 Open API를 두 번째 adapter이자 UX reference로 고정 |

이 초기 GSTACK review 시점의 미해결 제품·아키텍처·구현 결정은 없었다. 이후 G4B~G4E continuation이 당시 next-work 문장을 대체했다. 현재 next work는 [`PLAN.md`](../PLAN.md)의 credentialed Kiwoom read/market evidence, K2B mock submit/query·lookup recovery와 G2의 남은 수동 release evidence다. 실전 주문, credential 등록, 외부 배포와 push는 이 보고서로 승인되지 않는다.

NO UNRESOLVED DECISIONS

## 2026-08-24 G4B continuation: local sample OHLCV contract and chart

The next product leaf was chosen as a credential-free provider-neutral OHLCV vertical slice before real Kiwoom candle transport. This fixes the consumer contract without guessing provider pagination, timezone, adjustment, minute-unit, or realtime semantics. The earlier Phase A statements that charts and broker work were out of scope remain historical scope records; this continuation supersedes them for G4B only.

- Go exposes `GET /v1/market-data/candles?symbol=AAPL&interval=1d` only when an explicit local fixture path is configured. Absent data fails with `503`; unknown series with `404`; malformed, oversized, unordered, inconsistent, or non-canonical data fails at startup.
- OpenAPI separates a provider-neutral `MarketDataCandles` state contract from the local-fixture response subtype. The local endpoint always reports `source=local_fixture`, `sample=true`, `state=stale` and a non-live issue.
- Flutter opens asset detail from holdings and renders a horizontally scrollable static price/volume candle surface using native `CustomPainter`. Exact values stay strings; doubles are limited to finite paint geometry. The text alternative is a horizontally scrollable, vertically lazy 500-row surface with header and cell semantics.
- Sample provenance is visible and machine-readable. Fixture values cannot count as current prices, broker evidence, reconciliation, strategy promotion data, or live readiness.
- `make check`, `make smoke`, Go race, Flutter web/Android/iOS release builds, and 17 Flutter tests pass locally. No runtime dependency was added.

The first 500-row table implementation failed the emulator raster budget at `22.446 ms` p95. After lazy-row optimization and a harness assertion that proves bidirectional table scrolling, two metadata-complete Flutter 3.47.1 Android 16/API 36 emulator runs recorded 727 frames at build/raster/total-span `0.928/5.749/10.698 ms` and 728 frames at `1.158/16.623/20.632 ms`. The build and raster phases pass the 16.67 ms budget in both runs. Physical Android/iOS profile and manual VoiceOver/TalkBack remain open; emulator variance is not release proof.

Deliberately deferred: real Kiwoom OHLCV/realtime, period switching, portfolio performance, average-cost and fill markers, cache/persistence, chart package, and broker-specific UI branches. Add them only after official/mock response observations preserve this canonical contract.

## 2026-08-24 G4C continuation: synthetic Kiwoom daily/minute candle contract

K1 moved one step past local fixtures without using credentials: Kiwoom daily `ka10081` and minute `ka10080` now share the existing OAuth/read transport and are bound to `POST /api/dostk/chart`. The public `/v1/market-data/candles` route remains local-fixture only, so no response is presented as live Kiwoom data.

- The adapter requests adjusted prices with `upd_stkpc_tp=1`, accepts official minute intervals `1,3,5,10,15,30,45,60`, and keeps submit API IDs outside the read allowlist.
- Provider dates/times are parsed as Korean market local time by operational assumption, then emitted as UTC RFC3339 bars with `Timezone=Asia/Seoul`, `Venue=XKRX`, and `PriceAdjustment=provider_adjusted`.
- OHLC prices are exact positive canonical decimals after Kiwoom sign/magnitude normalization; volume is exact non-negative canonical decimal.
- Pagination is bounded by the existing 32-page cap and the consumer-facing series is capped at the newest 500 bars. One bounded look-ahead page validates the cap boundary; exact duplicate timestamps collapse and conflicting duplicates fail closed.
- `go test -count=1 ./...`, `go test -race -count=1 ./...`, `make check`, and `make smoke` pass locally on 2026-08-24 KST.

Still open: credentialed Kiwoom mock/production candles, official timezone/freshness confirmation, realtime WebSocket, cache/persistence, reconciliation, Flutter wiring from Kiwoom data, mock orders, and live-order gates.

## 2026-08-24 G4D continuation: price-adjustment consumer contract

K1 already preserved `provider_adjusted` internally, but the public candle response and Flutter model dropped the price basis. G4D closes only that consumer-contract gap without generalizing source/freshness or wiring Kiwoom credentials.

- OpenAPI and HTTP require `price_adjustment` with only `unspecified` and `provider_adjusted` in the provider-neutral base contract.
- The current public route remains local-fixture-only and pins `price_adjustment=unspecified`; empty, provider-adjusted, or unknown values from that port fail closed.
- Flutter rejects missing/unknown values and requires the OpenAPI local-fixture subtype exactly: sample, unspecified adjustment, stale state, non-null source timestamp, and at least one provenance issue. It displays “조정 여부 확인 안 됨” or “공급자 조정 가격 · 조정 정확성 미검증” in asset metadata.
- TDD checkpoints are `4e81f2b`/`0519d20` for the consumer field and `bebbfaa`/`41093c0` for strict local provenance. `make check`, `make smoke`, Go race, and the existing Kiwoom candle suite pass locally on 2026-08-24 KST.

Still open: runtime Kiwoom market-data selection, credentialed mock/production observation, authoritative timezone/freshness, persistence/known-good retention, realtime, reconciliation, and corporate-action adjustment verification.

## 2026-08-24 G4E/K2A continuation: internal synthetic order-state log

K2A fixes the durable order/recovery contract before any Kiwoom order credential or network transport exists. The boundary is intentionally internal-only: synthetic Kiwoom `LIMIT` BUY/SELL for KRX/KRW, with no public API or Flutter order surface.

- `client_order_id`, event ID and provider execution aliases are durable and payload-bound. Conflicting reuse fails without mutating state.
- At the K2A checkpoint, risk verdict ordering was enforced before dispatch without a risk policy. K2C now supersedes direct approval with a reservation-bound credential-free BUY policy; durable `SUBMIT_DISPATCHED` still becomes `SUBMIT_UNKNOWN`, and account-wide new submit stays blocked until explicit reconciliation resolves it. Cancel of an independently known open order remains available as a risk-reducing command.
- Append-only replay covers submit ack/reject, partial/full/late fill, cancel dispatch/ack/reject, restart and overfill rejection. A provider “not found” observation alone cannot resolve unknown state.
- SQLite migration v2 added strict insert-only intent/event tables. Current backup v4 retains their canonical row hash/metadata and full replay, adds K2C authority/reservation digests and counts, and verifies exact migration history, STRICT/PK/UNIQUE/FK/rowid/trigger checks while also proving G4H broker snapshot state. Earlier backup formats are not automatically eligible for v4 activation.
- TDD checkpoints are `35ae80e`, `69f6f86`, `37bbff4` and GREEN `132de8e`. `make check`, `make smoke`, Go race/vet, 17 Flutter tests, 13 Python tests, 15 JSON contracts and an independent no-P0/P1 review pass locally on 2026-08-24 KST.

Still open for K2B: credentialed Kiwoom mock submit/query, broker lookup and reconciliation, production cash/position/fee/loss/time/freshness risk, broker-coupled runner fencing, public OpenAPI/Flutter order flow, market/amend orders, ledger mutation from fills and every live-money path.

## 2026-08-24 G4F/K2B0 continuation: known-order execution reconciliation

K2B0 adds only the smallest safe lookup bridge that existing K2A storage can support without credentials, network transport, a schema migration or a public order surface.

- A complete synthetic lookup may append executions only after an explicit ACK has already bound one opaque provider order ref to the local order. Account, order ref, canonical order tuple and UTC dispatch/observation window must agree.
- Complete execution observations are sorted by actual RFC3339Nano time and provider execution ref, then appended through the existing event hash/provider-execution idempotency path in one SQLite transaction. Any later conflict rolls the entire lookup back.
- Incomplete or missing lookup facts preserve the prior state. A complete lookup-only tuple match cannot bind `SUBMIT_UNKNOWN`; it returns `UNCORRELATED` and keeps the account-wide new-submit block.
- TDD started with the missing reconciliation API and added regressions for fractional timestamp order, incomplete evidence, changed executions, cross-order execution conflicts, mid-transaction rollback, tuple/time conflicts and raw identifier redaction. GREEN checkpoint is `4a2a6e4`; `make check`, Go race and independent safety review pass locally on 2026-08-24 KST.

Still open for K2B: official `kt10000`/`kt10001` submit and `ka10075`/`ka10076`/`kt00007`/`kt00009` credentialed mock observations, documented-or-observed correlation strong enough to handle a lost submit response, production risk/broker-coupled fencing, public OpenAPI/Flutter order flow, market/amend, ledger mutation and every live-money path.

## 2026-08-24 G4G/K2B1 continuation: dated execution scan

K2B1 adds the smallest provider-private observation boundary that can be justified from the current official `kt00009` account specification. It does not connect that observation to K2B0, storage, `SUBMIT_UNKNOWN`, HTTP or Flutter.

- The caller supplies an exact `YYYY-MM-DD`; the adapter sends one fixed stock/KRX/fills-only request and follows the existing bounded continuation transport to its terminal cursor.
- Every page requires an explicit zero `return_code` and execution array. Only `A`-prefixed KRX stocks, cash buy/sell, a non-empty provider order type, exact quantities/prices, valid provider-local `HH:mm:ss` and non-zero seven-digit order/execution IDs survive normalization.
- Opaque aliases include environment, account and requested date and use `dated_order`/`dated_execution` namespaces that the durable order-event validator rejects. Duplicate executions, changed order identity/order type, cumulative overfill or a later-page error discard the whole scan.
- `PaginationComplete` describes cursor traversal only. `ExecutionsComplete` remains false because the official contract does not establish retention, cross-page snapshot consistency, timezone, row ordering or identifier lifetime. The date and naive execution clock are deliberately not converted to UTC.
- TDD exposed and fixed two broader trust-boundary issues: cumulative fills could exceed order quantity, and the shared Kiwoom result checker accepted a missing `return_code`. Code checkpoint `d93cdee`; `make check`, `make smoke`, full Go race and independent architecture/test re-reviews pass locally on 2026-08-24 KST.

Still open for K2B: credentialed mock observations, authoritative empty/result/time/ID semantics, limiter behavior, lost-submit correlation, scheduled credentialed known-good sync, K2B0 mapping, production risk/broker-coupled fencing, public order flow, ledger mutation and every live-money path.

## 2026-08-24 G4H continuation: credential-free known-good broker snapshot

G4H persists only the existing all-or-nothing synthetic Kiwoom account snapshot. It creates no broker runtime, scheduler, public route, Flutter capability or order authority.

- `KiwoomSnapshot` now carries explicit `complete=true`; persistence revalidates provider/environment/exchange/account identity, canonical UTC and exact decimals, unique ascending KRX positions and open orders.
- One SQLite transaction reads the current ledger revision and KRX/KRW quantities for `account-main`, computes deterministic per-symbol `broker - ledger` quantities with exact rational arithmetic, and stores raw snapshot identity separately from the ledger-revision reconciliation record.
- Exact replay of the same raw snapshot and ledger revision returns the same reconciliation. A later ledger revision appends a new reconciliation without duplicating or mutating the raw snapshot. A changed payload at the same fetched-at, incomplete snapshot, invalid generated ID or corrupt durable hash fails closed without replacing the latest fetched known-good record.
- Migration v3 makes ledger events, broker snapshots and broker reconciliations insert-only. Current backup v4 preserves those proofs and adds execution-authority/risk-reservation digest/count plus strict schema/index/rowid/trigger checks and restored latest-record verification.
- TDD tests cover idempotency, concurrent replay, conflicts, last-known-good retention, arithmetic, DB mutation attempts, runtime/recovery corruption rejection, manifest tampering and weak-trigger restore rejection.

Still open: credentialed Kiwoom observation and scheduled known-good refresh, official freshness/timezone/retention, cash/valuation/open-order/full execution reconciliation, known-good API/UI, production risk, paper runner and all live-money paths.

## 2026-08-24 G4I/K2C continuation: credential-free execution authority

K2C adds the smallest internal authority that can prevent accidental synthetic dispatch without creating a broker transport or public order surface.

- Missing authority is fail-closed. Manual arm/halt is append-only; halt increments fencing and clears the lease. Each service creates a crypto-random process owner, and only the owner of an unexpired 30-second account lease with the exact fencing token can create a new authorization.
- Fixed `credential_free_buy_v1` accepts only Kiwoom synthetic KRX/KRW LIMIT BUY for `005930` and `000660`, at most 10 shares, at most KRW 1,000,000 per order and KRW 1,000,000 across active reservations. Exact rational arithmetic is reused; partial and unresolved states retain the full reservation until a terminal state.
- One immediate SQLite transaction inserts an immutable reservation and its consecutive reservation-bound `RISK_APPROVED` and `SUBMIT_DISPATCHED` events. Exact DB triggers reject ordinary append/direct SQL bypass; a collision rolls the entire transaction back.
- Migration v4 preserves reservation-free v3 order events as legacy non-authoritative history without backfill. Backup v4 hashes/counts authority and reservation records, replays transitions and bindings, and rejects missing/weak schema, foreign keys and guard triggers. A restored service receives a new owner and cannot reuse the saved process lease.
- TDD covers default-off, halt, stale/foreign/expired lease, two-handle lease and reservation races, fixed limits, terminal release, idempotency, DB bypass, atomic rollback, legacy migration, backup restore and weak-trigger rejection. `make check`, full Go race and `make smoke` pass locally on 2026-08-24 KST.

Still open: broker request/credential/transmission proof, SELL, cash/position/fee/daily-loss/order-rate/market-hours/stale-data risk, owner/strategy/promotion approval, broker-coupled long-running runner, public OpenAPI/Flutter order flow, paper/live readiness and every real-money path.

## 2026-08-24 G1 continuation: exact cash flows and stock-split replay

The original G1 golden slice proved deposits and fee-aware FIFO trades, but the product contract also names cash movements, dividends, fees, taxes and corporate actions as ledger authority. Schema v5 closes that local, credential-free gap without adding a new runtime or dependency.

- `WITHDRAWAL`, `FEE` and `TAX` require negative cash impact; `DEPOSIT` and instrument-bound `DIVIDEND` require positive cash impact. Trade-only fields are rejected where they do not apply.
- `SPLIT` requires an instrument, a positive exact ratio and zero cash impact. Replay multiplies each open FIFO lot quantity while preserving total cost basis; a split without an open holding rolls the whole apply back.
- Migration v5 rebuilds only the constrained `events` table, preserves existing v1-v4 rows, enforces event shape/cash direction with native CHECK constraints and restores insert-only triggers. Backup format remains v4 while the declared/required SQLite schema advances to v5.
- OpenAPI and backup contracts advance with the runtime. Focused cash/split, invalid-direction, rollback and v1-to-v5 migration tests pass with the existing order, broker snapshot and execution-authority recovery suite.

Still open: FX rates/conversions, correction events, dividend reinvestment, jurisdiction-specific tax classification, credentialed broker fill/cash reconciliation and every live-money path.
