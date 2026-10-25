# G4P First-Run Import Recovery

G4P1–3 scope: 실제 core가 정상 반환하는 `never_verified`와 빈 portfolio snapshot을 홈 empty state에서 기존 거래 내역 가져오기 경로로 연결한다. 이 첫 실행 복구 단위는 API·schema·dependency·broker request·주문 권한을 추가하지 않는다.

- [x] G4P1: `never_verified`이며 cash, holdings, realized PnL이 모두 빈 snapshot은 성공한 0종목 요약 대신 첫 거래 내역 가져오기 행동을 표시한다.
  CHECK: `cd apps/client && asdf exec flutter test test/vertical_slice_test.dart --plain-name 'first-run empty snapshot links to import at 200 percent text'`
  EXPECT: 버튼이 보이고 History의 CSV `TextField`로 이동한다.

- [x] G4P2: 비어 있지 않은 `never_verified` snapshot은 숨기지 않고 기존 신뢰 경고와 데이터를 유지한다.
  CHECK: `cd apps/client && asdf exec flutter test test/vertical_slice_test.dart --plain-name 'live-disabled trust banner remains explicit'`
  EXPECT: `아직 확인되지 않음` 경고와 기존 연결 경로가 유지된다.

- [x] G4P3: 첫 실행 CTA는 320px·200% text에서 세로 스크롤로 접근 가능하고 48dp 이상 target과 screen-reader label을 제공한다.
  CHECK: `cd apps/client && asdf exec flutter analyze && asdf exec flutter test`
  EXPECT: analyzer clean; 47 parser/widget tests pass without overflow or widget exception.

This gate does not prove imported data correctness beyond existing G1, physical VoiceOver/TalkBack, physical-device layout, live broker freshness, credentials, order capability, or live readiness.

<a id="csv-file-selection"></a>

## CSV 파일 선택

붙여넣기만 제공하던 거래 내역 화면에 파일 선택을 연결한다. 파일은 입력 초안을 채울 뿐이며 기존 `preview → 명시적 apply → receipt`가 계속 원장 변경의 유일한 경로다. 선택 직후 API를 호출하지 않는다. API·CSV schema·서버 행 수 검증·preview fingerprint·apply 멱등성 계약은 바꾸지 않는다.

Flutter에는 공통 파일 선택 표준 API가 없어 공식 [file_selector 1.1.0](https://pub.dev/packages/file_selector/versions/1.1.0)의 플랫폼 대화상자를 사용한다. 패키지의 BSD-3-Clause 라이선스를 확인했다. 현재 lock은 `cross_file 0.3.5+5`이며 프로젝트의 고정 Flutter 3.47.1로 해결했다. iOS용 CSV UTI와 Android/web용 확장자·MIME 필터를 함께 지정한다. 실제 iOS plugin 0.5.3+7의 최소 iOS 15는 기존 Runner 설정과 같아 배포 대상을 변경하지 않았다. macOS 지원을 새로 구성하지 않는다.

- `apps/client/lib/csv_file.dart`: 크기 metadata로 1MiB 초과를 먼저 거절하고, 읽은 chunk를 누적하기 전에 실제 바이트 수를 확인해 초과 시 stream을 취소한다. metadata 길이를 읽기 endpoint로 삼지 않는다. lock에 고정한 [cross_file 0.3.5+5](https://pub.dev/packages/cross_file/versions/0.3.5%2B5)는 경로 기반 `File.openRead`와 메모리 기반 `XFile.fromData`의 끝 범위 처리가 다르므로 실제 Android 반환 형식도 검사한다. 이 제한은 Dart 누적 buffer와 decode·preview에 받아들이는 CSV 한도다. Android 선택기는 그 전에 native 전체 byte 배열을 할당하므로 큰 파일의 OOM·지연을 방지하지 못한다. OS/provider의 복사·브라우저 Blob 메모리·부정확한 provider metadata 아래 원본 파일의 완전성도 보장하지 않는다. 선택한 사용자 파일은 삭제하지 않는다.
- 표준 `utf8.decode`가 잘못된 UTF-8을 거절하고 첫 BOM만 제거한다. 따라서 서버 `file_sha256`은 BOM 제거 후 실제 전송한 CSV의 해시다. 원본 파일 바이트의 해시 보존을 주장하지 않는다. [Dart decoder 규칙](https://api.dart.dev/dart-convert/Utf8Decoder/convert.html)
- `ActivityPage`: 취소·같은 내용 선택은 입력·selection·preview·receipt·기존 오류를 유지한다. 읽기·형식·용량 오류도 입력과 결과를 유지하며 경로·CSV 본문·원본 예외를 표시하지 않는다. 다른 내용이 성공하면 이전 preview와 receipt를 함께 무효화한다.
- 파일 선택·읽기 중에만 텍스트를 read-only로 두고 preview/apply와 파일 선택의 재진입을 막는다. 기존 preview/apply 중 수동 편집과 stale-preview 검사는 유지한다. 응답이 늦게 도착해도 dispose된 화면을 갱신하지 않는다. 버튼·오류·loading은 기존 theme·live region·reduced-motion widget을 재사용한다.

### 확인한 근거와 미완료 범위

앱 구현은 `cd91d16c7316b4dd8a23c69a43227097ec8ba0be`, 최종 검증 head는 `e8eaec341027e42e3a1271f43839e984af88f405`다. [PR #34](https://github.com/YouSangSon/Omni-Folio/pull/34)의 main 병합 `b82944e492ebd88309db47d596ebffb0cf21cba1`은 최종 head와 전체 tree가 같다. client tree는 모두 `5dce64fdd8e36967da255d049c8260ab774acc5c`다. 독립 리뷰에서 실제 Android selector의 `XFile.fromData`와 경로 기반 fake의 차이를 찾아, 정상 16바이트 파일의 `RangeError`를 재현한 뒤 reader를 수정했다.

- 관련 명령은 `cd apps/client && flutter test --no-pub --concurrency=1 test/csv_file_test.dart test/vertical_slice_test.dart test/activity_pagination_test.dart`다. [최종 원격 실행](https://github.com/YouSangSon/Omni-Folio/actions/runs/40149903980)은 **105개 단일 실행·ARM64 빌드·패키지 검증까지 전체 통과**했다. 권한 checker에는 실제 manifest의 정상 경로와 8개 변형 반례를 적용했다. 로그: `docs/_internal/skill-outputs/goal-20261025-csv-android-ci-final.log`; 구조화 근거: `goal-20261025-csv-android-ci-final-receipt.json`.
- `flutter analyze --no-pub`는 불필요 import 한 건을 제거한 최종 상태에서 exit 0, `No issues found!`다. Dart format·diff 확인과 네 언어 README의 실행 블록 20개·링크 35개·anchor 7개·코드 표기 215개·heading 30개 대응도 확인했다. 선택만으로 API 호출 없음, 명시적 preview/apply와 멱등성 키, 취소·동일 내용·오류 보존, 변경 무효화, 중복 클릭·dispose·899/900px·키보드·320px/200%·양쪽 theme/reduced motion을 fake에서 확인했다. 실제 OS 선택 결과로 표현하지 않는다.
- web release는 동일 소스의 격리 사본에서 고정 lock·`--no-web-resources-cdn --no-wasm-dry-run --no-pub`로 exit 0이다. `output/g2-web-startup-cd91d16c7316b4dd8a23c69a43227097ec8ba0be/`의 `COMPLETE`·`build.json`·web 결과를 보존했다. 이 단위는 새 startup 측정이나 브라우저 파일 선택을 실행하지 않았다. 임시 root와 그 경로를 사용하는 프로세스가 없음을 확인했다.
- Android는 격리 사본에서 worker 1·비병렬·단일 실행 Gradle·heap 1536MiB/Kotlin in-process로 시도했다. swapout이 2,819,419→2,842,239→2,843,151로 증가해 이번 wrapper만 종료했다. make exit 2/recipe 143이며 **APK 완료 근거가 없다.** 기록된 7개 PID와 임시 root·불완전 출력이 모두 없음을 확인했다. `goal-20261025-csv-file-android-build.log`와 `goal-20261025-csv-android-owner.json`에 중단·정리 근거를 남겼다. 다른 프로젝트의 실행이나 설정은 바꾸지 않았다.
- 이후 swapout이 약 9분간 2,888,147로 정체된 상태에서 기존 iOS target을 순차 실행했다. `output/ios-release-a3041dcba3d9b878717c930dc3a95b6b6cc7dd87/`의 `COMPLETE`·`build.json`·ARM64 `Runner.app`을 보존했고 exit 0이다. 같은 client tree `5dce64fdd8e36967da255d049c8260ab774acc5c`에서 운영 lock 보존·테스트 플러그인 부재·바이너리 hash·소유 임시 root/프로세스 회수를 확인했다. 이 빌드 동안 swapout 값은 유지됐다. 무서명이며 기기 설치/실행은 포함하지 않는다.
- 같은 안정화 구간의 Android 재개는 Javac에서 `integration_test` 클래스 부재로 실패했다. 새 하네스가 추가한 `--no-pub`는 고정 SDK의 [등록 재생성 조건](https://github.com/flutter/flutter/blob/3.47.1/packages/flutter_tools/lib/src/runner/flutter_command.dart#L2052-L2059)을 건너뛰어, 일반 pub get이 만든 dev 등록을 남긴다. [릴리스 Android 필터](https://github.com/flutter/flutter/blob/3.47.1/packages/flutter_tools/lib/src/flutter_plugins.dart#L1409-L1426)가 적용되는 정상 build 명령에는 이 옵션을 쓰지 않는다. 실제 수정 경로의 GREEN은 아래 CI에서 확인한다. 로그 `goal-20261025-csv-file-android-build-resumed.log`는 실패 상태로 보존했고 임시 root·불완전 출력·그 경로의 프로세스 부재를 확인했다. swapout 증가도 다시 관측해 추가 로컬 빌드는 보류했다.

### 공유 호스트 대신 활성화된 무료 CI 사용

2026-10-25 읽기 전용 GitHub API에서 저장소 `private=false`·`Actions enabled=true`를 확인했다. [공식 runner 기준](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)의 공개 저장소용 표준 `ubuntu-24.04`를 사용하며, 공개·동일 저장소 PR 조건을 job에 둔다. 비활성 서비스를 켜거나 과금·credential·보호 브랜치 설정을 바꾸지 않는다. artifact/cache 업로드는 없고, [저장 용량에 산입되지 않는 로그](https://docs.github.com/en/billing/concepts/product-billing/github-actions)에 결과 metadata만 남긴다. 이는 확인 시점의 무료 조건이며 장기 과금 차단을 보장하지 않는다.

`.github/workflows/client-android-release.yml`은 고정 action·Flutter 원본 SHA, read-only token·credential 비보존, worker 1·ephemeral Gradle user home을 사용한다. 최종 관련 unit/fake 105개 → 정상 pub-enabled ARM64 release → 등록·manifest·architecture·원본 불변·APK hash를 한 job에서 순차 확인한다. 통과 로그의 `ANDROID_RELEASE_VERIFIED`를 PR head·client tree와 연결한 뒤에만 병합한다. 이미 통과한 web/iOS는 소스·tool pin·builder가 같으면 재사용한다.

첫 CI의 마지막 검사는 Internet만 기대해 AndroidX의 앱 전용 `DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION`에서 실패했다. [공식 AndroidX manifest](https://raw.githubusercontent.com/androidx/androidx/androidx-main/core/core/src/main/AndroidManifest.xml)와 [ContextCompat의 호환 수신자 보호](https://github.com/androidx/androidx/blob/androidx-main/core/core/src/main/java/androidx/core/content/ContextCompat.java)를 확인했다. 앱 manifest를 바꾸지 않고 package에 정확히 묶인 권한 한 개와 `signature` 보호 수준(정수 2)만 추가 검증한다. 추가·중복·SDK 한정 사용 권한, 약화/누락/추가 flag 보호 수준, 다른 package와 `permission-tree` namespace를 거절하는 작은 fixture를 포함한다. Draft 해제만으로 같은 head가 다시 실행되지 않도록 `ready_for_review` 이벤트는 받지 않는다.

최종 APK는 23,153,102 bytes·`android-arm64`, SHA-256 `9833de52999c41090052bbcd1d74710e8990e5e18cf875dbc42ce9a551ca8a31`이다. 패키지의 custom permission 보호 수준은 실제 `0x2`로 확인했다. APK는 CI 임시 결과이며 artifact로 업로드하지 않았다. iOS/web은 client·tool pin·iOS builder/Makefile hash가 같음을 확인해 재사용했다. merge 직전 main base `bf04342` 불변과 CI head 일치를 확인했고, 병합 뒤 전체 tree 일치·고유 commit 0·worktree 미사용을 확인해 이 단위의 작업 브랜치만 정리했다.

이 입력·상태 보존·컴파일 단위는 완료했다. 실제 OS 대화상자·클라우드 provider·물리 VoiceOver/TalkBack·실기기·서명/스토어와 my-cloud 배포는 별도 미검증이다. 제공사 청구 트래픽은 로그인 불가로 확인하지 못했으며 새 전송/배포를 실행하지 않았다. 로컬 통합/E2E·DB stack은 실행하지 않았다. G2 전체와 원래 제품 Goal의 완료를 뜻하지 않는다.

### 실제 web 파일 입력 확인 (2026-10-25)

main `4f8224b`와 동일한 client tree `5dce64f`를 가진 기존 `cd91d16` web release를 재사용했다. `build.json`의 원본 대응과 주요 생성 파일 hash를 확인했으며 재빌드는 하지 않았다. API URL은 응답하지 않는 `http://127.0.0.1:9`이고, DB·API 없이 loopback 정적 서버와 Brave 탭 하나만 실행했다. macOS·다크 화면 2560×1300에서 확인했으며 브라우저의 정확한 버전은 기록하지 않았다.

- 실제 DOM의 `CSV 파일 선택` 버튼을 누르고 브라우저 filechooser로 단일 파일을 지정했다. 한글 파일명의 UTF-8 BOM CSV가 기존 초안을 교체하고, BOM 없는 기대 문자열과 텍스트 필드 값이 정확히 같았다. 이 합성 입력은 파일 읽기 확인용이며 서버 CSV 계약을 검증한 결과는 아니다.
- 이어 잘못된 UTF-8 파일을 지정했을 때 고정된 한국어 오류가 표시되고, 직전에 읽은 정상 문자열은 바뀌지 않았다. screenshot `output/g2-csv-browser/utf8-error-retained.jpg`를 로컬 보존했다(SHA-256 `2feafe1e8d086cd80cc75bea75eeaadad9c4250099af44ba368634d2bb60359d`).

앞선 접근성 클릭에서는 filechooser 이벤트 대기가 실패했고 DOM 클릭에서는 성공했다. 자동화 입력 방식의 결과를 구분하며, 앞선 실패만으로 앱의 파일 선택 결함을 주장하지 않는다. 고정 `file_selector_web 0.9.5`의 `DomHelper.getFiles`는 표준 file input과 `change`·`cancel` 이벤트를 사용한다. filechooser 자동화는 native OS 창·클라우드 provider·실제 취소 동작을 증명하지 않는다.

관측 중 host swapout이 5,111,457→5,176,067로 증가해 두 사례 뒤에는 검사를 추가하지 않았다. 정적 서버 exit 0과 소유 PID·listener·임시 fixture 부재, 탭 종료를 확인했다. 다른 작업은 변경하지 않았고 증가 원인을 이 검증에 단정하지 않는다. 소유권·관측 기록은 `docs/_internal/skill-outputs/goal-20261025-csv-browser{,-dom}-owner.json`에 남겼다.

이 브라우저 실행에서는 preview/apply·receipt, 크기 초과·동일 파일·취소, API 자동 전송 유무를 검증하지 않았다. 해당 unit/fake 105개와 실제 브라우저 증거는 범위가 다르다. 네이티브 플랫폼·실기기·수동 screen reader와 VM 검증도 계속 열려 있다.


<a id="manual-entry"></a>

## 거래 직접 입력과 반영 결과 구분

Phase A의 수동 입력 후속이다. `거래 직접 입력`에서 기존 10가지 원장 유형 중 한 건을 준비하고 `검토용 초안 만들기`로 CSV 초안 전체를 바꾼다. 취소는 기존 입력·preview·receipt·오류를 보존한다. 화면 진입과 초안 작성만으로 API를 호출하거나 원장을 바꾸지 않는다. 기존 Go preview·명시적 apply·receipt만 사용하며 API·schema·dependency·주문 권한을 추가하지 않는다. 현재 mapping의 로컬 `account-main`과 `AAPL`만 기본 제공하며 실제 계좌 선택을 뜻하지 않는다. 다른 종목은 기존 Go 검토에서 미해결로 표시된다.

폼 진입마다 한 번 만든 source ID를 해당 초안에서 유지한다. 새 폼은 새 거래 의도이므로 같은 거래를 다시 작성하면 자동 중복 제거를 보장하지 않는다. 금액은 입력의 선행/후행 0을 정규화하고 기존 BigInt scale 계산을 공유한다. 매수는 수량×단가+수수료의 음수, 매도는 수량×단가−수수료이며 결과가 0이나 음수인 경우도 원문대로 검토한다. 응답 parser의 canonical decimal 요구는 유지한다. 시각은 달력·시간대·최대 9자리 소수초를 검사하되 문자열을 그대로 CSV에 넣고 Go가 UTC로 정규화한다. `CASH_VOID`의 대상은 receipt가 아닌 원본 `source_event_id`이며 대상·반대 금액·계좌·통화·시각·중복 취소 여부는 Go가 검증한다.

반영 중 CSV 편집은 계속 허용한다. 요청의 CSV·preview ID·멱등성 키는 앱 세션 소유자가 보존해 탭 이동으로 잃지 않는다. 다른 입력의 늦은 성공은 `이전 입력 적용 확인`으로 구분하고 현재 preview를 무효화한다. 네트워크·5xx·잘못된 응답·알 수 없는 오류는 미확정으로 남기며 새 직접 입력을 막는다. `이전 요청 재시도`는 읽기 전용 조회가 아니라 원래 요청을 같은 키로 다시 보내며 미반영이면 실제로 반영할 수 있다. HTTP status/code가 409/stale_preview 또는 400/preview_not_found·preview_has_errors·invalid_ledger일 때만 미반영 거절로 확정해 새 검토를 허용한다. 이 분류는 receipt를 먼저 찾고 이후 검증/실패 시 transaction을 rollback하는 기존 Go apply 경계에 근거한다. 앱 종료 후 durable 재시도는 이번 범위가 아니다.

폼은 기존 Material theme와 48dp target을 사용하고 오류 요약에 포커스를 옮겨 필드로 연결한다. 고정된 소수의 필드를 한 스크롤 안에 유지해 하단 제출 후에도 요약을 찾아갈 수 있게 한다. 375px·가로 화면·200% 글자·키보드·양쪽 theme/reduced-motion, 중복 클릭·취소·유형 변경·늦은 응답을 widget fake로 검사한다. 실제 접근성 기기 동작과 구분한다.

### 검증

- RED: [40158637857](https://github.com/YouSangSon/Omni-Folio/actions/runs/40158637857)에서 직접 입력 버튼 부재, [40160552773](https://github.com/YouSangSon/Omni-Folio/actions/runs/40160552773)에서 편집 후 늦은 반영 결과의 잘못된 귀속을 재현했다. 고정 Flutter 설치와 lock 해결은 통과했고 의도한 widget assertion에서 실패했다.
- `contracts/fixtures/manual-entry-cases.json`의 19개 공통 사례를 Flutter CSV 생성/응답 parser와 Go 실제 SQLite preview·apply·동일 키 재시도·동일 source 재검토가 함께 사용한다. 10유형, 큰 정수·소수 scale·0/음수 매도 대금, 정/역분할, FX 두 leg, 인용/한글 취소 대상, 음수 상쇄, 시간대의 날짜 경계·nanosecond를 포함한다.
- Widget 흐름은 BUY/DEPOSIT 작성·취소·유형 전환과 요청/결과 경계를 중심으로 검사한다. 10유형 전체의 화면 조작을 검증한 것으로 표현하지 않는다.
- GREEN: 검증 head `71111934085059081c03a42b8059025cd8d5d299`와 [PR #42](https://github.com/YouSangSon/Omni-Folio/pull/42)의 main 병합 `2ecf2a63ff6f15f804af095c3ceb3ea41145f396`은 전체 tree가 같다. Client tree는 `6ef3776bdbf2b2703ab55af244af350c88d1bcc6`다. 독립 소스 리뷰에서 재시도·확정 거절·금액/시각 경계를 확인했고, 두 차례 widget 수정 실패 뒤 별도 진단으로 표시/포커스/스크롤 문제를 검토했다.
- [최종 Core CI 40162233406](https://github.com/YouSangSon/Omni-Folio/actions/runs/40162233406): `TestManualEntry*` 실제 SQLite 공유19사례가 1.280초, 전체 core가 346.065초에 통과했다. Go format/vet·internal packages·Python39·JSON18·소유 자원 cleanup·source identity도 통과했다. 명령은 `make test FLUTTER=true CORE_TEST_PACKAGES='-run=^TestManualEntry -count=1 -timeout=10m ./...'` 및 `make check DART=true FLUTTER=true CORE_TEST_PACKAGES='-timeout=30m ./...'`다. 시간은 해당 runner의 검사 소요이며 성능 개선율이 아니다.
- [최종 Client CI 40162233402](https://github.com/YouSangSon/Omni-Folio/actions/runs/40162233402): 집중43·전체191개, Dart format/analyze, 새 web release와 ARM64 APK·운영 plugin 등록·권한 반례8·원본 불변이 모두 통과했다. 테스트 명령은 `flutter test --no-pub --concurrency=1 test/manual_entry_test.dart` 및 `flutter test --no-pub --concurrency=1`이다. Web과 APK는 정상 pub-enabled release 명령으로 만든다. APK는 23,349,830 bytes·SHA256 `38ff3f7b83dab0748eefdbdb7932adeb71ed5f627686bf56410090d9e4d23e27`; 등록 파일 SHA256은 `5d31e4d80984b4f5254d2e6629d2cc2b16760ab4774a94faa863c48897f18326`이다. INTERNET와 앱 전용 signature receiver 권한만 허용한다. artifact/cache 업로드 없이 metadata 로그를 보존했다.
- 중간 widget 검사에서 기본 450ms route보다 짧은 300ms 대기와 실제 클릭 위치 미확인을 발견했다. 단순 대기 시간을 늘리지 않고 유한 route 완료·outer scroll·실제 hit test로 바꿨다. 375px/200% 가로 넘침은 dropdown 확장과 Wrap의 지역 최소 너비 보정 뒤 재현되지 않았으며 높이52는 유지했다. 전체 검사에서 새 버튼이 기존 CSV-first 키보드 동작을 바꾼 사실도 찾아 순서를 복원하고 직접 입력까지 이어지는 키보드 검사를 추가했다. 실패 로그와 최종 원격 로그/receipt는 `docs/_internal/skill-outputs/goal-20261025-manual-*`로 보존한다.
- 로컬 runtime/native build는 공유 호스트 swap 압박 때문에 실행하지 않았다. 새 client tree의 iOS 빌드·실기기·브라우저 입력·수동 screen reader·앱 재시작 복구와 VM 검증은 포함하지 않는다. 과거 iOS 빌드는 다른 client tree이므로 이번 기능의 빌드 증거로 재사용하지 않는다.
