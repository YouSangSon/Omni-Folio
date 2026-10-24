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


<a id="web-startup-observation-2026-10-25"></a>

## 웹 초기 전송과 첫 프레임의 단일 로컬 관측 (2026-10-25)

문제: 전체 glyph를 보존한 약 9.9 MiB TTF는 알려진 크기였지만, 브라우저에서 전송된 양과 초기 프레임의 관계는 기록하지 않았다. 파일 크기, 요청 완료, 프레임 신호, 사용자가 실제로 글자를 읽는 시점은 서로 다른 관측이다. 작은 전송량이 곧 빠른 화면을 보장하지 않고, 빠른 신호만으로 글자 표시를 증명할 수도 없다.

기존 원본 폰트와 앱은 유지했다. 현재 문구만 남기는 subset은 이후 이름·오류 메시지의 글자를 잃을 수 있고, 압축이나 새 폰트 도입은 같은 glyph·플랫폼 계약을 별도로 확인해야 한다. 먼저 실제 전송을 관측하는 방법을 택했다. source `ea597400a66a267c1e8a7cfab63a9e6199009abc`, client tree `a44fc6c4f533d466f214e7661e725161bea9b8d3`의 immutable 사본에서 Flutter 3.47.1 stable의 웹 release를 1회 빌드했다. `--no-web-resources-cdn --no-wasm-dry-run --no-pub`를 사용했고 pub resolution은 offline/enforced lock으로 고정했다. 20.6초에 성공했으며 원본 파일과 빌드 폰트 hash가 같았다.

관측은 Apple M1 Max·macOS 15.8.1·HeadlessChrome 151.0.7922.34, 1280×720/DPR1에서 한 번만 했다. 정적 HTTP/1.0 서버 1개 worker, 압축 없음, `Cache-Control: no-store`, 별도 CPU/네트워크 제한 요청 없음이다. DB·API는 띄우지 않고 `OMNI_API_URL=http://127.0.0.1:9`로 빌드한 뒤 `connect-src 'self'`로 그 API 주소를 차단했다. 따라서 초기 오류 화면 관측이며 정상 원장 조회나 실제 서비스 지연은 아니다. service worker의 제어도 없었다.

원본 HTML을 바꾸는 대신 이번 임시 서버가 응답하는 사본의 head에 `flutter-first-frame` 이벤트 시각과 CSP 위반을 기록하는 짧은 관측 코드를 넣었다. 생성된 JS·WASM·font는 빌드 hash 그대로다. [고정 Flutter rendering binding](https://github.com/flutter/flutter/blob/3.47.1/packages/flutter/lib/src/rendering/binding.dart#L60-L65)은 post-frame callback에서 이 신호를 보내며, installed web engine의 `platform_dispatcher.dart`가 브라우저 이벤트로 전달하는 것을 확인했다. 이는 모니터에 픽셀이 표시된 시각을 측정하는 API가 아니다. [W3C Resource Timing](https://www.w3.org/TR/2026/CRD-resource-timing-20260901/)으로 완료된 요청의 본문 크기·시각을 읽었다. `transferSize`는 header 추정값 등을 포함하므로 본문 `encodedBodySize`와 구분하며, 제공사 청구 트래픽으로 환산하지 않는다.

| 관측 | 이 실행의 값 | 해석 범위 |
| --- | --- | --- |
| Noto 본문 | 10,414,588 bytes | source와 같은 전체 TTF, 압축·HTTP cache 재사용 없음 |
| 완료된 하위 리소스 본문 | 18,855,157 bytes / 9개 | 모두 이번 정적 서버 origin이며 Noto의 비중은 55.2% |
| Noto 요청 | 시작 234.8 ms → 완료 250.3 ms, 15.5 ms | loopback 요청 시간이며 font 파싱·레이아웃 시간을 분리하지 않음 |
| Flutter 첫 프레임 신호 | 400.2 ms | navigation 시작 기준 신호, 첫 한글 표시 시각이나 물리 raster budget이 아님 |
| 오류 | 예상한 API CSP 차단 5개, uncaught JS 오류 0개 | backend를 의도적으로 분리한 오류 화면 |

[구조화 관측값](../.ecc/benchmarks/web-startup-20261025.json)에는 소스·환경·조건·요청별 본문·시각·자원 관측과 cleanup을 담았다. 로컬 `output/g2-web-startup-ea597400a66a267c1e8a7cfab63a9e6199009abc/`에는 원본 웹 빌드, `build.json`, raw/summary JSON, 관측 서버 receipt와 `home.png`를 보존했다. 캡처에서는 본문·navigation·버튼의 한글이 읽혔지만 400.2 ms 당시의 glyph 표시를 촬영한 것은 아니다. 로그는 `docs/_internal/skill-outputs/goal-20261025-web-startup-*`이다.

소유한 빌드 root는 회수됐고 build/server exit0, 서버 PID34166·브라우저 PID34406·listener54072의 부재를 확인했다. 공용 host의 swapout 누적값은 빌드 동안 같았지만 브라우저 관측과 정리 구간에 증가했다. 이를 이번 앱만의 비용으로 귀속하지 않았고 추가 runtime 관측을 중단했다. 표본 1개이며 OS cache·host 경합·실제 인터넷 조건을 통제한 비교가 아니다.

적용 확인: 이후 font를 줄이더라도 이 실행과 같은 글자 범위·압축·cache·host 조건을 맞추고, 첫 프레임 신호 뒤 늦게 글자가 뜨는지 별도로 확인해야 한다. 현재 값으로 p95·초기 표시 예산·성능 개선율·실기기·수동 screen reader·origin 부재에서의 offline bootstrap을 통과 처리하지 않는다. G2 전체와 실제 전송 조건의 후속 검증은 계속 열려 있다.
