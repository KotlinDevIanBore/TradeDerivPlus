# AUTO Mode — Implementation Plan

**Date:** 23 September 2026 · **Branch:** `digit-contracts-enabled-by-default` @ `4d425e0`
**Basis:** `auto-bot-investigation.md` (read-only survey, citations verified). Nothing here
re-investigates what that report answers.
**For:** the implementation session. Decisions in §11 are taken; reopen them deliberately, not
by accident.

---

## 1. Summary

### Plain language

We are building a bot that places one digit contract after another on the user's behalf, at a
fixed stake and a fixed interval, until a stop condition it was given at the start is met — or
until the user, an admin, or a safety limit stops it. It runs on the server, so it survives a
closed tab, a reload and a token expiry. It cannot make money: every contract on this platform
has negative expected value, the payout table is provisional, and the price feed's digits are
uniform (`auto-bot-investigation.md` §3.1, §2.5). **What it sells is discipline** — a stake
you set once, stop-loss and take-profit you commit to before the first contract, and a session
that ends when it should. The UI says exactly that and shows the expected return per contract
next to the Start button.

### The honest outcome of planning

Two things follow from the economics and should be said plainly rather than designed around:

1. **v1 is demo-only by flag.** Real-money runs are gated behind `bot_real_money_enabled`
   (default `false`) so the flip is product's decision, not a code change. Until product
   replaces the payout table (`internal/models/digit_payout.go:9-14`), that flag should stay
   off.
2. **The feature must not be marketed as "AI" or as profit-generating.** The benchmark's
   `✦ AI` pill is a naming choice we should not copy (`nav-layout-investigation.md:221`
   already warns against promising automation the product does not do — the reverse, promising
   intelligence the bot does not have, is worse). The label is **AUTO**, the copy is "runs your
   settings for you".

### What is built

- **Server-side risk controls that apply to humans too**, landed before any bot code exists:
  a maximum stake, a cap on open contracts per account, and a dedicated rate bucket for contract
  placement (today it shares the generic 20 req/s bucket, `routes.go:78,108`).
- **A durable run** (`bot_runs` table) with a provenance column on `digit_contracts`, totals
  that are maintained inside the existing settlement transaction so they cannot drift, a
  `stopping` state for the one contract in flight, and a global kill switch.
- **`BotService`**, one goroutine per run, sequential (one contract in flight), never touching
  the feed goroutine, calling `DigitService.Open` and nothing else for money.
- **A MANUAL / AUTO toggle inside the trading surface** where the benchmark puts it, swapping
  the order ticket for a bot ticket; a run card while running; a "BOT" chip on every contract the
  bot placed.
- **Digit e2e coverage in CI** as Phase 0, because CI currently runs no browser test against
  the digit surface at all (`.github/workflows/ci.yml:162-165`).

### Cost

Five phases, each shippable. **High overall**, weighted towards the backend: Phases 0–1 are
roughly two-thirds of the work and produce no user-visible bot. That is deliberate — the brief's
first non-negotiable is that risk controls land before the loop exists.

### Biggest risk

Not the loop; the **settlement hot path**. Every bot contract is settled by `DigitService.onTick`
on the single feed goroutine, one transaction per open contract per tick
(`digit_service.go:200-222`). The bot itself never runs there, but its contracts are settled
there. The global run cap (`BOT_MAX_RUNS`) is what bounds that load and it is not optional.

---

## 2. Target architecture

### 2.1 Where the loop lives — server-side, not on `OnTick`

**Decision:** `internal/service/bot_service.go`, one goroutine per run, modelled on the
ticker-and-`select` shape of `SignalService.Run` (`signal_service.go:46-69`) but **not** on
`FeedService.OnTick`. The bot does not need every tick; it needs "place, wait for settlement,
evaluate, sleep, repeat". Polling `DigitService.Get` for settlement (§2.3) keeps the bot
entirely off the feed goroutine and needs no unregister — the two hazards the investigation
flagged (`deriv_feed_service.go:84-88, 133-142`).

Why not client-side: a browser loop dies on tab blur (`refetchInterval` pauses in background
tabs, `post-migration-fixes-investigation.md:78`), on reload, and on the 15-minute access token
(`config.go:89`). A bot that stops when you lock your phone is not a bot.

Wiring in `cmd/api/main.go`: construct after `digits` (`:91`), start **after** `digits.Start(ctx)`
(`:106`) so the boot sweep has run, and add a shutdown hook beside `srv.Shutdown`
(`:146-150`) — today nothing waits for service goroutines on SIGTERM (§2.6).

### 2.2 Services and dependencies

```
BotService
  store        *repository.Store        (BotRuns, DigitContracts, Accounts, Wallets)
  digits       *DigitService            Open, Get       ← the only money path
  feed         *FeedService             Latest (cold-start check only)
  settings     *SettingsService         BotEnabled, BotAllowlist, BotRealMoneyEnabled
  hub          Broadcaster              bot_run events on UserTopic(userID)
  cfg          BotLimits                MinIntervalMS, MaxRuns, MaxStakeFraction
  mu           sync.Mutex
  runs         map[uuid.UUID]*liveRun   {cancel, stop chan, userID}
  wg           sync.WaitGroup
  pollEvery    time.Duration            250ms; tests set 10ms
```

`DigitService` gains two things and nothing else: a `DigitLimits{MaxStake, MaxOpenPerAccount}`
constructor parameter (`NewDigitService`, `digit_service.go:34` — update
`integration_setup_test.go:139` too), and, inside the existing `settle`/`voidContract`
transactions, one `BotRuns.ApplySettlement` call when the locked contract carries a
`bot_run_id` (§2.4). It already couples to `Transactions` the same way; this is not a second
money path. There is **no `OpenForBot`**.

### 2.3 The run loop

```
for {
    if reason := preCheck(run); reason != "" { finalise(reason); return }
    c, err := digits.Open(openCtx, OpenDigitInput{..., BotRunID: &run.ID})
    switch {
    case errors.Is(err, ErrInsufficientFunds), ErrAccountFrozen, ErrMarketClosed:
        finalise(mapped); return
    case errors.Is(err, ErrNoPrice) && coldStartRetries < 5:
        coldStartRetries++; sleep(interval); continue
    case err != nil:
        finalise("error", err); return          // never retry Open on an unknown error
    }
    settled := awaitSettlement(c.ID)            // poll digits.Get every pollEvery, deadline DigitVoidGrace+2s
    run = store.BotRuns.Get(run.ID)             // counters were bumped inside the settle tx
    if reason := postCheck(run, settled); reason != "" { finalise(reason); return }
    sleep(interval)                              // interruptible by stop and root ctx
}
```

Rules the reviewer's pass established, each binding:

- **`openCtx` is not the run's cancellable context.** Cancelling mid-`WithTx` is money-safe
  (pgx aborts the tx) but a cancel landing between `tx.Commit` (`store.go:70`) and `Open`
  returning leaves a committed contract the loop never saw. `Open` gets
  `context.WithTimeout(rootCtx, 10s)`; user-stop is a flag consulted before placement and after
  settlement, which is what the `stopping` state is for.
- **Never retry `Open` on a non-sentinel error.** The ambiguous-commit case above is the only
  way to double-place, and "don't retry" removes it. Belt and braces: before each placement,
  `DigitContracts.CountOpenByRun(runID)`; if `> 0`, await that contract instead of placing.
- **`awaitSettlement` treats `ErrDigitNotOpen` from `Get` as "re-read", not failure.**
  `FeedService.handle` updates `latest` (`deriv_feed_service.go:124-131`) *before* running
  observers (`:140-142`), so a poll in that window sees the newer tick, enters `settle`, loses
  the row lock to `onTick`, and `Get` returns `ErrDigitNotOpen` (`digit_service.go:286-290`,
  `:440`). `List` already swallows this (`:488-490`); `Get` does not.
- **Settlement takes 0.5–1.0 s on the mock feed, not ~500 ms.** The mock stamps
  `Epoch: time.Now().Unix()` (`pkg/derivclient/client.go:190,203`) on a 500 ms ticker (`:182`),
  so consecutive ticks share an epoch and "strictly greater" waits for the next second. Live
  Deriv is 1–2 s. The cadence ceiling is therefore ~1 contract/s regardless of `interval`.
- **Polling, not a settlement callback, for v1.** 50 runs × 4 polls/s = 200 PK reads/s
  against a 20-connection pool (`db.go:38`) — negligible, and the poll's `EnsureSettled`
  *offloads* the feed goroutine rather than loading it. An `OnSettled` hook (precedent:
  `WalletService.OnDepositSettled`, `wallet_service.go:32`) would still need the poll as a
  fallback for lost wakeups. Revisit at a few hundred runs.
- **Panics must be recovered per goroutine.** `middleware.Recovery` wraps HTTP only
  (`routes.go:45`); a panic in a service goroutine kills the process.
  `defer func(){ if r := recover(); r != nil { finalise("error") } }()` on every run.
- **`sync.WaitGroup` plus the cancel map**, with the goroutine deleting its own map entry on
  exit, so `StopAll` and shutdown can wait and no finished run leaves a stale entry.

### 2.4 Totals live in the settlement transaction

**Decision:** `bot_runs` counters (`contracts_placed`, `wins`, `losses`, `voids`,
`consecutive_losses`, `consecutive_voids`, `realised_pnl`) are written **inside** the
transactions that already commit the money: `contracts_placed++` in `Open`'s `WithTx`
(`digit_service.go:129-174`), the outcome columns in `settle` (`:285-357`) and `voidContract`
(`:369-410`). The loop never writes totals; it reads the row after each settlement.

Why: the alternative — the loop maintaining counters, and a boot-time aggregate finalising runs
after a restart — has a race. `digits.Start` runs its sweep synchronously (`:42`) before
`bots.Start` would run, but at that instant the feed has no ticks (`go feed.Run` at
`main.go:104` has barely started), so `feed.Latest` reports `ok=false`
(`deriv_feed_service.go:192-197`) and the sweep can only void. A contract opened <5 s before the
crash is still open after the boot sweep and settles on a later tick; an aggregate taken at boot
misses it. With counters in the settle tx, a straggler settled later still updates its (already
stopped) run, and `bots.Start` becomes one statement (§4.3).

### 2.5 Schema

Migration `migrations/0006_bot_runs.up.sql`. Only `*.up.sql` is applied and files are sorted
lexically (`db.go:74-79`); `0002`, `0004`, `0005` have no down file, so none is needed here.

```sql
-- Automated runs. A run is one durable AUTO session on one account; the contracts it
-- places point back via digit_contracts.bot_run_id. The totals on this row are maintained
-- inside the contract open/settle transactions, so they cannot drift from the contracts
-- they summarise.

CREATE TABLE IF NOT EXISTS bot_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id),
    symbol_id UUID NOT NULL REFERENCES symbols(id),
    -- running | stopping | stopped. stopping = stop requested, one contract still in flight.
    status VARCHAR(10) NOT NULL DEFAULT 'running',
    -- user | admin_kill | disabled | stop_loss | take_profit | max_consecutive_losses |
    -- max_contracts | max_duration | insufficient_funds | account_frozen | market_closed |
    -- feed_down | restart | shutdown | error
    stop_reason VARCHAR(32),
    last_error TEXT,
    -- Strategy (v1: fixed). Same shape as digit_contracts so the row reads like the
    -- contracts it places.
    contract_type VARCHAR(32) NOT NULL,
    selection JSONB NOT NULL,
    stake NUMERIC(18,2) NOT NULL,
    interval_ms INTEGER NOT NULL,
    -- Stop conditions: stop_loss, take_profit, max_consecutive_losses, max_contracts,
    -- max_duration_s. Absent key = not set. stop_loss is always present.
    limits JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Informational: real + bonus at start. Stop-loss is judged on realised_pnl, never on
    -- balance, because a human may trade the same account meanwhile.
    starting_balance NUMERIC(18,2) NOT NULL,
    contracts_placed INTEGER NOT NULL DEFAULT 0,
    wins INTEGER NOT NULL DEFAULT 0,
    losses INTEGER NOT NULL DEFAULT 0,
    voids INTEGER NOT NULL DEFAULT 0,
    -- Reset by a win, untouched by a void.
    consecutive_losses INTEGER NOT NULL DEFAULT 0,
    -- Reset by any settlement. Three in a row means the feed is not delivering.
    consecutive_voids INTEGER NOT NULL DEFAULT 0,
    realised_pnl NUMERIC(18,2) NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    stopped_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One live run per account. 'stopping' counts as live so a second run cannot start while
-- the first still has a contract in flight.
CREATE UNIQUE INDEX IF NOT EXISTS idx_bot_runs_one_live_per_account
    ON bot_runs (account_id) WHERE status IN ('running', 'stopping');
CREATE INDEX IF NOT EXISTS idx_bot_runs_account_started
    ON bot_runs (account_id, started_at DESC);

-- Provenance: which run placed a contract. NULL means a human did.
ALTER TABLE digit_contracts ADD COLUMN IF NOT EXISTS bot_run_id UUID REFERENCES bot_runs(id);
CREATE INDEX IF NOT EXISTS idx_digit_contracts_bot_run
    ON digit_contracts (bot_run_id) WHERE bot_run_id IS NOT NULL;
```

`bot_run_id` must be threaded through **all four** repo touchpoints in
`digit_contract_repo.go`: `digitSelect` (`:17-23`), `scanDigit` (`:25-46`), `Create`
(`:48-62`) and `GetOpenForUpdate` (`:73-95`, which has its own column list and is the one that
will be forgotten). `models.DigitContract` gains `BotRunID *uuid.UUID \`json:"bot_run_id"\``
(`digit_contract.go:53-76`). `OpenDigitInput` gains `BotRunID *uuid.UUID`
(`digit_service.go:57-64`), and the `digit_open` transaction's `Metadata` gains `bot_run_id`
(`:154-161`) so History can attribute the ledger row too.

New repo `internal/repository/bot_run_repo.go` (`BotRunRepo{db DBTX}`), registered in
`Store` (`store.go:16-28`) and `newStore` (`:35-53`) so it participates in `WithTx`:
`Create`, `Get`, `GetForUpdate`, `ListByAccount(status)`, `ListLive()`, `CountLive()`,
`SetStopping`, `Finalise(id, reason, err)`, `ApplySettlement(id, status, pnl)`,
`IncrementPlaced(id)`, `StopAllLive(reason)`.

### 2.6 Supervision, shutdown, restart

| Event | Behaviour |
|---|---|
| `bots.Start(ctx)` at boot | `UPDATE bot_runs SET status='stopped', stop_reason='restart', stopped_at=now() WHERE status IN ('running','stopping')`. Runs **do not resume**; a run silently resuming after a deploy is the surprise nobody wants. Any contract still open settles or voids via the existing sweep and updates its stopped run (§2.4). |
| SIGTERM | `main.go:44` cancels the root ctx; today only HTTP is drained (`:146-150`) and `pool.Close()` follows (`:58`). Add `bots.Shutdown(shutdownCtx)` beside `srv.Shutdown`: each loop abandons its settlement wait on root-ctx cancel and finalises `stopped/shutdown` using a **detached** `context.WithTimeout(context.Background(), 5s)`, exactly as `onTick` does (`digit_service.go:201`). Writes on the cancelled root ctx would silently fail. |
| Panic in a run | recovered; run finalised `error` with `last_error`. |
| User stop | row → `stopping`; if idle, finalise `user` now; if in flight, finalise when it settles. Idempotent: a stop on a stopped run returns 200 with the row. |
| Admin kill | one transaction: audit row + `Settings.Set(bot_enabled,"false")` + `UPDATE bot_runs … 'stopping' WHERE live`; then `bots.StopAll("admin_kill")` after commit (pattern: `admin_service.go:211-229, 249-283`). Wired like `admin.SetConnectionCounter` (`main.go:95`). |
| Flag flipped off by SQL | each loop's pre-check sees it within the 10 s settings cache (`settings_service.go:49`) and finalises `disabled`. |

### 2.7 API surface

All under `protected` (`routes.go:78`), all money-adjacent writes on a tight bucket.

| Method | Path | Bucket | Notes |
|---|---|---|---|
| `POST` | `/bot/runs` | new `placement` bucket (§3) | body: `account_id, symbol, contract_type, selection, stake, interval_ms, limits{…}`. 201 with the run. |
| `POST` | `/bot/runs/:id/stop` | `placement` | idempotent; 200 with the row. |
| `GET` | `/bot/runs?account_id=&status=live\|stopped` | group | paginated like digit list. |
| `GET` | `/bot/runs/:id` | group | ownership-checked like `DigitService.Get` (`:425-441`). |
| `GET` | `/digit-contracts?bot_run_id=` | group | one more predicate in `digitListWhere` (`digit_contract_repo.go:155-170`). |
| `GET` | `/digit-contracts/quote` | group | **additive**: `win_probability`, `expected_return` on `DigitQuote` (`digit_contract.go:80-86`) so the UI never computes economics itself. |
| `GET` | `/auth/me` | group | **additive**: `bot_enabled` (effective for this user), `bot_real_money_enabled` (`auth_handler.go:152-155`). |
| `GET` | `/admin/bot/runs?status=` | admin read (`routes.go:127`) | |
| `POST` | `/admin/bot/kill` | admin `actions` (`routes.go:140`) | `reason` required (≥10 chars, `requireReason`, `admin_service.go:239-245`); `models.ActionBotKill` beside `models.go:205-209`. |

Error codes: new sentinels in `service/errors.go` with cases in `fail`
(`handlers/response.go:29-84`): `ErrBotDisabled` 403 `bot_disabled`, `ErrBotRunActive` 409
`bot_run_active`, `ErrBotRealMoneyDisabled` 403 `bot_real_money_disabled`, `ErrBotCapacity`
503 `bot_capacity`, `ErrTooManyOpen` 409 `too_many_open`. Messages are user-facing sentences,
as the file's header requires.

WebSocket: one new event type `bot_run` on `UserTopic(userID)` (`deriv_feed_service.go:24-33`)
with the run row as payload, emitted on start, after every settlement, and on stop. The bot
holds `userID` from `StartRun` and broadcasts directly, avoiding `notify`'s per-event account
lookup (`digit_service.go:513-519`).

### 2.8 Feature flags

Three keys in `app_settings` — no migration, the table is generic
(`migrations/0004_app_settings.up.sql`):

| Key | Default | Seeded from | Meaning |
|---|---|---|---|
| `bot_enabled` | `false` | `BOT_ENABLED` | global switch; **gates the API**, not just the UI |
| `bot_allowlist` | `""` | `BOT_ALLOWLIST` | comma-separated user IDs; empty + enabled = everyone |
| `bot_real_money_enabled` | `false` | `BOT_REAL_MONEY_ENABLED` | `false` = `StartRun` refuses `real` accounts |

`SettingsService` is hardcoded to one key with one cache slot
(`settings_service.go:31-33, 74-101`); generalise to a `cachedString(key, default)` helper
first, then add the three accessors. `Bootstrap` seeds each missing row the way it seeds the
digit flag (`:55-69`) — env only seeds, never overwrites. Effective `bot_enabled` for a user
is `enabled && (allowlist == "" || contains(allowlist, userID))`, computed in one place and used
by both `/auth/me` and `StartRun`. This follows `digit-contracts-investigation.md:177` (global,
allowlist as the staged-rollout extension) and differs from the digit flag in one deliberate
way: **this flag gates the service.**

### 2.9 Observability

`internal/observability/metrics.go` gains:

| Metric | Type | Fed by |
|---|---|---|
| `bot_runs_live` | gauge | `Sampler.LiveBotRuns()` → `BotRuns.CountLive`, sampled with the others (`metrics.go:93-116`, `sampler.go:16-27`) |
| `bot_contracts_total{outcome}` | counter | `BotService.OnContract` hook, same shape as `OnDepositSettled` |
| `bot_contracts_placed_total` | counter | `BotService.OnPlacement` hook |
| `bot_runs_stopped_total{reason}` | counter | `BotService.OnStop` hook |
| `bot_placement_errors_total{code}` | counter | `BotService.OnPlacementError` hook |

> **Corrected in Phase 4 CP2–CP3, against measurements rather than on paper.** Two things were
> wrong here. `bot_contracts_total` was fed a `placed` increment as well as an outcome one, so
> each contract counted twice and every reading of the whole metric doubled the truth; placements
> now have their own counter and `bot_contracts_total` counts settlements, one per contract. And
> the expression below mixed units: `rate()` is per second, while `bot_runs_live × 60` is a
> per-minute figure, so as written the alert needed sixty contracts per second per run and could
> never fire. Both sides are now per minute.

What a runaway looks like: `increase(bot_contracts_total[1m]) > bot_runs_live × 60` (more than one
contract per second per run is impossible by construction, so any excess is a bug), or
`bot_runs_stopped_total{reason="error"}` rising. Ledger correctness stays with
`ledger_drift_accounts` (`metrics.go:51-56`), which already covers every bot transaction because
the bot uses the same money path. Logs: one `INFO` per start and stop with reason and totals;
`WARN` on placement error with run id and code.

Admin dashboard: `Dashboard` struct (`admin_queries.go:17-29`) gains `LiveBotRuns int`; the
tile grid (`web/src/features/admin/screens/Dashboard.tsx:156-175`) goes from 5 to 6 columns.

---

## 3. Risk controls

Every control is server-side. Client guards are convenience; the bot itself would bypass them.

### 3.1 Global — apply to humans and bot alike (Phase 0)

| Control | Default | Enforced at | Notes |
|---|---|---|---|
| Maximum stake | `DIGIT_MAX_STAKE=100` | `DigitService.Open`, beside the `> 0` check (`digit_service.go:77-79`) | validation error on `stake` so the ticket shows it inline (`DigitTicket.tsx:53-60` already surfaces `error.field === 'stake'`) |
| Open contracts per account | `DIGIT_MAX_OPEN_PER_ACCOUNT=5` | inside `Open`'s `WithTx`, **after** `Wallets.GetForUpdate` (`:130`) | the wallet lock serialises all Opens on an account, so the count is race-free; served by `idx_digit_contracts_account_status` (0005) |
| Placement rate bucket | `rate.Every(500ms), burst 5` | new `placement := protected.Group(…)` in `routes.go` holding `POST /digit-contracts`, `POST /trades`, `POST /bot/runs`, `POST /bot/runs/:id/stop` | quote GETs stay in the loose group. Contracts settle in ≥0.5 s; nobody legitimately places faster than 2/s |
| Comment fix | — | `digit_payout.go:55` | the "~5% house edge" comment is wrong (`auto-bot-investigation.md` §3.1); correct it to "0.95 × profit; realised edge 5%×(1−p)" |

### 3.2 Per-run — enforced by `BotService` (Phase 1)

| Control | Default | Bound | Enforced |
|---|---|---|---|
| `stake` | user-set | `≤ DIGIT_MAX_STAKE` and `≤ 10% of starting balance` | `StartRun` |
| `interval_ms` | 2000 | `≥ BOT_MIN_INTERVAL_MS=1000` | `StartRun` |
| `limits.stop_loss` | **required** | `> 0`, `≤ starting balance` | after each settlement: `realised_pnl ≤ −stop_loss` |
| `limits.take_profit` | optional | `> 0` | after each settlement |
| `limits.max_consecutive_losses` | 5 | `1–20` | after each settlement; a void neither counts nor resets |
| `limits.max_contracts` | 50 | `1–500` | before each placement; **counts voids**, else a stalled feed places unboundedly |
| `limits.max_duration_s` | 1800 | `60–7200` | before each placement |
| consecutive voids | 3 (fixed, not user-set) | — | → `feed_down`. `ErrNoPrice` cannot detect a stalled feed: `Open` only returns it when `Latest` has *never* had a tick (`digit_service.go:104-106`); a stalled feed has a stale `latest`, `Open` succeeds, and the contract voids 5 s later |
| in flight | 1 | structural | sequential loop + `CountOpenByRun` guard |
| global live runs | `BOT_MAX_RUNS=50` | — | `StartRun` → `ErrBotCapacity`. Bounds settlement load on the feed goroutine (§1) |
| account type | demo only | `bot_real_money_enabled` | `StartRun` |
| frozen / funds / market | — | — | `Open` returns the sentinel; run stops with the matching reason |

**Worst-case drawdown per run = `stop_loss + stake`** (the contract in flight when the
threshold trips). The UI states this figure, not `stop_loss`.

### 3.3 Idempotency

The bot's own path is single-threaded and never retries `Open`, so it needs no key. The manual
API's missing idempotency key (`auto-bot-investigation.md` §6.3) is a separate ticket; not in
this plan's scope.

---

## 4. Run model

### 4.1 Lifecycle

| State | Event | Action → next |
|---|---|---|
| running / idle | pre-check fails (flag off, not allowlisted, duration, max_contracts) | finalise(reason) → stopped |
| running / idle | `Open` ok | `contracts_placed++` in tx → running / in flight |
| running / idle | `Open` = insufficient funds / frozen / market closed | finalise(mapped) → stopped |
| running / idle | `Open` = no price, cold-start retries < 5 | sleep interval → running / idle |
| running / idle | `Open` = no price ×5, or any other error | finalise(feed_down / error) → stopped |
| running / in flight | poll: won / lost / void (counters bumped in settle tx) | read row → post-check → sleep → running / idle, or finalise → stopped |
| running / in flight | poll: `ErrDigitNotOpen` | re-read (lost the settle race) → running / in flight |
| running / in flight | poll deadline `DigitVoidGrace + 2s` | finalise(error, contract id) → stopped |
| running / * | user stop, admin kill, flag off | row → stopping; if idle finalise now → stopped |
| stopping | in-flight settles | finalise(user / admin_kill / disabled) → stopped |
| * / in flight | root ctx cancelled | abandon wait, finalise(shutdown) via detached ctx → stopped; contract left for the boot sweep |
| any goroutine | panic | recover, finalise(error) → stopped |
| running, stopping (rows at boot) | `bots.Start` | single UPDATE → stopped / restart |
| stopped | stop request | 200, no-op |

### 4.2 Stop-loss is judged on the run, not the balance

`starting_balance` is informational. A human can trade the same account while a run is live
(the open-contract cap counts both), so balance delta is not the run's P/L. `realised_pnl` sums
only this run's contracts and is written by the settlement tx (§2.4).

### 4.3 Restart

One statement at boot (§2.6). No aggregate, no race, no resume. The UI shows a `restart`-stopped
run with the copy "Stopped when the service restarted. Start again when you're ready."

### 4.4 Kill switch

Three layers, in order of immediacy: admin `POST /admin/bot/kill` (`StopAll` after an audited
commit, sub-second), the `bot_enabled` flag (SQL flip, ≤10 s via the settings cache), and the
per-user `POST /bot/runs/:id/stop`. Account freeze (`admin_service.go:249-284`) also stops a
run on its next placement via `ErrAccountFrozen` — the existing freeze check inside `Open`'s
transaction (`digit_service.go:134-137`) is what makes that work with no bot-specific code.

---

## 5. UI

Format follows `docs/ui-ux-plan.md`. Everything below is inside the existing one-screen shell;
no new route.

### 5.1 Decision: an inline mode toggle, not a Panel and not an Overlay

The benchmark places AUTO/MANUAL **inside the trading controls** — directly under the digit row
on mobile (`nav-layout-investigation.md:125`) and as a "TRADING MODE" block beside the ticket
on desktop (`:221`). A bot that runs *instead of* the manual ticket should occupy the manual
ticket's space. A Panel would put it in a side column away from the chart and would need a
fifth chrome item plus a new icon (Lucide `Bot` is taken by Signals, `panelItems.ts:15`); an
Overlay would be modal and hide the chart the user wants to watch. Inline needs neither.

### 5.2 Screen: Trade surface in AUTO mode (`DigitTradeScreen`)

**Route:** `/?symbol=R_75&mode=auto` — `mode` merged via `mergeParams`
(`lib/searchParams.ts`), like `symbol` (`DigitTradeScreen.tsx:85`). Reload keeps the mode.
While a run is live, mode is forced to `auto` and the toggle's MANUAL tab is disabled with the
hint "Stop the run to trade manually".

**Purpose:** configure and start a run without leaving the chart; watch it; stop it.

**Components** (top to bottom on mobile; on desktop the last three live in the 380 px ticket
column, `DigitTradeScreen.tsx:110`):
1. `SymbolPicker`, `ContractTypeTabs`, chart, `DigitDistributionRow` — unchanged.
2. **`TradingModeTabs`** — new, a copy of `ContractTypeTabs`' hand-rolled `role="tablist"`
   pattern (`ContractTypeTabs.tsx:21-43`) with `aria-label="Trading mode"` and two tabs,
   **MANUAL** / **AUTO**. Desktop gets a `CardLabel` "Trading mode" above it, matching the
   benchmark. Rendered only when `me.bot_enabled` is true; otherwise the surface is exactly
   today's.
3. `SessionStrip` — its digit branch declares `sm:grid-cols-4` but fills three cells
   (`SessionStrip.tsx:35-59`). The fourth cell becomes **"Auto"**: `—` when idle,
   `Running · 12/50` while live, `Stopped · stop loss` for a minute after a stop.
4. **`BotTicket`** (replaces `DigitTicket` when mode is `auto` and no run is live) —
   props-in/callbacks-out like `DigitTicket`, so it is testable with the existing convention
   (`OrderTicket.test.tsx:24-40`):
   - Selection controls identical to `DigitTicket` (`DigitPicker` for digit/barrier), plus a
     **side** choice (Even/Odd, Match/Differ, Over/Under) since the bot needs one, not two
     buttons.
   - Stake input (same `Input`, same `stakeError` logic, `DigitTicket.tsx:53-60`) with the
     server's maximum shown as a hint.
   - Interval: three chips `1s · 2s · 5s` (default 2s).
   - Stop conditions: **Stop loss** (required), **Take profit** (optional), **Max losses in a
     row** (default 5), **Max contracts** (default 50). Duration is a fixed 30 min in v1 with
     the copy "Runs for up to 30 minutes".
   - **Economics line**, from the quote's new fields: "Pays $19.04 on a win · **Expected
     return −2.4% per contract**". When `expected_return < −5%` (Matches only), an amber note:
     "This contract costs the most per play."
   - **Worst case line**: "Worst case you lose $110 (stop loss + one stake)".
   - Primary CTA **"Start auto run"**. Disabled until quotes load and stop loss is set, and
     for the same feed reasons as the manual ticket (`DigitTradeScreen.tsx:196-199`).
5. **`BotRunCard`** (replaces `BotTicket` while a run is `running` or `stopping`):
   - Header: `AUTO · R_75 · Even · $10 every 2s`, a pulsing dot, elapsed time.
   - Totals grid: Placed `12/50`, W/L/V `6 / 5 / 1`, Realised `−$2.40` (`Money signed`),
     Stop loss progress bar (`realised_pnl / −stop_loss`, red when past 75%).
   - Last contract line: `Lost · digit 7 · 3s ago`.
   - Danger CTA **"Stop run"**; while `stopping`: "Stopping after the current contract…" with
     the button disabled.
6. Toast on stop (via the `bot_run` WS event in `AppShell.tsx:107-121`'s style): success tone
   for `take_profit`, info for `user`/`max_*`, error tone with the reason for `stop_loss`,
   `feed_down`, `insufficient_funds`, `account_frozen`, `admin_kill`, `disabled`, `error`.

**Data needed**
- `GET /auth/me` → `bot_enabled`, `bot_real_money_enabled` (existing `useMe`, 10 s poll).
- `GET /digit-contracts/quote` → now with `win_probability`, `expected_return`
  (existing `useDigitQuote`, one call — the bot has one side).
- `GET /bot/runs?account_id=&status=live` → `useLiveBotRun(accountId)`; `refetchInterval`
  2 s while live, plus the `bot_run` socket event writing the cache directly the way `wallet`
  does (`useAccountStream.ts:81-91`).
- `POST /bot/runs` → `useStartBotRun()`; `POST /bot/runs/:id/stop` → `useStopBotRun()`.
  Both invalidate `botRuns(accountId)`, `digitContracts(accountId)`, `wallet(accountId)`.
- New keys in `queryKeys.ts`: `botRuns(accountId, status?)`, `botRun(id)`.

**States**
- Loading: `BotTicket` renders with a `Skeleton` in the economics line until the quote lands.
- Empty: not applicable.
- Error: `POST /bot/runs` failures use the same inline pattern as `DigitTicket`
  (`humanMessage(error)`, `DigitTicket.tsx:142-144`); `bot_run_active` → "A run is already
  live on this account" with a link that switches to the run card; `bot_real_money_disabled` →
  "Auto runs are available on demo accounts for now" (and the CTA is disabled up front when
  `isReal && !bot_real_money_enabled`, from `useActiveAccount` `useActiveAccount.ts:28-37`).
- Feed stale: same overlay and disable rules as manual.
- Real account, flag off: MANUAL/AUTO toggle still shows; AUTO tab shows the demo-only note
  instead of the form.

**Visual notes**
- The toggle is the only new chrome. No new icon, no fifth nav item.
- Copy never says "AI", "smart", "strategy" or "profit". The tab is **AUTO**; the ticket's
  title is "Auto run"; the sub-line is "Places your settings for you until a stop condition is
  met."
- Red/amber for the stop-loss bar only; totals use text colour, not backgrounds, per the
  Positions rule.

### 5.3 Contract rows

`DigitOpenCard`'s footer row (`DigitContractRows.tsx:26-29`) and `DigitSettledRow`'s chip line
(`:44-54`) get a neutral **BOT** chip, reusing the `void` chip treatment (`:80-91`), when
`contract.bot_run_id` is set. Same two components serve Positions and History, so both surfaces
pick it up.

### 5.4 Admin

- **Dashboard** (`Dashboard.tsx`): sixth tile "Live auto runs"; when > 0, the tile links to
  the runs page. A **"Stop all auto runs"** button in the header uses the existing
  `ReasonDialog` (`admin/components/ReasonDialog.test.tsx` shows its contract) and
  `POST /admin/bot/kill`.
- **Auto runs page** (`/admin/bot-runs`): one entry in `AdminShell.tsx:64-68`, one route in
  `AdminApp.tsx:36-50`; a table of live and recent runs (user, account, symbol, contract,
  stake, placed, realised, status, reason) with a per-row stop. Reuses the list/table pattern
  of `screens/Transactions.tsx`.
- Audit log already renders any action string; `bot_kill` rows appear with no UI change.

---

## 6. Phased plan

Each phase ships on its own, in the style of `digit-contracts-phase-0-1.md`.

### Phase 0 — Safety net and guards. No bot code.

**Built**
1. Digit e2e in CI. Two new Playwright projects `digit-mobile` / `digit-desktop`
   (`playwright.config.ts:25-28`) with `testMatch: /digit\..*\.spec\.ts/`, and `testIgnore`
   for the same pattern on the existing projects. A second CI job **"Browser tests (digit)"**
   duplicating the existing job (`ci.yml`) with `DIGIT_CONTRACTS_ENABLED: "true"` and
   `npx playwright test --project=digit-mobile --project=digit-desktop`. Specs:
   `digit.trading.spec.ts` (open Even at $10 → "opened" toast → settled row in Positions →
   balance moved by exactly the stake or the payout, using `accountPill` from
   `e2e/helpers.ts:21`) and `digit.panels.spec.ts` (the three-item bottom bar in digit mode,
   the case `user.panels.spec.ts:161` hard-codes for CFD). Reuse `waitForDigitSurface` from
   `e2e/digit-phase3-shots.mjs:29-32` (45 s, because the flag polls every 10 s).
2. `DigitLimits` on `DigitService` (§3.1): max stake, open-contract cap inside the wallet
   lock, `ErrTooManyOpen`.
3. `placement` rate bucket in `routes.go` for `POST /digit-contracts` and `POST /trades`.
4. `DigitQuote` gains `win_probability` and `expected_return`; `digit_payout.go:55` comment
   corrected.
5. Config: `DIGIT_MAX_STAKE`, `DIGIT_MAX_OPEN_PER_ACCOUNT` via `getFloat` (`config.go:167`).

**Still absent:** any bot. Nothing user-visible changes except a stake above $100 now says so.

**Exit:** both CI jobs green; `TestDigitMaxStakeRejected` and `TestDigitOpenCapIsRaceFree`
pass; `scripts/smoke.py` gains a burst check proving the placement bucket fires (mirror of the
auth-limiter check it already does).

**Complexity: Medium.** The e2e job is the bulk; the guards are small but touch the money tx.

### Phase 1 — Run model, service, API, flags, metrics. No UI.

**Built**
1. Migration `0006_bot_runs.up.sql` (§2.5); `BotRunRepo`; `bot_run_id` through all four repo
   touchpoints; model and `OpenDigitInput` fields; `digit_open` metadata.
2. Settlement-tx counters in `Open` / `settle` / `voidContract` (§2.4).
3. `SettingsService` generalised; three flags; `Bootstrap` seeds; `/auth/me` fields.
4. `BotService` (§2.2–2.3), `BotLimits` config (`BOT_MIN_INTERVAL_MS`, `BOT_MAX_RUNS`),
   restart statement, shutdown hook in `main.go`.
5. Handlers and routes (§2.7) for user and admin; sentinels in `errors.go` and `fail`.
6. `bot_run` WS event; metrics and sampler (§2.9); `Dashboard.LiveBotRuns`.
7. `scripts/bot_check.py` following `sltp_check.py`'s skeleton: register → `/auth/me` →
   start a run with a $10 stake and $30 stop loss on the mock feed → poll
   `GET /bot/runs/:id` until stopped → assert the reason is one of the limit reasons, every
   contract in `GET /digit-contracts?bot_run_id=` carries the id, and the ledger reconciles
   against the wallet (its existing final check). Added to the CI smoke step.
8. `cmd/seed`: seeds `bot_enabled=true` in development so local and CI can exercise it.

**Still absent:** any UI. The flag is on in dev/CI only.

**Exit:** the integration suite in §8 passes on a real Postgres; `bot_check.py` passes in CI;
`go test ./...`, `go vet`, gofmt clean; a run started by curl on the mock feed stops on its
stop-loss with `ledger_drift_accounts` at 0.

**Complexity: High.** The counters-in-tx change and shutdown semantics are the delicate parts.

### Phase 2 — User UI.

**Built:** `TradingModeTabs`, `BotTicket`, `BotRunCard`, the `SessionStrip` cell, BOT chips,
`useLiveBotRun` / `useStartBotRun` / `useStopBotRun`, `bot_run` in `AccountEvent`
(`useAccountStream.ts:9-19`) with cache write and toasts, `?mode=` param, types
(`types/index.ts`: `BotRun`, `bot_run_id` on `DigitContract`, `bot_enabled` on `User`).

**Exit:** `digit.auto.spec.ts` (mobile and desktop): switch to AUTO → start with stop loss $30
→ run card shows placements climbing → stop → toast → BOT chip on the settled rows → balance
equals starting balance + realised. Component tests for `BotTicket` (validation, economics line,
disabled reasons) and `BotRunCard` (stopping state) in the props-in/spies-out convention.

**Complexity: Medium–high.** Mostly form work; the run card's live state is the only subtle
piece.

### Phase 3 — Admin.

**Built:** dashboard tile and kill button, `/admin/bot-runs` page, `adminApi.botRuns` /
`adminApi.killBots`, `admin.spec.ts` case "stops all auto runs and the user is told why".

**Exit:** admin e2e green; a kill from the console stops a live run within a second and the
user sees the `admin_kill` toast.

**Complexity: Low–medium.**

### Phase 4 — Hardening and product gates.

**Built:** load run — 50 concurrent runs on the mock feed for 10 minutes, watching
`http_request_duration_seconds` on `/ws/prices` fan-out and settle latency; `OnSettled` hook
only if the poll shows up in that profile; a "Runs" tab in the Positions panel listing past runs
(optional); the real-money review with product (§10) and, if approved, flipping
`bot_real_money_enabled` with the confirmation dialog the CFD screen already has
(`TradeScreen.tsx:43,118-137`, `CONFIRM_THRESHOLD = 0.2`) ported to `BotTicket`.

**Exit:** load numbers recorded in the implementation report; product sign-off recorded on the
real-money question either way.

**Complexity: Low–medium**, excluding the product decision.

---

## 7. Breakage list

| Change | Who notices | Mitigation |
|---|---|---|
| Max stake $100 on `POST /digit-contracts` | humans staking more | inline error on the ticket; configurable |
| Open-contract cap 5 per account | a human clicking very fast | `too_many_open` message "Wait for your open contracts to settle" |
| `placement` bucket 2/s on `POST /digit-contracts` and `POST /trades` | nobody legitimate | quote GETs stay loose; e2e places ≤1/s |
| `DigitQuote` JSON gains two fields | nobody | additive |
| `DigitContract` JSON gains `bot_run_id` | nobody | additive, `null` for humans |
| `/auth/me` gains two booleans | nobody | additive; `User` type marks them optional like `digit_contracts_enabled` (`types/index.ts:73`) |
| `NewDigitService` signature | `integration_setup_test.go:139` | updated in the same commit |
| `SessionStrip` fourth cell | visual only | it was already an empty grid slot |
| CI gains a second browser job | CI time roughly doubles for the browser stage | acceptable; it is the regression net the bot drives |
| `main.go` shutdown now waits for runs (≤5 s) | deploys | inside the existing 15 s `shutdownCtx` |

**Must not change:** `DigitService.Open`'s locking order and ledger writes (`:129-174`);
settlement semantics (`:268-423`); the CFD path; the digit flag's behaviour; the
`ledger_drift_accounts` invariant; anything about `OnTick` registration.

---

## 8. Test impact

### Unit (no container)
- `bot_service_test.go`: stop-condition evaluation as a pure function over a run row (every
  reason in §3.2; void neither counts nor resets; `max_contracts` counts voids).
- `settings_service_test.go`: clones for the three keys; allowlist parsing; effective-enabled.
- `digit_payout_test.go` (new): `win_probability`/`expected_return` match the §3.1 table in the
  investigation.

### Integration (`go test -tags integration ./internal/service/...`)
Harness: `newHarness` (`integration_setup_test.go:90`, add `bots` and a real
`SettingsService` over `store.Settings`), `h.newUser` (`:170`), `h.newDemoAccount` (`:218`),
`h.newAdmin` (`:204`), `InjectTickAt` / `InjectTickQuiet` (`deriv_feed_service.go:155,164`),
`SweepForTest` / `SettleForTest` (`digit_service.go:522,526`), `h.assertReconciled` (`:295`),
a capturing `Broadcaster` beside `noopBroadcaster` (`:322`). Templates: `TestDigitWinEven`
(`digit_integration_test.go:20`), `TestDigitAutoVoid` (`:165`), `TestDigitRestartRecovery`
(`:202`), `TestConcurrentOpensCannotOverspend` (`concurrency_test.go:158`),
`TestFreezeRacesOpen` (`:273`).

Phase 0:
- `TestDigitMaxStakeRejected`; `TestDigitOpenCapIsRaceFree` (2×cap concurrent opens with no
  settlement tick: exactly `cap` succeed, reconciled).

Phase 1:
- `TestBotRunPlacesSequentiallyWithProvenance` — never >1 open per run; every contract and its
  `digit_open` transaction carry the run id.
- `TestBotStopLossHaltsRun`; `TestBotTakeProfitHaltsRun`.
- `TestBotVoidIsNotALoss` — stalled feed + backdated `void_after` + sweep: `voids=1`,
  `consecutive_losses` and `realised_pnl` unchanged, run continues.
- `TestBotConsecutiveVoidsStopFeedDown`; `TestBotMaxConsecutiveLossesResetsOnWinNotVoid`;
  `TestBotMaxContractsCountsVoids`; `TestBotMaxDuration`.
- `TestBotUserStopWaitsForInFlight` — `stopping` while open; `stopped/user` after the tick;
  totals include that contract.
- `TestBotKillSwitchStopsAllRuns` — three users; `admin_kill` on all; `bot_enabled=false`;
  audit row; `StartRun` refused with `bot_disabled`.
- `TestBotRestartMarksRunsStoppedAndTotalsReconcile` — seed a `running` row and an open bot
  contract; `InjectTickQuiet`; `digits.Start` then `bots.Start`; row is `stopped/restart`; the
  sweep settles the straggler and the counters still update.
- `TestBotShutdownFinalisesRun` — cancel the root ctx mid-flight; `stopped/shutdown`; contract
  still open; boot sweep voids; reconciled.
- `TestBotAndHumanConcurrentOpensNeverOverdraw` — balance 100, stake 10, bot running plus 20
  human goroutines; debits ≤ 100; cap never exceeded; `assertReconciled`.
- `TestBotRefusesSecondRunOnSameAccount` (including while `stopping`); `TestBotStartGates`
  (flag off, not allowlisted, real account, stake over bound, interval under floor, frozen,
  capacity); `TestBotStopsOnInsufficientFunds`; `TestBotFreezeStopsRun`;
  `TestBotLosesSettleRaceGracefully` — exactly one settlement when the poll and `SettleForTest`
  race.

### Browser
Phase 0: `digit.trading.spec.ts`, `digit.panels.spec.ts` in a new CI job. Phase 2:
`digit.auto.spec.ts`. Phase 3: one `admin.spec.ts` case. The existing CFD job is untouched.

### Scripts
`scripts/bot_check.py` (Phase 1) in the CI smoke step; `smoke.py` gains the placement-bucket
burst check (Phase 0).

---

## 9. Effort estimate

| Phase | Effort | Notes |
|---|---|---|
| 0 — e2e job + global guards | **Medium** | the e2e job is most of it; guards touch the money tx |
| 1 — schema, service, API, flags, metrics | **High** | counters-in-tx, shutdown, and ~20 integration tests |
| 2 — user UI | **Medium–high** | form-heavy; one live component |
| 3 — admin | **Low–medium** | pattern-following |
| 4 — hardening / product gates | **Low–medium** | plus a product decision |

Phases 0 and 1 together are roughly two-thirds of the total and produce no visible bot.

---

## 10. Open questions for the client

Product calls, not engineering. The plan is built so each is a flag or a copy change, not a
rewrite.

1. **Should a bot ship at all against a payout table marked "NOT final for real-money use"**
   (`digit_payout.go:9-14`)? The plan's answer is "demo-only until the table is final", but
   whether to ship even that is yours.
2. **What is the bot's honest value proposition, and what does the UI say?** Every contract has
   negative expected value (−0.50% to −10.00%). The plan's copy is "runs your settings for you"
   and shows the expected return per contract. If you want the number hidden, say so — but
   hiding it is the compliance-relevant choice, not showing it.
3. **Demo-only for v1, or real money?** Built as a flag (`bot_real_money_enabled`, default
   off). Flipping it is the decision; the confirmation dialog for large stakes is Phase 4.
4. **Does a bot need regulatory treatment the manual surface does not?** Unknown to
   engineering. If yes, it likely affects the copy in (2) and the real-money gate in (3).
5. **Which contract types may a bot use?** The plan allows all three with the edge displayed
   and an amber note on Matches (−10%). Restricting to the −0.50% band (Over 0 / Under 9) is a
   one-line server change if you prefer a smaller burn rate over choice.

---

## 11. Decisions taken in this plan

Do not reopen these by accident. Each has its reasoning in the section cited.

1. **Loop is server-side, one goroutine per run, sequential, never on `OnTick`** (§2.1, §2.3).
2. **Settlement wait is polling `DigitService.Get`, not a callback** (§2.3). Revisit at a few
   hundred runs.
3. **Run totals are written inside the existing open/settle/void transactions**, never by the
   loop (§2.4). This is what makes restart one statement.
4. **Runs do not resume across restart** (§2.6).
5. **`Open` never runs on the run's cancellable context and is never retried on an unknown
   error** (§2.3).
6. **`stopping` is a persisted state and counts as live for the one-run-per-account index**
   (§2.5, §4.1).
7. **`feed_down` is three consecutive voids, not `ErrNoPrice`** (§3.2).
8. **Global guards apply to humans too and land first**: max stake $100, open cap 5,
   placement bucket 2/s (§3.1, Phase 0).
9. **Per-run bounds**: stake ≤ min($100, 10% of balance), interval ≥ 1 s, stop-loss required,
   `max_contracts` counts voids, `BOT_MAX_RUNS=50` (§3.2).
10. **v1 strategy is `fixed` only** — one type, one selection, one stake, one interval. No
    history-reading, no alternation, no martingale, no multi-symbol, no backtesting, no
    sharing, no copy trading. Digits are uniform; none of those could produce an edge and all
    of them would imply one (§1, `auto-bot-investigation.md` §2.5).
11. **All three contract types allowed; the UI shows expected return per contract and warns on
    Matches** (§5.2). Restriction is product's call (§10.5).
12. **The bot flag gates the service, not just the UI** — unlike the digit flag (§2.8).
13. **Demo-only by flag** (`bot_real_money_enabled=false`), not by code (§2.8).
14. **UI is an inline MANUAL/AUTO toggle in the trade surface**, not a Panel or Overlay; no
    new icon; the word "AI" does not appear (§5.1).
15. **Digit e2e coverage is Phase 0**, as a second CI job, before any guard or bot code merges
    (§6).
16. **Worst-case drawdown is stated as `stop_loss + stake`** in the UI (§3.2).
17. **Manual-API idempotency is out of scope**; the bot path does not need it (§3.3).
