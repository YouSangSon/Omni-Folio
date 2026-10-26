# G1.28 직접 입력 종목 가격의 단위와 완전한 버전

원장에 반영한 종목의 직접 입력 가격을 보유평가·현재 KRW 평가·TWR·XIRR·KRW/USD 지수 비교에 연결한다. [G1.27 환율](g1w-owner-fx-series.md)의 완전한 버전·철회·재시도 원칙을 재사용한다. 제공자 인증·자동 수집·실제 투자 성과를 증명하는 단위는 아니다.

## 작은 작동 모델과 선택

주가100→200, 직접 USD→KRW 환율1000→900, USD100 입금 후1주 보유라면 원통화 평가액은USD200, KRW 평가액은180000,365일 TWR/XIRR은80%다. 같은 기간 USD 지수100→110은 native10%·KRW−1%이며 원화 TWR 차이는81%p다. 금액은 exact rational, 공개 수익률은 기존 반올림·수치 정책을 유지한다. 공유 합성 예제의 독립 Fraction 기준값이며 실제 시장 수익률이 아니다.

가격은 수량과 같은 단위여야 곱할 수 있다. `unadjusted`는 관측 순간 원장1주당 가격이며 과거 분할·배당을 소급 조정하지 않았다는 소유자 선언이다. 마지막 분할 시각과 같은 가격은 어느 단위인지 보장하지 못하므로 거절하고1ns 이후부터 허용한다. 제공자의 조정 여부를 검증한 것은 아니다. 다른 가격 파일에도 적용할 확인 기준은 “이 가격의1주와 현재 원장의1주가 같은가?”다.

| 대안 | 선택 근거 |
|---|---|
| 기존 fixture/source 허용값 확대 | 기존 row hash·조회·Kiwoom 계약에 수정·권한 의미를 섞게 됨 |
| 별도 전체 버전 journal | 기존 계약을 유지하며 최신 append 하나로 수정·철회·재시도를 정의 가능 |
| 자동 provider 수집 | 실제 가격 단위·기업행사·사용권·개정·휴일·실패 정책을 먼저 확인해야 함 |

별도 journal을 선택했다. 기존 지수/환율의 닫힌 JSON·CLI 파일 경계·SQLite 복구 패턴과 표준 라이브러리를 사용한다. 새 의존성·공개 mutation API·provider 요청·스케줄러를 추가하지 않는다. 입력 버전의 venue는 별도 instrument listing이나 Kiwoom 권한을 만들지 않는다.

Portfolio Performance의 [historical prices](https://help.portfolio-performance.info/en/concepts/historical-prices/)는 latest/close/adjusted 가격과 평가에 필요한 가격·환율을 구분하고, [CSV import](https://help.portfolio-performance.info/en/reference/file/import/csv-import/)는 거래 가격과 별도의 historical quote 입력을 설명한다. 이 개념만 참고했다. PP의 종가 관례를 우리 exact observed-time 정책과 같다고 주장하지 않으며 코드·실제 시장 자료는 가져오지 않았다. 자료 선정/사용권은 [비교 gate](g1v-portfolio-benchmark-comparison.md)의 미확인 범위를 유지한다.

## 입력·보관·복구 계약

- `owner-price-import -db <existing.db> -input <regular.json>`은 literal DB path와 bounded regular file만 허용한다. 없는 DB·FIFO·symlink·디렉터리·추가 인자·외부 source 옵션을 거절한다. 취소·commit 후 출력 실패·같은 입력 재시도를 보존하고 공개 DB 오류에 경로·내부 오류를 넣지 않는다.
- [owner-price-series.v1](../contracts/owner-price-series.schema.json)는 root10필드/point2필드, raw/canonical 각1MiB,0..10000점, 안전한128 UTF-8 byte identity/version, 대문자 symbol/venue·3자 currency, 양수 canonical decimal≤64자, 정렬된 고유 UTC nanosecond 시각을 요구한다. unknown/duplicate/escaped-duplicate/null/비정상 surrogate·trailing JSON은 거절한다.
- `instrument_id=instrument_+lower(symbol)`인 canonical ID와 이미 검증·반영된 원장의 `(instrument_id,currency)` 소속을 admission 및 전체 recovery에서 확인한다. 매도 완료 종목도 입력할 수 있다. legacy 표시명의 대소문자는 바꾸지 않고 owner 가격 선택만 canonical ID로 연결한다. fixture의 기존 exact symbol 선택은 유지한다.
- `observed_at ≤ fetched_at ≤ recorded_at`; 기록 clock은 서버 소유이며 journal 전체에서 역행하지 않는다. 같은 종목·통화·버전은 payload/sample/fetched 변경이 충돌이고 exact retry는 원래 receipt를 반환하며 clock/append를 호출하지 않는다.
- 보고서 cutoff까지 알려진 최신 append를 종목·통화별로 한 번 선택하고 모든 과거 경계도 같은 whole version만 쓴다. 새 버전은 전체 교체, 빈 점은 철회다. 빠진 점을 이전 버전으로 채우거나 옛 파일 재시도로 재활성화하지 않는다.
- 같은 종목·통화에는 어느 venue에서도 기존 fixture/Kiwoom row와 owner 버전을 섞지 못한다. 빈/미래 버전도 출처를 고정하며 철회로 전환 권한을 만들지 않는다. 양방향 SQL guard와 전체 journal proof를 함께 확인한다.
- schema25의 STRICT/insert-only journal은 canonical payload·hash·연속 sequence·metadata·시각·원장 소속을 증명한다. exact DDL/index/trigger와 TEMP 포함 auxiliary inventory 및 기존 가격 schema도 확인한다. backup19는 SHA/version_count/point_count와 후보 receipt check/SHA/count7필드를 원본·복사본·복구본에서 비교하며 missing/null/변조를 거절한다.
- backup18/schema24의 owner FX와 v17/v16 chronological 비보관 권한을 보존한다. source의 당시 proof와 lease/replay를 검증한 뒤 owned copy에 빈 새 journal만 만든다. old format은 새 proof 또는 source의 사전 생성 owner journal을 주장할 수 없다. v15 이하 archival 경계와 원본 bytes를 보존한다.

## 계산·공개·화면

- 별도 가격 검증 context에 실제 read transaction/cutoff를 묶어 current/TWR/benchmark가 공유한다. native holding은 FX proof를 요구하지 않는다. 기존 ledger/FX/price 복구 경계와 report clock을 유지한다.
- 공개 owner ID는 `owner_price_<sequence>_<point>`지만 legacy ID와 같은 문자열일 수 있으므로 내부 시각 map은 owner에만 NUL namespace를 쓴다. legacy ID는 NUL을 금지하며 공개 HTTP whitelist에는 내부 ID가 없다.
- owner 가격만 현재 보유평가에서 마지막 canonical SPLIT보다 엄격히 늦어야 한다. TWR은 기존 모든 source의 과거 경계별 split guard를 유지한다. 실패해도 선택된 source/version/sample/mark는 남고 aggregate는 없다.24시간 초과 여부는 mark의 stale 상태로 별도 표시한다.
- 실제 선택한 owner 가격이 있을 때 current/TWR/XIRR/benchmark는 v3, native holding은 v2다. fixture v1·owner FX만 쓰는 v2는 유지한다. `owner_declared_price`는 비차단 안내이며 sample 및 FX 안내와 독립적이다. XIRR은 수치 판정 후 안내를 더한다. benchmark 최상위 issues는 기존 목록 실패만 소유한다.
- native holding의 owner mark는 source/version/unadjusted/sample/state를 공개한다. 제공자 미검증은 `unknown`, 관측 나이24시간 초과면 `stale`이다. `split_basis` line은 `split_price_unavailable` 사유와 mark를 보존하되 금액/합계를 내지 않는다. 비샘플 owner 가격으로 완전한 경우 새 `complete` 상태를 허용한다.
- Flutter의 strict parser는 source-dependent field set·policy/notice·sample·시간/분할·통화별 합계의 일치를 확인한다. 홈/보유/TWR/XIRR/비교의 `직접 입력 가격 사용 · 시세 제공자가 검증한 값이 아닙니다.`는 예시/환율 안내와 별도다. 누락 입력 건수에 안내를 세지 않고 보유 상세에서 버전·무조정·분할 실패 이유와 이전 전체 정상값을 보존한다. 기존 Material/Noto/semantics를 재사용한다.

## 검증 기록

- 첫 RED head `67e549a294f0017d91695a3186ad9633e8cf1a8e`, [Core40280331004](https://github.com/YouSangSon/Omni-Folio/actions/runs/40280331004)는005930 BUY fixture가 기존 resolver에서 반영되지 않아 CLI 도달 전에 실패했다. 이 결과를 기능 RED로 세지 않는다.
- admitted AAPL/USD와 기존 owner FX로 fixture를 고친 head `e1982c2a5557627e48085114a65122b17310934e`, [Core40280642296](https://github.com/YouSangSon/Omni-Folio/actions/runs/40280642296)는 실제 `unknown command "owner-price-import"`로 실패했다. 두 전체 로그와 입력/원인 수정 근거를 내부 receipt에 보존한다.
- 독립 설계 리뷰의 canonical 소속 recovery·mixed-case 연결·native complete parser·oldv18 FX 권한 지적을 설계/구현에 반영했다. 구현 리뷰에서 unused Go import와 새 complete 상태의 통화별 totals 검사 누락을 발견해 제거/검사 확장과 반례를 추가했다. 독립 최종 정적 리뷰는 추가 차단 결함 없음으로 끝났고, mixed-case 회귀는 소문자 BUY를 경제 시각상 먼저 두어 실제 소문자 line label을 assert하도록 보강했다. 원격 CI는 진행 중이다.
- 첫 구현 `d911ccfa5fcf367bdea0b4dbbebbc18ecf33e625`의 Core40284366067은 집중 검사 통과 후 전체 Go447.572s에서 기존 `TestHoldingValuationHTTPStoredPriceView`의 properties==legacy 필드 수 가정으로 실패했다. owner 전용 `source_version`의 조건부 required/legacy 금지와 기존 fixture 응답의 정확한 required 필드를 함께 검사하도록 보완하고 집중 단계에 포함했다. Client40284366090은 집중40·전체403·분석·web/ARM64·권한/소스 일치를 통과했다. 보강 중간 head9707188의 Core40284897238/Client40284897315는 알려진 실패의 중복 실행을 피하려고 취소했으며 성공으로 세지 않는다. 최종 candidate에는 mixed-case 회귀 보강과 계약 검사 수정이 포함된다. 공유8개 실제 HTTP producer fixture를 Flutter parser와 같이 검사하며 split exact/1ns·과거점 제거·withdraw/retry·namespace·소속·manifest 변조·oldv18 권한·엄격 parser·화면200%/두 theme·semantics cleanup·refresh 보존을 포함한다. 합성 입력 검사이며 실제 provider 정확성의 증거가 아니다.
- 로컬 Go format·pinned Dart31files zero delta·JSON36/local refs·README4언어 shell26blocks/로컬 링크·diff 정적 검사를 통과했다. Go/Flutter runtime·전체 check·web/ARM64 build와 source identity는 기존 공개 무료 Ubuntu CI에서 확인한다. **최종 결과·commit/merge tree는 아직 없음.**

## 남는 경계

실제 시장 파일·사용권·제공자/기업행사 정확성·자동 수집·휴일/개정/freshness, 최신 iOS·실기기·수동 screen reader·VM 배포는 미검증이다. chart OHLCV와 owner 평가가격을 같은 데이터로 간주하지 않는다. 전체 Goal은 계속 진행 중이다.
