# Omni Folio Execution Plan

상태: G0·G1 로컬 통과, G3 하위 gate 로컬 통과(G1.6 append-only cash-flow void, G1.7 restore mismatch error redaction, G1.8 exact FX exchange, G1.9 sanitized ledger activity, G1.10 direct FX observation storage, G1.11 direct-FX cash valuation, G1.12 durable security price observation, G1.13 native-currency holding valuation, G1.14 versioned FIFO residual allocation, G3.7 atomic paper halt/rollback safety, G3.8A append-only paper operational evaluation, G3.8B Go-trusted execution-policy contract, G3.8C1 immutable account-global paper accounting session, G3.8C2 SELL·capital-safe paper accounting, G3.8C3 immutable cutoff·mark·equity·return·drawdown evidence, G3.8D strategy-window performance evidence, G3.8E versioned paper performance safety policy·atomic automatic halt/rollback, G3.8F1 scheduled one-shot runner, G3.8F2 DB-leased/fenced always-on local runner 포함), G2 자동 접근성·reduced-motion 통과 및 native profile·screen-reader 증거 보강 중, G4A 키움 K0 합성 계약 통과, G4B local sample OHLCV 수직 슬라이스 통과, G4C K1 credential-free candle 합성 계약 통과, G4D price-adjustment consumer contract 통과, G4E/K2A 내부 합성 주문 상태 로그 통과, G4F/K2B0 알려진 주문 체결 조정 통과, G4G/K2B1 날짜 지정 체결 스캔 통과, G4H credential-free known-good broker snapshot 영속화·원장 수량 diff 통과, G4I/K2C credential-free execution authority 통과, G4J/K2B2 credential-free 키움 mock 지정가 submit transport 계약 통과, G4K 저장된 증권사-원장 보유수량 대조 read model·Flutter 연결 화면 통과, G4L 무결성 검증된 로컬 주문 lifecycle read API·Flutter 연결 화면 통과, G4M 저장 대조 홈 신뢰 요약·연결 상세 이동 통과, G4N 로컬 주문 pending action DTO·홈 안전 경고·연결 상세 이동 통과, G4O local daily chart 표시 범위 선택 통과, G4P 첫 실행 empty snapshot의 거래 내역 가져오기 복구 경로 통과, G4Q credential-free 키움 최신 1틱 체결 정규화 통과, G4R credential-free 키움 0B 실시간 가격 프레임 계약 통과, G4S credential-free 키움 최신 체결 durable price observation 저장 계약 통과, G4T credential-free 키움 최신 체결 one-shot capture 통과, G4U owner-declared 상장 소유권 원장·키움 수집/저장 강제 통과, G4V/K2B3 provider/mode-bound LIMIT dispatch capability port 통과

## Now

- [x] 제품·안전 목표와 조사 기준선
- [x] Flutter/Go/Python 및 state-authority ADR
- [x] G0 contracts와 monorepo 실행 명령
- [x] G1 CSV preview → atomic apply → ledger snapshot/receipt, exact cash-flow·split·두 통화 환전 replay, append-only `CASH_VOID`, direct FX observation, cash-only direct-FX valuation, durable security price observation, internal native-currency holding valuation, current KRW whole-portfolio valuation과 versioned FIFO residual allocation, owner-declared instrument listing, paper operational evaluation, schema v22/backup v16 및 supported legacy owned-copy migration/restore proof
- [ ] G2 동일 fixture를 표시하는 Flutter client와 iOS·Android·web build 완료; 실제 import API 기반 demo seed, 모바일 bottom navigation·desktop rail, bounded Overview·lazy Holdings, provenance count·signed PnL·단일 actionable semantics를 포함한 자동 검증 통과, chart 포함 Android emulator profile 2회 통과, 수동 screen-reader·physical-device profile 증거 남음
- [x] G3 동일 market fixture를 읽는 deterministic Python backtest와 walk-forward 개선 runner
- [x] local OCI/Compose 정의와 root check/smoke

## Next

- [x] CSV 파일 선택: UTF-8·1MiB 초안을 기존 preview·명시적 apply에 연결했다. 관련 105개·권한 반례 8개, 정적 분석과 web·Android·iOS 무서명 release를 확인하고 [PR #34](https://github.com/YouSangSon/Omni-Folio/pull/34)를 main `b82944e`에 병합했다. 실제 OS 대화상자·provider·실기기는 별도 미검증 ([근거·한계](gates/g4p-first-run-import-recovery.md#csv-file-selection)).

- [x] G1.19 거래 내역 정렬·페이지 cursor와 현재 평가 cutoff의 소수초 역전 수정. 실제 API 미래 합산·페이지 순서 RED/GREEN과 독립 리뷰 통과 ([gate](gates/g1o-exact-activity-cutoff.md)).
- [x] G1.20 FIFO/snapshot·broker quantity·CASH_VOID의 정확한 UTC 재생과 schema22/backup v16의 명시적 정책 전환. 집중 회귀·독립 리뷰·최종 전체 check(Go core386.630초, Flutter108·Python34·JSON17)·로컬 smoke·소유 자원 정리 통과 ([gate](gates/g1p-chronological-ledger-replay.md)).
- [x] G1.18 현재 평가 release browser QA: 실제 KRW 입금 합계·503 정상값 보존·USD 환율 누락 후 합계 제거·375/768/1440 반응형·키보드 재조회를 검증했다. 오류/평가 불가 카드의 중복 semantics와 새 테스트 handle 해제 순서를 교정했고 Flutter 108개·Go 전체 재사용·Python 34개·JSON 17개·수정 후 release 및 owned cleanup을 통과했다. 물리 screen reader와 시각 baseline 비교는 별도 ([gate](gates/g1n-current-valuation-browser.md)).
- [x] 테스트 시작/정리 preflight: 관측 실패·빈 값의 사전 차단 40개 하위 사례, 실제 smoke build failure 자손 누수 RED/GREEN, 성공·실패·SIGINT·SIGTERM 11개 process-tree matrix와 하네스 중단 정리를 통과했다. 최종 `make check`·실제 `make smoke` 종료 후 소유 listener·binary·temp root 없음을 확인. SIGKILL·host crash guardian 복구는 별도 ([gate](gates/g0-resource-preflight.md)).
- [x] G1.16 거래 내역 전체 탐색: revision-bound cursor의 50건 단위 이전/다음 페이지, 첫 snapshot·cursor trail 보존, import/refresh 원자 교체, 양방향 nanosecond 경계·cursor loop, 오류·중복 클릭·dispose·스크롤·접근성을 Flutter 13개와 전체 98개에서 검증. 실제 release Chrome에서 101→102건 스냅샷, 3페이지 왕복, 400 오류 보존·키보드 복구·외부 요청 0개와 소유 자원 정리 통과 ([gate](gates/g1l-ledger-activity-pagination.md)).
- [x] G1.17 현재 KRW 전체 평가: replay-verified 원장·현금·보유자산·local fixture 가격·직접 FX를 서버 cutoff 하나와 read transaction 하나에서 exact 합산하고, 누락·stale·모호 입력은 모든 aggregate를 숨긴다. query 없는 closed API, Flutter optional known-good 소비, revision mismatch·sample/broker 비권위·320px/200% semantics를 검증한다 ([gate](gates/g1m-current-portfolio-valuation.md)).
- [x] G3.8H 브라우저 통합 확인: 실제 CLI로 저장한 정책을 Flutter release/Chrome에서 조회하고 375·768·1440 화면, 오류 후 원래 관찰 보존, 키보드 재조회·복귀와 owned cleanup 확인. 실행 불가 계좌 예제를 고치고 실제 초기화 명령 RED/GREEN·race 검증. baseline 비교·물리 screen reader·frame budget은 별도 ([gate](gates/g3ad-paper-monitor-browser.md)).
- [x] G2 로컬 한글 폰트 전달: upstream 고정 Noto Sans KR·OFL과 Cupertino 아이콘을 번들에 포함하고 기본/명시적 버튼 테마의 폰트 누락을 RED/GREEN으로 수정. 새 release 브라우저에서 loopback 외 요청을 차단하고 Home·Import·Connections·monitor 및 외부 요청 0건 검증 ([gate](gates/g2d-local-fonts.md)).
- [x] G2 웹 초기 전송 관측: 원본 폰트를 유지한 release 빌드와 API·DB 없는 단일 정적 페이지에서 본문 크기·요청 완료·첫 프레임 신호를 기록하고 소유 자원을 정리했다. 한 번의 loopback 관측이며 초기 glyph 표시·성능 예산 통과가 아니다 ([근거](gates/g2d-local-fonts.md#web-startup-observation-2026-10-25)).
- [ ] G2 폰트 후속: 실제 전송 조건에서의 초기 glyph 표시·예산, 물리 iOS·Android glyph·profile과 웹 origin 자체가 없는 offline bootstrap은 별도 검증. 단일 로컬 관측을 p95·완전한 offline PWA로 승격하지 않음.
- [x] G2 Android release 네트워크 권한: 실제 병합 manifest의 누락 RED 후 공통 manifest에 `INTERNET` 한 줄 추가. ARM64 APK 권한 차이·실제 native API 조회·등록 보고서·연결 실패 보존/복구·다크 화면과 소유 AVD/프로세스 정리 검증. 실기기·iOS 최신 빌드·스토어 계측 격리는 별도 ([근거](gates/g2-client.md#android-release-networking-2026-10-25)).

- [x] G2 iOS 무서명 포장: immutable app 사본에서 SDK 테스트 플러그인만 제거하고 운영 lock·원본 source·native 바이너리·출력 hash를 확인. unit/fake 11개와 실제 `-jobs 1` 빌드 1회 및 소유 자원 정리 통과. 서명·실기기·스토어·실제 SIGINT는 별도 ([근거](gates/g2-client.md#ios-release-packaging-2026-10-25)).

- [ ] my-cloud 비공개 검증: Compose 자원·로그 예산 정규화 검사 후 실제 VM의 공통 잠금·여유 자원·청구 트래픽과 런타임 적용을 확인한다. 현재 SSH는 TCP22 timeout, 제공사 청구 조회는 로그인 대기다. 신규 이미지·대용량 전송은 보류 ([조건](gates/g6-deployment.md#single-node-resource-budget)).

- [x] 키움 K0 read-only transport/normalization 합성 계약
- [x] provider-neutral local fixture OHLCV API와 Flutter 종목 상세 price/volume chart·정확한 표·샘플 provenance
- [x] K1 키움 `ka10080`/`ka10081` 합성 candle 계약 검증: KRX 6자리 symbol, 지원 interval, signed price magnitude·exact decimal OHLCV, pagination normalization/dedupe/conflict/cap 확인
- [x] OpenAPI·HTTP·Flutter에 `price_adjustment` 필수 계약 연결: local fixture `unspecified` 고정, provider-adjusted 의미 보수적 표시, 잘못된 값 fail-closed
- [x] G4E/K2A 내부 합성 주문 상태 로그: LIMIT/KRW/KRX, risk verdict ordering, durable unknown submit, 계좌 단위 신규 submit 차단, 알려진 주문 cancel 허용, 당시 schema v10/backup v6의 주문 로그 복구 증명
- [x] G4F/K2B0 내부 합성 known-order reconciliation: 이미 ACK된 provider order ref만 완전한 execution lookup으로 원자 반영하고 lookup-only `SUBMIT_UNKNOWN` 결합 금지
- [x] G4G/K2B1 내부 합성 `kt00009` 날짜 지정 체결 스캔: terminal pagination, 엄격한 주식/체결 정규화, date/account/environment 별도 alias, naive execution clock과 불완전성 보존
- [x] G4H credential-free known-good broker snapshot: complete KRX raw snapshot과 ledger revision별 reconciliation을 SQLite에 원자 저장, replay/conflict, 종목 수량 diff, 실패 시 이전 known-good 보존, insert-only ledger/broker state와 schema v10/backup v6 복구 증명
- [x] G4I/K2C 내부 합성 execution authority: default-off kill switch, process owner, 30초 lease/fencing, 고정 BUY 한도, immutable reservation과 승인/dispatch 원자성, DB 우회 차단, backup v6 복구 증명
- [x] G4J/K2B2 credential-free 키움 mock LIMIT BUY submit transport: token preflight, durable dispatch-before-write, write 무재시도, snapshot-compatible opaque ACK, definitive reject와 `SUBMIT_UNKNOWN` 보존
- [x] G4K credential-free stored reconciliation read view: 최신 raw snapshot의 canonical hash·metadata와 ledger-revision 수량 diff를 fail-closed 재검증하고 account/internal ID/hash/raw snapshot을 제거한 `freshness=unverified` HTTP DTO와 Flutter loading/empty/error/retry/일치·불일치 text semantics를 검증
- [x] G4L credential-free local order lifecycle read view: 기존 주문 recovery proof로 전체 append-only log를 fail-closed 재검증하고 account/client/provider/internal ID를 제거한 `broker_freshness=unverified` HTTP DTO와 Flutter loading/empty/error/retry/known-good 유지·200% text·unknown 재주문 금지 semantics를 검증
- [x] G4M Flutter overview reconciliation trust summary: G4K 저장 결과를 공유해 현재 상태가 아님을 유지하면서 일치·불일치 수와 수집·저장 시각을 홈에 요약하고 기존 연결 상세로 이동하며 375px·200% text semantics를 검증
- [x] G4N local order pending-action and overview warning contract: G4L DTO가 `pending_action=SUBMIT|CANCEL|none`을 필수로 노출해 `FILLED` 상태에 남은 cancel 결과 대기를 숨기지 않고, Flutter가 연결 상세와 홈에서 현재 broker 상태가 아닌 미확정 건수·추가 조작 금지 문구를 표시하며 account/client/provider/internal ID를 계속 제거
- [x] G4O local daily chart display range: 마지막 수신 봉을 기준으로 `30일/90일/365일/전체`를 client-side에서 선택하고 차트·semantics·정확한 OHLCV 표가 같은 범위를 사용하며 sample/stale/price-adjustment provenance를 유지
- [x] G4W 차트 원천 종목 ID·통화·시간 의미 공개, 확장 fixture의 명시적 identity 검증과 선택 종목 불일치 차단; legacy unknown·이전 정상 자료 유지. 평균단가·마커 완료는 아래 후속 gate로 유지
- [x] G4X 종목 원장 상세: 한 revision의 잔여 FIFO 평균원가·원본 BUY/SELL 50건, 시세 실패와 독립 진입, 원본 시각·ID 및 실패 시 이전 결과 보존. 전체 check·smoke·release browser 통과 ([gate](gates/g4x-instrument-ledger-detail.md))
- [x] G1.21 중복만 가져온 뒤 cash/holding/current valuation의 잘못된 시각 동일성 비교를 교정. 기존 metadata 응답·cutoff와 전체 proof를 유지하며 현재 revision의 모든 event 기록 시각을 나노초 기준으로 검사. 전체 check·smoke와 merged main의 실제 375px 중복 import → 평가·원장 refresh 통과 ([gate](gates/g1q-valuation-metadata-cutoff.md))
- [x] G2 responsive shell: 900px 경계 전환 시 CSV·선택·focus·preview·오류·진행 중 apply·receipt 보존. 양방향 회귀와 Flutter122 검사, 실제 375↔1280px release 흐름 확인 ([근거](gates/g2-client.md#responsive-import-state-2026-10-23))
- [x] G2 import 오류 문구: 실제 미리보기가 남아 있을 때만 보존 안내. 첫 실패·입력 무효화·보존 실패 회귀와 release 화면 확인 ([근거](gates/g2-client.md#preview-error-state-2026-10-23))
- [x] G4P first-run import recovery: 실제 core의 `never_verified`와 빈 cash/holdings/realized PnL snapshot 조합을 홈 empty state로 처리해 기존 거래 내역 가져오기 경로로 연결하고 320px·200% text·48dp·semantics를 검증
- [x] G4Q credential-free Kiwoom latest trade: 공식 `ka10079` 1틱 응답의 가격과 provider 체결 시각을 기존 read transport로 정규화하고 잘못된 가격·시간 순서·같은 초의 다른 가격·미래 체결을 fail-closed; durable source identity·price-adjustment·persistence는 추측하지 않음
- [x] G4R credential-free Kiwoom realtime price: 공식 `0B` 단일 KRX-format symbol 등록 packet과 bounded `REAL` frame의 가격·naive provider clock·로컬 receive 시각을 stdlib로 정규화하고 frame 내부 중복/모호성을 fail-closed; venue를 주장하지 않고 WebSocket dependency·network loop·날짜/identity 추론은 추가하지 않음
- [x] G4S credential-free Kiwoom durable latest-trade observation: `ka10079` 최신 체결 DTO를 `kiwoom_mock`/`kiwoom_production` 관측 슬롯으로 schema v12 append-only price series에 저장하고 같은 초 다른 가격·tampered ID·비 KRX/KRW identity를 fail-closed; 기존 holding valuation/public route는 계속 `local_fixture`만 사용
- [x] G4T credential-free Kiwoom one-shot latest-trade capture: 당시에는 잘못된 service/client/symbol을 네트워크 전에 거절하고 `instrument_<symbol>` 관례로 G4Q read와 G4S append를 연결했으며 provider 실패·같은 슬롯 가격 충돌에도 이전 복구 증거를 보존; instrument authority는 아래 G4U가 owner-declared registry로 대체
- [x] G4U owner-declared listing ownership: schema v13의 `DECLARE`/`REVOKE` insert-only 이벤트 원장이 `(venue,symbol,currency)`의 현재 instrument를 명시적으로 소유하고 backup v8이 원장 hash·event/active count를 검증; G4T는 미등록·철회 종목을 네트워크 전에 차단하고 G4S는 저장 transaction 안에서 다시 확인해 수집 중 철회도 저장하지 않으며, v12 Kiwoom 가격은 보존하되 소유권을 추론하지 않음
- [x] G4V/K2B3 provider/mode-bound credential-free LIMIT dispatch capability port: application service에서 Kiwoom client·환경·token·provider error 의존을 제거하고 zero/mismatch를 preflight 전에 차단하면서 durable dispatch-before-write, one-shot write, definitive reject·conservative unknown과 redaction을 유지
- [x] G1.6 append-only cash-flow correction: 같은 계좌·통화의 기존 입출금·배당·수수료·세금을 exact 반전하는 `CASH_VOID`, 원본 provenance 보존, 중복/chain/trade target 차단, schema v8/backup v5 restore와 Flutter 보존 disclosure
- [x] G1.7 restore mismatch error redaction: golden snapshot 불일치는 fail-closed하되 반환 오류와 CLI log에서 cash·holding·실현손익·event/receipt provenance를 제거하고 회귀 테스트로 고정
- [x] G1.8 exact FX exchange: 하나의 `FX_EXCHANGE`에 매도 통화·음수 금액과 다른 매수 통화·양수 금액을 보존, schema v9/backup v5와 v8 임시-copy migration restore 검증, Flutter 양쪽 leg disclosure
- [x] G1.9 sanitized ledger activity: 전체 원장 replay/canonical 검증 뒤 revision-bound keyset cursor로 최신 local event를 익명화해 조회하고 Flutter 내역 탭에서 현재 증권사 상태가 아님을 명시해 표시
- [x] G1.10 direct FX observation storage: 방향·source·observed/fetched/recorded 시각·canonical row hash를 가진 local fixture 관측값을 schema v10 insert-only series로 저장하고 exact-pair/as-of GET, backup v6 digest/count, legacy v8/v9 copy migration을 검증
- [x] G1.11 direct-FX cash valuation: replay-verified 현재 원장 cash를 explicit as-of·24시간 정책으로 exact direct pair에만 평가하고 missing/stale pair의 aggregate를 숨기며 sample/stale provenance와 기존 whole-portfolio unavailable 경계를 유지
- [x] G1.12 durable security price observation: local fixture 종목 가격을 exact identity·canonical hash·세 시각과 함께 schema v11 insert-only series로 보존하고 internal as-of·backup v7 digest/count·legacy v10 owned-copy restore를 검증하며 whole-portfolio unavailable 경계를 유지
- [x] G1.13 native-currency holding valuation: 같은 read transaction에서 원장과 전체 가격 series를 검증하고 unique as-of venue의 24시간 이내 local fixture만 사용해 원통화 시장가·미실현손익·통화별 합계를 exact 계산하며 누락·모호·stale이면 aggregate를 숨김
- [x] G1.14 versioned FIFO cost allocation: 기존 유한 decimal 배분은 exact 보존하고 반복소수인 부분 lot만 `fifo_exact_else_half_even_residual_8_v1`로 half-even 양자화; 잔여 원가는 lot에 남겨 최종 청산 시 exact 소비하며 snapshot/OpenAPI/Flutter와 legacy golden restore 호환성을 검증
- [x] G1.15 저장 가격 보유자산 평가 read view: internal ID를 제외한 GET API와 독립 Flutter 상세에서 같은 원장 revision의 수량·원가·원통화 평가·가격 provenance를 보여주고 sample/stale, 누락 시 합계 숨김, refresh 실패 시 알려진 정상 결과 보존을 검증 ([gate](gates/g1k-holding-valuation-view.md))
- [ ] 원장 후속: historical-FX cost basis·performance valuation, 사용자 선택 기준통화와 표시/통화별 rounding 정책, provider FX/source priority·calendar 정책, Kiwoom price observation을 valuation authority로 승격할 freshness 정책, FX/거래·분할·기업행사 정정, 배당 재투자·국가별 세금 분류와 credentialed broker 체결/현금 reconciliation
- [ ] 키움 live/mock credential 검증, official timezone/freshness와 REST tick/0B identity·order 관찰, WebSocket LOGIN/PING/reconnect/resubscribe/backpressure, credentialed scheduled known-good refresh, credentialed ledger reconciliation ([실제 연결 재개 조건](gates/g4r-kiwoom-realtime-price.md#realtime-connection-readiness-2026-10-25)).
- [ ] 실제 키움 OHLCV를 local chart contract에 연결, G4X 원장 조회와 비교할 봉 시간 구간·가격 조정 기준 및 실제 체결 timestamp 계약을 정의한 뒤 marker를 추가하고 physical accessibility/performance budget을 증명
- [ ] K2B 후속: credentialed mock 관찰, submit 조회·안전한 unknown-submit correlation, SELL 정책, public 주문 mutation/확인 UI, production risk·broker-coupled runner fencing, 시장가·정정, 체결-원장 reconciliation
- [x] deterministic strategy candidate search와 walk-forward challenger evidence
- [x] Go/SQLite append-only research evidence·paper-candidate selection registry, stale 선택 차단, 직전 선택/`no_strategy` 수동 rollback, 현재 schema v14/backup v9 복구 증명
- [x] G3.5 strategy-bound order authority: 선택 result SHA와 exact selection event를 intent에 보존하고 신규 record와 durable dispatch에서 현재 registry 상태를 fail-closed 재검증
- [x] G3.6 credential-free paper execution foundation: `paper-signal.v2` 목표 수량을 체결+미완결 BUY에 원자 netting해 Go가 delta OrderIntent를 만들고 K2C risk·공통 주문 상태 머신·결정적 부분/완전 체결·동시 재실행·현재 schema v13/backup v8 복구를 검증하며 Kiwoom transport 진입을 차단
- [x] G3.7 atomic paper halt/rollback safety: 신규 paper intent 기록·K2C 승인·durable dispatch를 현재 process lease·exact fencing 검증과 한 transaction으로 묶고, 수동 strategy rollback은 모든 활성 execution authority를 fencing halt한 뒤 같은 transaction에 rollback event를 append
- [x] G3.8A append-only paper operational evaluation: 전체 주문·전략 replay에서 현재 account/selection의 완료 표본과 미확정 action만 Go가 직접 분류하고 schema v14/backup v9에 immutable evidence로 보존; caller 성과 입력과 자동 authority mutation은 차단
- [x] G3.8B Go-trusted execution-policy contract: Go registry·복구 공통 디코더가 연구 산출물의 exact execution fields, canonical decimal, 범위와 close-signal/next-open-fill 의미를 Python과 독립적으로 검증하며 공개 result schema도 같은 구조를 강제
- [x] G3.8C1 immutable paper accounting session: 계좌별 최초 선택 artifact에서만 starting cash와 전체 execution policy를 파생해 schema v15/backup v10의 insert-only 복구 증명으로 보존; strategy 변경은 reset하지 않고 legacy paper order는 replay-only·uncapitalized로 유지
- [x] G3.8C2 ex-post closed-bar open·persisted sequence cutoff에 묶인 target reduction/SELL, KRX whole-share participation, fixed per-fill fee·SELL tax·adverse slippage, account-global session policy equality, one-active-order ceiling, current lease/fence, sole `FILL_RECORDED` journal의 replay-derived cash/FIFO/PnL, oversell/overdraft 및 schema v17/backup v11 복구 보호
- [x] G3.8C2-R1 동작 보존형 domain 경계: exact decimal/FIFO shared kernel과 순수 paper fill·cash·FIFO·PnL `paperdomain`을 분리하고, 잘못된 직접 fill은 상태 무변경으로 fail-closed하며 SQL·lease·provenance·recovery orchestration은 application에 유지
- [x] G3.8C3 immutable order/price cutoff, complete marks, account-global equity, returns, drawdown recovery evidence
- [x] G3.8D current-selection strategy-window performance evidence, schema v19/backup v13 recovery proof, v12 owned-copy empty-D migration
- [x] G3.8E fixed v1 local paper performance policy, schema v20/backup v14 recovery proof, deterministic all-armed halt와 exact one-pop rollback의 atomic automatic provenance, v13 owned-copy empty-policy migration
- [x] G3.8F1 scheduled one-shot paper evaluation/action runner: `paper-run-due` CLI가 최신 available local fixture close를 C3/D/E로 닫고 retry·두 owner 동시 실행을 idempotent journal로 수렴시키며 완료 chain retry 전 paper 성과 정책 root recovery를 다시 증명
- [x] G3.8F2 DB-leased/fenced always-on local scheduler: 현재 전역 strategy selection에 맞춘 단일 active runner, 10초 heartbeat/30초 TTL, exact stage fencing, stale-owner 회수와 success/failure/SIGINT/SIGTERM cleanup proof
- [x] G3.8F3 성과 전용 scheduler가 현재 선택의 최신 저장 C3에서 미완료 D/E를 먼저 완료하고 다음 호출에서 최신 close로 진행. 누적 손실 중지·HOLD 진행·재시도·이전 선택 경계, focused race·전체 check·smoke·소유 자원 정리 검증 ([gate](gates/g3ae-paper-pending-policy-recovery.md))
- [ ] G3.8F 이후 alerting, shadow promotion evidence, CronJob packaging과 broker/live authority는 별도 gate
- [x] G3.8G1 기존 SMA 판정을 공유하는 offline paper target proposal CLI와 closed contract·입력 provenance 검증; 비신뢰 제안이며 연구 증명·주문 권한 아님 ([gate](gates/g3q-paper-signal-proposals.md))
- [ ] G3.8G2 proposal의 Go 검증·선택 binding, session/bar ingress, 순차 fill→policy→signal 실행과 process-owned paper authority·lease cleanup 및 지속 제안 생성·수집·실행 연결; 로컬 하위 gate와 상시 운영·소스 전달 검증을 구분
  - [x] G3.8G2A stored-series 독립 SMA 판정·Go receipt deadline과 proposal→v3 signal→OPEN paper order의 단일 transaction admission ([gate](gates/g3r-paper-proposal-admission.md))
  - [x] G3.8G2B 명시적 초기화·CSV/연구 원본 검증·fill→policy→signal 수동 CLI와 FIFO 거절·누락 봉 부분체결·정책중지 재시작 검증. 실제 실행 파일의 SIGINT/SIGTERM 종료, SIGKILL 후 즉시 재시작 차단·실제 TTL 만료 후 중복 없는 복구 및 소유 자원 정리까지 로컬 통과; 상시 실행을 뜻하지 않음 ([gate](gates/g3s-local-paper-workflow.md), [사용법](docs/local-paper-workflow.md))
  - [x] G3.8G2C 기존 Python 생성기의 `--watch` NDJSON 제안 스트림; 반복 입력·역행/재작성·입출력 실패·OS signal과 전체 check 로컬 검증. 정확한 CSV byte 전달과 durable Go 소비·지속 실행 lease는 별도 후속이며 자동 rearm하지 않음 ([gate](gates/g3t-paper-proposal-watch.md))
  - [x] G3.8G2D Python `--bundle`의 정확한 CSV byte 전달과 Go 수동 `paper-execute -bundle` 연결; 경로 교체·중복 재시도·잘못된 입력·FIFO·전체 check 로컬 통과. 지속 소비·lease 갱신·상시 실행은 별도 후속 ([gate](gates/g3u-paper-input-bundle.md))
  - [x] G3.8G2E 현재 owner의 execution/global lease 원자 갱신과 수동 단계 연결; 최초 TTL 이후 실제 체결·동시 갱신·rollback·backup/restore·전체 check 및 최종 targeted race 로컬 통과. 지속 입력 소비·idle heartbeat와 장기 처리량 검증은 별도 후속 ([gate](gates/g3v-paper-execution-heartbeat.md))
  - [x] G3.8G2F pipe NDJSON 연속 소비와 idle 원자 갱신, 최초 1회 arm 및 reader/생산자 종료 연결; 실제 signal·30초 이후 갱신·외부 halt·재연결 재시도와 전체 check 로컬 통과. 장기 운영 부하·소스 누락 방지·durable 전달은 별도 후속 ([gate](gates/g3w-paper-input-stream.md))
  - [x] G3.8G2G 실제 Python watch→Go pipe에서 새 CSV·체결·정책 halt와 양쪽 프로세스 종료·명시적 재연결, 별도 context 복구를 검증하고 취소 원인 redaction 오류를 교정. focused race·전체 check 로컬 통과; 장기 부하·소스 완결성은 미검증 ([gate](gates/g3x-paper-pipeline.md))
  - [x] G3.8G2H 실제 1,000회 갱신 이력의 구간별 갱신/복구 p50·p95·p99와 런타임 기준값을 보존. 논리 lease clock과 실제 작업 시간을 구분하며 장기 운영·성능 개선으로 주장하지 않음 ([gate](gates/g3y-paper-history-profile.md))
  - [x] G3.8G2I 같은 transaction의 전체 검증 뒤 중복 계좌 이력 읽기를 최신 레코드 조회로 축소하고 migration 조기 반환 우회를 공통 경계에서 차단. 과거 손상·표시 변조 RED/GREEN, focused race·전체 check 통과; 마지막 구간 갱신 p95 28.39→19.99ms의 로컬 비교이며 장기 운영은 미검증 ([gate](gates/g3z-paper-renewal-replay.md))
  - [x] G3.8G2J 실제 Python→Go 연결을 122.029초 실행해 12개 입력의 durable 처리·13개 정책·11회 갱신과 종료 후 전체 복구·소유 리소스 반환을 검증. 120회 관찰에서 최소 lease 여유 12.005초이며 하루 운영·관찰 사이 무중단 보증은 아님 ([gate](gates/g3aa-paper-real-clock-soak.md))
  - [x] G3.8G2K 누락 봉의 체결·성과·안전정책을 봉별로 연결해 중간 손실 이후 체결을 차단. RED/GREEN, D/E 재시작·selection 경계, 실제 signal 복구, opt-in soak 포함 전체 check·focused race·소유 자원 정리와 리뷰 통과. 수동 성과 전용 명령의 조합·기존 과거 누락 소급 복구 및 전체 G3.8G2 완료는 아님 ([gate](gates/g3ab-paper-chronological-recovery.md))
  - [x] G3.8G2L 선택적 `--bundle --capture-dir`로 관찰한 정확한 입력을 stdout 전에 보관하고 기존 Go 명령으로 명시적 재실행. 동시 생성·저장 실패·출력 단절·원본 교체·재시도와 Go 전체/Python39·리뷰·소유 자원 정리 통과. 처리 완료 ACK·자동 재실행·소스 완결성·freshness는 보장하지 않음 ([gate](gates/g3af-paper-input-capture.md))
- [ ] strategy/risk/paper runner와 자동 paper/shadow promotion evidence
- [x] G3.8H 로컬 모의 정책 상태 GET·Flutter 조회: 복구 검증된 소유권·미완료 chain·최근 정책만 표시하고 실행/갱신 권한은 만들지 않음. 전체 check, 최종 focused race, Flutter 84·analyze, 계약·보안 검사, 읽기 전용 리뷰와 owned cleanup 통과. 브라우저 실렌더링·물리 기기·알림·상시 운영 증거는 별도 ([gate](gates/g3ac-paper-monitor.md))

- [x] G3.9 저장된 백테스트 보고서: 최신 등록 연구의 GET·Flutter 조회, 지표 원문 정밀도·평가 의미·빈 상태·오류 보존. Go 전체·Flutter135·Python39·계약 검사와 실제 등록→브라우저 조회·offline 보존·키보드·모바일 검증. 지표 재계산·현재 선택 전략 성과·실브로커 준비는 별도 ([gate](gates/g3ag-research-report.md))

## Later, only after gates

- [ ] 토스증권 Open API read-only adapter
- [ ] PostgreSQL maintenance migration
- [ ] G6 통과 후 Kind + Podman 로컬 검증을 포함한 Kubernetes deployment adapter
- [ ] limited live 자동매매

## Decision map

- 문서 인덱스: [`docs/README.md`](docs/README.md)
- Runtime와 monorepo: [`docs/adr/0001-runtime-and-monorepo.md`](docs/adr/0001-runtime-and-monorepo.md)
- 제품 목표와 실행 권한: [`goal.md`](goal.md)
- 상세 계획과 review 기록: [`docs/omni-folio-plan.md`](docs/omni-folio-plan.md)
- 브로커 우선순위와 Toss-inspired UX: [`docs/broker-priority-and-ux.md`](docs/broker-priority-and-ux.md)
- Gate 상태: [`GATES.md`](GATES.md)
- 보안 보고와 credential 사고 대응: [`SECURITY.md`](SECURITY.md)
