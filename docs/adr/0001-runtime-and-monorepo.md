# ADR-0001: Runtime and Monorepo Boundaries

- Status: Accepted
- Date: 2026-08-23
- Supersedes: docs-only React/PWA assumption and cloud-time PostgreSQL selection

## Context

Omni Folio는 iOS·Android·web 클라이언트, 여러 증권 API, 정확한 원장, 차트, 주문, 백테스트와 단계적 자동매매를 한 저장소에서 개발한다. 휴대폰은 OS 정책 때문에 정시 background execution을 보장하지 못하고, 실주문과 원장은 하나의 항상 켜진 authority가 필요하다. 초기 사용자는 한 명이므로 분산 인프라보다 명시적인 경계와 복구 가능성이 먼저다.

## Decision

```text
apps/client (Flutter: iOS, Android, web)
          |
          | versioned HTTP/SSE contract; decimal strings
          v
services/core (Go modular monolith)
  ledger | portfolio | broker | market data | orders | risk
          |
          +-- SQLite, one writer, local/single-node profile
          |
          +-- PostgreSQL before multi-replica or Kubernetes profile

services/research (Python)
  research + backtest + reproducible artifacts
  no broker credential, no order-submit permission

contracts
  OpenAPI + JSON Schema + cross-runtime fixtures

infra
  local process/OCI first; Kubernetes only after its entry gate
```

### Client: Flutter

Flutter 하나로 iOS, Android, app-centric web을 제공한다. 현재 UI 코드가 없으므로 React 자산을 폐기하는 비용이 없다. 서버가 대량 CSV, 시계열 downsampling, 계산을 맡고 클라이언트는 화면에 필요한 read model만 받는다. 이 경계는 Flutter web에서 isolate가 없는 제약도 피한다.

Flutter는 공식적으로 iOS, Android, web 배포를 지원하며 app-centric web 경험에 적합하다고 설명한다. 큰 목록은 lazy builder를 사용하고 release/profile mode에서 60Hz 16ms, 120Hz 8ms frame budget을 실제 fixture로 측정한다. ([supported platforms](https://docs.flutter.dev/reference/supported-platforms), [web FAQ](https://docs.flutter.dev/platform-integration/web/faq), [performance](https://docs.flutter.dev/perf/best-practices))

### Execution and API core: Go

하나의 Go binary와 같은 domain package에서 `api`, 이후 `worker`, `runner`, `execution-gateway` 역할을 command로 나눈다. Go의 내장 동시성과 표준 HTTP·profiling 도구는 다수 provider stream, bounded backpressure, 작은 OCI image, p50/p95/p99 측정에 맞는다. 측정된 hot path가 생긴 뒤에만 PGO를 사용한다. ([Go](https://go.dev/), [concurrency](https://go.dev/doc/effective_go#concurrency), [PGO](https://go.dev/doc/pgo))

### Research and backtesting: Python

Python은 전략 탐색, 데이터 분석, 백테스트, 보고서에 사용한다. batch/CLI로 시작하고 운영 DB 쓰기 권한, broker credential, 외부 order-submit egress를 주지 않는다. 실시간 전략이 필요해지면 versioned signal/target만 Go risk/execution 경계로 보낸다. Python `asyncio`는 I/O에 적합하고 NumPy 계열은 벡터 계산에 강하지만, 실행 authority를 두 언어에 나누지 않는다. ([asyncio](https://docs.python.org/3/library/asyncio.html), [NumPy](https://numpy.org/doc/stable/user/whatisnumpy.html))

### Backend ecosystem decision

언어는 서비스 수만큼 미리 늘리지 않는다. Java와 Kotlin은 서로 다른 backend 후보가 아니라 같은 JVM 생태계로 평가한다.

| Runtime | Best fit | Why it is not the default now | Promotion trigger |
|---|---|---|---|
| Go | broker I/O, API, order/risk authority, small OCI image | exact decimal과 exhaustive state transition을 application code/test로 보강해야 함 | current default |
| Python | research, vectorized analysis, backtest/report | operational authority와 CPU research가 같은 failure domain이 됨 | current research boundary |
| Kotlin/JVM + Spring | `BigDecimal`, sealed state model, Java broker SDK, mature transaction/observability stack | 한 명이 운영하는 초기 앱에 framework·memory surface가 크고 Python research 경계는 여전히 필요 | 필수 broker SDK가 JVM-only이거나 팀/JVM estate가 주 운영 표준이 될 때 Go core를 대체 평가 |
| Rust | bounded low-latency parser/calculation/execution hot path | adapter 반복 속도와 운영 생태계 비용이 현재 병목보다 큼 | profile에서 DB/network가 아닌 Go CPU·GC가 p99 budget을 깨는 것이 재현될 때만 좁게 도입 |

Kotlin/JVM은 금융 도메인에서 강한 대안이다. `BigDecimal`과 sealed hierarchy, Spring observability·scheduling은 장점이지만, 현재 목표는 가장 작은 always-on authority와 빠른 multi-broker I/O다. Java virtual thread나 Rust의 GC 없는 실행은 측정 전 선택 근거로 쓰지 않는다. ([BigDecimal](https://docs.oracle.com/en/java/javase/25/docs/api/java.base/java/math/BigDecimal.html), [Kotlin sealed classes](https://kotlinlang.org/docs/sealed-classes.html), [Spring observability](https://docs.spring.io/spring-boot/reference/actuator/observability.html), [Java virtual threads](https://docs.oracle.com/en/java/javase/25/core/virtual-threads.html), [Rust](https://rust-lang.org/))

### Storage: SQLite first, PostgreSQL gate

초기 원장 authority는 Go 프로세스와 같은 host의 SQLite 한 파일이다. 직접 network filesystem으로 공유하지 않고 write connection을 하나로 제한한다. SQLite는 동시 writer가 하나이며 multi-host/network access에는 client/server DB를 권고한다. ([appropriate uses](https://www.sqlite.org/whentouse.html), [network caveats](https://www.sqlite.org/useovernet.html))

다음 중 하나가 필요하면 dual-write 없이 maintenance migration으로 PostgreSQL에 승격한다: 두 번째 API replica, API/worker 독립 확장, 높은 동시 쓰기, HA/PITR, managed database, Kubernetes. PostgreSQL `NUMERIC`과 MVCC를 사용하고 row count, checksum, ledger invariant, order sequence를 검증한다. ([numeric](https://www.postgresql.org/docs/current/datatype-numeric.html), [MVCC](https://www.postgresql.org/docs/current/mvcc-intro.html))

## Alternatives considered

| Option | Strength | Cost here | Decision |
|---|---|---|---|
| React DOM + Expo | React/TypeScript 생태계와 DOM 접근성 | web/mobile UI가 두 갈래가 되고 chart·native 차이를 별도 유지 | Flutter 단일 client를 우선 |
| Kotlin Multiplatform | Android/iOS 공유와 JVM 생태계 | web UI는 아직 별도 판단이 필요하고 서버 authority와 모바일 domain 공유 이점이 작음 | Kotlin 중심 팀이 될 때 재평가 |
| Python-only backend | 가장 적은 backend 언어와 빠른 quant 통합 | 주문 authority, CPU 분석, API가 같은 failure/scale domain에 결합 | research만 Python |
| Java/Kotlin backend | BigDecimal, sealed state model, 성숙한 서버·관측·broker SDK 생태계 | 개인 앱에 JVM 운영비와 framework surface가 크고 Python research는 남음 | JVM-only SDK 또는 팀/JVM 운영 표준이 생기면 core 대체 평가 |
| Rust backend | 메모리 안전성과 예측 가능한 자원 | broker adapter와 제품 반복 비용이 크며 현재 병목은 network/DB | Go profile이 실제 SLA를 깨면 좁은 component로 검토 |
| PostgreSQL from day one | 즉시 multi-process·Kubernetes 친화 | local-first Phase A에 DB daemon·backup 운영을 먼저 강제 | SQLite ceiling을 측정한 뒤 승격 |

## Non-negotiable boundaries

- 휴대폰 background task는 opportunistic cache refresh와 알림 보조만 한다. runner, reconciliation, kill switch, token renewal, order submit은 always-on server에서 실행한다.
- mobile SQLite는 `server_revision`, `as_of`, `sync_cursor`가 있는 read cache다. offline order outbox는 없다.
- API JSON decimal은 canonical string이다. DB와 각 runtime에서 precision/scale과 rounding boundary를 검증한다.
- `live-enabled`는 환경변수, 앱 토글, runner 시작만으로 만들 수 없다. 서버의 만료 있는 owner 승인, broker/account/strategy allowlist, promotion evidence, healthy kill switch를 매 주문 전에 검증한다.
- Python과 client는 contracts에만 의존하고 Go domain package나 operational tables에 직접 의존하지 않는다.

## Kubernetes entry gate

Kubernetes manifest는 다음이 모두 증명된 뒤 만든다.

1. PostgreSQL migration과 restore drill이 통과했다.
2. API는 stateless하고 worker/runner는 DB claim·lease·fencing을 사용한다.
3. OCI image, non-root, read-only filesystem, resource requests/limits, probes가 검증됐다.
4. load test가 독립 scaling 필요를 보여 준다.
5. rollout은 신규 주문 차단, reconciliation, lease handoff, smoke, rollback 순서를 통과한다.
6. secret encryption/RBAC와 외부 secret store 경계가 결정됐다.

Kubernetes Job/CronJob과 rolling Deployment도 중복 실행 가능성을 없애지 않으므로 DB idempotency와 fencing은 유지한다. ([Jobs](https://kubernetes.io/docs/concepts/workloads/controllers/job/), [CronJobs](https://kubernetes.io/docs/concepts/workloads/controllers/cron-jobs/), [Deployments](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/))

## Consequences

- 처음부터 네 언어를 섞지 않는다. 제품 runtime은 Dart, Go, Python 세 개이며 SQL과 wire schema만 공유한다.
- 백테스트와 실전 기대수익은 분리된다. promotion에 사용하는 simulation/risk parity는 Go authority와 contract test로 별도 증명한다.
- local profile은 가장 작게 유지하지만 PostgreSQL 승격을 실제 migration으로 취급한다.
- Kubernetes, Redis, Kafka, service mesh, plugin SDK, service-per-broker는 아직 만들지 않는다.
