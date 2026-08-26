# G2 Client Gate

## Pass when

- 같은 Flutter codebase가 iOS, Android, web에서 build된다.
- first slice가 trust 상태, import preview, apply receipt, stale/error/empty/success를 표시한다.
- mobile cache는 read-only replica이며 offline order를 저장하지 않는다.
- 44pt/48dp touch target, semantic label, Dynamic Type/text scale, light/dark contrast, reduced motion을 검증한다.
- first slice의 representative import/list fixture에서 60Hz p95 frame budget을 profile mode로 기록한다. chart budget은 실제 chart가 시작되는 G4 진입 gate다.

## Evidence

- 2026-08-24 `flutter analyze` and 17 tests pass, including malformed financial JSON fail-closed, retained/retry error state, atomic apply receipt, preview invalidation, 320×480 surfaces at 200% text, semantic headings and trust detail, labeled 44pt/48dp targets, light/dark text contrast, reduced-motion static loading/zero-duration navigation, and the G4B chart/table states and semantics.
- 2026-08-26 `flutter test test/vertical_slice_test.dart` passes 18 tests. When service status is available but no portfolio snapshot can be loaded, the empty state now links directly to the existing transaction import flow instead of repeating the failed refresh; the test proves the CTA reaches the real CSV `TextField`.
- 2026-08-26 `flutter analyze` and 29 tests pass. G4K adds strict stored-reconciliation UI, and G4L adds strict local-order parsing, fixed-route handling, sanitized retained-known-good refresh behavior and 200% unknown-state semantics.
- Release builds pass for web, iOS without codesigning (`Runner.app`, 16.3 MB), and Android (`app-release.apk`, 49.7 MB).
- Playwright at 390×844 and desktop reaches `/v1/status`, `/v1/portfolio/snapshot`, preview and apply with HTTP 200 and zero console errors. Screenshot: `output/playwright/omni-overview-mobile-toss-inspired.png` (local ignored evidence).
- A reproducible Flutter SDK `integration_test` profile harness measures a 120-holding lazy list and import-screen transition, excluding network and DB time. A local Android-emulator session collected 614 frames: build p95 1.026 ms passed, raster p95 16.674 ms missed the strict 16.67 ms phase budget by 0.004 ms, and total-span p95 was 22.222 ms. The original JSON identifies only `platform=android`, so it does not independently prove the observed Android 16 API 36 session metadata. This is a recorded near miss, not a pass.
- A second run with complete metadata on Flutter 3.47.1, `Medium_Phone_API_36.1_emulator`, Android 16/API 36, and a measured 411.43×914.29 logical viewport at 2.625× collected 595 frames: build p95 2.745 ms passed, raster p95 31.851 ms failed, and total-span p95 was 54.008 ms. The two emulator runs show material variance; neither is physical-device release evidence and the poorer complete run is the current gate result.
- The harness was later expanded for G4B to include a horizontally scrollable 500-bar chart and bidirectional scrolling of the lazy exact-data table. Two valid optimized emulator runs pass the build/raster budget at `0.928/5.749 ms` and `1.158/16.623 ms`; see `g4b-market-data-chart.md`. These runs improve the local baseline but do not close this physical-device G2 gate.
- The harness now requires Flutter version, device, and OS defines and records them with the measured viewport in JSON artifacts. It uses Flutter's SDK `integration_test` as a direct dev dependency while direct main pub dependencies remain unchanged. Flutter 3.47.1 nevertheless links `IntegrationTestPlugin` into the iOS release binary through generated SPM integration. That impact is tracked, not cleared for store distribution; isolate or remove the harness before shipping if the release artifact must exclude test instrumentation.
- Chrome 151 profile built successfully but `flutter drive` produced no test connection/result in two bounded attempts and was stopped. No web profile claim is made.
- **Gate remains open:** strict raster p95 on representative physical Android/iOS hardware and manual VoiceOver/TalkBack or equivalent screen-reader evidence are not yet recorded. Emulator results do not substitute for physical-device release evidence.

## Reproduce

```sh
cd apps/client
flutter analyze
flutter test
FLUTTER_TEST_OUTPUTS_DIR="$PWD/../../output/g2-profile" \
  flutter drive --profile -d <device-id> \
  --dart-define=G2_FLUTTER_VERSION=<flutter-version> \
  --dart-define=G2_DEVICE=<device-model> \
  --dart-define=G2_OS_VERSION=<os-version> \
  --driver=test_driver/g2_profile_frame_test.dart \
  --target=integration_test/g2_profile_frame_test.dart
```

The profile command is expected to fail when either build or raster p95 exceeds 16.67 ms; it still writes `integration_response_data.json` on failure.
