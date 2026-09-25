# B2C withdrawals · CP3 — the pass that asks

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** `b2c-withdrawals-investigation.md` §5.2 CP3 — the status-query reconciliation pass, the
give-up horizon, and the metrics. Still on the conservative rule: **the pass asks, and there is no
code in it that could resend.**

---

## 1. What was built

### 1.1 A pass that asks rather than concludes

`MpesaPayoutService.Start` runs one pass at boot and one per `MPESA_PAYOUT_QUERY_EVERY`, in the
deposit pass's shape: `TryLock` so ticks cannot stack, the boot pass inside the goroutine so a boot
does not wait on a hundred network calls, and the root context so a ticker dies on SIGTERM.

`Reconcile` lists open payouts whose next look has come — oldest first, bounded at 100 — and for
each either gives up or asks. What it cannot do is finish the job, and that is the shape of the API
rather than a limitation: Daraja's Transaction Status endpoint is **asynchronous**, so the pass
issues a question, records that it asked, widens the backoff, and stops. The answer arrives later on
its own route, and `HandleStatusResult` classifies it there.

So the deposit reconciler's list-query-classify-settle does not transplant. Written out:

| Row | What the pass does | Counted as |
|---|---|---|
| older than `MPESA_PAYOUT_GIVE_UP` | `MarkGaveUp`: off the schedule, still open, an ERROR line; **not queried, and not refunded** | `gave_up` |
| the query could not be issued | nothing — the row keeps its schedule and is still due | `error` |
| asked | `MarkQueried`: attempts + 1, next look on the ladder | `queried` |
| settled between the read and the write | nothing | `already_settled` |

Backoff: **5m, 15m, 30m, then 1h**, marked provisional. Longer at the top than the deposit ladder
because each rung costs two round trips rather than one — asking again before the previous answer
has had time to arrive asks about something already in flight.

### 1.2 Give-up does not refund

Past the horizon the pass stops asking and leaves the payout open and unscheduled. It does **not**
settle and does **not** refund, and that is the whole design of the `unknown` state applied one step
further out: the customer's money is already gone, and giving up means we stopped asking, not that
we learned anything. Refunding there would be exactly the guess `unknown` exists to refuse, with a
day's delay attached.

`next_query_at IS NULL` on an open row is the marker, the convention the deposit reconciler set, and
it is why every other path that leaves a row open sets a schedule. `MarkGaveUp` is guarded against
the terminal states so a horizon crossing cannot reopen a settled payout, and against its own
repetition so the metric cannot double-count.

The ERROR line is deliberate and loud: *a debited customer has no answer and no process left*.

### 1.3 A third route, for a third kind of document

Status answers arrive at `POST /api/v1/mpesa/b2c/status`, not at the payout result route. The client
sends `B2CStatusResultURL` on status queries, `ValidateB2CConfig` requires all three URLs to differ,
and `config.Load` refuses any two of them equal at boot.

The reason is the rule CP1 built and CP2 enforced, one step along: a non-zero result code on a
**status answer** means the *query* failed, not the payout. A `2001` there is our credential being
wrong and says nothing whatever about the customer's money. Handing status answers and payout
results to one handler would let a failed question look like a failed payout — and `2001` is a code
that is terminal on the payout channel, so that mistake would refund a customer whose payout may
have succeeded. Three meanings, three channels, and the channel is always the discriminator.

### 1.4 Metrics

| Metric | Type | Meaning |
|---|---|---|
| `mpesa_payouts_total{outcome}` | counter | one per payout **settled**, by `completed` / `failed` / `never_received` |
| `mpesa_payout_queries_total{outcome}` | counter | one per payout the **pass examined**, by what it did |
| `mpesa_payouts_unknown` | gauge | **the number to alert on** |
| `mpesa_payouts_given_up` | gauge | open payouts nothing is looking at any more |
| `mpesa_b2c_utility_available_kes` | gauge | the business's remaining B2C float |

Two counters rather than one label set, for the reason `bot_contracts_total` had to be corrected:
asking a question and establishing an outcome are two events about one payout, and summing them
would count it twice. `never_received` is broken out of `failed` because they mean different things
operationally — Daraja refusing, versus us establishing that Daraja never had the request.

**`mpesa_payouts_unknown` is the one worth paging on**, and it is a different thing from a stale
deposit: each one is a customer whose balance has already been debited and about whom Daraja has said
nothing either way, so it is neither safe to refund nor safe to call paid. The sampler also logs an
ERROR line when it is non-zero, so whoever reads logs does not need a dashboard to learn it.

The float gauge is fed from the utility balance Daraja reports on **every successful payout** —
free, needing no extra call, and the difference between noticing an empty utility account before
customers do or after. A blank or unparseable value is ignored rather than zeroing the gauge, because
zero float is an emergency and must not be reported by a parse failure. `QueryAccountBalance` exists
from CP1 for when there has not been a payout lately; wiring a periodic check is on the deferred list
as optional.

Mock mode runs the pass with no querier at all: nothing is asked, and the listing, the schedule and
the horizon stay exercised continuously — the parts most likely to rot unseen.

---

## 2. Tests

Fourteen new integration tests in `internal/service/payout_reconcile_test.go`.

| Test | Pins |
|---|---|
| `TestPassAsksAndWidensTheBackoff` | five rungs at 5m/15m/30m/1h/1h (±2s); a pass inside each backoff asks nothing; nothing settles and no money moves across all five |
| `TestPassNeverResendsThePayout` | ten passes, including from `unknown`: **one** payout request in total, ten questions |
| `TestPassFailureToAskIsNotAnAttempt` | two failed asks leave attempts and the schedule untouched and the row still due; when Daraja returns, the ask lands |
| `TestStatusAnswerSettlesAPayout` | `Completed` settles with the receipt, `settled_by = query`, and no balance change |
| `TestStatusAnswerOfDeclinedRefunds` | `Declined` refunds |
| `TestFailedStatusQueryConcludesNothing` | query codes `2001`, `SFC_IC0003`, `1` — terminal on the payout channel — conclude nothing here; `Pending` likewise |
| `TestStatusAnswerOnTheTimeoutChannelConcludesNothing` | a `Completed` answer on the timeout channel settles nothing |
| `TestGiveUpStopsAskingWithoutRefunding` | zero queries past the horizon, schedule cleared, status unchanged, **balance still debited**, off the pass's list but visible as open and counted as given up |
| `TestPassIgnoresSettledPayouts` | a settled payout is never asked about, and a late `Declined` answer for one refunds nothing |
| `TestBootPassAsksAboutAStrandedPayout` | a fresh service over the same store asks within 5s |
| `TestPassRunsInMockModeWithoutAsking` | the schedule advances, nothing settles |
| `TestPayoutMetricsAndFloatAreReported` | one settlement each for completed / failed / never_received; `queried` and `gave_up` once each, and `gave_up` **not** re-counted by a second pass; the float arrives from a successful result |
| `TestUnknownAndGivenUpCountsAreSampleable` | the alerting gauge's source: 0 → 1 on an ambiguous send; given-up counts in both |
| `TestPassWriteGuardsAreNoOpsOnSettledPayouts` | `MarkQueried` and `MarkGaveUp` refuse a settled payout and apply to an open one; a second `MarkGaveUp` is a no-op |

### A harness omission that made a test pass for the wrong reason

The harness config did not set the payout schedule, so `MpesaPayoutGiveUp` was **zero** and every
payout was instantly past the horizon. Three tests failed outright — the pass never asked anything —
but `TestGiveUpStopsAskingWithoutRefunding` **passed**, because with the horizon at zero it could not
tell a working horizon from one that swallows everything.

This is the third time the harness has been caught building `config.Config` directly, where `Load`'s
validation never runs: the withdrawal ceilings defaulted to zero in CP0, the reconciliation min-age
in STK CP2, and now this. The comment in the harness names it.

### Mutation checks

Seven, each applied with the one-match assertion, built before its test ran, restored
byte-identically.

| # | Mutation | Result |
|---|---|---|
| N1 | **give-up refunds** | **fail**: `payout is "failed" after give-up, want still unknown — giving up is not learning something` |
| N2 | **the pass resends** from `unknown` | **fail**: `11 payout requests after ten passes, want 1 — the pass resent a payout, and whether Safaricom deduplicates a repeated request id is unproven` |
| N3 | a failure to ask counts as an attempt | **fail**: `attempts 1 after two failed asks, want 0` |
| N4 | a failed status query read as a failed payout | **fail**: `query code 2001 settled the withdrawal as "failed" — a failed QUERY says nothing about the payout` |
| N5 | the give-up horizon disabled | **fail**: `asked 1 times about a payout past the horizon, want 0`, and `queried map[queried:2], want one gave_up` |
| N6 | `MarkGaveUp` unguarded | **fail**: `MarkGaveUp claimed a settled payout` |
| N7 | `MarkQueried` unguarded | **fail**: `MarkQueried claimed a settled payout; a pass that lost this race would reschedule a finished payout` |

**N5, N6 and N7 each needed a second attempt, and for two different reasons.**

N5 never applied: the shell quoting in the driver mangled `&&` into an invalid JSON escape, the
script raised, and the run printed `ok`. That is the CP2 lesson recurring — the third time shell
quoting has broken a mutation in this project — and it is why the re-run greps the mutated file for
the change before believing the result. The fix was a heredoc instead of shell-quoted JSON.

N6 and N7 applied cleanly and **survived**, and that was the code being right in a way the tests
could not see. Both guards protect a race the pass cannot reach sequentially: `ListDue` filters out
settled payouts, so the pass never calls either method on one. Rather than delete the guards or leave
them unproven, `TestPassWriteGuardsAreNoOpsOnSettledPayouts` pins their contract at the repository —
refuse a settled payout, apply to an open one — and both mutations then fail.

### Re-running every earlier mutation

The habit this checkpoint was asked to adopt: after adding new guard logic, check the **old**
mutations still fail, because CP2 found one that had quietly stopped.

| Mutation | Still fails? |
|---|---|
| CP0 M1 — the one-in-flight index made non-unique | ✓ |
| CP0 M3 — fail open with no flag source | ✓ |
| CP0 M4 — the refund also inserts a compensating row | ✓ |
| CP1 M2 — an unreadable result code defaults to zero | ✓ |
| CP2 M1c — both timeout rules removed | ✓ |
| CP2 M2 — an ambiguous send refunds | ✓ |
| CP2 M3c — both refund guards removed | ✓ |
| CP2 M4b — both send-guard clauses removed | ✓ |
| CP2 M5 — `never_received` collapsed into `unknown` | ✓ |

Nine for nine. Two of them first reported *"NO LONGER FAILS"* — and both times the mutation had
never applied, again through shell quoting (`\&\&` and `\&models`). Re-applied through heredocs, both
fail as they should. Worth stating plainly: had those two lines been believed, this report would have
claimed CP3 broke two money guards that it had not touched. The check is only as good as the proof
that the mutation landed.

---

## 3. Verification

| Command | Result |
|---|---|
| `go build ./...`, `gofmt -l`, `go vet` (both tag sets) | clean |
| `go test ./...` | ok |
| the fourteen new tests | pass |
| `go test -tags integration ./internal/service/ -count=1` | **ok, 54.7s** — whole suite |
| N1–N7 | as tabled; each restored byte-identical |
| the nine earlier mutations, re-run | all still fail |
| boot with the status URL equal to the result URL | **refused**: `MPESA_B2C_RESULT_URL and MPESA_B2C_STATUS_RESULT_URL must differ…` |
| boot with four of the five payout values set | **refused**: `payouts need all of … or none of them` |
| the five new metrics at boot | all present, every series at zero |
| `POST /mpesa/b2c/status` with an unmatchable payout | **200** |
| `python3 scripts/smoke.py` | **ALL CHECKS PASSED** |
| `npx playwright test --project=mobile --project=desktop` | **29 passed, 3 skipped, 2.9m** |
| `npx playwright test --project=digit-mobile --project=digit-desktop` | **20 passed, 2 skipped, 2.2m** |
| `python3 scripts/bot_check.py` | **AUTOMATED RUN VERIFIED** |
| the pass running live on a 5s interval | see below |

Browser rows were produced the way STK CP4 established. Flags left as found, `withdrawals_enabled`
still false, so nothing served exercises a payout and `smoke.py` still sees the platform refusal.

### The pass, live

With withdrawals off there are no payouts, so the pass logs nothing and proves nothing by running.
To see it work, an open payout was seeded directly — a zero-amount pending withdrawal, chosen so it
contributes nothing to the ledger sum and cannot raise the drift alarm while it exists — against an
API on a 5-second interval:

| Reading | `queried` | `gave_up` | `mpesa_payouts_unknown` | the row |
|---|---|---|---|---|
| before | 0 | 0 | 0 | — |
| 12s after seeding it due | **1** | 0 | 0 † | `reconcile_attempts 1`, next look in the future |
| 12s after ageing it past 24h | 1 | **1** | **1** | `unknown`, off the schedule, `gave up asking after 25h0m0s; needs an operator` |

```
level=ERROR msg="payout reconcile: GIVING UP; a debited customer has no answer and no process left"
    originator=wd-cp3-live-probe status=unknown age=25h0m0s attempts=1
```

† The gauges are sampled once a minute, so the first reading predates a sample.

Note what the counters say about the design: `queried` stopped at 1 rather than climbing every five
seconds, because the backoff held the row off the list; and `gave_up` is 1 after repeated passes,
because a given-up payout is off the schedule and cannot be re-counted. The row was deleted
afterwards and `ledger_drift_accounts` is 0.

---

## 4. What CP4 inherits

- `SettleByAdmin` and `MarkNeverReceived`, both tested and both going through the one guarded
  `settle`, for the console's two actions on a stuck payout: mark it paid, or mark it never received.
- `ListOpen` and `CountGivenUp` for the operator's view, and `mpesa_payouts_given_up` for the queue
  it is working through.
- The one thing still missing to close the loop automatically: a status-query code that means
  "Safaricom has never seen this". Identify it — from the probe, or from the first real pass against
  sandbox — and `MarkNeverReceived` stops being an operator's conclusion. It is on the deferred list,
  next to the resend question, which the same probe answers.

CP4 not started.
