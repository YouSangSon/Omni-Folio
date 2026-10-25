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

Flutter에는 공통 파일 선택 표준 API가 없어 공식 [file_selector 1.1.0](https://pub.dev/packages/file_selector/versions/1.1.0)의 플랫폼 대화상자를 사용한다. BSD-3-Clause 고지는 Flutter license registry에 포함된다. 현재 lock은 `cross_file 0.3.5+5`이며 프로젝트의 고정 Flutter 3.47.1로 해결했다. iOS용 CSV UTI와 Android/web용 확장자·MIME 필터를 함께 지정한다. macOS 지원을 새로 구성하지 않는다.

- `apps/client/lib/csv_file.dart`: 크기 metadata로 1MiB 초과를 먼저 거절하고, 읽은 chunk를 누적하기 전에 실제 바이트 수를 확인해 초과 시 stream을 취소한다. metadata 길이를 읽기 endpoint로 삼지 않는다. lock에 고정한 [cross_file 0.3.5+5](https://pub.dev/packages/cross_file/versions/0.3.5%2B5)는 경로 기반 `File.openRead`와 메모리 기반 `XFile.fromData`의 끝 범위 처리가 다르므로 실제 Android 반환 형식도 검사한다. 이 제한은 Dart 누적 buffer와 decode·preview에 받아들이는 CSV 한도다. Android 선택기는 그 전에 native 전체 byte 배열을 할당하므로 큰 파일의 OOM·지연을 방지하지 못한다. OS/provider의 복사·브라우저 Blob 메모리·부정확한 provider metadata 아래 원본 파일의 완전성도 보장하지 않는다. 선택한 사용자 파일은 삭제하지 않는다.
- 표준 `utf8.decode`가 잘못된 UTF-8을 거절하고 첫 BOM만 제거한다. 따라서 서버 `file_sha256`은 BOM 제거 후 실제 전송한 CSV의 해시다. 원본 파일 바이트의 해시 보존을 주장하지 않는다. [Dart decoder 규칙](https://api.dart.dev/dart-convert/Utf8Decoder/convert.html)
- `ActivityPage`: 취소·같은 내용 선택은 입력·selection·preview·receipt·기존 오류를 유지한다. 읽기·형식·용량 오류도 입력과 결과를 유지하며 경로·CSV 본문·원본 예외를 표시하지 않는다. 다른 내용이 성공하면 이전 preview와 receipt를 함께 무효화한다.
- 파일 선택·읽기 중에만 텍스트를 read-only로 두고 preview/apply와 파일 선택의 재진입을 막는다. 기존 preview/apply 중 수동 편집과 stale-preview 검사는 유지한다. 응답이 늦게 도착해도 dispose된 화면을 갱신하지 않는다. 버튼·오류·loading은 기존 theme·live region·reduced-motion widget을 재사용한다.

검증 중: 작은 reader unit/fake와 widget 검사, analyzer, web·Android·iOS 빌드 결과는 아래에 기록한다. 실제 OS 파일 대화상자·클라우드 파일 provider·물리 VoiceOver/TalkBack은 별도이며 이 단위에서 증명하지 않는다. 로컬 통합/E2E·DB stack은 실행하지 않는다.
