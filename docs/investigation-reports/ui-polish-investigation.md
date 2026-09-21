# TradeDerivPlus — Rendering & Visual Polish Investigation

**Type:** Investigation only. No implementation code, styling, or component changes were made in this pass.
**Date:** 21 September 2026
**Benchmark:** TNNbinary (tnnbinary.com/trade) — chart quality, live data density, risk-control visibility.
**Reference screenshots:** not found in the repository or `docs/`. The gap analysis below is written against the feature list supplied in the brief. Anything that depends on reading the screenshots directly is listed in *Open questions*.

---

## 1. Summary

TradeDerivPlus is a complete, working four-screen trading app with a consistent dark design system, real WebSocket price streaming, and a proper order ticket. The gap against the benchmark is almost entirely about **density and chart capability, not missing plumbing**: the Trade screen has one hidden-axis line chart, three short timeframe buttons, and no price scale, crosshair readout, percentage change, or session statistics, so it reads as "app with a chart" rather than "trading terminal". Several of the items in the brief turn out to be cheaper than expected — stop loss and take profit are *already* inline-configurable (the "OFF" state is a collapsed default, not a disabled feature), and the session-stats and history data already exist in the API and are simply not surfaced on the Trade screen.

Two things constrain the work and need a decision before design starts. First, **the live price feed is genuinely broken in the deployed build** — the shipped JavaScript bundle points its WebSocket at `ws://localhost:8080`, which can never work from a browser on a hosted site; this must be fixed first, because every improvement discussed here is a price-driven improvement. Second, **the product is not a binary-payout product** — profit is proportional to price movement, so the benchmark's "payout preview" has no direct equivalent and needs to be re-specified as a projected profit/loss preview rather than copied.

The only genuinely expensive items are richer charting (candles, zoom, crosshair with axis labels, timeframes longer than about five minutes), because the server keeps only a short in-memory tick buffer and has no candle data or price history table at all.

---

## 2. Current state inventory

### 2.1 Stack and where things live

| Layer | What is used |
|---|---|
| Frontend | React 19, TypeScript, Vite 8, `react-router-dom` 7 (`web/package.json`) |
| Styling | Tailwind CSS 3.4, dark-only (`darkMode: 'class'`), `tailwindcss-animate`; no CSS custom properties anywhere |
| Primitives | Radix UI (dialog, tabs, switch, toast, tooltip, label, progress, separator, slot) wrapped in `web/src/components/ui/*` |
| Server state | TanStack Query v5 (`web/src/hooks/useQueries.ts`) |
| Client state | Zustand — `store/session.ts` (auth, active account), `store/market.ts` (live prices) |
| Charting | **Recharts 3.10** only (`web/src/features/trading/PriceChart.tsx`) |
| Money | `decimal.js`; all amounts cross the wire as strings (`web/src/types/index.ts:1-3`) |
| Backend | Go + Gin + PostgreSQL, WebSocket hub (`internal/`, `pkg/ws`, `pkg/derivclient`) |

Routes live in `web/src/App.tsx` under `AppShell`: `/` (Home), `/trade`, `/portfolio`, `/wallet`, plus `/signals`, `/profile`, auth screens, and a separate desktop console at `/admin` (out of scope for this audit).

The shell is `web/src/features/shell/AppShell.tsx`: sticky `TopBar` (h-14, `bg-bg/90` + backdrop blur), content capped at `max-w-[1200px]`, bottom `TabBar` on mobile (`h-tabbar` = 64px) swapped for a 220px sidebar at `lg`. Four bottom-nav entries — Home, Trade, Portfolio, Wallet (`TabBar.tsx:11-16`).

### 2.2 Design tokens — exact current values

All tokens are Tailwind theme extensions in `web/tailwind.config.js`. There are **no CSS variables**, so any consumer that needs a colour in JavaScript (the chart, SVG rings) currently hardcodes the hex.

**Colour** (`tailwind.config.js:7-26`)

| Token | Value | Role |
|---|---|---|
| `bg` | `#0A0A0B` | page background |
| `surface` | `#141416` | cards |
| `elevated` | `#1A1A1D` | sheets, dialogs, active chips |
| `border` | `#232326` | 1px card borders |
| `hairline` | `#1C1C1F` | dividers |
| `text` | `#F4F4F5` | primary text |
| `muted` | `#8A8A93` | secondary text |
| `faint` | `#5A5A62` | tertiary / disabled |
| `accent.DEFAULT` | `#E24B4A` | the single red accent: primary CTA, active tab, sell, loss |
| `accent.hover` | `#C93F3E` | — |
| `accent.subtle` | `rgba(226,75,74,0.12)` | selected rows, subtle fills |
| `profit` | `#22C55E` | profit, buy |
| `loss` | `#E24B4A` | loss (same hex as accent) |
| `warning` / `demo` | `#F59E0B` | pending, warnings, DEMO pill |
| `real` | `#22C55E` | REAL pill |

**Type scale** (`tailwind.config.js:30-36`): `meta` 12/16, `body` 14/20, `card` 16/24, `title` 24/32, `hero` 36/40. Family: Inter → system-ui fallbacks. There is **no size between 24px and 36px**, and nothing below 12px except ad-hoc `text-[10px]` used in a few badges (e.g. `OrderTicket.tsx:281`, `SymbolPicker.tsx:85`).

**Radius / spacing / motion**: `rounded-card` 16px, `rounded-pill` 999px, `spacing.gutter` 16px, `spacing.tabbar` 64px (`tailwind.config.js:37-44`). Animations: `flash-up` / `flash-down` (300ms price flash), `slide-up` (sheets), `fade-in`, `shimmer` (`tailwind.config.js:45-58`). `web/src/index.css` adds only `color-scheme: dark`, the `.tnum` tabular-numerals helper, and `.no-scrollbar`.

**Elevation**: there are no shadow tokens. Depth is expressed purely as `bg-surface` + `border-border` (`components/ui/Card.tsx:3-9`), which is why the UI reads flat compared with a platform that uses layered panels.

**Hardcoded hex values outside the token system** — these are the places a palette change would silently miss:

- `features/trading/PriceChart.tsx:53` (`#22C55E` / `#E24B4A` line stroke), `:87-94` (tooltip `#1A1A1D`, border and cursor `#232326`), `:100-113` (SL/TP reference lines and labels)
- `features/wallet/CountdownRing.tsx:41-47` (`#232326`, `#E24B4A`)
- `tailwind.config.js:46-47` (keyframe colours)

The intended design language is documented in `docs/ui-ux-plan.md:25-66` and the implementation matches it closely.

### 2.3 Home (`/`)

`features/home/HomeScreen.tsx`, composed of `BalanceCard`, `AccountStrip`, `OpenPositionsSummary`, `RecentActivity`, and the shared `SignalCard`; `DepositSheet` / `WithdrawSheet` / `AccountSwitcher` mount as overlays. Single column on mobile, two columns at `lg`.

Data is live, not mocked: `useWallet(accountId)` → `GET /accounts/:id/wallet`, `useSignals(undefined, 1)`, `useTrades(accountId, 'open', 3)`, `useTransactions(accountId, undefined, 5)` (`HomeScreen.tsx:40-42`). Open-position rows compute unrealised P/L client-side from the live price store (`OpenPositionsSummary.tsx:10-11`). Balances additionally refresh from the account WebSocket (`hooks/useAccountStream.ts:68-77`).

Hardcoded: the 3/5/1 list limits above, the `slice(0, 3)` / `slice(0, 5)` re-clamps, the withdraw-gate copy, and `isLoading={false}` passed to `AccountSwitcher` (`HomeScreen.tsx:130`). Loading is skeletons throughout; the open-positions block has **no error state** (it silently renders nothing) and no empty state by design (`OpenPositionsSummary.tsx:33`).

### 2.4 Trade (`/trade`) — the screen under review

Rendered by `features/trading/TradeScreen.tsx` (314 lines), in this order:

1. **Header row** — `SymbolPicker` (pill button: display name + category chip + chevron) and `ConnectionDot` (`TradeScreen.tsx:145-153`).
2. **Price + range row** — `PriceTag` at `text-title` (24px) and `ChartRangeToggle` (`:155-165`).
3. **Chart area** — fixed `h-[240px]` mobile / `h-[340px]` desktop. Before any ticks arrive: a skeleton, then after 5 seconds a "No price data for this symbol yet" message with a reload button (`:76-81`, `:168-185`). When the feed is stale, a translucent overlay reading "Market feed offline" or "Reconnecting…" covers the chart (`:197-203`).
4. **Signal strip** — one-line latest signal with an "Apply" affordance (`:206-218`).
5. **Open positions on this symbol** — direction badge, stake, live P/L, Close button (`:220-253`).
6. **Order ticket** — sticky above the tab bar on mobile, a 380px right column at `lg` (`:143`, `:256-280`).
7. **Confirmation sheet** — only for real accounts when stake ≥ 20% of balance (`:35-36`, `:100-112`).

**Charting approach.** `PriceChartLazy.tsx` code-splits Recharts so only the Trade screen pays for it (README:185-186 notes ~100kB). `PriceChart.tsx` renders a Recharts `AreaChart` of the raw tick series — there is no candle endpoint, so ticks are drawn directly (`PriceChart.tsx:17-22`). Concretely:

- `YAxis` is present but `hide` (`:83`) — **no visible price scale**.
- Line colour is green or red depending on whether the last point is above the first point of the visible window (`:48-53`).
- A Recharts `Tooltip` gives a hover readout with a thin vertical cursor line (`:85-95`) — no axis-labelled crosshair, no last-price tag pinned to a scale.
- `ReferenceLine` draws SL and TP with `SL` / `TP` labels while the ticket has them set (`:97-115`).
- No X axis, no gridlines, no zoom, no pan, no reset control, no drawing tools, no candles, no volume, no indicator overlays. `isAnimationActive={false}`, `type="linear"`.
- Timeframes are `1 | 5 | 15` minutes (`chartRange.ts:3`, `ChartRangeToggle.tsx:13`), implemented by slicing the in-memory tick buffer client-side — no refetch (`PriceChart.tsx:41-46`).

**Live data plumbing.** `usePriceStream(symbols)` opens one WebSocket to `/api/v1/ws/prices`, keeps the subscription set in place when the symbol changes, and seeds the chart once per symbol from `GET /symbols/:code/ticks?minutes=5` (`hooks/usePriceStream.ts:29-98`). `store/market.ts` keeps the latest tick plus a 600-tick ring buffer per symbol (`market.ts:6`). Connection status is set only by the socket, with `offline` after 3 consecutive failures (`lib/socket.ts:30-31, 85-98`).

**The order ticket** (`OrderTicket.tsx`) is a pure component: direction buttons, stake input with `+5 / +10 / +50 / Max` chips, stop-loss and take-profit fields, available balance, and a submit button labelled with direction, symbol, and stake. It shows **no projected profit or loss** — the only derived figure is percentage distance from current price for each level (`:116-120`).

**Important product note:** the P/L model is proportional, not binary. `lib/pnl.ts:15-27` mirrors `internal/models/pnl.go`: `buy: stake × (current − entry) / entry`. There is no payout ratio, no fixed return percentage, and no expiry time anywhere in the model (`types/index.ts:74-92`).

### 2.5 Portfolio (`/portfolio`)

`features/portfolio/PortfolioScreen.tsx` + `PositionCard.tsx`. `PageHeader`, then three summary tiles, then Radix tabs.

Tiles come from one call, `useTradeSummary(accountId)` → `GET /trades/summary`, refetching every 15s: `open_pnl`, `realised_today`, `open_positions`, `prices_complete` (`types/index.ts:138-144`). Note that "Today" is actually **the last 24 hours**, not the calendar day — `RealisedSince(ctx, accountID, 24)` (`internal/repository/trade_repo.go:129-134`).

Tabs are **Open / Closed**, and they are already URL-driven: the active tab is read from `?tab=` and written back with `replace: true` (`PortfolioScreen.tsx:91`, `:170-174`). Open positions are cards with live P/L, SL/TP chips, and Close; closed trades are compact rows grouped under sticky day headings via `dayLabel()`. Both tabs fetch a fixed `limit` of 50 on page 1 (`:96-97`). `Page<T>` carries `total` and `pages` (`types/index.ts:146-153`) but the UI never reads them, so **anything older than the most recent 50 trades is unreachable**.

The summary tiles have no error state; both lists have skeleton, empty, and error states.

### 2.6 Wallet (`/wallet`)

`features/wallet/WalletScreen.tsx` reuses `BalanceCard`, then a horizontally scrolling filter strip (All / Deposits / Withdrawals / Trades — a hardcoded `FILTERS` array held in local `useState`, **not** in the URL, `WalletScreen.tsx:20-25, 32-39`), then up to 50 transactions with pending deposits pinned to the top (`:46-52`). `DepositSheet` runs a four-step M-Pesa flow with a 60-second `CountdownRing`, 3-second polling, and a 120-second give-up (`DepositSheet.tsx:22-24`); `WithdrawSheet` gates on `kyc_status === 'verified'`. This screen is the most "finished" of the four in terms of state coverage.

### 2.7 What is mocked versus live

Nothing in the four screens uses fake data arrays or placeholder content — every number comes from the API or the live price socket. The *simulation* is server-side and deliberate: `USE_MOCK_FEED=true` generates a local random walk per instrument (`pkg/derivclient/client.go:140-206`, ~2 ticks/second), and M-Pesa is simulated. Instruments are seeded in `migrations/0002_seed_symbols.up.sql`, including the frontend's default `R_75`.

---

## 3. Gap analysis

| Feature | Have it? | Gap | Complexity | Depends on |
|---|---|---|---|---|
| **Price scale (Y axis)** | No — axis exists but `hide` (`PriceChart.tsx:83`) | No reference for how far price has moved; chart reads decorative | **Low** with Recharts (unhide + style + token-ise colours) | Nothing new; existing tick data |
| **Timeframe tabs — short (1m/5m/15m)** | Partially (`1m 5m 15m`, `ChartRangeToggle.tsx`) | Styled as small text buttons, not a segmented control; 15m is usually empty because the seed endpoint only returns ~5 min and the server buffer holds ~5 min | **Low** for the control; **Medium** to make 15m honest | Server buffer `tickBufferSize = 600` (`internal/service/deriv_feed_service.go:37`) |
| **Timeframe tabs — long (1h/1d/1w)** | No | No data source exists. No tick or candle table in the schema (`migrations/0001_init.up.sql`); history is in-memory only and lost on restart | **High** | New persistence (tick/candle table or Deriv history API) — backend work |
| **Candlesticks / OHLC** | No — tick line only | Benchmark's "real platform" feel is largely candles | **High** | Same data gap as above, plus a charting library that draws candles |
| **Crosshair with axis labels** | Partial — Recharts `Tooltip` + thin cursor (`:85-95`) | No price/time labels pinned to the axes, no magnet-to-series readout | **Medium** in Recharts, **Low** if a purpose-built chart library is adopted | Price scale work above |
| **Zoom / pan / reset controls** | No | Not supported by the current `AreaChart` setup | **Medium–High** in Recharts (hand-rolled brush/domain state) | Longer history to zoom into is what makes it worthwhile |
| **Drawing tools** | No | Entirely absent | **High** | New dependency; arguably beyond a polish pass |
| **Live price + % change beside instrument name** | Price yes (`PriceTag`, `TradeScreen.tsx:157-162`); % change **no** | Percentage change is not computed anywhere in the codebase. Change over the visible window is derivable from the existing buffer; "change today" is not | **Low** for session/window change; **High** for a true daily change | Daily change needs a session-open or daily-open price the server does not store |
| **Live payout preview in the ticket** | No | Ticket shows stake, distance-to-level %, and available balance only (`OrderTicket.tsx:116-120, 215-218`). **The product has no payout concept** — P/L is proportional (`lib/pnl.ts:15-27`), so this must be re-specified as projected P/L at the SL and TP levels, plus P/L per unit of price movement | **Low** (pure client maths with `pnl()` already imported by the screen) | Product decision on how to phrase it; live price only |
| **Risk controls visible and configurable inline** | **Yes, already** — `LevelField` has an On/Off toggle and a numeric input in the ticket, with validation and chart reference lines (`OrderTicket.tsx:186-212, 245-309`) | Not a missing feature: "Off"/"Not set" is the collapsed default. The gap is purely visual — a 10px Off chip and a dashed "Not set" box read as *disabled* rather than *available* | **Low** — styling and copy only | Nothing |
| **Session stats on Trade (P/L, open count)** | No on Trade; yes on Portfolio | `useTradeSummary` already returns `open_pnl`, `realised_today`, `open_positions` and is simply not called by `TradeScreen` | **Low** | Existing endpoint `GET /trades/summary` |
| **Session stats — trade count, win/loss** | No, nowhere | Not computed on the client or the server. Derivable from the closed-trade list (each closed trade carries `pnl`) but only within the fetched 50 rows; an accurate figure needs a new aggregate | **Low** (approximate, from existing list) / **Medium** (accurate, new endpoint) | New backend aggregate for the accurate version |
| **"Session" as a concept** | No | There is no session entity, boundary, or timestamp in the data model — only per-trade `opened_at` / `closed_at` and a rolling 24-hour sum | **Medium** | Product definition of what a "session" is, then a query |
| **History as a third Portfolio tab** | No — Open / Closed only | Structurally easy: tabs are already Radix + URL-driven, so a third trigger and panel slot straight in (`PortfolioScreen.tsx:170-236`). The API already supports paging (`limit`/`offset`, with `total` and `pages` returned). What is missing is any paging UI, and any date-range filter server-side — `ListByAccount` filters on status only (`internal/repository/trade_repo.go:75-92`) | **Medium** | Paging UI; a date filter would need a new query parameter |
| **Instrument header density** | Partial | Price is large and live; missing next to it: % change, high/low of window, spread or pip size, and the symbol code itself (the pill shows display name and category only, `SymbolPicker.tsx:83-88`) | **Low** for layout; the data limits above apply to % change | Live price store; daily figures blocked as noted |
| **Spacing / typography scale** | Solid and consistent | Type scale jumps 24px → 36px with nothing between, and sub-12px sizes are ad-hoc `text-[10px]`; a denser terminal look usually wants an 11px and a 20px step | **Low** | Additive token changes only |
| **Card elevation / borders** | Flat by design — 1px border + surface fill, no shadow tokens (`ui/Card.tsx:3-9`) | No layering vocabulary for a dense panel UI | **Low** | Additive `boxShadow` tokens; does not disturb existing colours |
| **Empty states** | Good coverage — shared `EmptyState` used on Portfolio, Wallet, Home activity, symbol picker | Trade screen has no designed empty state (a bare sentence plus a reload button, `TradeScreen.tsx:170-185`); Home open-positions silently renders nothing | **Low** | Nothing |
| **Loading states** | Good — dimension-matched skeletons nearly everywhere (`ui/Skeleton.tsx:3-6`) | Chart skeleton is a plain grey block; no shimmer, no axis scaffold | **Low** | Nothing |
| **Error states** | Mostly good — shared `ErrorState` with human-readable messages (`ErrorState.tsx:44-51`) | Missing on Portfolio summary tiles and Home open positions | **Low** | Nothing |
| **Feed status transparency** | Yes — `ConnectionDot` in top bar and beside the chart, plus chart overlay | Overlay offers no retry action; the only recovery path offered anywhere is a full page reload | **Low** | Blocker in section 4 matters far more than this |

### 3.1 Feasibility notes

**Achievable with existing data and component/CSS work only (no new dependency, no backend change):**

- Unhiding and styling the price scale; restyling the timeframe control as a segmented control.
- Percentage change over the *visible chart window*, computed from the tick buffer already in `store/market.ts`.
- Projected P/L preview in the ticket at the SL and TP levels — `pnl()` is already imported by the Trade screen (`TradeScreen.tsx:26`).
- Session statistics strip on Trade using the existing `useTradeSummary` hook.
- Making the SL/TP controls *look* configurable; denser instrument header; elevation, type-scale, and empty/loading/error polish.
- Approximate trade count and win/loss derived from the already-fetched closed-trade page.

**Needs a new dependency:**

- Candlesticks, axis-labelled crosshair, built-in zoom/pan, and drawing tools are not things Recharts does well. `lightweight-charts` (~45kB gzipped) is the conventional choice and is *already named as an option* in `docs/ui-ux-plan.md:260`. The swap is well contained: `PriceChart.tsx` is the only consumer and is already lazy-loaded behind `PriceChartLazy.tsx`, so the blast radius is one file plus its props. Recharts would still be needed for the admin console dashboards, so bundle size would grow rather than shift.

**Needs a new API or backend change:**

- Any timeframe beyond roughly five minutes, any candle data, and any "change since daily open" figure. The server keeps 600 ticks per symbol in memory (`internal/service/deriv_feed_service.go:37`), and `GET /symbols/:code/ticks` clamps `minutes` to 1–30 but can only return what the buffer holds (`internal/handlers/market_handler.go:55-69`). There is **no ticks or candles table in the schema**, and the buffer is lost on every restart.
- Accurate win/loss and lifetime trade counts (a `SUM`/`COUNT` aggregate alongside the existing `RealisedSince`).
- A date-range filter for a History view, if filtering is wanted rather than plain paging.

**Conflicts with the current design system:**

- No true conflicts. The palette is worth preserving as-is, and every addition discussed above is additive (new shadow tokens, one or two new type steps).
- One real friction point: because tokens exist **only** as Tailwind theme values and not as CSS variables, any chart library must be themed by duplicating hex values in JavaScript — which is exactly how `PriceChart.tsx` already does it, and how `CountdownRing.tsx` drifted. Introducing CSS variables that Tailwind reads from would remove that duplication, but it touches the token layer and should be an explicit, separately agreed decision rather than a side effect of chart work.

---

## 4. Pre-requisite blockers

### Blocker 1 — the live price feed cannot connect in the deployed build (high confidence, must fix first)

This is a configuration and deployment problem, not a code defect, and it is separate from the design work. Evidence:

1. The WebSocket base URL is a build-time constant with a localhost fallback: `const WS_BASE = import.meta.env.VITE_WS_URL ?? 'ws://localhost:8080/api/v1/ws'` (`web/src/lib/socket.ts:1`).
2. **The built bundle in `web/dist` contains the literal `ws://localhost:8080/api/v1/ws`**, confirming `VITE_WS_URL` was unset at build time. A browser on an HTTPS host can never reach that address, and insecure `ws://` from an HTTPS page is blocked outright.
3. Nothing in the repository supplies a production value. `web/.env.development` sets localhost, CI sets localhost for end-to-end tests (`.github/workflows/ci.yml:190-191`), and `web/vercel.json` rewrites only HTTP `/api/*` traffic to Railway — it says nothing about WebSockets.
4. The failure then presents exactly as reported: three failed connection attempts, roughly 3–7 seconds, and the socket reports `offline` (`lib/socket.ts:30-31, 93-98`), which drives both the "Market feed offline" chart overlay and the "Waiting for a live price before you can trade" ticket message (`TradeScreen.tsx:74, 197-203, 262-270`).
5. A second, independent trip hazard sits behind it: the backend runs its own origin check on the WebSocket upgrade against `CORS_ORIGIN`, which defaults to `http://localhost:5173` (`internal/handlers/ws_handler.go:36-46`, `internal/config/config.go:106`). Even with a correct `wss://` URL, the handshake is rejected unless Railway's `CORS_ORIGIN` exactly matches the deployed frontend origin. The committed `.env` still reads `CORS_ORIGIN=http://localhost:5173`.

Notably, this is **not** a missing API key or a Deriv credential problem. `USE_MOCK_FEED` defaults to `true` (`internal/config/config.go:88`) and the mock generator produces ~2 ticks/second locally with no credentials. The Railway API is alive — `GET /api/v1/symbols` returns `401` (auth required), which means the server is up and routing. If the real Deriv feed were the problem, the symptom would be different: the socket would report `live` and the ticket would read "Connecting to the market…" with an empty chart, not "offline".

There is also a related, already-documented sibling issue for the HTTP base URL in [`docs/api-base-url-investigation-report.md`](../api-base-url-investigation-report.md): `VITE_API_URL` must include the `/api/v1` suffix. The WebSocket variable is the same class of build-time configuration risk and was not covered there.

**Why this blocks design work:** every item in section 3 — price scale, percentage change, session P/L, payout preview, chart polish — is only observable with ticks arriving. Without a feed, the work cannot be reviewed, demoed, or visually QA'd, and the Trade screen will keep rendering its stale-feed overlay over whatever is built.

### Blocker 2 — no local environment currently running (minor, local only)

Nothing is listening on `:8080` or `:5173` on this machine, so the app cannot be exercised locally either. The local `.env` is otherwise correct for offline development (`USE_MOCK_FEED=true`, `CORS_ORIGIN=http://localhost:5173`). One thing to watch: `DATABASE_URL` points at port **5434** while the Go config default is 5432, so the database must be started via `docker-compose` for the API to boot.

### Blocker 3 — price history depth (blocks a specific subset only)

Not a bug, but it hard-limits scope. With only a rolling ~5-minute in-memory buffer and no persistence, the existing 15-minute timeframe button is already close to a promise the data cannot keep, and anything longer, plus candles and daily percentage change, is unreachable until history is stored somewhere. This needs a decision before the chart scope is fixed, because it is the difference between a low-complexity polish pass and a high-complexity data project.

---

## 5. Out of scope (confirmed)

- **Automated / bot trading is explicitly out of scope for this report.** The benchmark's AUTO vs MANUAL toggle, any auto-trade execution logic, and the use of target or stop-loss values as bot limits were not investigated, scoped, or estimated here. It is on record as a separate future initiative with its own investigation.
- Related note so it is not conflated later: TradeDerivPlus *does* have an EMA/RSI **signal** feature (`/signals`, plus the signal strip on the Trade screen). That is advisory only — a signal can prefill the order ticket, but a human still presses Buy or Sell. It is not automated trading and is not part of the out-of-scope item above.
- **New trading features** generally — expiries, multipliers, contract types, payout structures, and additional instruments are not part of this look-and-feel pass.
- **The admin console** (`/admin`) is a separate desktop-first surface and was not audited.
- **Light mode / theming.** The app is deliberately dark-only (`docs/ui-ux-plan.md:15`) and the approved palette is being preserved.

---

## 6. Open questions for the team

1. **Is a production `VITE_WS_URL` set in the Vercel project?** The repository cannot answer this — it is a dashboard value. Needed: the exact configured value, and confirmation of whether `wss://` points straight at Railway or through a Vercel rewrite.
2. **Does Vercel forward WebSocket upgrades through the `/api/*` rewrite?** This determines whether the frontend can use a same-origin relative WebSocket path or must target Railway directly. Not answerable from code.
3. **What is `CORS_ORIGIN` set to on Railway?** The WebSocket origin check will reject the deployed frontend unless it matches exactly, scheme and host, with no trailing slash.
4. **Is `USE_MOCK_FEED` true or false in production, and is a real Deriv feed intended for this build?** If the client expects real market data rather than a simulated walk, that changes both the blocker fix and the history conversation.
5. **Is there a price-history service provisioned but not wired up?** Nothing in the repository suggests one — no ticks or candles table, no history client, no migration. Confirming this is genuinely absent decides whether candles and long timeframes are in or out.
6. **How should "payout" be presented, given the product is not binary?** The benchmark shows a fixed-return payout; TradeDerivPlus profit scales with price movement. Options include projected P/L at the take-profit level, P/L per pip, or a small risk/reward ratio. This is a product and client-expectation decision, not a technical one.
7. **What does "session" mean to the client?** Since the day the app opened, the last 24 hours, or since a deliberate "start session" action? Nothing in the data model expresses it today, and the existing "Today" tile is really a rolling 24-hour sum.
8. **Should the "History" view span accounts, or stay per-account?** Everything today is scoped to the active account, and demo and real are strictly separated.
9. **Can we get the TNNbinary reference screenshots?** They are not in the repository or `docs/`. Several judgements — exact chart density, how the risk controls are laid out, what the header packs in, how many session statistics are shown at once — rest on reading the visual rather than the feature list, and the complexity estimates for chart work may move once they are reviewed.
10. **Is a modest bundle-size increase acceptable on the Trade screen?** Adopting a dedicated charting library adds roughly 45kB gzipped on top of Recharts, which stays for the admin dashboards. The Trade screen is already the heaviest route by design.
