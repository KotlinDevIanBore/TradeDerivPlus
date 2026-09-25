# STK reconciliation · CP3 — the pass

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** plan §2.4, §2.5, §2.9 — `Start`, `Reconcile`, the backoff ladder, give-up, the two
metrics and the sampler, and the `main.go` wiring. This is the checkpoint where the server
first writes money on the strength of a query, so §8 group D and its mutation matrix are most of
what follows.

---

## 1. What was built

### The pass — `internal/service/mpesa_service.go`

`Start(ctx)` runs one pass, then one per `MPESA_RECONCILE_EVERY`, until the root context is
cancelled. `Reconcile(ctx)` is one pass: `ListPending(now, 100)`, then per row —

| Row | What happens | Counted as |
|---|---|---|
| older than `GIVE_UP` | `MarkGivenUp` (schedule → `NULL`), one WARN line; **not queried** | `gave_up` |
| querier returns `error` | WARN; **nothing written** — the row keeps its schedule, is still due, and the next pass asks again | `error` |
| `Succeeded`, and `settle` won | credited through `settle(…, 0, desc, "", "query")` — the callback's function, the callback's guards, the callback's T→W order | `succeeded` |
| `Succeeded`, and `settle` lost | a callback landed between our read and our write; it credited, we did not | `already_settled` |
| `Failed` | **not written** (D2); INFO line naming the code; backoff advanced | `failed_ignored` |
| `Unresolved` | backoff advanced | `unresolved` |

Backoff after the n-th answered query: **3m, 10m, 30m, then 1h** — `reconcileBackoff`, marked
provisional in the code as the rest of the schedule is. `advance` writes `attempts+1` and
`now + backoffAfter(attempts+1)` through `MarkQueried`, which is guarded on `pending`.

`settle` is unchanged from CP0. In particular it still refuses with no result handler, and it
still refuses *before* `UpdateResult` — which is what makes the `main.go` order a safety
property and not a preference: `mpesa.Start(ctx)` sits after `NewWalletService` and beside
`digits.Start`, with a comment saying why.

### Metrics — `internal/observability`

`mpesa_reconcile_total{outcome}`, one increment per row examined, fed through
`MpesaService.OnReconcile` the way `deposits_total` is fed through `OnDepositSettled`; every
outcome series is initialised at zero at boot so a dashboard sees the shape before anything has
happened. `mpesa_deposits_pending_stale`, a gauge, from a new `Sampler.StaleDeposits` →
`Mpesa.CountPendingOlderThan(MIN_AGE)`; `NewStoreSampler` takes the min-age. The help text on
the counter carries the reading from the plan: stale rising with `error` is a Daraja outage,
with `unresolved` is Daraja saying "processing" for a long time, with neither is the pass not
running.

### Six things beyond the letter of the plan

1. **The boot pass runs inside the goroutine**, not synchronously as `DigitService.Start`'s
   sweep does. A sweep is a database statement; a pass is up to a hundred Daraja round trips, and
   a boot that blocks on them is a boot that fails its health check. `Start` returns at once.
2. **Give-up takes the row off the schedule** (`MarkGivenUp`, `next_query_at = NULL`) rather
   than the plan's `continue`. With `continue`, a given-up row is re-listed and re-counted on
   every pass — and worse, `ListPending` orders oldest-due first with a bound of 100, so
   given-up rows are exactly the ones that would fill the batch and starve the live rows behind
   them. `NULL` was defined in CP2 as "never scheduled"; it now means "not scheduled — never, or
   given up". Both are invisible to the pass and both stay in the stale gauge, which keys on
   `created_at`. The console can tell them apart by `reconcile_attempts` and age.
3. **`already_settled`**, a sixth outcome. The plan's five had nowhere honest to put "Daraja
   said paid and the callback beat us to it". Counting it as `succeeded` would say the pass
   credited when it did not.
4. **Ticks do not stack.** `tick` takes a `TryLock`; a pass still running when the next tick
   fires is skipped over with a WARN, not run alongside. `Reconcile` itself does not lock, so
   tests can run passes concurrently on purpose — which `TestQueryAndLateCallbackCreditExactlyOnce`
   does.
5. **Each query has a 15s timeout** of its own, under the client's 30s, so a hung Daraja holds a
   pass for a bounded time per row.
6. **`Unresolved` is not logged per row.** It is the common case — one line per pending deposit
   per pass would be most of the log. It is in the counter. `error` and `gave_up` are WARN,
   `succeeded` and `failed_ignored` INFO.

---

## 2. Tests — `internal/service/mpesa_reconcile_test.go` (group D)

`stubQuerier` answers per checkout id, counts calls, and can run a function *during* a query —
which is how the two race tests land a callback inside the query's window deterministically
rather than hoping eight goroutines find it. `outcomes` captures `OnReconcile`. `h.dueDeposit`
makes one deposit with a zero min-age so its first query is due at once; the harness has no
floor, on purpose, and the test names say so.

| Test | Pins |
|---|---|
| `TestQuerySettledDepositCreditsOnce` | balance 100, `completed`, `settled_by = query`, **no `receipt` key**, mpesa row `completed`/`0`, one `succeeded`; a second pass asks nothing |
| `TestQueryAndLateCallbackCreditExactlyOnce` | five deposits × eight callers released together, four passes and four callbacks each: balance is exactly `100 × n`, `assertReconciled` every time |
| `TestCallbackThenQueryIsANoop` | the callback lands *during* the query; the pass's `settle` loses; `settled_by` and the receipt are the callback's; one `already_settled` |
| `TestCallbackDuringAnUnresolvedQueryLeavesTheRowAlone` | same window, `Unresolved` answer: `MarkQueried` after the callback touches nothing — attempts 0, schedule unchanged |
| `TestUnresolvedAdvancesBackoffAndNothingElse` | five rungs: attempts 1…5, next query 3m/10m/30m/1h/1h out (±2s); a pass inside each backoff asks nothing; balance 0, row and ledger pending throughout |
| `TestQuerierErrorIsNotAnAttempt` | two passes, two queries, attempts still 0, schedule unchanged, two `error`; when Daraja answers the same row settles |
| `TestQueryFailedIsNotWritten` | `1032` from a query: row pending, `result_code` NULL, attempts 1, one `failed_ignored`; a later callback still credits |
| `TestGiveUpRowIsNotQueried` | 25h old: zero queries, schedule `NULL`, exactly one `gave_up` across two passes, row and ledger pending, stale gauge 1 |
| `TestBootPassSettlesAStrandedDeposit` | fresh `MpesaService` + `WalletService` over the same store, `Start`, credited once within 5s |
| `TestPassWithoutAResultHandlerLeavesTheRowForOneThatHasOne` | a service with no wallet: one `error`, row still pending, guard intact; the wired service then settles it |
| `TestPassNeverTouchesAMockDeposit` | mock querier, production min-age: a fresh mock deposit is not reached; `simulateCallback` settles it (either way) with attempts still 0 |

Plus `TestReconcileBackoffLadder` (unit, `package service`).

All eleven passed on their first run. Each ends in `assertReconciled` except the give-up test,
whose ledger is asserted pending directly.

### The mutation matrix

Eight mutations. Each was applied by a script that **asserts exactly one match** for the text it
replaces and prints `APPLIED` before the test runs; each restore was checked byte-for-byte
against the backup. The three from `mpesa-double-credit-fix.md` §3 first, on
`TestQueryAndLateCallbackCreditExactlyOnce`:

| # | Mutation | Expected | Result |
|---|---|---|---|
| M1 | `mpesa_repo` `UpdateResult` guard removed only | pass — the transaction guard covers | **pass** |
| M2 | `transaction_repo` `UpdateStatusIfPending` guard removed only | pass — the mpesa guard covers | **pass** |
| M3 | **both removed** | fail, double credit | **fail**: `after 8 concurrent passes and callbacks on deposit 2 the balance is 300, want 200` |

M1 and M2 passing is the point, not a gap: either guard alone holds the line, and a check that
removed one would have certified a fix that was half there. M3 is the pass and the callback both
crediting one deposit — the outcome the plan's "biggest risk" names.

| # | Mutation | Test | Result |
|---|---|---|---|
| M4 | `MarkQueried` guard removed | `TestCallbackDuringAnUnresolvedQueryLeavesTheRowAlone` | **fail**: `the pass wrote to a row the callback had settled: attempts 1 next 01:53:34 (was 01:50:34)` |
| M5 | a querier `error` calls `advance` | `TestQuerierErrorIsNotAnAttempt` | **fail**: `Daraja asked 1 times across two passes, want 2 — an error must leave the row due` |
| M6 | `Failed` is written through `settle` | `TestQueryFailedIsNotWritten` | **fail**: `mpesa row is "failed" after a failed query, want pending — a query wrote Failed` |
| M7 | the horizon check disabled | `TestGiveUpRowIsNotQueried` | **fail**: `Daraja asked 1 times about a row past the horizon, want 0` |
| M8 | `settle` skips silently with no handler, after winning the guard | `TestPassWithoutAResultHandler…` | **fail**: `outcomes [succeeded], want one error` |

**M8 had to be run twice.** The first version removed the refusal block whole, orphaned the
`errors` import, and produced `FAIL … [build failed]` — a red line that is not a test failure
and would have been read as one by a grep for `FAIL`. The driver's pattern included `build
failed` precisely so this would be visible; the mutation was rewritten to keep the import in use,
`go vet` was run on the mutated tree before the test, and the failure above is from that run.
CP2's lesson was a mutation that never applied; this is its twin, a mutation that applied and
did not compile. Both look like evidence and neither is.

---

## 3. Verification

| Command | Result |
|---|---|
| `go build ./...`, `gofmt -l`, `go vet` (both tag sets) | clean |
| `go test ./...` | ok |
| the eleven group D tests | pass, first run |
| `go test -tags integration ./internal/service/ -count=1` | **ok, 49.3s** — whole suite |
| M1–M8 | as tabled; restored byte-identical after each |
| `python3 scripts/smoke.py` | **ALL CHECKS PASSED** |
| `python3 scripts/bot_check.py` | **AUTOMATED RUN VERIFIED** |
| `npx playwright test --project=mobile --project=desktop` | **28 passed, 3 skipped, 3.8m** — CP0's count exactly |
| `npx playwright test --project=digit-mobile --project=digit-desktop` | **20 passed, 2 skipped, 2.6m** — CP0's count exactly |

### The browser suites took three runs, and none of the failures was the pass

The first run of both pairs failed broadly, and it is recorded here because a green table with
no history would hide what it took. Three things were wrong, all in the local environment:

1. **`digit_contracts_enabled` was `true`** in the local database, and the CFD projects need it
   `false` — the CFD buy button does not exist on the digit surface, and every early failure was
   that locator. CP0 met the same thing and flipped the flag for its run; this checkpoint's first
   run did not.
2. **That run's admin spec killed the bot and failed before re-enabling it** (audit log:
   `bot_kill` at 23:01 UTC with no `bot_enable` after it), so the digit pair that followed failed
   its own fail-fast guard with *automated trading is off for this API*. The flag was restored to
   `true` by hand, as found.
3. **The local API runs the production auth rate limit** (`3s`/burst 10); CI runs both e2e jobs
   at `200ms`/25. With the flag fixed, a second CFD run still lost five tests, three of them
   sessions bouncing to `/login`. The API log for that run shows nine `auth/refresh` 429s, and a
   429 on refresh logs the user out — the item already on the deferred list. The third run, under
   CI's two `AUTH_RATE_*` values and nothing else changed, is the row in the table: zero 429s.

Two of my own commands also died at their first line with exit 144 and no output, because a
`pkill -f "playwright test"` matched the shell running the script that contained it. Noted for
the same reason as the mutation lessons: a verification step that kills itself looks, from the
outside, exactly like one that ran.

None of the three touches the reconciler. The pass ran throughout every attempt, on a 5-second
schedule for the first and the default for the rest, and the API log has no ERROR line and no
skipped tick across all of them.

### The exit criterion: a mock-mode run

The API rebuilt and started with `MPESA_RECONCILE_EVERY=5s` (boot log: `mpesa_reconcile_every=5s
mpesa_reconcile_min_age=3m0s mpesa_reconcile_give_up=24h0m0s`). All six `mpesa_reconcile_total`
series present at 0 from boot. A pending row was inserted with `created_at` 5 minutes ago and
`next_query_at = now()`:

| Reading | `unresolved` | `mpesa_deposits_pending_stale` | `deposits_total` |
|---|---|---|---|
| at boot, no pending rows | 0 | 0 | absent |
| ~13s after the insert (two or three ticks) | **1** | 0 † | absent |
| after `smoke.py` | 1 | 0 † | `completed` 1 |
| after the gauge's next minute sample | 1 | **1** | `completed` 1 |

The row after the first tick: `reconcile_attempts = 1`, `next_query_at` more than two minutes
out — the 3m first rung. That is why `unresolved` reads 1 and not 3: the second and third ticks
listed nothing, because the row was inside its backoff. The counter advances at the rate the
schedule allows, which is the behaviour, not a shortfall. `deposits_total` was moved only by
smoke's own callback-settled deposit; the pass moved it by nothing. Zero `skipping this tick`
lines, zero ERROR lines.

† The gauge is sampled once a minute by `Collect`, and its boot sample preceded the insert.

Both browser rows were produced against an API started with CI's two `AUTH_RATE_*` values and
the flags in the state each pair needs; see the section above for why. The seeded row was
deleted afterwards. The API left on 8080 is the rebuilt one, restarted on the default schedule
and the default rate limit once the suites had finished. Flags left as found: `bot_enabled` and
`digit_contracts_enabled` both `true`, `bot_real_money_enabled` `false`.

---

## 4. Noted, not changed

- **Concurrent passes can under-count attempts.** `Reconcile` is lock-free so tests can race
  it; two passes reading `attempts = n` both write `n + 1`. In production `tick`'s `TryLock`
  means it does not happen; in tests it is harmless. Recorded so nobody adds the lock to
  `Reconcile` and wonders why the race test stopped racing.
- **`deposits_total` counts query-settled deposits** because they go through `settleDeposit`,
  which is the only place `onDepositSettled` fires. Not asserted in the harness — its wallet
  service registers no hook — but it is the same line a callback takes and the same hook.
- **The 60s floor is still only in `Load`.** CP2's report said so; nothing here changed it.
  `TestPassNeverTouchesAMockDeposit` uses the harness's 3m default rather than the floor, which
  is the stronger case.

CP4 not started.
