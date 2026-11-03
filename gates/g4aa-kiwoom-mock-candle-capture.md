# G4AA Kiwoom Mock Candle Capture

## Contract

`kiwoom-mock-candles -symbol 005930 -interval 1d -out <new private JSON>`은 기존 `KiwoomClient.Candles`의 bounded reader를 실제 CLI에서 사용하고 요청·원본 시각·조회 시각을 연구 파일로 보존한다. DB를 읽지 않는 종목 코드 단위 관찰이다. 현재 상장 정보로 과거 instrument 소유권을 만들지 않으며 G4T/G4U 가격 저장, HTTP·Flutter, schema v27/backup v21과 paper fixture 계약은 그대로 유지한다.

- `-symbol`, `-interval`, `-out`을 모두 요구한다. 종목은 숫자 6자리 KRX 코드이며 interval은 `1d`, `1m`, `3m`, `5m`, `10m`, `15m`, `30m`, `45m`, `60m`이다. base-date·host·environment·credential·DB·overwrite 옵션과 환경변수 fallback은 없다.
- 입력과 출력 경로를 검증한 뒤에만 기존 G4Z macOS Keychain reader와 정확히 다섯 필드인 decoder를 사용한다. 계좌·alias 필드도 기존 규칙으로 검증하지만 계좌 요청이나 출력에 사용하지 않는다. Keychain 항목과 권한을 만들거나 바꾸지 않는다.
- 새 transport는 고정 HTTPS mock host의 OAuth와 `ka10080`/`ka10081` chart만 허용한다. 기존 mock-sync의 OAuth·계좌 세 API allowlist는 확장하지 않는다. 두 명령은 같은 프로세스 pacer를 공유하며 OAuth·401 1회 갱신·모든 페이지를 포함해 최소 1초 간격이다. 여러 프로세스의 quota를 보장하지 않는다. 전체 명령은 2분, 호출별 제한은 기존 10초이며 429/5xx를 새로 재시도하지 않는다.

## Research artifact

[`kiwoom-candle-capture.v1` schema](../contracts/kiwoom-candle-capture.schema.json)는 닫힌 JSON 키를 정의한다. [합성 fixture](../contracts/fixtures/kiwoom-candle-capture.json)는 실제 test-binary `main()` 성공 경로와 비교한다. 파일은 마지막 newline을 포함해 최대 1 MiB, 최신 최대 500봉이며 기존 32페이지 상한을 유지한다. reader는 cap에 도달한 다음 페이지의 overlap까지 검사하고 중복·충돌·순서를 검증한 뒤 최신 범위를 오름차순으로 선택한다. 전체 이력을 수집했다는 뜻은 아니다.

`request.api_id`와 `request.body`는 실제 전송한 의미상 요청과 같다. 조회 시작 UTC clock에서 고정 KST 기준 `base_dt`를 한 번 계산하고 페이지·갱신 동안 그대로 사용한다. `upd_stkpc_tp=1`과 기준일은 요청 근거이며, 조정이 실제 적용되었거나 서로 다른 capture의 주가 단위가 같다는 보장은 아니다. 원본 `dt` 또는 `cntr_tm`은 해당 OHLCV와 함께 `provider_time`에 보존한다. 값은 기존 magnitude/decimal 검증과 정규화를 따른다.

`fetch_started_at`과 `fetched_at`은 실제 UTC RFC3339Nano clock이다. 종료가 시작보다 앞서면 새 capture만 거절한다. 발행 경계는 원본 시각이 일봉 8자리·분봉 14자리 ASCII 숫자인지도 검사한다. 기존 `Candles`의 반환형·KST 정규화 `At`·오류 계약과 분수 초 허용에는 이 검사를 추가하지 않는다. capture에는 정규화 `at`, 봉 시작·종료·공개·가용 시각을 만들지 않는다. `time_evidence`는 제공자 timezone·bar boundary·publication time이 `not_provided`이며 bar completion은 `unverified`임을 명시한다. 이 파일은 closed-bar 입력, canonical 가격 평가, paper 입력이나 주문 권한이 아니다.

## Publication and recovery

출력 부모는 이미 존재하고 현재 사용자 소유여야 하며 기존 ledger export의 상위 경로 신뢰 정책을 만족해야 한다. 기존 파일·symlink·다른 사용자에게 쓰기가 열린 경로는 secret/network 접근 전에 거절한다. 새 파일은 같은 폴더의 고유한 0600 temp에 기록하고 hash·Sync·Close 뒤 hard link로 생성한다. 같은 경로의 동시 실행에서는 Link 하나만 성공하며 기존 bytes를 읽어 새 성공으로 반환하거나 덮어쓰지 않는다.

credential·제공자·페이지·파싱·overlap·취소·JSON 크기·Link 이전 쓰기/Sync/Close 실패는 final 파일을 발행하지 않는다. cleanup은 자신이 만든 temp 하나만 대상으로 하며 원인과 cleanup 오류를 함께 보존한다. Link 성공 뒤 temp 제거·디렉터리 Sync·stdout 영수증 쓰기는 실패할 수 있다. 이때 완성 파일은 남고 명령은 실패한다. 파일을 rollback으로 삭제하지 않는다. 기존 경로를 먼저 확인하고 새 관찰은 다른 명시적 경로로 실행한다. SIGKILL 뒤 내구성을 보장하지 않는다.

stdout은 `schema_version=kiwoom-candle-capture-receipt.v1`, `rows`, 정확히 발행한 bytes의 `sha256`만 담는다. 별도 CaptureID와 self-hash는 없다. token·credential·원본 계좌·alias·cursor·원본 응답/오류는 파일·영수증·명령 오류에 내보내지 않는다. ledger CSV는 같은 파일 생성/발행 mechanics만 공유하고 행별 취소·CSV Flush/Error·32 MiB/short-write 검사·DB 실경로/alias 및 sidecar 차단을 유지한다.

## Local evidence

- 실제 baseline native binary와 dispatch 회귀 검사가 각각 `unknown command "kiwoom-mock-candles"` RED를 보였다. 초기 recursive Make override 누락으로 시작된 의도치 않은 검사는 종료한 도구 실패로 따로 보존했다.
- `TestKiwoomMockCandles*`는 synthetic secret과 실제 `httptest` TLS 서버로 요청·원본 시간·UTC 조회 시간·KST 자정·pagination·refresh·실패·권한·경로·경합·receipt를 검증한다. private runtime factory를 `_test.go` subprocess 안에서만 바꾸고 실제 `main()`의 TLS 성공과 SIGINT/SIGTERM 취소를 실행한다. 이 성공은 production binary의 실제 Keychain/키움 성공을 뜻하지 않는다.
- `TestPrivateFilePublicationFailureOutcomes`는 encoder/cancel/Link collision 이전 실패와 실제 디렉터리 read 권한 실패로 Link 이후 오류를 구분한다. 자기 temp만 정리하고 기존 파일과 완성 파일을 보존한다. 기존 `TestLedgerExport*`, `TestKiwoomCandles*`, `TestKiwoomMockSync*`도 집중 검사의 범위다.
- 독립 source 리뷰에서 Go 시간 파서의 dot/comma 분수 초 허용과 새 JSON의 정확한 wire 형식 차이를 발견했다. 실제 TLS/CLI RED 뒤 새 발행 경계만 보완했고, 두 형식의 기존 `Candles` 허용도 명시적으로 검증했다. 수정분 독립 재리뷰를 통과했다.
- 고정 Go 1.24.5/Python 3.14.5, `GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS=-p=1 PYTHONDONTWRITEBYTECODE=1`에서 집중 GREEN·관련 race와 최종 전체 검사가 모두 종료 코드 0이다. 최종 전체 검사는 Go format/vet·Core 399.927초·내부 6 package·Python compile/연구 테스트 48개·JSON 계약 47개와 기존 resource preflight/cleanup을 포함한다. 전체 Core 검사는 최종 runtime에서 한 번 수행했다.
- 최종 runtime/test 입력 SHA256은 `d40a43641bbc093335bf563dd626ad36b546e9e0b85fd3e46cc39df9e28cc40a`다. 소스 고정 뒤 문서 결과만 갱신했다. 전체 명령·종료 코드·hash·cleanup 기록은 `docs/_internal/skill-outputs/goal-20261103-market-input-grounding/implementation-*`에 보존한다.
- 고정된 세 functional CLI QA 시나리오를 recorder로 순차 실행했다. 현재 test executable의 실제 `main()`이 로컬 TLS를 통과해 만든 원본 JSON·영수증·stdout/stderr를 보존했고, 파일 773 bytes·0600·정확한 SHA 일치를 확인했다. 별도로 빌드한 production binary는 기존 경로에서 기대한 exit 1과 빈 stdout으로 조기 거절하며 같은 bytes·권한을 유지했다. 이 wrapper와 recorder 001/002는 exit 0, recorder 003은 기대한 exit 1이며 모두 terminal/complete다. 부모의 독립 대조와 recorder materialization은 `pass`, `open=[]`로 끝났으며 결과는 같은 근거 폴더의 `cli-qa/probes/qa-report.md`에 있다.
- Client는 PR74의 통과한 CI head `64c9326b508d9773a3f8ca746db07ddc364f8d68`와 관련 입력 164개(기존 fixture 38개 포함)의 Git blob 일치를 확인해 로컬 검사를 재사용했다. 새 candle fixture를 Client가 읽지 않음을 확인했으며 Client를 다시 실행하지 않았다. 새 fixture는 Client workflow의 경로 조건에 해당하므로 해당 CI도 실행 대상이다. 현재 브랜치의 CI·merge·배포 통과를 뜻하지 않는다.

실제 Keychain·외부 제공자 요청·유료 실행·실거래·새 VM은 수행하지 않았다. closed-bar 근거와 실제 paper 운영은 후속 작업이며, 본 수집 단위로 전체 투자 자동화 목표가 완료되지는 않는다.
