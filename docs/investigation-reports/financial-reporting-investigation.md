# Investigation: financial / house-edge reporting for the admin console

**Date:** 26 September 2026 · **Branch:** `master` @ `8430ae7` · **Kind:** read-only. No code changed.
Go paths are repo-relative; web paths are under `web/`.

---

## Summary

**The data is there and the arithmetic works, with three qualifications that decide the shape of the whole thing.**

1. **House P&L is `−SUM(digit_contracts.pnl)`, and that identity is exact** — not an approximation. Verified against the settlement code, not just the column names (§1.3).
2. **`digit_contracts` cannot tell you whether a stake was real money or bonus money.** There is no `balance_type` column on it; the funding balance lives only on the `transactions` row, joined by `transactions.reference = digit_contracts.id::text`. Every "real money" figure needs that join or it is wrong (§1.4). This is the single most important finding here.
3. **Excluding demo is necessary but not sufficient.** Demo accounts are funded entirely from bonus balance, so excluding `account_type = 'demo'` removes most of the distortion — but an admin adjustment can put bonus balance on a *real* account, and `pickBalance` spends bonus first. A real account trading promotional money produces real payouts against money the house gave away (§1.4).

**Realised-vs-theoretical edge is the most valuable thing here and it is cheap.** The payout table is still marked NOT FINAL FOR REAL-MONEY USE, and `models.ExpectedReturnFor` gives the predicted edge per selection. Comparing it against what actually happened is a live check on the table — and on the RNG feed. It needs no new data.

**Effort, honestly:** the queries are a day's work on patterns that already exist. The screen is standard `DataTable` + tiles. The two things that are *not* free are the `real-vs-bonus` join (§1.4) and one index (§4.1). No new infrastructure — no cache, no job, no new chart library.

**One scoping fact that changes the priority:** `bot_real_money_enabled` and `withdrawals_enabled` are both `false`, and no production user is `verified`. **Every figure this dashboard would show is currently zero or demo.** That is an argument for building it now — it can be built and read correctly against demo data before there is any real money to get wrong — not for deferring it.

---

## 1. What data already exists

### 1.1 The tables that matter

| Table | Columns that carry money | Migration |
|---|---|---|
| **`digit_contracts`** | `stake NUMERIC(18,2)`, `payout_ratio NUMERIC(10,6)`, `potential_payout NUMERIC(18,2)`, `pnl NUMERIC(18,2)`, `status` (`open\|won\|lost\|void`), `settled_at`, `opened_at`, `contract_type`, `selection JSONB`, `bot_run_id`, `account_id` | `0005`, `bot_run_id` added in `0006` |
| **`transactions`** | `type`, `balance_type` (`real\|bonus`), `amount` (signed), `status`, `reference`, `created_at` | `0001` |
| **`trades`** (CFD) | `stake`, `pnl`, `status`, `opened_at`, `closed_at` | `0001` |
| **`bot_runs`** | `stake`, `contracts_placed`, `wins`, `losses`, `realised_pnl`, `starting_balance`, `status`, `stop_reason` | `0006` |
| **`accounts`** | `account_type` (`real\|demo`) — the demo filter hangs off this | `0001` |
| **`mpesa_payouts`** | `amount` (KES), `status` | `0009` |

Transaction types (`internal/models/models.go:29-39`): `deposit`, `withdrawal`, `trade_open`, `trade_close`, **`digit_open`**, **`digit_settle`**, `bonus_credit`, `admin_adjustment`.

### 1.2 Aggregate patterns already in the codebase

There is more to build on than the brief assumed. Four existing patterns, in increasing order of usefulness:

| Pattern | Where | Why it matters here |
|---|---|---|
| **`balanceAffectingRows`** | [`reconciliation.go:35-38`](../../internal/repository/reconciliation.go) — `status = 'completed' OR (status = 'pending' AND type = 'withdrawal')` | The one predicate that defines "this money has moved". **Any deposit/withdrawal figure must use it**, or it will disagree with the dashboard and the drift gauge. A pending withdrawal is money already gone. |
| **`TodayTotals(txType)`** | [`transaction_repo.go:200-214`](../../internal/repository/transaction_repo.go) | `count(*), sum(abs(amount))` over a type and a day boundary. Generalising this to an arbitrary range is the deposit/withdrawal half of the whole feature. Its comment records a real bug: filtering on `'completed'` alone made withdrawal tiles structurally zero. |
| **`DigitSummary`** | [`digit_contract_repo.go:259-273`](../../internal/repository/digit_contract_repo.go) | **The closest thing to a template.** Already does `count(*) FILTER (WHERE status = …)` and `sum(pnl) FILTER (…)` over `digit_contracts` since a timestamp. The financial version is this query with the account filter swapped for a global one and a `GROUP BY contract_type`. |
| **`DepositsByHour`** | `transaction_repo.go:236-260` | A `generate_series` time-bucket left-joined to real rows, so empty hours appear as zeros rather than gaps. This is exactly the shape a "volume over time" series needs, and the bug it avoids — missing buckets — is the one a naive `GROUP BY date_trunc` hits. |

`AdminService.Dashboard` ([`admin_queries.go:39-81`](../../internal/service/admin_queries.go)) composes these as **sequential independent queries** — eleven of them, each `if … err != nil { return nil, err }`. A financial summary would follow the same shape.

### 1.3 Can house P&L be computed correctly? Yes, and here is the proof

The settlement path, `internal/service/digit_service.go:367-372`:

```go
pnl := c.Stake.Neg()                              // loss: −stake
credit := decimal.Zero
if won {
    credit = models.WinCredit(c.Stake, c.PayoutRatio)   // stake × (1 + ratio)
    pnl = credit.Sub(c.Stake)                            // win: +stake × ratio
}
```

And a void refunds the stake with `pnl := decimal.Zero` (`:470`), netting to zero against the open debit.

So per contract, from the **house's** side:

| Outcome | Customer `pnl` | House position |
|---|---|---|
| Lost | `−stake` | `+stake` |
| Won | `+stake × ratio` | `−stake × ratio` |
| Void | `0` | `0` |

**House P&L = `−SUM(pnl)` over settled contracts.** This is exact, not an estimate.

**The "stakes minus payouts" formulation in the brief is also correct, and it is worth showing they agree**, because the equivalence is what lets the dashboard state both without them drifting:

```
total_stakes  = Σ stake                      (over won ∪ lost)
total_payouts = Σ stake × (1 + ratio)        (over won only)
stakes − payouts
  = Σ_lost stake + Σ_won stake − Σ_won stake × (1 + ratio)
  = Σ_lost stake − Σ_won stake × ratio
  = −Σ pnl                                                    ∎
```

Voids drop out of both sides. **One caveat that makes them differ in practice:** `WinCredit` applies `.Round(2)`, so the payout figure carries per-contract rounding that `pnl` inherits. Computing both from the same `pnl` column keeps them consistent; computing `total_payouts` independently as `stake × (1 + payout_ratio)` in SQL will disagree by fractions of a cent per winning contract. **Derive payouts from `pnl`, not from `payout_ratio`.**

**A structural safeguard already exists and should be reused:** `Drift` (`reconciliation.go:68`) compares every wallet against the sum of its ledger rows. If that gauge is zero, the ledger and the balances agree, which means a P&L computed from `digit_contracts` and one computed from `transactions` must also agree. A financial screen that shows house P&L alongside the drift count is stating its own trustworthiness.

### 1.4 The accounting subtleties — the part that will be got wrong

**(a) `digit_contracts` has no `balance_type`.** The funding balance is recorded only on the `transactions` row. The link is `transactions.reference = digit_contracts.id::text` with `type = 'digit_open'` — which is exactly what `QueryDigitFundingBalance` does one row at a time ([`transaction_repo.go:122-126`](../../internal/repository/transaction_repo.go)), and what the aggregate must do in bulk:

```sql
FROM digit_contracts dc
JOIN accounts a  ON a.id = dc.account_id
JOIN transactions t ON t.reference = dc.id::text AND t.type = 'digit_open'
WHERE a.account_type = 'real' AND t.balance_type = 'real'
```

**`transactions.reference` is `VARCHAR(100)` with a plain btree index** (`idx_transactions_reference`, `0003:51`), so the join is indexed — but it is a text-to-uuid comparison, which is a correctness smell worth stating even though it works.

**(b) Demo is funded entirely from bonus.** `CreateAccount` gives a demo account `DemoStartingBalance` as **bonus** (`wallet_service.go:163-182`, `TxBonusCredit` / `BalanceBonus`), and a real account **zero** of both. `pickBalance` spends bonus first (`trading_service.go:467-475`). So today, filtering `account_type = 'real'` removes essentially all bonus-funded volume.

**(c) But bonus on a real account is possible.** `AdminService.Adjust` accepts `balance_type = 'bonus'` on any account (`admin_service.go:593-595`). The moment anyone runs a promotion, a real account holds bonus money, `pickBalance` spends it first, and a win on it pays **real, withdrawable value** out of money the house never received. That is a genuine house cost and it is invisible to an `account_type`-only filter.

**Recommendation:** treat real-vs-bonus as a first-class split, not a filter. Three figures, not one: house P&L on real-funded stakes (the business number), house P&L on bonus-funded stakes on real accounts (**promotional cost**), and demo (excluded from money entirely, kept for engagement).

**(d) Void contracts must be excluded from volume, not just from P&L.** They net to zero but their stake is real volume that never resolved. Count them separately; `DigitSummary` already does.

**(e) Bot runs are not a separate P&L source.** `bot_runs.realised_pnl` is a maintained total of the contracts the run placed — those same rows are already in `digit_contracts` with `bot_run_id` set. **Summing both double-counts.** Use `digit_contracts.bot_run_id IS NOT NULL` for the bot/manual split, and treat `realised_pnl` as a cross-check.

**(f) CFD `trades` is a second, parallel source.** Same `stake`/`pnl` shape. `digit_contracts_enabled` chooses the surface, so both may hold data. A financial screen that reads only `digit_contracts` will silently under-report if CFD is ever enabled.

**(g) Deposits and withdrawals are in KES-converted USD.** `transactions.amount` is USD, converted at `KES_PER_USD` at the time (`wallet_service.go:726-732`), with the KES figure in `metadata.amount_kes`. A float position quoted in USD is therefore at historical rates.

---

## 2. What a real financial dashboard needs to answer

Ordered by what the business cannot operate without.

### 2.1 Is the house winning what the maths says it should? — **the highest-value question**

This is a control, not a vanity metric, and it is the one thing on this list that nothing else in the system can tell you.

- **Realised edge** = `−SUM(pnl) / SUM(stake)` over settled non-void contracts.
- **Theoretical edge** = the stake-weighted mean of `models.ExpectedReturnFor(contract_type, selection)`, now `5% × (1 − p)` for every selection — between −0.50% and −4.50% ([`digit_payout.go`](../../internal/models/digit_payout.go), PR #14).

Per contract type and barrier, realised should converge on theoretical. It is a live check on three things at once: **the payout table** (still marked NOT FINAL), **the feed's uniformity** (the whole edge assumes last digits are uniform and independent), and **the settlement code**.

A confidence interval matters more than the point estimate — at n=100 contracts the noise dwarfs a 2% edge. The screen should show the sample size beside the figure and say when it is too small to read, rather than inviting someone to over-read early numbers.

**Computing theoretical edge needs Go, not SQL** — `ExpectedReturnFor` parses the `selection` JSONB per contract type. Either aggregate stakes grouped by `(contract_type, selection)` in SQL and weight in Go, or store the expected return at open. The first needs no migration and is the right call at this volume.

### 2.2 Volume and house P&L over a range

Total staked, house win/loss, contract count, win rate — over a selectable range, **split real / bonus-on-real / demo** per §1.4.

### 2.3 Per-contract-type breakdown

Volume, house P&L and realised edge grouped by `contract_type`. This is where a mispriced selection shows up: if Matches is heavily traded and its realised edge sits below the others, that is a pricing problem visible nowhere else. `DataTable` renders this as-is.

### 2.4 Deposits, withdrawals and net float

`SUM(deposits) − SUM(withdrawals)` over the range, both through `balanceAffectingRows`. **Net float is not profit** — it is customer money held. The screen must not present it as earnings; the pairing that means something is float against the sum of all customer balances (`SELECT sum(real_balance) FROM wallets`), because that is the liability the float covers.

Worth surfacing alongside: `mpesa_payouts` in `unknown` or past give-up (money debited with no confirmed outcome — the state `PayoutOutcomeUnknown` alerts on).

### 2.5 Bot versus manual

`bot_run_id IS NOT NULL` splits it. Worth having, for one specific reason: **a bot places at a fixed interval and never tires**, so if the edge is right it converges faster than manual play and diverging bot/manual edges point at either the strategy or the feed. It is also the population most exposed if `bot_real_money_enabled` is ever turned on.

---

## 3. Existing design patterns to reuse

### 3.1 The components

| Component | File | Fit |
|---|---|---|
| **`DataTable<T>`** | [`components/DataTable.tsx`](../../web/src/features/admin/components/DataTable.tsx) | `Column<T>` with `align: 'right'` for money, sticky header, its own horizontal scroll, built-in loading/error/empty states and pagination. **Per-contract-type breakdown drops straight in.** |
| **`Tile`** | `screens/Dashboard.tsx:17-38` | `label` / `value` / `sub`. The summary figures are tiles. Not exported — would need lifting to `components/`. |
| **`DepositChart`** | `screens/Dashboard.tsx:83-126` | See below. |
| **`SearchInput`**, **`badges.tsx`**, **`ReasonDialog`** | `components/` | Filter chips and status pills; `ReasonDialog` is for actions and is **not** needed — this screen is read-only. |
| Filter-chip row | `screens/Transactions.tsx`, `HistoryPanel.tsx` | The pattern for a range selector (24h / 7d / 30d / all). |

### 3.2 Charting — recharts is already a dependency, and the admin console deliberately does not use it

**`recharts ^3.10.1` is in `package.json`** and used by the trading `PriceChart`. But `DepositChart` — the only chart in the admin console — is **hand-rolled from flex divs and percentage heights** (`Dashboard.tsx:83-126`), not recharts.

That looks like an oversight and is probably a decision: a 24-bar histogram needs no library, and the admin bundle stays free of a charting dependency. Worth honouring rather than overriding. Two options:

- **Extend the hand-rolled pattern** for a volume-over-time bar series. Same `generate_series` data shape as `DepositsByHour`, same rendering. No new dependency, consistent with the one chart that exists.
- **Use recharts** if the design needs dual axes or a line-plus-bar (realised edge over time against theoretical, say) — it is already a dependency, so this costs bundle size in the admin chunk, not a new package.

**Recommendation: start with the hand-rolled pattern.** The first version's charts are a volume histogram and a P&L bar series, both of which the existing pattern renders. Reach for recharts only when a chart genuinely needs two axes, and say so when doing it.

### 3.3 New screen, tiles, or both — both, in that order

- **A new screen**, `/admin/finance`, is the right home: a range selector plus four or five tiles, a volume chart, and two `DataTable`s (by contract type, by day). It hangs off the existing `AdminShell` nav array (`AdminShell.tsx:75-81`) as one more entry, and a new route in the admin group (`routes.go:194-201`).
- **Then two tiles on the existing dashboard** — house P&L today, and realised-vs-theoretical edge — linking through. The dashboard is what an operator has open; the screen is where they go to understand a number.

Do not put the whole thing on the dashboard. That screen already carries eleven queries, and §4.2's cost argument applies to it specifically.

---

## 4. Performance and correctness

### 4.1 The index that has to exist

`digit_contracts` has four indexes (`0005:31-38`, `0006:52`): `(account_id, status)`, `(bot_run_id) WHERE NOT NULL`, and two partial ones on open contracts. **None covers a time-ranged aggregate.**

`WHERE settled_at >= $1 AND status IN ('won','lost')` therefore **sequentially scans the whole table**. Fine at thousands of rows; not fine later, and a bot placing a contract every two seconds is 43,200 rows per run-day per account.

**Recommended:** `CREATE INDEX idx_digit_contracts_settled ON digit_contracts (settled_at DESC) WHERE settled_at IS NOT NULL` — partial, so open contracts stay out, matching this codebase's existing partial-index style. If per-type grouping proves slow, `(settled_at DESC, contract_type)`.

The `transactions` side is already covered by `idx_transactions_created` (`0003:50`) for the deposit/withdrawal half.

**Measure before adding more.** One partial index on `settled_at` is a safe pre-emptive add; anything beyond that should follow an `EXPLAIN ANALYZE` against realistic row counts, not a guess.

### 4.2 Live or cached? — **live, and here is the threshold**

The existing precedent is split, and both halves are informative:

- **Dashboard is live** — eleven queries on every page load (`admin_queries.go:39-81`), no caching.
- **Prometheus gauges are sampled** — `StoreSampler` (`observability/sampler.go`) runs counts on a schedule because Prometheus scrapes every 15s regardless of whether anyone is looking.

A financial screen is **operator-triggered and infrequent**, like the dashboard, not continuously polled like a gauge. **Compute live.** With the index in §4.1 an aggregate over a month of contracts is a bounded index scan, and a stale financial figure is worse than a slow one.

**When that stops being true:** roughly when a single range aggregate exceeds a second or two — order 10⁶–10⁷ settled contracts. At that point the answer is a **daily rollup table** (`digit_daily_stats`: date, contract_type, funding balance, account type, stakes, payouts, counts) written by a nightly job, with today computed live and added on. That is real new infrastructure — a job, a table, a backfill, and a reconciliation between rollup and source — and it should not be built until the query is measurably slow.

One thing that *would* justify caching earlier: if the dashboard tiles in §3.3 add a full aggregate to every dashboard load. Keep the tiles' queries narrow (today only, which the `settled_at` index serves directly), or cache just those.

### 4.3 Correctness risks, ranked

1. **Omitting the funding-balance join** (§1.4a) — the most likely mistake, because `digit_contracts` looks complete on its own. Produces a house P&L that counts promotional money as earnings.
2. **Deriving payouts from `payout_ratio` instead of `pnl`** (§1.3) — two figures that should agree by construction, drifting by rounding.
3. **Not using `balanceAffectingRows`** for deposits/withdrawals (§1.2) — the exact bug that comment records, which made withdrawal tiles structurally zero.
4. **Double-counting bot P&L** by summing `bot_runs.realised_pnl` alongside `digit_contracts.pnl` (§1.4e).
5. **Reading realised edge at small n** (§2.1) — a presentation risk, not a query bug, and the one most likely to cause a wrong business decision.

---

## 5. Recommended shape for the plan

**Simple — existing patterns, no new infrastructure:**

- One repository file, `internal/repository/finance_repo.go`, with three or four range-scoped aggregates modelled on `DigitSummary` and `TodayTotals`: totals, by-contract-type, by-day series, deposits/withdrawals/float.
- One service method, `AdminService.Finance(ctx, from, to)`, composing them the way `Dashboard` composes its eleven — plus the theoretical-edge weighting in Go (§2.1).
- One handler + route, `GET /admin/finance`, in the existing admin group.
- One screen, `screens/Finance.tsx`: range chips, tiles, the hand-rolled bar chart, two `DataTable`s. One nav entry.
- `Tile` lifted from `Dashboard.tsx` into `components/` so both screens share it.
- **One migration: the partial index in §4.1.**

**Needs a decision before it can be built:**

- **Does "house P&L" include bonus-funded wins?** (§1.4c) — an accounting question, not an engineering one. Recommendation: show both, separately labelled.
- **Is CFD `trades` in scope?** (§1.4f) — depends on whether the CFD surface will ever be enabled in production.

**Deliberately not in the first version:**

- Rollup tables and any caching (§4.2) — until measured.
- Recharts in the admin console (§3.2) — until a chart needs it.
- Exports, scheduled reports, per-user profitability — none is needed to answer the launch question.

**Verification, when it is built:** this is money-path-adjacent — it reports on money rather than moving it, so no mutation matrix, but the aggregates deserve integration tests against a seeded database with known outcomes. The strongest test available is a **cross-check**: compute house P&L from `digit_contracts.pnl` and independently from `transactions` (`digit_open` minus `digit_settle`), and assert they agree. Two paths to the same number, the same shape as the drift check that already guards the ledger.
