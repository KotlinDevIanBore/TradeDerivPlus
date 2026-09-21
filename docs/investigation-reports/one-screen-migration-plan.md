# TradeDerivPlus — One-Screen Model Migration Plan (Pass 2b)

**Type:** Planning only. No implementation code, styling, or component changes were made in this pass.
**Date:** 21 September 2026

**Prior reading, in order:** [`ui-polish-investigation.md`](./ui-polish-investigation.md) → [`ui-polish-implementation.md`](../implementation-reports/ui-polish-implementation.md) → [`nav-layout-investigation.md`](./nav-layout-investigation.md). This plan builds on the third report's sections 4.1 (the three depths), 4.5 (overlay versus route), and 4.6 (desktop) rather than repeating them.

**Confirmed decisions this plan is built on** (not re-litigated): full one-screen model; desktop sidebar replaced by a horizontal top bar; Live Chat left out entirely; contract-type tabs, the digit row, AUTO/MANUAL and any bot logic remain out of scope; no "AI" nav item — the slot in that position either points at the existing advisory Signals feature under its own name, or is left out.

---

## 1. Summary

### The short version

The migration is more affordable than "rewrite the navigation" sounds, for one reason: **the persistent screen already exists and Pass 1 just finished polishing it.** The Trade screen — chart with a price scale, window change beside the price, session figures, order ticket with projected profit and loss — becomes the app, essentially as built. Nothing in Pass 1 needs reworking.

The work is not in building a new main surface. It is in **dissolving four screens into panels that sit over it**, and the largest of those four, Home, turns out to be almost entirely a summary of things that live elsewhere: its balance card is already reused by Wallet, its transaction list is the Wallet list, its positions block is a three-row preview of Portfolio, and its signal card is a copy of what Signals shows. Home's only genuinely unique contributions are the balance figure and the Deposit and Withdraw buttons — and all three of those are moving into a top bar that is visible everywhere. So Home does not need to be rebuilt as a panel. It gets taken apart, and most of the pieces are deleted rather than moved.

### What the client is buying

Money actions and the balance become visible on every screen instead of two. Positions, History, Signals and Account become panels that slide over the chart instead of destinations that replace it. The chart stops disappearing when you check something. On a phone, dropping the bottom tab bar returns 64 pixels of height to the chart — which is the scarcest thing on the main surface. On desktop, removing the 220px sidebar and uncapping the layout width turns a 528-pixel-wide chart into roughly 760, even after a positions panel is added (the arithmetic is in section 2.4, and it is the difference between a chart that looks like a widget and one that looks like a terminal).

### Cost, in plain terms

Six phases. **Medium-to-high overall.** The app stays usable and reviewable after every one of them, and the change the client actually complained about — "I have to leave the screen to add money" — is delivered in **Phase 1**, before any of the risky structural work starts. If budget ran out after Phase 1, the complaint would already be answered.

The riskiest phase is **Phase 2**, migrating Portfolio into the first panel. Not because Portfolio is hard, but because it is where four new things meet for the first time: the panel component, the new URL scheme, the rebuilt price-feed subscription, and the three-column desktop layout. Everything in Phase 3 is a repeat of the pattern Phase 2 establishes, which is precisely why only one screen should go through it.

### Two things found in the code that change the plan

Both are invisible today and would be severe after migration, which is why they are Phase 0 rather than "fix it when it bites":

**The price feed would cut out when a panel closes.** `usePriceStream` builds its own WebSocket per component that calls it, and the socket's `close()` reports `offline` to the shared market store (`lib/socket.ts:108-114`). Today this is harmless because only one screen is mounted at a time. Once a Positions panel sits over a permanently-mounted Trade surface and both subscribe, closing the panel would tell the whole app the market feed is down — which greys the prices, shows the "Market feed offline" overlay, and disables the order ticket. The feed subscription has to become a single shell-owned thing before any panel exists.

**The URL becomes shared property, and two screens currently assume they own it.** Both `TradeScreen` (`:115`) and `PortfolioScreen` (`:175`) write query parameters with `setParams({ … }, { replace: true })`, which **replaces the entire query string** rather than merging into it. On separate routes that is fine. On one route carrying the chart's symbol *and* which panel is open *and* that panel's tab, it means switching instrument silently closes your positions panel, and switching the panel's tab silently resets your chart to the default symbol. It is a guaranteed bug that would be blamed on the panels.

### One thing that is not a code problem

Two items from the earlier passes are still outstanding and neither is fixed by this migration. The deployed price feed still needs its two dashboard values applied (Pass 1, Step 0), without which none of this can be demonstrated to the client on the live site. And **Withdraw remains unusable by every real user** — it is gated on a verified KYC status that no user can reach through the app — which matters more after this migration than before it, because Withdraw gains a permanent slot in the menu and the top bar.

---

## 2. Target architecture

### 2.1 The persistent main surface: yes, it is today's Trade screen, full stop

The honest answer to "what does the main surface contain, given there are no contract-type tabs to switch between" is that **it contains exactly what the Trade screen contains today, and the one-screen model changes its framing rather than its content.**

The benchmark's main area has an axis of variation — Matches/Differs, Even/Odd, Over/Under change what the digit row and the two contract buttons mean. TradeDerivPlus has no such axis. Pass 1 established why: this product's profit is proportional to price movement, with no payout ratio, no fixed return, and no expiry. There is nothing to switch between.

So the persistent surface has exactly two axes of variation, both of which already exist and are already URL- or state-backed:

- **Which instrument** — the `SymbolPicker`, backed by `?symbol=` (`TradeScreen.tsx:62`).
- **Which chart window** — the 1m/5m/15m segmented control Pass 1 restyled, held in component state.

Its content, unchanged from today: instrument header (name, code, category chip, live price, window change), chart range toggle, chart with its right-hand price scale, the latest-signal strip for the current symbol, the `SessionStrip`, open positions on the current symbol, and the order ticket.

This is worth stating plainly to the client because it sets expectations. **The main surface is more static than the benchmark's**, and no amount of navigation work changes that — the variation the benchmark gets from contract tabs, we can only get from the instrument picker and the desktop panel layout. If the client's mental image of "like the benchmark" includes the main area changing as you tap across the top, that image does not survive contact with this product's contract model, and it is better said now than discovered in review.

### 2.2 The new top bar

One row, 56px, at both breakpoints — the same height as today, so the sticky offsets described in section 5 can be fixed once rather than made conditional.

**Desktop (≥1024px)** — replacing both today's top bar and the 220px sidebar:

| Position | Item | Notes |
|---|---|---|
| Left | Logo mark + "TradeDerivPlus" wordmark | Links to the bare main surface, i.e. closes any open panel |
| Left, after the logo | **Positions · History · Signals** | The three content panels. Text links with icons, active styling when the panel is open |
| Right | `ConnectionDot` | Kept. We have a live feed indicator; the benchmark does not. `hideWhenIdle` as today |
| Right | Account pill **with balance** | Currently shows account type and number only — the balance appears nowhere in the shell. Opens the account switcher |
| Right | **Deposit** button (accent) | The single highest-value addition. Present on every screen |
| Right | Profile icon | Opens the Account panel |

Two deliberate differences from the benchmark's desktop row. It lists Deposit both as a text nav item *and* as an accent button on the right; we carry it once, as the button. And it has "AI" between History and Live Chat; we have Signals, named Signals, per the confirmed decision.

**Mobile (<1024px):**

| Position | Item |
|---|---|
| Left | Hamburger → nav drawer, then the logo mark (the wordmark is already `hidden sm:inline`, `TopBar.tsx:24`) |
| Centre | Account pill with balance |
| Right | `ConnectionDot`, then a compact **Deposit** button |

**Withdraw** does not get a top-bar slot at either width. It lives in the drawer on mobile and behind the account pill and Account panel on desktop. Reason: it is gated for every user today (section 1), it is used far less often than Deposit, and the space is genuinely contested — see below.

**The bell is dropped, not stubbed.** The prior investigation established there is no notification persistence anywhere: the events exist and already raise toasts, but nothing is stored, so there is no list for a bell to open. A bell that opens an empty panel is worse than no bell, and a disabled bell advertises something with no delivery date. It can be added in an afternoon once a notifications table exists; leaving a hole in the layout for it costs nothing today.

**Width crowding at 360px, with the arithmetic.** A 360px viewport minus the 16px gutters gives 328px. Hamburger 40 + logo mark 28 + 8 gap = **76px** on the left. Connection dot 8 + gap 12 + a `size="sm"` Deposit button at roughly 84px = **104px** on the right. That leaves about **148px** for the centre pill. Today's pill is a type badge plus an account number — around 120px — and a formatted balance like `$10,000.00` adds roughly 80px. It does not fit.

**The fix, decided rather than deferred: on mobile the pill shows the type badge and the balance, and drops the account number.** The number is still visible in the account switcher sheet and on the Account panel, and it is not what a trader is checking mid-session — the balance is. If that is still tight on the narrowest devices, the fallback is an icon-only Deposit button with an `aria-label`, which buys back about 50px.

### 2.3 The bottom bar: recommend removing it

The prior investigation's straw man was Trade / Positions / Menu. **Revised: drop the bottom bar entirely**, with a two-item bar as the fallback if the client wants the familiarity.

The reasoning, item by item:

- **"Trade" does not work as a bottom-bar item in this model.** Trade is not a destination any more — it is the background. A tab whose meaning is "close whatever is open" is not a peer of the others, and it would be the only tab that never highlights anything.
- **"Menu" is already the hamburger**, at the top left, where the benchmark puts it. Two triggers for one drawer is worse than one.
- **"Positions" is the only item that earns a slot** — and the need it serves is already met on the main surface. Pass 1's `SessionStrip` shows Open P/L and the open-position count under the chart, and the surface separately lists open positions on the current instrument. Constant awareness of positions is covered; the panel is for detail, and a drawer entry plus a desktop top-bar link is the right weight for detail.

Against that, the cost of keeping a bar is concrete: 64px of a phone's height, on the screen where the chart lives, contested by the sticky order ticket that already occupies the bottom edge (`TradeScreen.tsx:295`). Removing it lets the ticket sit at `bottom-0` and gives the chart the difference.

The deeper reason the benchmark's three-item bar does not transfer: its bottom bar exists because its **top** bar is full of account things (balance, deposit, bell) and its bottom bar holds session things (chat, bot, positions). Strip out Live Chat and the bot, as both confirmed decisions require, and there is no second category left to justify a second bar.

**Fallback, if the client wants a bottom bar:** two items, **Positions · Menu**, at a reduced height, with the drawer trigger removed from the top bar so there is still only one of each. Not recommended, but coherent.

### 2.4 Desktop layout, and the chart-width trap

This is the part that needs deciding before Phase 2, because it is easy to make the chart *worse* while adding a panel.

Today, `AppShell` caps everything at `max-w-[1200px]` (`AppShell.tsx:106`) and puts the 220px sidebar inside that cap, and `TradeScreen` lays out `lg:grid-cols-[1fr_380px]` with `lg:px-6` and a 24px gap (`TradeScreen.tsx:161`). Working it through: 1200 − 220 sidebar − 48 padding − 24 gap − 380 ticket = **about 528px of chart**.

Remove the sidebar and add a 280px positions panel with another 24px gap, keeping the 1200px cap: 1200 − 48 − 380 − 280 − 48 of gaps = **about 444px**. The chart would be *narrower than it is today*, and Pass 1's price scale and window-change work would be the first casualties.

The benchmark does not have this problem because **it is not capped** — it uses the full viewport. Measuring its screenshot at 1024px wide: positions panel about 19% of the width, chart about 53%, controls about 25%.

**So the recommendation is to let the main surface go full-width** (or raise the cap substantially, e.g. 1600px) rather than sit inside the 1200px reading-width cap that suits Home and Wallet but not a trading terminal. At 1440px full-width with the same proportions: 275px panel, ~760px chart, 380px ticket — the chart ends up roughly 45% wider than today rather than 16% narrower. Panels and sheets keep a comfortable reading width of their own; only the main surface uncaps.

### 2.5 What used to be a route and is now an overlay

In prose, because it is a small enough structure to say out loud.

There is **one route for the signed-in app**, `/`, and it always renders the Trade surface. Over it, four panels can be open, one at a time, each identified by a `?panel=` value on that same route: `positions`, `history`, `signals`, `account`. A panel is a left-hand column on desktop and a full-height sheet on mobile, built from the existing Radix dialog that `ui/Sheet` already wraps. Because the panel is in the URL, the browser back button closes it, a support agent can send a link that opens it, and the login redirect — which already round-trips `pathname + search` through `?next=` (`AppShell.tsx:62`) — carries the panel state through a session expiry for free.

Under the panels, nothing changes: the chart keeps ticking, the symbol stays selected, the order ticket keeps its state.

Separately from the panels there are **sheets**, which are flows rather than content and stay exactly as they are today — ephemeral React state, no URL: deposit, withdraw, the account switcher, the symbol picker, the transaction detail, the close-position confirmation and the large-stake confirmation. Plus, on mobile only, the navigation drawer, which is a menu rather than content and is likewise ephemeral.

The old routes do not simply vanish. `/trade`, `/portfolio`, `/wallet`, `/signals` and `/profile` each become an explicit redirect onto `/` with the right parameters, mapping `?tab=` where it exists, because live links already point at them — `signalHref()` emits `/trade?symbol=…&direction=…` from three call sites (`SignalCard.tsx:23-32`), and users have bookmarks. Relying on the existing catch-all (`App.tsx:84`) is not good enough: it would drop them on the bare main surface with their parameters silently discarded.

`/login`, `/register` and the whole `/admin/*` console are untouched by this migration.

---

## 3. Per-screen disposition

| Current screen | Becomes | Where its content lives | Route survives? |
|---|---|---|---|
| **Home** (`/`) | **Dissolved.** Not a panel — taken apart, with most pieces deleted as duplicates | See the breakdown below | **Y** as a path — `/` now renders the Trade surface |
| ├ `BalanceCard` | Kept as a component, no longer on a screen of its own | Already reused at the top of the History panel (today's `WalletScreen.tsx:59-69`). Its balance figure moves into the top-bar account pill; its Deposit and Withdraw buttons become the top-bar button and the drawer entries | — |
| ├ `AccountStrip` | **Deleted** | The account switcher sheet already does this job better — it lists each account *with its balance* (`AccountSwitcher.tsx:13-45`), which the chips do not | — |
| ├ `OpenPositionsSummary` | **Deleted** | A three-row preview of a panel that is now one tap away. The main surface separately lists positions on the current instrument, and `SessionStrip` carries the open count and open P/L | — |
| ├ `RecentActivity` | **Deleted** (wrapper only) | It renders five `TransactionRow`s — the same component the History panel renders fifty of, with filters | — |
| └ latest-signal `SignalCard` | **Deleted** (the Home hero instance) | The main surface already has a signal strip for the current instrument (`TradeScreen.tsx:234-247`); the Signals panel carries the full list. `SignalCard` itself stays, used by the panel | — |
| **Trade** (`/trade`) | **The persistent main surface** | Unchanged in content and largely unchanged in code. Only its wrapper moves: `sticky bottom-tabbar` → `sticky bottom-0` once the tab bar is gone | **Y** as a redirect to `/`, preserving the query string — `signalHref()` URLs depend on it |
| **Portfolio** (`/portfolio`) | **Positions panel** — desktop left column, mobile full-height sheet | Whole screen moves nearly intact: the three summary tiles, the Open/Closed tabs, `PositionCard`, `ClosedTradeRow`, the day groupings, the close confirmation. Its `?tab=` becomes a parameter on the single route | **Y** as a redirect: `/portfolio?tab=closed` → `/?panel=positions&tab=closed` |
| **Wallet** (`/wallet`) | **History panel** (the benchmark's name for it) | Whole screen moves intact: `BalanceCard`, the four filter chips, the transaction list with pending deposits pinned, `TransactionDetail`. Filter state stays local, as today | **Y** as a redirect → `/?panel=history` |
| **Signals** (`/signals`) | **Signals panel** — and the honest occupant of the benchmark's "AI" slot, under its own name | Whole screen moves: the symbol chips, the card grid, the "new signals" pill. One real fix needed — its scroll listener, see section 5 | **Y** as a redirect → `/?panel=signals` |
| **Profile** (`/profile`) | **Account panel** (the benchmark's name) | Whole screen moves: the identity rows, the KYC card, the accounts list, Log Out. If the client ever supplies support contacts, this is where the benchmark's drawer-footer contacts would go | **Y** as a redirect → `/?panel=account` |

**The point worth showing a client from this table:** four of the six screens move essentially whole, as containers change around them. Only Home is genuinely destroyed, and Home is destroyed because it was a summary of the other five.

---

## 4. Migration phases

Each phase leaves the app working, reviewable, and shippable. Each has a verification checkpoint that does not depend on the phases after it.

### Phase 0 — Prerequisites, with no visible change

**Built:**

1. **One shell-owned price subscription.** `usePriceStream` moves out of individual screens and into `AppShell`, with the wanted-symbol set merged from whoever needs it (the current instrument, plus every symbol with an open position) rather than each caller opening its own socket. This is the fix for the "closing a panel kills the feed" problem in section 1.
2. **Parameter merging.** `TradeScreen.tsx:115` and `PortfolioScreen.tsx:175` stop replacing the query string and start merging into it.
3. **Sticky offsets decoupled from the top bar's height.** The two hand-matched magic numbers (`PortfolioScreen.tsx:228` `top-14`, `SignalsScreen.tsx:77` `top-16`) get a single shared source.
4. **Distinct landmark names.** `TabBar` and `Sidebar` stop both being `aria-label="Main"`, and the six e2e selectors that paper over the collision with `.first()` are updated. Doing this *before* the nav changes shape means the test changes in Phase 4 are about the new structure, not about a pre-existing smell.

**Still using the old structure:** everything. No user-visible change at all.

**Verification:** the app looks and behaves identically. `npm run typecheck`, `npm test` and the full Playwright suite stay green. Manually: switch instrument and confirm the URL keeps its other parameters; walk Home → Trade → Portfolio → Home and confirm the connection dot never drops to offline and the ticket never disables.

**Complexity: Medium.** Small in lines, but it touches the live feed, which is the app's heartbeat.

### Phase 1 — New shell chrome, old routes intact

**Built:**

- The new `TopBar` at both breakpoints per section 2.2: hamburger on mobile, the Positions/History/Signals group on desktop, account pill with balance, Deposit button, profile icon. No bell.
- The mobile nav drawer — Radix dialog, left-anchored, listing Deposit, Withdraw, History, Signals, Account, Log Out. **Its items still navigate to the existing routes.**
- `DepositSheet`, `WithdrawSheet` and `AccountSwitcher` hoisted into `AppShell` and mounted once, with the duplicate mounts removed from `HomeScreen` (`:131-155`) and `WalletScreen` (`:128-144`). The in-page buttons call the shell's openers. This needs `useWallet(accountId)` in the shell, which shares a React Query key with the screens already calling it, so it adds no network traffic.
- The desktop `Sidebar` is removed in this phase, not a later one. Running a sidebar and a top-bar nav group simultaneously would be confusing to review and worse to use.

**Still using the old structure:** every screen is still a route; the mobile `TabBar` is still there; no panels exist yet.

**Verification:** every destination reachable from the new chrome at both widths. Deposit opens from the Trade screen, from Portfolio, from Signals — from everywhere. The Playwright desktop project needs its nav selectors pointed at the top bar; the mobile project still uses the tab bar.

**Complexity: Medium.** This is the first client-visible step and **it already answers the complaint that started Pass 2.**

### Phase 2 — The panel primitive, and Portfolio through it

**Built:**

- The panel component: either a `side` prop on `ui/Sheet` or a sibling that shares its overlay and animation. Left-anchored on desktop as a column, full-height sheet on mobile (`Sheet` already has a `full` variant). `tailwindcss-animate` is already installed and provides `slide-in-from-left`, so no new keyframe and no new dependency.
- The `?panel=` scheme and a small hook to read and write it, merging rather than replacing (which Phase 0 made safe).
- The desktop three-column layout and the width decision from section 2.4.
- `PortfolioScreen` migrated into the Positions panel, keeping its Open/Closed tabs and its `?tab=` parameter.
- `/portfolio` becomes a redirect mapping `?tab=`.
- The inbound references fixed: the "View in Portfolio" toast action (`TradeScreen.tsx:147`) opens the panel instead of navigating, and the panel's empty-state "Go to Trade" link (`PortfolioScreen.tsx:196`) becomes "Close".

**Still using the old structure:** Wallet, Signals and Profile are still routes; Home is still Home; the mobile `TabBar` is still there, now with Portfolio removed from it or pointed at the panel.

**Verification:** `/portfolio?tab=closed` lands on the open panel with the right tab. Back closes the panel and returns to the bare chart. The chart keeps ticking with the panel open, **and the ticket stays enabled after the panel closes** — the specific regression Phase 0 exists to prevent, and the thing to check hardest here. On desktop, measure the chart: it should be wider than today's 528px, not narrower.

**Complexity: High. This is the riskiest phase**, because it is the only one where four new things meet at once — the panel component, the URL scheme, the rebuilt feed subscription under real concurrent load, and the new desktop grid. Phase 3 is deliberately just repetition of whatever this phase settles.

### Phase 3 — The remaining three screens into panels

**Built:** Wallet → History panel; Signals → Signals panel; Profile → Account panel. Three separate, independently reviewable pieces of work following Phase 2's pattern. Redirects for each old route. `SignalsScreen`'s window-scroll listener rebound to the panel's scroll container (section 5).

**Still using the old structure:** Home, and the mobile `TabBar`.

**Verification:** each panel deep-links and back-buttons correctly; the transaction detail sheet still opens from inside the History panel (a sheet over a panel — worth checking the focus trap and the stacking explicitly, since it is the first nested case).

**Complexity: Medium** for all three (Low each, once Phase 2 has set the pattern).

### Phase 4 — Dissolve Home, remove the bottom bar, own the URLs

**Built:**

- `HomeScreen`, `AccountStrip`, `OpenPositionsSummary` and `RecentActivity` deleted per section 3. `BalanceCard` kept — the History panel uses it.
- `/` renders the Trade surface; `/trade` redirects to `/` preserving its query string; `signalHref()` emits `/` with the same parameters.
- `TabBar` removed. The order ticket moves to `sticky bottom-0`; `AppShell`'s bottom padding and the toast offset stop compensating for a bar that no longer exists.
- `DepositSheet`'s "Start trading" button (`:266-271`) closes the sheet instead of navigating to `/trade`.

**Still using the old structure:** nothing.

**Verification:** the rewritten e2e suite (section 6) passes at both widths. Every old URL redirects rather than silently landing on the bare surface. Nothing renders a blank screen.

**Complexity: Medium.** Small in code, but this is where every e2e navigation selector changes, so the test rewrite belongs *in* this phase rather than after it.

### Phase 5 — Cleanup

**Built:** remove `spacing.tabbar` if nothing references it; decide the fate of `PageHeader` (panels supply their own headers through `Sheet`'s title row) and of Pass 1's unused `section` 20px type step, which panel headers are the natural home for — closing out Pass 1 follow-up #2. Sweep any now-dead imports.

**Complexity: Low.**

---

## 5. Breakage list

Everything found in the codebase that is coupled to the current screen-and-route structure. Ordered by how badly it fails and how quietly.

| # | What breaks | Where | Why | Fix |
|---|---|---|---|---|
| 1 | **The price feed reports offline when a panel closes**, greying prices and disabling the order ticket | `hooks/usePriceStream.ts:29-60`, `lib/socket.ts:108-114` | Each calling component builds its own socket, and `close()` calls `onStatus('offline')` on the shared market store. Only safe today because one screen mounts at a time | One shell-owned subscription with a merged symbol set. **Phase 0** |
| 2 | **Switching instrument closes your panel; switching a panel tab resets your chart** | `TradeScreen.tsx:115`, `PortfolioScreen.tsx:175` | Both write with `setParams({ … }, { replace: true })`, replacing the whole query string. Harmless on separate routes, fatal on one | Merge into existing parameters. **Phase 0** |
| 3 | Sticky headings land in the wrong place inside panels | `PortfolioScreen.tsx:228` (`sticky top-14`), `SignalsScreen.tsx:77` (`sticky top-16`) | Both are magic numbers hand-matched to the top bar's `h-14`. A panel scrolls its own container, so "56px from the top of the window" is not where the panel's header is | `top-0` relative to the panel, from a shared source rather than a literal. **Phase 0** |
| 4 | An 80px dead gap at the foot of every screen, and the order ticket floating 64px above the bottom edge | `spacing.tabbar` has four consumers: `TabBar.tsx:24` (height), `AppShell.tsx:108` (padding), `TradeScreen.tsx:295` (`bottom-tabbar`), `ui/Toast.tsx:120` (offset) | Removing the bar without updating all four | Update all four together in **Phase 4**; drop the token in Phase 5 |
| 5 | The "N new signals" pill never appears, or never dismisses | `SignalsScreen.tsx:26-33` | Listens on `window.scrollY`. Inside a panel the window does not scroll — the panel's container does | Bind to the panel's scroll container via a ref. **Phase 3** |
| 6 | Two navigation landmarks with the same accessible name | `TabBar.tsx:22` and `:55`, both `aria-label="Main"` | Both are in the DOM, one display-hidden. This is why all six e2e nav selectors need `.first()` | Distinct names before the nav changes shape. **Phase 0** |
| 7 | e2e navigation stops resolving | `user.trading.spec.ts:14,29,34,44`; `user.deposit.spec.ts:10,50` | Select by `getByRole('navigation', { name: 'Main' })` then a link named Home/Trade/Portfolio/Wallet | Rewrite with breakpoint-aware helpers. **Phase 4** — see section 6 |
| 8 | The signup assertion fails on a screen that no longer has a balance card | `e2e/helpers.ts` `register()` waits for `**/`, then specs assert `section[aria-label="Balance"]` (`user.trading.spec.ts:9-12,30-32,45`) | After migration `/` is the chart, which has no balance card | Assert the balance in the top-bar pill, or open the History panel. **Phase 4** |
| 9 | Signal deep links point at a route that has become a redirect | `signalHref()` `SignalCard.tsx:23-32`, called from `SignalCard` (both variants) and `TradeScreen.tsx:238` | Emits `/trade?symbol=…&direction=…&sl=…&tp=…&expired=1` | Emit `/` with the same parameters, and **keep the `/trade` redirect permanently** for links already in the wild. **Phase 4** |
| 10 | A toast action navigates to a route that no longer exists | `TradeScreen.tsx:147` — "View in Portfolio" | Calls `navigate('/portfolio')` | Open the Positions panel. **Phase 2** |
| 11 | The deposit success step navigates away from the app's only screen | `DepositSheet.tsx:266-271` — "Start trading" | Calls `navigate('/trade')` | Close the sheet. **Phase 4** |
| 12 | An empty-state link to a route that is now the background | `PortfolioScreen.tsx:196` — "Go to Trade" | `<Link to="/trade">` | "Close". **Phase 2** |
| 13 | Home's three in-page links die with Home | `HomeScreen.tsx:82` (`/signals`), `OpenPositionsSummary.tsx:40` (`/portfolio`), `RecentActivity.tsx:21` (`/wallet`) | The components are deleted | Confirm nothing else imports them before deleting — `RecentActivity` and `OpenPositionsSummary` are currently Home-only. **Phase 4** |
| 14 | Duplicate sheet mounts multiply instead of consolidating | `DepositSheet`/`WithdrawSheet` in both `HomeScreen.tsx:139-155` and `WalletScreen.tsx:128-144`; `AccountSwitcher` in both `AppShell.tsx:115-121` and `HomeScreen.tsx:131-137` | Hoisting without removing leaves two of each mounted | Hoist *and* delete, in the same commit. **Phase 1** |
| 15 | Two `/signals` sockets open at once | `AppShell.tsx:32` and `SignalsScreen.tsx:22` both call `useSignalStream` | Already true today, and harmless only because that hook's `onStatus` is a deliberate no-op (`useSignalStream.ts:45-48`). The same pattern with `usePriceStream` is breakage #1 | Fold into the Phase 0 socket consolidation |
| 16 | The account-error banner's placement becomes ambiguous | `AppShell.tsx:91-104` | It currently sits full-width between the top bar and the content. With a persistent desktop left panel, "between" has two meanings | Decide: full-width above everything, or inside the main column only. **Phase 2** |
| 17 | `PageHeader` assumes a page | Used by Portfolio, Wallet, Profile, Signals (`features/shell/PageHeader.tsx`) | Panels get their title from `Sheet`'s header row, so two headers would stack | Drop it from migrated screens; decide its fate in **Phase 5** |
| 18 | Toasts and the order ticket compete for the bottom edge on mobile | `ui/Toast.tsx:120` (80px) versus `TradeScreen.tsx:295` (64px) | They already overlap today on the Trade screen; once the ticket sits at `bottom-0` the overlap changes shape | Re-measure once the bar is gone. **Phase 4** |
| 19 | Nested overlay behaviour is untested | `TransactionDetail` inside the History panel; the close-position confirmation inside the Positions panel | A Radix dialog inside a Radix dialog — focus trap, Escape precedence, scroll lock and stacking all need confirming | Verify explicitly at **Phase 3**, the first nested case |
| 20 | User e2e specs run at both widths, and the mobile nav will be behind a closed drawer | `playwright.config.ts` — the `desktop` project has no `testMatch`, so it runs the user specs too | Today `.first()` hides the difference because both bars are in the DOM. After migration the desktop nav is inline and the mobile nav is inside a drawer that must be opened first | Breakpoint-aware helper. **Phase 4** — the largest single test change |

---

## 6. Test impact

Existing test files, and what happens to each.

| File | Verdict | Why |
|---|---|---|
| `src/lib/format.test.ts` | **Passes unmodified** | Pure formatting functions |
| `src/lib/pnl.test.ts` | **Passes unmodified** | Pure arithmetic |
| `src/features/trading/sessionStats.test.ts` | **Passes unmodified** | Pass 1's four cases exercise a pure function over a closed-trade array. No routing, no DOM, no shell |
| `src/features/trading/OrderTicket.test.tsx` | **Passes unmodified** | Pass 1's ticket is a pure component driven entirely by props. Its wrapper's positioning changes; the component does not |
| `src/features/wallet/DepositSheet.test.tsx` | **Passes unmodified** | Already renders inside a `MemoryRouter` with the wallet API and rate lookup stubbed. Hoisting where the sheet is *mounted* does not change the props it is tested with |
| `src/features/admin/components/ReasonDialog.test.tsx` | **Passes unmodified** | Admin console, untouched by this migration |
| `e2e/helpers.ts` | **Changed** | `register()`'s post-signup landing expectation (breakage #8), plus a new breakpoint-aware navigation helper — on desktop click the top bar, on mobile open the drawer first (breakage #20) |
| `e2e/user.trading.spec.ts` | **Rewritten** | Four nav clicks, three balance assertions against Home's balance card, and one `page.goto('/trade')`. The trading flow itself — stake, submit, confirm, close — survives unchanged |
| `e2e/user.deposit.spec.ts` | **Rewritten** | Two nav clicks. The M-Pesa flow assertions survive unchanged. Note that "Deposit" and "Top up demo" will now be reachable from the top bar, which actually *shortens* both tests |
| `e2e/admin.spec.ts` | **Passes unmodified** | Separate surface. Its two known failures are the pre-existing rate-limit issue diagnosed in Pass 1, unrelated to this work |

**Worth adding, and cheap while the structure is fresh:**

1. A **panel URL round-trip** test: deep-link `/?panel=positions&tab=closed`, assert the panel and tab, press back, assert the bare surface. This is the guarantee the whole `?panel=` scheme rests on.
2. A **feed-survives-a-panel** test: open a panel, close it, assert the order ticket is still enabled and the connection dot still green. This is breakage #1's regression test, and without it that bug can silently return with any future refactor.
3. A **redirect** test over the five old paths, including `/portfolio?tab=closed` mapping its parameter across.

None of these exist today; all three are Phase 4 work alongside the rewrite.

---

## 7. Risk to Pass 1 work

The short answer is that Pass 1 is the least endangered work in the codebase, because it happened to build the thing that becomes the app.

**Survives completely unchanged** — every one of these is pure or presentational, driven by props, with no knowledge of routes or shell:

- `PriceChart.tsx` (the right-hand price scale, span-based axis precision, the current-price marker), `chartTheme.ts`, `ChartSkeleton.tsx`, `PriceChartLazy.tsx`
- `ChartRangeToggle.tsx` (the segmented control with `aria-pressed`) and `chartRange.ts` (`windowTicks`, `windowChange`)
- `OrderTicket.tsx` — the projected-P/L-per-level hints, the "1% move is worth" line, and the `+ Set a level` restyle
- `sessionStats.ts` and its tests. Its session-start module constant survives a panel open and close for the same reason it survived navigation: it is module-level, not component state. If anything it gets *more* reliable, because the Trade surface — and therefore the closed-trade query it derives from — is now always mounted
- The token additions: `nano`, `section`, `shadow-card`, `shadow-elevated`
- The error and empty states added to Home's open-positions block and Portfolio's summary tiles — although Home's instance disappears with Home, so that specific one is deleted rather than broken

**Needs repositioning, minor:**

- The order ticket's wrapper, `sticky bottom-tabbar` → `sticky bottom-0` (`TradeScreen.tsx:295`).
- The chart's fixed heights (`h-[240px]` mobile, `lg:h-[340px]`) are worth revisiting once mobile reclaims 64px from the tab bar and desktop changes width. Not broken, just under-using new space.

**The one real risk, and it is a layout risk rather than a code risk:** the desktop chart width arithmetic in section 2.4. Adding a positions panel inside the existing 1200px cap makes the chart *narrower* than today, which would quietly undo the readability Pass 1 bought with the price scale. Uncapping the main surface turns that into a large gain instead. **This needs deciding before Phase 2, not during it.**

### Where `SessionStrip` should live

The benchmark puts its session figures at the foot of the desktop positions panel, and the brief asks whether ours should follow.

**Recommendation: leave it where Pass 1 put it, under the chart on the main surface.** The benchmark can afford foot-of-panel because its panel is persistent on desktop, so those figures are always on screen. In this plan the panel is dismissible and, on mobile, entirely hidden until opened — so moving the session figures there would hide them exactly when the user is trading, which inverts the reason Pass 1 built the strip ("open profit and loss, open positions, and what this session has done, without a trip to Portfolio"). Keeping it on the main surface is both truer to Pass 1's intent and the more one-screen choice.

It is a client-visible design decision rather than a technical one, so it is listed again in the open questions.

One genuine redundancy to keep an eye on rather than act on now: the main surface's "Your positions on R_75" block overlaps conceptually with the Positions panel. They are not the same — one is instrument-scoped and inline, the other is everything — but if the panel proves quick enough to open, the inline block may stop earning its space. Worth a look after Phase 2, not a decision now.

---

## 8. Effort estimate

| Phase | What it delivers | Complexity | Depends on |
|---|---|---|---|
| **0 — Prerequisites** | One shell-owned price subscription; parameter merging; decoupled sticky offsets; distinct landmark names. No visible change | **Medium** | Nothing |
| **1 — Shell chrome** | New top bar at both widths, mobile drawer, hoisted deposit/withdraw/switcher, sidebar removed. **Answers the original complaint** | **Medium** | Phase 0 (parameter merging) |
| **2 — Panel primitive + Positions** | The panel component, the `?panel=` scheme, the three-column desktop layout, Portfolio migrated | **High — riskiest phase** | Phases 0 and 1 |
| **3 — History, Signals, Account panels** | The remaining three screens, one reviewable piece each | **Medium** (Low each) | Phase 2 |
| **4 — Dissolve Home, drop the bottom bar, redirects, test rewrite** | Home deleted, `/` becomes Trade, old URLs redirect, e2e rewritten | **Medium** | Phase 3 |
| **5 — Cleanup** | Dead tokens and components removed; Pass 1's unused `section` step resolved | **Low** | Phase 4 |

**Overall: Medium-to-High.** For a client conversation, the honest framing is that this is not a rewrite of the app — the API, the data layer, the design system, the money flows and the main trading surface all survive untouched. It is a rewrite of the app's **navigation, its URL scheme, and the containers around four of its six screens**. The two genuinely hard parts are the panel primitive (Phase 2) and the fact that the price feed and the URL both become shared resources (Phase 0), and both are known in advance rather than discovered late.

Two costs that sit outside this table and are worth naming so they are not a surprise: the deployed feed configuration fix still outstanding from Pass 1, without which none of this is demonstrable on the live site; and the KYC gap that makes Withdraw unusable, which this migration makes more prominent rather than fixing.

---

## 9. Open questions

1. **Is dropping the mobile bottom bar acceptable?** Recommended in section 2.3 — every destination is one tap from the top bar or drawer, and the 64px goes to the chart. The fallback is a two-item Positions · Menu bar. This is a visible change to how the app feels on a phone and should not be decided by engineering alone.

2. **Confirm the notification bell is dropped rather than stubbed.** There is nothing behind it and no notifications table. Recommended: drop it, add it when there is something to show.

3. **Is the desktop positions panel open by default and persistent (benchmark-style), or closed and dismissible?** This decides whether the desktop layout is two columns or three most of the time, and it feeds directly into the next question.

4. **Can the main surface break out of the 1200px width cap?** Section 2.4 shows the chart shrinking from about 528px to about 444px if a panel is added inside the current cap, versus growing to about 760px if the surface goes full-width at 1440. Strong recommendation to uncap, but it changes how the app looks on a large monitor and is a client-facing decision.

5. **Does the client accept that Home disappears?** Today it is where a user lands after login. After this migration they land on the chart. Some users expect a "hub" that summarises before they commit to a screen; the benchmark has none, but the benchmark's users arrive expecting a terminal.

6. **Should the app live at `/` or at `/trade`?** This plan assumes `/` with `/trade` redirecting, which makes the shortest URL the app. If marketing or existing links favour `/trade`, the redirect reverses at no cost — but it should be settled before Phase 4 rather than after.

7. **`SessionStrip` under the chart, or at the foot of the positions panel?** Section 7 recommends under the chart, because our panel is dismissible and mobile-hidden where the benchmark's is persistent. The benchmark does it the other way, so this is worth confirming against whatever the client actually liked about that screenshot.

8. **What replaces Home's latest-signal card for discovery?** The main surface's signal strip only shows signals for the currently-selected instrument, so a strong signal on another instrument becomes invisible until the user opens the Signals panel. Signal toasts already fire app-wide (`AppShell.tsx:32-37`), which may be enough — but it is a real reduction in visibility and should be a conscious one.

9. **Is the mobile account pill acceptable without the account number?** Section 2.2 trades the number for the balance because both do not fit at 360px. The number stays visible in the switcher and the Account panel.

10. **Withdraw and KYC.** Carried forward unresolved from Pass 2a: no user can reach a verified status through the app, so Withdraw always refuses. This migration gives it a permanent slot in the drawer. Is KYC submission scheduled, or should Withdraw be presented differently until it is?

11. **Deployed feed fix.** Also carried forward: until the two dashboard values from Pass 1 Step 0 are applied and Vercel is redeployed, the live site shows "Market feed offline" over a dead chart — so none of this work can be demonstrated to the client on the real site regardless of how well it is built.

12. **Are How to Trade and Refer & Earn wanted in the nav after all?** Both were confirmed absent in Pass 2a and neither is in this plan. If either comes back, it changes the top bar's width budget on desktop and adds rows to the mobile drawer — cheap to accommodate now, more expensive once the layout is settled.
