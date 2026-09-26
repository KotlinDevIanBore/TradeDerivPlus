# Investigation: mobile layout problems on the trading screen

**Date:** 26 September 2026 · **Branch:** `master` @ `a672bd7` · **Kind:** read-only. No code changed.
**Method:** the digit trading surface run locally (`DIGIT_CONTRACTS_ENABLED=true`, `BOT_ENABLED=true`, mock feed) and measured in the desktop app's browser pane with viewport emulation at 375×812, 390×844, 412×915, 375×667 and 1280×800. Every number below is a `getBoundingClientRect()` reading of the live DOM, not an estimate from class names. The pane was hidden during the session, so the page did not paint — layout still computes, screenshots do not, which is why this report has geometry and no pictures. 360×800 could not be emulated faithfully (the pane reported 376×834); the one figure quoted for it is derived, and says so.

Web paths are under `web/`.

---

## Summary

Three reported problems, three different root causes — and the first one is not a tab-row problem at all.

1. **The tab row overflows because its font is 16px, not the 12px the source asks for.** `ContractTypeTabs` writes `text-meta` (12px) but the rendered element has no font-size class: `cn()` is plain `tailwind-merge`, which does not know this project's custom `meta` token, files `text-meta` under *text colour*, and lets the later `text-muted` / `text-accent` win. The button falls back to the reset's `font-size: 100%` = 16px. At 16px the three tabs need 403px; at the intended 12px they would need roughly 310px and fit every phone. **The same defect silently strips the font-size in 30 `cn()` calls across 23 files** — the Trading-mode tabs and the desktop nav row among them, both verified at 16px on screen. This is a one-file fix with an app-wide visual consequence.

2. **The ticket is `position: sticky`, not fixed or absolute — and that is exactly why it covers the chart.** It is pinned to the bottom edge above the tab bar, it is 295–524px tall depending on contract type and mode, and the chart is a fixed 240px. Whatever the viewport height, the chart gets what is left. Over/Under on a 375×667 phone: **0px of chart visible.** In AUTO mode on 375×812: **3px.** Desktop uses the same component `lg:static` in its own grid column and is fine. The fix here is structural, not CSS.

3. **Toasts are anchored to the same bottom edge as the ticket** — `fixed bottom:80px, z-60`, growing upward, 64px each, up to three, `pointer-events: auto`. One toast covers the buy buttons (48 of 52px). Three cover the stake input (44 of 44px). Signals fire every 2 minutes in production and every contract raises two toasts (opened, settled), so during active trading the stack is rarely empty. On desktop the same toaster sits top-right and covers Switch account / Deposit / Withdraw / Account — same mechanism, different victim, and already noted as pre-existing in the Phase 3 report.

**On the "project's own publishing standards":** there is no `viewport-fit=cover` and no `env(safe-area-inset-*)` anywhere in `web/src`, the Tailwind config, `index.html`, or `docs/`. The brief assumed a convention this repository does not have. That is itself a finding (§4.3): the fixed 64px tab bar sits in the home-indicator zone on notched iPhones with nothing accounting for it.

---

## 1. The tab row overflow

### What the code says

[`ContractTypeTabs.tsx:24-36`](../../web/src/features/trading/ContractTypeTabs.tsx):

```tsx
<div role="tablist" className="flex gap-1 overflow-x-auto px-gutter lg:px-0">
  <button className={cn(
    'shrink-0 rounded-pill px-3 py-1.5 text-meta font-semibold transition-colors',
    selected ? 'bg-accent-subtle text-accent' : 'text-muted hover:bg-surface hover:text-text',
  )}>
```

So the brief's hypotheses — fixed width, no wrap, no scroll container — are each wrong in an instructive way. It **is** a horizontal scroll container (`overflow-x-auto`), the items **are** `shrink-0`, and the font **is** specified as 12px. "Over/Under" is not clipped; it is scrolled off the edge of a container that gives no sign it scrolls.

### What the browser renders

| Property | Source | Rendered |
|---|---|---|
| Class list | `… text-meta font-semibold … text-muted …` | `shrink-0 rounded-pill px-3 py-1.5 font-semibold transition-colors text-muted hover:…` — **no `text-meta`** |
| Font size | 12px | **16px** |
| Row content width | ≈310px (est. at 12px) | **403px** |

A probe `<span class="text-meta">` inserted into the same page computes to 12px, so the utility exists and works. The class is removed before it reaches the DOM.

### Root cause: `tailwind-merge` does not know `text-meta` is a size

[`lib/cn.ts`](../../web/src/lib/cn.ts) is `twMerge(clsx(inputs))` with no `extendTailwindMerge`. tailwind-merge resolves conflicts by class *group*; it knows the stock sizes (`text-xs` … `text-9xl`) and treats any other `text-<word>` as a colour. `text-meta` and `text-muted` therefore land in the same group, and the later one wins. `text-nano`, `text-body`, `text-card`, `text-title` and `text-hero` are all affected the same way.

**Blast radius, measured by parsing every `cn(` call for a custom size and a colour in the same call: 30 calls in 23 files.** Not every one is visible — some sizes coincide with 16px anyway — but the two tab rows on this screen are, and so is [`TopBar.tsx:18`](../../web/src/features/shell/TopBar.tsx) (`text-body` + `text-muted`, the desktop nav row). Plain `className="text-meta text-muted"` strings are unaffected; only `cn()` merges.

<details><summary>The 30 call sites</summary>

`components/DirectionBadge.tsx:19` · `components/AccountPill.tsx:23` · `components/ui/Input.tsx:45` · `components/ui/Tabs.tsx:18` · `components/ui/Card.tsx:17,21` · `features/signals/SignalsPanel.tsx:55,69` · `features/signals/SignalCard.tsx:51` · `features/portfolio/PositionCard.tsx:65` · `features/auth/RegisterScreen.tsx:154` · `features/trading/DigitDistributionRow.tsx:40` · `features/trading/BotTicket.tsx:242,313,399` · `features/trading/ChartRangeToggle.tsx:23` · `features/trading/DigitPicker.tsx:28` · `features/trading/OrderTicket.tsx:169,312,340` · `features/trading/TradingModeTabs.tsx:53` · `features/trading/ContractTypeTabs.tsx:35` · `features/shell/TopBar.tsx:18` · `features/wallet/HistoryPanel.tsx:167` · `features/admin/AdminShell.tsx:117,170` · `features/admin/components/DataTable.tsx:72` · `features/admin/components/ReasonDialog.tsx:74` · `features/admin/screens/ChangePassword.tsx:90` · `features/admin/screens/UserDetail.tsx:353`

</details>

### Where the overflow starts

Content is 403px wide (16px left gutter + three pills + 4px gaps + 16px right padding, which Chromium does count in `scrollWidth`).

| Viewport width | Overflow | Devices |
|---|---|---|
| 360 | 43px (derived: 403 − 360) | Galaxy S-series, many Androids |
| **375** | **28px** — measured | iPhone SE/8/X/11 Pro/12 mini/13 mini |
| **390** | **13px** — measured | iPhone 12/13/14/15 |
| **412** | **0** — measured, fits | **Pixel 7 — the e2e device** |

The Playwright mobile projects run on Pixel 7 (`playwright.config.ts:46,54`). At 412px the row fits by 9px, so no test has ever seen this. The overflow appears on every iPhone width and disappears on the one Android width CI uses.

### Recommended shape

- **Config, one file:** replace `twMerge` in `lib/cn.ts` with `extendTailwindMerge({ extend: { classGroups: { 'font-size': [{ text: ['nano','meta','body','card','title','hero'] }] } } })`. This fixes all 30 sites at once — and shrinks text that has been silently 16px, so it needs a visual pass across the admin console and the trading surface afterwards, not just this row. Add a unit test for `cn('text-meta text-muted')` preserving both, so the merge can never regress silently again.
- **CSS, this component:** the row will then fit at every width above, so the scroll affordance question becomes moot for three tabs. If a fourth tab ever arrives, add `no-scrollbar` plus an edge fade or `scroll-padding` — the present state (scrollable, no cue, `px-gutter` inside the scroller) reads as truncation and should not be kept as the fallback.

---

## 2. The ticket over the chart

### What the code says

[`DigitTradeScreen.tsx:277`](../../web/src/features/trading/DigitTradeScreen.tsx):

```tsx
<div className="sticky bottom-tabbar z-20 mt-4 lg:static lg:mt-0">
```

Computed on mobile: `position: sticky; bottom: 64px; z-index: 20`. **Not fixed, not absolute.** It participates in document flow — the page is 1150px tall at 375×812 and scrolls 338px — but a sticky element with a `bottom` offset paints itself at that offset whenever its natural position is below it, which on a phone is always. The result is a panel permanently pinned above the tab bar that paints over everything that scrolls beneath it. The same pattern is at [`TradeScreen.tsx:304`](../../web/src/features/trading/TradeScreen.tsx) for the CFD surface, so any fix applies to both.

The chart is a fixed height: `h-[240px]` on mobile, `lg:h-[340px]` ([`PriceChart.tsx:91`](../../web/src/features/trading/PriceChart.tsx), [`DigitTradeScreen.tsx:236`](../../web/src/features/trading/DigitTradeScreen.tsx)). Nothing ties it to the viewport or to the ticket's height. What the user sees of it is `min(chart.bottom, ticket.top) − max(chart.top, header.bottom)`.

### How tall the ticket is

| Ticket | Sticky block height |
|---|---|
| Even/Odd, manual | 295px |
| **Matches/Differs or Over/Under, manual** | **393px** |
| AUTO (`BotTicket`) | **524px** |

The 393px breaks down as: Trading-mode tab block 53 (16 of it `pt-4`), then the ticket: digit picker **98** (see §4.2 — it wraps to two rows), stake block 110 (label, 44px input, quick-stake chips), fee line 16, buy buttons 52, plus the ticket's own `p-4`.

### How much chart survives — the numbers the plan should target

Header is 57px (56 + border). Tab bar 64px. Chart 240px. Over/Under, manual, unless stated.

| Viewport | Chart visible, page scrolled to top | Scrolled to bottom | Note |
|---|---|---|---|
| **375×667** (iPhone SE/8) | **0px** | **0px** | The ticket's top edge (y=194) is *above* the chart's top. Nothing of the chart is on screen at any scroll position. |
| 375×812 | 134px | 66px | Even/Odd: 232 / 164. **AUTO: 3px / 0px.** |
| 390×844 | 166px | 98px | |
| 412×915 (Pixel 7) | 237px | 169px | Effectively the whole chart. The e2e device again hides the problem. |

"Scrolled to bottom" is lower because scrolling moves the chart up under the sticky header; the ticket does not move.

**Two caveats that make real phones worse than this table.** First, these are bare viewport heights; a real mobile browser's URL bar and toolbar take roughly 80–100px of `innerHeight` until they collapse, so a nominal 375×812 phone behaves closer to 375×720 — which puts Over/Under at roughly 40–50px of chart, i.e. the "thin sliver" in the screenshots. Second, `BotTicket` at 524px leaves nothing on *any* common phone. The AUTO screenshots would show no chart at all.

### Mobile versus desktop

Desktop is correct and it is the same component: at 1280×800 the ticket computes `position: static`, sits in the `lg:grid-cols-[1fr_380px]` right-hand column at 648px tall, and the chart (340px) is fully visible beside it. The divergence is entirely the `sticky … lg:static` pair — there is no separate mobile layout path to fix, only a mobile positioning decision.

### Viewport meta and safe areas

[`index.html:6`](../../web/index.html): `width=device-width, initial-scale=1.0`. No `viewport-fit=cover`. A repo-wide search for `safe-area`, `viewport-fit` and `env(` returns nothing in `web/src`, `tailwind.config.js`, `index.html` or `docs/`. So nothing here is *interacting badly* with safe-area handling — there is none to interact with. The consequence is §4.3.

### Recommended shape — structural

The competitor stacks (tabs → chart → digit row → mode → stake → buttons) and lets the page scroll. This screen pins the whole ticket. Two shapes preserve the "act without scrolling" intent without burying the chart:

- **(A) Stack the ticket in flow; pin only the action bar.** Ticket becomes an ordinary block below the session strip. A small sticky bar carries just the two buy buttons (≈68px with padding) and, optionally, the current stake as text. The chart keeps its full 240px on every height in the table above, including 375×667, and the primary action stays one tap away. This is the closest match to the reference and needs no expand/collapse state.
- **(B) Keep it sticky but collapsed.** Pinned strip shows stake + selection + buy buttons; tapping expands the full ticket as a sheet over the chart. More state, more to test, and the sheet still covers the chart while open — which is the moment the user wants to see the price.

Either way, **set a floor and test it**: at least **160px of chart at 375×667 with Over/Under selected** (two thirds of the chart on the smallest common phone), asserted in a Playwright project that is *not* Pixel 7. Simple CSS that helps regardless: collapse the double top padding (`pt-4` on the mode block plus the ticket's own padding), and fix the digit picker wrap (§4.2), which alone returns ~40px.

---

## 3. Toasts over the stake input

### What the code says

[`Toast.tsx:118-126`](../../web/src/components/ui/Toast.tsx):

```tsx
'pointer-events-none fixed inset-x-0 z-[60] flex flex-col gap-2 px-gutter',
'bottom-[calc(theme(spacing.tabbar)+16px)]',          // mobile: bottom 80px
'sm:bottom-auto sm:left-auto sm:right-6 sm:top-6 sm:w-[360px] sm:px-0',
```

Rows are `pointer-events-auto` (`:76`), 5s each (`:33`), at most three (`:29`, `slice(-2)`). Mounted once in `App.tsx:86`. No awareness of what is beneath.

### Measured, 375×812, Over/Under, three toasts live

| Element | y-range | Covered by toasts |
|---|---|---|
| Toast 3 (newest) | 668–732 | — |
| Toast 2 | 596–660 | — |
| Toast 1 (oldest) | 524–588 | — |
| **Buy buttons** | 664–716 | **48 of 52px** by *one* toast; fully by two |
| Quick-stake chips + fee line | ~600–660 | fully by two |
| **Stake input** | 541–585 | **44 of 44px** by three |

The arithmetic is exact: the ticket's bottom is `812 − 64 = 748` and the toaster's is `812 − 80 = 732`, so the stack grows upward *from 16px inside the ticket*. It is not that toasts happen to land on the form — they are anchored to it. And because rows are `pointer-events-auto`, a toast over the buy button intercepts the tap rather than passing it through.

### Cadence

`SIGNAL_INTERVAL` defaults to 2 minutes (`config.go:299`), and signal toasts fire on the digit surface too ("New BUY signal on CRASH500" was observed here). Each contract raises "opened" and then "won/lost". A manual trader placing contracts as they settle keeps two toasts alive continuously and a third every two minutes — the stake input in the screenshots is covered whenever the stack reaches three.

### Desktop

Same toaster, `sm:top-6 sm:right-6`, 360px wide. Measured at 1280×800 with three toasts: they cover **Switch account, Deposit, Withdraw and Account** in the top bar. Not less visible — a different victim. `one-screen-migration-phase-3.md:196` already recorded this as pre-existing. The 24px offsets ignore the 56px header.

### Recommended shape — CSS/positioning, plus one behaviour

- **Mobile:** anchor the toaster to the *top*, below the header (`top: calc(theme(spacing.topbar) + 8px)`), or above the sticky ticket's top edge rather than 16px inside it. The bottom of a phone screen is where this app's controls live; the top is where nothing is interactive. If (A) in §2 lands, "above the action bar" also works.
- **Desktop:** offset below the header (`sm:top-[calc(theme(spacing.topbar)+16px)]`), which fixes the top-bar coverage for free.
- **Behaviour:** collapse repeated signal toasts into one ("2 new signals"), and shorten "opened" — the settlement toast that follows one tick later makes it redundant. Neither is required for the overlap fix; both cut how often the stack reaches three.

---

## 4. Everything else found at mobile widths

### 4.1 The font-size defect is app-wide — the largest finding here

Covered in §1. Restated because it outranks the three reported problems: 30 `cn()` sites render at an unintended size today, and fixing `cn.ts` will change the look of screens nobody has reported.

### 4.2 The digit picker wraps to two rows at 375px

[`DigitPicker.tsx`](../../web/src/features/trading/DigitPicker.tsx): ten 36px buttons with 4px gaps need 396px; the ticket's content box is 343px. `flex-wrap: wrap` puts **"0 1 2 3 4 5 6 7" on one row and "8 9" on the next.** Two orphaned digits, an extra 40px in the ticket, and a row that looks broken. At 412px it fits in one row — Pixel 7 again. **Fix: CSS** — `flex-nowrap justify-between` with 30–32px buttons (ten of them fit in 343 with ≥2px gaps), or a 10-column grid.

### 4.3 No safe-area handling anywhere

No `viewport-fit=cover`; no `env(safe-area-inset-bottom)`. The tab bar is `fixed`, 64px, at `bottom: 0`, and the toaster sits 16px above it. On any iPhone with a home indicator the bottom ~34px of the tab bar is under the system gesture area; in standalone/PWA mode the collision is total. Not measurable in emulation, stated from the code. **Fix: CSS** — `viewport-fit=cover` in `index.html`, `padding-bottom: env(safe-area-inset-bottom)` on `TabBar`, and the sticky ticket's and toaster's bottom offsets expressed against the tab bar's *real* height. Project-wide, not this screen.

### 4.4 No horizontal page overflow

`document.scrollingElement.scrollWidth` = viewport width at every size tested; an element scan for anything extending past the right edge (excluding the intentionally scrollable tab row and fixed elements) found nothing. The page body does not scroll sideways. Good.

### 4.5 Conventions: none to have been built before

The screen was written on 23 September (`fb9613a`, `e15f0a9`), the same week as the one-screen migration. The only responsive guidance in `docs/` is `ui-ux-plan.md:568` — "screenshot each on 375px and 1280px" — and the migration reports' 360/390px references. Neither is a convention this screen violates; there is no documented mobile standard, and the e2e device (Pixel 7, 412px) is wide enough that both the tab overflow and the digit-picker wrap are invisible to CI. **The plan should add a 375px-class Playwright project** (e.g. `iPhone 13 mini` or `iPhone SE`) before fixing anything, so the fixes have something to fail against.

---

## 5. Fix classification

| Problem | Root cause | Shape | Kind |
|---|---|---|---|
| §1 Tab row overflow | `twMerge` drops `text-meta` → 16px | `extendTailwindMerge` in `lib/cn.ts` + visual pass + a `cn` unit test | **Config, one file; app-wide effect** |
| §2 Ticket covers chart | Whole ticket `sticky bottom-64px`, 295–524px, against a 240px fixed chart | Stack ticket in flow, pin only the action bar (or collapse-to-strip); floor of ≥160px chart at 375×667 | **Structural** |
| §3 Toasts over form | Toaster anchored 16px inside the ticket's bottom edge, `pointer-events-auto` rows | Move to top on mobile; below header on desktop; collapse signal toasts | **CSS**, plus one small behaviour change |
| §4.2 Digit picker wraps | 10×36 + gaps > 343px | `flex-nowrap` / grid, 30–32px buttons | **CSS** |
| §4.3 Safe areas | Absent project-wide | `viewport-fit=cover`, `env(safe-area-inset-bottom)` on tab bar and bottom offsets | **CSS, project-wide** |
| §4.5 Test coverage | Only Pixel 7 (412px) | Add a 375px Playwright project first | **Test config** |

Order that pays off: **4.5 → 1 → 4.2 → 3 → 2 → 4.3.** The test project first so every later change has a failing check; the `cn.ts` fix second because it changes the size of things the structural work will then be laid out around.
