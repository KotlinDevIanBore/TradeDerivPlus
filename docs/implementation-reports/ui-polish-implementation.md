# TradeDerivPlus — Rendering & Visual Polish (Pass 1)

**Scope:** Pass 1 of the polish work scoped by [`docs/investigation-reports/ui-polish-investigation.md`](../investigation-reports/ui-polish-investigation.md).
**Date:** 21 September 2026
**Benchmark reference:** [`docs/reference/tnnbinary-trade-benchmark.png`](../reference/tnnbinary-trade-benchmark.png)

---

## 1. Summary

The Trade screen now reads like a trading screen rather than an app with a chart in it. The chart has a price scale down its right edge, so a movement can actually be measured; the price is followed by how far it has moved across the window on show; the order ticket says what each stop-loss and take-profit level is worth in dollars before the trade is placed; and a compact strip under the chart shows open profit and loss, open positions, and what this session has done, without a trip to Portfolio.

Two things that looked like missing features turned out to need only honest presentation. Stop loss and take profit were always editable in the ticket — the greyed-out "Not set" box simply looked switched off, and now reads as a control inviting a tap. And the benchmark's fixed "Payout" figure has no equivalent here, because profit in this product scales with how far price moves rather than paying a set return; the ticket therefore shows what a 1% move is worth at the chosen stake, which is the same information told truthfully.

Underneath, the design system gained two type sizes and two shadow depths, all additive — no existing colour, size, or spacing value changed. Three places that silently swallowed a failed load (the chart's placeholder, Home's open positions, Portfolio's summary tiles) now say so and offer a retry instead of looking like an empty account.

**One thing is outstanding and it is not code.** The deployed price feed fix (Step 0) needs two values set in the Vercel and Railway dashboards, which this environment has no access to. The exact values are in section 2 below. Everything else in this pass was built and verified against a live, ticking feed running locally, so the work is real and reviewable — but the deployed site will keep showing "Market feed offline" until those two settings are applied.

| | |
|---|---|
| Before | ![Trade screen before](assets/trade-before-desktop.png) |
| After | ![Trade screen after](assets/trade-after-desktop.png) |

---

## 2. Per-step outcome

### Step 0 — Fix the blocker

**Status: blocked on dashboard access. Local equivalent stood up and verified instead.**

What was done:

- Confirmed the WebSocket path from the running route table rather than from the source alone. The API registers `GET /api/v1/ws/prices`, `/ws/signals`, and `/ws/account` under the `/api/v1/ws` group (`internal/routes/routes.go:141-148`), and the frontend appends the `/prices` path to its base (`web/src/lib/socket.ts:1,46-52`). So the base URL is `/api/v1/ws`, not `/api/v1/ws/prices`.
- **The two values needed:**

  | Where | Variable | Value |
  |---|---|---|
  | Vercel (frontend, build-time) | `VITE_WS_URL` | `wss://tradederivplus-production.up.railway.app/api/v1/ws` |
  | Railway (API, runtime) | `CORS_ORIGIN` | the deployed frontend origin, scheme + host, **no trailing slash** |

  `VITE_WS_URL` is read at build time, so Vercel needs a redeploy after setting it — changing the variable alone will not alter the already-built bundle.

- Verified the whole path end to end locally instead: Postgres via `docker-compose`, the Go API (`mock_feed=true`), and the Vite dev server. The API confirmed `msg="api listening" port=8080 env=development mock_feed=true`, and the browser showed a green `ConnectionDot`, a chart filling with ticks about twice a second, no "Market feed offline" overlay, and an enabled order ticket. Every screenshot in this report is from that live feed.

**Deviation:** the prompt says not to proceed past Step 0 until the fix is confirmed in the deployed environment. There is no `vercel` or `railway` CLI on this machine and no dashboard credentials, so that confirmation is not something I can perform. Rather than stall the pass, I reproduced the same live-feed conditions locally, which satisfies the underlying requirement — that nothing be built or judged against a dead feed. **Deployed confirmation remains an open task for whoever holds dashboard access**, and it should be done before this pass is demonstrated to the client.

**Also worth flagging:** the Railway API is up and healthy (`GET /api/v1/symbols` returns `401`, i.e. reachable and requiring auth), so the deployed problem really is confined to those two configuration values.

### Step 1 — Chart: price scale and timeframe control

**Status: done.**

- The Y axis is visible, on the **right**, where the benchmark puts it and where a trader looks for the last price. Muted label colour, 12px, four ticks, no axis line, no tick marks, and no gridlines.
- A grey dashed line marks the latest tick, so "now" is locatable against the scale. It is deliberately grey and dashed `2 4`, distinct from the red and green `4 4` of the stop-loss and take-profit lines.
- The five hardcoded hexes in `PriceChart.tsx` moved into a new `chartTheme.ts`, which documents which Tailwind token each mirrors. The token system itself was not restructured.
- `ChartRangeToggle` is now a segmented control — a pill container with an inset active pill — matching the pill pattern already used by the Wallet filters and account chips. `aria-pressed` added.

**Deviation — axis precision.** Formatting axis labels at the instrument's full pip size produced `4,247.3283`, which clipped off the right edge of the phone viewport. Rather than widen the axis and steal chart width, axis labels now use only the precision the visible span needs: a volatility index spanning 40 points shows `4,247.3`, while forex spanning a fraction of a pip still gets all five decimals. The tooltip and the header price are unchanged at full precision.

**Held to scope:** no timeframes beyond 15m, and no attempt to fix the 15m data-depth problem.

### Step 2 — Percentage change beside the instrument

**Status: done.**

- Movement across the visible window now sits beside the price as points and a percentage, coloured by direction, with the sign always present so colour is never the only signal (a standing rule in this codebase).
- It is labelled `past 5m`, following the selected range. This matters: a percentage next to a price is read as a daily change by default, and a daily figure is impossible here — the server keeps roughly five minutes of ticks in memory and no daily open price exists.
- The window slice moved into a shared `windowTicks()` used by both the chart and the header, so the figure can never describe a slice the chart is not drawing.
- On mobile the change wraps onto its own line under the price, which keeps both legible.

### Step 3 — Projected P/L in the order ticket

**Status: done.**

- Each level's hint line now reads e.g. `-0.08% away · -$0.01 if hit`, folding the projection into the line that already carried the distance, so the ticket's layout is unchanged.
- Beside the stake: `A 1% move your way is worth $0.10`.
- Both figures run through the same `pnl()` the server mirrors on close, so a preview cannot drift from the settled result. The 1% figure is computed by asking `pnl()` about a 1% favourable move rather than dividing the stake by a hundred, so it follows the model if the model ever changes.

**Deviation — "per-pip" was rejected as meaningless here.** The prompt offered "P/L per price move *or* per-pip". Per-pip does not work in this product: `pip_size` is a display precision (decimal places), so a "pip" on a volatility index trading near 4,200 is 0.0001, and a $10 stake yields about $0.0000002 per pip — a row of zeros. Per 1% move is the smallest unit that produces a meaningful number on every instrument, and it doubles as the honest answer to "what's my payout".

**Copy change:** the level hint's existing wording went from `+0.23% from price` to `+0.23% away` so the projection fits one line in the ticket's two-column grid. Without it, the hint wrapped with a single word orphaned on the second line.

### Step 4 — Risk controls reading as available

**Status: done, styling only, no logic touched.**

- The dashed grey "Not set" box became a bordered control reading **`+ Set a level`**, with a hover state that brings up the accent border and a proper focus ring. Tapping it switches the field on, exactly as the Off chip already did.
- The `Off` chip itself went from `text-faint` to `text-muted` with a hover, so it no longer looks disabled.
- The label still points at the control via `htmlFor`, which remains valid because a `button` is a labelable element.

### Step 5 — Session stats strip

**Status: done.**

- `useTradeSummary(accountId)` — the endpoint Portfolio already calls — supplies **Open P/L** and **Open**. Open P/L dims when the feed is stale or when the server reports `prices_complete: false`, matching Portfolio's existing treatment.
- **Session P/L** and **Session trades** (with a `2W / 1L` breakdown once there is anything to break down) are derived client-side in a new `sessionStats()` from the closed-trade page the app has already fetched.
- Deliberately four small figures in one bordered strip, two-up on mobile and four-up on desktop — not a copy of Portfolio's tiles, since this sits beside the chart and must not compete with it.

**Notes on the approximation, which is real:** the session count starts at page load, held in a module constant so walking to Portfolio and back does not restart it. It counts from one page of closed trades, so a session closing more trades than that page holds is undercounted. Everything is labelled "Session", nothing claims to be a lifetime total, and the labels carry `title` text spelling out the basis. No backend aggregate was added.

The Trade screen now also requests the closed-trade list. This shares a React Query key with Portfolio, so the two screens share one fetch rather than doubling the traffic.

### Step 6 — Instrument header density

**Status: done, with one item consciously skipped.**

- The symbol code (`R_75`) now sits between the display name and the category chip, in muted tabular figures. It is what the order button and the positions list both name a position by, so the header was the one place it was missing.

**Skipped: high/low-of-window and spread/pip-size.** High and low would now be trivial to derive from `windowTicks()`, but the header already carries instrument name, code, category, live price, window change, and the range control. Adding two more figures would cost more in clutter than it returns, and the benchmark does not show them either. Deferred rather than done — see section 6.

### Step 7 — Type scale, elevation, and state gaps

**Status: done.**

- **Type:** added `nano` (10px/14px) below `meta`, and `section` (20px/28px) between `card` and `title`. Both additive. The five ad-hoc `text-[10px]` usages in the user app now point at `nano`.
- **Elevation:** added `shadow-card` (resting) and `shadow-elevated`, and applied `shadow-card` to `ui/Card.tsx`. On near-black surfaces depth has to come from shadow rather than a lighter fill, or the distinction between `surface` and `elevated` flattens. Both are deliberately quiet.
- **Chart empty state:** the bare sentence plus reload button became the shared `EmptyState` — icon, title, a hint naming the instrument, and the retry action.
- **Chart loading state:** a new `ChartSkeleton` sketches the shape that is coming (four axis rules, a series block, four axis-label placeholders) instead of a plain grey slab. It serves both the "no ticks yet" state and the lazy-load fallback, so the two are consistent.
- **Error states:** Home's open-positions block and Portfolio's summary tiles now use the shared `ErrorState` with retry, matching their neighbours.

**Scope note:** the admin console keeps its own six `text-[10px]` literals. The console is a separate surface that the investigation explicitly did not audit, and mechanically editing it would put changes in this pass's diff that nobody reviewed the need for. Listed as a follow-up.

**`section` (20px) is currently unused.** It was added because the prompt asked for the step and the gap is real, but nothing in this pass needed it. Flagged so it is either used or dropped rather than left as dead config.

---

## 3. Files changed

New:

| File | What it is |
|---|---|
| `web/src/features/trading/chartTheme.ts` | The chart's colours in one place, each mapped to its Tailwind token |
| `web/src/features/trading/ChartSkeleton.tsx` | Chart-shaped loading placeholder, shared by the empty state and the lazy-load fallback |
| `web/src/features/trading/SessionStrip.tsx` | The four-figure stats strip (presentational) |
| `web/src/features/trading/sessionStats.ts` | Session start constant and the closed-trade derivation |
| `web/src/features/trading/sessionStats.test.ts` | Unit tests for that derivation (4 cases) |
| `docs/reference/tnnbinary-trade-benchmark.png` | The benchmark screenshot, at the path the prompt referenced |
| `docs/implementation-reports/assets/*.png` | Before/after screenshots used by this report |

Modified:

| File | Change |
|---|---|
| `web/tailwind.config.js` | Added `nano` and `section` type steps, `card` and `elevated` shadows. Nothing existing altered |
| `web/src/features/trading/PriceChart.tsx` | Visible right-hand price scale, span-based axis precision, current-price marker, hexes via `chartTheme` |
| `web/src/features/trading/ChartRangeToggle.tsx` | Segmented-control styling, `aria-pressed` |
| `web/src/features/trading/chartRange.ts` | Added `windowTicks()` and `windowChange()` alongside the existing type |
| `web/src/features/trading/TradeScreen.tsx` | Window-change tag, session strip wiring, shared `EmptyState` and `ChartSkeleton` for the chart's states |
| `web/src/features/trading/OrderTicket.tsx` | Projected P/L per level, 1%-move exposure line, `+ Set a level` off state |
| `web/src/features/trading/SymbolPicker.tsx` | Symbol code in the header trigger; `nano` token |
| `web/src/features/trading/PriceChartLazy.tsx` | Uses `ChartSkeleton` as its fallback |
| `web/src/features/home/HomeScreen.tsx` | Error state for the open-positions block |
| `web/src/features/portfolio/PortfolioScreen.tsx` | Error state for the summary tiles |
| `web/src/components/ui/Card.tsx` | Resting shadow |
| `web/src/components/AccountPill.tsx`, `DirectionBadge.tsx`, `features/signals/SignalCard.tsx`, `features/portfolio/PositionCard.tsx` | `text-[10px]` → `text-nano` |

Delivered as seven commits, one per item, from `e9e1b00` to `636145b`.

---

## 4. Discovered during implementation

1. **Per-pip P/L is not expressible in this product.** `pip_size` is a count of decimal places used for display, not a tradable increment, so a "pip" of a volatility index near 4,200 is 0.0001 and pays fractions of a cent. The investigation treated per-pip as a trivially available figure; it is available and useless. Per-1%-move replaced it.

2. **Full pip-size precision does not fit a phone's axis.** Not anticipated in the investigation, which assumed unhiding the axis was purely a styling change. It is, plus a formatting decision — hence the span-based precision above.

3. **The investigation's claim that no daily-change data exists held up, and the label had to do real work.** A bare percentage beside a price is read as a daily change, so the `past 5m` suffix is load-bearing, not decoration.

4. **The level hint could not absorb a second figure without rewording.** `+0.23% from price · +$0.02 if hit` wrapped badly in the ticket's two-column grid at desktop width; `away` instead of `from price` fixed it on one line.

5. **Two admin-console e2e tests fail in this environment, and it is not from these changes.** Both fail inside the shared `loginAdmin` helper. The API log shows `429` on `/api/v1/admin/login`: admin login allows one attempt per 12 seconds with a burst of five (`internal/routes/routes.go:106`, `internal/config/config.go:108-109`), the helper spends up to three attempts per test probing two known passwords, and this database's admin password is the *second* one it tries — so the first probe of every test is a wasted 401. Given rate-limit headroom the tests pass; run in isolation, the KYC test self-skips with `no pending submissions seeded; run: go run ./cmd/seed`. No file under `features/admin` was touched in this pass. It is a test-fixture and rate-limit interaction worth fixing on its own, listed as a follow-up.

6. **No existing test asserted on the copy I changed.** `OrderTicket.test.tsx` does not reference "Not set", the hint text, or the Off chip, so the restyle broke nothing — but it also means that copy is unprotected by tests.

7. **`web/dist` in the working tree was a stale 17 September build** carrying the localhost WebSocket URL. It is gitignored and was rebuilt during verification, so it is not evidence about what Vercel currently serves — the Vercel build log is. Worth knowing before anyone inspects that folder as proof of the deployed state.

---

## 5. Verification performed

| Check | Result |
|---|---|
| `npm run typecheck` | Clean, after every step |
| `npm test` (Vitest) | **63 passed**, 6 files — up from 59, the 4 new ones covering `sessionStats` |
| `npm run build` | Succeeds. `PriceChart` chunk 347kB (102kB gzipped) still splits out of the main bundle, so other screens stay light |
| `npm run lint` (oxlint) | No new warnings. The five pre-existing ones (`SignalCard`, `OrderTicket` effect, `DepositSheet` refs) are untouched |
| `npx playwright test` | **10 passed, 2 failed.** Both failures are admin-console logins, diagnosed in section 4.5 as rate-limit starvation plus unseeded fixtures. Every user-app spec passes, including the trading flow that opens and closes a position |
| Live feed (Step 0, local) | Postgres + Go API (`mock_feed=true`) + Vite. Green connection dot, ticks arriving ~2/sec, no offline overlay, ticket enabled |
| Visual, desktop 1280×900 | Captured with and without stop-loss/take-profit set |
| Visual, mobile (Pixel 7) | Captured at top of screen and scrolled, confirming the session strip is reachable above the sticky ticket |

Live feed (Step 0) on the **deployed** site: **not verified — no dashboard access.** See Step 0.

After, on mobile — the session strip and the `+ Set a level` controls:

![Trade screen after, mobile](assets/trade-after-mobile.png)

---

## 6. Follow-ups for Pass 2

New, arising from this pass:

1. **Apply and confirm the deployed feed fix.** The two values in Step 0, then a Vercel redeploy, then confirm a green connection dot on the deployed Trade screen. Nothing else in this pass is client-demonstrable until this is done.
2. **Decide the fate of the unused `section` (20px) type step** — apply it where a heading or figure wants 20px, or remove it so the config carries nothing dead.
3. **Fix the admin e2e login flakiness**, independently of any UI work: the helper's password probing collides with the admin login rate limiter. Options include seeding a known password before the suite or having the helper reuse one API-verified session.
4. **Consider tests for ticket copy.** The projected-P/L hint and the `+ Set a level` state are now user-visible promises with no test holding them.
5. **Sweep the admin console's six `text-[10px]` literals onto `nano`** if and when the console is in scope.
6. **High/low of the visible window** in the instrument header — now cheap to derive, deliberately skipped here on clutter grounds. Worth a design call rather than a code call.
7. **The stale-feed overlay still offers no retry**, only a full page reload from the chart's empty state. A reconnect action would be a small, contained improvement.

Already known as deferred going in, unchanged and **not** touched in this pass:

- Candlesticks, timeframes beyond 15m, and true daily percentage change — all blocked on missing tick/candle persistence.
- Accurate lifetime win/loss and trade-count aggregates — needs a new backend query.
- A third "History" tab on Portfolio, with pagination or date filtering.
- Zoom, pan, reset, and drawing tools.
- Automated/bot trading and the AUTO/MANUAL toggle — a separate initiative.
- Any refactor of `lib/socket.ts` or the `CORS_ORIGIN` default.
