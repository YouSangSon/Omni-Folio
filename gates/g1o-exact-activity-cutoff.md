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

## 호환성과 남은 위험

기존 `snapshotFrom`의 FIFO 정렬·AsOf, broker quantity replay와 cash-void DB trigger에도 문자열 시각 비교가 남아 있다. 이를 곧바로 교체하면 기존 backup의 snapshot JSON/provenance/asOf 동등성 검사를 깨뜨릴 수 있다. 별도 버전 정책과 이전 backup 복원·경제 결과 대조가 필요하다. 이번 수정은 해당 금융 replay 문제의 해결을 주장하지 않는다.

Activity cursor는 service 시작마다 바뀌는 키로 봉인되므로 정상 서버 재시작 시 기존 cursor는 거절된다. 향후 키를 영속화하는 hot upgrade를 도입하면 정렬 버전이 다른 cursor를 명시적으로 무효화해야 한다. SQLite datetime/julianday의 제한된 소수 정밀도로 나노초를 대체하지 않는다.
