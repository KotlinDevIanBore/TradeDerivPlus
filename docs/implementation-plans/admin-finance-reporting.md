# Implementation plan: admin Finance / house-edge screen

**Date:** 26 September 2026 · **Branch:** `master` @ `8430ae7`
**Source:** [`docs/investigation-reports/financial-reporting-investigation.md`](../investigation-reports/financial-reporting-investigation.md) — the investigation this plan executes; section numbers cited as § refer to it.
**Kind:** plan. Shapes, not finished artifacts: the SQL and components are sketched so a reader can execute without re-reading the investigation, and left for the implementation to write in full.


## Context

`docs/investigation-reports/financial-reporting-investigation.md` established that the admin console has no screen answering the business question — how much was traded, what the house won or lost, and whether the realised edge matches the payout table. It also established that the data exists, that house P&L is exactly `−SUM(digit_contracts.pnl)`, and that the one trap is that `digit_contracts` carries no funding balance: whether a stake was real or bonus money lives only on the `digit_open` ledger row.

**Status:** plan, not yet executed. No application code has been changed for it.

**Sign convention throughout:** everything is house-side. `house_pnl = −SUM(pnl)`; `realised_edge = house_pnl / stakes`; `theoretical_edge = −(stake-weighted ExpectedReturnFor)`. Positive means the house is ahead. Stated in the Go struct comment and on the screen.

---

## The two decisions, resolved

**D1 — House P&L and bonus-funded wins.** The headline house P&L counts **real accounts, real-funded stakes only**. Bonus-funded stakes on real accounts are reported separately, labelled **"Promotional cost"** — a win there pays withdrawable value against money the house gave away (investigation §1.4c), so it is a real cost but not trading performance. Demo accounts are **excluded from every money figure** and shown only as a contract count. The three-way split is one `CASE` over `(accounts.account_type, transactions.balance_type)`, computed in the same query, so the three can never be summed by mistake. *Why not fold promotional into the headline:* it would make a promotion look like a losing week; *why not hide it:* it is the figure that tells you what a promotion actually cost.

**D2 — CFD `trades` is out of v1.** Digit contracts are the production default surface (`ci.yml` states it; `DIGIT_CONTRACTS_ENABLED` defaults on). A second source doubles every aggregate and every test for a surface that may never be enabled in production. The omission must be **visible**, not silent: the screen carries a permanent scope note ("Digit contracts only. CFD trades are not included.") and the report JSON carries `surface: "digit_contracts"`. `finance_repo.go` is shaped so a `trades` aggregate is one more function returning the same bucket shape.

---

## 1. Scope

**In v1**
- Digit contracts only (D2), three-way funding split in every money figure (D1).
- Ranges `24h | 7d | 30d | all`. Live computation, no cache (investigation §4.2).
- Realised vs theoretical edge with sample size and a too-few-to-read flag (§2.1).
- Per-contract-type table; per-day table and a hand-rolled bar chart; deposits, withdrawals, net float, customer liability (§2.2–2.4).
- Bot vs manual by `bot_run_id IS NOT NULL` only (§1.4e).
- One partial index migration (§4.1).
- Two narrow dashboard tiles linking through.

**Deferred, named so the omission is visible**
- CFD `trades` (D2). Rollup tables / caching until a query is measurably slow (§4.2). recharts in the admin console until a chart needs two axes (§3.2). Exports, scheduled reports, per-user profitability. Custom `from`/`to` params — the service already takes `(from, to)`, so this is handler-only work later.

---

## 2. Data layer — `internal/repository/finance_repo.go` (new)

`FinanceRepo{db DBTX}`, registered in `newStore` (`internal/repository/store.go:37`) — that constructor is what `WithTx` uses, so registering in `NewStore` alone would leave it nil inside transactions. All ranges are half-open `[from, to)`.

### 2.1 One scope fragment — the guard against risk #1

Every contract aggregate in the file is built on this fragment. The file header states that **no other `FROM digit_contracts` may appear in the file**, and that `payout_ratio` and `bot_runs` are never read here (risks #2, #4).

```sql
-- financeScope
FROM digit_contracts dc
JOIN accounts a     ON a.id = dc.account_id
JOIN transactions t ON t.reference = dc.id::text AND t.type = 'digit_open'
WHERE dc.status IN ('won','lost','void')
  AND dc.settled_at >= $1 AND dc.settled_at < $2
```

Join to `digit_open` only — one row per contract, always present (joining `digit_settle` too would double rows). `t.balance_type` on it is the funding balance; `QueryDigitFundingBalance` (`transaction_repo.go:122`) does the same one row at a time.

```sql
-- fundingBucket
CASE WHEN a.account_type = 'demo'  THEN 'demo'
     WHEN t.balance_type  = 'bonus' THEN 'promotional'
     ELSE 'real' END
```

### 2.2 The aggregates

Shared row shape:

```go
type FinanceBucket struct {
    Contracts, Wins, Losses, Voids int
    Stakes       decimal.Decimal // sum(stake) FILTER (status <> 'void')   — voids are not volume, §1.4d
    HousePnL     decimal.Decimal // -COALESCE(sum(pnl),0)                   — from pnl only, never payout_ratio, §1.3
    BotContracts int             // count(*) FILTER (bot_run_id IS NOT NULL)
    BotHousePnL  decimal.Decimal // -sum(pnl) FILTER (bot_run_id IS NOT NULL)
}
```

| Fn | Shape | Pattern reused |
|---|---|---|
| `Totals(ctx, from, to) (map[string]FinanceBucket, error)` | `SELECT <fundingBucket> AS bucket, count(*), count FILTER won/lost/void, sum(stake) FILTER (status<>'void'), -sum(pnl), bot filters … <financeScope> GROUP BY 1`. Missing buckets zero-valued in Go. | `DigitSummary`, `digit_contract_repo.go:259` |
| `ByContractType(ctx, from, to) ([]FinanceTypeRow, error)` | Same select list plus `dc.contract_type`; `GROUP BY bucket, contract_type ORDER BY contract_type`. Service folds to one row per type. | same |
| `ByDay(ctx, from, to) ([]FinanceDayRow, error)` | `WITH days AS (generate_series(date_trunc('day',$1), date_trunc('day',$2 - '1 microsecond'), '1 day')), settled AS (SELECT date_trunc('day', settled_at) d, <fundingBucket>, stake, pnl, status <financeScope>) SELECT … FROM days LEFT JOIN settled ON d GROUP BY d ORDER BY d LIMIT 366`. Empty days are zeros, not gaps. Cap at 366; service sets `by_day_capped`. | `DepositsByHour`, `transaction_repo.go:236` |
| `EdgeCells(ctx, from, to) ([]EdgeCell, error)` | `SELECT contract_type, selection, count(*), sum(stake), -sum(pnl), and the same three FILTER (bucket='real') <financeScope> AND status <> 'void' GROUP BY contract_type, selection`. `selection` scanned as `[]byte`, parsed with `models.ParseDigitSelection` (`digit_contract.go:46`). Two populations in one pass: all funding (the game/feed check, readable against demo today) and real-only. | new; SQL-side grouping, Go-side weighting (§2.1) |
| `Flows(ctx, from, to) (FinanceFlows, error)` | `count/sum(abs(amount)) FILTER (type='deposit'/'withdrawal') FROM transactions WHERE type IN (…) AND ` + `balanceAffectingRows` + ` AND created_at in range`; plus `SELECT COALESCE(sum(w.real_balance),0) FROM wallets w JOIN accounts a … WHERE a.account_type='real'` for liability (point-in-time). | `TodayTotals`, `transaction_repo.go:200`; the const from `reconciliation.go:35` — same package, compile-time reuse (risk #3) |
| `EarliestSettlement(ctx) (time.Time, bool, error)` | `SELECT min(settled_at) FROM digit_contracts WHERE settled_at IS NOT NULL` — for `range=all`, an index-end lookup. | — |

### 2.3 Migration

`migrations/0011_digit_contracts_settled_index.up.sql`:
`CREATE INDEX IF NOT EXISTS idx_digit_contracts_settled ON digit_contracts (settled_at DESC) WHERE settled_at IS NOT NULL;` — partial, matching `0005:33-38` / `0006:52`. **Plain `CREATE INDEX`, not `CONCURRENTLY`:** `repository.Migrate` (`db.go:96-101`) wraps each file in a transaction, and `CONCURRENTLY` cannot run inside one. Fine at current row counts; the header comment says so for whoever revisits it at scale.

`…down.sql`: `DROP INDEX IF EXISTS idx_digit_contracts_settled;` with the same note `0010`'s down file carries — `Migrate` reads only `*.up.sql`, so this is documentation and a hand-run rollback.

Independently shippable; changes no behaviour.

---

## 3. Service layer — `internal/service/admin_queries.go` (reads live there; `Dashboard` is at `:39`)

### 3.1 Range

```go
const FinanceMaxSpan = 400 * 24 * time.Hour
// FinanceRange maps 24h|7d|30d|all onto [from, to). Zero `from` = unbounded.
func FinanceRange(name string, now time.Time) (from, to time.Time, err error) // unknown → invalid("range", …)
```

`Finance` validates `to` non-zero; if `from` non-zero, `from < to` and span ≤ `FinanceMaxSpan`, else `invalid(...)` → 400 via `fail` (`response.go:29`). Zero `from` → `EarliestSettlement`; none → `to − 24h` so the series still renders.

### 3.2 `AdminService.Finance(ctx, from, to) (*FinanceReport, error)`

Sequential independent queries, the `Dashboard` shape: Totals → ByContractType → ByDay → EdgeCells → Flows, each `if err != nil { return nil, err }`. Then in Go:

- Fold Totals into `Real`, `Promotional`, `DemoContracts` — demo's money fields never leave the service.
- Fold ByContractType into one `FinanceTypeRow` per type.
- **Edge weighting** — `WeightTheoreticalEdge(cells, pick) EdgeCheck` (exported, for the unit test): per cell `er, err := models.ExpectedReturnFor(type, sel)`; on error add the cell's count to `Unpriced` and skip — a selection the pricer no longer recognises must be visible, not dropped; accumulate `Σ stake·(−er)` and `Σ stake`. `Theoretical = weighted/Σstake`, `Realised = ΣhousePnL/Σstake`, `SampleSize = Σn`, `TooFewToRead = SampleSize < FinanceMinEdgeSample`. Run twice: all-funding and real-only pickers. Zero stakes → zeros and the flag.
- `FinanceMinEdgeSample = 1000`, with the justification in its comment: per-contract outcome SD ≈ one stake, so SE(edge) ≈ 1/√n; at n=1000 that is ±3.2 pp against edges of 0.5–4.5 pp — barely readable; below it is noise (§2.1, risk #5).

### 3.3 `FinanceReport` (JSON names)

```go
type EdgeCheck struct {
    SampleSize int `json:"sample_size"`; Realised, Theoretical decimal.Decimal `json:"realised","theoretical"`
    TooFewToRead bool `json:"too_few_to_read"`; MinSample int `json:"min_sample"`; Unpriced int `json:"unpriced"`
}
type FinanceTypeRow struct { ContractType string; Contracts int /* all funding */; Real, Promotional repository.FinanceBucket; Edge EdgeCheck }
type FinanceDayRow  struct { Day time.Time; Contracts int; RealStakes, RealHousePnL, PromotionalHousePnL decimal.Decimal; DemoContracts int }
type FinanceFlows   struct { Deposits int; DepositsUSD decimal.Decimal; Withdrawals int; WithdrawalsUSD, NetFloatUSD, CustomerLiabilityUSD decimal.Decimal }

type FinanceReport struct {
    Surface       string `json:"surface"`   // "digit_contracts" — the visible D2 label
    From, To, GeneratedAt time.Time
    Real, Promotional repository.FinanceBucket
    DemoContracts int
    Edge, EdgeReal EdgeCheck               // all-funding (game check) and real-only
    ByType []FinanceTypeRow; ByDay []FinanceDayRow; ByDayCapped bool
    Flows FinanceFlows
}
```
Slices initialised empty, never nil (existing convention).

### 3.4 Dashboard headline

`financeHeadline(ctx)` = Totals + EdgeCells over `[now−24h, now)` only — two queries bounded by the new index, not five (honours §4.2's warning about the dashboard). Adds `Finance *FinanceHeadline{HousePnL24h, Stakes24h; Contracts24h; Edge}` to `Dashboard`.

---

## 4. API

`internal/routes/routes.go:194` — `admin.GET("/finance", d.Handler.Admin.Finance)` directly after `/dashboard`, in the full-admin rate-limited read group.

`internal/handlers/admin_handler.go`, after `Dashboard` (`:131-138`), same shape:

```go
// GET /admin/finance?range=24h|7d|30d|all   (default 7d)
func (h *AdminHandler) Finance(c *gin.Context) {
    from, to, err := service.FinanceRange(c.DefaultQuery("range", "7d"), time.Now())
    if err != nil { fail(c, err); return }     // ValidationError → 400, field "range"
    report, err := h.admin.Finance(c.Request.Context(), from, to)
    if err != nil { fail(c, err); return }
    c.JSON(http.StatusOK, report)
}
```
No pagination — the by-day table is bounded by the cap.

---

## 5. Frontend — `web/src/features/admin/`

| Change | File | Shape |
|---|---|---|
| Types | `adminTypes.ts` | `FinanceBucket`, `EdgeCheck`, `FinanceTypeRow`, `FinanceDayRow`, `FinanceFlows`, `FinanceReport`, `FinanceRange = '24h'\|'7d'\|'30d'\|'all'`; decimals as `string` like `Dashboard`; `finance: FinanceHeadline \| null` on `Dashboard`. |
| API + keys | `adminApi.ts:115`, `:212` | `finance: (range) => request<FinanceReport>(\`/admin/finance${qs({ range })}\`)`; `adminKeys.finance: (range) => ['admin','finance',range] as const`. |
| Tile lift | new `components/Tile.tsx`; `screens/Dashboard.tsx:17-35` | Move `Tile` verbatim, export it, import in Dashboard. Pure move. |
| Screen | new `screens/Finance.tsx` → `AdminFinance` | Below. |
| Nav | `AdminShell.tsx:74-82` | `{ to: '/admin/finance', label: 'Finance', icon: Landmark }` after Transactions. |
| Route | `AdminApp.tsx:38-54` | `<Route path="finance" element={<AdminFinance />} />`. |
| Dashboard tiles | `screens/Dashboard.tsx:257-298` | "House P&L, 24h" (`signedMoney`, sub: staked) and "Edge, 24h" (`edgeReadout`, sub: n) — both `<Link to="/admin/finance?range=24h">`. Labelled 24h, not "today", so tile and screen agree. |

**`Finance.tsx`, top to bottom:** `AdminPageHeader` (exists — `KYCQueue.tsx`, `BotRuns.tsx`) with the exported `FINANCE_SCOPE_NOTE` → `FilterChips<FinanceRange>` (`components/SearchInput.tsx:63`), URL-synced with the `update` helper pattern from `Transactions.tsx:67-75`, `useQuery(adminKeys.finance(range))` → five tiles: House P&L (real, `signedMoney`), Volume staked (real; wins/losses/voids sub), Promotional cost, Edge (`edgeReadout`, sub `edgeNoisePP`), Net float (sub: customer liability — **never the word "profit"**, §2.4); demo as a small count line → `HousePnLChart`, extending `DepositChart` (`Dashboard.tsx:83-125`): same flex column-per-bucket with a centre baseline, positive up in `bg-profit`, negative down in `bg-accent`, `title` tooltip via `dayLabel` + `signedMoney`; no recharts (§3.2) → `DataTable<FinanceTypeRow>` (Type, Contracts, Real staked, Real house P&L, Realised, Theoretical, n — flagged when too few) → `DataTable<FinanceDayRow>` with a note when `by_day_capped`.

Pure exported helpers (the `killConsequence` pattern): `rangeLabel`, `edgeReadout(edge)` (withholds the comparison sentence and names n/min when flagged), `edgeNoisePP(n)` (`±(100/√n).toFixed(1)` pp; `—` at 0), `edgePercent(value)` via `signedPercent` (`lib/format.ts:45`), `contractTypeLabel`.

---

## 6. Verification

Not a money-moving path: no mutation matrix. Aggregates get integration tests against seeded outcomes.

**Integration — new `internal/service/finance_integration_test.go`** (`//go:build integration`; `newHarness`, `h.digits.Start`, deterministic settlement by injected price's last digit via `h.feed.InjectTickAt`; voids by backdating `void_after` then `h.digits.SweepForTest`, as `TestDigitAutoVoid` does). Funding the three buckets: **real** — `h.verifiedWithBalance(usd)` (`withdrawal_guards_test.go:22`; currently the only harness path to a real balance, note it, do not add another); **promotional** — `h.newUser(1000)`, a *real* account holding bonus; **demo** — `h.newDemoAccount`.

| Test | Asserts |
|---|---|
| `TestFinanceHousePnLMatchesLedger` | The cross-check: `Real.HousePnL + Promotional.HousePnL == −SUM(t.amount)` over `transactions t JOIN digit_contracts dc ON dc.id::text = t.reference WHERE t.type IN ('digit_open','digit_settle')` for settled contracts in range on real accounts, run directly via `h.pool`. Exact by construction: open = −stake; settle = +credit / 0 / +stake for win / loss / void. Seeds one contract at stake `3.33` so `WinCredit`'s `.Round(2)` bites — a `payout_ratio`-derived figure fails here (risk #2). Ends with `h.assertReconciled` per account. |
| `TestFinanceSeededOutcomesAndFundingSplit` | On each of the three accounts: 3 wins, 2 losses, 1 void at stake 10 on even/odd (ratio 0.9520 → win pnl +9.52). Per bucket: contracts 6, wins 3, losses 2, voids 1, stakes 50, house_pnl **−8.56**, realised **−0.1712**. `DemoContracts == 6` with no demo money field; `Edge.SampleSize == 15`, `EdgeReal.SampleSize == 5`, `Theoretical == 0.024`, `TooFewToRead`. An open contract and one settled outside `[from,to)` are excluded. |
| `TestFinanceByDayHasNoGaps` | A 7-day range yields exactly 7 rows, empty days zero-valued; by-day real house P&L sums to `Real.HousePnL`. |
| `TestFinanceFlowsUseBalanceAffectingRows` | A pending withdrawal (`h.withdraw`) counts; a pending deposit inserted directly does not; `NetFloatUSD == deposits − withdrawals`; `CustomerLiabilityUSD == sum(real_balance)`. Risk #3. |
| `TestFinanceBotSplitDoesNotDoubleCount` | `newBotDriver` + `resolve(run, "win","lose")` plus one manual contract: `Real.Contracts == 3`, `BotContracts == 2`, `BotHousePnL == −bot_runs.realised_pnl`, `HousePnL == BotHousePnL + manual`. Risk #4. |
| `TestFinanceRangeValidation` | Reversed range and a 401-day span are `*ValidationError`. |

**Unit — new `internal/service/finance_test.go`** (untagged): `TestWeightedTheoreticalEdge` — `{even/odd, 100}` + `{matches 7, 300}` → `(100·0.024 + 300·0.045)/400` exactly; an unparseable selection increments `Unpriced`; zero cells → zeros and the flag. `TestFinanceRange` — each enum's `[from,to)`; `all` gives zero `from`; unknown → `ValidationError` on `range`.

**Web — new `screens/Finance.test.tsx`** (`Dashboard.test.tsx` pattern, pure functions): `edgeReadout` withholds the comparison when flagged; `edgeNoisePP(1000) == '±3.2 pp'`, `edgeNoisePP(0) == '—'`; `FINANCE_SCOPE_NOTE` matches `/Digit contracts only/`; `rangeLabel` covers all four.

**Builds:** `go build ./... && go vet ./... && go test ./internal/...`; `go test -tags integration ./internal/service/ -run 'Finance|Dashboard'`; in `web/`: `npm run typecheck && npm run lint && npx vitest run && npm run build`. **Optional** e2e smoke in `admin.spec.ts`: `loginAdmin`, `goto('/admin/finance')`, heading and scope note visible.

---

## 7. Sequencing

| # | Unit | Files | Shippable alone? |
|---|---|---|---|
| 1 | Migration | `migrations/0011_…{up,down}.sql` | Yes — no behaviour change |
| 2 | Repo + integration tests | `finance_repo.go`, `store.go`, `finance_integration_test.go` | Yes — dead until 3, but the SQL review is the one that matters |
| 3 | Service + unit test | `admin_queries.go`, `finance_test.go` | Yes |
| 4 | Handler + route | `admin_handler.go`, `routes.go` | Yes — API live, curl-verifiable |
| 5 | Types + API + keys | `adminTypes.ts`, `adminApi.ts` | Yes (unused exports) |
| 6 | Tile lift | `components/Tile.tsx`, `Dashboard.tsx` | Yes — independent refactor, could land first |
| 7 | Finance screen + tests | `Finance.tsx`, `Finance.test.tsx` | With 8 |
| 8 | Nav + route | `AdminShell.tsx`, `AdminApp.tsx` | With 7 — the user-visible landing |
| 9 | Dashboard tiles | `admin_queries.go` (`financeHeadline`), `adminTypes.ts`, `Dashboard.tsx` | Yes — last and independently revertible; the only piece that touches every dashboard load |

Natural PR grouping: (1) · (2+3+4) · (5+6+7+8) · (9).

---

## 8. Risks carried forward (investigation §4.3) → explicit guards

| # | Risk | Guard |
|---|---|---|
| 1 | Omitting the funding-balance join | `financeScope` is the only `FROM digit_contracts` in `finance_repo.go`; the header forbids another. `TestFinanceSeededOutcomesAndFundingSplit` fails if promotional money lands in `real`. |
| 2 | Deriving payouts from `payout_ratio` | `HousePnL` field comment: "from `pnl` only — investigation §1.3"; `payout_ratio` never appears in the file. `TestFinanceHousePnLMatchesLedger` seeds stake 3.33 so rounding would expose it. |
| 3 | Not using `balanceAffectingRows` | `Flows` concatenates the `reconciliation.go:35` const — compile-time reuse. `TestFinanceFlowsUseBalanceAffectingRows` asserts a pending withdrawal counts and a pending deposit does not. |
| 4 | Double-counting bot P&L | Bot split is `bot_run_id IS NOT NULL` inside the same scope; `bot_runs` never read by finance code (header comment). `TestFinanceBotSplitDoesNotDoubleCount` asserts total = bot + manual and bot = `−realised_pnl`. |
| 5 | Reading realised edge at small n | `FinanceMinEdgeSample` with the SE justification; `TooFewToRead` computed server-side; `edgeReadout` test asserts the comparison sentence is withheld; the tile always shows n and `edgeNoisePP`. |
