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
└─ G1.14 versioned recurring FIFO cost allocation + residual conservation
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
└─ G3.7 atomic execution halt and strategy rollback proof
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
- local 검증, broker sandbox, live readiness, 실제 운영 증거를 따로 보고한다.
- external deploy, credential, live 주문, push는 명시 승인 없이는 실행하지 않는다.

세부 acceptance는 [`gates/`](gates/)의 leaf gate를 따른다.
