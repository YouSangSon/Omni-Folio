# Omni Folio Gates

## Goal

iOS·Android·web에서 사용할 수 있고 local/single-node에서 시작해 검증 후 PostgreSQL·Kubernetes로 승격 가능한 개인용 멀티 증권 앱을 만든다. 원장, 주문, 백테스트, 자동매매는 정확성·복구·fail-closed 증거 없이는 다음 단계로 넘어가지 않는다.

## Gate tree

```text
G0 architecture and contracts
├─ G0.1 runtime/state authority ADR
├─ G0.2 OpenAPI and cross-runtime decimal contract
└─ G0.3 runnable monorepo commands
   |
G1 ledger vertical slice
├─ G1.1 migrate + canonical transaction
├─ G1.2 CSV preview + idempotent atomic apply
├─ G1.3 FIFO snapshot + provenance
├─ G1.4 backup + verified restore
├─ G1.5 exact cash flows + stock-split replay
├─ G1.6 append-only cash-flow void + schema v8 restore proof
├─ G1.7 restore mismatch error redaction
├─ G1.8 exact two-leg FX exchange + schema v9/v8 restore proof
├─ G1.9 replay-verified sanitized ledger activity read
├─ G1.10 append-only direct FX observation + schema v10/backup v6 proof
├─ G1.11 replay-verified direct-FX cash-only valuation
├─ G1.12 append-only security price observation + schema v11/backup v7 proof
├─ G1.13 replay-verified native-currency holding valuation
├─ G1.14 versioned recurring FIFO cost allocation + residual conservation
└─ G1.15 sanitized stored-price holding valuation API and Flutter detail
   |
G2 client vertical slice
├─ G2.1 Flutter iOS/Android/web build
├─ G2.2 trust status + import preview/apply receipt
└─ G2.3 accessibility + frame budget
   |
G3 research vertical slice
├─ G3.1 deterministic run manifest
├─ G3.2 fee/slippage/lookahead fixtures
├─ G3.3 no credential/order permission proof
├─ G3.4 Go/SQLite append-only paper-candidate registry and rollback proof
├─ G3.5 strategy-selection-bound order record and durable-dispatch proof
├─ G3.6 credential-free target-netted paper fill, replay and restore proof
├─ G3.7 atomic execution halt and strategy rollback proof
├─ G3.8A append-only paper operational evaluation evidence
├─ G3.8B Go-trusted strategy execution-policy contract
├─ G3.8C1 immutable account-global paper accounting session
├─ G3.8C2 SELL and capital-safe paper accounting
├─ G3.8C3 immutable marks and account-global performance evidence
├─ G3.8D current-selection strategy-window performance evidence
├─ G3.8E versioned paper performance safety policy and atomic automatic action
└─ G3.8F scheduled local paper policy runner
   ├─ G3.8F1 one-shot paper policy command
   └─ G3.8F2 DB-leased/fenced always-on runner
   |
G4 Kiwoom read-only -> charts/realtime -> Kiwoom mock order
├─ G4A Kiwoom K0 read contract
├─ G4B provider-neutral local sample OHLCV and asset-detail chart
├─ G4C Kiwoom K1 synthetic candle contract
├─ G4D market-data price-adjustment consumer contract
├─ G4E/K2A Kiwoom internal synthetic order-state log
├─ G4F/K2B0 Kiwoom known-order execution reconciliation
├─ G4G/K2B1 Kiwoom synthetic dated execution scan
├─ G4H Kiwoom broker known-good snapshot persistence
├─ G4I/K2C internal synthetic execution authority proof
├─ G4J/K2B2 credential-free Kiwoom mock LIMIT submit transport
├─ G4K stored broker/ledger position reconciliation read view
├─ G4L verified local order-lifecycle read view
├─ G4M overview stored-reconciliation trust summary
├─ G4N local order pending-action and overview warning contract
├─ G4O local daily chart display-range selection
├─ G4P first-run empty-snapshot import recovery
├─ G4Q Kiwoom credential-free latest-trade normalization
├─ G4R Kiwoom credential-free 0B realtime-price frame contract
├─ G4S Kiwoom credential-free durable latest-trade observation
├─ G4T Kiwoom credential-free one-shot latest-trade capture
├─ G4U owner-declared instrument listing ownership and Kiwoom enforcement
├─ K2B Kiwoom mock-order broker transport and lookup recovery
└─ then Toss Securities read-only as the second adapter
   |
G5 paper -> shadow -> canary -> limited live
   |
G6 PostgreSQL and Kubernetes promotion
```

## Root completion evidence

- `make check` 또는 동등한 단일 명령이 각 활성 서브프로젝트의 format, lint, unit, contract test를 실행한다.
- 검증 명령은 성공·실패·중단 뒤 자신이 만든 프로세스, 임시 파일, Podman/Kind/Testcontainers 리소스를 소유 label·세션·명시적 ID 기준으로 회수하며 전역 prune을 사용하지 않는다.
- 동일 fixture의 decimal·ID·timestamp가 Flutter, Go, Python contract test에서 일치한다.
- 2026-08-30 `make check`와 `make smoke`는 실제 preview/apply API를 쓰는 demo seed의 적용·완전 중복 no-op·충돌 fail-closed와 65개 Flutter parser/widget 검증을 통과했다. G2의 physical-device profile과 수동 screen-reader 증거는 여전히 열려 있다.
- G3.8C1은 계좌 전역 최초 선택 artifact의 불변 starting-capital authority만 증명한다. schema v15/backup v10의 독립 session digest/count, exact restore objects, v9/schema-v14 owned-copy empty-session proof와 strategy/order corruption fail-closed가 필요하며, runner·성과·profit·production readiness를 뜻하지 않는다. 세부 근거는 [`gates/g3j-paper-accounting-session.md`](gates/g3j-paper-accounting-session.md)에 둔다.
- G3.8C2는 account-global session과 같은 execution policy에 묶인 ex-post closed-bar BUY/SELL, exact cost와 replay-derived cash/FIFO/PnL, schema v17/backup v11 복구만 증명한다. 브로커 체결, marks/equity/returns/drawdown, scheduler, threshold, 자동 halt/rollback 또는 live readiness가 아니다. 구현·태스크 리뷰와 fresh local/mock 명령 근거는 [`gates/g3k-paper-fill-accounting.md`](gates/g3k-paper-fill-accounting.md)에 분리한다.
- G3.8C3는 transaction-current order/market cutoff, complete `paper_fixture` daily-close marks, account-global cash/equity/return/drawdown, insert-only recovery proof를 local에서 검증한다. 이는 profit, threshold, automatic halt/rollback, public UI, broker truth, deployment 또는 live readiness가 아니며, 세부 근거는 [`gates/g3l-paper-performance-evidence.md`](gates/g3l-paper-performance-evidence.md)에 둔다.
- G3.8D는 current non-`no_strategy` selection의 첫 C3 point를 attribution anchor로 삼는 strategy-window paper performance evidence와 schema v19/backup v13 proof를 검증한다. 이는 threshold, decision, automatic halt/rollback, public UI, broker truth, deployment 또는 live readiness가 아니며, 세부 근거는 [`gates/g3m-paper-strategy-performance.md`](gates/g3m-paper-strategy-performance.md)에 둔다.
- G3.8E는 복구 검증된 latest same-selection G3.8D evidence에만 고정 v1 local paper threshold를 적용하고, action이면 모든 captured armed authority의 결정적 halt와 exact one-pop rollback을 한 transaction에서 append한다. schema v20/backup v14, v13 owned-copy, full forward/reverse recovery와 cleanup matrix를 검증하지만 scheduler, public API/UI, broker call, promotion, deployment, live authority 또는 수익성을 뜻하지 않는다. 세부 근거는 [`gates/g3n-paper-performance-policy.md`](gates/g3n-paper-performance-policy.md)에 둔다.
- G3.8F1은 external scheduler가 호출할 수 있는 local one-shot CLI `paper-run-due`와 내부 `runDuePaperPerformancePolicy`를 추가한다. 최신 available local fixture close만 as_of로 쓰고 C3/D/E idempotent journal을 재사용해 retry와 두 owner 동시 실행이 같은 durable 결과로 수렴함을 검증하며, 완료 chain retry 전 paper 성과 정책 root recovery를 다시 증명한다. daemon, public API/UI, broker call, credential, deployment, shadow/live promotion 또는 수익성을 뜻하지 않는다. 세부 근거는 [`gates/g3o-scheduled-paper-runner.md`](gates/g3o-scheduled-paper-runner.md)에 둔다.
- G3.8F2는 전역 current strategy selection과 exact account를 묶는 schema v21 singleton lease, 단조 증가 fencing token, 10초 heartbeat/30초 TTL, stale-owner 회수, C3/D/E transaction 내부 exact fence 재검증과 `paper-run-loop`의 success/failure/SIGINT/SIGTERM cleanup을 local에서 검증한다. 현재 selection 자체가 전역이므로 runner도 전역 직렬화하며, broker call·credential·public API/UI·deployment·shadow/live authority 또는 수익성을 뜻하지 않는다. 세부 근거는 [`gates/g3p-always-on-paper-runner.md`](gates/g3p-always-on-paper-runner.md)에 둔다.
- local 검증, broker sandbox, live readiness, 실제 운영 증거를 따로 보고한다.
- G1.15는 기존 native holding 계산을 sanitized GET과 독립 Flutter 상세에 연결한다. 같은 원장 revision의 수량·원가·가격·평가, exact nanosecond 24시간 경계, 통화별 합계와 sample/stale 경계, empty/partial/retained-error 화면 및 내부 ID 비노출을 로컬 검증한다. 2026-09-05 `make check`는 Go 전체, Flutter 74개, Python 17개, JSON 계약 15개와 scoped cleanup을 통과했다. 전체 계좌 평가, broker-backed freshness, 물리기기·수동 screen-reader 검증 또는 배포 증거는 아니다. 세부 근거는 [`gates/g1k-holding-valuation-view.md`](gates/g1k-holding-valuation-view.md)에 둔다.
- external deploy, credential, live 주문, push는 명시 승인 없이는 실행하지 않는다.
- G3.8G1은 기존 SMA 판정을 공유한 offline 목표 제안과 입력 hash·종목·연구 이후 시점 경계만 증명한다. 2026-09-05 `make check`의 Go 전체·Flutter 전체·Python 25개·JSON 16개 구문 검사·owned cleanup self-test가 통과했다. JSON schema 필드/분기는 별도 회귀 검사이며 완전한 JSON Schema validator 실행은 아니다. 재해시 가능한 비신뢰 입력은 Go registry·선택 검증을 대체하지 않는다. 신호 admission·session/bar ingress·paper 체결 운영 연결은 G3.8G2에 남아 있다. [세부 gate](gates/g3q-paper-signal-proposals.md)

- G3.8G2A는 비신뢰 proposal을 저장 series에서 독립 검증하고 현재 선택·소유 lease 확인과 v3 signal/OPEN paper order 기록을 한 transaction으로 연결한다. `make check` 통과 후 재시도 수정은 focused race로 검증했다. 만료 후 기존 결과 재조회와 무주문 결정의 불변성도 확인했다. 수동 CLI·원본 CSV ingress·초기화·lease 종료는 아래 G3.8G2B에서 검증하며, 지속 제안 생성·수집·실행을 포함한 G3.8G2 전체는 미완료다. [세부 gate](gates/g3r-paper-proposal-admission.md)

- G3.8G2B는 수동 로컬 초기화·CSV/연구 원본 검증·fill→policy→signal 실행 경로와 소유권 보존 중지를 로컬 검증했다. FIFO 거절·누락 봉 부분체결·정책중지 재시작에 더해 실제 실행 파일의 SIGINT/SIGTERM, SIGKILL 후 실제 TTL 만료·중복 없는 복구를 확인했다. 리뷰 보강 후 `make check`와 소유 자원 정리가 통과했다. 상시 실행·증권사 연결·배포 증거가 아니며 전체 G3.8G2는 미완료다. [gate](gates/g3s-local-paper-workflow.md), [사용법](docs/local-paper-workflow.md)

- G3.8G2C는 `signal_cli --watch`로 같은 연구 후보의 새 마지막 봉 제안을 자동 생성한다. 생성·프레이밍만 담당하며 durable 전달·Go 접수·주문 실행은 아직 연결하지 않는다. 반복 입력과 입출력/프로세스 실패, 전체 `make check`·소유 자원 정리를 로컬 통과했다. [gate](gates/g3t-paper-proposal-watch.md)

세부 acceptance는 [`gates/`](gates/)의 leaf gate를 따른다.
