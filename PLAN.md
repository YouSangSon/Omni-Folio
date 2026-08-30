# Omni Folio Execution Plan

상태: G0·G1·G3 로컬 통과(G1.6 append-only cash-flow void, G1.7 restore mismatch error redaction, G1.8 exact FX exchange, G1.9 sanitized ledger activity, G1.10 direct FX observation storage, G1.11 direct-FX cash valuation, G1.12 durable security price observation, G1.13 native-currency holding valuation, G1.14 versioned FIFO residual allocation, G3.7 atomic paper halt/rollback safety, G3.8A append-only paper operational evaluation, G3.8B Go-trusted execution-policy contract, G3.8C1 immutable account-global paper accounting session 포함), G2 자동 접근성·reduced-motion 통과 및 native profile·screen-reader 증거 보강 중, G4A 키움 K0 합성 계약 통과, G4B local sample OHLCV 수직 슬라이스 통과, G4C K1 credential-free candle 합성 계약 통과, G4D price-adjustment consumer contract 통과, G4E/K2A 내부 합성 주문 상태 로그 통과, G4F/K2B0 알려진 주문 체결 조정 계약 통과, G4G/K2B1 날짜 지정 체결 스캔 통과, G4H credential-free known-good broker snapshot 영속화·원장 수량 diff 통과, G4I/K2C credential-free execution authority 통과, G4J/K2B2 credential-free 키움 mock 지정가 submit transport 계약 통과, G4K 저장된 증권사-원장 보유수량 대조 read model·Flutter 연결 화면 통과, G4L 무결성 검증된 로컬 주문 lifecycle read API·Flutter 연결 화면 통과, G4M 저장 대조 홈 신뢰 요약·연결 상세 이동 통과, G4N 로컬 주문 pending action DTO·홈 안전 경고·연결 상세 이동 통과, G4O local daily chart 표시 범위 선택 통과, G4P 첫 실행 empty snapshot의 거래 내역 가져오기 복구 경로 통과, G4Q credential-free 키움 최신 1틱 체결 정규화 통과, G4R credential-free 키움 0B 실시간 가격 프레임 계약 통과, G4S credential-free 키움 최신 체결 durable price observation 저장 계약 통과, G4T credential-free 키움 최신 체결 one-shot capture 통과, G4U owner-declared 상장 소유권 원장·키움 수집/저장 강제 통과

## Now

- [x] 제품·안전 목표와 조사 기준선
- [x] Flutter/Go/Python 및 state-authority ADR
- [x] G0 contracts와 monorepo 실행 명령
- [x] G1 CSV preview → atomic apply → ledger snapshot/receipt, exact cash-flow·split·두 통화 환전 replay, append-only `CASH_VOID`, direct FX observation, cash-only direct-FX valuation, durable security price observation, internal native-currency holding valuation와 versioned FIFO residual allocation, owner-declared instrument listing, paper operational evaluation, schema v15/backup v10 및 legacy v8-v14 owned-copy migration/restore proof
- [ ] G2 동일 fixture를 표시하는 Flutter client와 iOS·Android·web build 완료; 실제 import API 기반 demo seed, 모바일 bottom navigation·desktop rail, bounded Overview·lazy Holdings, provenance count·signed PnL·단일 actionable semantics를 포함한 자동 검증 통과, chart 포함 Android emulator profile 2회 통과, 수동 screen-reader·physical-device profile 증거 남음
- [x] G3 동일 market fixture를 읽는 deterministic Python backtest와 walk-forward 개선 runner
- [x] local OCI/Compose 정의와 root check/smoke

## Next

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
- [x] G4P first-run import recovery: 실제 core의 `never_verified`와 빈 cash/holdings/realized PnL snapshot 조합을 홈 empty state로 처리해 기존 거래 내역 가져오기 경로로 연결하고 320px·200% text·48dp·semantics를 검증
- [x] G4Q credential-free Kiwoom latest trade: 공식 `ka10079` 1틱 응답의 가격과 provider 체결 시각을 기존 read transport로 정규화하고 잘못된 가격·시간 순서·같은 초의 다른 가격·미래 체결을 fail-closed; durable source identity·price-adjustment·persistence는 추측하지 않음
- [x] G4R credential-free Kiwoom realtime price: 공식 `0B` 단일 KRX-format symbol 등록 packet과 bounded `REAL` frame의 가격·naive provider clock·로컬 receive 시각을 stdlib로 정규화하고 frame 내부 중복/모호성을 fail-closed; venue를 주장하지 않고 WebSocket dependency·network loop·날짜/identity 추론은 추가하지 않음
- [x] G4S credential-free Kiwoom durable latest-trade observation: `ka10079` 최신 체결 DTO를 `kiwoom_mock`/`kiwoom_production` 관측 슬롯으로 schema v12 append-only price series에 저장하고 같은 초 다른 가격·tampered ID·비 KRX/KRW identity를 fail-closed; 기존 holding valuation/public route는 계속 `local_fixture`만 사용
- [x] G4T credential-free Kiwoom one-shot latest-trade capture: 당시에는 잘못된 service/client/symbol을 네트워크 전에 거절하고 `instrument_<symbol>` 관례로 G4Q read와 G4S append를 연결했으며 provider 실패·같은 슬롯 가격 충돌에도 이전 복구 증거를 보존; instrument authority는 아래 G4U가 owner-declared registry로 대체
- [x] G4U owner-declared listing ownership: schema v13의 `DECLARE`/`REVOKE` insert-only 이벤트 원장이 `(venue,symbol,currency)`의 현재 instrument를 명시적으로 소유하고 backup v8이 원장 hash·event/active count를 검증; G4T는 미등록·철회 종목을 네트워크 전에 차단하고 G4S는 저장 transaction 안에서 다시 확인해 수집 중 철회도 저장하지 않으며, v12 Kiwoom 가격은 보존하되 소유권을 추론하지 않음
- [x] G1.6 append-only cash-flow correction: 같은 계좌·통화의 기존 입출금·배당·수수료·세금을 exact 반전하는 `CASH_VOID`, 원본 provenance 보존, 중복/chain/trade target 차단, schema v8/backup v5 restore와 Flutter 보존 disclosure
- [x] G1.7 restore mismatch error redaction: golden snapshot 불일치는 fail-closed하되 반환 오류와 CLI log에서 cash·holding·실현손익·event/receipt provenance를 제거하고 회귀 테스트로 고정
- [x] G1.8 exact FX exchange: 하나의 `FX_EXCHANGE`에 매도 통화·음수 금액과 다른 매수 통화·양수 금액을 보존, schema v9/backup v5와 v8 임시-copy migration restore 검증, Flutter 양쪽 leg disclosure
- [x] G1.9 sanitized ledger activity: 전체 원장 replay/canonical 검증 뒤 revision-bound keyset cursor로 최신 local event를 익명화해 조회하고 Flutter 내역 탭에서 현재 증권사 상태가 아님을 명시해 표시
- [x] G1.10 direct FX observation storage: 방향·source·observed/fetched/recorded 시각·canonical row hash를 가진 local fixture 관측값을 schema v10 insert-only series로 저장하고 exact-pair/as-of GET, backup v6 digest/count, legacy v8/v9 copy migration을 검증
- [x] G1.11 direct-FX cash valuation: replay-verified 현재 원장 cash를 explicit as-of·24시간 정책으로 exact direct pair에만 평가하고 missing/stale pair의 aggregate를 숨기며 sample/stale provenance와 기존 whole-portfolio unavailable 경계를 유지
- [x] G1.12 durable security price observation: local fixture 종목 가격을 exact identity·canonical hash·세 시각과 함께 schema v11 insert-only series로 보존하고 internal as-of·backup v7 digest/count·legacy v10 owned-copy restore를 검증하며 whole-portfolio unavailable 경계를 유지
- [x] G1.13 native-currency holding valuation: 같은 read transaction에서 원장과 전체 가격 series를 검증하고 unique as-of venue의 24시간 이내 local fixture만 사용해 원통화 시장가·미실현손익·통화별 합계를 exact 계산하며 누락·모호·stale이면 aggregate를 숨김
- [x] G1.14 versioned FIFO cost allocation: 기존 유한 decimal 배분은 exact 보존하고 반복소수인 부분 lot만 `fifo_exact_else_half_even_residual_8_v1`로 half-even 양자화; 잔여 원가는 lot에 남겨 최종 청산 시 exact 소비하며 snapshot/OpenAPI/Flutter와 legacy golden restore 호환성을 검증
- [ ] 원장 후속: public/base-currency whole-portfolio·performance valuation, historical FX와 표시/통화별 rounding 정책, provider FX/source priority·calendar 정책, Kiwoom price observation을 valuation authority로 승격할 서버 cutoff·freshness 정책, FX/거래·분할·기업행사 정정, 배당 재투자·국가별 세금 분류와 credentialed broker 체결/현금 reconciliation
- [ ] 키움 live/mock credential 검증, official timezone/freshness와 REST tick/0B identity·order 관찰, WebSocket LOGIN/PING/reconnect/resubscribe/backpressure, credentialed scheduled known-good refresh, credentialed ledger reconciliation
- [ ] 실제 키움 OHLCV를 local chart contract에 연결, 평균단가의 통화·반올림 계약과 실제 체결 timestamp read model을 먼저 정의한 뒤 marker를 추가하고 physical accessibility/performance budget을 증명
- [ ] K2B 후속: credentialed mock 관찰, submit 조회·안전한 unknown-submit correlation, SELL 정책, public 주문 mutation/확인 UI, production risk·broker-coupled runner fencing, 시장가·정정, 체결-원장 reconciliation
- [x] deterministic strategy candidate search와 walk-forward challenger evidence
- [x] Go/SQLite append-only research evidence·paper-candidate selection registry, stale 선택 차단, 직전 선택/`no_strategy` 수동 rollback, 현재 schema v14/backup v9 복구 증명
- [x] G3.5 strategy-bound order authority: 선택 result SHA와 exact selection event를 intent에 보존하고 신규 record와 durable dispatch에서 현재 registry 상태를 fail-closed 재검증
- [x] G3.6 credential-free paper execution foundation: `paper-signal.v2` 목표 수량을 체결+미완결 BUY에 원자 netting해 Go가 delta OrderIntent를 만들고 K2C risk·공통 주문 상태 머신·결정적 부분/완전 체결·동시 재실행·현재 schema v13/backup v8 복구를 검증하며 Kiwoom transport 진입을 차단
- [x] G3.7 atomic paper halt/rollback safety: 신규 paper intent 기록·K2C 승인·durable dispatch를 현재 process lease·exact fencing 검증과 한 transaction으로 묶고, 수동 strategy rollback은 모든 활성 execution authority를 fencing halt한 뒤 같은 transaction에 rollback event를 append
- [x] G3.8A append-only paper operational evaluation: 전체 주문·전략 replay에서 현재 account/selection의 완료 표본과 미확정 action만 Go가 직접 분류하고 schema v14/backup v9에 immutable evidence로 보존; caller 성과 입력과 자동 authority mutation은 차단
- [x] G3.8B Go-trusted execution-policy contract: Go registry·복구 공통 디코더가 연구 산출물의 exact execution fields, canonical decimal, 범위와 close-signal/next-open-fill 의미를 Python과 독립적으로 검증하며 공개 result schema도 같은 구조를 강제
- [x] G3.8C1 immutable paper accounting session: 계좌별 최초 선택 artifact에서만 starting cash와 전체 execution policy를 파생해 schema v15/backup v10의 insert-only 복구 증명으로 보존; strategy 변경은 reset하지 않고 legacy paper order는 replay-only·uncapitalized로 유지
- [ ] G3.8C2 target reduction/SELL와 eligible-bar fill, fee/tax/slippage, cash/lots, oversell/overdraft protection
- [ ] G3.8C3 immutable order/price cutoff, marks, equity, returns, drawdown
- [ ] G3.8C2/C3 이후 versioned thresholds와 자동 halt/rollback provenance
- [ ] strategy/risk/paper runner와 자동 paper/shadow promotion evidence

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
