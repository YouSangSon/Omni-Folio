# Gates: G4A Kiwoom Read Contract

Scope: live credential, public route, persistence 없이 키움 계좌·잔고·미체결 read-only transport와 정규화 경계를 증명한다.

- [x] G4A1: 고정 운영·모의 endpoint, OAuth cache, read API-ID allowlist, pagination이 합성 응답에서 계약대로 동작한다.
  CHECK: cd services/core && go test -run '^TestKiwoom' -count=1 -v ./...
  EXPECT: /PASS/
  EVIDENCE: 2026-08-24 KST PASS. Synthetic contract tests covered fixed host/path assertions, token cache refresh, allowlist fail-closed, KRX-only boundary, environment-bound aliases, pagination continuation, repeated cursor rejection, API-ID mismatch rejection, and body rate-limit code classification.

- [x] G4A2: 금액·수량이 exact decimal string으로 정규화되고 secret, token, 원계좌번호, 원주문번호가 결과와 오류에 남지 않는다.
  CHECK: cd services/core && go test -run '^TestKiwoom' -count=1 -v ./...
  EXPECT: /PASS/
  EVIDENCE: 2026-08-24 KST PASS. Synthetic tests covered canonical decimal normalization, signed current-price magnitude, JSON-number/exponent rejection, negative quantity/order-price rejection, invalid order time/exchange rejection, account/order aliasing, masked account output, and error redaction.

- [x] G4A3: 새 adapter를 포함한 Go core 전체가 race detector를 통과한다.
  CHECK: cd services/core && go test -race -count=1 ./...
  EXPECT: /ok\s+omni-folio\/services\/core/
  EVIDENCE: 2026-08-24 KST PASS: ok omni-folio/services/core 1.565s.

이 leaf의 통과는 합성 contract evidence일 뿐이다. 키움 credential 발급, 모의 API 호출, known-good persistence, reconciliation, Flutter 공개 계약과 실주문 준비를 증명하지 않는다.
