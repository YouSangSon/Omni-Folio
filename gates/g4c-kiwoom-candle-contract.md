# Gates: G4C Kiwoom K1 Synthetic Candle Contract

Scope: credential-free synthetic Kiwoom `POST /api/dostk/chart` contract for `ka10080` (minute) and `ka10081` (daily). This is not a broker integration gate.

- [x] G4C1: KRX six-digit symbols and only `1d` plus `1/3/5/10/15/30/45/60m` are accepted; the correct API-ID and chart route are selected.
  CHECK: `cd services/core && go test -run '^TestKiwoom' -count=1 -v ./...`
  EVIDENCE: 2026-08-24 KST PASS. Synthetic tests prove `ka10080` and `ka10081` use `/api/dostk/chart`, account IDs still use `/api/dostk/acnt`, and submit IDs fail before network.

- [x] G4C2: signed prices normalize to magnitude, all price/OHLC fields remain exact decimals, and volume is nonnegative.
  CHECK: `cd services/core && go test -run '^TestKiwoomCandles' -count=1 -v ./...`
  EVIDENCE: 2026-08-24 KST PASS. Malformed decimals, JSON numbers, zero prices, negative volume, invalid dates, and invalid OHLC ranges fail closed.

- [x] G4C3: descending pages normalize UTC ascending; identical overlaps dedupe; conflicting overlaps reject; output is capped to newest 500 bars; `upd_stkpc_tp=1` is internally `provider_adjusted`.
  CHECK: `cd services/core && go test -run '^TestKiwoomCandles' -count=1 -v ./... && go test -count=1 ./... && go test -race -count=1 ./...`
  EVIDENCE: 2026-08-24 KST PASS. The synthetic multi-page fixture returns strict ascending UTC RFC3339 bars and provider-adjusted metadata; one bounded look-ahead page catches a conflicting overlap after the 500-bar cap. Core unit and race tests pass.

- [x] G4C4: K1 does not regress the root local vertical slice.
  CHECK: `make check && make smoke`
  EVIDENCE: 2026-08-24 KST PASS. `make check` validates Go vet/tests, Flutter analyze/tests, Python tests, and JSON contracts; `make smoke` prints `health, status, preview, apply, snapshot, market data OK`.

Operational assumption: official candle documentation does not state timestamp timezone, so timestamps are interpreted as Asia/Seoul until real provider evidence changes it.

This gate proves no credential, broker request, live/current/fresh data, public endpoint, persistence, adjustment-event correctness, realtime, reconciliation, or order capability. The public route remains `source=local_fixture`, `sample=true`, `state=stale`.
