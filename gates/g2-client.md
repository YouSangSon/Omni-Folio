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
- 2026-08-27 `flutter analyze` and 44 tests pass. Overview, asset-detail, import-preview and atomic-apply failures replace upstream exception text with context-specific recovery copy; G4O also verifies one-read chart range filtering, matching exact-table rows, selected semantics, and valid empty refresh recovery.
- Release builds pass for web, iOS without codesigning (`Runner.app`, 16.4 MB), and Android (`app-release.apk`, 50.0 MB).
- Playwright at 390×844 and desktop reaches `/v1/status`, `/v1/portfolio/snapshot`, preview and apply with HTTP 200 and zero console errors. Screenshot: `output/playwright/omni-overview-mobile-toss-inspired.png` (local ignored evidence).
- A reproducible Flutter SDK `integration_test` profile harness measures a 120-holding lazy list and import-screen transition, excluding network and DB time. A local Android-emulator session collected 614 frames: build p95 1.026 ms passed, raster p95 16.674 ms missed the strict 16.67 ms phase budget by 0.004 ms, and total-span p95 was 22.222 ms. The original JSON identifies only `platform=android`, so it does not independently prove the observed Android 16 API 36 session metadata. This is a recorded near miss, not a pass.
- A second run with complete metadata on Flutter 3.47.1, `Medium_Phone_API_36.1_emulator`, Android 16/API 36, and a measured 411.43×914.29 logical viewport at 2.625× collected 595 frames: build p95 2.745 ms passed, raster p95 31.851 ms failed, and total-span p95 was 54.008 ms. The two emulator runs show material variance; neither is physical-device release evidence and the poorer complete run is the current gate result.
- The G4B profile evidence included a horizontally scrollable 500-bar chart and bidirectional scrolling of its lazy exact-data table. Two valid optimized emulator runs pass the build/raster budget at `0.928/5.749 ms` and `1.158/16.623 ms`; see `g4b-market-data-chart.md`. G4O now makes the harness render all 500 bars, select the 30-day range, and exercise its 31-row exact table, but that updated harness has not produced new physical-device evidence.
- The harness now requires Flutter version, device, and OS defines and records them with the measured viewport in JSON artifacts. It uses Flutter's SDK `integration_test` as a direct dev dependency while direct main pub dependencies remain unchanged. Flutter 3.47.1 nevertheless links `IntegrationTestPlugin` into the iOS release binary through generated SPM integration. That impact is tracked, not cleared for store distribution; isolate or remove the harness before shipping if the release artifact must exclude test instrumentation.
- Chrome 151 profile built successfully but `flutter drive` produced no test connection/result in two bounded attempts and was stopped. No web profile claim is made.
- 2026-08-30 `make check` passes 65 Flutter tests. The responsive shell uses mobile bottom navigation and a bounded desktop rail, Overview limits its holding preview while Holdings retains a builder delegate, snapshot provenance is strictly parsed into non-identifying counts before realized PnL, each currency has signed gain/loss text and semantic-token color, and each holding exposes one combined actionable semantics node. `make smoke` also exercises `make seed-demo` through the real preview/apply API for fresh apply, exact-duplicate no-op, and optimized-Python conflict rejection.
- 2026-09-05 the new paper monitor was rendered from the real Go API in Flutter 3.47.1 release / Chrome 152 at 375, 768 and 1440 widths, including stored policy, offline retention, dark/reduced-motion preference and keyboard refresh/return. This is not a visual-regression baseline, frame profile or screen-reader pass. External Korean fallback-font requests and a Cupertino font warning leave cold-start offline text/platform-icon coverage open. See [browser evidence](g3ad-paper-monitor-browser.md).
- 2026-09-05 [local-font follow-up](g2d-local-fonts.md) bundles pinned Noto/OFL and Cupertino assets, fixes explicit button-style inheritance, and passes 85 Flutter tests plus a fresh release browser with zero external requests across exercised screens. This closes the observed delivery defect, not universal glyphs, physical platform rendering, payload performance, or offline-origin bootstrap.
- **Gate remains open:** strict raster p95 on representative physical Android/iOS hardware and manual VoiceOver/TalkBack or equivalent screen-reader evidence are not yet recorded. Emulator results do not substitute for physical-device release evidence.

## Responsive import state (2026-10-23)

The release browser verification recorded in [G1.21](g1q-valuation-metadata-cutoff.md#release-browser-supplement) exposed lost CSV/preview state when crossing the 900px shell boundary. The old shell replaced `content` with `Row/Expanded/content`, disposing `ActivityPage`; a pending apply could therefore finish without its receipt being displayed.

The shell now keeps `Row/Expanded/content` at every width and inserts only the desktop rail/divider conditionally. This preserves the active page's element and state without a global key, new controller, cache or tab-retention policy. API, idempotency, money, navigation actions and design tokens are unchanged.

- Both new 899→900 and 900→899 widget cases first failed with an empty CSV value. After the fix they pass for draft, selection, focus, preview, sanitized apply error, pending apply, disabled apply button, completed receipt and unchanged idempotency key. Activity requests remain one initial load plus the deliberate post-apply refresh. Logs: `goal-20261023-responsive-import-{red,focused}.log` under `docs/_internal/skill-outputs/`.
- Final `make check GO=true` exits 0: Flutter 122, Python 34, format/analyze, resource preflight/cleanup and 17 JSON syntax checks. Go test/vet is explicitly skipped: core source/tests, root tests, contracts, Makefile and pinned tool versions equal PR #23's fully checked `272dfa7`, so its passing Go/vet evidence is reused. This is not a new full Go run or full JSON Schema validation.
- Independent read-only review found no blocker. Physical-device/profile and manual screen-reader gates above remain open.
- Actual Flutter release/Go API at 375×812 ↔ 1280×900 preserves CSV, preview and the same receipt in both directions. Tab/Enter creates the preview; duplicate apply returns 0 applied/3 skipped at revision3. Editing input clears preview/receipt; malformed input returns 400, its error survives resizing, and corrected CSV retries successfully. Pending apply and failure/retry timing are controlled by the widget tests rather than inferred from fast real HTTP.
- Inspected screenshots: `output/playwright/responsive-import-{preview-375,receipt-375,receipt-1280,error-375}.png`. Network and console evidence: `docs/_internal/skill-outputs/goal-20261023-responsive-import-{network,console}.txt`. Only the intentional bad-CSV 400 and absent broker-reconciliation 404 are request failures; CPU-only rendering does not establish a performance result. A stale browser selector during retry setup was rejected before action; a fresh snapshot resolved it.
- Browser wrapper exits 0 with the real API/release build, preflight 1/1 and Python 34/34. Owned test roots/processes, browser tab/daemon and ports 18082/18083 are clear. Browser log: `goal-20261023-responsive-import-browser.log`.

Follow-up observed in the error screenshot: when there is no retained preview, the existing generic preview-error copy still says the previous preview is preserved. Correct that claim according to actual state; this layout fix does not change the error text.

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
