# Gates: G4B Local Sample Market Data and Chart

Scope: credential·broker request·live claim 없이 provider-neutral OHLCV 계약과 Flutter 종목 상세 chart의 정확성·상태·접근성·성능 경계를 증명한다.

- [x] G4B1: 명시적으로 전달한 local CSV fixture만 제공하고, 미설정은 `503`, 없는 series는 `404`, 잘못된 query와 port mismatch는 fail-closed한다.
  CHECK: `make check && make smoke && (cd services/core && go test -race -count=1 ./...)`
  EXPECT: root checks pass; smoke prints `market data OK`; race test prints `ok omni-folio/services/core`.
  EVIDENCE: 2026-08-24 KST PASS. Input is capped at 1 MiB and 500 bars, metadata and timestamps are consistent/strictly ordered, OHLC and volume use exact canonical decimals, and the endpoint pins `price_adjustment=unspecified`, `source=local_fixture`, `sample=true`, `state=stale` with a non-live issue. The provider-neutral base schema remains separate from this local-fixture subtype.

- [x] G4B2: Flutter asset detail shows source/as-of/fetched-at, price/volume, loading·empty·error·retained failure·partial·stale·success, sample warning, non-color filled/outline cues, chart summary, and an exact OHLCV alternative.
  CHECK: `cd apps/client && flutter analyze && flutter test`
  EXPECT: no analyzer issue; all widget/model/API tests pass.
  EVIDENCE: 2026-08-24 KST PASS. 17 tests cover response binding, 500-bar cap, exact >2^53 OHLC ordering, chart loading, 200% text, touch/contrast semantics, retained-error live announcement, table header/cell labels, and state recovery. Canvas geometry uses finite doubles only for pixels; displayed and compared values remain canonical strings. No chart dependency was added.

- [ ] G4B3: chart/table frame budget and assistive-technology evidence are complete on representative physical Android and iOS hardware.
  CHECK: run the command in `apps/client/README.md` on each physical platform and manually traverse the sample warning, chart summary, refresh failure, and exact table with TalkBack/VoiceOver.
  EXPECT: build and raster p95 each `<=16.67 ms`; metadata and `table_scroll_exercised=true` recorded; no unlabeled or unreachable content.
  EVIDENCE: Android emulator optimization evidence only. The original 500-row intrinsic table failed at 626 frames with build/raster/total-span p95 `3.230/22.446/36.109 ms`. Replacing it with a fixed-width lazy list produced two valid, metadata-complete runs that exercised bidirectional table scrolling: 727 frames at `0.928/5.749/10.698 ms` and 728 frames at `1.158/16.623/20.632 ms`. Both build/raster phases pass, but the narrow second raster margin and prior emulator variance do not replace physical-device evidence. An intermediate hit-test-missed run was excluded.

This leaf does not prove current prices, Kiwoom candle semantics, realtime, portfolio performance, period switching, average-cost/fill markers, manual screen-reader usability, or any order capability. Sample data must never be promoted or mixed as live data.
