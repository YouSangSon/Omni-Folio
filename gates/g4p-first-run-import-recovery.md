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

앱 소스는 `cd91d16c7316b4dd8a23c69a43227097ec8ba0be`, client tree는 `5dce64fdd8e36967da255d049c8260ab774acc5c`다. 독립 리뷰에서 실제 Android selector의 `XFile.fromData`와 경로 기반 fake의 차이를 찾아, 정상 16바이트 파일의 `RangeError`를 재현한 뒤 reader를 수정했다. 용량 경계·한국어/BOM·잘못된 UTF-8·초과 stream 취소와 실제 메모리 XFile을 작은 검사로 확인했다.

- 관련 명령은 `cd apps/client && asdf exec flutter test --no-pub --concurrency=1 test/csv_file_test.dart test/vertical_slice_test.dart test/activity_pagination_test.dart`다. 마지막 실행은 101개 통과·화면 가시성 검사 4개 실패였다. 외부 ListView 스크롤과 화면 갱신을 명시하도록 해당 검사만 고친 뒤 `--name 'CSV selection fits|import operations never render upstream error details|ledger activity error is redacted and retryable'`로 4개가 통과했다. 생산 소스·공유 fixture가 같아 기존 101개 근거를 재사용했다. **단일 전체 실행 105개 통과는 아니다.** 로그: `docs/_internal/skill-outputs/goal-20261025-csv-file-client-final-v2.log`, `goal-20261025-csv-file-visible-fixed.log`.
- `flutter analyze --no-pub`는 불필요 import 한 건을 제거한 최종 상태에서 exit 0, `No issues found!`다. Dart format·diff 확인과 네 언어 README의 실행 블록 20개·링크 35개·anchor 7개·코드 표기 215개·heading 30개 대응도 확인했다. 선택만으로 API 호출 없음, 명시적 preview/apply와 멱등성 키, 취소·동일 내용·오류 보존, 변경 무효화, 중복 클릭·dispose·899/900px·키보드·320px/200%·양쪽 theme/reduced motion을 fake에서 확인했다. 실제 OS 선택 결과로 표현하지 않는다.
- web release는 동일 소스의 격리 사본에서 고정 lock·`--no-web-resources-cdn --no-wasm-dry-run --no-pub`로 exit 0이다. `output/g2-web-startup-cd91d16c7316b4dd8a23c69a43227097ec8ba0be/`의 `COMPLETE`·`build.json`·web 결과를 보존했다. 이 단위는 새 startup 측정이나 브라우저 파일 선택을 실행하지 않았다. 임시 root와 그 경로를 사용하는 프로세스가 없음을 확인했다.
- Android는 격리 사본에서 worker 1·비병렬·단일 실행 Gradle·heap 1536MiB/Kotlin in-process로 시도했다. swapout이 2,819,419→2,842,239→2,843,151로 증가해 이번 wrapper만 종료했다. make exit 2/recipe 143이며 **APK 완료 근거가 없다.** 기록된 7개 PID와 임시 root·불완전 출력이 모두 없음을 확인했다. `goal-20261025-csv-file-android-build.log`와 `goal-20261025-csv-android-owner.json`에 중단·정리 근거를 남겼다. 다른 프로젝트의 실행이나 설정은 바꾸지 않았다.
- 이후 swapout이 약 9분간 2,888,147로 정체된 상태에서 기존 iOS target을 순차 실행했다. `output/ios-release-a3041dcba3d9b878717c930dc3a95b6b6cc7dd87/`의 `COMPLETE`·`build.json`·ARM64 `Runner.app`을 보존했고 exit 0이다. 같은 client tree `5dce64fdd8e36967da255d049c8260ab774acc5c`에서 운영 lock 보존·테스트 플러그인 부재·바이너리 hash·소유 임시 root/프로세스 회수를 확인했다. 이 빌드 동안 swapout 값은 유지됐다. 무서명이며 기기 설치/실행은 포함하지 않는다.
- 같은 안정화 구간의 Android 재개는 Javac에서 `integration_test` 클래스 부재로 실패했다. 새 하네스가 추가한 `--no-pub`는 고정 SDK의 [등록 재생성 조건](https://github.com/flutter/flutter/blob/3.47.1/packages/flutter_tools/lib/src/runner/flutter_command.dart#L2052-L2059)을 건너뛰어, 일반 pub get이 만든 dev 등록을 남긴다. [릴리스 Android 필터](https://github.com/flutter/flutter/blob/3.47.1/packages/flutter_tools/lib/src/flutter_plugins.dart#L1409-L1426)가 적용되는 정상 build 명령에는 이 옵션을 쓰지 않는다. 실제 수정 경로의 GREEN은 아래 CI에서 확인한다. 로그 `goal-20261025-csv-file-android-build-resumed.log`는 실패 상태로 보존했고 임시 root·불완전 출력·그 경로의 프로세스 부재를 확인했다. swapout 증가도 다시 관측해 추가 로컬 빌드는 보류했다.

### 공유 호스트 대신 활성화된 무료 CI 사용

2026-10-25 읽기 전용 GitHub API에서 저장소 `private=false`·`Actions enabled=true`를 확인했다. [공식 runner 기준](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)의 공개 저장소용 표준 `ubuntu-24.04`를 사용하며, 공개·동일 저장소 PR 조건을 job에 둔다. 비활성 서비스를 켜거나 과금·credential·보호 브랜치 설정을 바꾸지 않는다. artifact/cache 업로드는 없고, [저장 용량에 산입되지 않는 로그](https://docs.github.com/en/billing/concepts/product-billing/github-actions)에 결과 metadata만 남긴다. 이는 확인 시점의 무료 조건이며 장기 과금 차단을 보장하지 않는다.

`.github/workflows/client-android-release.yml`은 고정 action·Flutter 원본 SHA, read-only token·credential 비보존, worker 1·ephemeral Gradle user home을 사용한다. 최종 관련 unit/fake 105개 → 정상 pub-enabled ARM64 release → 등록·manifest·architecture·원본 불변·APK hash를 한 job에서 순차 확인한다. 통과 로그의 `ANDROID_RELEASE_VERIFIED`를 PR head·client tree와 연결한 뒤에만 병합한다. 이미 통과한 web/iOS는 소스·tool pin·builder가 같으면 재사용한다.

현재 남은 조건은 해당 PR 소스에 대응하는 실제 Android CI 성공과 최종 관련 검사 통과다. main base가 바뀌면 변경 범위를 다시 확인한다. 실제 OS 대화상자·클라우드 provider·물리 VoiceOver/TalkBack·실기기·서명/스토어는 별도 미검증이다. 로컬 통합/E2E·DB stack은 실행하지 않았다.
