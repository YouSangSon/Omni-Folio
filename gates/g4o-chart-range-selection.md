# G4O Local Daily Chart Display Range

Scope: 이미 검증된 daily candle 응답 안에서 Flutter 표시 범위만 선택한다. 새 API·interval·credential·broker request·cache·live/current claim은 만들지 않는다.

- [x] G4O1: `30일/90일/365일/전체` 선택은 기기 시간이 아니라 마지막 수신 봉을 기준으로 계산하고 cutoff와 같은 timestamp를 포함한다.
  CHECK: `cd apps/client && asdf exec flutter test test/vertical_slice_test.dart --plain-name 'chart range selection updates chart and exact table together'`
  EXPECT: 선택한 30일 범위의 chart semantics와 정확한 표가 같은 3개 봉을 사용하고 cutoff 이전 봉은 표에 나타나지 않는다.

- [x] G4O2: 유한 범위를 선택한 뒤 valid empty series로 새로고침해도 `bars.last`를 읽지 않고 기존 empty state를 표시한다.
  CHECK: `cd apps/client && asdf exec flutter test test/vertical_slice_test.dart --plain-name 'finite chart range preserves a valid empty refresh state'`
  EXPECT: `표시할 봉이 없습니다`가 보이고 widget exception이 없다.

- [x] G4O3: 선택 상태는 48dp Material control, visible text, button/selected semantics로 제공되고 375px·200% text에서 overflow와 unlabeled target이 없다.
  CHECK: `cd apps/client && asdf exec flutter analyze && asdf exec flutter test`
  EXPECT: analyzer clean; 44 parser/widget tests pass, including labeled tap-target guidance.

- [x] G4O4: sample/stale/source/as-of/fetched-at/issue/price-adjustment disclosure를 유지하고 선택 변경은 이미 받은 bar만 필터링한다.
  CHECK: `make check && make smoke`
  EXPECT: root contract/check/smoke remains green and no new runtime dependency or network path exists.

This gate does not prove real Kiwoom candles, interval selection, candle completeness, current prices, physical VoiceOver/TalkBack or device frame budget, average cost, fill markers, reconciliation, order capability, or live readiness.
