# Omni Folio Execution Plan

상태: G0·G1·G3 로컬 통과, G2 자동 접근성·reduced-motion 통과 및 native profile·screen-reader 증거 보강 중, G4A 키움 K0 합성 계약 통과, G4B local sample OHLCV 수직 슬라이스 통과, G4C K1 credential-free candle 합성 계약 통과, G4D price-adjustment consumer contract 통과, G4E/K2A 내부 합성 Kiwoom 주문 상태 로그 통과, G4F/K2B0 알려진 주문 체결 조정 계약 통과, G4G/K2B1 날짜 지정 체결 스캔 합성 계약 통과, G4H credential-free known-good broker snapshot 영속화·원장 수량 diff 통과

## Now

- [x] 제품·안전 목표와 조사 기준선
- [x] Flutter/Go/Python 및 state-authority ADR
- [x] G0 contracts와 monorepo 실행 명령
- [x] G1 CSV preview → atomic apply → ledger snapshot/receipt
- [ ] G2 동일 fixture를 표시하는 Flutter client와 iOS·Android·web build 완료; semantics·touch target·light/dark contrast·reduced-motion 자동 검증과 chart 포함 Android emulator profile 2회 통과, 수동 screen-reader·physical-device profile 증거 남음
- [x] G3 동일 market fixture를 읽는 deterministic Python backtest와 walk-forward 개선 runner
- [x] local OCI/Compose 정의와 root check/smoke

## Next

- [x] 키움 K0 read-only transport/normalization 합성 계약
- [x] provider-neutral local fixture OHLCV API와 Flutter 종목 상세 price/volume chart·정확한 표·샘플 provenance
- [x] K1 키움 `ka10080`/`ka10081` 합성 candle 계약 검증: KRX 6자리 symbol, 지원 interval, signed price magnitude·exact decimal OHLCV, pagination normalization/dedupe/conflict/cap 확인
- [x] OpenAPI·HTTP·Flutter에 `price_adjustment` 필수 계약 연결: local fixture `unspecified` 고정, provider-adjusted 의미 보수적 표시, 잘못된 값 fail-closed
- [x] G4E/K2A 내부 합성 주문 상태 로그: LIMIT/KRW/KRX, risk verdict ordering, durable unknown submit, 계좌 단위 신규 submit 차단, 알려진 주문 cancel 허용, 현재 schema/backup v3의 주문 로그 복구 증명
- [x] G4F/K2B0 내부 합성 known-order reconciliation: 이미 ACK된 provider order ref만 완전한 execution lookup으로 원자 반영하고 lookup-only `SUBMIT_UNKNOWN` 결합 금지
- [x] G4G/K2B1 내부 합성 `kt00009` 날짜 지정 체결 스캔: terminal pagination, 엄격한 주식/체결 정규화, date/account/environment 별도 alias, naive execution clock과 불완전성 보존
- [x] G4H credential-free known-good broker snapshot: complete KRX raw snapshot과 ledger revision별 reconciliation을 SQLite에 원자 저장, replay/conflict, 종목 수량 diff, 실패 시 이전 known-good 보존, insert-only ledger/broker state와 schema/backup v3 복구 증명
- [ ] 키움 live/mock credential 검증, official timezone/freshness 관찰, credentialed scheduled known-good refresh, credentialed ledger reconciliation
- [ ] 실제 키움 OHLCV를 local chart contract에 연결, 기간 선택·평균단가·체결 marker와 physical accessibility/performance budget
- [ ] K2B 키움 모의주문 broker submit/query transport, credentialed mock 관찰, 안전한 unknown-submit correlation, public route/UI, 실제 risk policy·fencing, 시장가·정정, 체결-원장 reconciliation
- [x] deterministic strategy candidate search와 walk-forward challenger evidence
- [ ] append-only champion registry, paper 성능 evidence와 이전 champion/`no_strategy` rollback
- [ ] strategy/risk/paper runner와 자동 paper/shadow promotion evidence

## Later, only after gates

- [ ] 토스증권 Open API read-only adapter
- [ ] PostgreSQL maintenance migration
- [ ] Kubernetes deployment adapter
- [ ] limited live 자동매매

## Decision map

- 문서 인덱스: [`docs/README.md`](docs/README.md)
- Runtime와 monorepo: [`docs/adr/0001-runtime-and-monorepo.md`](docs/adr/0001-runtime-and-monorepo.md)
- 제품 목표: [`docs/goal-prompt.md`](docs/goal-prompt.md)
- 상세 계획과 review 기록: [`docs/omni-folio-plan.md`](docs/omni-folio-plan.md)
- 브로커 우선순위와 Toss-inspired UX: [`docs/broker-priority-and-ux.md`](docs/broker-priority-and-ux.md)
- Gate 상태: [`GATES.md`](GATES.md)
- 보안 보고와 credential 사고 대응: [`SECURITY.md`](SECURITY.md)
