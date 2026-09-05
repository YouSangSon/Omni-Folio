# G2 local font delivery

Date: 2026-09-05. Branch `fix/client-local-fonts`; baseline `1329e0f`. Root owns implementation/test/browser evidence; `continuous_ingress_review` performs read-only review. Research helper failed with a model-capacity error; root completed the bounded research directly.

## Contract and decision

The local app must display its Korean UI without fetching fallback fonts from an external host. This means a fresh browser may reach the loopback web/API origin, not that the app can bootstrap when its origin is unavailable. No service worker, offline trading queue, or broker authority is introduced.

Use the design's existing Noto Sans KR family as one unmodified upstream variable TTF, plus its OFL notice in the app asset bundle. Pin exact upstream commit, hash and 10,414,588-byte cost in [asset provenance](../apps/client/assets/fonts/README.md). Keep full upstream glyph coverage rather than a subset inferred from today's strings. This is not universal Unicode coverage or a startup performance pass. No runtime font-download package, font processing toolchain, or new typography framework is added.

`cupertino_icons` 1.0.9 is exact-pinned with the official archive hash in `pubspec.lock`; it is Flutter's asset-only package with no runtime Dart dependencies. The real release build changes the missing-font warning into successful tree shaking (257,628 → 1,472 bytes), and the actual browser fetches that asset successfully. This proves delivery, not usage of every glyph or physical iOS icon correctness. Its MIT notice is retained in Flutter's generated notices. The 2026-09-05 OSV query for Pub `cupertino_icons` 1.0.9 returned `{}` (no reported advisories, not a guarantee of safety).

## TDD and actual rendering boundary

1. RED: bundled-font test fails because `assets/fonts/NotoSansKR.ttf` is absent. Existing 84 Flutter tests still pass; wrapper exits 2 and cleans up.
2. Initial GREEN: font/icon assets and text-family assertions pass, but a fresh release browser with external requests blocked shows missing glyph boxes in the first-run import button. Root inspected the actual screenshot `output/playwright/local-font-red-375.png`; external fallback requests fail. Asset existence and ordinary text-theme tests were insufficient.
3. Root cause: explicit `ElevatedButton`/`OutlinedButton` text styles replace the default style. Flutter `button_style_button.dart` passes the resolved style directly to its `Material`, so the app's custom style also needs the font family. Fix the two shared button themes, not each button caller; app-bar/navigation overrides likewise specify the bundled family.
4. Add a resolved rendered-text assertion to the existing 320px/200%-text first-run action test; it fails before the shared fix. The asset test name is narrowed to asset availability, not physical glyph usage.

## Final results

- `make check CORE_TEST_PACKAGES=./internal/exact` exits 0 after the shared button fix: Flutter 85 tests, analyzer, format, Go exact (cached), full Go vet, Python 34/5.459s, 17 JSON contracts and resource cleanup pass. Go production did not change; this is not a fresh full Go regression/race run.
- Real Flutter 3.47.1 / Dart 3.13.1 release build completes in 21.3s with local CanvasKit and no missing-font warning. HeadlessChrome 152 on macOS starts as a new nonpersistent `about:blank` session; routing is installed before the first app navigation, allowing only the exact loopback API/web origins and blocking every other request. No previous app storage is imported.
- The initial Home (375×812), first-run import navigation, Connections (1440×1000), and empty-policy monitor in dark/reduced-motion mode (768×1024 and scrolled 812×375 landscape) work with readable Korean. The four final screenshots `output/playwright/local-font-{home-375,connections-1440,monitor-dark-768,monitor-landscape}.png` were inspected. No horizontal clipping or missing-glyph boxes were observed. This is bounded rendering evidence, not an all-screen visual regression baseline or physical screen-reader/profile pass.
- The full final request inventory contains 17 loopback GETs, **zero external requests**, and successful Noto/Cupertino/Material asset loads. The sole console error is expected empty-DB broker reconciliation 404, with zero warnings. The OFL asset is reachable with the same hash as its source file; generated NOTICES includes `cupertino_icons` and its copyright. Upstream OFL line 21 has one trailing space, deliberately preserved for byte-identical provenance; `git diff --check` excludes only that vendored license, not application code.
- Final browser wrapper exits 0 (Python 34/4.141s) and closes exact browser PID 41905, API 43582 and web 43583. Those PIDs, ports 18082/18083, temp root `omni-folio-test.fySVpg`, build/coverage and named browser inventory are absent afterward. Prior browser RED's PIDs 23176/23629/23630 and `omni-folio-test.4EHAdH` are likewise absent. Screenshots and ignored QA harness remain intentional evidence. No Podman/Kind resources were created.
- Independent review confirms both shared themes cover all current Elevated/Outlined callers and corrects the earlier assumption that every custom style inherits the default family. No production blocker remains. Physical iOS/Android glyphs/profile, payload/first-paint budget, broader glyph coverage and true offline-origin bootstrap remain open.

## Sources and research scope

One focused Exa search plus the official [Flutter custom-font guide](https://docs.flutter.dev/cookbook/design/fonts), [Cupertino package](https://pub.dev/packages/cupertino_icons) and pinned upstream [metadata/license](https://github.com/google/fonts/tree/b38c5c93af322c45f633e17ac440ec1e6c94d489/ofl/notosanskr) were read. GitHub/Pub metadata independently supplied file sizes, immutable hashes and dependency declarations. Flutter supports TTF across supported platforms; variable weights avoid duplicate static assets. The UI skill's font query returned no Flutter-specific match after one retry, so official SDK guidance and observed behavior were used instead. This is bounded implementation research, not an ecosystem-wide comparison.
