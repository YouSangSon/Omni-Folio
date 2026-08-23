# G2 Client Gate

## Pass when

- 같은 Flutter codebase가 iOS, Android, web에서 build된다.
- first slice가 trust 상태, import preview, apply receipt, stale/error/empty/success를 표시한다.
- mobile cache는 read-only replica이며 offline order를 저장하지 않는다.
- 44pt/48dp touch target, semantic label, Dynamic Type/text scale, light/dark contrast, reduced motion을 검증한다.
- first slice의 representative import/list fixture에서 60Hz p95 frame budget을 profile mode로 기록한다. chart budget은 실제 chart가 시작되는 G4 진입 gate다.

## Evidence

- 2026-08-24 `flutter analyze` and 7 tests pass, including malformed financial JSON fail-closed, retained/retry error state, atomic apply receipt, preview invalidation, and a 320×480 surface at 200% text.
- Release builds pass for web, iOS without codesigning (`Runner.app`, 16.4 MB), and Android (`app-release.apk`, 49.5 MB).
- Playwright at 390×844 and desktop reaches `/v1/status`, `/v1/portfolio/snapshot`, preview and apply with HTTP 200 and zero console errors. Screenshot: `output/playwright/omni-overview-mobile-toss-inspired.png` (local ignored evidence).
- **Gate remains open:** representative profile-mode p95 frame timing, manual VoiceOver/TalkBack or equivalent screen-reader pass, and reduced-motion verification are not yet recorded.
