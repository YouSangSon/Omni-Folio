# G1.25 KRW·USD 비교 지수 원본의 버전별 보관

상태: [PR #48](https://github.com/YouSangSon/Omni-Folio/pull/48) 구현 중. 실제 CLI 부재 RED를 확인했고 파일 입력·조회·불변 보관·백업/복구와 회귀 검사를 작성했다. 최종 독립 리뷰와 무료 원격 전체 검사는 아직 대기 중이다. 이 선행 단위는 실제 benchmark 수익률 비교의 완료를 뜻하지 않는다.

## 문제와 선택

같은 이름의 가격이라도 배당 재투자 여부와 분할 조정 기준에 따라 수익률은 달라진다. 현재 종목 가격의 `price_adjustment=unspecified`를 총수익 지수로 해석하지 않는다. 정규화된 지수 한 버전 전체를 보관하고 계산 시 사용할 버전을 고정할 수 있게 한다. 원본의 보관 무결성, 제공자 데이터의 정확성, 사용 권한, 비교 계산의 정확성은 서로 다른 증명이다.

예를 들어 같은 버전 이름으로 100→110을 넣은 뒤 100→120을 보내면 덮어쓰지 않고 충돌로 거부한다. 새 버전으로 보관할 수 있지만 과거 버전을 재시도해 최신 선택을 되돌리지는 않는다. 시작점과 끝점을 서로 다른 버전에서 임의로 섞는 비교도 후속 계산에서 허용하지 않는다.

사용자 선택에 따라 KRW와 USD를 모두 지원한다. 이 단위는 각 지수의 선언 통화를 그대로 보관한다. USD 수익률과 KRW 환산 수익률, 같은 정확한 TWR 기간/보고서 시점을 사용하는 비교·공개 API·Flutter 화면은 다음 구현 범위다.

## 입력과 보관 계약

- [`benchmark-index-series.v1`](../contracts/benchmark-index-series.schema.json)은 필수 필드10개와 점별 필드2개만 받는다. UTF-8·surrogate·중복/미지정/누락/null 필드·추가 JSON을 거부한다. 원문과 canonical JSON은 각각1 MiB 이하, 점은1–10,000개다. CLI는 열린 파일 descriptor에서 regular file과 크기를 검사해 FIFO 입력을 거부한다.
- 통화는 `KRW` 또는 `USD`, 수익 기준은 `price_index`, `gross_total_return`, `net_total_return`다. level은 기존 core의 canonical nonnegative decimal 문자열이며64자 이하이고0을 허용한다.0을 미래 수익률의 분모로 사용하는 것은 별도 거부 대상이다.
- 관측 시각은 canonical UTC로 엄격히 증가하며 `fetched_at` 이후일 수 없다. label은 trim된160 UTF-8 bytes 이하, 두 ID는128 bytes 이하이고 Unicode 공백/제어 문자를 거부한다. 출처는512 bytes 이하의 공개 HTTPS 참조로 제한하며 credentials/query/fragment를 받거나 URL을 호출하지 않는다.
- `(benchmark_id, source_version)`은 immutable unique identity다. 같은 ID는 통화·수익 기준·출처 참조·sample 여부를 바꿀 수 없다. label은 새 버전에서 바꿀 수 있다. `source_version`은 소유자 파일 버전이며 제공자 검증 revision이나 사전순 최신 시각이 아니다. `fetched_at`도 payload에 포함되므로 같은 버전에서 바꾸면 충돌이다.
- 정확한 재시도는 기존 receipt/recorded time을 반환한다. clock 호출·새 행·과거 버전 재선택을 하지 않는다. 전체 기록 증명과 identity 확인·append는 같은 write transaction에서 실행한다. 새 writer는 기존 regular DB만 escaped `file:` URI와 `mode=rw`, immediate transaction으로 연다. status는 기존 read-only opener를 사용한다.
- receipt의 `storage_check=verified`는 저장 무결성, `authority=owner_declared`는 소유자 선언을 뜻한다. 제공자·시장·실제 투자 성과를 인증하지 않는다. 예제 두 파일은 `example.invalid` 출처의 synthetic sample이다.

## 복구와 이전 형식

schema23은 STRICT table, `(benchmark_id,source_version)` unique, latest index, append-only update/delete guard와 연속 sequence·recorded time·identity insert guard를 추가한다. 비어 있는 기록도 embedded migration의 물리 DDL/index/trigger와 일치해야 한다. 모든 행은 닫힌 parser·canonical payload/SHA·행 hash·연속 순서·identity·시각을 다시 증명한다.

backup17/schema23은 source와 별도 candidate에서 기록 digest·버전 수·점 수를 대조한다. 세 manifest 필드와 네 receipt 필드는 빠지거나 null일 수 없으며 각각 실제 proof와 맞아야 한다. 서버 시작·status·writer·백업/복구가 같은 proof를 사용한다. 이는 로컬 변경 감지이며 외부 서명이나 제공자 인증을 추가하지 않는다.

backup16/schema22는 기존 chronological FIFO v2 권한을 유지한다. backup15 이하의 기존 archival 검증과 구분하며, 새 latest format 상수로 이 권한을 판정하지 않는다. 지원하는 이전 형식은 원본을 바꾸지 않는 소유한 복사본에서 migration하고 benchmark 기록이 비어 있어야 한다. 이전 manifest가 새 benchmark proof 필드를 주장하면 거부한다. 기존 source policy·활성 paper lease 검사도 유지한다. 어떤 검증도 활성 DB 교체를 자동 실행하지 않는다.

## 출처와 데이터 사용 범위

- [Portfolio Performance benchmark 문서](https://help.portfolio-performance.info/en/how-to/benchmarking/)의 같은 기간 정규화와 가격/배당 기준 구분을 참고한다. 코드·원문·시장 자료를 복제하지 않았다.
- [FRED SP500 공식 주석](https://fred.stlouisfed.org/series/SP500)은 배당을 포함하지 않는 가격지수라고 밝히고, 자료 재현의 사전 서면 허가 조건을 표시한다. 사이트에서 값을 볼 수 있는 것만으로 공개 fixture 배포 권한을 인정하지 않는다.
- [KRX OPEN API 이용약관](https://openapi.krx.co.kr/contents/OPP/INFO/OPPINFO002.jsp)은 API의 인증키·비상업적 이용·화면 출처 표시·제3자 제공 제한을 규정한다. 이 API 약관을 별도 수동 다운로드의 계약으로 확대하지 않는다. KOSPI의 실제 다운로드 경로·통화/수익 기준의 원문과 사용 권한은 아직 확정하지 않았다.
- 이 단위는 실제 KOSPI/S&P500 자료·API key·자동 수집기를 추가하지 않는다. 실제 지수 선정과 사용 가능한 owner dataset을 확인한 뒤 비교를 연결한다. 날짜·시장 마감 시각이나 분할/배당 의미를 추측해 metadata를 채우지 않는다.

## 검증과 다음 작업

- RED: `90b961b778abfb3faf3c225b66fbcad740b6571c`, [Core 40184149595](https://github.com/YouSangSon/Omni-Folio/actions/runs/40184149595)의 `TestBenchmarkSeriesCLIImportsAnExplicitIndexVersion`이 실제 `unknown command "benchmark-import"`로 실패했다. 이후 전체 검사는 실행되지 않았다.
- 집중 테스트는 두 통화/세 수익 기준, 닫힌 JSON·byte/점 수·시각·취소, 버전 충돌/정확한 재시도/동시 writer, 특수문자 DB 경로·미생성/FIFO·출력 실패 후 재시도, 빈/비어 있지 않은 물리 schema·journal 손상, 실제 backup/restore와 이전 형식의 권한·원본 보존을 대상으로 작성했다. 작성은 통과 증거가 아니다.
- 로컬에서는 format·JSON·문서/명령 대응·diff만 확인한다. Go/Flutter runtime·native build·DB stack은 공유 호스트에서 실행하지 않는다. 기존 public Ubuntu 무료 Actions가 필요한 실행 검사를 소유한다. 유료 runner·새 VM·cache/artifact 업로드·credential 변경은 없다.
- 최종 독립 리뷰·최종 head의 Core/Client 결과·네 README 구조/명령 대응과 source identity를 확인한 뒤 완료 상태를 갱신한다. 같은 정확한 TWR 관찰과 version/FX를 고정하는 비교 구현은 계속 진행한다.

Transfer check: 100→110과100→120을 같은 버전 이름으로 넣으면 두 번째 입력은 거부되어야 한다. 새 버전 입력 뒤 첫 파일을 재시도해도 최신 조회는 새 버전이어야 하며, KRW 지수를 USD로 바꾸려면 다른 identity가 필요하다.
