# Omni Folio Design

This is the product-facing design contract for the Flutter client. [`design-system/omni-folio/MASTER.md`](design-system/omni-folio/MASTER.md) defines its reusable tokens and accessibility rules.

## Direction

Omni Folio is a personal multi-broker portfolio app, not a marketing site or trading terminal. The experience is mobile-first, calm, and trust-first: before performance, show whether the data is fresh, complete, and recoverable. Flutter is the single client for iOS, Android, and app-centric web; the earlier React/PWA recommendation is superseded.

The interaction model is Toss-inspired, not a copy of Toss Securities. Use plain language, one primary decision per screen, strong numeric hierarchy, progressive disclosure, and the same mental model for Korean and US stocks. Do not copy Toss trade dress, brand assets, exact layouts, or motion. Kiwoom is the first execution provider and Toss Securities is the second planned provider; provider-specific TR codes and raw errors stay out of the primary UI.

Use Noto Sans KR or the platform system sans-serif, tabular figures for financial values, and light/dark themes with semantic tokens. Never use Caveat, Quicksand, scroll storytelling, hover-only affordances, or animation needed to understand data.

## Information architecture

```text
Overview       trust state → total/cash → performance
Holdings       positions → asset detail → lots and transactions
Activity       transactions → import review → recovery
Data           broker connections → export → backup/restore
Contextual     order review, chart detail, settings
```

Mobile has four persistent destinations: Home, Holdings, History, Connections. Desktop may render the same hierarchy in a sidebar. Do not show disabled future features merely to advertise them. Use user language first and place ledger revisions, provider request IDs, and raw diagnostics behind a details action.

## Required screens

| Screen | User decision | Must show |
|---|---|---|
| Overview | Can I trust this snapshot? | per-source freshness, sync failures, total value/cash, recent import/verified backup, then return metrics |
| Import review | Should these records enter the ledger? | source, normalized count, duplicates/errors, preview diff, explicit confirm/apply and recovery |
| Orders | Can I safely submit this intent? | broker capability, paper/shadow/live mode, quantity/price/fee, risk outcomes, confirmation, order lifecycle |
| Chart / asset detail | What changed and why? | period, price/volume, fills and average cost, timestamp/source, text and table alternative |

Each data screen implements `loading`, `empty`, `error`, `partial`, `stale`, and `success`. Retain known-good data during refresh or partial failure; name the affected broker/account, last successful timestamp, impact, and recovery action. Gains/losses require sign and text as well as color.

Cash-flow correction rows stay inside Import review. Show the original source ID, type, currency and amount beside the exact reversing amount, and state that the original record is preserved and offset. Do not expose internal event/account IDs or imply that trade, split, FX, or broker reconciliation correction is supported.

FX exchange rows stay inside Import review and show both exact cash legs as `USD 100 매도 → KRW 137000 매수` in visible and screen-reader text. State that the row neither calculates an exchange rate nor represents a current quote. Do not imply FX valuation, FX correction, broker cash reconciliation, or tax classification.

History may show the replay-verified local ledger as a read-only `최근 거래` section. Keep CSV import as the first decision and hide the history section while the user is editing or reviewing an import. Each row uses a vertical layout, exact signed cash values, event time and one combined screen-reader label; FX shows both legs and correction rows say the original is preserved. Label the section `로컬 원장 기록 · 현재 증권사 상태 아님`, retain its last known-good page on refresh failure, and expose no internal/account identifiers, valuation, inferred FX rate, correction, order, filter, or export action.

Connections may show the verified local order log as read-only history. It must say that no broker refresh occurred, hide account/client/provider/internal identifiers, retain the last known-good view after refresh failure, and expose no submit, retry-submit, cancel, or amend action. `SUBMIT_UNKNOWN` uses the fixed warning `브로커 결과 미확정 · 재주문 금지` in visible and screen-reader text. If a locally filled order still has `pending_action=CANCEL`, show that the order is filled but cancel confirmation is still unknown and prohibit further action.

Overview may summarize the stored broker-to-ledger position reconciliation, but it must say `현재 상태 아님`, show match or mismatch count plus broker-fetched and locally-recorded times, and link to Connections for per-symbol details. It reuses the same retained known-good object as Connections, never performs an extra broker refresh, and never exposes raw errors or internal/account identifiers.

Overview may also warn about unresolved local submit or cancel actions from the same retained order log used by Connections. It must label the source as local and not current broker state, prohibit resubmission or further manipulation, avoid claiming safety when the log is empty or unavailable, and add no order mutation.

On first run, `never_verified` with an empty snapshot is an empty state rather than a successful zero-portfolio summary. Overview offers one primary action to open the existing transaction import flow; a non-empty unverified snapshot remains visible with its trust warning.

For local product review, `make seed-demo` sends the existing golden CSV through the real import preview/apply API and is idempotent once those rows exist. It must not bypass the API, write the database directly, invent broker freshness, or turn unavailable valuation into a displayed estimate.

## Interaction and accessibility

- Minimum 48×48 logical-pixel interactive targets, which also clears the 44pt iOS floor; default text/control size 16 logical px or larger.
- All actions work with touch, pointer, keyboard, and screen reader. Hover is supplementary only.
- Provide visible focus, logical traversal, semantic labels, chart summaries/table alternatives, 200% text support, and reduced-motion final states.
- Motion is quiet feedback only: never autoplay, scroll-scrub, or shift layout to present a decision.

## Live-order boundary in the UI

The app may display a live capability but never owns it. A live confirmation shows owner-approved broker/account/strategy scope and expiry, while the server independently checks approval, allowlist, promotion evidence, and kill switch for every order. Mobile background behavior is limited to cache refresh and push; it cannot run order submission, reconciliation, or kill-switch authority.

Strategy rollback is a server-owned safety action: it atomically halts and fences every armed execution account before changing the selected strategy. The client may request and display that result, but it never emulates the halt locally.

G3.8C2 paper fill accounting, G3.8C3 account-global performance, G3.8D strategy-window performance, G3.8E paper performance safety actions, and G3.8F1/F2 one-shot or DB-leased always-on local policy runs are internal Go evidence boundaries and add no client surface. The UI must not present modeled cash, FIFO PnL, ex-post bar-open fills, fixture marks, strategy-window returns, scheduled paper runs, or automatic paper halts as current portfolio value, broker execution, advice, or live strategy performance until a later versioned read contract explicitly promotes them.
