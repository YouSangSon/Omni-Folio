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
- The harness now requires Flutter version, device, and OS defines and records them with the measured viewport in JSON artifacts. It uses Flutter's SDK `integration_test` as a direct dev dependency while direct main pub dependencies remain unchanged. Flutter 3.47.1 nevertheless links `IntegrationTestPlugin` into the iOS release binary through generated SPM integration. That earlier artifact retains the plugin. The separate unsigned release path now verifies its exclusion while preserving this harness; see the [2026-10-25 packaging evidence](#ios-release-packaging-2026-10-25). Signing and store distribution remain unverified.
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

Follow-up observed in the error screenshot: when there is no retained preview, the generic preview-error copy still said the previous preview was preserved. The separate correction below closes this issue.

## Preview error state (2026-10-23)

Preview failures now append the retained-preview sentence only while `_preview` exists. The generic redacted error, input invalidation, retry, apply and idempotency contracts are unchanged.

- One regression first failed because a first-attempt failure claimed retained state. After the fix, its default-height viewport left the lazy preview card unbuilt; the test now uses the existing explicit-height pattern with view teardown. It verifies first failure, retained-preview retry failure, and failure after an input edit, including apply availability and no apply calls. The corrected focused run passes; both earlier failures remain in `goal-20261023-import-error-{red,focused,focused-corrected}.log` under `docs/_internal/skill-outputs/`.
- Final `make check GO=true` exits 0: Flutter 123, Python 34, format/analyze, resource preflight/cleanup and 17 JSON syntax checks. Core source/tests, contracts, root tests, Makefile and pinned toolchain equal checked `272dfa7`; its Go/vet evidence is reused, not rerun. Log: `goal-20261023-import-error-check.log`.
- Actual release web/Go API at 375×812: malformed CSV fails with 400 without a retained claim or preview; corrected golden CSV succeeds with 200 (including Tab/Enter); editing it invalidates the preview and another 400 again makes no retained claim. A second correction succeeds. Stopping only the owned API then makes retry fail while the whole prior preview remains, with the retained sentence and apply button. The full retained card was inspected at 375×1200. No apply was submitted in this copy-only browser check.
- Inspected screenshots: `output/playwright/import-error-{no-preview-375,invalidated-375,retained-375,retained-apply-375}.png`. Network/console/snapshots use the `goal-20261023-import-error-` prefix. Expected errors are missing optional reconciliation 404, bad CSV 400 and the intentional offline connection refusal; the network tool labels that failed connection pending. A refill initially missed Flutter's active editor and repeated the old invalid input; focusing before filling corrected it. Semantic scrolling did not expose the whole card and CDP wheel input was denied by the tool allowlist, so the supported taller viewport was used. CPU-only rendering establishes no performance claim.
- The browser build and UI checks passed, but its wrapper exited 2 during the trailing, unchanged Python suite: 11 subprocess cases exceeded existing 3-second deadlines. Measured host load was 124.83–139.88 on 10 logical CPUs; CLI import alone took 2.11s wall versus 0.11s CPU. A subsequent root-wrapped run of only the two affected tests passed in 8.907s with unchanged deadlines. Logs: `goal-20261023-import-error-{browser,python-focused}.log`. This does not relabel the failed wrapper as a pass or repeat the full suite.
- Owned test roots/processes, browser tab/daemon and ports 18082/18083 are clear. GitHub Actions has no workflows; physical-device/profile, manual screen-reader, real-broker and deployment evidence remain outside this validation.

## Android release networking (2026-10-25)

The main Android manifest now declares `INTERNET` so release builds can reach the Go API. Debug/profile already declared it, which hid the missing release permission from development runs. No API, Dart logic, dependency, signing or cleartext-policy setting changes.

- RED: `:app:processReleaseMainManifest` on archived base `e6963bc` produced a real release manifest without `INTERNET`. The final ARM64 release APK adds exactly that permission to the pre-existing permission multiset, including the AndroidX signature permission. The packaged application remains non-debuggable and has no new cleartext override. The final APK compiled in 59.8s (23.1 MB); the runtime copy SHA-256 is `73c38109cc4f1f1ccd86909fdd8235fc78e6375cb437ddf1e3e79ebd21fa8323`.
- Actual Android release runtime passed on Android 16 / SDK 36, full SDK 36.1, ARM64, emulator 36.3.10 at 1080×2400 / 420dpi. The installed package grants `INTERNET`. The real Go API and sample import produce USD 778 cash, AAPL quantity 6 and USD +78.6 realized PnL after the native refresh button. Connections opens an empty report; actual Python artifact registration plus native refresh displays exact percentages, windows and holdout dates. Removing the scoped reverse connection and reconnecting only this ADB transport produces the fixed failure message while retaining the shown report and original observation time. Restoring it advances the observation time without changing the registered result. Light/dark screenshots, scrolled holdout/baseline metrics and Back were inspected; app logs contain no fatal/Flutter error. Screenshots: `output/g2-native/android-{release-populated,report-populated,report-offline,report-dark-top,report-dark-bottom}.png`; XML/logs use the `goal-20261025-android-` prefix. Initial launch/theme-transition captures were not used as settled-screen evidence. Physical-device, manual screen-reader, performance and store-distribution checks remain open; signing still uses the existing local debug key.
- Go/Dart/Python source, tests, public contracts, root tests, Makefile and tool pins are unchanged from PR #28; its combined full Go, Flutter 135, Python 39 and JSON 17 coverage is reused. The 13 recorded source/contract/test digests were checked unchanged. This does not claim a new complete `make check` run.
- Harness failures are preserved: the first archived copy lacked Flutter-generated ignored Gradle wrapper files; the pinned SDK cache supplies them. A packaged-permission assertion initially assumed no AndroidX signature permission; comparing with the observed RED manifest corrected the expectation, and the preserved APK manifest passed that check. The first runtime attempt used an unsupported ADB listen hostname; a foreground listener on the isolated port is now verified before starting the emulator. An automatically forked ADB server was identified by PID/start/port and explicitly cleaned up. A later cold boot failed before app launch: `avdmanager` generated `target=android-0`; the public parser clamps this to API 3, which fails the ARM64 HVF API ≥21 gate. Only the owned AVD root ini was corrected to SDK 36 from the installed image’s actual `build.prop`; the same waiting runtime then booted and passed without changing the shared SDK, image, existing user AVD, security settings or boot deadline. [Parser](https://android.googlesource.com/platform/external/qemu/+/emu-master-dev/android/emu/avd/src/android/avd/info.c#513), [HVF condition](https://android.googlesource.com/platform/external/qemu/+/emu-master-dev/android-qemu2-glue/main.cpp#2673). Logs use `docs/_internal/skill-outputs/goal-20261025-android-*`.
- Build and runtime wrappers both exited 0 after their owned stop markers. Their temporary roots, recorded processes/groups and API/ADB/emulator listeners on 18094/5041/5580/5581 were confirmed absent. Failed attempts retain their failed status and logs. No new iOS build or physical-device result is claimed.
- All four READMEs now document the same local ARM64 APK build/install and scoped `adb reverse` flow. They retain identical command blocks, links, anchors and code literals. On-device loopback refers to the device; reversing the named port forwards it to the host API. This grants no broker credential or submit authority.

<a id="ios-release-packaging-2026-10-25"></a>

## iOS 릴리스의 테스트 플러그인 격리 (2026-10-25)

문제는 앱 코드의 테스트 사용 여부가 아니라 Flutter의 네이티브 플러그인 등록 방식이었다. 고정 SDK 3.47.1은 Android/Linux/Windows release에서 dev plugin을 걸러내지만 iOS/macOS에서는 전체 목록을 사용한다. `dev_dependencies`의 `integration_test`가 생성된 Swift package와 registrant를 통해 앞선 iOS release의 `IntegrationTestPlugin`으로 연결된 이유다. [고정 SDK 구현](https://github.com/flutter/flutter/blob/3.47.1/packages/flutter_tools/lib/src/flutter_plugins.dart#L1407-L1470), [상위 이슈 #163874](https://github.com/flutter/flutter/issues/163874), [CocoaPods 관련 이슈 #167379](https://github.com/flutter/flutter/issues/167379)가 이 경계를 설명한다. SwiftPM을 끄거나 지원하지 않는 `pub get --no-dev`를 사용하는 것으로 해결하지 않았다.

빌드는 원본 설계도에서 포장용 사본 하나를 만드는 방식이다. 커밋된 앱과 tool pin을 `git archive`로 복사하고, 그 사본에서만 공식 [`pub remove --offline integration_test`](https://dart.dev/tools/pub/cmd/pub-remove)를 실행한다. 변경 전후 운영 의존성의 전체 전이 목록·버전·source·lock 블록을 비교하고, 허용한 staged pubspec 두 파일 외 원본 파일의 hash를 다시 확인한다. 별도 profile 앱을 만드는 대안은 host 설정과 새로운 실행 경로를 추가하므로, 기존 측정 harness를 보존하는 이 방법을 택했다. 원본 `apps/client`·profile harness·pubspec·lock은 바꾸지 않았다.

구현은 `Makefile`의 `client-ios-release`와 `tools/ios_release.py`가 소유한다. Flutter config 생성 후 `xcodebuild -jobs 1`로 무서명 release를 한 번 빌드한다. plugin graph·registrant·SPM manifest와 완성된 앱의 모든 plist-declared Mach-O 실행 파일을 검사해 `IntegrationTestPlugin`이 남으면 실패한다. 운영 소스, 빌더와 Makefile의 hash, Flutter/Xcode 버전, 정확한 빌드 인자와 실행 파일 SHA-256을 `build.json`에 보관한다. 외부 symlink를 거부하고 내부 파일 링크는 실제 바이트로 복사한 뒤 hash를 비교해, 임시 빌드 경로를 지워도 결과가 유지되게 한다.

```sh
make check-ios-release
make client-ios-release API_URL=http://127.0.0.1:8080
```

- 작은 unit/fake 검사 11개가 0.045초에 통과했다. `tests/test_ios_release.py`는 운영 lock 변경, native plugin 잔존, 검사 불가능한 바이너리, 기존 출력 보존, 내부/외부 링크, 빌더 변경과 늦은 자식 실행 취소·owner 재사용을 다룬다. 실제 통합 테스트를 실행한 결과는 아니다. 로그: `docs/_internal/skill-outputs/goal-20261025-ios-release-unit-final.log`.
- 실제 빌드는 고정 Flutter 3.47.1 stable·Python 3.14.5·Xcode 26.3 (17C529)에서 exit 0, `BUILD SUCCEEDED`와 최종 검증 메시지를 기록했다. 직접 관찰한 Xcode 인자는 `-jobs 1`이다. 출력은 `output/ios-release-4736694f87b939d4e8841717afb0f0c35b8f0127/`이며 `COMPLETE`, `build.json`, ARM64 `Runner.app`을 보존했다. 로그: `docs/_internal/skill-outputs/goal-20261025-ios-release-build.log`.
- 이 결과의 앱 소스는 `4736694f87b939d4e8841717afb0f0c35b8f0127`, client tree는 `a44fc6c4f533d466f214e7661e725161bea9b8d3`이다. 빌더와 Makefile은 실행 시작 시점의 파일 hash로 따로 고정했다. 이후 도구·문서 커밋은 root SHA가 달라지므로 같은 앱 tree와 빌더 hash인지 확인해 이 증거를 연결한다. 최종 Runner SHA-256은 `57effadecbfc09c0ccd47018d81e48a2270df26cc94ca312d78c7e411e4ac644`이고 App/Flutter framework hash도 `build.json`에 있다.
- 원래 앱의 생성 파일과 기존 출력은 건드리지 않았다. 성공 뒤 이번 임시 root와 기록된 owner/child/Xcode PID·process group의 부재를 확인했다. 새 target은 광역 `clean-test-resources`를 호출하지 않고 자기 root만 회수하며, 회수가 확인된 뒤에만 `COMPLETE`를 만든다. 취소 시 늦은 자식의 진입을 막고 기존 bounded group cleanup을 재사용한다. 새 target 자체의 실제 SIGINT 실행은 검증하지 않았다.
- 관측 중 memory free가 43%→33%→49%로 변했고 swapout 누적값은 초기 증가 후 같은 값으로 안정됐다. 종료·OOM·디스크 부족은 관측하지 않았지만 자원 사용량 개선이나 성능 benchmark를 증명하지 않는다. 별도 DB·서버·시뮬레이터·실기기·VM 실행은 이 빌드에 포함하지 않았다.

같은 source SHA의 출력 경로가 이미 있으면 덮어쓰지 않고 실패한다. API URL은 앱에 들어가는 공개 build-time 값이므로 credential/query/fragment를 거부한다. 기본 loopback은 빌드 구성 예시이며 기기에서 호스트 API에 연결됐다는 뜻은 아니다. **무서명 artifact의 테스트 플러그인 제외까지만 통과**했으며 서명·설치·실기기 렌더링/성능·수동 screen reader·스토어 심사는 계속 미확인이다. G2 전체는 열려 있다.

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
