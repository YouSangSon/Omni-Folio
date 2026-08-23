# Omni-Folio Documentation

문서의 기준 진입점입니다. 같은 결정을 여러 파일에서 재정의하지 않고 아래 owner 문서로 연결합니다.

## 처음 읽는 순서

1. [`../goal.md`](../goal.md) — 현재 목표, 실행 권한, 안전 상한
2. [`../PLAN.md`](../PLAN.md) — Now/Next/Later 실행 순서
3. [`../GATES.md`](../GATES.md) — 단계별 promotion tree
4. [`../DESIGN.md`](../DESIGN.md) — Flutter 제품 경험과 접근성 계약
5. [`adr/0001-runtime-and-monorepo.md`](adr/0001-runtime-and-monorepo.md) — Flutter/Go/Python, SQLite/PostgreSQL/Kubernetes 경계

## 목적별 문서

| 목적 | 문서 | 역할 |
|---|---|---|
| 도메인 용어·불변식 | [`../CONTEXT.md`](../CONTEXT.md) | ledger, broker truth, reconciliation, live-disabled의 공통 언어 |
| 구현용 전체 목표 | [`goal-prompt.md`](goal-prompt.md) | 제품·도메인·자동매매·cloud acceptance의 정식 prompt |
| 상세 실행 계획 | [`omni-folio-plan.md`](omni-folio-plan.md) | 조사, 단계, 위험, review decision ledger |
| API·오픈소스 조사 | [`omni-folio-research-report.md`](omni-folio-research-report.md) | 공식 소스와 후보 생태계 비교 |
| 브로커·UX 결정 | [`broker-priority-and-ux.md`](broker-priority-and-ux.md) | 키움 우선, 토스증권 두 번째, Toss-inspired 원칙 |
| 로컬 프로젝트 재사용 | [`reuse-audit.md`](reuse-audit.md) | Parallax/Mimir/akasha에서 채택·보류·거절한 패턴과 provenance |
| UI token | [`../design-system/omni-folio/MASTER.md`](../design-system/omni-folio/MASTER.md) | 색상, typography, state, responsive/accessibility 규칙 |
| 보안 보고 | [`../SECURITY.md`](../SECURITY.md) | credential·취약점 보고와 사고 대응 |

## 실행 가능한 계약

- HTTP API source of truth: [`../contracts/openapi.json`](../contracts/openapi.json)
- JSON/decimal/fixture 계약: [`../contracts/`](../contracts/)
- Gate별 acceptance: [`../gates/`](../gates/)
- 현재 Kiwoom dated execution leaf: [`../gates/g4g-kiwoom-dated-execution-scan.md`](../gates/g4g-kiwoom-dated-execution-scan.md)
- Root 검증 명령: `make check && make smoke`

문서가 코드·계약과 충돌하면 실제 동작을 조용히 문서에 맞추지 않습니다. 먼저 충돌을 기록하고 owner 계약과 executable test를 함께 갱신합니다. 브로커 limit·인증·endpoint는 바뀔 수 있으므로 adapter 구현 시 공식 문서와 실제 응답을 다시 확인합니다.
