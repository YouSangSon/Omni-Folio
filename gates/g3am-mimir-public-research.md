# G3AM Mimir public research reader

Mimir's offline exporter publishes one `mimir-public-research.v1` file. The existing Go service reads that file through optional `serve -research-feed`; the client reads native filings, news and Mimir-processed analysis through `GET /v1/research/feed?market=US&symbol=AAPL`. The public wire contract is owned by [`contracts/openapi.json`](../contracts/openapi.json).

```sh
# In the separate Mimir checkout, read existing stored data only.
python -m mimir.export_research --data-root /local/mimir/data \
  --config-dir /local/mimir/config --since 2026-11-01 --until 2026-11-03 \
  --out /local/research/mimir.json
# In this checkout, pass the completed artifact to the existing server.
make run-core RESEARCH_FEED=/local/research/mimir.json
```

Add `--sample` when exporting synthetic inputs. No provider, credential, LLM or Telegram call is needed for export. Mimir retains its own research universe and aliases. Omni's private holdings and watchlist never enter the producer. No raw Mimir code or dataset is bundled here; the fixture contains synthetic `example.test` material only.

The file is limited to an inclusive 31-day UTC window, 500 items and 1 MiB. Go JSON HTML escaping can expand the HTTP representation, so the client accepts at most 6 MiB + 4096 bytes while the artifact remains capped at 1 MiB. Overflow fails rather than truncating. The producer preserves explicit news tags, existing mention/word boundaries, market ambiguity rules, historical stored identities and per-output-subject IDs. Source-evidence readiness only distinguishes legacy missing metadata from explicit source lists. Recorded usage evidence and discovery metadata do not establish legal approval. Discovery target/URL, required attribution and the distinct rights source URL remain visible. No individual analysis signal is dropped from an aggregate score.

Go validates the whole bounded regular file before exact market/symbol filtering. Closed branch keys, duplicate keys/IDs, trailing JSON, Unicode, timestamps, URLs, text/list limits and scores fail closed. The existing closed-object callers retain their null rejection; only the new article title/summary keys explicitly allow null. Reading makes no DB, schema, valuation, strategy, broker or order writes and uses no hidden last-good server cache.

| Condition | HTTP |
| --- | --- |
| No configured file path | 503 `research_not_configured` |
| Configured file missing | 404 `research_feed_missing` |
| Invalid, unreadable, oversized or non-regular file | 503 `research_feed_unavailable` |
| Malformed, repeated or unsupported query | 400 `invalid_query` |
| Valid exact pair with no items | 200 empty items |

All responses use `Cache-Control: no-store`. Errors expose no path, input or raw exception. The client retains the original successful observation and its timestamps after refresh errors; a new valid empty response clears old cards. A different identity never inherits another identity's observation. Watchlist venue gives the market; holdings and Connections require an explicit market choice because holdings have no market field. URLs are selectable/copyable native text.

Local verification used Go 1.24.5, Python 3.14.5 and Flutter 3.47.1 on macOS ARM64. The initial full root `make check` exited 2 after the full Go suite passed in 521.956s and an existing Connections test tried to tap an offscreen button. The visibility fix preserved its original API-call and result assertions. The full Go pass was reused only after matching production/contract hashes and proving that existing research test bytes were unchanged apart from the added HTTP escaping case. The subsequent component command `make check CORE_TEST_PACKAGES='-run=TestPublicResearch -count=1 -timeout=10m ./...'` exited 0, including the new Go boundary tests, Python research 48 tests and JSON 45 files. The final UI-only changes then passed client formatting, analysis and all 483 client tests. This is combined component evidence, not a claim that the original full root command passed.

The actual offline exporter fed native Go HTTP and the release Flutter reader using visibly synthetic input. A real 1,268,539-byte Go response containing 100 escaped analysis items also passed the actual Dart `RestOmniApi`. The UI verified success, valid empty, error/retained observation and explicit market selection. Initial Chromium accessibility findings were fixed through the SDK's existing `SelectableText.semanticsLabel` and page-local `Card.semanticContainer: false`. The final independent Chromium tree had 199 active nodes, no merged card banners, separate headings/source buttons, and working Tab/Enter source expansion. Source/discovery/rights/terms roles remain distinct. Physical devices and VoiceOver were not checked.

The validated producer is commit `90a0bfeff2c658ce780dee33f6ccee0e38aa7f9b` in [Mimir draft PR #24](https://github.com/YouSangSon/Mimir/pull/24). Local integration is verified; Mimir main does not yet contain the exporter and its merge awaits approval of the inspection policy. Hosted Mimir Actions remain disabled by user policy. Local checks, remote CI, merge and deployment remain separate delivery gates.
