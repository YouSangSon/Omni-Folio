# G1.18 Current valuation release browser QA

Scope: G1.17 release Flutter와 임시 Go/SQLite API를 연결해 실제 empty, KRW cash-only complete, direct FX missing unavailable 전이를 관찰한다. 샘플 가격 합산 산술은 G1.17 자동 검증 근거를 재사용한다.

- [x] B1: 실제 빈 원장을 표시하고 KRW 입금 가져오기 뒤 서버 합계와 표시 금액이 일치한다.
  EVIDENCE: 2026-09-08 release Chrome에서 첫 가져오기 CTA 확인. 실제 preview/apply API로 KRW 1234567.89 입금 후 rev_0000000001의 cash/total 1234567.89, holdings 0을 API와 화면에서 대조했다.
- [x] B2: 재조회 실패는 원래 평가를 보존하며 정상 unavailable은 이전 합계를 제거한다.
  EVIDENCE: valuation endpoint만 503을 주입해 기존 합계와 16:00 평가 시각 및 오류 문구를 유지했다. USD 입금으로 rev_0000000002의 missing_direct_fx를 만든 뒤 주입을 제거하고 키보드 재조회하자 16:02 unavailable로 교체하며 세 aggregate를 숨겼다. 원통화 현금 기록은 유지했다.
- [x] B3: 375·768·1440 viewport에서 화면을 검사하고 키보드 재조회를 확인한다.
  EVIDENCE: Flutter 3.47.1-stable release/Chrome 152에서 375×812 light, 768×1024 및 1440×900 dark/reduced-motion 캡처를 검사했다. 금액·본문·시각 잘림은 관찰되지 않았다. desktop Home → Shift+Tab → Enter가 실제 재조회를 수행해 unavailable로 바뀌었다. 최종 수정 빌드에서도 unavailable/초기 503 상태의 제목·본문·시각이 semantic DOM에 각각 한 번만 노출됨을 확인했다.
- [x] B4: 전용 브라우저·API·정적 서버와 임시 원장이 종료 후 회수된다.
  EVIDENCE: 두 browser Make wrapper 모두 exit 0. 마지막 core/web/browser PID 31227/31228/28306과 18082/18083 listener, core.test/flutter_tester/전용 browser session process의 부재를 확인했다. N7zuEA/dDKmnt 임시 root, client build/coverage, core binary도 종료 후 없으며 cleanup self-test가 통과했다. screenshot·지정 로그는 의도한 검증 산출물로 보존했다.

기준 스크린샷 비교, 물리 screen reader, physical-device profile, Core Web Vitals와 live provider freshness는 이 관찰만으로 완료되지 않는다.

## 발견한 결함과 회귀

실제 unavailable semantic DOM에서 제목·본문·시각이 두 번씩 나타났다. `Semantics.label`은 기본적으로 자식 Text의 semantics를 대체하지 않는다. 로딩/오류 및 empty/unavailable 카드에 완전한 통합 label을 유지한 채 `excludeSemantics: true`를 적용했다. 해당 카드에는 자식 조작이 없으며 live-region 설정은 보존했다. [Flutter API 근거](https://api.flutter.dev/flutter/widgets/Semantics/excludeSemantics.html)

`failed valuation announces each sentence once`와 `unavailable valuation announces each sentence once`는 수정 전 제목 발생 횟수 expected 1, actual 2로 실패했다. 최종 자동 검사와 수정 후 release 재검증은 아래에 기록한다.

처음 `make check`에서 Go 전체는 281.748초로 통과했다. Flutter 새 테스트의 중복 검사는 통과했으나 `addTearDown`이 semantics 종료 검사보다 늦게 실행돼 handle 미해제 두 건으로 실패했다. 두 테스트의 본문 `try/finally`로 해제를 옮긴 뒤 `make check CORE_TEST_PACKAGES=./internal/exact` exit 0: format·Go vet·Flutter analyze·Flutter 108개·Python 34개·JSON 17개·cleanup self-test가 통과했다. Go production/test 소스는 변경되지 않아 앞선 전체 결과를 재사용한다. 로그는 `valuation-semantics-check.log`와 `valuation-semantics-recovery-check.log`다. 독립 read-only 리뷰도 정보·동작 유실과 cleanup 범위의 추가 P0–P2 결함을 찾지 못했다.

## 재현 조건

기존 Make 소유권 wrapper 안에서 임시 Go 바이너리·SQLite와 `flutter build web --release --no-web-resources-cdn` 산출물을 실행했다. 전용 API/web origin은 127.0.0.1:18082/18083이며 실제 사용자 DB와 credential은 사용하지 않는다. 입금 fixture는 `source_event_id,account_id,type,occurred_at,symbol,quantity,price,fee,currency,amount` 헤더와 `qa-KRW,account-main,DEPOSIT,2026-01-01T00:00:00Z,,,,,KRW,1234567.89` 한 행이다. USD 단계는 ID와 통화만 USD로 교체한다. 두 단계 모두 preview 가능·신규 1행과 apply 1행을 확인했다.

키보드 재조회는 desktop Home navigation을 활성화한 뒤 Shift+Tab → Enter로 실행했다. DOM `focus()`만 주는 방식은 Flutter 내부 포커스를 증명하지 못해 근거에서 제외했다. 브라우저 console의 broker-reconciliation/latest 404는 빈 대조 기록에 대한 기존 응답이며 valuation 503은 의도한 오류 주입이다.

로컬 screenshot은 ignored `output/playwright/valuation-*.png`, wrapper 로그는 ignored `docs/_internal/skill-outputs/valuation-browser.log`에 있다. 화면 캡처와 semantic DOM 검사는 물리 screen reader의 발화 검증을 대신하지 않는다.

최종 캡처는 `valuation-unavailable-{375,768,1440}-final.png`, 재빌드 wrapper 로그는 `valuation-browser-final.log`다. CLI의 일부 후속 호출에서 tab이 about:blank로 바뀌어 ref가 무효화됐으므로 해당 호출은 제품 검증 근거에서 제외했다. 유효한 URL·snapshot·실제 API 응답을 확인한 관찰만 위 근거에 포함한다. 외부 요청 수·자동 axe 검사·시각 baseline 차이는 측정하지 않았으므로 통과로 주장하지 않는다.
