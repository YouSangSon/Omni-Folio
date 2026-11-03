# G4Z Kiwoom Mock Snapshot Sync

## Contract

`kiwoom-mock-sync -db <existing migrated DB> -ledger-account account-main`은 기존 `KiwoomClient.Snapshot(KRX)`의 첫 CLI 소비자다. 모의 계좌의 보유수량·미체결을 기존 snapshot journal에 저장하고 현재 원장 수량과 대조한다. 기존 `GET /v1/broker-reconciliation/latest`와 Flutter 연결 화면이 결과를 그대로 읽는다. schema v27/backup v21, HTTP·Flutter 계약과 dependency는 바꾸지 않는다.

- 두 인자를 모두 요구한다. production·mode·base URL·credential 인자와 환경변수 fallback은 없다. DB를 생성하거나 migration하지 않는다.
- 비밀이나 네트워크 접근 전에 기존 DB의 schema history, 원장 재생 정책·이벤트, broker recovery와 단일 mock/KRX 계좌 이력을 검증한다.
- embedded migration 003과 broker 테이블·인덱스·불변 trigger 및 원장 update/delete guard의 정의를 비교한다. broker inventory의 누락·추가와 관련 TEMP 객체를 거절하며, 저장 직전 같은 검사도 반복한다. 정상 row만 남은 DB라도 삭제 guard가 사라지거나 무효화됐다면 바인딩을 다시 만들지 않는다.
- macOS에서만 `/usr/bin/security find-generic-password -s com.omni-folio.kiwoom.mock.readonly.v1 -a credentials -w`로 기존 일반 암호 항목 하나를 읽는다. 비밀을 argv에 넣지 않고 stderr를 버린다. stdout은 8 KiB, subprocess는 10초로 제한하고 취소·초과·실패에도 Wait를 마친다. 항목 생성·변경이나 Keychain 설정 변경은 하지 않는다.
- JSON은 `schema_version`, `app_key`, `secret_key`, `alias_key_base64`, `account_number` 다섯 필드만 받는다. 중복·알 수 없는 필드·뒤따르는 JSON을 거절한다. 버전은 `kiwoom-mock-read.v1`, 각 키는 1,024바이트 이하이며 공백뿐인 값과 제어문자를 거절한다. alias key는 정확히 32바이트의 canonical 표준 Base64, 계좌는 숫자 10자리다.
- 설정한 계좌의 alias가 기존 바인딩과 다르면 인증 요청 전 중단한다. 고정된 `https://mockapi.kiwoom.com`의 OAuth·계좌 읽기 세 API만 허용한다. OAuth·401 refresh·모든 페이지를 포함해 같은 프로세스의 요청은 최소 1초 간격이다. 429/5xx 자동 재시도는 없고 기존 401 1회 갱신만 유지한다. 전체 명령은 2분, 각 제공자 호출은 기존 10초 상한을 따른다.
- 완전한 조회 뒤 응답 `AccountRef`를 설정 계좌의 alias와 비교한다. 저장하는 동일 `BEGIN IMMEDIATE` transaction 안에서 schema·원장·broker recovery와 단일 `(environment,account_ref)` 바인딩을 다시 검사한다. 기존 KRX 이력의 production·여러 계좌와 조회 중 추가된 orphan·손상 기록을 거절한다. 서로 다른 계좌의 동시 첫 sync는 하나만 저장할 수 있다. 첫 성공이 기존 journal에 바인딩을 남기며 새 테이블은 없다.
- 조회 중 정상 원장 import는 허용한다. 저장은 그 시점의 원장 revision과 대조한다. 같은 snapshot·revision 재시도는 기존 결과를 반환한다. 일반 `recordKiwoomSnapshot` 호출자는 기존 의미를 유지하며 transaction 소유 append helper만 공유한다.
- 제공자 실패·부분 조회·잘못된 계좌는 새 snapshot을 저장하지 않는다. stdout에는 provider·mode·조회/기록 시각·원장 revision·포지션/미체결 건수·수량 일치 여부만 출력한다. 원본 계좌·order ID·이름·현금·키·원본 오류·DB 경로는 출력하지 않는다.

## Recovery and limits

stdout 쓰기는 DB commit 뒤에 수행한다. writer 오류는 원래 오류를 반환하므로 명령 실패가 곧 미저장을 뜻하지 않는다. 기존 GET과 앱 연결 화면에서 저장된 상태를 먼저 확인하고 새 조회로 재시도한다. 실제 제공자의 새 조회 시각이 다르면 기존 append-only 계약에 따라 새 snapshot이 생길 수 있다.

`complete=true`는 필요한 조회와 페이지를 모두 읽었다는 뜻이다. 여러 호출이 제공자 전체의 원자적 동일 시점 snapshot을 구성한다는 보장은 없다. freshness는 계속 `unverified`이며 원장의 `last_verified_at`을 갱신하지 않는다. 현금·평가액·체결을 원장에 반영하거나 정정하지 않으며, 계좌 초기화·권한 활성화·주문 제출·스케줄러·실거래 승격은 없다.

호출 간격은 프로세스 내부에서만 공유한다. 여러 프로세스의 제공자 전체 할당량은 보장하지 않으므로 동시 실행을 피한다. Keychain 접근 권한, 실제 모의 앱 키·계좌의 응답과 quota는 사용자 환경에서 별도로 확인해야 한다. 이번 검증에서는 비밀 생성·변경이나 실제 Keychain·키움 접근을 수행하지 않았다.

## Local evidence

- `TestKiwoomMockSyncDispatchRequiresExplicitArguments`를 기존 `runContext`에서 먼저 실행해 `unknown command "kiwoom-mock-sync"` RED를 확인했다. Make 인용 오류는 별도 도구 실패로 구분했다.
- `TestKiwoomMockSync*`는 합성 secret reader와 실제 로컬 TLS HTTP 서버를 사용한다. 고정 mock host를 검사한 뒤 테스트 서버로만 전달하며 외부 네트워크와 실제 Keychain 호출은 없다. OAuth·계좌·평가/미체결 페이지·정규화·SQLite·기존 GET까지 연결한다.
- 입력 경계, subprocess stdout 초과·오류·취소·join, 401/429/5xx/timeout/부분 응답의 이전 기록 보존, 계좌 불일치 0 write와 설정 불일치 0 HTTP, DB preflight 0 secret/network, 조회 중 이력 변경·동시 첫 바인딩·정상 원장 import, 멱등 재시도와 commit 뒤 writer 오류를 검증한다. 기존 `TestG4H*`는 일반 저장 래퍼의 동작 보존을 검사한다.
- 2026-11-03에 독립 Astra 재리뷰를 통과했다. 정상 row의 guard 누락으로 바인딩을 지울 수 있던 지적은 physical schema 검사와 pre-secret·조회 중 변경 회귀 검사로 보완했다.
- 검증 환경은 Darwin/arm64, Go 1.24.5, Python 3.14.5다. Go·gofmt·Python은 asdf 설치본의 절대 경로를 사용했고 `GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS=-p=1`과 해당 도구 우선 PATH를 고정했다. 다음 Make 검사는 모두 종료 코드 0이다.

```sh
make test FLUTTER=true CORE_TEST_PACKAGES="-run='^(TestKiwoomMockSync|TestG4H)' -count=1 -timeout=10m ./..."
make test FLUTTER=true CORE_TEST_PACKAGES='-race -run=^TestKiwoomMockSync -count=1 -timeout=10m .'
make check DART=true FLUTTER=true CORE_TEST_PACKAGES='-timeout=30m ./...'
make smoke
```

- 최종 집중 검사는 새 테스트 함수 18개와 기존 G4H 7개를 포함한다. race는 새 테스트만 실행했고, 전체 Core package는 762.156초에 통과했다. 전체 검사에는 내부 Go 6개 package, 연구 테스트 48개, JSON 계약 44개, Go format/vet와 resource cleanup이 포함된다. Client 검사·빌드는 다시 실행하지 않았다.
- native smoke는 시작 전 `127.0.0.1:18080` bind 확인 후 기존 health/status/import/snapshot/activity/market 경로를 통과했다. 다른 포트 소유 프로세스는 종료하지 않았다. README 네 언어의 새 shell 블록 일치와 변경 Markdown 7개의 상대 링크 311개 대상·fence 균형도 확인했다.
- 실제 unknown-command RED와 도구 인용 실패, timeout fixture 정리 실패의 stack, 미해결 종목 import fixture 수정, 최종 집중·race·전체·smoke 로그는 `docs/_internal/skill-outputs/goal-20261103-kiwoom-mock-sync`에 구분해 보존한다. 최종 검사의 Go 파일 6개 SHA-256은 문서 결과 갱신 뒤에도 같다. 로컬 결과를 CI·merge·배포·실제 제공자 관찰로 간주하지 않는다.
