# G4Y 관심종목과 저장 가격

상태: 독립 설계·구현 리뷰, 원격 Core·Client 검사와 main 병합을 완료했다. 전체 Goal 완료나 실제 시세 연결을 뜻하지 않는다.

## 사용 흐름과 권한

상단 관심종목 버튼은 포트폴리오가 비어 있거나 조회에 실패해도 열린다. XKRX·KRW와 XNAS/XNYS·USD의 시장·심볼·통화 조합을 최대50개까지 저장·삭제하며 목록은 앱 재실행 후에도 서버에 남는다. 입력은 상장 사실 확인이 아니다. 관심목록을 바꿔도 원장·보유자산·상장 등록 권한·가격 입력 권한은 바뀌지 않는다. 원장에 등장하지 않은 새 종목은 기존 owner 가격 파일을 가져올 수 없으며, 저장 자료가 없으면 가격 없음으로 표시한다.

가격·평가·시세 상태를 하나로 취급하지 않는다. GET은 같은 transaction에서 시각을 한 번 잡고 최신 owner 전체 버전과 fixture 관측을 읽은 뒤 정확한 tuple을 선택한다. 철회나 시장 변경 시 이전 버전 가격을 되살리지 않는다. 관측·수집·기록 시각이 모두 cutoff 이하여야 하며, 다른 instrument identity나 같은 최신 시각의 가격/표시 출처 충돌은 해당 행을 ambiguous로 만든다. 다른 행은 유지하되 DB·hash·schema 무결성 오류는 전체 응답을 실패시킨다.

fixture는 언제나 sample/stale이다. owner 가격은 관측 후 정확히24시간까지 unknown, 이후 stale이며 unknown은 현재 시세 인증이 아니다. 원통화 원문 가격만 표시하고 합산·FX·수익률·차트 연결은 하지 않는다. private/account/observation ID는 반환하지 않는다.

## 저장과 복구

schema26의 mutable singleton은 원장 이력이 아닌 개인 설정이다. 초기 행은 migration에서 revision0·빈 목록·null 시각과 canonical hash로 만든다. 행 누락을 초기화로 해석하지 않는다. 조회·저장은 정확한 schema와 추가 trigger/temp object, 1행, canonical JSON/hash를 확인한다. 변경은 revision을 한 번 올리고 no-op은 그대로 유지한다. 초기 seed 이후 INSERT/REPLACE와 DELETE는 금지하며 빈 목록도 기존 singleton에 저장한다.

POST `/v1/watchlist`는16KiB의 닫힌 JSON과 명시적 `expected_revision`을 받는다. 같은 revision에서만 목록을 원자 교체하고 저장값을 읽어 확인한다. 현재 revision이 expected+1이고 원하는 목록과 같으면 재시도 ACK를 반환한다. 더 오래된 요청은 원하는 목록이 우연히 같아도409다. 브라우저 Origin은 기존 설정과 정확히 일치하거나 native/local 요청처럼 없어야 하며, CORS를 인증으로 간주하지 않는다. 오류는 고정된 공개 문구다.

클라이언트는 GET/POST를 직렬 실행하고 불확실 요청의 원래 revision·목록을 앱 세션에서 보존한다. 추가·삭제 ACK는 실제 변경이므로 revision+1이어야 한다. 재조회 revision이 더 크거나, 같으면서 원하는 목록이면 대기 요청을 해제할 수 있다. 낮은 revision은 복원/DB 교체 가능성이 있어 성공 확인으로 처리하지 않는다. 이 revision은 백업 복원을 가로지르는 epoch가 아니다. POST ACK 뒤 가격 조회가 실패해도 저장 목록은 유지하며 다른 revision의 가격을 표시하지 않는다. 앱 종료 후 대기 요청 보존은 제공하지 않는다.

backup20은 source/candidate/restore의 관심목록 hash·revision·개수를 증명한다. v19/schema25의 owner-price·FX·nonarchival FIFO v2·runner lease 검증은 유지한다. v19 이하가 새 관심목록 필드나 table을 주장하면 거절하며, 과거 원본은 변경하지 않고 소유 임시 복사본에서 migration한다. 활성 runner lease는 여전히 복원 활성화를 막는다.

## 검증 근거

- RED: `cf3e4a6061e33e90e8a5d6180b393ddd924b3542`, [Core run40632161851](https://github.com/YouSangSon/Omni-Folio/actions/runs/40632161851), job122630802609. 실제 GET `/v1/watchlist`가404를 반환했다. 전체 검사와 source identity는 skipped이며 통과로 세지 않는다.
- 검증 대상: 서버 HTTP·동시 CAS·동일 요청 재시도·ABA·cap·Origin·clock·철회·가격 충돌·private 값 비노출·현재/과거 백업, shared `golden-watchlist.json` producer/consumer, bounded HTTP와 Flutter 요청 수명·폼·이동·접근성.
- 로컬에서는 정적 JSON/참조·diff·고정 formatter만 사용한다. runtime·테스트·analyze·빌드는 실행하지 않는다. 기존 무료 표준 Ubuntu CI의 최종 소스 검사 결과를 아래에 기록한다.

독립 설계 리뷰에서 누락 singleton을 빈 초기 상태로 해석하는 문제와 schema25의 runner 검증 허용 누락을 보완했다. 구현 리뷰에서는 temp view가 같은 이름의 table을 가리는 경로, REPLACE의 revision 초기화, revision이 증가하지 않은 변경 ACK, Dart 테스트 closure/cascade 결합을 수정했다. 수정 후 독립 재검토에서 남은 차단 지적은 없었다. SQLite의 [temp 우선 이름 해석](https://www.sqlite.org/lang_naming.html)과 [REPLACE의 DELETE trigger 조건](https://www.sqlite.org/lang_conflict.html)을 공식 문서로 확인했다.

## 최종 검증과 병합 근거

최종 head `da49b77ebd7942debc93ec9d0514bf57f7e80a2d`를 [PR #61](https://github.com/YouSangSon/Omni-Folio/pull/61)로 main `034d546ea1ea379a8640a1b09d924bdbd2de89d4`에 병합했다. ancestry와 전체 tree `7c3d00922698ae92df856897d523b1c4e83972fe` 일치를 확인했다. 이후 문서 변경은 검증된 코드·계약·workflow가 같은지 대조해 근거를 재사용한다.

| 검증 | 근거 | 결과 |
| --- | --- | --- |
| Core | head `1296bbf0d92f981e72b229e8ad86efd9ffaf2d60`, [run40635432865](https://github.com/YouSangSon/Omni-Folio/actions/runs/40635432865), job122641207638 | 관심목록 집중2.210s·root 전체446.025s·내부5 package·vet/형식·Python39·JSON 구문39·자원 정리·source identity 통과 |
| Client | 최종 head, [run40636055417](https://github.com/YouSangSon/Omni-Folio/actions/runs/40636055417), job122643168088 | 집중22/전체457·format42파일0변경·analyze·web/Android ARM64 release·권한 반례8·source identity 통과 |
| APK | client tree `ff3d36ccec088bc3458ea1ef93e7401ffde1ec2e` | 23677898 bytes, SHA256 `3df29b09bd85894d0a2e4ee9d0d7940be7a5b98ba120e12988d94184a9817f8b` |
| Android 등록·권한 | registrant SHA256 `5d31e4d80984b4f5254d2e6629d2cc2b16760ab4774a94faa863c48897f18326` | file selector 등록·IntegrationTestPlugin 제외·INTERNET와 signature receiver 권한 확인 |
| 로컬 정적 확인 | JSON 구문39·OpenAPI 참조322·기존 path/schema 불변 | README 네 언어 shell29개 일치·변경 Markdown 경로·gofmt·고정 Dart formatter·diff 확인. runtime 실행 없음 |

Core head 이후 변경은 Flutter 두 파일의 중괄호와 기존 테스트의 refresh 버튼 선택뿐이다. 최종 head와 Core head의 `services/core`, `services/research`, `contracts`, `tests`, `tools`, `Makefile`, `.tool-versions`, Core workflow의 Git 객체가 모두 같음을 확인했다. 대기 중인 중복 Core run40635745630/40636055324는 취소했으며 통과로 세지 않는다.

중간 실패는 최종 결과와 구분한다.

- `34ecb14`의 Core run40634847552/job122639330607은 모호성 fixture가 같은 종목·시각의 DB 유일성 제한을 위반해 실패했다. 서로 다른 instrument ID를 사용해 실제 허용된 모호성을 검사하도록 수정했다. Client run40634847538/job122639330708은 집중21통과/1실패였다. 스크롤 뒤 frame 갱신 전 화면 밖 버튼을 눌렀으므로 frame을 진행하고 hit-test 가능성을 단언하도록 고쳤다.
- `1296bbf`의 Client run40635432858/job122641207557은 집중22개를 통과했으나 중괄호 lint3건으로 분석이 실패했다. 형식만 수정했고 전체 검사·빌드는 skipped였다.
- `5b27e4a`의 Client run40635745594/job122642176069은 집중22·분석을 통과했고 전체456통과/1실패였다. 기존 테스트가 IconButton 하나를 가정해 새 관심종목 버튼까지 선택했다. refresh 버튼만 선택하도록 바꾸고 기존 동작·접근성 단언을 유지했다. 최종 head에서 전체457개가 통과했다.

로그 전문과 소스 일치·패키지·중간 실패 근거는 ignored `docs/_internal/skill-outputs/goal-20261028-watchlist-*`에 보존했다. 자동 합성 검증은 실제 제공자 정확성·실기기·배포 증거를 대신하지 않는다. 확인 예: revision1 요청이 끝난 뒤 목록이 revision3까지 바뀌었다면, 원하는 내용이 우연히 같아도 오래된 revision1 재시도를 새 성공으로 인정하지 않는다.

## 남은 범위

실제 가격 제공자·상장/자료 정확성·자동 갱신·새 iOS 빌드·실기기/수동 screen reader·VM/배포는 미검증이다. 관심종목 등록이 실제 broker/market 권한을 만들지 않는 경계는 후속 연동에서도 유지한다.
