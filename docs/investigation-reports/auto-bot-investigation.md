# Investigation: Automated Bot Trading ("AUTO" mode)

**Date:** 23 September 2026 · **Branch:** `digit-contracts-enabled-by-default` @ `4d425e0`
**Scope:** Read-only. No code was modified.
**For:** the planning session (Fable) that will scope AUTO mode, and the implementation session after it.

Baseline health at time of writing: `go build ./...`, `go vet ./...` and `npx tsc --noEmit` all clean.

---

## Read this first

Three findings change what the bot should be, before any design work starts.

**1. There is no automation scaffolding at all.** Not a stub, not a flag, not a dead export, not a
commented-out block, not a TODO. A case-sensitive search for `AUTO`, `MANUAL`, `TradingMode`,
`autoMode`, `martingale` across `web/ internal/ cmd/ pkg/ migrations/ scripts/ .github/` returns
exactly one hit, and it is unrelated: `scripts/sltp_check.py:124`, a test print. Every "bot" in the
codebase is the advisory EMA/RSI signal service or a Lucide icon. This is a greenfield feature.

**2. The payout table is explicitly not real.** `internal/models/digit_payout.go:9-14` says so in its
own header: *"Placeholder payout ratios for v1. NOT final for real-money use — replace once product
confirms the table."* Every number a bot's expected-value maths would rest on is provisional.

**3. Every contract type has negative expected value, and the edge is not uniform.** Computed
directly from `PayoutRatioFor` (see §3). It ranges from **-0.50%** to **-10.00%** of turnover per
contract. A bot is therefore a *convenience and discipline* feature, not a profit feature, and which
contract it picks matters by a factor of twenty.

---

## 1. Trade execution pipeline

### 1.1 The path, end to end

There are two parallel products. The **digit path is the active default**; the older CFD path is the
fallback. Both are live on the API at all times (see §1.4).

```
DigitTicket.tsx  fire()              :81-89     user clicks Over / Under (etc.)
  └─ DigitTradeScreen.tsx  submit()  :87-107    guards accountId + symbol
      └─ useOpenDigit()              useQueries.ts:159-169
          └─ digitApi.open()         api/digit.ts:36
              └─ POST /api/v1/digit-contracts
                  └─ routes.go:108   protected group, middleware.Auth + RateLimit(20/s, burst 60)
                      └─ DigitHandler.Open        digit_handler.go:96-129
                          └─ DigitService.Open    digit_service.go:66-186
                              └─ DigitContractRepo.Create  digit_contract_repo.go:48-62
```

**Who owns the click.** `OutcomeButton` (`web/src/features/trading/DigitTicket.tsx:203-242`) is
rendered twice by `DigitTicket` (`:147-164`); each calls `fire('primary')` or `fire('secondary')`.
`fire` re-checks `canSubmit` then invokes the `onSubmit` prop. The mutation itself lives in the
parent, `DigitTradeScreen.tsx:87-107` — **this `submit()` function is the exact seam an AUTO loop
would drive.**

**Client-side validation** (all in `DigitTicket.tsx`):

| Check | Line | Rule |
|---|---|---|
| Stake | `:53-60` | `> 0` and `<= real + bonus`. **No minimum beyond zero, no maximum.** |
| Quotes loaded | `:72-79` | `canSubmit` requires **both** `primaryQuote.data` and `secondaryQuote.data` |
| Submit re-guard | `:81-83` | `if (!canSubmit) return` |
| Feed liveness | `DigitTradeScreen.tsx:196-199` | `disabled={!tick \|\| stale}` |
| Symbol | `DigitTradeScreen.tsx:57` | filtered to `category === 'volatility'` |

Digit and barrier are never unselected: `digit` defaults `0`, `barrier` defaults `4`
(`DigitTicket.tsx:47-48`).

### 1.2 Request and response shapes

`POST /api/v1/digit-contracts`, `Authorization: Bearer <access>`. Request struct
`openDigitRequest`, `digit_handler.go:79-85`. **Stake is a string, not a number.**

```json
{ "account_id": "…uuid", "symbol": "R_75", "contract_type": "over_under",
  "selection": { "side": "over", "barrier": 4 }, "stake": "10" }
```
Selection per type: `{"parity":"even"}` · `{"mode":"match","digit":7}` · `{"side":"over","barrier":4}`.

**201 Created** returns the full `models.DigitContract` (`internal/models/digit_contract.go:53-76`);
all decimals marshal as **JSON strings**:

```json
{ "id":"…", "account_id":"…", "contract_type":"over_under",
  "selection":{"side":"over","barrier":4},
  "stake":"10", "payout_ratio":"0.95", "potential_payout":"19.5",
  "entry_price":"9449.94", "entry_digit":4, "entry_tick_epoch":1758600000,
  "status":"open", "settlement_price":null, "settlement_digit":null,
  "settled_at":null, "pnl":null,
  "opened_at":"…", "void_after":"…",
  "symbol_code":"R_75", "symbol_name":"Volatility 75 Index", "pip_size":2 }
```

Errors use one envelope (`internal/handlers/response.go:17-25`):
`{"error":{"message","code","field"}}`. Codes a bot must handle: `validation_failed` 422,
`insufficient_funds` 400, `no_price` **503**, `market_closed` 400, `account_frozen` 403,
`digit_not_open` 409, `rate_limited` 429.

### 1.3 Settlement — there is no duration parameter

**A contract settles on the first tick whose epoch is strictly greater than the entry tick.** There
is no duration, no tick-count, no expiry field anywhere in the digit path. On the mock feed
(500 ms ticks) that is roughly half a second.

Three settlement triggers, all funnelling into `DigitService.settle` (`digit_service.go:268-364`):

| Trigger | Line | Notes |
|---|---|---|
| Tick observer (primary) | `:200-222` | `s.feed.OnTick(s.onTick)`; settles every open contract on that symbol |
| Sweep (safety net) | `:224-248` | every `DigitSweepEvery = 5s` (`:23`), plus once at `Start` for crash recovery |
| Check-on-read | `:252-264` | `EnsureSettled`, called from `Get` and `List` — **GET requests can write** |

Win/lose is decided by `models.DigitWins` (`internal/models/digit.go:44-83`) against
`models.LastDigit(tick.Price, pipSize)` (`digit.go:19-31`). **The payout ratio is snapshotted onto the
contract row at open** and used verbatim at settlement, so changing the table cannot retro-price an
open contract.

**Void:** if no settlement tick arrives within `DigitVoidGrace = 5s` (`digit_service.go:19`), the
contract is voided and the stake refunded in full (`voidContract`, `:366-423`). A bot's accounting
must treat `status === "void"` as a third outcome, not a loss.

Double-settlement is prevented by `DigitContracts.GetOpenForUpdate`
(`digit_contract_repo.go:73-95`, `SELECT … WHERE id=$1 AND status='open' FOR UPDATE`), plus
`AND status='open'` on both the `Settle` and `Void` UPDATEs.

### 1.4 What handles it — no external broker

**This platform is its own broker.** There is no order routing to Deriv or anyone else. Contracts
are priced from a local static table, settled against the local tick buffer, and credited to a local
ledger. The only external dependency is the *price feed*, and even that defaults to a local
simulator (§2.1).

The CFD path (`trading_service.go`, `OrderTicket.tsx`, `POST /trades`) still exists in parallel with
its own table, ticket and settlement. The feature flag `digit_contracts_enabled` switches **only the
UI** — `POST /digit-contracts` and `POST /trades` are both registered unconditionally
(`routes.go:99-108`) and `DigitService.Open` never consults settings. **A bot hitting the API is not
gated by the flag.**

### 1.5 UI response handling

`useOpenDigit.onSuccess` (`useQueries.ts:159-169`) invalidates `digitContracts`, `wallet` and
`transactions` for the account. `DigitTradeScreen` then bumps `ticketKey` (`:99`), remounting
`DigitTicket` to reset stake, digit and barrier, and raises a toast (`:101-105`).

**Settlement does not come back from the mutation.** It arrives over the account WebSocket as
`digit_settled`, handled in `useAccountStream.ts:113-126`, with toasts in `AppShell.tsx:108-122`.

---

## 2. Market data and price feed

### 2.1 Backend: real and simulated, one flag

`pkg/derivclient/client.go` holds two implementations behind
`type Feed interface { Run(ctx); Ticks() <-chan Tick }` (`:212-215`):

- `Client` (`:25`) — real Deriv WebSocket, `wss://ws.derivws.com/websockets/v3`, subscribes with
  `{"ticks": s, "subscribe": 1}` (`:80`), reconnects with 1s→30s backoff.
- `MockClient` (`:128`) — random walk, **one tick per symbol every 500 ms** (`:182`).

Switched by `Config.UseMockFeed` (`internal/config/config.go:26`), **default `true`**
(`:94`, `getBool("USE_MOCK_FEED", true)`), wired at `cmd/api/main.go:77-81`.

Both drop ticks rather than queue when the consumer is behind (`client.go:118-122`).

### 2.2 The server-side tick buffer — the bot's data source

`internal/service/deriv_feed_service.go`. `FeedService` holds `latest map[string]models.Tick` and
`history map[string][]models.Tick`, capped at `tickBufferSize = 600` (`:37`) — about **five minutes**
at the mock rate. **Memory only; lost on restart; never persisted.**

Read API, all safe for a bot to call:

| Method | Line | Returns |
|---|---|---|
| `Price(code)` | `:182` | latest price |
| `Latest(code)` | `:192` | latest tick including `Epoch` |
| `History(code, limit)` | `:201` | recent ticks (a **copy**) |
| `Series(code, window)` | `:216` | windowed price slice — what the signal bot uses |
| `Prices()` | `:234` | snapshot of every symbol |

**The push hook**, `deriv_feed_service.go:84-88`:
```go
type TickObserver func(tick models.Tick)   // :41
func (s *FeedService) OnTick(o TickObserver)
```
Two things to know: observers run **synchronously on the single feed goroutine**
(`handle`, `:133-142`), so a slow observer stalls price delivery for every connected user; and
there is **no unregister** — a start/stop bot needs an internal enabled flag, not registration churn.

### 2.3 Chart history on first render

HTTP, not the socket: `GET /api/v1/symbols/:code/ticks?minutes=5` (`routes.go:96` →
`MarketHandler.Ticks`, `market_handler.go:55-69`). `minutes` is clamped 1–30, but it serves the same
600-tick memory buffer, so it can never return more than ~5 minutes of data regardless.

### 2.4 Client-side buffer

`web/src/store/market.ts` — Zustand, `BUFFER = 600` (`:6`), **no persist middleware**. A client-side
bot can read it directly without React:

```ts
useMarketStore.getState().history['R_75']   // Tick[]
useMarketStore.getState().prices            // Record<string, Tick>
useMarketStore.getState().watch(['R_75'])   // force a symbol to stream
useMarketStore.subscribe(...)               // vanilla Zustand subscription
```

`web/src/lib/lastDigit.ts` is the TypeScript port of `models.LastDigit`, and its header warns the two
"must match … exactly, or the digit row and settlement disagree". Its only production consumer today
is `DigitDistributionRow.tsx:18-23`, which already computes a 0–9 frequency table over the whole
buffer — **that is the reference implementation of a digit-frequency read for a strategy.**

### 2.5 Is there an exploitable pattern in the feed? No.

I sampled 200,000 ticks through a replica of the mock generator and the server's rounding, then
applied `LastDigit`:

| Symbol (pip size) | χ² (9 df) | min digit | max digit | Verdict |
|---|---:|---:|---:|---|
| R_10 (3) | 21.8 | 9.84% | 10.20% | uniform |
| R_75 (4) | 8.9 | 9.89% | 10.12% | uniform |
| R_100 (2) | 16.6 | 9.86% | 10.18% | uniform |
| EURUSD (5) | 4.3 | 9.90% | 10.06% | uniform |

(Critical value at p=0.001 is 27.88.) Digits are uniform and ticks are independent, so **no strategy
reading digit history can produce an edge on this feed.** This is good news for test fidelity — dev
will not show a phantom edge — but it means a "smart" strategy cannot beat §3's house edge.

---

## 3. Contract types and payout logic

All three live in `internal/models/digit_payout.go` (66 lines). `PayoutRatioFor` is both the pricing
function **and** the selection validator — `validateDigitSelection` (`digit_service.go:188-198`)
delegates to it. Semantics: **win credit = stake × (1 + ratio)**; a loss forfeits the stake.

| Type | Identifier | Required params | Ratio source |
|---|---|---|---|
| Matches / Differs | `matches_differs` | `mode` ∈ match\|differ, `digit` 0–9 | static: `8.0000` / `0.1000` (`:18-19`) |
| Even / Odd | `even_odd` | `parity` ∈ even\|odd | static: `0.9520` (`:17`) |
| Over / Under | `over_under` | `side` ∈ over\|under, `barrier` 0–9 | **computed** (`:49-62`) |

Over/Under formula: `winCount = 9-barrier` (over) or `barrier` (under); `ratio = ((10/winCount) - 1)
× 0.95`, rounded to 4dp. `winCount <= 0` errors — so **Over 9 and Under 0 are rejected**.

There is **no duration, no stake minimum beyond zero, and no stake maximum** anywhere.

### 3.1 House edge — computed, and it is not what the code comment says

Expected value per unit staked is `p × (1 + ratio) − 1`. Computed from `PayoutRatioFor` directly:

| Contract | Win prob | Ratio | Edge (per trade) |
|---|---:|---:|---:|
| **Over 0 / Under 9** | 0.9 | 0.1056 | **−0.50%** ← least bad |
| Differs (any digit) | 0.9 | 0.1000 | −1.00% |
| Over 1 / Under 8 | 0.8 | 0.2375 | −1.00% |
| Over 2 / Under 7 | 0.7 | 0.4071 | −1.50% |
| Over 3 / Under 6 | 0.6 | 0.6333 | −2.00% |
| **Even / Odd** | 0.5 | 0.9520 | **−2.40%** |
| Over 4 / Under 5 | 0.5 | 0.9500 | −2.50% |
| Over 5 / Under 4 | 0.4 | 1.4250 | −3.00% |
| Over 6 / Under 3 | 0.3 | 2.2167 | −3.50% |
| Over 7 / Under 2 | 0.2 | 3.8000 | −4.00% |
| Over 8 / Under 1 | 0.1 | 8.5500 | −4.50% |
| **Matches (any digit)** | 0.1 | 8.0000 | **−10.00%** ← worst |

**The code comment at `digit_payout.go:55` says "house edge ~5%". That is not what the formula
produces.** The 0.95 factor is applied to the *profit*, not the payout, so the realised edge is
`5% × (1 − p)` and varies from 0.50% to 4.50% across barriers. Worth correcting in the code, and
worth knowing before a bot is pointed at a barrier.

Practical consequence: **the contract a bot selects matters more than any strategy it runs.** A bot
looping Matches burns twenty times more capital per trade than one looping Over 0.

---

## 4. State and session management

### 4.1 Where state lives

| Concern | Where | Persisted? |
|---|---|---|
| Access token | `store/session.ts` (Zustand) | **No** — memory only, by design |
| Refresh token | httpOnly cookie | browser-managed |
| `user`, `activeAccountId` | `store/session.ts` `partialize` (`:49-54`) | localStorage |
| Prices, tick history, connection status | `store/market.ts` | **No persist middleware** |
| Balance, contracts, transactions | React Query | server, cached |

### 4.2 Server state and refresh cadence

`web/src/hooks/useQueries.ts`, keys in `web/src/lib/queryKeys.ts`:

| Hook | staleTime | refetchInterval |
|---|---|---|
| `useMe` | 10s | **10s** |
| `useWallet` | 10s | none (`refetchOnWindowFocus: true`) |
| `useOpenDigits(id)` | default 30s | **5s** while `status === 'open'` |
| `useOpenDigits(id,'settled')` | default | none |
| `useTrades(id,'open')` | default | 15s |
| `useDigitQuote` | 60s | none; `placeholderData: prev` |

Global defaults `App.tsx:17-30`: `staleTime 30_000`, `refetchOnWindowFocus: false`, and a retry
predicate that **never retries 4xx** — so a 429 is not retried automatically. A bot must back off itself.

### 4.3 Balance liveness

Three mechanisms: a WebSocket push that writes the cache **directly**
(`useAccountStream.ts:81-91`, recomputing `total` because the server omits it), a
`refetchOnWindowFocus` safety net, and invalidation on every trade mutation. Server-side, `wallet`
events are broadcast immediately after the transaction commits (`digit_service.go:183`).

### 4.4 Is there a trading session already? Barely, and it will not survive a bot

Yes in name, no in substance. `web/src/features/trading/sessionStats.ts:9`:

```ts
export const SESSION_STARTED_AT = Date.now()
```

A **module constant**. Not persisted, no server row, no query key. **A page reload restarts the
count.** `digitSessionStats` (`:59-88`) derives wins/losses/voids/realised from the settled contracts
already fetched, and `SessionStrip.tsx` renders them.

Two hard limits a bot will hit immediately:

1. The settled list is **one page of 50** (`useQueries.ts:131`). The file says so itself (`:19-25`):
   *"deliberately approximate: the closed list is one page, so a session that closed more trades than
   that page holds is undercounted."* A bot doing a few hundred contracts will silently under-report
   its own P/L.
2. There is **no start/stop, no run identity, no durable totals, no stop-loss or take-profit for a
   session**. All of that is new.

Server aggregates that do exist: `GET /trades/summary` and `GET /digit-contracts/summary`
(`repository.DigitSummary` — counts plus `session_pnl` over the **last 24 hours**, not a session).

---

## 5. UI trading panel structure

### 5.1 Component tree

```
App (App.tsx)                              QueryClient, Router, Toaster
└── AppShell                               owns account ctx + all three sockets
    │   usePriceFeed() :75 · useSignalFeed() :75 · useAccountStream() :87
    ├── TopBar                             account pill, panel row (desktop)
    ├── Panel  ← one at a time, mounted once, driven by ?panel=
    │   ├── PositionsPanel ─┬ DigitPositionsPanel   (no close action)
    │   │                   └ CfdPositionsPanel
    │   ├── HistoryPanel                   digit / CFD fork
    │   ├── SignalsPanel                   hidden when digit flag on
    │   └── AccountPanel
    ├── <main><Outlet/></main>
    │   └── MainSurface                    forks on digit_contracts_enabled
    │       └── DigitTradeScreen           ◀ ALL TRADING STATE LIVES HERE
    │           │  range :49 · contractType :50 · ticketKey :51 · waitedForPrice :52
    │           │  ?symbol= :54 (default 'R_75' :28)
    │           ├── SymbolPicker           favourites in localStorage
    │           ├── ContractTypeTabs       controlled, stateless
    │           ├── PriceChartLazy → PriceChart
    │           ├── DigitDistributionRow   pure; 0–9 frequency over the buffer
    │           ├── SessionStrip           pure
    │           └── DigitTicket key={ticketKey}    ◀ THE ORDER TICKET
    │                 state: stake='10' :46 · digit=0 :47 · barrier=4 :48
    │                 ├── DigitPicker      (Digit for matches, Barrier for over/under)
    │                 └── OutcomeButton ×2 label + payout from the server quote
    ├── TabBar · NavDrawer · AccountSwitcher · DepositSheet · WithdrawSheet
```

Note `DigitTicket` does **not** own `contractType` — the tabs live in the parent, and there is no
`onContractTypeChange` prop. Payouts are **not computed client-side**: two parallel
`useDigitQuote` calls hit `GET /digit-contracts/quote` (`DigitTicket.tsx:69-70`).

### 5.2 Is there a Trading Mode toggle anywhere?

**No.** Confirmed by independent search. But the *intended* design is already documented from the
benchmark, which is unusually useful prior art:

- `docs/investigation-reports/nav-layout-investigation.md:125` — the benchmark's mobile trade screen
  places AUTO/MANUAL directly under the digit row, above the stake controls.
- `:221` — on desktop, an `✦ AI` pill sits beside a **"TRADING MODE" panel whose two options are
  AUTO and MANUAL**.
- `:166` — the report concludes the benchmark's `AI` nav item "is almost certainly the benchmark's
  auto-trading feature".
- `docs/investigation-reports/digit-contracts-investigation.md:177` — pre-decides that trading mode
  should be **global, not per-user**, with a user-ID allowlist as the staged-rollout extension.

### 5.3 Where a Bot panel would slot in

Four registries, two of which are total `Record` types and will fail the build until updated:

| # | File : line | What to add |
|---|---|---|
| 1 | `web/src/features/shell/usePanel.ts:12` | `'bot'` in the `PANELS` tuple |
| 2 | `web/src/features/shell/panelItems.ts:12-17` | `bot: { label, icon }` — **total Record, compile error until done** |
| 3 | `web/src/features/shell/AppShell.tsx:33-38` | `bot: <BotPanel />` — **total Record, same** |
| 4 | `web/src/features/shell/panelItems.ts:24-27` | whether `chromePanels()` shows it in CFD mode |

Both nav surfaces then pick it up for free (`TabBar.tsx:20`, `TopBar.tsx:39-42`), and the panel title
comes from `PANEL_ITEMS`.

Alternatively a bot **control sheet** could be an `Overlay` rather than a panel — one line in the
union at `features/shell/overlays.ts:11`, one facade method, one mount in `AppShell`. Cheaper if the
UI is modal rather than a persistent column.

**Icon collision:** `panelItems.ts:15` already uses Lucide's `Bot` icon for the *Signals* panel. A
real bot panel needs a different icon, and `nav-layout-investigation.md:221` warns specifically
against naming an advisory feature in a way that "would promise automation the product does not do".

---

## 6. Auth and balance

### 6.1 Tokens

`web/src/api/client.ts`: access token from the store into an `Authorization` header (`:94-107`),
every request `credentials: 'include'`. Refresh is a bodyless `POST /auth/refresh` relying on the
httpOnly cookie (`:54`), deduped by a module-level single-flight (`:46, 118-121`), with **exactly one**
silent retry per 401. `bootstrapSession()` (`:78`) runs before the router.

Access token TTL is **15 minutes** (`config.go:89`). A long-running bot must handle refresh; going
through `api.get`/`api.post` gets it free. A server-side bot would need its own token, or to call the
service layer directly and skip HTTP entirely.

### 6.2 Rate limiting — the headline risk

`internal/middleware/ratelimit.go`, keyed **per user** on authenticated routes (`:35-38`), not per IP.

| Group | routes.go | Limit |
|---|---|---|
| `/auth/*` | `:66` | 1 per 3s, burst 10 |
| **`protected` (everything authenticated)** | `:78` | **20 req/s, burst 60** |
| `money` (deposit, withdraw) | `:91` | 1 per 2s, burst 5 |
| admin actions | `:140` | 1/s, burst 10 |

**`POST /digit-contracts` (`:108`) and `POST /trades` (`:101`) sit in `protected`, not in `money`.**
Deposits and withdrawals get a dedicated tight bucket; **placing contracts, which also moves money,
does not.** A bot can legitimately fire **20 contracts per second, burst 60**, sharing that budget
with the UI's own polling.

Verified empirically: 14 sequential registrations against `/auth` returned `201 ×10` then `429 ×4`,
so the limiter works — it is the *placement* budget that is loose.

### 6.3 Guards that exist, and the one that does not

**Server-side balance checking is sound.** `DigitService.Open` (`digit_service.go:129-174`) locks the
wallet row and the account row inside one transaction before debiting:

```go
w, err := st.Wallets.GetForUpdate(ctx, account.ID)     // SELECT … FOR UPDATE
locked, err := st.Accounts.GetForUpdate(ctx, account.ID)
if locked.IsFrozen() { return ErrAccountFrozen }
balanceType := pickBalance(w, in.Stake)
if balanceType == "" { return ErrInsufficientFunds }
```

`WalletRepo.Adjust` (`wallet_repo.go:49-67`) additionally refuses to go negative in SQL
(`WHERE … + $2 >= 0`). The ledger row and the balance change commit together. A naive bot **cannot
overdraw**. Concurrency coverage already exists in `internal/service/concurrency_test.go`.

**What is missing entirely:**

- No maximum stake — only `> 0` (`digit_service.go:77-79`). A bot can stake the whole balance.
- No cap on concurrent open contracts.
- No cooldown or minimum interval between placements.
- No idempotency key or nonce on `POST /digit-contracts` — two identical concurrent posts create two
  contracts.
- No large-stake confirmation on the digit surface. The CFD screen has one
  (`TradeScreen.tsx:43,118-137`, `CONFIRM_THRESHOLD = 0.2`); `DigitTradeScreen.submit` calls
  `mutate` directly.
- `pickBalance` (`trading_service.go:465-475`) does **not split** across bonus and real: a stake
  larger than either single balance is refused even when the sum would cover it.

**Client-side double-submit guards** are UI-only. `canSubmit` includes `!submitting`
(`DigitTicket.tsx:72-79`) and `Button` couples `loading` to `disabled` (`components/ui/Button.tsx:61`)
— but a bot calling `openDigit.mutate(...)` or `digitApi.open(...)` bypasses all of it. There is **no
debounce or throttle on trade placement anywhere.**

---

## 7. Existing automation hooks

**None.** Verified independently and by exhaustive search.

- No AUTO/MANUAL toggle, in any state, anywhere in code.
- No bot service, stub, dead export, or commented-out block.
- **No `TODO`/`FIXME`/`XXX`/`HACK` in the repository at all.**
- No bot table, column, or migration. `digit_contracts` has **no provenance column** — nothing like
  `source`, `opened_by` or `bot_run_id`. A bot's contracts would be indistinguishable from a human's.
- No scheduling dependency in `go.mod` or `web/package.json` (no cron, asynq, machinery, river,
  gocron, bull). Every loop is a hand-rolled `time.NewTicker` + `select`.
- No scheduled CI workflow — `.github/workflows/ci.yml` triggers on push and PR only.

### 7.1 What *does* exist and is directly reusable

**A generic feature-flag store.** `internal/repository/settings_repo.go` is a plain key/value table
(`app_settings`, migration `0004`) with `Get`/`Set`; its own doc mentions "a future admin button".
Adding `bot_enabled` needs **no migration** — one new `const` plus an accessor in
`settings_service.go` (which today is hardcoded to the single digit flag, `:23-35`).

**Two proven loop idioms to copy:**

1. **Ticker-driven pull** — `SignalService.Run` (`internal/service/signal_service.go:46-69`):
   a 45s warmup, `time.NewTicker(cfg.SignalInterval)`, one immediate pass, then
   `select { case <-ctx.Done(): return; case <-t.C: … }`. Started as a bare `go signals.Run(ctx)` at
   `cmd/api/main.go:103`. It pulls data with `s.feed.Series(code, 10*time.Minute)` (`:89`).
   **An AUTO bot would slot in at exactly that line.**
2. **Tick-push plus safety-net sweep** — `DigitService.Start` (`digit_service.go:39-54`):
   `OnTick` for the primary path, an immediate sweep for crash recovery, then a 5s ticker.

**The signal bot is the closest precedent but is not a trading bot.** It inserts a row and
broadcasts; there is no code path from `SignalService` into `TradingService.Open` or
`DigitService.Open`. It is advisory only, which `docs/investigation-reports/ui-polish-investigation.md:217`
states explicitly.

### 7.2 Prior art in docs

Five reports defer automation; **none scopes it.**
`ui-polish-investigation.md:216` is the explicit hand-off: automated trading "is on record as a
separate future initiative with its own investigation" — this document.

Most useful to read next: `nav-layout-investigation.md` §125/§166/§221/§279 for the benchmark's AUTO
UI, and `digit-contracts-investigation.md` §177 for the global-vs-per-user decision.

---

## 8. Gaps and open questions

### 8.1 Does not exist — must be built

| Area | What is missing |
|---|---|
| Strategy engine | No strategy abstraction, no rule/condition model, no backtest. `signals.strategy` is a label string holding `'ema_rsi'`, not a definition. |
| Execution loop | No bot loop. Two idioms exist to copy (§7.1) but nothing wired. |
| Stop conditions | No stop-loss, take-profit, max-consecutive-losses, max-trades, or time limit for a *session*. CFD SL/TP is per-position and does not apply to digit contracts at all. |
| Session identity | `SESSION_STARTED_AT` is a module constant that resets on reload. No run id, no start/stop, no durable totals. |
| Persistence of bot runs | No table, no columns, no provenance on `digit_contracts`. |
| Bot UI | No panel, no overlay, no toggle. Four registries to touch (§5.3). |
| Bot-specific limits | No stake ceiling, no exposure cap, no placement cooldown, no idempotency. |
| Kill switch | No way to stop a running bot other than killing the process. |
| Observability | `internal/observability/metrics.go` has no bot metrics; the ledger-drift gauge exists and would catch accounting bugs, but nothing would show a bot running away. |
| Digit e2e coverage | CI pins `DIGIT_CONTRACTS_ENABLED: "false"` (`ci.yml:162-165`), so **no browser test touches the digit surface at all**. |

### 8.2 Architectural constraints that make a naive loop dangerous

1. **20 contracts/second is permitted** (§6.2). Nothing in the stack objects. At a $10 stake that is
   $200/second of turnover, losing $1–$20/second at the edges in §3.1. **This is the single most
   dangerous gap.**
2. **No stake ceiling and no exposure cap** — a bug in stake sizing can stake the entire balance in
   one contract, or open unbounded concurrent contracts.
3. **A server-side bot registered via `OnTick` would run on the feed goroutine**
   (`deriv_feed_service.go:133-142`) and stall price delivery for every connected user. It must hand
   off to its own goroutine or queue. `OnTick` also has **no unregister**, so start/stop must be an
   internal flag.
4. **Settlement already does per-tick DB work** (`digit_service.go:200-222` does a
   `ListOpenBySymbol` plus one transaction per open contract, on every tick). A bot that multiplies
   open contracts multiplies that load on the hot path.
5. **Session P/L silently under-counts past 50 settled contracts** (§4.4) — a bot's own displayed
   results would be wrong, which is worse than showing nothing.
6. **Void is a third outcome.** A stalled feed refunds rather than settles (`DigitVoidGrace = 5s`).
   Any win/loss streak logic that treats void as a loss will misfire during feed hiccups.
7. **No idempotency.** A retry after a timeout can double-place.
8. **The flag does not gate the API** (§1.4) — turning digit contracts "off" would not stop a bot.
9. **`refetchInterval` pauses when the tab is unfocused** unless `refetchIntervalInBackground` is set,
   and it is not — flagged in `post-migration-fixes-investigation.md:78`. A *client-side* bot in a
   background tab would go blind.

### 8.3 Open questions for product, not engineering

- **The payout table is placeholder** (`digit_payout.go:9-14`; `digit-contracts-phase-0-1.md:122`:
  "Do not use with real money until product replaces these numbers"). Should a bot ship against
  provisional economics at all?
- **Every contract has negative EV** (§3.1). What is the bot's honest value proposition, and how is
  that communicated in the UI without implying profit?
- **Which contract types may a bot use?** Restricting it to the −0.50% band (Over 0 / Under 9) versus
  allowing Matches at −10% is a twenty-fold difference in burn rate.
- **Demo only, or real money?** Demo-first would de-risk everything in §8.2.
- **Global or per-account?** `digit-contracts-investigation.md:177` already argues global with an
  allowlist.

---

## Recommendations for the bot implementation plan

Five things the planning session must account for.

**1. Decide the economics question before the architecture.** Every contract type has negative
expected value, from −0.50% to −10.00% per trade, and the payout table is explicitly marked not
final. A bot cannot be sold as a way to make money on these numbers. Planning should settle what it
*is* for — consistency, speed, hands-off session limits — and whether it ships demo-only until
product replaces the table. This is a product decision that changes the UI copy, the risk controls,
and arguably whether the feature ships at all.

**2. Rate limiting and exposure caps are the critical safety gap, and they are not there.**
`POST /digit-contracts` sits on the generic 20/s bucket rather than the tight `money` bucket that
guards deposits, and there is no stake ceiling, no concurrent-contract cap, no cooldown and no
idempotency key. Any plan must add server-side limits *before* a loop exists, not after — and they
belong on the server, because the client guards are trivially bypassed by the bot itself.

**3. A trading session must be built from scratch, server-side.** What exists is a module constant
that resets on reload and a P/L figure derived from one 50-row page. A bot needs a durable run with
an id, start/stop, running totals, stop-loss and take-profit for the session, and a kill switch.
That means a new table and a provenance column on `digit_contracts`, which today cannot distinguish
a bot's contract from a human's.

**4. Put the loop server-side, modelled on `SignalService.Run`, and keep it off the feed goroutine.**
The precedent is `internal/service/signal_service.go:46-69` started at `cmd/api/main.go:103`. If the
bot instead hooks `FeedService.OnTick`, it must hand off immediately: observers run synchronously on
the single feed goroutine, so blocking there stalls prices for every user. `OnTick` has no
unregister, so start/stop is an internal flag. A client-side loop is the wrong choice — it dies on
tab blur, on reload, and on token expiry.

**5. The UI seams are clean and the benchmark design is already documented.** The order ticket's
submit path is one function (`DigitTradeScreen.tsx:87-107`), a Bot panel needs entries in exactly
four registries (two of which fail the build until updated), and
`nav-layout-investigation.md` §125/§221 already describes where AUTO/MANUAL sits in the reference
product. Budget the UI as small. Budget the risk controls, the session model and the testing as
large — and note that CI currently runs no browser test against the digit surface at all, so digit
e2e coverage is a prerequisite, not a follow-up.
