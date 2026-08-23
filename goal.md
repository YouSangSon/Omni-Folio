# Omni Folio Goal

정식 목표와 제품·안전·아키텍처 계약은 [`docs/goal-prompt.md`](docs/goal-prompt.md)를 따른다. 구현자는 작업을 시작하기 전에 해당 문서와 [`docs/omni-folio-plan.md`](docs/omni-folio-plan.md), [`docs/omni-folio-research-report.md`](docs/omni-folio-research-report.md), [`DESIGN.md`](DESIGN.md)를 읽는다.

## 실행 권한

- 합의된 제품 목표 안의 가역적인 로컬 설계·구현·테스트·문서화·로컬 Git 체크포인트는 추천안을 선택해 재승인 없이 계속 진행한다.
- 구현 중 결함, 누락, 더 단순하거나 안전한 경로를 발견하면 필요한 범위에서 계획과 구현을 함께 개선하고 판단 근거를 기록한다.
- 추측성 기능, 사용되지 않는 추상화, 측정 근거 없는 분산 인프라는 추가하지 않는다.
- 실제 자금 주문, live 주문 활성화, 외부 배포·push·publish, credential 또는 유료 외부 자원 변경, 파괴적·비가역 작업은 별도 명시 승인을 받는다.

## 현재 권장 경로

Phase A는 Flutter 하나로 iOS·Android·app-centric web을 제공하고, Go 모듈러 모놀리스가 ledger/order/risk/broker authority를 소유한다. Python은 research/backtest 전용이며 broker credential이나 주문 제출 권한을 갖지 않는다. SQLite single-writer local에서 `CSV import → preview → idempotent apply → append-only ledger → holdings/cash/P&L → versioned backup/restore` 수직 슬라이스를 먼저 검증한다.

PostgreSQL maintenance migration과 restore 증명 전에는 multi-replica 또는 Kubernetes를 도입하거나 manifest를 만들지 않는다. live 주문은 서버가 owner 승인 만료, broker/account/strategy allowlist, promotion evidence, healthy kill switch를 **매 주문** 검증할 때만 허용한다. 휴대폰 background는 cache refresh와 push 보조만 하며 주문·reconciliation·kill switch의 authority가 아니다.
