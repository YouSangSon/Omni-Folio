# G4P First-Run Import Recovery

Scope: 실제 core가 정상 반환하는 `never_verified`와 빈 portfolio snapshot을 홈 empty state에서 기존 거래 내역 가져오기 경로로 연결한다. API·schema·dependency·broker request·주문 권한은 추가하지 않는다.

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
