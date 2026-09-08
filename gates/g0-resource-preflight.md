# G0 test resource identity preflight

상태: 권한 복구 후 시작 실패 사전 차단, 실제 smoke 빌드 실패의 자손 회수, 정상 전체 실행과 성공·실패·SIGINT·SIGTERM 자원 matrix 검증 통과 (2026-09-08).

## 원인과 변경

이전 제한 환경에서 `make test`는 `ps` 실행을 거절당했다. 기존 wrapper는 temp root를 먼저 만들고 빈 owner command/start를 기록한 채 자식을 시작했다. 자식도 start 관측에 실패해 incomplete marker가 남았고 기존 cleanup은 이를 안전하게 거절했다. 테스트 본체는 실행되지 않았다. 현재 환경에서는 `ps`, Flutter 3.47.1 실행과 새 로컬 Git branch 생성이 성공했으며, 이전 권한 차단을 현재 blocker로 재사용하지 않는다.

`Makefile`의 공통 `require-process-identity`는 `test`, `check`, `smoke`, `test-resource-cleanup`, `clean-test-resources` 초입에서 현재 recipe shell의 command/start를 확인한다. `ps` 종료 상태를 먼저 확인한 뒤 공백을 정규화하고 빈 값을 거절한다. 생성 wrapper는 같은 관측값을 metadata에 기록한다. 기존 PID/PGID/start 비교·stale-owner 판정·조건부 종료·경로 제한을 제거하거나 완화하지 않는다. 관측 불가를 stale로 오판한 정리도 시작하지 않는다.

기존 `ps | awk` 형태는 정규화 명령의 성공이 관측 실패를 가릴 수 있었다. 새 회귀는 `ps`가 그럴듯한 stdout을 쓰고도 nonzero로 종료하는 사례를 별도로 확인한다. 권한 우회, fake identity를 사용한 실제 테스트 실행, SDK relocation은 하지 않는다.

복구된 환경의 실제 process-tree matrix에서 별도 누수를 재현했다. `smoke`는 실패한 build launcher를 기다린 뒤 `child_pid`를 지워, trap이 임시 root만 삭제하고 살아 있는 자손을 놓쳤다. 기존 `stop_child_group`을 `define-owned-child-cleanup`으로 공유하고 smoke도 기록된 PGID/start를 확인해 그룹을 회수한 뒤 root를 삭제한다. 그룹 정리 실패 시 원래 실패를 유지하고 metadata/root를 보존하며 무기한 wait하지 않는다.

## 검증 근거

- RED: 기존 실제 `make test` exit 2, 본체 진입 전 `ps` 거절 및 incomplete metadata. 소유 parent PID 71263과 child PGID 71276의 부재를 `kill -0`의 `No such process`로 확인한 뒤 이 실행의 `omni-folio-test.nrTLvD` metadata와 빈 디렉터리만 제거했다.
- RED: 실제 Make recipe를 격리된 임시 cwd에서 실행한 최초 25개 실패 주입 사례가 모두 실패했다. 외부 프로세스 관측은 stub, 작업과 `mktemp`는 종료 전용 sentinel로 대체하여 실제 workload는 시작하지 않는다.
- GREEN: 최종 `make check`에서 preflight unittest 1개의 40개 하위 사례가 8.423초에 통과했다. 5개 진입점 × command/start 거절·빈 값·공백·실패 stdout 및 정상 진입 대조를 포함한다. 정상 대조도 실제 workload를 실행하지 않으며, cleanup 대조는 자기 fixture의 artifact만 지운다. 잘못된 Make 구문이나 무조건 중단이 green이 되지 않도록 정상 대조와 오류 원인을 함께 확인한다.
- 실제 process-tree matrix는 `test`/`check`의 정상·workload exit 42·SIGINT·SIGTERM과 `smoke`의 build 실패·SIGINT·SIGTERM, 총 11개 사례를 통과했다. 각 사례는 기록한 main/descendant/leaf PID·start·PGID의 부재, session root·artifact 제거, 무관 sentinel 보존을 확인했다. 하네스 자체의 INT=130, TERM=143, timeout과 startup 중단에서도 소유 fixture 잔여가 0임을 별도로 확인했다.
- Python `TemporaryDirectory`와 기다리는 subprocess handle을 사용하고 cleanup 오류를 숨기지 않는다. identity가 없거나 다시 관측할 수 없으면 임시 root와 metadata를 보존해 다른 프로세스를 종료하지 않는다. [Python 공식 tempfile 문서](https://docs.python.org/3/library/tempfile.html#tempfile.TemporaryDirectory)
- 제한 회귀 종료 후 task temp parent의 `omni-folio-*` 디렉터리 inventory는 비어 있었다. Podman/Kind·listener·실제 앱 서버를 만들지 않았다.
- 권한 복구 전 `make check`는 `ps: Operation not permitted` 다음 고정 preflight 오류로 본체 진입 전에 종료했다. 이는 안전한 시작 거절의 RED 증거일 뿐 현재 blocker가 아니다.
- 권한 복구 후 최종 `make check` exit 0: Dart format 13개, Flutter analyze, Go 전체, Flutter 98개, Python 34개, cleanup self-test, JSON 계약 17개가 통과했다. 실제 `make smoke` exit 0에서 build/migrate/server와 health·ready·status·preview/apply/idempotency/conflict·snapshot/activity/candles를 통과했고, 종료 후 listener·core binary·smoke root가 없음을 확인했다.
- 독립 리뷰: Makefile의 shell/PID/정리 계약과 하네스 startup identity 보존에서 추가 critical 결함 없음. 리뷰는 실행 증거를 대신하지 않고 위 실제 matrix와 함께 판정했다.

전체 로그는 로컬 ignored `docs/_internal/skill-outputs/`의 `preflight-red.log`, `preflight-check.log`, `activity-preflight-check-final.log`, `preflight-restored-smoke.log`, `resource-lifecycle-restored.log`에 둔다. GitHub CI나 배포 증거가 아니다.

## 범위 경계와 다음 작업

- process-tree matrix는 실제 프로세스를 만드는 수동 진단 target으로 두며 기본 `test-body`에 추가하지 않는다. 하네스 밖의 SIGKILL·host crash는 trap을 실행할 수 없으므로 별도 guardian·다음 실행 복구 증거 없이 완전 회수를 주장하지 않는다.
- 이 변경은 시작 시점의 관측 불가를 차단한다. preflight 이후 관측 권한이 바뀌거나 child metadata 기록이 실패하는 동적 경로는 별도 검증이 필요하며 incomplete marker 거절을 약화하지 않는다.
- 외부 push/merge/배포는 실행하지 않는다. Go API·원장·주문 코드는 이 변경에 포함하지 않는다.
- Flutter 거래 내역 전체 탐색은 별도 목적의 UI 변경으로 병행 중이며, 이 자원 gate 통과가 해당 기능의 통과를 뜻하지 않는다.
