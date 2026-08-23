# Omni Folio Goal

정식 목표와 제품·안전·아키텍처 계약은 [`docs/goal-prompt.md`](docs/goal-prompt.md)를 따른다. 구현자는 작업을 시작하기 전에 해당 문서와 [`docs/omni-folio-plan.md`](docs/omni-folio-plan.md), [`docs/omni-folio-research-report.md`](docs/omni-folio-research-report.md), [`DESIGN.md`](DESIGN.md)를 읽는다.

## 실행 권한

- 합의된 제품 목표 안의 가역적인 로컬 설계·구현·테스트·문서화·로컬 Git 체크포인트는 추천안을 선택해 재승인 없이 계속 진행한다.
- 구현 중 결함, 누락, 더 단순하거나 안전한 경로를 발견하면 필요한 범위에서 계획과 구현을 함께 개선하고 판단 근거를 기록한다.
- 추측성 기능, 사용되지 않는 추상화, 측정 근거 없는 분산 인프라는 추가하지 않는다.
- 신뢰할 수 있는 오픈소스 프레임워크와 라이브러리가 정확성·성능·개발 속도를 실질적으로 높이면 직접 재구현하지 않고 사용한다. 다만 유지보수 상태, 라이선스, 보안 이력, 버전 고정과 골든 fixture 교차검증을 먼저 통과한다.
- 실제 자금 주문, live 주문 활성화, 외부 배포·push·publish, credential 또는 유료 외부 자원 변경, 파괴적·비가역 작업은 별도 명시 승인을 받는다.

## 현재 권장 경로

Phase A는 asdf로 고정한 Flutter stable 하나로 iOS·Android·app-centric web을 제공하고, Go 모듈러 모놀리스가 ledger/order/risk/broker authority를 소유한다. Python은 research/backtest 전용이며 broker credential이나 주문 제출 권한을 갖지 않는다. SQLite single-writer local에서 `CSV import → preview → idempotent apply → append-only ledger → holdings/cash/P&L → versioned backup/restore` 수직 슬라이스를 먼저 검증한다. 첫 브로커는 키움 REST API이고 `read-only → 차트·실시간 → 모의주문` 순서로 완성한다. 키움 계약이 통과한 뒤 토스증권 Open API를 두 번째 어댑터로 추가한다. 공개 리서치·문서형 콘텐츠나 SEO가 실제 요구될 때만 Next.js를 별도 web surface로 추가하며, Flutter 제품 화면을 React로 중복 구현하지 않는다.

실제 키움 OHLCV를 연결하기 전에는 provider-neutral local fixture로 API·chart·접근성·성능 계약을 검증한다. sample·synthetic·fixture 시세는 API와 화면에서 실시간이 아님을 명시하고 live/current 데이터와 혼합하거나 broker 연동 증거로 승격하지 않는다.

Flutter 제품 경험은 영웅문 화면을 재현하지 않는다. 토스증권에서 참고한 쉬운 용어, 한 화면 한 결정, 점진적 상세 공개, 국내·미국의 일관된 흐름, 명확한 주문 확인과 접근성을 Omni Folio 고유 디자인으로 구현한다. 상세한 공급자·UX 결정은 [`docs/broker-priority-and-ux.md`](docs/broker-priority-and-ux.md)를 따른다.

PostgreSQL maintenance migration과 restore 증명 전에는 multi-replica 또는 Kubernetes를 도입하거나 manifest를 만들지 않는다. live 주문은 서버가 owner 승인 만료, broker/account/strategy allowlist, promotion evidence, healthy kill switch를 **매 주문** 검증할 때만 허용한다. 휴대폰 background는 cache refresh와 push 보조만 하며 주문·reconciliation·kill switch의 authority가 아니다.

투자 알고리즘은 고정된 일회성 기능이 아니라 자동 개선 루프를 갖는다. Python 연구 프로세스가 versioned 전략과 제한된 파라미터 후보를 자동 생성하고, 불변 데이터 snapshot에서 비용·지연을 포함한 시계열 분할과 walk-forward 검증을 수행해 champion/challenger evidence를 만든다. 검증 통과 후보는 자동으로 `research_candidate → paper_candidate → paper → shadow`까지만 승격하거나 실패 시 이전 champion으로 되돌린다. 실행 중인 전략 코드를 자기 수정하거나 생성 코드를 바로 실행하지 않으며, 실제 자금의 canary/live 승격은 자동화하지 않고 별도 owner 승인과 Go risk/execution gate를 요구한다.
