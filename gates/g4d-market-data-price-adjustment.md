# Gates: G4D Market-Data Price-Adjustment Consumer Contract

Scope: K1이 내부에 보존한 가격 조정 기준을 HTTP·OpenAPI·Flutter에서 잃지 않되, local fixture나 합성 Kiwoom 결과를 live data로 승격하지 않는다.

## Acceptance and evidence

- [x] G4D1: provider-neutral `MarketDataCandles`는 `price_adjustment`를 필수로 하고 `unspecified`, `provider_adjusted`만 허용한다.
  CHECK: `cd services/core && go test -run '^TestMarketData' -count=1 ./...`
  EVIDENCE: 2026-08-24 KST PASS. OpenAPI required/enum과 local-fixture subtype의 `const=unspecified`를 executable Go test가 확인한다.

- [x] G4D2: 공개 local-fixture route는 `price_adjustment=unspecified`를 반환하고 빈 값, `provider_adjusted`, 미지원 값은 fail-closed한다.
  CHECK: `cd services/core && go test -run '^TestMarketData' -count=1 ./... && go test -race -count=1 ./...`
  EVIDENCE: 2026-08-24 KST PASS. Exact HTTP response와 잘못된 port 결과의 500 경계를 검증했고 race suite도 통과했다.

- [x] G4D3: Flutter는 두 값을 엄격히 파싱하고 local fixture의 전체 provenance 모순을 거절하며, 가격 기준을 보수적인 쉬운 말로 표시한다.
  CHECK: `cd apps/client && flutter analyze && flutter test`
  EVIDENCE: 2026-08-24 KST PASS. 17 tests가 미지원 값과 `local_fixture`의 `sample=true`, `price_adjustment=unspecified`, `state=stale`, non-null `source_as_of`, non-empty issue 조합을 검증하고 두 UI 문구, 200% text와 기존 접근성·차트 상태도 확인한다.

- [x] G4D4: root vertical slice와 K1 내부 provenance를 회귀시키지 않는다.
  CHECK: `make check && make smoke && (cd services/core && go test -run '^TestKiwoomCandles' -count=1 ./...)`
  EVIDENCE: 2026-08-24 KST PASS. 17 Flutter tests, 13 Python tests, Go unit/vet/race, 15 JSON contracts와 smoke가 통과하고 Kiwoom K1은 계속 `provider_adjusted`를 보존한다.

## TDD evidence report

Source plan: [`../PLAN.md`](../PLAN.md)의 G4C 다음 consumer-contract gap에서 도출했다.

User journey: 자산 상세를 보는 사용자는 차트 가격이 조정 가격인지 확인되지 않은 가격인지 알아야 잘못된 비교를 피할 수 있다. Adapter 구현자는 미지원 가격 기준을 조용히 노출하지 않고 계약 위반으로 발견해야 한다.

| Guarantee | Test | Type | Result |
|---|---|---|---|
| HTTP fixture가 `price_adjustment=unspecified`를 정확히 반환 | `TestMarketDataCandlesFixtureResponseIsExact` | integration | PASS |
| local route가 빈 값·provider-adjusted·미지원 값을 500으로 거절 | `TestMarketDataCandlesErrorsAndQueryValidation` | boundary | PASS |
| OpenAPI required/enum/local const가 코드와 일치 | `TestMarketDataOpenAPIRequiresPriceAdjustment` | contract | PASS |
| Flutter가 enum과 OpenAPI local-fixture subtype 전체를 검증 | `golden preview and snapshot parse canonical decimal strings` | model | PASS |
| 두 가격 기준이 200% text에서도 보수적으로 표시 | asset-detail widget tests | UI/accessibility | PASS |

RED checkpoint `4e81f2b`: Go exact response·fail-closed·OpenAPI tests와 Flutter parser/UI tests가 의도한 누락 때문에 실패했다. GREEN checkpoint `0519d20`: 같은 focused suites가 통과했다. 독립 리뷰에서 발견한 local subtype 불일치는 `bebbfaa` RED와 `41093c0` GREEN으로 닫았다.

Coverage: Go 전체는 기존 기준선 포함 76.2%지만 이번 경로인 `market_data.go` 함수들은 80.0%~100.0%이고 handler는 87.9%다. Flutter line coverage는 95.4%(766/803)다. 전체 Go 80% 미달은 숨기지 않고 후속 test debt로 남긴다.

Known gaps: physical VoiceOver/TalkBack과 frame profile은 G4B3에 남아 있다. 이 gate는 credential, broker request, live/current/freshness, corporate-action adjustment correctness, persistence, realtime, reconciliation 또는 order capability를 증명하지 않는다.
