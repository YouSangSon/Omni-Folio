# G1.20 Versioned chronological ledger replay

상태: 구현·집중 회귀·독립 리뷰·최종 전체 검사·로컬 smoke 통과(2026-10-23).

## 원인과 결과

원장 FIFO·snapshot AsOf와 broker quantity가 raw RFC3339Nano 문자열 순서를 사용했다. `00Z`가 `00.1Z`보다 뒤에 정렬되어 매수보다 이른 매도를 허용하거나 정상 매도를 거절했다. CASH_VOID의 Go 시간 검사는 옳았지만 SQLite guard가 문자열을 비교해 정상 1ns 뒤 취소를 거절했다.

| 같은 이벤트 | 이전 v1 재생 | 현재 v2 재생 |
| --- | --- | --- |
| 00Z에 1주×10 매수, .1Z에 1주×20 매수, .2Z에 1주×30 매도 | 잔여 원가10·실현손익10·AsOf00Z | 잔여 원가20·실현손익20·AsOf.2Z |

canonical UTC 검증 뒤 terminal Z를 제외한 정확한 정렬과 sequence 동률 순서를 사용한다. `fifo_utc_nanosecond_exact_else_half_even_residual_8_v2`는 기존 유한 decimal/반복소수 half-even·잔여 원가 보존 계산을 유지한다. 세무 원가 정책이나 투자 성과가 아니다.

## 전환과 복구 계약

- schema22는 singleton replay selector와 한 번만 기록하는 전환 receipt를 추가한다. revision0이면서 이벤트가 실제로 없는 DB만 v2 genesis다. COUNT/MIN/MAX와 revision이 일치하지 않으면 복구·정상 조회를 거절한다.
- 기존 비어 있지 않은 DB는 v1 준비 상태로 남는다. 정상 서버 시작·원장 snapshot과 새 event INSERT는 거절된다. 구 버전 core를 중지한 DB 또는 소유한 복사본에서만 전환한다.
- preview는 실제 원장 revision, 모든 canonical event 필드와 sequence의 digest, v1/v2 전체 snapshot과 hash를 함께 반환한다. 서로 다른 FIFO/PnL·AsOf·provenance와 실제 ledger_meta.recorded_at을 검토한다.
- activate는 같은 IMMEDIATE write transaction에서 예상 revision·event digest·새 snapshot hash를 다시 확인한다. 불일치·잘못된 시간 순서·DB 오류는 selector와 receipt를 모두 rollback한다. 성공한 receipt는 전환 당시 prefix와 캡처한 metadata 시각으로 계속 재검증한다.
- SQLite REPLACE는 UPDATE/DELETE guard만으로 막히지 않는다. state/receipt의 BEFORE INSERT guard도 replacement를 거절하며 DDL·guard 자체를 복구 시 검사한다. recursive_triggers 설정 완화에 의존하지 않는다.
- duplicate-only import는 event 수를 늘리지 않아도 ledger_meta.recorded_at을 바꾼다. 마지막 event 시각으로 대체하지 않는다. metadata만 달라져도 preview hash가 바뀌어 재검토가 필요하다. snapshot GET의 검증과 계산은 같은 읽기 transaction이다.
- backup v16/schema22는 현재 v2 selector·명시적 v2 golden·전환 prefix proof를 요구한다. 전체 DB SHA가 selector/receipt를 이미 포함하므로 중복 manifest digest는 추가하지 않았다.
- 지원하는 과거 v5–v15 artifact는 원본 DB/golden/manifest hash를 확인하고 소유한 migrated copy에서 v1 결과 전체를 비교한다. 누락 policy는 이 과거 분기에서만 v1로 해석한다. 검증 실패 후 v1으로 재시도하는 fallback은 없다. 과거 eligible_for_activation은 당시 정책의 기록이며, 과거 검증 성공은 현재 v2 활성화 성공을 뜻하지 않는다.
- 기존 broker record JSON·hash·시각은 유지한다. 같은 BUY/SELL 집합의 최종 수량은 순서에 무관하므로, 새 순서가 nonnegative 검사를 통과하고 현재 전체 차이가 저장 결과와 정확히 같을 때만 기존 cache를 반환한다. 불일치는 덮거나 재해시하지 않고 거절한다.

## 로컬 전환 절차

Core와 Flutter를 함께 업데이트한다. 원본 backup과 v1 golden을 보존하고, core가 중지된 소유 후보 경로를 지정한다. 아래 명령은 저장소 root 기준이다.

```sh
replay_candidate=/absolute/path/to/owned-candidate.db
replay_preview=$(mktemp)
go -C services/core run . migrate -db "$replay_candidate"
go -C services/core run . ledger-replay-preview -db "$replay_candidate" > "$replay_preview"
```

preview의 legacy_snapshot과 snapshot을 검토한 뒤 **해당 preview의 값**을 사용한다. 파일 bytes의 SHA를 snapshot_sha256 대신 넣지 않는다.

```sh
go -C services/core run . ledger-replay-activate -db "$replay_candidate" \
  -expected-revision REVIEWED_REVISION \
  -expected-events-sha REVIEWED_EVENTS_SHA256 \
  -expected-snapshot-sha REVIEWED_SNAPSHOT_SHA256
```

별도 새 golden 파일에는 preview JSON의 snapshot 객체만 저장한다. 기존 golden을 덮지 않는다. 이 새 golden으로 `backup -db CANDIDATE -out NEW_BACKUP -golden NEW_GOLDEN -manifest NEW_MANIFEST`와 `verify-restore -db NEW_BACKUP -golden NEW_GOLDEN -manifest NEW_MANIFEST`를 통과한 뒤 후보를 현재 DB로 사용한다. 기존 core 프로세스의 계산 정책이 실행 중에 바뀌는 것은 지원하지 않는다. 전환 뒤 v1으로 되돌리는 UPDATE 대신 보존한 원본으로 별도 복구를 검증한다.

## 검증 근거

- 실제 import/API/SQLite RED 5건: 잘못된 FIFO 원가·AsOf, 정상 1ns 뒤 매도 거절, 미래 매수로 과거 매도 허용, broker quantity 음수, 정상 CASH_VOID 거절. 수정 후 집중 회귀 통과.
- `TestLedgerReplay*`: 기대 revision/digest/hash 오류·backdated activation·metadata-only 변경 거절, 실제 duplicate-only import, 즉시 활성 결과의 승인 SHA 일치, 이후 append 뒤 prefix 복구, receipt 제거/재해시·SQL REPLACE 거절, CLI preview/activate와 필수 입력·없는 DB 거절, 소수초 FIFO v15 원본 보존·누락/unknown policy 경계를 검증한다.
- revision0 뒤 기존 거래가 숨는 경우는 fresh와 legacy migration 모두 RED를 재현한 뒤 차단했다. 마지막 집중 범위 `TestLedgerReplay|TestLedgerChronology|TestGoldenVerticalSliceAndBackupRestore|TestG4H|Legacy|MigratesV8|Migration`는 Go1.24.5 exit0(15.243초)이다.
- 독립 read-only 리뷰에서 REPLACE·duplicate metadata·genesis count 및 GET 읽기 transaction 경계를 보완했고 남은 차단 사유가 없다. 리뷰는 실행 결과를 대신하지 않는다.
- 최종 `make check CORE_TEST_PACKAGES='-timeout=30m ./...'` exit0: core 전체386.630초·내부5개 Go package, Flutter108개, Python34개, format/vet/analyze·Python compile·resource preflight/cleanup, JSON17개 구문 검사 통과. Go1.24.5·Flutter3.47.1-stable/Dart3.13.1·Python3.14.5의 절대 경로와 자식 PATH를 고정했다. JSON 구문 통과는 완전한 JSON Schema validator 실행을 뜻하지 않는다.
- 첫 최종 전체 실행은 미래 schema 번호·v1→최신 schema 기대값·v9의 과거 policy golden fixture 3곳에서 실패했다. 검증을 완화하지 않고 각 fixture를 현재/과거 계약에 맞춰 고쳤으며, 해당3건 집중 검사와 위 전체 실행을 모두 통과했다.
- 최종 `make smoke` exit0: health·status·preview·apply·snapshot·activity·market data. 소유 test temp root와 실행 process가 남지 않았다. GitHub Actions workflow0개이며 CI 통과를 주장하지 않는다. 실제 사용자 DB 전환·실브로커·배포·물리기기 검증은 이 gate 범위가 아니다.

로컬 원시 로그: ignored `docs/_internal/skill-outputs/goal-20261023-{chronology-red-final,replay-transition-green,replay-legacy-final,replay-proof-final,replay-genesis-red,replay-count-final,chronology-check-final,chronology-fixture-fixes,chronology-check-rechecked,chronology-smoke-final}.log`. `chronology-check-final`은 위3건 실패 로그이며 `chronology-check-rechecked`가 최종 통과 로그다. 앞선 중간 전체 검사는 새 genesis 결함 확인으로 SIGTERM 후 정리했으며 통과로 집계하지 않는다.
