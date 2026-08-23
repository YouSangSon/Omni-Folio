# Omni Folio Goal

정식 목표와 제품·안전·아키텍처 계약은 [`docs/goal-prompt.md`](docs/goal-prompt.md)를 따른다. 구현자는 작업을 시작하기 전에 해당 문서와 [`docs/omni-folio-plan.md`](docs/omni-folio-plan.md), [`docs/omni-folio-research-report.md`](docs/omni-folio-research-report.md), [`DESIGN.md`](DESIGN.md)를 읽는다.

## 실행 권한

- 합의된 제품 목표 안의 가역적인 로컬 설계·구현·테스트·문서화·로컬 Git 체크포인트는 추천안을 선택해 재승인 없이 계속 진행한다.
- 구현 중 결함, 누락, 더 단순하거나 안전한 경로를 발견하면 필요한 범위에서 계획과 구현을 함께 개선하고 판단 근거를 기록한다.
- 추측성 기능, 사용되지 않는 추상화, 측정 근거 없는 분산 인프라는 추가하지 않는다.
- 신뢰할 수 있는 오픈소스 프레임워크와 라이브러리가 정확성·성능·개발 속도를 실질적으로 높이면 직접 재구현하지 않고 사용한다. 다만 유지보수 상태, 라이선스, 보안 이력, 버전 고정과 골든 fixture 교차검증을 먼저 통과한다.
- 실제 자금 주문, live 주문 활성화, 외부 배포·push·publish, credential 또는 유료 외부 자원 변경, 파괴적·비가역 작업은 별도 명시 승인을 받는다.

## 현재 권장 경로

Phase A는 asdf로 고정한 Flutter stable 하나로 iOS·Android·app-centric web을 제공하고, Go 모듈러 모놀리스가 ledger/order/risk/broker authority를 소유한다. Python은 research/backtest 전용이며 broker credential이나 주문 제출 권한을 갖지 않는다. SQLite single-writer local에서 `CSV import → preview → idempotent apply → append-only ledger → holdings/cash/P&L → versioned backup/restore` 수직 슬라이스를 먼저 검증한다. 첫 브로커는 키움 REST API이고 `read-only → 차트·실시간 → 모의주문` 순서로 완성한다. 키움 계약이 통과한 뒤 토스증권 Open API를 두 번째 어댑터로 추가한다. 공개 리서치·문서형 콘텐츠나 SEO가 실제 요구될 때만 Next.js를 별도 web surface로 추가하며, Flutter 제품 화면을 React로 중복 구현하지 않는다.

실제 키움 OHLCV를 연결하기 전에는 provider-neutral local fixture로 API·chart·접근성·성능 계약을 검증한다. sample·synthetic·fixture 시세는 API와 화면에서 실시간이 아님을 명시하고 live/current 데이터와 혼합하거나 broker 연동 증거로 승격하지 않는다.

K1은 credential-free synthetic `POST /api/dostk/chart`의 `ka10080`(분봉)·`ka10081`(일봉) 계약을 구현했다. KRX 여섯 자리 symbol과 `1d`, `1/3/5/10/15/30/45/60m`를 받아 signed price magnitude·exact decimal OHLCV·nonnegative volume을 정규화하고, descending page를 UTC ascending으로, identical overlap을 dedupe, conflicting overlap을 거절하며 newest 500개로 제한한다. `upd_stkpc_tp=1`은 내부 `provider_adjusted` provenance일 뿐이다. official candle timestamp timezone이 명시되지 않아 Asia/Seoul을 운영 가정으로 둔다. 이 계약은 credential, broker request, live/current/freshness, public endpoint, persistence, adjustment-event correctness, realtime, reconciliation 또는 order capability를 증명하지 않는다. 공개 route는 계속 `local_fixture`/`sample`/`stale`다.

G4D는 기존 K1 가격 기준이 consumer contract에서 사라지지 않도록 `price_adjustment`를 OpenAPI·HTTP·Flutter에 필수로 연결했다. 공개 local fixture route는 `unspecified`만 허용하고 그 밖의 값은 fail-closed하며, Flutter는 local fixture가 `sample=true`, `state=stale`, non-null `source_as_of`, non-empty issue까지 갖춘 경우만 받는다. 두 가격 기준은 각각 “조정 여부 확인 안 됨”과 “공급자 조정 가격·정확성 미검증”으로 표시한다. 이 단계도 Kiwoom runtime 연결이나 live/freshness 증거가 아니다.

G4E/K2A는 Go 내부의 합성 Kiwoom `LIMIT`/`KRW`/`KRX` 주문 상태 로그를 구현했다. durable client-order idempotency, append-only intent/event, risk verdict 순서, `SUBMIT_UNKNOWN` 재시작 복구, 미확정 command가 있는 계좌의 신규 submit 차단, 알려진 open order의 risk-reducing cancel 허용, fill/cancel/reject replay를 검증한다. 현재 backup v4는 주문 행 hash·metadata·replay와 schema v4의 STRICT/UNIQUE/FK/rowid sequence/insert-only 제약을 source와 restore 후보 모두에서 확인한다. 실제 broker network submit, credentialed mock 호출, public API/UI, 시장가·정정, 체결-원장 reconciliation은 여전히 증명하지 않는다.

G4F/K2B0는 Go 내부 합성 조회로 **이미 명시적 ACK에 의해 opaque provider order ref가 묶인 주문**의 체결만 조정한다. 전체 조회와 전체 체결 목록이 모두 완결되고 account·주문번호·종목·방향·수량·가격·UTC 시간이 일치할 때만 체결을 시간·체결번호 순으로 기존 append-only 이벤트에 한 transaction으로 반영한다. 미완결·미발견·충돌은 상태를 바꾸지 않는다. 주문번호를 받지 못한 `SUBMIT_UNKNOWN`은 동일 속성·시간의 단일 주문이 보여도 `UNCORRELATED`로 유지하고 계좌 신규 submit을 계속 차단한다. 이는 credential, broker request, credentialed mock 관찰, unknown-submit 복구, public API/UI, production risk/broker-coupled fencing 또는 ledger mutation을 증명하지 않는다.

G4G/K2B1은 Go 내부에서 명시 날짜의 합성 키움 `kt00009` 체결 page를 읽고 정규화하는 provider-private 경계다. `A`-prefix KRX 주식, 현금 매수·매도, provider 주문유형, exact decimal과 non-zero 7자리 주문·체결번호만 허용하고 environment·account·날짜가 포함된 별도 dated alias를 만든다. terminal cursor는 해당 요청의 pagination 완료만 뜻하며 전체 주문·체결 이력 완료를 뜻하지 않는다. 공식 명세가 timezone·retention·ID lifetime을 보장하지 않으므로 요청 날짜와 `HH:mm:ss` 체결시각을 UTC로 결합하지 않고 `ExecutionsComplete=false`를 유지한다. 이 결과는 K2B0, `SUBMIT_UNKNOWN`, DB, public API/UI에 연결하지 않는다.

G4H는 기존 합성 `KiwoomSnapshot`의 완결성·identity·canonical decimal·정렬/중복 계약을 다시 검증한 뒤 Go/SQLite가 한 transaction으로 raw snapshot과 저장 시점 ledger revision의 종목별 broker-minus-ledger reconciliation을 분리 기록한다. 동일 raw snapshot·동일 ledger revision replay는 idempotent하고, 같은 raw snapshot이라도 ledger revision이 바뀌면 새 reconciliation만 append한다. 같은 fetched-at의 payload 충돌, 불완전 snapshot, 잘못된 ID는 전체 거절해 이전 known-good를 보존한다. 현재 schema/backup v4는 ledger event, broker snapshot, broker reconciliation과 K2C authority/reservation을 insert-only로 보호하고 각 state digest/count를 restore 후보와 비교한다. 이는 credentialed broker sync, scheduling, authoritative freshness/timezone, 현금·평가금액 자동 보정, public API/UI 또는 live 증거가 아니다.

G4I/K2C는 Go 내부 합성 주문의 실행 권한만 추가한다. 권한 기록이 없으면 기본 차단하고, 수동 arm/halt와 프로세스마다 새로 생성되는 owner ID, 30초 DB lease, 단조 증가 fencing token을 사용한다. 고정 정책은 `005930`·`000660`의 `BUY/LIMIT/KRX/KRW`만, 수량 10주 이하, 주문 limit notional 100만 원 이하, 계좌 active reservation 합계 100만 원 이하로 제한한다. immutable reservation과 reservation에 묶인 `RISK_APPROVED`/`SUBMIT_DISPATCHED`를 한 SQLite transaction에 연속 기록하고 DB trigger가 직접 우회를 차단한다. 이 수치는 production risk 한도가 아니며, broker request·credential·실제 송신·SELL·현금/보유/수수료/손실/시간/freshness 정책·owner/strategy 승인·public API/UI·paper/live readiness를 증명하지 않는다.

Flutter 제품 경험은 영웅문 화면을 재현하지 않는다. 토스증권에서 참고한 쉬운 용어, 한 화면 한 결정, 점진적 상세 공개, 국내·미국의 일관된 흐름, 명확한 주문 확인과 접근성을 Omni Folio 고유 디자인으로 구현한다. 상세한 공급자·UX 결정은 [`docs/broker-priority-and-ux.md`](docs/broker-priority-and-ux.md)를 따른다.

PostgreSQL maintenance migration과 restore 증명 전에는 multi-replica 또는 Kubernetes를 도입하거나 manifest를 만들지 않는다. live 주문은 서버가 owner 승인 만료, broker/account/strategy allowlist, promotion evidence, healthy kill switch를 **매 주문** 검증할 때만 허용한다. 휴대폰 background는 cache refresh와 push 보조만 하며 주문·reconciliation·kill switch의 authority가 아니다.

투자 알고리즘은 고정된 일회성 기능이 아니라 자동 개선 루프를 갖는다. Python 연구 프로세스가 versioned 전략과 제한된 파라미터 후보를 자동 생성하고, 불변 데이터 snapshot에서 비용·지연을 포함한 시계열 분할과 walk-forward 검증을 수행해 champion/challenger evidence를 만든다. 검증 통과 후보는 자동으로 `research_candidate → paper_candidate → paper → shadow`까지만 승격하거나 실패 시 이전 champion으로 되돌린다. 실행 중인 전략 코드를 자기 수정하거나 생성 코드를 바로 실행하지 않으며, 실제 자금의 canary/live 승격은 자동화하지 않고 별도 owner 승인과 Go risk/execution gate를 요구한다.
