# Omni Folio Design System Master

Flutter app UI의 기본 계약이다. 페이지별 문서는 이 계약을 좁힐 수는 있어도 신뢰, 접근성, 상태 표시 규칙을 완화할 수 없다. 마케팅·scroll storytelling·hover-only 상호작용과 CSS 구현 규칙은 적용하지 않는다.

## Product posture

- Mobile-first, trust-first: 수익보다 데이터 freshness, 오류 범위, 복구 행동을 먼저 보인다.
- Calm and progressive: 작은 화면에서는 한 가지 결정을 위한 정보만, 넓은 화면에서는 같은 정보의 표·차트 병치를 제공한다.
- Toss-inspired, not copied: 쉬운 말, 강한 숫자 위계, 점진적 상세 공개와 짧은 주문 확인 흐름을 쓰되 브랜드·화면·motion을 복제하지 않는다.
- Flutter iOS/Android/app-centric web의 동일한 semantic token과 widget state를 사용한다.

## Semantic tokens

| Token | Light | Dark | Purpose |
|---|---|---|---|
| `surface.canvas` | `#F2F4F6` | `#17171C` | page background |
| `surface.raised` | `#FFFFFF` | `#202027` | cards, sheets |
| `content.primary` | `#191F28` | `#F2F4F6` | primary text |
| `content.secondary` | `#6B7684` | `#B0B8C1` | metadata |
| `border.subtle` | `#D1D6DB` | `#3A3A43` | grouping |
| `action.primary` | `#2563EB` | `#60A5FA` | primary action/focus |
| `status.positive` | `#047857` | `#34D399` | gain/healthy |
| `status.negative` | `#B91C1C` | `#FCA5A5` | loss/blocking |
| `status.warning` | `#A16207` | `#FCD34D` | stale/attention |
| `status.info` | `#0369A1` | `#7DD3FC` | sync/context |

Status color never carries meaning alone: include sign, text, icon, and where relevant the last-success timestamp.

## Type, spacing, and interaction

- Font: Noto Sans KR when bundled/available; otherwise platform system sans-serif. No Caveat or Quicksand.
- Numbers: use tabular figures for price, quantity, percentage, date/time, and all ledger columns.
- Type scale: 12 metadata, 14 body, 16 control, 20 section, 28 portfolio value. Default body/control text is at least 16 logical px.
- Spacing scale: 4, 8, 12, 16, 24, 32. Radius: 12 controls, 16 cards/sheets. Use surface contrast and only necessary borders before elevation; no decorative shadows.
- Targets are at least 48×48 logical px, which also clears the 44pt iOS floor. Touch, mouse, keyboard, and assistive-technology activation must reach the same action; hover may supplement but never reveal required information or controls.
- Visible focus, logical focus order, semantic labels, screen-reader summary for charts, 200% text zoom, and reduced motion are release requirements.
- Motion is optional feedback only: no scroll narrative, auto-play, layout shift, or animation needed to understand data. `reduce motion` renders the final state immediately.

## Navigation and screen states

Mobile uses a bottom navigation for Home, Holdings, History, and Connections. Desktop may use a sidebar with the same destinations. Asset detail, import review, and order review are contextual routes; an unavailable capability is not shown as a teaser.

| Screen | First question answered | Required content |
|---|---|---|
| Overview | Is my data trustworthy now? | freshness, unresolved sync/error, total/cash, last import/backup, then performance |
| Import review | What will change? | source, duplicate/error count, diff preview, confirm/apply or recovery |
| Orders | Can this order be sent safely? | mode, capability, price/quantity/fee, limits, explicit confirmation, lifecycle/reconciliation |
| Chart / asset detail | What changed and what supports it? | period, price/volume, average cost and fills, data timestamp, textual/table alternative |

Every data screen explicitly renders `loading`, `empty`, `error`, `partial`, `stale`, and `success`:

- `loading`: stable skeleton and announced busy state; never replace a usable previous value with blank space.
- `empty`: explain why it is empty and offer one relevant action.
- `error`: state cause, affected broker/account, retained data, and recovery action.
- `partial`/`stale`: keep successful data visible, name the failed source, show last success and retry path.
- `success`: show data timestamp and source when the value can influence a decision.

## Order safety

Order UI is informational, not authority. It must show `paper`, `shadow`, `live-disabled`, or `live-enabled`; live confirmation must state the broker/account/strategy scope and approval expiry. The server independently validates approval, allowlist, promotion evidence, and kill switch on every submit. Background mobile work may refresh cache or present push only.

## Delivery checklist

- [ ] Light and dark semantic tokens meet readable contrast.
- [ ] Numeric values use tabular figures and color-independent gain/loss text.
- [ ] 375 px, tablet, and desktop layouts preserve all primary actions without horizontal scrolling.
- [ ] Touch, keyboard, screen reader, 200% text, and reduced-motion paths were checked.
- [ ] Overview, import, orders, and chart implement all six states.
- [ ] Order UI does not claim a local toggle, cache, or background task can authorize live execution.
