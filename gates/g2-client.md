# G2 Client Gate

## Pass when

- 같은 Flutter codebase가 iOS, Android, web에서 build된다.
- first slice가 trust 상태, import preview, apply receipt, stale/error/empty/success를 표시한다.
- mobile cache는 read-only replica이며 offline order를 저장하지 않는다.
- 44pt/48dp touch target, semantic label, Dynamic Type/text scale, light/dark contrast, reduced motion을 검증한다.
- first slice의 representative import/list fixture에서 60Hz p95 frame budget을 profile mode로 기록한다. chart budget은 실제 chart가 시작되는 G4 진입 gate다.

## Evidence

- Pending widget/integration tests and profile report.
