# G1.16 Ledger Activity Pagination

Scope: G1.9의 replay-verified, revision-bound keyset cursor를 Flutter가 50건 단위로 전체 탐색하되 서버 snapshot, 순서, 비공개 cursor와 오류 보존 계약을 약화하지 않는다. 원장·API schema·broker 조회·주문 권한과 새 의존성은 변경하지 않는다.

## 클라이언트 계약

- [x] 첫 요청은 cursor와 client 임의 limit 없이 서버 기본 50건을 사용하고, 이후 요청은 불투명 cursor를 URI query로 인코딩한다. cursor를 page number나 복호화 가능한 정보로 해석하지 않는다. [AIP-158](https://google.aip.dev/158)
- [x] 한 페이지는 최대 50건이며 continuation cursor가 있으면 정확히 50건이다. 최초 페이지의 `ledger_revision`·`recorded_at`을 snapshot authority로 유지하고 이후 페이지가 다르면 전체 응답을 거절한다.
- [x] 순서와 양방향 경계는 기존 UTC nanosecond parser로 검증한다. 같은 시각의 서로 다른 이벤트와 동일한 public payload를 중복으로 오판하지 않으며, 요청 cursor와 이전 trail을 반복하는 loop는 거절한다.
- [x] 이전 페이지로 돌아갈 때는 첫 snapshot과 cursor trail을 재사용한다. 새 import·수동 refresh는 완전한 새 첫 페이지를 원자적으로 교체하며, epoch이 지난 성공·실패 응답은 현재 화면을 덮지 않는다.
- [x] 페이지 요청 실패 시 알려진 정상 페이지와 스크롤 위치를 유지하고 server error body와 내부 ID를 노출하지 않는다. 로딩·실패·페이지 상태는 focus를 빼지 않는 live region으로 알리고 탐색 버튼은 최소 48dp를 보존한다. [WCAG 2.2 status messages](https://www.w3.org/WAI/WCAG22/Understanding/status-messages.html)

## 검증 근거

- 행동 RED는 API/model은 통과하지만 페이지 탐색 UI 검증이 실패했다. 최종 `activity_pagination_test.dart` 13개는 URI encoding, 50건 계약, nanosecond 순서·양방향 경계, 101건 3페이지 왕복, cursor loop·동일 payload, 오류 보존·redaction, refresh/import stale race, 중복 클릭·dispose, 320px·200% text·dark·reduced-motion·48dp semantics를 통과했다.
- 최종 `make check` exit 0: Dart format 13개, Flutter analyze, Go 전체, Flutter 98개, Python 34개, cleanup self-test, JSON 계약 17개. 전체 로그는 ignored `docs/_internal/skill-outputs/activity-preflight-check-final.log`에 둔다.
- 실제 Flutter release web·Chrome 152·임시 Go/SQLite API에서 101건을 import한 후 1→2→3→2→1 왕복이 같은 revision을 유지했다. 최초 왕복은 unbound GET 1회와 cursor GET 3회로 첫 페이지를 다시 읽지 않음을 확인했다. 102번째 import 후 명시 refresh는 새 revision으로 교체했고, 주입한 cursor 400은 정상 페이지와 고정 오류 문구를 유지했으며 키보드 refresh로 복구했다.
- 375×812 light, 768×1024 dark/reduced-motion, 1440×900 dark에서 overflow·clipping 없이 페이지를 표시했고 desktop에서 키보드 Enter로 이전 페이지를 열었다. 요청 24개의 loopback 외 네트워크는 0개였다. 종료 후 전용 listener, browser/core process, temp SQLite/root, client build·coverage·core binary의 부재를 확인했다.
- 독립 리뷰에서 cursor loop, pending explicit refresh, scroll anchor, stable back/forward, lower nanosecond bound의 추가 P1/P2 결함을 발견하지 못했다. 리뷰는 추가 테스트를 실행하지 않았으며 위 실행 근거와 함께 판정한다.

## 범위 경계

이 gate는 최신 local ledger snapshot의 페이지 탐색을 증명할 뿐 필터·검색·export, broker activity·current status, 앱 재시작 후 offline cursor resume, multi-replica cursor 안정성, 물리 iOS·Android, 수동 screen-reader, 배포, live-money readiness를 증명하지 않는다. 반응형 화면을 다시 mount하면 새 첫 페이지를 요청하며, 보존 주장은 같은 mounted state 내에 한정한다.
