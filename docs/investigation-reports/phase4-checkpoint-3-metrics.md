# Phase 4 · CP2 follow-up — the two metrics the load run found

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** the double-counted contract metric and the missing placement-error metric, both found
by measurement in Checkpoint 2. Closes both deferred items.

---

## 1. The double count

`BotService` called one hook twice per contract — `onContract("placed")` when it placed, and
`onContract(outcome)` when it settled — so a single counter received two increments for one
contract. Anything reading the whole metric read double. The load run measured it plainly: 10,166
`placed` against 4,905 `won` plus 5,278 `lost`.

**Fixed by splitting the event, not by patching the query.** A placement and a settlement are two
things that happen to one contract, and a label is the wrong way to say that — summing across
labels is the natural thing for a reader to do, and it was wrong to do it.

| Metric | Now |
|---|---|
| `bot_contracts_total{outcome}` | **settlements**, one per contract, `won` / `lost` / `void` |
| `bot_contracts_placed_total` | **placements**, one per contract, no labels |

`bot_contracts_total` keeps the name and the label §2.9 specified, and now satisfies what the
alert's rationale assumed all along: one contract, one increment.

## 2. The alert's units were wrong too

Confirming the threshold "actually fires at the right rate" turned up a second error in §2.9,
independent of the double count.

> `rate(bot_contracts_total[1m]) > bot_runs_live × 60`

`rate()` in Prometheus is **per second**. `bot_runs_live × 60` is a **per-minute** figure — sixty
contracts per run per minute, which is the one-per-second ceiling the sentence describes. Mixing
them asks for sixty contracts *per second* per run: sixty times the physical ceiling, so the alert
could not fire under any circumstance including the runaway it was written for.

Corrected to put both sides in the same unit:

> `increase(bot_contracts_total[1m]) > bot_runs_live × 60`

Together the two errors cancelled in direction but not in size: the double count made the measured
value twice too large, and the unit mismatch made the threshold sixty times too large.

## 3. `bot_placement_errors_total`

In §2.9's table, absent from `metrics.go`. Built now, labelled by the error's own code —
`insufficient_funds`, `account_frozen`, `market_closed`, `no_price`, `too_many_open`,
`bot_run_not_live`, `error` — which is the same vocabulary the API answers a client with, so a
dashboard label and a support conversation use the same word.

Recorded in **one place**, immediately after `place` returns, rather than in each branch of the
switch that follows: a branch added later cannot quietly stop being counted.

What it is for: without it, a run that *cannot* place and a run that simply *is not* placing look
identical — `bot_contracts_placed_total` flat in both cases.

---

## 4. Confirming the threshold fires at the right rate

Measured, not reasoned. 20 runs, four consecutive minutes, evaluating the corrected expression
against the live counters:

| minute | `increase(bot_contracts_total[1m])` | placed | `bot_runs_live` | threshold | ratio | fires |
|---|---|---|---|---|---|---|
| 1 | 400 | 400 | 20 | 1,200 | **0.33** | no |
| 2 | 400 | 400 | 20 | 1,200 | **0.33** | no |
| 3 | 400 | 400 | 20 | 1,200 | **0.33** | no |
| 4 | 400 | 400 | 20 | 1,200 | **0.33** | no |

Placement errors over the window: 0.

**The two counters now agree** — 400 and 400 — which is the fix, visible. A contract is placed once
and settles once, about a second apart, so over a minute the two rates are the same number.

**The ratio is the answer to "at the right rate".** 0.33 is not an arbitrary margin: a run holds
one contract at a time and a contract takes about a second to settle on top of the 2 s interval, so
a healthy run places one roughly every three seconds against a ceiling of one per second. The alert
sits at a third of its threshold and would fire if throughput tripled — which cannot happen while
the loop is sequential, which is exactly why exceeding it means a bug rather than load.

For comparison, on the same traffic:

| Expression | Value | Threshold | Fires at |
|---|---|---|---|
| Corrected | 400/min | 1,200/min | 3× the real rate |
| With the double count (before) | 800/min | 1,200/min | **1.5× the real rate** — half the intended margin |
| As §2.9 originally wrote it | 13.3/s | 1,200 | 180× the physical ceiling — never |

The 50-run figures from Checkpoint 2 sit the same way: 1,000 settlements a minute against a
threshold of 3,000.

---

## 5. Tests

`internal/service/bot_metrics_test.go`, against the hooks rather than against Prometheus — the
hooks are the contract, and this service deliberately never imports the metrics package.

- **`TestBotContractCountersCountEachContractOnce`** — a four-contract run resolved
  won/lost/void/won. The placement counter must read 4, the outcome counters must total 4 (not 8),
  they must break down 2/1/1, and **there must be no `placed` label on the outcome counter**, which
  is the double count named directly.
- **`TestBotPlacementErrorsAreCountedByCode`** — a run with one contract settled, then the account
  frozen underneath it. The next placement is refused inside `Open`'s transaction: exactly one
  `account_frozen` placement error, and the placement counter still reads 1, because a refusal is
  not a placement.

Mutation-checked: putting the `onContract("placed")` call back makes the first test fail with

> `outcome counters total 8, want 4 — one per contract, not one per event: map[lost:1 placed:4 void:1 won:2]`

---

## 6. Commands

| Command | Result |
|---|---|
| `go test -tags integration ./internal/service/ -count=1` | **ok, 26.6s** |
| `go test ./...` | ok |
| `go vet ./...`, `go vet -tags integration ./...`, `gofmt -l` | clean |
| `python3 scripts/bot_check.py` | **AUTOMATED RUN VERIFIED** |
| `npx playwright test --project=digit-mobile --project=digit-desktop` | **20 passed, 2 skipped**, exit 0 |
| mutation: the double count restored | the counter test fails with the map above; restored |
| live alert evaluation, 20 runs × 4 minutes | table in §4 |

---

## 7. Closed

Both deferred items are moved to Closed against **P4 CP3**. The plan's §2.9 now carries the
corrected table and expression, with a note recording both errors against the table they came
from — the formula was wrong on paper for two independent reasons, and only a measurement found
either.

Checkpoint 4 remains gated on the §10 answers, which have not been given.
