# TradeDerivPlus — UI/UX Plan (screen-by-screen)

## Context

The technical spec defines stack, schema, API contract and folder layout for TradeDerivPlus, a
Deriv-style trading app with M-Pesa deposits and a signal bot. This document is the UI/UX
planning pass. It hands the implementing session a screen-by-screen breakdown: what each screen
is for, which components it is built from, which API calls feed it, how it behaves in
loading / empty / error states, and how it should look. No code is written here.

Assumptions made (flag to the user if wrong):
- **Mobile-first responsive web.** The reference screenshot is a phone layout and the spec
  mentions mobile clients later. Desktop gets a left sidebar instead of the bottom tab bar;
  everything else is the same components at wider breakpoints.
- **Dark-only theme.** Near-black surfaces, one red accent. No light mode in v1.
- **Demo account is the default active account** after registration. Real account is created
  by the user from the account switcher.
- Amounts display in the account's `currency` (USD) except M-Pesa amounts, which are KES.

The spec is the source of truth. Where this plan needs something the API contract does not yet
provide, it is listed in **"API contract gaps"** at the end as proposed extensions, not forks.

---

## Design language (applies to every screen)

**Surfaces**
- Page background: `#0A0A0B`. Cards: `#141416` with a 1px `#232326` border, 16px radius.
  Elevated card (dialogs, sheets): `#1A1A1D`.
- One accent: red `#E24B4A`. Used for primary CTAs, active tab indicator, "sell"/loss values,
  and destructive confirmations. Do not use it for decoration.
- Semantic greens are allowed only for profit and "buy": `#22C55E`. Amber `#F59E0B` for
  pending / warning. Muted text `#8A8A93`, primary text `#F4F4F5`.
- Demo vs Real must be visually unmistakable: a small pill next to the balance
  (`DEMO` in amber outline, `REAL` in green outline). The pill appears in the header on
  every screen, not just Home.

**Type**
- Bold geometric sans for headings (Inter / Geist, weight 700). Balance and prices use
  `tabular-nums` so digits do not jitter as ticks arrive.
- Sizes: page title 24/32, card title 16/24, body 14/20, meta 12/16, hero balance 36/40.

**Spacing**
- 16px page gutter on mobile, 24px on desktop. 12px gap between cards. Generous vertical
  whitespace inside cards (20px padding).

**Motion**
- Price changes flash text color (green up / red down) for 300ms then settle. No layout
  animation on tick updates.
- Sheets slide up on mobile, dialogs fade on desktop. Both use shadcn `Dialog` / `Drawer`.

**Number formatting (`/lib/formatters`)**
- Money: `$1,234.56`, always 2dp, negative as `-$12.30` in red.
- Prices: 5dp for forex, 2–3dp for volatility/boom-crash (take from symbol category).
- Relative time: "just now", "3m ago", "2h ago", then a date.

**Shared composites (`/components`)**
- `PriceTag` — price + direction flash + optional delta.
- `StatusBadge` — pending (amber) / completed (green) / failed (red) / open / closed.
- `DirectionBadge` — BUY (green) / SELL (red), filled, uppercase, 11px.
- `AccountPill` — `DEMO` / `REAL` + account number.
- `EmptyState` — icon, one-line title, one-line hint, optional CTA.
- `ErrorState` — one-line message + Retry button; never a raw error string.
- `ConnectionDot` — WebSocket status (green live / amber reconnecting / grey offline).
- Skeletons for every card (shadcn `Skeleton`, same dimensions as the loaded card).

---

## Navigation shell

**Screen name:** App shell (`/`)

**Purpose:** Persistent frame around every authenticated screen. Holds account context,
navigation, and live-connection status.

**Components**
- Top bar: app wordmark left, `AccountPill` (tap opens Account Switcher sheet) center,
  `ConnectionDot` + avatar/menu right.
- Bottom tab bar (mobile) / left sidebar (desktop): Home, Trade, Portfolio, Wallet.
  Active tab uses the red accent underline/icon.
- Global toast region (shadcn `Toaster`) for signal pushes, deposit confirmations, trade
  closes.

**Data needed**
- `GET /api/v1/accounts` — populates the switcher and picks the active account
  (Zustand `activeAccountId`, persisted to localStorage).
- `WS /api/v1/ws/signals` — subscribed once at shell level; new signals invalidate the
  React Query `signals` key and raise a toast.
- Token refresh via `POST /api/v1/auth/refresh` on 401 (silent, in the API client).

**States**
- Loading: top bar renders immediately; `AccountPill` shows a skeleton until accounts load.
- Empty (no accounts at all): should not happen post-registration, but if it does, show a
  full-screen "Create your first account" panel that calls `POST /api/v1/accounts`.
- Error (accounts fetch fails): pill shows "—", tabs still work, a slim red banner under the
  top bar says "Couldn't load accounts" with Retry.
- WS disconnected: `ConnectionDot` turns amber with tooltip "Reconnecting…"; after 3 failed
  retries it turns grey and a banner offers "Reconnect".

**Visual notes**
- Bottom bar is 64px, blurred near-black, hairline top border. Icons 24px, labels 11px.
- Unauthenticated routes (`/login`, `/register`) render without the shell.

---

## Screen: Login

**Route:** `/login`

**Purpose:** Authenticate an existing user and land them on Home.

**Components**
- Wordmark + one-line tagline.
- `LoginForm`: phone number (with `+254` prefix, digits only), password, "Log in" primary
  button, "Forgot password?" text link (non-functional placeholder in v1, no endpoint).
- Footer link: "New here? Create account".

**Data needed**
- `POST /api/v1/auth/login` → access + refresh tokens stored in Zustand `session`.
- On success, prefetch `GET /api/v1/accounts` before navigating.

**States**
- Loading: button shows spinner, form disabled.
- Error: inline red text under the form ("Phone number or password is incorrect"). Network
  error → "Can't reach the server. Check your connection." Never show the raw API error.
- Validation: phone must be 9–12 digits; password non-empty. Validate on blur, not on every
  keystroke.

**Visual notes**
- Centered single column, max-width 400px. No card border on the form itself; the page
  background is the surface. Red accent only on the primary button.

---

## Screen: Register

**Route:** `/register`

**Purpose:** Create a user, auto-create a demo account, land on Home with the demo account
active.

**Components**
- `RegisterForm`: full name, phone number, email (optional), password, confirm password,
  "Create account" primary button, checkbox "I agree to the terms" (required).
- Footer link: "Already have an account? Log in".

**Data needed**
- `POST /api/v1/auth/register` → tokens.
- Then `GET /api/v1/accounts`. If empty, `POST /api/v1/accounts` with `account_type: demo`
  (see gap G1: ideally the backend creates the demo account on register).

**States**
- Loading / error as Login. Duplicate phone → "This number is already registered" with a
  "Log in instead" link.
- Password rules shown as a small checklist that ticks off live (min 8 chars, one number).

**Visual notes**
- Same layout as Login. Password strength is a 3-segment bar in muted → amber → green.

---

## Screen: Home

**Route:** `/`

**Purpose:** The daily landing screen. Shows how much money the user has, which account they
are on, and the latest signal so they can act in one tap.

**Components** (top to bottom)
1. `BalanceCard` (hero) — label "Total balance", hero number, split row "Real $x · Bonus $y",
   `AccountPill`, two buttons: **Deposit** (red, primary) and **Withdraw** (outline).
2. `AccountSwitcherStrip` — horizontally scrollable chips, one per account
   (`DEMO 900001`, `REAL 900002`, `+ New account`). Tapping switches `activeAccountId`.
3. `SignalCard` (latest signal across all symbols, or for the user's last-traded symbol):
   - Top row: `DirectionBadge`, symbol display name, confidence as `78%` with a thin
     progress bar beneath.
   - Middle: entry / SL / TP in a 3-column grid, labels in muted 12px, values tabular.
   - Bottom row: "Updated 3m ago" (relative, ticks every 30s) + strategy tag (`EMA+RSI`),
     and a full-width CTA **"Trade this signal"** that deep-links to Trade with symbol,
     direction, SL and TP pre-filled.
4. `OpenPositionsSummary` — compact list of up to 3 open trades (symbol, direction, live
   PnL), "View all" link to Portfolio. Hidden entirely if no open trades.
5. `RecentActivity` — last 5 transactions (type icon, amount, status badge, time), "See all"
   link to Wallet.

**Data needed**
- `GET /api/v1/accounts/:id/wallet` for the active account — `real_balance`,
  `bonus_balance`. Refetch on window focus and on any WS balance update (gap G2).
- `GET /api/v1/signals?limit=1` (gap G3: needs a "latest across symbols" mode, or fetch
  per-symbol and pick newest client-side).
- `GET /api/v1/trades?status=open` (first 3).
- `GET /api/v1/wallet/transactions?limit=5`.
- `WS /api/v1/ws/prices?symbols=<open trade symbols>` for live PnL on the summary.

**States**
- Loading: every card renders its skeleton at the same height as the loaded version so the
  page does not jump. Balance skeleton is a wide bar where the number goes.
- Empty:
  - No signals yet → `SignalCard` shows "The bot hasn't published a signal yet" with a
    muted bot icon; no CTA.
  - No trades → section hidden.
  - No transactions → "No activity yet. Make your first deposit." with Deposit CTA.
- Error:
  - Wallet fails → `BalanceCard` shows "—" for the number and an inline Retry; Deposit
    button stays enabled (deposit does not depend on reading the balance).
  - Signal fails → card shows `ErrorState` inline. Other cards are independent; one
    failing does not blank the page.
- Demo account active: `BalanceCard` gets a subtle amber left border and the Deposit button
  reads **"Top up demo"** (still calls the same endpoint; backend decides). Withdraw is
  hidden on demo.
- KYC not verified + Real account active: Withdraw button disabled with helper text "Verify
  your identity to withdraw" (gap G4: no KYC endpoint; link to a placeholder screen).

**Visual notes**
- This is the screen that must match the reference: balance number is the single biggest
  thing on the page; the signal card is the second. Everything else is quiet.
- `SignalCard` confidence bar is red-tinted at low confidence (<50), amber 50–70, green >70.

---

## Screen: Account Switcher (sheet)

**Route:** none, sheet over any screen

**Purpose:** Switch the active account or create a new one.

**Components**
- List of accounts: `AccountPill`, account number, balance (cached from React Query), check
  mark on the active one.
- "Create real account" / "Create demo account" buttons (each only shown if that type does
  not exist yet; assume one of each max per user in v1).

**Data needed**
- `GET /api/v1/accounts`, `GET /api/v1/accounts/:id/wallet` per account (parallel, cached).
- `POST /api/v1/accounts` `{ account_type }`.

**States**
- Loading: two skeleton rows.
- Error on create: toast "Couldn't create account" + Retry.
- Switching account invalidates wallet, trades, transactions queries and resubscribes the
  price WS with that account's open-trade symbols.

**Visual notes**
- Bottom sheet, drag handle, max 60% viewport height.

---

## Screen: Trade

**Route:** `/trade?symbol=R_75&direction=buy&sl=…&tp=…` (all query params optional)

**Purpose:** Pick a symbol, watch the live price, place a trade.

**Components**
1. `SymbolPicker` — header pill showing current symbol + category tag; tapping opens a sheet
   grouped by category (Volatility / Boom & Crash / Forex) with search. Each row shows
   display name, code, live price if subscribed, and a star for favourites (localStorage).
2. `PriceHeader` — big `PriceTag` (current tick), tiny sparkline of last 60 ticks, and the
   `ConnectionDot`.
3. `PriceChart` — line chart of the tick stream (lightweight-charts or Recharts). Ticks only
   in v1 since there is no candle endpoint (gap G5). Time range toggle: 1m / 5m / 15m of
   buffered ticks. Overlay horizontal lines for SL and TP when set in the form.
4. `OrderTicket` (sticky bottom card on mobile, right column on desktop):
   - `DirectionToggle` — two large segmented buttons **BUY** (green) / **SELL** (red).
   - Stake input with currency prefix, quick-add chips (`+5`, `+10`, `+50`, `Max`).
   - Stop loss and Take profit inputs (price), each with a "Off" toggle. Show distance from
     current price as a muted hint ("−1.2%").
   - Balance line: "Available $1,234.56" (real + bonus, per active account).
   - Primary CTA: **"Buy R_75 for $10"** — label updates live with direction and stake.
5. `LatestSignalStrip` — one-line strip above the ticket: "Signal: BUY 78% · 3m ago · Apply".
   Apply fills direction/SL/TP.
6. `OpenPositionsForSymbol` — collapsible list under the chart of open trades on this symbol
   with live PnL and a Close button each.

**Data needed**
- `GET /api/v1/symbols` — cached for the session.
- `WS /api/v1/ws/prices?symbols=<selected>` — `usePriceStream(symbol)` hook keeps a ring
  buffer of ticks in Zustand.
- `GET /api/v1/accounts/:id/wallet` — available balance.
- `GET /api/v1/signals?symbol=<code>&limit=1`.
- `GET /api/v1/trades?status=open` filtered by symbol client-side.
- `POST /api/v1/trades` `{ account_id, symbol_id, direction, stake, stop_loss?, take_profit? }`.
- `POST /api/v1/trades/:id/close`.

**States**
- Loading: chart area shows a skeleton with "Connecting to market…"; ticket is disabled
  until the first tick arrives (entry price needed for SL/TP hints).
- Empty: no ticks after 5s → "No price data for this symbol yet" with Retry. Symbol picker
  with empty search → "No symbols match".
- Error:
  - WS dropped → chart freezes, amber overlay "Reconnecting…", ticket disabled. Never let a
    trade go out on a stale price without the user seeing the overlay.
  - `POST /trades` fails → inline error above the CTA, with the API's human message when it
    is a business rule ("Insufficient balance", "Market closed") and a generic one otherwise.
  - Symbol inactive (`is_active=false`) → picker greys it out with "Unavailable".
- Validation: stake > 0 and ≤ available; SL below entry for buy / above for sell; TP the
  inverse. Validate on change, show one message at a time under the offending field.
- Success: CTA briefly turns to a check mark "Position opened", the new trade appears in
  `OpenPositionsForSymbol`, toast with "View in Portfolio".
- Confirm step: on **Real** account with stake ≥ 20% of balance, show a confirmation
  dialog ("Buy R_75 for $500 — that's 40% of your balance"). Demo never confirms.

**Visual notes**
- The chart is the hero on this screen, edge-to-edge on mobile (no card border), ticket sits
  on top with a 16px radius top edge. BUY/SELL toggle is the only place green and red sit
  side by side; both at full saturation when selected, 30% opacity when not.
- Prices in the chart and header use tabular numerals and never reflow the layout.

---

## Screen: Portfolio

**Route:** `/portfolio` with tabs `?tab=open|closed`

**Purpose:** Monitor open positions with live PnL and review closed history.

**Components**
1. `PortfolioSummaryBar` — three tiles: Open PnL (live, signed, colored), Today's realised
   PnL, Open positions count.
2. `Tabs` — Open / Closed (shadcn `Tabs`, red underline).
3. `PositionCard` (Open tab):
   - Row 1: symbol name, `DirectionBadge`, stake.
   - Row 2: entry → current `PriceTag`, live PnL large and colored.
   - Row 3: SL / TP chips (muted, "—" if unset), "Opened 12m ago".
   - Swipe-left on mobile or trailing button on desktop reveals **Close** (red outline).
   - Tapping opens `PositionDetailSheet` with the same info plus a mini chart and a
     full-width **Close position** button.
4. `ClosedTradeRow` (Closed tab): compact row — symbol, direction, stake, realised PnL,
   closed time. Grouped by day with sticky day headers.
5. `CloseConfirmDialog` — "Close R_75 BUY at 1234.56? Estimated PnL −$3.20".

**Data needed**
- `GET /api/v1/trades?status=open` — refetch every 10s as a fallback; PnL computed
  client-side from the live tick: `(current − entry) × stake / entry` for buy, negated for
  sell (gap G6: confirm PnL formula with backend; the client must match `pnl` on close).
- `GET /api/v1/trades?status=closed` — paginated (gap G7: contract has no pagination params
  on trades).
- `WS /api/v1/ws/prices?symbols=<all open symbols>`.
- `POST /api/v1/trades/:id/close`.

**States**
- Loading: summary tiles skeleton + 3 card skeletons.
- Empty:
  - Open → "No open positions" with **"Go to Trade"** CTA.
  - Closed → "No closed trades yet".
- Error: `ErrorState` in the list area; summary bar shows "—".
- Closing: card gets a spinner overlay; on success it animates out and a toast shows the
  realised PnL; on failure the overlay clears and a toast says "Couldn't close. Try again."
- Price stream down: PnL values dim to 50% and show "stale" tag; Close stays enabled (the
  backend prices the close, not the client).

**Visual notes**
- PnL is the loudest number on each card. Green/red text only, no colored backgrounds, so a
  list of ten positions does not look like a Christmas tree.

---

## Screen: Signals

**Route:** `/signals` (reachable from Home signal card "See all" and a header icon; not a
bottom tab)

**Purpose:** Browse recent bot signals per symbol, with live pushes.

**Components**
- `SymbolFilterChips` — All + one chip per symbol that has signals (Volatility first).
- `SignalCard` list (same component as Home, compact variant: no CTA in the card, whole
  card tappable → Trade prefilled).
- "New signal" pill that slides in at the top when a WS push arrives while the user is
  scrolled down (like Twitter's "new posts").

**Data needed**
- `GET /api/v1/signals?symbol=<code>&limit=20` (gap G3 for "all symbols").
- `WS /api/v1/ws/signals` — prepend into the React Query cache.

**States**
- Loading: 4 compact card skeletons.
- Empty: "No signals for R_75 yet. The bot runs every N minutes." (N from config, or omit.)
- Error: `ErrorState` with Retry.
- Signals older than 30 minutes get a muted "Expired" tag and no tap-to-trade prefill of
  entry price (SL/TP still copied; user sees a warning in the ticket).

**Visual notes**
- Confidence bar color rule same as Home. Strategy tag is a monospace-feeling small chip.

---

## Screen: Wallet

**Route:** `/wallet`

**Purpose:** Money in, money out, and the ledger.

**Components**
1. `BalanceCard` (same as Home, without the account strip).
2. Action row: **Deposit** (red), **Withdraw** (outline).
3. `TransactionFilters` — segmented: All / Deposits / Withdrawals / Trades.
4. `TransactionList` — infinite scroll. Each `TransactionRow`: type icon (M-Pesa green
   for deposit, arrow-out for withdrawal, chart icon for trade_open/close, gift for
   bonus_credit), title ("M-Pesa deposit", "Trade R_75 BUY"), amount signed and colored,
   `StatusBadge`, relative time. Tap → `TransactionDetailSheet` (reference, metadata,
   absolute timestamp, "Copy reference").
5. Pending deposits pinned at the top with an amber left border and a "Waiting for M-Pesa"
   label (see Deposit flow).

**Data needed**
- `GET /api/v1/accounts/:id/wallet`.
- `GET /api/v1/wallet/transactions?page=&limit=&type=` (contract says paginated; confirm
  the param names, gap G7).

**States**
- Loading: balance skeleton + 6 row skeletons.
- Empty: "No transactions yet" with Deposit CTA; filtered-empty says "No deposits yet" etc.
- Error: `ErrorState` in the list; balance card independent.
- Demo account: Withdraw hidden, Deposit relabelled "Top up demo", ledger still shown.

**Visual notes**
- Rows are 64px, no card borders between rows, hairline dividers. Amount column
  right-aligned, tabular.

---

## Flow: Deposit (M-Pesa STK push)

**Route:** `/wallet/deposit` as a full-height sheet on mobile, dialog on desktop. Four steps
inside one component with a step indicator.

**Purpose:** Get money into the real account with as few taps as possible and make the
pending window feel safe.

### Step 1 — Amount and phone
- Components: amount input (KES, large, numeric keypad), quick chips
  (`500`, `1,000`, `2,000`, `5,000`), conversion hint "≈ $7.70 USD" (rate from config or
  omitted if unknown, gap G8), phone number field pre-filled from the user's profile and
  editable, primary CTA **"Send STK push"**.
- Data: user phone from session (gap G9: no `/me` endpoint; store phone at login/register).
- Validation: min/max amount (from config), Safaricom number format. Real account only;
  if a demo account is active, the sheet says "Switch to your real account to deposit" with
  a switch button.

### Step 2 — Sending
- `POST /api/v1/wallet/deposit` `{ account_id, amount, phone_number }` → returns
  `transaction_id` and `checkout_request_id`.
- UI: spinner with "Sending request to M-Pesa…". Disable back navigation for ~3s.
- Error: "M-Pesa didn't accept the request" + Retry (stays on step 1 with values kept).

### Step 3 — Pending (waiting for the user to enter their PIN on their phone)
- Components: large phone illustration, headline "Check your phone", body "Enter your
  M-Pesa PIN to complete the KES 1,000 deposit", a 60-second countdown ring, the
  transaction reference in small muted text, secondary link "Didn't get the prompt? Resend"
  (enabled after countdown), tertiary "I'll wait, close this" which dismisses the sheet but
  leaves the pinned pending row on the Wallet screen.
- Data: status arrives either by WS balance/transaction push (gap G2) or by polling
  `GET /api/v1/wallet/transactions?id=<transaction_id>` every 3s (gap G10: needs a
  single-transaction status endpoint). The plan recommends WS push plus polling fallback.
- Timeout: after 120s with no callback, move to a soft-failure state: "We haven't heard
  back from M-Pesa. If you completed the payment, your balance will update automatically."
  with "Try again" and "Contact support" links. Keep the ledger row as `pending`.

### Step 4 — Result
- Success: green check animation, "KES 1,000 deposited", new balance shown, buttons
  **"Start trading"** (→ Trade) and "Done" (→ Wallet). Balance card on Home/Wallet
  refetches.
- Failure (`result_code ≠ 0`): red X, human message mapped from `result_desc` (e.g.
  "You cancelled the request", "Insufficient M-Pesa balance"), **"Try again"** returns to
  step 1 with values kept.

**Visual notes**
- The pending step is the emotional peak of the app; give it the most polish. Countdown ring
  in red accent, phone illustration in muted greys, no other color.

---

## Flow: Withdraw

**Route:** `/wallet/withdraw` sheet/dialog.

**Purpose:** Send real balance to M-Pesa.

**Components**
- Amount input with "Available $x" line and **Max** chip; phone number field
  (pre-filled); fee/arrival note ("Arrives in 1–5 minutes, no fee" or whatever backend
  returns); primary CTA **"Withdraw"**; confirmation dialog restating amount and phone.

**Data needed**
- `POST /api/v1/wallet/withdraw` `{ account_id, amount, phone_number }`.
- `GET /api/v1/accounts/:id/wallet` for available balance. Only `real_balance` is
  withdrawable; the UI states "Bonus balance can't be withdrawn".

**States**
- KYC pending/rejected → the sheet opens directly to a "Verification required" panel.
- Loading: CTA spinner. Success: pending row in the ledger, toast "Withdrawal requested".
- Error: inline message; insufficient balance is caught client-side before the request.

---

## Screen: Profile / Settings

**Route:** `/profile` (from the header avatar)

**Purpose:** Minimal in v1. Show identity and KYC status, log out.

**Components**
- Name, phone, email row.
- KYC status card: `StatusBadge` pending / verified / rejected with one line of copy and a
  placeholder "Start verification" button (gap G4).
- Accounts list (read-only, links to switcher).
- "Log out" (outline red) → clears Zustand session and React Query cache.

**Data needed**
- Session user object (gap G9).
- `GET /api/v1/accounts`.

**States**
- Trivial; loading skeleton for the account list.

---

## Cross-cutting behaviours

- **Route guard:** unauthenticated → `/login` with `?next=`. Access token in memory,
  refresh token in httpOnly cookie if the backend supports it, else localStorage (decide
  with backend; note in implementation).
- **React Query keys:** `['accounts']`, `['wallet', accountId]`, `['trades', accountId,
  status]`, `['transactions', accountId, filters]`, `['signals', symbol]`, `['symbols']`.
  Switching account invalidates everything keyed by `accountId`.
- **WS lifecycle:** one `usePriceStream` per screen that needs prices, subscribing to the
  union of symbols it displays; unsubscribes on unmount. `useSignals` lives in the shell.
  Exponential backoff reconnect (1s → 30s), visible via `ConnectionDot`.
- **Optimistic UI:** none for money. Trades and deposits always wait for the server.
- **Accessibility:** every colored PnL/direction also has text (BUY/SELL, +/−), 44px tap
  targets, focus rings on the red accent, `aria-live="polite"` on balance and PnL regions.
- **Responsive:** ≥1024px uses a 2-column layout on Trade (chart left, ticket right) and
  Home (balance+signal left, positions+activity right). Sidebar replaces the bottom bar.

---

## API contract gaps (proposed extensions to the spec, not forks)

| # | Need | Proposal |
|---|---|---|
| G1 | Demo account on signup | `POST /auth/register` creates a demo account and returns it, or the client calls `POST /accounts` immediately after. |
| G2 | Live balance / transaction status | Add `WS /api/v1/ws/account` pushing `{ type: 'wallet' | 'transaction', payload }` for the active account, so deposit confirmations and trade closes update without polling. |
| G3 | Latest signal across symbols | Allow `GET /signals?limit=N` with no `symbol` to return newest across all symbols. |
| G4 | KYC | No endpoint. UI shows status from `users.kyc_status` and a placeholder CTA. |
| G5 | Chart history | No candle/tick-history endpoint. v1 chart is live ticks only; add `GET /symbols/:code/ticks?minutes=15` later. |
| G6 | PnL formula | Client computes live PnL for open trades; must match `trading_service.go`. Document the formula in the spec. |
| G7 | Pagination params | Define `?page=&limit=` (or cursor) for `GET /trades` and `GET /wallet/transactions`, plus `?type=` filter on transactions. |
| G8 | KES↔USD rate | Deposit is KES, wallet is USD. Either the backend returns the credited USD amount in the deposit response, or expose a rate endpoint. |
| G9 | Current user | Add `GET /api/v1/auth/me` (or return the user object from login/register). |
| G10 | Single transaction status | `GET /api/v1/wallet/transactions/:id` for deposit polling fallback. |

---

## Suggested build order for the implementing session

1. Design tokens, shared composites, app shell with auth guard and account switcher.
2. Login / Register.
3. Wallet + Deposit flow (the money path is the riskiest, get it right early).
4. Home (composes BalanceCard, SignalCard, summaries).
5. Trade (price stream hook, chart, order ticket).
6. Portfolio.
7. Signals list, Profile.

## Verification (for the implementing session)

- Each screen renders all four states (loading / empty / error / loaded) via Storybook-style
  fixtures or a mock API toggle; screenshot each on 375px and 1280px.
- Deposit flow tested against Daraja sandbox: success, user-cancel, timeout.
- Kill the WS mid-session on Trade and Portfolio; confirm the stale overlays appear and
  the ticket disables.
- Switch Demo → Real and confirm every balance, position and ledger view re-keys.

---

# Part 2 — Admin console (round two)

Same screen → purpose → components → data → states → visual format as Part 1. The admin
console is a separate route tree under `/admin`, with its own session, its own login, and
its own layout. It shares the design tokens and primitives but not the mobile-first
posture: an admin sits at a desk, so this is **desktop-first, data-dense**, with a fixed
left sidebar and tables rather than cards. It still has to be usable at tablet width for
a support person on the move, so nothing depends on hover.

## Admin design language (deltas from Part 1)

- **Surfaces and type are identical.** Same near-black, same red accent, same Inter.
- **Tables, not cards.** Rows 44px, hairline dividers, sticky header, right-aligned tabular
  numerals for money. Column headers in 12px muted uppercase.
- **The accent is reserved for destructive and money-moving actions**: Freeze, Adjust,
  Reject. Approve and Unfreeze are green outline. Everything else is neutral.
- **Every money-moving or state-changing action requires a reason and a confirmation.**
  No exceptions. The reason is written to `admin_audit_log`, and the confirmation restates
  exactly what will happen to whom.
- **The admin's identity is always visible** in the sidebar footer, with role and a
  "Change password" link. What they did is attributable, and they can see that.
- **Amber banner, full width, on every screen** while `must_change_password` is true:
  "You are using the default password. Change it now." with a link. In production the
  backend refuses the session outright; the banner is the dev-mode nudge.

**Shared admin composites (`/features/admin/components`)**
- `DataTable` — sticky header, row hover, empty and error slots, pagination footer
  ("Showing 1–25 of 412", prev/next).
- `SearchInput` — debounced 300ms, clears with Escape.
- `FilterChips` — same as the wallet's segment control.
- `ReasonDialog` — title, one-line consequence, required textarea (min 10 chars),
  confirm button in the action's colour. Used by Freeze, Reject, Adjust.
- `AuditRow` — admin, action badge, target link, reason, time.
- `AccountStatusBadge` — `ACTIVE` neutral, `FROZEN` red outline.
- `KYCBadge` — pending amber, approved green, rejected red.

## Admin shell

**Route:** `/admin/*`

**Purpose:** Frame for every admin screen. Holds the admin session and navigation.

**Components**
- Left sidebar, 240px, always visible from `md` up; collapses to a top drawer below.
  Links: Dashboard, Users, Transactions, KYC queue (with pending count badge), Audit log.
- Sidebar footer: admin username, role pill, "Change password", "Sign out".
- Top: page title, breadcrumb for detail pages ("Users / +254712…").
- `must_change_password` banner (see above).

**Data needed**
- `POST /api/v1/admin/login` → admin token (separate JWT scope `admin`, stored in memory
  only, with its own refresh cookie at path `/api/v1/admin`).
- `GET /api/v1/admin/kyc?status=pending` for the sidebar count, refetched every 60s.

**States**
- Unauthenticated → `/admin/login`. A **user** token never opens an admin route and an
  admin token never opens a user route: the middleware checks scope, not just validity.
- Session expired mid-action → redirect to login with `?next=`, and the in-progress form
  values are lost. Acceptable; admin actions are short.

**Visual notes**
- No bottom tab bar. The sidebar uses the same active-link treatment as the user app's
  desktop sidebar.

## Screen: Admin login

**Route:** `/admin/login`

**Purpose:** Authenticate an admin. Deliberately plain and unbranded relative to the user
login, so nobody mistakes it for the trading app.

**Components**
- Wordmark with "Admin" suffix. Username, password, "Sign in".

**Data needed**
- `POST /api/v1/admin/login` `{ username, password }` → `{ admin: { id, username, role,
  must_change_password }, access_token }` and the refresh cookie.

**States**
- Loading: button spinner. Error: "Username or password is incorrect", same message for
  both, same timing (bcrypt compare runs on a miss too).
- Rate limited (5 per minute per IP): "Too many attempts. Wait a minute."
- `APP_ENV != development` and `must_change_password` true → the backend returns
  `password_change_required` and the client lands on Change password with no way around
  it.

## Screen: Change password

**Route:** `/admin/change-password`

**Purpose:** Clear `must_change_password`. Forced in production, offered in dev.

**Components**
- Current password, new password (same live checklist as user register, plus "not the
  default"), confirm. "Change password" primary.

**Data needed**
- `POST /api/v1/admin/change-password` `{ current_password, new_password }`.

**States**
- Success → toast, banner disappears, redirect to `?next=` or Dashboard.
- Wrong current password → field error. New equals current or equals `admin123` →
  refused with a field error.

## Screen: Dashboard

**Route:** `/admin`

**Purpose:** Is anything wrong right now? Five numbers and one chart, refreshed every 30s.

**Components**
- Five stat tiles in one row: Users, Deposits today (count + USD), Withdrawals today
  (count + USD), Open positions, Live WS connections.
- A sixth tile that matters more than the others: **Ledger reconciliation** — the number
  of accounts where `SUM(transactions) ≠ wallet balance`. Green "0 accounts drifted" or a
  red count that links to the affected accounts. This is the observability gauge surfaced
  in the UI.
- "Deposits, last 24h" bar chart: success vs failed per hour. A Daraja outage is visible
  here before a user complains.
- Recent audit log, last 8 rows, "See all".

**Data needed**
- `GET /api/v1/admin/dashboard` → all six numbers plus the 24-bucket deposit series.
  One call; the dashboard must never fan out.
- `GET /api/v1/admin/audit-log?limit=8`.

**States**
- Loading: six skeleton tiles at final size.
- Error: `ErrorState` in place of the tile grid; audit list independent.
- Reconciliation nonzero: the tile turns red and pins to the top of the page above the
  others. This is the one state that should be impossible to miss.

## Screen: Users

**Route:** `/admin/users?search=&page=`

**Purpose:** Find a user.

**Components**
- `SearchInput` matching phone (normalised), email, name, or account number.
- `DataTable`: Name, Phone, KYC (`KYCBadge`), Accounts (two pills: `DEMO 900001`,
  `REAL 900002 · FROZEN` when applicable), Real balance, Joined. Row click → detail.
- Pagination footer.

**Data needed**
- `GET /api/v1/admin/users?search=&page=&limit=25`. Each row carries its accounts and
  wallets denormalised so the table is one call.

**States**
- Loading: 10 skeleton rows.
- Empty search: "No users match '0712…'".
- Empty system: "No users yet".
- Error: `ErrorState` with retry, search input stays usable.

**Visual notes**
- Frozen accounts show the `FROZEN` badge inline in the Accounts column so a support
  agent scanning the list sees it without opening the row.

## Screen: User detail

**Route:** `/admin/users/:id`

**Purpose:** Everything about one user, and every action that can be taken on them.
This is where money-moving admin actions live, so it is the most carefully gated screen.

**Components**
1. Header: name, phone, email, joined, `KYCBadge`. If a pending KYC submission exists,
   an inline "Review" link to the KYC queue filtered to this user.
2. **Accounts** — one card per account:
   - `AccountPill`, `AccountStatusBadge`, real and bonus balance, total.
   - `frozen_reason` shown in amber under the badge when frozen, with who froze it and
     when (from the audit log).
   - Actions row: **Freeze** (red outline) or **Unfreeze** (green outline), and
     **Adjust balance** (red outline). Both open `ReasonDialog`.
3. **Recent transactions** — last 25, same `TransactionRow` as the user app plus an
   "Admin" column showing the admin's username on `admin_adjustment` rows. "View all"
   → Transactions screen filtered to this user.
4. **Open positions** — read-only list. Admins cannot close positions in this round.
5. **Audit trail for this user** — every `admin_audit_log` row targeting this user or
   their accounts, newest first.

**Data needed**
- `GET /api/v1/admin/users/:id` → user, accounts with wallets, last 25 transactions,
  open trades, KYC submissions, audit rows for this user. One call.
- `POST /api/v1/admin/accounts/:id/freeze` `{ reason }`.
- `POST /api/v1/admin/accounts/:id/unfreeze` `{ reason }`.
- `POST /api/v1/admin/accounts/:id/adjust` `{ amount, balance_type, reason }`.

**Flows**
- **Freeze:** `ReasonDialog` titled "Freeze account 900002?", consequence "The user will
  not be able to trade, deposit, or withdraw from this account until it is unfrozen.
  Open positions stay open and can still be closed." Reason required. Confirm is red.
- **Unfreeze:** same dialog, green confirm, reason required (why it is safe again).
- **Adjust balance:** a two-step dialog. Step one: amount (signed; a leading minus
  debits), balance type (real / bonus radio), reason. Step two restates it: "Credit
  **$50.00** to **real** balance on **REAL 900002**, owned by **+254712…**. This writes a
  ledger row that cannot be undone; a mistake is corrected with a second adjustment."
  Confirm is red. A debit that would take the balance negative is refused by the
  server and the dialog shows the message under the amount field.

**States**
- Loading: header skeleton, two account card skeletons.
- Not found: "No user with that id" and a link back to Users.
- Action in flight: the dialog's confirm button spins; the card behind it dims.
- Action success: toast ("Account 900002 frozen"), the card's badge and the audit
  trail update from the response without a full refetch.
- Action failure: the dialog stays open with the error under the reason field; nothing
  on the page changes.

**Visual notes**
- The adjust dialog's confirmation step uses bold for the four facts (amount, balance
  type, account, owner) and nothing else. That is the sentence the admin is agreeing to.

## Screen: Transactions

**Route:** `/admin/transactions?search=&type=&status=&page=`

**Purpose:** Find a transaction: by reference, receipt, phone, account number, or id.
The first place support goes when a user says "my deposit didn't arrive".

**Components**
- `SearchInput`.
- `FilterChips` for type (All, Deposits, Withdrawals, Trades, Adjustments) and status
  (All, Pending, Completed, Failed).
- `DataTable`: Time, Type, Account (pill, links to user), Amount (signed, coloured),
  Status, Reference (truncated, copy on click), Admin (username on adjustments, else —).
  Row click → detail sheet (same `TransactionDetail` as the user app, plus the raw
  `metadata` JSON in a collapsible block, and the linked `mpesa_requests` row for
  deposits with `result_code` and `result_desc`).

**Data needed**
- `GET /api/v1/admin/transactions?search=&type=&status=&page=&limit=25`.

**States**
- Loading, empty ("No transactions match"), error as Users.
- **Stuck pending:** a deposit pending for more than 10 minutes gets an amber left border
  and a "Stuck?" tag. Clicking it shows the Daraja result if any arrived, so support can
  tell "user never entered PIN" from "callback never came".

## Screen: KYC queue

**Route:** `/admin/kyc?status=pending`

**Purpose:** Approve or reject identity submissions. In this round the submissions are
seeded placeholders; the screen is built so it is ready the moment the upload flow lands.

**Components**
- `FilterChips`: Pending (default), Approved, Rejected.
- `DataTable`: Submitted, User (name + phone, links to detail), ID number, Document
  (a link, or the literal placeholder text rendered in muted italics with a "no upload
  flow yet" tooltip so nobody clicks expecting a file), Status, Reviewed by / when.
- Row actions on pending rows: **Approve** (green outline), **Reject** (red outline).
  Approve confirms with a one-line dialog ("Approve +254712…? This enables withdrawals
  on their real account."). Reject opens `ReasonDialog`.

**Data needed**
- `GET /api/v1/admin/kyc?status=&page=&limit=`.
- `POST /api/v1/admin/kyc/:id/approve`. Sets `users.kyc_status = verified` in the same
  transaction as the submission update.
- `POST /api/v1/admin/kyc/:id/reject` `{ reason }`. Sets `users.kyc_status = rejected`.

**States**
- Empty pending queue: "Nothing waiting for review" with a green check. This is the good
  state and should look like it.
- Approve success: row moves out of the pending filter, toast, sidebar badge decrements.
- The user's app reflects it on next `GET /auth/me` (300s stale time) or immediately if
  they reload; the Withdraw button unlocks. **A `kyc` event on the user's account socket**
  is the cleaner path and is proposed in round-2-plan.md.

## Screen: Audit log

**Route:** `/admin/audit-log?page=`

**Purpose:** Who did what, to whom, why. Read-only, append-only, cannot be filtered
away: the full list is always one click from the sidebar.

**Components**
- `DataTable` of `AuditRow`: Time, Admin, Action (badge: freeze red, unfreeze green,
  adjustment red, kyc_approve green, kyc_reject red), Target (type + link), Reason
  (full text, wraps), Details (expandable metadata: for adjustments, amount and balance
  type and the resulting `transaction_id`).
- Optional filter by admin and by action, both as chips.

**Data needed**
- `GET /api/v1/admin/audit-log?page=&limit=50&admin_id=&action=`.

**States**
- Loading, empty ("No admin actions recorded yet"), error.

**Visual notes**
- No search box. The audit log is read top to bottom; search would invite the idea
  that some rows matter less than others.

## Cross-cutting (admin)

- **Route tree:** `/admin/login`, `/admin/change-password`, then everything else under
  the shell. Lives in `src/features/admin/` with its own `AdminShell`, `adminApi`
  client, `adminSession` store slice, and query keys prefixed `['admin', …]`.
- **Sessions are separate.** Logging into admin does not log into the user app and
  vice versa. Two stores, two cookies (different `Path`), two middlewares.
- **Every mutation invalidates the audit log query** in addition to its own resource,
  so the trail is never stale on screen.
- **Money is never formatted by hand.** Same `lib/format` as the user app.
- **Responsive:** sidebar collapses to a drawer below `md`; tables scroll horizontally
  inside their own container; the page body never scrolls sideways.
