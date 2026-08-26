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

## Interaction and accessibility

- Minimum 48×48 logical-pixel interactive targets, which also clears the 44pt iOS floor; default text/control size 16 logical px or larger.
- All actions work with touch, pointer, keyboard, and screen reader. Hover is supplementary only.
- Provide visible focus, logical traversal, semantic labels, chart summaries/table alternatives, 200% text support, and reduced-motion final states.
- Motion is quiet feedback only: never autoplay, scroll-scrub, or shift layout to present a decision.

## Live-order boundary in the UI

The app may display a live capability but never owns it. A live confirmation shows owner-approved broker/account/strategy scope and expiry, while the server independently checks approval, allowlist, promotion evidence, and kill switch for every order. Mobile background behavior is limited to cache refresh and push; it cannot run order submission, reconciliation, or kill-switch authority.

Strategy rollback is a server-owned safety action: it atomically halts and fences every armed execution account before changing the selected strategy. The client may request and display that result, but it never emulates the halt locally.
