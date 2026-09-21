# TradeDerivPlus — Navigation & Layout Restructure Investigation (Pass 2)

**Type:** Investigation only. No implementation code, styling, or component changes were made in this pass.
**Date:** 21 September 2026
**Prior context:** [`ui-polish-investigation.md`](./ui-polish-investigation.md) (Pass 1 investigation) and [`ui-polish-implementation.md`](../implementation-reports/ui-polish-implementation.md) (Pass 1 build). Nothing in this report revisits or revises Pass 1's chart, ticket, or stats work.

**Benchmark references** (all three now committed at the paths the brief named):

| File | What it shows |
|---|---|
| [`docs/reference/tnnbinary-mobile-menu.jpeg`](../reference/tnnbinary-mobile-menu.jpeg) | Mobile hamburger drawer |
| [`docs/reference/tnnbinary-mobile-trade.jpeg`](../reference/tnnbinary-mobile-trade.jpeg) | Mobile trade screen: top bar, contract tabs, chart, bottom bar |
| [`docs/reference/tnnbinary-trade-benchmark.png`](../reference/tnnbinary-trade-benchmark.png) | Desktop trade screen (carried over from Pass 1) |

The two mobile images arrived as PNG screenshots and were converted to JPEG to match the filenames the brief specified. No cropping or editing.

---

## 1. Summary

### The assumption you need to correct if it's wrong

The client attached the two mobile screenshots with the words *"this is how they look in web."* Those images are phone screenshots — the Android status bar, the Chrome mobile address bar, and the home-gesture pill are all visible in them. **This report therefore assumes the client meant "this is how it looks on the site," not "this is the desktop layout," and treats both the mobile pattern and the existing desktop screenshot as in scope.** If the client actually wants the *desktop* site restructured to look like those phone screenshots — a hamburger and a three-item bottom bar on a 1400px-wide monitor — that is a different and much stranger brief, and this report would need redoing. Please confirm before any design work starts.

### A second correction, this one about the desktop screenshot

The brief describes the desktop benchmark as having *"a persistent left sidebar (Trader's Hub, Deposit, Withdraw, History, AI, Live Chat, How to Trade) plus a top bar."* Reading the screenshot directly, that is not what it does. **All of those items sit in one horizontal row across the top**, next to the logo. The left-hand column of the desktop benchmark is not navigation at all — it is a positions panel with Open / Closed / History tabs, an empty state reading "No open positions," and session figures pinned to its foot ("Session: 0 trades (0W / 0L)", "Session P/L: +0.00 USD"). It even has a close button, so it is a dismissible panel rather than a fixture.

This matters because it changes what "match the benchmark on desktop" means. The benchmark's desktop has **no navigation sidebar whatsoever**; TradeDerivPlus's desktop sidebar (Home, Trade, Portfolio, Wallet) has no counterpart in it. Worth noting too: those session figures in the benchmark's left panel are the same four numbers Pass 1 already built as the `SessionStrip` on the Trade screen, so that particular piece of the benchmark is already done, just positioned differently.

### What's actually going on with the complaint

The client's stated complaint is that *"the Home screen still requires navigating away to do most actions — deposit, withdraw, check history."* Taken literally, that is not correct, and the client's own screenshot of TradeDerivPlus shows why: **Home already has Deposit and Withdraw as full-width buttons in the balance card**, and both open as sheets over the current screen without navigating anywhere. Recent activity and open positions are on Home too.

What *is* true, and is almost certainly what the client is reacting to, is three separate things that feel like one:

1. **Those buttons exist on Home and Wallet only.** On the Trade screen, Portfolio, Signals, or Profile there is no way to add money without first tapping back to Home or Wallet. The benchmark has a Deposit button in the top bar that is present on every screen, plus a drawer reachable from every screen.
2. **There is no single place that lists everything.** TradeDerivPlus's destinations are scattered: four in the bottom bar, two more in the top bar (the account pill and a profile icon), and the Signals screen has no navigation entry at all — it is reachable only through a "See all" link on Home. The benchmark puts seven items, two support contacts, and Log Out in one drawer.
3. **Several benchmark menu entries have no equivalent here to reach.** How to Trade, Refer & Earn, and Live Chat do not exist in TradeDerivPlus in any form — not as a screen, not as an endpoint, not as a database table. Four of the benchmark drawer's seven rows are features rather than navigation.

Underneath all of it is a structural difference worth naming plainly, because it explains the feeling better than any individual button does. **The benchmark is a one-screen app with overlays. TradeDerivPlus is a four-screen app with tabs.** The benchmark never makes you "navigate away" because it never navigates at all — the trade screen is the app, and everything else is a drawer, a panel, or a modal on top of it. Copying its navigation without adopting that model would give you a hamburger menu that still changes screens, which solves the cosmetics and not the complaint.

### Cost, in one paragraph

The mechanical part is cheap. Navigation lives in exactly two components (`TabBar.tsx` exports both the mobile bar and the desktop sidebar from one shared array, and `AppShell.tsx` renders them), the route table in `App.tsx` is flat with no nesting, and Radix's dialog primitive is already a dependency wrapped as `ui/Sheet` — a left-hand drawer is that same primitive with different position classes and no new library. The single highest-value change identified, making Deposit and Withdraw reachable from every screen, needs no new UI at all: it is moving two already-built sheets from the two screens that own copies of them up into the shell, where the account data they need is already loaded. The expensive part is not code, it is deciding what the menu should contain, given that half the benchmark's menu is features TradeDerivPlus has never had.

### One blocker found on the way

**Withdraw is effectively unreachable for every real user, and it has nothing to do with navigation.** The button is gated on `kyc_status === 'verified'`, and there is no way for a user to submit verification: the Profile screen's "Start verification" button is hard-disabled with the caption "Verification is not available yet," and the API has no user-facing KYC submission route — only admin-side approve and reject. So a Withdraw entry in a new menu would open a sheet that every user is blocked from using. This should be understood before Withdraw is given more prominence, not after.

---

## 2. Current navigation inventory

### 2.1 The shell — `web/src/features/shell/AppShell.tsx` (124 lines)

`AppShell` is the single layout wrapper for every authenticated user screen. It is mounted as a layout route in `App.tsx:60-67` with six children rendered through `<Outlet />`: `/`, `/trade`, `/portfolio`, `/wallet`, `/signals`, `/profile`. The admin console at `/admin/*` sits outside it with its own shell and is not part of this pass.

What the shell owns, in order:

1. **Auth gate** (`:61-64`) — unauthenticated users are redirected to `/login?next=<current path + search>`, so deep links survive a login round trip.
2. **Account context** — `useActiveAccount()` resolves the active account, its id, and the full account list; every screen reads from this hook rather than the store.
3. **Two long-lived WebSocket subscriptions** — `useSignalStream` raises a toast on a new signal, `useAccountStream` raises one on a settled deposit or an auto-closed position. Both live here specifically so an event reaches the user on any screen.
4. **An account-less escape hatch** (`:67-85`) — if the user somehow has zero accounts, the whole shell is replaced by a "Create your first account" empty state.
5. **The layout itself** (`:87-123`):

```87:113:web/src/features/shell/AppShell.tsx
    <div className="flex min-h-full flex-col">
      <TopBar account={account} isLoading={isLoading} onOpenSwitcher={() => setSwitcherOpen(true)} />
      {/* ... account-load error banner ... */}
      <div className="mx-auto flex w-full max-w-[1200px] flex-1">
        <Sidebar />
        <main className="min-w-0 flex-1 pb-[calc(theme(spacing.tabbar)+16px)] lg:pb-8">
          <Outlet />
        </main>
      </div>
      <TabBar />
```

6. **One overlay** — `AccountSwitcher`, opened by the top bar. It is the only sheet the shell currently owns.

**The breakpoint is Tailwind's default `lg`, 1024px, and it is the only one navigation uses.** Below 1024px: bottom `TabBar`, no sidebar, `main` padded by 80px at the foot to clear the bar. At 1024px and up: 220px `Sidebar`, no bottom bar, 32px foot padding. There is no intermediate tablet treatment — at 1023px you get the phone layout at full width.

An account-loading failure renders a dismissable-free banner between the top bar and the content with a Retry (`:91-104`), which is worth knowing because any new chrome added between those two elements has to coexist with it.

### 2.2 Top bar — `web/src/features/shell/TopBar.tsx` (56 lines)

A single `sticky top-0 z-30` header, `h-14` (56px), with the same three slots at every width — there is no breakpoint-specific behaviour except one text label:

| Slot | Contents | Behaviour |
|---|---|---|
| Left | Accent-red square logo mark + wordmark "TradeDerivPlus" | Links to `/`. **The wordmark is `hidden sm:inline`** — below 640px only the mark shows |
| Centre | `AccountPill` (account type badge + account number) + chevron | Button, opens `AccountSwitcher`. Skeleton while accounts load, an em dash if there is no account |
| Right | `ConnectionDot` (`hideWhenIdle`, so invisible when the feed is healthy) + a person icon linking to `/profile` | The person icon is the only route into Profile anywhere in the app |

Against the benchmark's top bar, TradeDerivPlus is missing: the hamburger trigger, the balance figure (the pill shows account type and number, **not** money — the balance only appears inside the account switcher sheet and on Home/Wallet), a Deposit button, a notification bell, and a sound toggle. It has one thing the benchmark does not: a live connection indicator.

### 2.3 Tab bar and sidebar — `web/src/features/shell/TabBar.tsx` (79 lines)

Both are in one file and both render the same array, which is the key fact for any restructure:

```11:16:web/src/features/shell/TabBar.tsx
const tabs: Tab[] = [
  { to: '/', label: 'Home', icon: Home },
  { to: '/trade', label: 'Trade', icon: CandlestickChart },
  { to: '/portfolio', label: 'Portfolio', icon: Briefcase },
  { to: '/wallet', label: 'Wallet', icon: Wallet },
]
```

**`TabBar`** — `fixed inset-x-0 bottom-0 z-30 h-tabbar` (64px), `bg-bg/90` with backdrop blur, hidden at `lg`. Four equal-width `flex-1` items, each a `NavLink` with icon above an 11px label, accent-red when active, muted otherwise. `end` is set on `/` only, so `/trade` does not light up Home.

**`Sidebar`** — `hidden lg:block`, 220px fixed width, right hairline border, the same four `NavLink`s laid out as horizontal rows with `bg-accent-subtle text-accent` when active.

Active state comes from `NavLink`'s `isActive` render prop, and React Router adds `aria-current="page"` to the active link automatically. **Neither bar knows about `/signals` or `/profile`** — visiting either leaves all four items inactive, which is a real if minor inconsistency today.

Three smaller observations that matter later:

- **Both navs carry `aria-label="Main"`.** Only one is visible at a time (the other is display-hidden), but both are in the DOM, so there are two navigation landmarks with the same accessible name. This is why all three Playwright specs have to write `.getByRole('navigation', { name: 'Main' }).…first()`.
- The bottom bar's label is `text-[11px]`, an ad-hoc size. Pass 1 added a `nano` (10px) token and swept the user app's `text-[10px]` literals onto it; this 11px value was not part of that sweep and is still the only one of its kind in the user app.
- The 64px height is a Tailwind token, `spacing.tabbar`, and it has exactly four consumers: the bar's own height, `AppShell`'s bottom padding, the Trade screen's sticky order ticket (`bottom-tabbar`, `TradeScreen.tsx:295`), and the toast stack's offset (`ui/Toast.tsx:120`). Changing the bar's height through the token therefore moves all four together; changing it any other way silently breaks the ticket and the toasts on mobile.

### 2.4 Benchmark structure, as read from the screenshots

**Mobile trade screen** (`tnnbinary-mobile-trade.jpeg`), top to bottom: a top bar with hamburger, logo, balance pill reading `R $0.00` with a chevron, a sound icon, a green **Deposit** button, and a notification bell; contract-type tabs (Matches/Differs, Even/Odd, Over/Under); the chart; the digit row; AUTO/MANUAL; the stake and risk controls; the two contract buttons; and a three-item bottom bar — **Live Chat, AI, Positions** — with AI centred and given a raised gradient treatment.

**Mobile drawer** (`tnnbinary-mobile-menu.jpeg`): a left-anchored panel covering roughly 85% of the width with the page dimmed behind it, header showing the logo plus the word "Menu" and a close ×, then seven icon+label rows — Deposit, Withdraw, History, How to Trade, Refer & Earn, Account, Live Chat. Below a divider, two contact blocks (WhatsApp Care with a phone number, Support with an email address), then Log Out in red.

**Desktop** (`tnnbinary-trade-benchmark.png`): one horizontal top row — logo, a chart-ish icon, Trader's Hub, Deposit, Withdraw, History, an accented `✦ AI` pill, Live Chat, How to Trade, and on the right an account pill (`REAL $0.00`), a Deposit button, a bell, and a profile icon. Below that, three columns: the positions panel on the left, the chart in the middle, the trading controls on the right.

So the benchmark's own three surfaces disagree with each other about where things live, which is normal and worth stating: **Deposit appears in all three** (top bar on both, drawer on mobile), History appears in the desktop top bar, the mobile drawer, *and* as a tab inside the desktop positions panel, and Live Chat appears in the desktop top bar, the mobile drawer, and the mobile bottom bar. The stable pattern is not "everything goes in the hamburger" — it is **"money and support are always one tap away, whatever the width."**

### 2.5 Existing drawer / sheet / dialog infrastructure

There is one shared primitive and it is already doing this job well.

`web/src/components/ui/Sheet.tsx` wraps `@radix-ui/react-dialog` — already a direct dependency — as a bottom sheet on mobile that becomes a 440px centred dialog from `sm` up. It supplies the overlay (`bg-black/70`, `animate-fade-in`), the `animate-slide-up` entrance, a mobile drag handle, an optional title with a `hideTitle` escape that keeps it for screen readers, an optional description, a close button, a `full` height variant, and a re-exported `SheetClose`. Radix gives focus trapping, Escape-to-close, scroll locking, and `aria-modal` for free.

Current consumers: `AccountSwitcher`, `DepositSheet`, `WithdrawSheet`, `TransactionDetail`, `SymbolPicker`, the Trade screen's large-stake confirmation, and Portfolio's close-position confirmation.

**A left-anchored nav drawer is the same primitive with different position classes.** The only obstacle is that `Sheet` hardcodes its geometry in one `cn(...)` string with no `side` prop, so it would need either a new prop or a sibling component. No new dependency is required either way: `tailwindcss-animate` is already installed and registered in `tailwind.config.js`, which supplies `slide-in-from-left` and the `data-[state=open]:animate-in` pattern out of the box.

### 2.6 Where the money sheets currently live — and are duplicated

`DepositSheet` and `WithdrawSheet` are mounted **twice each**, once by `HomeScreen` (`:139-155`) and once by `WalletScreen` (`:128-144`), with local `useState` booleans in each screen. `AccountSwitcher` is mounted twice as well, once in `AppShell` and once in `HomeScreen`. Their props are the interesting part:

| Component | Props | Available in `AppShell` today? |
|---|---|---|
| `DepositSheet` | `open`, `onOpenChange`, `account`, `realAccount`, `onSwitchToReal` | `account` yes; `realAccount` is `accounts.find(a => a.account_type === 'real')`, derivable from data the shell already has; `onSwitchToReal` is a one-line store call |
| `WithdrawSheet` | `open`, `onOpenChange`, `account`, `wallet` | `account` yes; `wallet` needs `useWallet(accountId)` — which shares a React Query key with the two screens that already call it, so hoisting it adds no network traffic |

Neither sheet reads route state, and neither is positioned relative to its parent (both portal to the document body). `DepositSheet` does call `navigate('/trade')` from its success step (`:270`), which works identically from anywhere. **Both are already shell-mountable as written.** That is the single most important structural finding in this report.

### 2.7 Features referenced by the benchmark menu that do not exist here

Searched across the whole repository (frontend, Go backend, and migrations):

| Benchmark item | Present in TradeDerivPlus? |
|---|---|
| Live Chat | **No.** No chat UI, no messages table, no WebSocket channel for it (the three sockets are `/ws/prices`, `/ws/signals`, `/ws/account`), no third-party widget (no Intercom, Crisp, or Tawk). The word "support" appears only in Profile's KYC-rejected copy |
| Refer & Earn | **No.** No referral code, no referral table, no endpoint |
| How to Trade | **No.** No onboarding, help, or education content anywhere |
| Notification bell | **No.** Notifications exist only as transient toasts raised from the account and signal sockets. Nothing is persisted, so there is no history to open a bell onto |
| Sound toggle | **No.** No audio anywhere in the frontend |
| Trader's Hub | **No direct equivalent.** Home is the nearest thing |
| AI | **No** — and see section 5, this is almost certainly the benchmark's auto-trading feature, which Pass 1 put out of scope |

---

## 3. Gap analysis

Tap counts are from a cold start on an arbitrary screen. "Worst case" means the user is on the screen furthest from the action. The bottom bar is always visible on mobile and the sidebar always visible on desktop, so nothing today is worse than two taps — the question is whether that second tap changes screens, because a screen change is what the client is objecting to.

| Action / Item | Current path | Benchmark path | Gap | Complexity | Depends on |
|---|---|---|---|---|---|
| **Deposit** | Home or Wallet: 1 tap, opens `DepositSheet` in place. From Trade / Portfolio / Signals / Profile: 2 taps **and a screen change** (bottom bar → Home or Wallet, then Deposit) | Top-bar Deposit button, 1 tap, every screen, no screen change. Also in the drawer (2 taps) | Not missing — mis-placed. The sheet is built, tested, and already opens over the current screen; it is just mounted by two screens instead of by the shell | **Low.** Hoist state and both mounts into `AppShell`, add one top-bar button. No new component, no new endpoint | `useWallet` in the shell (shared query key, no extra fetch); a decision on the top bar's crowding at <640px |
| **Withdraw** | Home or Wallet: 1 tap. Elsewhere: 2 taps and a screen change. **Disabled for every user** — gated on `kyc_status === 'verified'` with no way to become verified | Drawer item, 2 taps from anywhere | Same placement gap as Deposit, plus a functional blocker that navigation cannot fix | **Low** for placement. The KYC gate is a **separate, larger** piece of work: no user-facing submission UI, no upload endpoint, only admin approve/reject exists | A product decision on whether to surface a control that always refuses; KYC submission is its own project |
| **History (money)** | Wallet tab: 1 tap, lands on the transaction ledger with All / Deposits / Withdrawals / Trades filters | Drawer → History, 2 taps | Reachable and good. The gap is naming, not access — a user looking for "History" has to know it is called "Wallet" | **Low** (a label change) to **Medium** (if money and trade history are to be merged into one screen) | Deciding what "History" means — see open questions |
| **History (trades)** | Portfolio → Closed tab: 2 taps, URL-driven at `?tab=closed`. Capped at the most recent 50 with no paging UI | Desktop positions panel has an explicit History tab beside Open and Closed | Two histories in two places under two names, and neither is called History. The 50-row ceiling is a known Pass 1 deferral, unchanged | **Medium** — Pass 1 already scoped a third Portfolio tab; paging UI is the real cost | `Page<T>` already returns `total` and `pages`; the UI ignores both |
| **Account / Profile** | Person icon in the top bar: 1 tap from anywhere | Drawer → Account (2 taps); desktop also has a profile icon in the top bar (1 tap) | **TradeDerivPlus is already better than the benchmark here.** The only risk is a restructure *losing* this by moving Profile into a drawer | **Low** — keep the icon, optionally mirror the entry in the drawer | Nothing |
| **Account switcher** | Account pill in the top bar: 1 tap from anywhere | Balance pill with chevron in the top bar: 1 tap | Parity of access. Difference in content: the benchmark's pill shows the **balance**; ours shows account type and number, with the balance only inside the sheet | **Low** — adding the balance to the pill is a `useWallet` call the shell can make anyway | The same shell-level `useWallet` as Deposit |
| **Signals** | **No navigation entry at all.** Only reachable via "See all" on Home (2 taps from elsewhere) or the signal strip on Trade. Neither nav bar knows the route exists | No equivalent feature in the benchmark | The one genuinely orphaned route in the app. A user who does not scroll Home may never learn Signals exists | **Low** — one entry in the shared `tabs` array, or a drawer row | A decision on where it belongs, given the bottom bar is already full at four |
| **Live Chat / Support** | **Does not exist in any form** | Mobile bottom-bar item, drawer row, desktop top-bar item, plus WhatsApp and email contacts in the drawer footer | Total absence, not a navigation gap. No chat infrastructure of any kind | **High** as a feature (real-time chat, persistence, agent side). **Low** if it becomes a drawer footer with a WhatsApp link and a support email, which is what the benchmark's *footer* actually is | A support channel existing off-platform; out of scope as a feature per the brief |
| **Log out** | Profile → Log out button: 2 taps | Drawer footer: 2 taps | Parity | **Low** if mirrored into a drawer | Nothing |
| **How to Trade** | Does not exist | Drawer row + desktop top-bar item | Absent. It is content, not navigation | **Medium** (someone has to write it) | Content the client would have to supply |
| **Refer & Earn** | Does not exist | Drawer row | Absent. A referral scheme is a backend feature: codes, attribution, payouts, an admin view | **High** | A commercial decision, then a backend build |
| **Notifications (bell)** | Toasts only, nothing persisted | Bell in the top bar at both widths | A bell would open onto nothing. The events exist (they already fire toasts) but none are stored | **Medium** — needs a table, an endpoint, and a read/unread model | A backend notifications store |
| **Sound toggle** | Does not exist | Speaker icon in the mobile top bar | Absent, and there is nothing to mute | **Low** as a stub; only meaningful once something makes a sound | A decision on whether audio cues are wanted at all |
| **Trader's Hub** | Home (`/`) is the nearest equivalent | Desktop top-bar item | Naming difference only | **Low** | Nothing |
| **Positions** | Portfolio tab: 1 tap, a full screen | Mobile bottom-bar item; desktop a dismissible left panel with Open / Closed / History and session stats at its foot | Different model — a screen versus an overlay panel. The **session figures at the panel's foot already exist** on our Trade screen, built in Pass 1 as `SessionStrip` | **Medium–High** if positions become an overlay panel on Trade; **zero** if Portfolio stays a screen | A decision on the one-screen-with-overlays model (section 4.1) |

### 3.1 So is the complaint about Home?

No. Home is the *best*-served screen in the app for this: it has the balance, inline Deposit and Withdraw, recent activity, open positions, and the latest signal, with links onward to Wallet, Portfolio, and Signals. If the client is comparing Home to the benchmark's drawer, the honest answer is that Home already does more without navigating than the benchmark's trade screen does.

The gap is on **every other screen**. Deposit, Withdraw, and the balance figure vanish the moment the user leaves Home or Wallet, and on the Trade screen — where a user runs out of money and most needs to add some — there is no way to do it without leaving the chart. That is the complaint, stated accurately.

One caveat on interpretation: the client may have been looking at the **deployed** build, where the Pass 1 Step 0 configuration fix has still not been applied, so the price feed is offline and the Trade screen shows its "Market feed offline" overlay. An app whose main screen looks dead will feel emptier than one with a ticking chart, regardless of navigation. Worth confirming which build the judgement was made against before reading too much into it.

---

## 4. Structural feasibility

### 4.1 The model difference, which sets the ceiling on everything else

The benchmark is a single-screen application. The chart is always there; Positions, Live Chat, and the menu are overlays on top of it; only the contract tabs change what the main area shows. TradeDerivPlus is a four-screen application with tab navigation, and each screen owns its own data, its own scroll position, and in two cases its own copy of the money sheets.

A restructure can go to one of three depths, and they cost very differently:

1. **Surface the actions, keep the screens.** Move the money sheets up into the shell, add a top-bar Deposit button and a hamburger drawer that lists everything, leave the four screens and the four-item bottom bar alone. Contained, reversible, touches roughly five files, and addresses the substance of the complaint.
2. **Restructure the bars to match the benchmark's shape.** Reduce the bottom bar to three items and move the rest into the drawer. Cosmetically closer, but see 4.2 — two of the benchmark's three bottom items cannot be built.
3. **Adopt the one-screen model.** Trade becomes the app; Positions becomes an overlay panel; Home, Portfolio, and Wallet stop being destinations. This is a product rewrite, not a layout pass, and it would discard Pass 1's positioning decisions along with a lot of working screens.

Nothing in the brief or the screenshots tells us which of the three the client has in mind. That is the first open question in section 6.

### 4.2 Why the benchmark's three-item bottom bar cannot be copied

Its items are Live Chat, AI, and Positions.

- **Live Chat** — no infrastructure, and explicitly not a feature to build in this pass.
- **AI** — the centred, gradient-highlighted item. On desktop the same `✦ AI` pill sits in the top bar directly beside a TRADING MODE panel whose two options are AUTO and MANUAL. On the strength of that adjacency, AI is almost certainly the benchmark's automated-trading entry point, which Pass 1 recorded as a separate initiative and explicitly out of scope. TradeDerivPlus's nearest relative is the Signals feature, which is advisory only — a signal can prefill the ticket but a human still presses Buy or Sell. Putting "AI" on a bottom bar that leads to advisory signals would promise automation the product does not do.
- **Positions** — the only one that maps cleanly, onto Portfolio.

**So one of the three transfers.** Copying the *shape* would mean inventing two items, and the only honest three-item bar available from today's features would be something like Trade, Positions, and Menu — which is a different design from the benchmark's, arrived at by a different route. Worth agreeing that explicitly rather than discovering it in review.

### 4.3 How coupled the shell actually is

Less than the brief feared. Concretely:

- **Navigation is two components in one file, driven by one array.** Adding, removing, or reordering destinations at both widths is a single edit to `tabs` in `TabBar.tsx:11-16`. Nothing else in the codebase imports that array or duplicates it.
- **The route table is flat.** `App.tsx:60-67` lists six sibling routes under the layout route with no nesting, no index routes, and no route-level data loaders. Adding a route is one line; removing one is one line plus finding its inbound links.
- **`AppShell` already owns modal state.** `switcherOpen` is a plain `useState` passed down to `TopBar` as a callback. A drawer would follow that exact pattern, and moving the money sheets up means moving two more booleans alongside it.
- **Active-route logic is entirely `NavLink`'s.** There is no manual `useLocation` comparison, no route-name registry, and no breadcrumb system to keep in sync. `AppShell` reads `location` once, only to build the post-login redirect.

What a restructure would touch, in rough order of blast radius:

| Area | What changes | Risk |
|---|---|---|
| `TabBar.tsx` | The `tabs` array; possibly a split into separate mobile and desktop lists if the two stop matching | Low — self-contained |
| `AppShell.tsx` | Drawer state and mount; hoisted deposit/withdraw state and mounts; a `useWallet` call | Low–Medium — it is the file every screen depends on, so a mistake here is visible everywhere at once |
| `TopBar.tsx` | Hamburger trigger, Deposit button, possibly the balance in the pill | **Medium at narrow widths.** The bar is `h-14` with three slots; the benchmark fits six controls at 460px partly because its logo is short and its balance reads `$0.00`. "TradeDerivPlus" plus a full account number plus a Deposit button will not fit a 360px phone without dropping something |
| `HomeScreen.tsx`, `WalletScreen.tsx` | Remove their local sheet state and mounts, call the shell's openers instead | Low, but it changes two screens Pass 1 just touched — worth a careful diff |
| `PortfolioScreen.tsx`, `SignalsScreen.tsx` | Nothing, unless the top bar's height changes — see the sticky-offset note below | Low but easy to miss |
| `web/e2e/*.spec.ts` | Three flows navigate by `getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Home' \| 'Trade' \| 'Portfolio' \| 'Wallet' }).first()`. Removing any of those four labels from the bars breaks `user.trading.spec.ts` and `user.deposit.spec.ts` | Low cost, certain to happen — budget for it rather than being surprised |

### 4.4 Two silent breakages to watch for

**Hardcoded sticky offsets against the 56px top bar.** `PortfolioScreen.tsx:228` pins its day headings with `sticky top-14`, and `SignalsScreen.tsx:77` pins its filter row with `sticky top-16`. Both are magic numbers matched by hand to `TopBar`'s `h-14`. If the top bar grows to fit a hamburger and a Deposit button — or if a second row is added under it, as the benchmark's contract tabs are — these two elements will sit under the bar or float away from it, with no type error and no test failure. (There is no CSS-variable layer in this codebase; Pass 1 noted the same absence for chart colours.)

**Bottom-bar height, conversely, is safe** as long as it is changed through the `spacing.tabbar` token, because all four consumers read from it.

### 4.5 Routing and URL implications

**Overlays are React state, not URL state.** Every sheet in the app — deposit, withdraw, account switcher, transaction detail, symbol picker — is a Radix dialog driven by a local boolean. Consequences, all of which are true today and would simply become more visible if the drawer becomes a primary navigation surface:

- **Nothing overlay-shaped is deep-linkable.** There is no URL that opens the deposit sheet. Support cannot send a user a link that lands on it.
- **The Android back button does not close a sheet; it navigates.** Radix handles Escape and outside-click, but not `popstate`. On a phone, back from an open deposit sheet takes the user to the previous *route* with the sheet still notionally open, then unmounted. Users expect back to dismiss. This is an existing wart; a hamburger drawer would make it a frequent one, because drawers are the control people reflexively back out of.
- **So the choice of mechanism per menu item matters.** Items that *show content* (History, Account, How to Trade) should stay routes, where back and deep-linking work for free. Items that are *flows* (Deposit, Withdraw) are fine as sheets, as they are now. A menu that mixes the two will behave inconsistently unless that is a deliberate, documented rule.

**One latent bug to avoid inheriting.** If anyone reaches for query-param overlay state (`?sheet=deposit`), note that `PortfolioScreen.tsx:175` writes its tab with `setParams({ tab: v }, { replace: true })`, which **replaces the entire query string** rather than merging. Any sibling parameter on that screen would be silently dropped on a tab switch. The tab param itself is well-behaved otherwise: `?tab=closed` deep-links correctly, anything else falls back to `open`, and `replace: true` keeps tab switching out of the back stack.

**Unknown routes vanish quietly.** `App.tsx:84` sends everything unmatched to `/` with `replace`. If the menu ever links to a route that has not shipped yet, the user lands on Home with no explanation — worth knowing before a "coming soon" entry is wired as a link.

### 4.6 Desktop: structurally different, or just missing content?

Mostly missing content, once the desktop screenshot is read correctly (section 1).

- **The benchmark has no navigation sidebar**, so "match the benchmark" on desktop means *removing* the 220px sidebar and putting Home, Trade, Portfolio, and Wallet into the top bar — a wider, busier top bar and a wider content area. That is a layout change, not a content one, and it is a legitimate design question rather than an obvious improvement.
- **The benchmark's left column is a positions panel.** Reproducing that means positions become an overlay on Trade rather than a screen, which is the depth-3 change in 4.1.
- **What is genuinely just missing content** in the desktop top bar: a Deposit button, a notification bell, the balance on the account pill. All three are additive to the current three-slot header and none require a layout change.
- **A middle path worth considering** (not a recommendation, just an option the structure permits cheaply): keep the sidebar and *put the utility items in it below a divider* — Deposit, Withdraw, History, Account — which is closer to what the brief described than to what the benchmark does, but fits the existing 220px column with no layout change and no top-bar crowding. It also makes the desktop and mobile stories consistent, because the drawer would carry the same list.

---

## 5. Out of scope (confirmed)

- **Contract-type tabs** (Matches/Differs, Even/Odd, Over/Under) — noted as present in both mobile and desktop benchmarks and not investigated. They belong to binary digit contracts, a contract structure TradeDerivPlus does not have; Pass 1 established that this product's P/L is proportional to price movement with no payout ratio and no expiry.
- **Digit-distribution row** (0–9 with percentages, one highlighted) — part of the same binary digit contract structure. Noted, not scoped.
- **AUTO / MANUAL toggle** — confirmed out of scope in Pass 1 and still out of scope. Seen in the mobile screenshot directly under the digit row and in the desktop screenshot's TRADING MODE panel. **Flagged, not scoped.** Related: the `AI` item in the benchmark's bottom bar and desktop top bar is almost certainly this same automated-trading feature, so it is out of scope by extension — which is exactly why the three-item bottom bar cannot be copied (section 4.2).
- **Live Chat as a feature** — recorded as a **total absence**, not a build item. There is no chat UI, no message persistence, no dedicated socket channel, and no third-party widget in the codebase. Building it is a project of its own. The only cheap thing in the benchmark's chat area is its drawer *footer* — a WhatsApp number and a support email, which are links, not infrastructure.
- **The Trade screen's chart, order ticket, and stats strip** — delivered in Pass 1 and not revisited here. The only point of contact is that the ticket is `sticky bottom-tabbar` on mobile, so it moves with the bottom bar's height token.
- **The admin console** (`/admin/*`) — a separate surface with its own shell, session, and sidebar. Unaudited in Pass 1 and unaudited here.
- **Payout figures** on the benchmark's contract buttons (`$19.52`, `95.20% Payout`) — a binary-contract concept with no equivalent in this product, already re-specified in Pass 1 as projected P/L.

---

## 6. Open questions for the team

1. **Mobile, desktop, or both?** The single most important question, and everything below depends on it. The client sent phone screenshots captioned "web," so we do not know whether they want the phone layout changed, the desktop layout changed, or the desktop layout made to resemble a phone. See section 1.

2. **Is the desktop benchmark understood correctly?** The brief described a left nav sidebar; the screenshot shows a horizontal top bar with a positions panel on the left. Please confirm the client is asking for the **top-bar** arrangement, because "keep our sidebar and add to it" and "replace our sidebar with a top bar" are different pieces of work with different risks.

3. **How deep does this go — actions, bars, or model?** The three depths in section 4.1. Our reading is that depth 1 (surface the actions, keep the screens) addresses the actual complaint at a fraction of the cost, but that is a client decision.

4. **Should the four-item bottom bar change at all?** It cannot become the benchmark's three items, because two of those three do not exist here (section 4.2). If a hamburger drawer is added and the bottom bar keeps its four destinations, the drawer becomes a second, overlapping navigation surface — which needs a clear rule about what belongs in which, or it will feel redundant.

5. **Live Chat: placeholder, contact details, or omitted?** Three options. A disabled "coming soon" row sets an expectation with no delivery date. A drawer footer with a WhatsApp link and a support email is cheap, honest, and is what the benchmark's footer actually is — but it needs a real number and a real address from the client. Omitting it entirely is the third. Our reading is that the footer option is the best value, conditional on the contacts existing.

6. **What does "History" mean to the client?** TradeDerivPlus has two histories — money (Wallet) and trades (Portfolio → Closed) — and neither is called History. Does the benchmark's single History entry mean one merged view, or is pointing "History" at the existing Wallet ledger enough? Note that trade history is still capped at the most recent 50 rows with no paging, a Pass 1 deferral.

7. **Are How to Trade and Refer & Earn wanted as products?** Both are absent end to end. How to Trade needs content from the client. Refer & Earn is a backend feature — codes, attribution, payout rules, an admin view — and is not a navigation item that happens to be missing.

8. **Should the notification bell be built, stubbed, or skipped?** The events already exist and already raise toasts; none are stored, so a bell would open onto an empty list until a notifications table and endpoint exist.

9. **Does Signals get a navigation home?** It is currently the app's one orphaned route, reachable only through an in-page link on Home. A drawer would be the natural place, if a drawer happens.

10. **What do we do about Withdraw being permanently disabled?** No user can reach `kyc_status === 'verified'` through the app: the Profile verification button is hard-disabled and captioned "Verification is not available yet," and the API exposes no user-side KYC submission. Giving Withdraw more prominence without fixing that means giving a dead control more prominence. Is KYC submission a scheduled piece of work?

11. **Was the client looking at the deployed build?** Pass 1's Step 0 configuration fix (a `VITE_WS_URL` on Vercel and a `CORS_ORIGIN` on Railway) has still not been applied by anyone with dashboard access, so the deployed Trade screen shows "Market feed offline" over a dead chart. That affects how full the app feels and is worth ruling in or out before reading the feedback as purely about navigation.

12. **Is the account balance wanted in the top bar?** The benchmark's pill shows money; ours shows account type and number. Adding it is cheap and would help on the Trade screen, where the balance is otherwise invisible — but it competes for space with a hamburger and a Deposit button on a 360px phone (section 4.3).
