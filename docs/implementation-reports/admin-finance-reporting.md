# Admin Finance / house-edge screen — implementation report

**Plan:** [`docs/implementation-plans/admin-finance-reporting.md`](../implementation-plans/admin-finance-reporting.md) · **Investigation:** [`financial-reporting-investigation.md`](../investigation-reports/financial-reporting-investigation.md)
**Date:** 26 September 2026 · **Branch:** `feat/admin-finance-reporting` off `master` @ `8430ae7`
**Status:** complete. Four commits, 20 files, +2455/−21.

---

## 1. Summary

All four PR groups from §7 landed, in order. `GET /admin/finance?range=24h|7d|30d|all`
answers what was traded, what the house won or lost, and whether the realised edge matches
the payout table; `/admin/finance` renders it; two dashboard tiles link through.

**The feature's whole difficulty is one join, and the code is built around defending it.**
`digit_contracts` carries no `balance_type` — whether a stake was real or bonus money lives
only on the `digit_open` ledger row — so every money figure is split three ways through one
`CASE`, and the repository file states as rules the three mistakes that would silently
produce a wrong number. Two of those rules were checked by mutation rather than asserted.

**Nothing was descoped.** Everything §1 listed as v1 is present; everything it listed as
deferred remains deferred.

| Group | Commit | What |
|---|---|---|
| 1 | `6af9838` | Migration 0011: partial index on `settled_at` |
| 2 | `0a1b1a9` | `finance_repo.go`, `admin_finance.go`, handler, route, 6 integration + 6 unit tests |
| 3 | `9f11223` | Types, API, `Tile` lift, `Finance.tsx`, 10 unit tests, nav, route |
| 4 | `772a3dc` | `financeHeadline` + two dashboard tiles |

---

## 2. What was built

### Group 1 — migration (`6af9838`)

[`migrations/0011_digit_contracts_settled_index.up.sql`](../../migrations/0011_digit_contracts_settled_index.up.sql)
and `.down.sql`. Partial index on `(settled_at DESC) WHERE settled_at IS NOT NULL`, matching
the style of the two open-contract partial indexes beside it. Plain `CREATE INDEX` with the
comment explaining that `repository.Migrate` wraps each file in a transaction so
`CONCURRENTLY` cannot be used, and that it should be built by hand at scale.

Verified by applying all eleven migrations in order to a scratch database and reading back
`pg_indexes`; the down file drops it.

### Group 2 — data, service, API (`0a1b1a9`)

[`internal/repository/finance_repo.go`](../../internal/repository/finance_repo.go) — six
aggregates (`Totals`, `ByContractType`, `ByDay`, `EdgeCells`, `Flows`, `EarliestSettlement`),
all built on one `financeScope` fragment and one `fundingBucket` CASE. Registered in
`newStore` (`store.go`), not just `NewStore`, so it exists inside `WithTx` too.

[`internal/service/admin_finance.go`](../../internal/service/admin_finance.go) —
`FinanceRange`, `Finance`, `WeightTheoreticalEdge`, `financeHeadline`, and the report types.

[`internal/handlers/admin_handler.go`](../../internal/handlers/admin_handler.go) `Finance`,
[`internal/routes/routes.go`](../../internal/routes/routes.go) `admin.GET("/finance", …)`.

### Group 3 — the screen (`9f11223`)

`adminTypes.ts` (7 interfaces + `FinanceRange`), `adminApi.ts` (`finance()`, `adminKeys.finance`),
`components/Tile.tsx` (lifted verbatim from `Dashboard.tsx`),
[`screens/Finance.tsx`](../../web/src/features/admin/screens/Finance.tsx), `Finance.test.tsx`,
plus the `AdminShell` nav entry and the `AdminApp` route.

### Group 4 — dashboard tiles (`772a3dc`)

`financeHeadline` wired into `AdminService.Dashboard`, two tiles in `Dashboard.tsx`.

---

## 3. Deviations from the plan

Four, none of them scope changes.

**3.1 The service file is new, not an addition to `admin_queries.go`.** The plan said
"`admin_queries.go` (or a new `admin_finance.go` beside it)" and left it open; the finance
code is ~330 lines and `admin_queries.go` is already 274, so it went in its own file. Only
the `Dashboard` struct field and the `financeHeadline` call touch `admin_queries.go`.

**3.2 `EarliestSettlement` is a second `FROM digit_contracts`, and the rule says so.** The
plan's §8 guard #1 reads "`financeScope` is the only `FROM digit_contracts` in
`finance_repo.go`". That is not achievable: `EarliestSettlement` has no range to scope by. I
wrote the rule that way first, noticed the file contradicted its own header, and made the
rule exact instead — the guard now says every query that reads **money** uses `financeScope`,
and names `EarliestSettlement` as the one exception that returns a timestamp and therefore
*cannot* express the bug. A comment that is almost true is worse than none.

**3.3 `TestFinanceBotSplitDoesNotDoubleCount` asserts an invariant, not the literal counts.**
The plan specified `Real.Contracts == 3` and `BotContracts == 2`. A bot run keeps placing
after `resolve()` returns, so those constants are a timing race (see §5.2). The test now
stops the run, drains it, reads `contracts_placed` back, and asserts
`BotContracts == run.contracts_placed` and `Contracts == BotContracts + 1` — the same
property, without the flake. The `BotHousePnL == −realised_pnl` and manual `== −9.52`
assertions are unchanged.

**3.4 The account is promotional, not real, in that one test.** `newBotDriver`'s
`startEvenOddRun` funds via `h.newUser(…)`, which credits *bonus*, so the bot's contracts
land in the promotional bucket. The test asserts against `report.Promotional`. The property
under test — bot vs manual within one bucket — is unaffected.

Everything else is as specified, including every figure in §6's seeded arithmetic.

---

## 4. Risk guards — each one, cited

All five §8 guards are in the diff. The two whose guard is "this test catches it" were
verified by mutation rather than assumed.

| # | Risk | Guard, in the code | Verified |
|---|---|---|---|
| 1 | Omitting the funding-balance join | Rule at [`finance_repo.go:17-24`](../../internal/repository/finance_repo.go); `financeScope` at `:53` carries the join; `fundingBucket` at `:72` | **Mutation:** replaced the CASE with a literal `'real'`. `TestFinanceSeededOutcomesAndFundingSplit` failed — `contracts=18 wins=9 losses=6 voids=3, want 6/3/2/1` — all three accounts collapsed into `real`, which is promotional money counted as earnings. Restored, hash-verified. |
| 2 | Deriving payouts from `payout_ratio` | Rule at `:25-27`; `HousePnL` field comment "from the `pnl` column only" at `:86`. `payout_ratio` appears **only in those two comments** and in no SQL — grepped | `TestFinanceHousePnLMatchesLedger` seeds stake `3.33`: `3.33 × 1.9520 = 6.50016` stored as `6.50`, so a ratio-derived pnl would be `3.17016` against the ledger's `3.17` |
| 3 | Not using `balanceAffectingRows` | `Flows` concatenates the `reconciliation.go:35` const at [`:330`](../../internal/repository/finance_repo.go) — compile-time reuse, not a copy | **Mutation:** swapped it for `status = 'completed'`. `TestFinanceFlowsUseBalanceAffectingRows` failed — `withdrawals = 0 / 0, want 1 / 100` — reproducing the exact historical bug that comment records. Restored, hash-verified. |
| 4 | Double-counting bot P&L | Rule at `:28-31`; the split is `dc.bot_run_id IS NOT NULL` at `:101-102`, inside `financeScope`. `bot_runs` is never read by finance code — grepped | `TestFinanceBotSplitDoesNotDoubleCount` asserts `BotHousePnL == −bot_runs.realised_pnl` and `total − bot == exactly one manual contract` |
| 5 | Reading realised edge at small n | `FinanceMinEdgeSample = 1000` with the SE justification at [`admin_finance.go:42`](../../internal/service/admin_finance.go); `TooFewToRead` computed server-side at `:300`; [`edgeReadout`](../../web/src/features/admin/screens/Finance.tsx) at `:60` withholds the comparison; `edgeNoisePP` at `:47` | Unit-tested both sides; seen live — the screen showed "—" and "14 of 1000 contracts" rather than a −25% edge |

**The `settled_at` tripwire** the brief required is at
[`finance_repo.go:40-46`](../../internal/repository/finance_repo.go), verbatim as specified,
directly above `financeScope`.

---

## 5. Test results

### 5.1 What each test verified

**Integration** — [`internal/service/finance_integration_test.go`](../../internal/service/finance_integration_test.go), 6 tests, all pass:

| Test | What it actually checked |
|---|---|
| `TestFinanceHousePnLMatchesLedger` | House P&L computed two independent ways — through the aggregate, and by summing the `digit_open`/`digit_settle` ledger rows — are **equal**, and equal the hand figure **−20.29** (two accounts at −8.56 plus the 3.33 win's −3.17). Both accounts reconcile. |
| `TestFinanceSeededOutcomesAndFundingSplit` | Per bucket, real and promotional: **6 contracts, 3W/2L/1V, 50 staked, −8.56 house P&L**. Stakes are 50 not 60 — the void is refunded, not volume. `demo_contracts == 6` with no demo money field. `Edge.SampleSize == 15`, `EdgeReal.SampleSize == 5`, `Theoretical == 0.024`, `EdgeReal.Realised == −0.1712`, `TooFewToRead`, `Unpriced == 0`. An open contract and one settled 10 days earlier are both excluded. |
| `TestFinanceByDayHasNoGaps` | A 7-day range yields ≥7 rows with **at least one zero-valued empty day**, not a gap, and the by-day real house P&L **sums to the total**. |
| `TestFinanceFlowsUseBalanceAffectingRows` | A **pending withdrawal counts** (1 / 100), a **pending deposit does not** (deposits stay 1 / 500), net float 400, liability equals `sum(real_balance)`. |
| `TestFinanceBotSplitDoesNotDoubleCount` | `BotContracts == run.contracts_placed`, `Contracts == bot + 1`, `BotHousePnL == −realised_pnl`, manual `== −9.52`. |
| `TestFinanceRangeValidation` | Reversed range and a 401-day span are both `*ValidationError`; unbounded is accepted. |

**Unit** — [`internal/service/finance_test.go`](../../internal/service/finance_test.go), 6 tests, all pass. Beyond the plan's list I added `TestWeightedEdgeIsWeightedByStakeNotCount`, because the plan's own example — `(100·0.024 + 300·0.045)/400` — gives the *same answer* count-weighted, so it could not have caught the wrong weighting. The added test uses 100 × $1 against 1 × $100: stake-weighted **0.024980**, count-weighted would be 0.00540. Also: `Unpriced` counts both a pricer rejection and malformed JSON (8 total) and keeps them out of the denominator; the two pickers see different populations.

**Web** — [`Finance.test.tsx`](../../web/src/features/admin/screens/Finance.test.tsx), 10 tests, all pass: the readout withholds the comparison and contains no `%` when flagged; `edgeNoisePP(1000) == '±3.2 pp'`, `(10000) == '±1.0 pp'`, `(0) == '—'`; `edgePercent('-0.1712') == '-17.12%'`; the scope note matches `/Digit contracts only/` and `/CFD/`; all four range labels; an unknown contract type passes through rather than blanking.

### 5.2 End-to-end, in a browser

Against a fresh database with the API in digit mode, 14 contracts placed through the real
HTTP path on a demo account:

- `demo_contracts: 14`, `real.contracts: 0`, `promotional.contracts: 0` — **the demo exclusion works on live data**, not only in tests.
- Edge: `n=14, realised −0.254857, theoretical 0.024, too_few_to_read true`. Checked by hand: house P&L `−(85.68 − 50) = −35.68`, stakes `140`, `−35.68/140 = −0.254857`. Exact.
- `?range=nonsense` → **HTTP 422** with `field: "range"`.
- The screen renders: scope note in the header, five tiles, the edge tile showing **"—" / "14 of 1000 contracts"**, the by-day table with a row per day including empty ones, `Even / Odd` at theoretical `+2.40%` with realised withheld. The range chip writes `?range=24h` and refetches.
- `/admin/finance` served **5× 200, 2× 422** (the bad-range probes), **zero 401s**.

### 5.3 The required commands

| Command | Result |
|---|---|
| `go build ./...` | **exit 0** |
| `go vet ./...` | **exit 0** |
| `go test ./internal/...` | **exit 0** — config, models, service, storage all ok |
| `go test -tags integration ./internal/service/ -run 'Finance\|Dashboard'` | **exit 0**, 5.3s |
| `npm run typecheck` | **exit 0** |
| `npm run lint` | **exit 0** |
| `npx vitest run` | **exit 0** — **155 passed, 16 files** (was 145/15) |
| `npm run build` | **exit 0**, built in 1.77s |

`npm run lint` reports 5 `only-export-components` warnings in `Finance.tsx` — the fast-refresh
rule firing because pure helpers are exported from a component file. That is the existing
pattern in this codebase (`Dashboard.tsx` and `BotRuns.tsx` carry the same warning, and their
tests import those exports); lint exits 0.

---

## 6. Facts the plan or investigation did not anticipate

**6.1 The tick used to open a contract settles the previous one.** `financeSeeder.open`
injects a tick at `100.10`, which at pip size 4 is `100.1000` — last digit **0**, even, so it
**wins** a pending Even bet. My first draft opened a deliberately-unsettled contract before
the one that was meant to be stale, and the tick from the next `open` settled it: the test
failed with `contracts=7 wins=4`. Seeding order is load-bearing in any digit test that wants
something left open, and the test now says so in a comment.

**6.2 A bot run keeps placing after `resolve()`.** `resolve` plays named outcomes but does not
stop the loop, so a third contract appeared before the "manual" one and the count was 4, not
3. The run must be stopped and drained first. This is why deviation 3.3 exists.

**6.3 `ValidationError` maps to 422, not 400.** The plan's handler sketch says "400 via
`fail`"; `response.go:32` maps it to `http.StatusUnprocessableEntity`. The behaviour is
correct and unchanged — only the plan's annotation was wrong.

**6.4 Days bucket in UTC, and the browser may disagree about which day that is.**
`date_trunc('day', settled_at)` runs in the database session timezone, which is **UTC**
(verified: `SHOW timezone` → UTC). Contracts settled at 21:16 UTC bucket to that UTC day; a
browser at +03:00, where the local clock has already passed midnight, labels that bucket
"Yesterday". Consistent and not a defect — the existing `DepositsByHour` chart has the same
property — but it is the timezone subtlety the money-mechanics investigation flagged, now
visible on a second screen. Worth a deliberate decision about reporting timezone before
anyone reconciles these figures against a bank statement.

**6.5 `newUser` funds bonus, so bot-driver accounts are promotional.** Noted at 3.4. The only
harness path to a real balance remains `verifiedWithBalance`, as the plan said; I did not add
another.

---

## 7. Left unfinished or deferred

Nothing beyond §1's deferred list, which is unchanged: CFD `trades` (D2), rollup tables and
caching, recharts, exports, scheduled reports, per-user profitability, and custom `from`/`to`
params.

Two things worth naming that are not scope gaps:

- **The optional e2e smoke was not added.** §6 marked it optional and I verified the screen
  directly in a browser instead (§5.2), which covers the same ground — heading, scope note,
  data rendering — without adding a test to a suite that has to log into the admin console.
- **Every figure this screen shows today is zero or demo.** `withdrawals_enabled` and
  `bot_real_money_enabled` are both false and no production user is verified, so the real and
  promotional buckets will read zero until that changes. That is the investigation's own
  argument for building it now — it can be made correct against demo data before there is real
  money to get wrong — and §5.2 confirms it behaves correctly with only demo volume.
