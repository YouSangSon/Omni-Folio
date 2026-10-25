# G1.27 직접 입력 환율의 완전한 버전과 성과 연결

목적은 소유자가 준비한 직접 환율로 KRW 평가·TWR·XIRR·USD 지수의 KRW 환산을 연결하면서 저장 무결성과 시장 자료의 권위를 구분하는 것이다. [G1.25 지수 보관](g1u-benchmark-series.md), [G1.26 비교](g1v-portfolio-benchmark-comparison.md)의 실제 시장 자료 선정 문제를 해결했다고 주장하지 않는다.

## 작은 작동 모델

한 통화 방향의 파일 한 개가 완전한 버전이다. 보고서 시각까지 알려진 최신 버전을 한 번 선택한 뒤 기말·모든 과거 입출금 경계가 그 버전만 사용한다. 예를 들어 USD100, 시작환율1000, 종료환율900이면 기말 KRW90000과 TWR−10%다. USD 지수가100→110이면 native+10%, KRW−1%, 포트폴리오와의 차이−9%p다. 금액·growth는 exact rational, 공개 수익률의 마지막 단계만 기존 half-even 정책을 따른다.

수정 버전에서 시작점이 빠지면 해당 과거 계산은 불가하다. 이전 버전의 시작점을 가져와 서로 다른 수정 이력을 섞지 않는다. 빈 `points`는 모든 점의 철회다. 예전 파일을 그대로 재시도하면 예전 receipt를 반환할 뿐 최신 선택을 되돌리지 않는다. 새 지수 보정·정책 파일에도 적용할 수 있는 확인 기준은 “새 버전에서 빠진 과거 값을 예전 버전으로 조용히 채우는가?”다.

## 선택과 대안

| 선택지 | 결과와 비용 |
|---|---|
| 기존 fixture 행의 source 확대와 점별 정정 | 오래된 hash·GET·복구 계약에 새 권한·정정 관계가 들어가며 전체 버전의 의미가 불분명해짐 |
| 별도의 완전한 버전 journal | 기존 fixture 계약을 유지하고 수정·철회·재시도를 append sequence 하나로 정의할 수 있음 |
| 제공자 자동 수집 | 정확한 release·개정·휴일·권한·실패 정책과 외부 요청을 먼저 정의해야 함 |

두 번째를 선택했다. 기존 지수의 닫힌 JSON·파일 경계·SQLite 복구 패턴과 표준 라이브러리를 재사용하며 새 의존성·공개 mutation API·새 provider를 추가하지 않는다.

Portfolio Performance의 [통화 문서](https://help.portfolio-performance.info/en/reference/view/general-data/currencies/)는 기준 환율과 실제 거래 환율의 차이를 설명한다. [사용자 정의 데이터 공급자](https://help.portfolio-performance.info/en/reference/file/new/)처럼 입력 출처를 드러내는 개념만 참고했다. `FX_EXCHANGE`의 거래 cash leg를 시장 관측으로 승격하지 않는다.

공식 직접 USD→KRW 후보인 [Federal Reserve H.10](https://www.federalreserve.gov/releases/h10/Hist/)은 New York 정오 매입 기준이며 주간 발표다. [자료 개정 설명](https://www.federalreserve.gov/releases/h10/about.htm), [Board 자료 사용 조건](https://www.federalreserve.gov/disclaimer.htm), [2026년 DDP 전환·정정 안내](https://www.federalreserve.gov/feeds/h10.html)를 확인했다. 주간 자료를 기존24시간 정책의 현재 관측으로 취급할 수 없고 제공자 개정 정책도 필요하므로 자동 수집은 선택하지 않았다. 이 단위에서는 실제 시장 파일·provider 코드·credential을 받지 않았다.

## 입력·출처 계약

- CLI는 `owner-fx-import -db <existing.db> -input <regular.json>`만 허용한다. URI처럼 보이는 DB 이름도 literal path로 연다. 없는 DB·FIFO·디렉터리·과대 파일·추가 인자를 거절하며 외부 DB 오류나 경로를 공개하지 않는다. 취소와 commit 후 출력 실패를 보존한다.
- [owner-fx-series.v1](../contracts/owner-fx-series.schema.json)는 정확히7개 root 필드, 점마다2개 필드다. raw/canonical 각각1MiB, 0..10000점, 안전한128 UTF-8 byte source version, 서로 다른3자 대문자 통화, 양수 canonical decimal≤64자, 정렬된 중복 없는 UTC nanosecond 시각을 요구한다. unknown/duplicate/escaped-duplicate/null/비정상 surrogate·trailing JSON은 거절한다.
- `observed_at ≤ fetched_at ≤ recorded_at`. 기록 clock은 서버 소유이며 journal 전체에서 역행하지 않는다. 완전히 같은 버전 재시도는 clock도 append도 호출하지 않는다. 같은 통화 방향·버전의 내용 또는 sample·수집시각 변경은 충돌이다.
- 자료의 `authority=owner_declared`와 receipt의 `storage_check=verified`는 별개다. 후자는 해시·저장 상태의 무결성만 증명한다. `sample`도 별개의 선언이다. 직접 입력이면서 sample이면 두 안내가 모두 남는다.
- 기존 fixture와 한 방향을 혼합할 수 없다. 빈 버전·미래에 알려질 버전도 방향의 출처 구분을 유지한다. 양방향 SQL guard와 전체 journal 재검증을 함께 사용한다.
- legacy `GET /v1/market-data/fx/latest`와 fixture writer/hash/validator는 바꾸지 않는다. owner 자료는 이 GET에 나오지 않는다. 역산·교차환율·provider source 우선순위를 추측하지 않는다.

## 계산·조회·화면

- 하나의 read transaction과 clock으로 전체 FX journal을 증명하고 cutoff의 whole version을 선택한다. 검증·선택 결과에는 실제 transaction과 cutoff를 묶으며 current valuation·TWR·benchmark helper가 다른 transaction/시각의 결과를 받지 못한다. 가격·원장 검증 경계는 유지한다.
- owner 점의 공개 ID는 `owner_fx_<sequence>_<point>`다. legacy ID는 같은 문자열일 수 있으므로 내부 시각 map은 owner에만 NUL namespace를 붙인다. 기존 ID 검증은 NUL을 금지한다. 공개 JSON에는 내부 key가 나오지 않는다.
- 실제로 선택된 owner FX가 있어야 `owner_declared_fx`와 해당 v2 policy가 나온다. 사용하지 않은 통화 방향·빈 철회는 그 자체로 사용 안내를 만들지 않는다. fixture만 쓰는 v1 응답과 기존 golden은 유지한다.
- 안내는 실패 사유가 아니다. 특히 XIRR 수치 계산 후 finish에서 provenance를 더해 계산을 막지 않는다. stale/부분 선택에서도 선택된 출처와 sample은 유지하며 없는 합계를 만들지 않는다. benchmark 최상위 issues는 목록 실패만 소유한다.
- cash FX의 `state=unknown`은 제공자가 현재성·정확성을 인증하지 않았다는 뜻이다. 나이가24시간을 넘으면 `stale`; 금액 계산의 가능 여부는 line status가 소유한다. `sample=false`도 provider 인증을 뜻하지 않는다.
- Flutter는 v1/v2와 warning의 일치를 엄격히 검증한다. benchmark owner FX mark는 source/version/sample 세 필드가 모두 있어야 하며 양 끝의 버전·sample이 일치해야 한다. 기존 fixture mark는 두 필드 계약을 유지한다.
- Home·TWR·XIRR·비교에서 `직접 입력 환율 사용 · 시세 제공자가 검증한 값이 아닙니다.`를 표시한다. sample 안내와 분리하며 Home의 누락 건수에 출처 안내를 세지 않는다. 비교 상세는 선택한 환율 버전을 표시한다. 새 화면 구조·패키지·언어 지원은 추가하지 않는다.

## schema24 / backup18와 이전 권한

`owner_fx_series_versions`는 STRICT/insert-only이며 canonical payload·hash·순번·세 시각과 방향별 버전 unique를 증명한다. exact DDL/index/trigger 및 TEMP 포함 auxiliary inventory를 확인한다. 기존 FX 물리 계약도 확인해 무시 trigger나 바뀐 guard가 빈/후속 조회를 통과하지 못하게 한다.

backup18/schema24는 owner journal의 SHA/version_count/point_count와 후보 receipt의 check/SHA/count를 모두 요구하고 원본/복사본/복구본을 비교한다. missing/null/다른 값은 거절한다. 기존 지수·가격·원장·lease 증명을 보존한다.

backup17/schema23과 backup16/schema22는 기존 chronological FIFO v2의 비보관용 권한을 유지한다. source 단계에서 당시 schema와 lease·replay·지수 증명을 먼저 확인한 뒤 owned copy에 빈 owner journal만 만든다. old format은 새 owner proof나 원본의 사전 생성 owner table을 주장할 수 없다. v15 이하는 기존 archival 경계를 유지한다. 원본을 migration하거나 활성 lease를 숨기지 않는다.

## 검증 기록

- 실제 RED: `c078d7de69491dd1b43666f28320043f7abb1e93`, [Core40194885753](https://github.com/YouSangSon/Omni-Folio/actions/runs/40194885753), `unknown command "owner-fx-import"`. 기존 실패 영수증과 전체 로그를 보존한다.
- 독립 설계와 구현 정적 리뷰에서 cutoff·완전한 버전·철회 후 출처 고정·namespace·이전 backup 권한을 확인했다. unavailable 비교가 FX mark를 내보내지 않는 기존 계약에 맞춰 테스트 기대값을 고쳤고 추가 차단 결함은 없었다. fixture 주식가격+비샘플 owner FX의 두 안내와 복구 후 빈 버전/출처 고정 검사도 보강했다.
- 첫 구현 `2c87e80de49cb66cdcd9ebb3096223cb277bffa9`의 [Client40200103106](https://github.com/YouSangSon/Omni-Folio/actions/runs/40200103106)은 파서/화면29건 통과 후 SemanticsHandle7건을 종료 전에 해제하지 않아 실패했다. 기존 패턴대로 `finally`에서 해제해 검사를 유지했다. [같은 head Core40200103118](https://github.com/YouSangSon/Omni-Folio/actions/runs/40200103118)의 집중 검사는 통과했으나 최종 head 검사로 교체하면서 전체 진행 중 취소했다. 부분/취소 결과를 전체 성공으로 세지 않는다.
- 최종 검증 head `85d744386ffab923edbf62b6b211125999869048`, [구현 PR #52](https://github.com/YouSangSon/Omni-Folio/pull/52), main `d2c1c8912aa847897f676cc66d351403ce824527`. merge tree `5ba4001aa2bb7637448141ddd3073a54807e6001`가 테스트한 전체 tree와 같음을 확인했다.
- [Core40200350255/job121190168667](https://github.com/YouSangSon/Omni-Folio/actions/runs/40200350255)은 owner 집중2.679s·전체 Go318.658s·내부5패키지·vet/format·Python39·JSON syntax33·소유 자원 cleanup/source identity를 모두 통과했다.
- [Client40200350268/job121190016371](https://github.com/YouSangSon/Omni-Folio/actions/runs/40200350268)은 집중36·전체363·Dart30files zero delta/분석·web/ARM64 release·권한 반례8개·source identity를 모두 통과했다. client tree `0cb3546d379f4d70560816afd6b5acb510bad4aa`, APK23,481,098 bytes·SHA256 `8e61d083ff80a56852c23ed094428f4fc867baccc88d384e7466d21f009541f8`, registrant SHA256 `5d31e4d80984b4f5254d2e6629d2cc2b16760ab4774a94faa863c48897f18326`. 빌드 결과와 검증 소스의 연결이며 byte-identical 재현이나 실행 성능 측정은 아니다.
- 공유 합성 응답은 현금/현재 평가/TWR/XIRR/비교와 owner sample/부분 FX/stale FX8사례다. 독립 Fraction 기준값을 사용하며 시장 자료 정확성의 증거가 아니다. source/sample 분리·과거 시점·수정/철회·재시도·복구·출처 혼합·tx/cutoff·물리 schema·엄격 파서·200% 글자/두 theme와 접근성 반례를 검사했다.
- 로컬은 정적 텍스트·JSON33·schema 참조·Go/Dart format과 README4언어의 명령/링크/anchor 일치만 검사했다. 번역 설명이 있는 text block은 동일 문자열 비교 대상이 아니며 shell block을 비교했다. 기존 v1 보고서 골든은 바꾸지 않았다. Go/Flutter runtime·빌드는 기존 공개 무료 Ubuntu CI에서만 수행했다.
- 후속 검증 기록은 PLAN/GATES/이 gate3개 문서만 바꾸며 코드·계약·workflow·client tree를 유지한다. 같은 소스의 통과 증거를 재사용하고 전체 검사를 반복하지 않는다.

## 남는 경계

실제 시장 데이터 선정·사용권·제공자 정확성·휴일/개정/freshness, 종목 가격 입력·broker 성과, 새 iOS·실기기·수동 스크린 리더·VM 배포는 이 단위의 증거가 아니다. 전체 Goal도 계속 진행 중이다.
