# G3.8G2M — 보관 입력과 DB 기록 대조

## 문제와 원리

입력 보관 파일은 해당 byte를 관찰했다는 증거다. `signal=none`은 signal journal을 남기지 않고, 정책 HALT는 새 제안 접수 전에 성공 반환할 수 있다. 신호·주문 기록이 있어도 이후 봉의 기존 주문 복구가 필요할 수 있으므로 이들을 하나의 “완료” 값으로 합치지 않는다. 파일 ACK가 이전 DB 복원 뒤에도 남는 문제도 피해야 한다.

`paper-input-status`는 보관 bundle 한 건과 **지정한 DB의 현재 기록**을 대조한다. `completion=unknown`, `execution_authorized=false`, `freshness=unverified`를 항상 명시하며, 기록 부재를 미처리·재실행 허가로 바꾸지 않는다. 기존 복구 증명과 read model을 재사용하며 별도 완료 저널은 만들지 않는다. 명시적 재실행은 기존 `paper-execute -bundle ... -arm-paper`를 사용하고, 조회가 재실행 여부를 결정하지 않는다.

## 구현과 계약

- [`paper_input_status.go`](../services/core/paper_input_status.go)는 기존 bounded regular-file reader·bundle/proposal decoder·연구 CSV 검증을 사용한다. [SQLite의 `mode=ro`와 URI 규칙](https://www.sqlite.org/uri.html)에 따라 표준 `net/url`로 경로와 query를 구분해 인코딩하고 deferred transaction으로 연다. `ReadOnly` 옵션만으로 쓰기 차단을 주장하지 않는다.
- schema와 전체 paper runner 복구 증명을 확인한 동일 transaction에서 selection·research/session binding, 입력 봉·최초 receipt, signal과 연결 주문의 저장 상태를 조회한다. 과거의 정확한 selection event도 조회할 수 있으며 현재 선택과의 일치는 별도 값이다.
- 입력의 구조·hash·연구 binding과 저장 기록을 확인한다. SMA admission, 현재 만료 여부·시장 freshness·실행 가능성은 판정하지 않는다. 마지막 봉의 입력 hash·내용 충돌, 범위 안 저장 봉 누락, 손상은 오류이며 정상적인 기록 부재와 구분한다.
- 마지막 봉이 없으면 `bar_state=not_observed_in_this_database`, 있으면 `matched`와 최초 `bar_recorded_at`·그 시각 +30초의 `proposal_deadline`을 반환한다. deadline 표시는 현재 실행 허가가 아니다. `none`의 signal 부재와 signal은 있으나 order가 없는 no-delta를 구분하고, 다른 입력의 정책 HALT를 이 입력의 완료로 귀속하지 않는다.
- snapshot CSV 파싱을 기존 importer와 공유하되 importer의 시각 관측 위치·순서·원자 저장·최초 receipt 규칙을 유지한다. 새 schema·backup 형식·공개 API·UI·credential·lease/arm 동작은 없다. CLI 오류는 입력·DB의 원본 내용과 옵션 sentinel을 제거한다.

## 검증 근거와 범위

RED head `616c309`의 [무료 표준 Core CI](https://github.com/YouSangSon/Omni-Folio/actions/runs/40153465449)에서 기존 명령의 `unknown command "paper-input-status"`를 재현했다. `TestPaperInputStatus*`는 미관찰·import-only·신호/주문·과거 선택, none/no-delta·HALT, 이전 verified backup, 실제 read-only 쓰기 거절·특수문자 URI, 취소·손상·오류 redaction을 다룬다. 저장 내용 충돌·중간 봉 누락·최초 receipt byte 충돌은 hash를 다시 맞춘 입력으로 저장 경계까지 검증한다.

검사와 최종 통과 결과는 [PR #38](https://github.com/YouSangSon/Omni-Folio/pull/38)의 해당 head에 연결한다. `.github/workflows/core-checks.yml`은 public/same-repo PR만 고정 action·표준 runner·read-only token으로 실행하고 cache/artifact를 업로드하지 않는다. 집중 검사는 기존 `make test`, 최종 검사는 `make check DART=true FLUTTER=true CORE_TEST_PACKAGES='-timeout=30m ./...'`로 소유 preflight/cleanup을 유지한다. Flutter 소스는 같으므로 이번 Core 검사에서 Dart format·Flutter analyze/test를 실행하지 않는다.

이번 Goal은 사용자 요청으로 VM을 우선 건너뛴다. 로컬 SQLite 통합·프로세스 fixture·전체 stack을 실행하지 않으며, 원격 결과와 로컬 format/link 검사를 구분한다. read-only SQLite도 보조파일·reader lock 접근은 가능하므로 파일시스템 무변경을 보장하지 않는다. 조회 시점 뒤 상태 변경, 실제 정전·장기 운영·소스 완결성·자동 재실행·실브로커·실기기 검증은 별도다.

## Linux CI에서 드러난 watcher 종료 경계

첫 구현은 기존 시각 helper의 `bool` 반환형을 오류로 연결해 컴파일에 실패했다. 이를 교정한 뒤 새 Go 조회·native CLI 검사는 통과했지만, 기존 Python watcher의 SIGINT 종료 검사가 타임아웃됐다. 전체 Core 검사는 이 단계에서 시작하지 않았다. 실패를 전체 통과로 합산하지 않는다.

[통제된 RED](https://github.com/YouSangSon/Omni-Folio/actions/runs/40154797894)의 `513ba84`는 별도 launcher가 `SIGINT=SIG_IGN`을 설정·확인한 뒤 실제 모듈을 `execv`한다. 첫 제안 뒤 SIGINT를 보내도 기존 5초 안에 끝나지 않았다. [Python의 기본 signal 규칙](https://docs.python.org/3.14/library/signal.html#general-rules)은 부모가 바꾼 SIGINT 상태가 기본 `KeyboardInterrupt` 처리의 조건임을 설명한다. Makefile background wrapper가 disposition을 초기화하지 않는 점은 소스로 확인했고, 통제된 상속 무시는 원격으로 재현했다.

수정은 [`signal_cli.py`](../services/research/omni_research/signal_cli.py)의 CLI 실행 진입점에서만 `signal.default_int_handler`를 등록하는 것이다. 기존 `KeyboardInterrupt → 130`과 보관 함수의 `finally` 정리를 재사용한다. import/library 호출의 전역 signal 정책과 SIGTERM의 OS 기본 종료, 공통 Makefile owner/PGID cleanup은 변경하지 않는다. 일반 상속·명시적 상속 무시와 SIGTERM/SIGKILL·잘못된 입력 사례를 유지하며 종료 대기를 늘리지 않는다. 모든 중단 위치나 실제 정전을 증명하는 범위는 아니다.
