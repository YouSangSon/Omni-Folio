# G1.19 Exact activity ordering and current valuation cutoff

Scope: 거래 내역 페이지와 현재 KRW 평가의 시각 경계를 canonical UTC 나노초 순서에 맞춘다. 원장 쓰기·FIFO 계산 정책·기존 snapshot 및 backup golden은 변경하지 않는다.

## 원인과 수정

RFC3339Nano는 소수부의 뒤쪽 0을 생략한다. `00Z`가 `00.9Z`보다 문자열 비교에서 크므로 원래 SQL 정렬은 최신순을 뒤집었다. 새 Flutter parser는 이 응답을 거절하며, snapshot의 문자열 최대 발생시각을 사용하던 현재 평가는 `.1Z` cutoff 뒤 `.9Z` 거래까지 포함했다. Go 1.24.5 `src/time/format.go:44–45`도 이 형식의 문자열 정렬 한계를 명시한다.

전체 event proof가 canonical UTC를 확인한 뒤, 거래 내역의 ORDER BY와 keyset 비교에서 모두 `rtrim(occurred_at,'Z')`를 사용한다. 소수부 없는 초는 같은 초의 소수부보다 먼저, 같은 시각은 기존 private sequence 순서로 정렬된다. 원본 시각과 cursor payload는 그대로 보존한다. 현재 평가는 같은 읽기 transaction에서 이 순서의 최대 발생시각을 조회해 cutoff 검증과 `LedgerAsOf`에 함께 사용한다.

## 검증

- RED: 실제 Go 회귀 2개 exit 1. 거래 내역 첫 페이지는 최신 금액 5 대신 과거 금액 1을 반환했고, 미래 `.9Z` 입금을 `.1Z` 평가에 포함해 HTTP 200과 합계 3을 반환했다.
- GREEN: `TestLedgerActivitiesOrderFractionalSecondsAcrossPages`, 전체 `TestCurrentPortfolioValuation*`, `TestG1ELedger*`를 Go 1.24.5로 실행해 exit 0(26.387초). 전체 초·1ns·가변 소수부·동일 시각 sequence·마지막 cursor, 미래 cutoff 거절과 이후 정상 합계/정확한 LedgerAsOf를 검증한다.
- 수정 후 독립 읽기 전용 리뷰: 새 정렬과 cursor 조건·동일 transaction·기존 복구 계약 보존에 병합 차단 사항 없음. 리뷰는 실행 결과를 대신하지 않는다.
- 원시 로그: ignored `docs/_internal/skill-outputs/goal-20261022-time-{red,green}.log`.

## 최종 로컬 검증 (2026-10-23)

Go 1.24.5, Flutter 3.47.1-stable, Python 3.14.5의 절대 경로와 자식 process PATH를 고정했다. 실행 소스는 `6283e70`이며 이후 변경은 gate 문서뿐이다.

- `make check`는 format·Go vet·Flutter analyze·Python compile·내부 Go 5 package를 통과했지만 root Go package의 기본 10분 제한으로 exit 2였다.
- 같은 root package를 `make test CORE_TEST_PACKAGES='-timeout=30m .'`로 끝까지 실행했다. 1544.039초 뒤 SIGKILL 복구 child 종료와 stream executable 빌드의 `crypto/x509` 조회 오류 두 건으로 exit 2였다. 나머지 Go 검사에는 실패가 없었다.
- 해당 두 테스트를 원래 개별 deadline 그대로 다시 실행했다: `make test CORE_TEST_PACKAGES="-timeout=10m -run='TestLocalPaperExecutableSignalRecovery|TestPaperStreamExecutableLifecycle' -count=1 ."` exit 0. Go 200.734초, Flutter 108개, Python 34개, resource preflight와 cleanup self-test가 통과했다. 이전 두 실패의 원인은 확정하지 않았으며, 소스나 개별 deadline을 바꿔 통과시킨 것은 아니다.
- `make contract-check` exit 0: JSON 17개 구문 확인. 완전한 JSON Schema 적합성 검사와 구분한다. `make smoke` exit 0: 실제 임시 Go/SQLite의 health·status·preview/apply·중복/충돌·snapshot·activity·market data와 종료 정리를 확인했다.
- 실패 필터의 첫 셸 인용 오류와 사용자 요청에 따른 중단 실행은 통과 근거에서 제외했다. 동일 실행 소스의 통과 범위를 재사용한 분할 검증이며, 전체 `make check` 일괄 성공이나 GitHub CI 성공으로 표현하지 않는다. 저장소 Actions workflow는 0개다.
- Flutter 소스는 G1.18 release 브라우저 검증 당시 `d9e98aa`와 동일하다. 해당 UI 근거를 재사용하며 새 실제 기기·screen-reader·배포 검증은 주장하지 않는다.

로컬 로그: `docs/_internal/skill-outputs/goal-20261022-pinned-{check,test-budget}.log`, `goal-20261023-resumed-{recovery,contract-check,smoke}.log`.

## 호환성과 남은 위험

이 checkpoint는 기존 FIFO/snapshot·broker quantity·cash-void의 문자열 비교를 남겨 두었다. 이후 [G1.20](g1p-chronological-ledger-replay.md)이 영속 정책 selector·명시적 전환 receipt와 과거 golden 보관 검증을 추가해 이 경계를 교정한다. G1.19 자체의 조회 수정과 구분한다.

Activity cursor는 service 시작마다 바뀌는 키로 봉인되므로 정상 서버 재시작 시 기존 cursor는 거절된다. 향후 키를 영속화하는 hot upgrade를 도입하면 정렬 버전이 다른 cursor를 명시적으로 무효화해야 한다. SQLite datetime/julianday의 제한된 소수 정밀도로 나노초를 대체하지 않는다.
