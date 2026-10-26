# G1.29 앱에서 가격·환율·지수 파일 검토와 명시적 저장

상태: 구현·필수 원격 검증·main 병합 완료 ([PR #57](https://github.com/YouSangSon/Omni-Folio/pull/57)). 전체 Goal 완료나 배포 완료를 뜻하지 않는다.

## 계약과 판단

- 연결 → 가격·환율·지수 파일에서 기존 세 JSON 계약을 선택한다. UTF-8와 실제 스트림 1MiB 한도를 CSV와 공유한다. KRW·USD 지수를 모두 지원하며 제공사 조회·credentials·새 의존성·migration은 추가하지 않는다.
- `POST /v1/market-data/imports/preview`는 기존 strict decoder만 사용한다. DB·clock을 읽거나 저장하지 않는다. `kind`, 기존 receipt의 metadata에서 `storage_check`/`recorded_at`를 제외한 `summary`, 정확한 입력 문자열 `input`을 반환한다. 입력 형식 확인은 현재성·사용권·DB admission 검증이 아니다.
- 클라이언트는 보낸 문자열과 echo를 정확히 비교하고 identity/version/sample/fetched/count/first-last를 입력에 연결한다. 이는 요청 대응 확인이며 canonical SHA의 독립 계산·검증이 아니다. 응답은 JSON escape 팽창을 포함해 `6MiB+16KiB`로 제한한다.
- `POST /v1/market-data/imports/apply`는 같은 원본을 기존 importer로 다시 검증·원자 저장하고 `{kind,receipt}`를 반환한다. preview token/cache/table은 없다. receipt의 모든 summary 필드와 `verified`, canonical 기록 시각≥수집 시각을 확인한 뒤 저장 확인을 표시한다. 과거 exact retry는 최신 버전을 재활성화하지 않는다.
- 400 `owner_data_invalid`는 입력/admission, 409 `owner_data_conflict`는 불변 버전/identity/검증된 legacy 출처 충돌이다. DB·journal proof·서버 clock 역행/무효·commit·미분류 실패는 고정 공개 메시지의 500 `owner_data_unavailable`로 남겨 private DB 값을 노출하지 않는다. 새 append는 전체 proof 이후 같은 tx에서 마지막 전역 기록 시각과 서버 clock을 먼저 확인하고 건강한 clock에서만 미래 수집을 400으로 분류한다. exact retry는 clock보다 앞선다. CLI의 고정 공개 오류는 유지한다.
- 두 새 경로는 Origin 부재를 허용하되, 존재하는 빈/다중/미설정/불일치 Origin은 본문·DB 접근 전 403으로 거절한다. 허용한 Origin의 OPTIONS는 기존 CORS를 쓴다. 기존 경로 동작은 바꾸지 않는다. 이는 공개 다중 사용자 인증이 아닌 기존 로컬 단일 소유자 경계다.

## 앱 수명과 저장 불확실성

- 파일 선택, 입력 검토, 저장은 각각 명시적이다. picker 취소/실패는 이전 초안·검토·결과를 보존한다. generation과 캡처한 원본으로 늦은 picker/preview 응답이 다른 파일에 붙지 못한다.
- root controller가 대기 원본·검토·확인서를 소유해 화면·탭 이동에서 유지한다. 첫 확정 거절은 초안을 해제할 수 있으나, 한 번이라도 통신/timeout/5xx/잘못된 2xx가 발생했다면 이후 4xx로 이전 commit 부재를 추론하지 않는다. 일치하는 확인서만 대기를 해제한다. 앱/프로세스 재시작 보존은 지원하지 않는다.
- 클라이언트 40초 제한은 기다림 제한이며 취소/rollback 보장이 아니다. 실제 서버 read15s/write30s 경계와 구분한다.
- 저장 확인 후 이미 진행 중인 snapshot/current 조회가 끝나면 명시적으로 한 번 병합된 새로고침을 시작한다. 기존 busy 호출의 의미를 바꾸지 않는다. 읽기 갱신 실패는 저장 확인을 취소하지 않으며 기존 정상값/오류 표시를 쓴다.
- 새 버전 전체 대체, 빈 가격/환율 철회, 지수 최소1점, owner/sample 독립, 출처의 영구보관·종료 후 사용권과 append-only/backup의 관계를 화면에서 설명한다. 제공사 인증이나 실제 시장 파일 확보를 주장하지 않는다.

## 검증

실제 원격 RED: `46fd9b90666867d404cf48b73fcc7d884d38da8a`, Core run `40292011521`, job `121469041449`에서 새 preview가 HTTP404로 실패했다. full/source identity는 skipped다. 구현 후 기존 무료 표준 Ubuntu Core/Client workflow에서 집중 검사→필수 전체 검사·분석·web/ARM64·권한/소스 일치를 통과했다. 실패/cancel은 통과로 재사용하지 않는다.

로컬은 문서/JSON/Go·Dart 형식/Git만 확인한다. 새 iOS·실기기·수동 screen reader/OS 파일 대화상자·실제 provider/파일 사용권·VM/배포는 별도다. 실제 시장 파일/사용권 확보를 완료했다고 주장하지 않는다. KOSPI 200 TR·S&P 500 TR 도입 조건은 [기존 source owner](g1u-benchmark-series.md#benchmark-source-policy)에 남긴다.


## 최종 검증과 병합 근거

검증 head `283dc37fbdcb7c70be70d7f3fb5ad4477a948ec4`를 main `55f39e8814ee86740e2987a1732a1965ef853544`에 병합했다. 전체 tree `53d212172df55be1efc57ff1dbff368918e5663d`가 동일하고 ancestry를 확인했다. 문서 후속 변경은 이 runtime 근거와 소스/계약/workflow 동일성을 대조해 재사용하며 전체 suite를 중복 실행하지 않는다.

| 검증 | 최종 근거 | 결과 |
| --- | --- | --- |
| Core 집중/전체 | [run40296837285](https://github.com/YouSangSon/Omni-Folio/actions/runs/40296837285), job121484838821 | 새 HTTP 집중1.015s, root 전체450.130s와 내부5 package, vet/형식, Python39, JSON37, 소유 자원 정리·source identity 통과 |
| Client | [run40296837322](https://github.com/YouSangSon/Omni-Folio/actions/runs/40296837322), job121484841313 | 집중15/전체418, format34파일0변경, analyze, web 및 ARM64 release, 권한 반례8·source identity 통과 |
| APK | client tree `592cc946c155b3466955740813c024087ee622cd` | 23546634 bytes, SHA256 `ca19ac80f72ed2e84fab72e7f8e11ee0947923959a90ffe72448e0838af7f20c` |
| Plugin/권한 | registrant SHA256 `5d31e4d80984b4f5254d2e6629d2cc2b16760ab4774a94faa863c48897f18326` | Android file selector 등록, IntegrationTestPlugin 제외, INTERNET 및 signature receiver 권한만 허용 |
| 로컬 정적 확인 | JSON37/로컬 OpenAPI ref/변경 Markdown 경로/4개 README | 실행 shell block27개가 KO/JA/EN/ZH에서 동일하고 기존 명령을 보존, gofmt·고정 Dart formatter·diff 통과. 로컬 runtime 검사 없음 |

독립 설계/구현 리뷰에서 무관한 reconciliation/order 조회가 저장 후 갱신을 막는 문제와 integration profile fake의 새 API 누락을 수정했다. `busy=false` 직후와 current 조회 완료 시점에서 pending refresh를 drain하며, 무관한 조회를 미완료로 둔 회귀 검사로 확인한다. 입력 검토의 raw echo는 독립 SHA 검증으로 과장하지 않는다.

중간 실패도 보존한다. `762f166` Client 집중15는 통과했으나 중괄호 lint6건으로 analyze가 실패했다. `c30b5b5`는 집중/분석 통과 뒤 기존 연결 화면 전체412통과/6실패였다. 새 카드로 바뀐 viewport와 안내 문구를 반영하고 기존 상태/오류/semantics 단언은 유지했다. `ab71111`은 테스트 보완에 SDK에 없는 `WidgetTester.ensureVisible(alignment:)`를 사용해 compile 단계에서 실패했고 검사는0개였다. `a5f1c7e`의 obsolete run과 이전 Core 전체 run들은 취소했으며 성공으로 세지 않는다. 고정 SDK의 실제 wrapper signature와 scroll 내부 코드를 확인해 중복 호출을 제거하고 scroll→frame→tap/semantics 순서로 보완한 뒤 독립 재리뷰에서 잔여 차단 없음, 최종 head 전체 CI에서 통과했다.

이 검사는 실제 HTTP producer와 공유 합성 wire를 strict Flutter consumer에 연결한 자동 근거다. 실제 제공사·시장 자료 정확성, 앱 재시작 복구, 새 iOS/실기기/수동 screen reader/OS 파일 대화상자와 VM/배포는 증명하지 않는다. 전체 로그와 중간 실패·취소 분류는 ignored `docs/_internal/skill-outputs/goal-20261026-owner-data-import-*.log` 및 unit receipt에 보존한다.
