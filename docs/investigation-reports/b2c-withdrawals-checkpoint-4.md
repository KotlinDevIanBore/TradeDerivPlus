# B2C withdrawals · CP4 — the console, and the copy

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** `b2c-withdrawals-investigation.md` §5.2 CP4 — the operator's two jobs on a stuck payout,
the view that shows them one, and the withdrawal sheet's copy now that a payout exists. The last
checkpoint of the plan.

---

## 1. What was built

### 1.1 `ResolvePayout`, on the `ResolveDeposit` template

`POST /admin/payouts/:id/resolve`, where `:id` is the **withdrawal's transaction id** — the same
identifier the deposit route takes, so an operator working from the transactions list uses one kind
of id for both directions.

It requires a reason, refuses anything but an open payout on a pending withdrawal, and then settles
through the payout service's own `settle` rather than writing anything itself. That is the property
the whole feature rests on: a result arriving afterwards loses the guard and moves nothing. Audited
as `models.ActionPayoutResolve` over `TargetTransaction`, carrying the account number, the M-Pesa
request id, the outcome and the payout's status before the operator touched it.

The audit row is written **after** the settlement, in its own transaction, exactly as
`ResolveDeposit` does and for the same reason: the refund belongs to the wallet service's
transaction, whose lock order every settlement keeps, and the console must not be the first path to
take a wallet lock from outside it. Both the payout row and the ledger row record the operator, so
the money is attributed even if the process died between the two writes.

**Three outcomes, not two.** The brief named two — mark paid, mark never received — and the two
service methods provide three, because "the customer was not paid" is two different findings:

| Outcome | Money | What it records |
|---|---|---|
| `paid` | **none** — the debit already happened | the customer has the money |
| `never_received` | refund | M-Pesa never had the request |
| `failed` | refund | M-Pesa had the request and did not pay |

The two refunds move identical money, so the distinction is purely about the record — and it is
worth having for two reasons. Writing "never received" for a payout M-Pesa did attempt would be a
false record; and `never_received` is the only state a resend could ever start from, if the probe
ever shows that a repeated request id is safe to send. Collapsing them is mutation Q5.

### 1.2 The operator's view

A **Payouts** screen at `/admin/payouts`, in the sidebar with a count of what is open — a nag on
purpose, because each row is a customer whose balance is down with no answer.

Four filters: **Open**, **Unknown**, **No longer checking** (open with no schedule, which is the
pass having given up), and **Settled**. Each row shows the age, the payout's own state, the account,
the destination number, the amount, and how many status queries have been sent about it.

The sheet leads with what the state *means* rather than what it is called, because the two middle
states are the whole reason the screen exists:

> **Unknown** — M-Pesa has not said either way: the request may or may not have reached them. The
> balance is already debited, so this must not be refunded on a guess.

and it shows the **M-Pesa request id**, which is what an operator searches the portal for and the
only reference a payout settled without a receipt will ever have. Then the three actions, each
through `ReasonDialog`, each stating its consequence. The refunding ones say the thing that makes
them risky:

> If M-Pesa later reports the payout succeeded, that report is logged and credits nothing — the
> customer would hold both the money and the refund, and a B2C payout cannot be reversed.

`payout_resolve` has its own audit label, tone and filter chip.

### 1.3 The withdrawal sheet's copy, and the promise it now keeps

Before: *"Arrives in 1 to 5 minutes. No fee."* under the form, and *"It should arrive within a few
minutes."* in the toast. Two promises, and nothing behind either — the same class of copy the
deposit sheet carried until `e627765`, made twice.

After:

- under the form: **"No fee. If it doesn't go through, the money returns to your balance."** No fee
  is a fact. How long it takes is not ours to promise, because the payout is confirmed by M-Pesa on
  M-Pesa's timing and a stalled request can take hours. What is worth saying is what happens if it
  fails, since that is what a customer worries about while their balance is already down.
- the toast on send: **"Sent to M-Pesa — we'll tell you as soon as M-Pesa confirms it."**

That second one is a promise, so it had to become true. The sheet closes as soon as the request is
sent and nothing was watching afterwards: a withdrawal's settlement fired a socket event that only
invalidated a cache. `useAccountStream` now raises a toast when a withdrawal reaches a terminal
state — *"Withdrawal sent, M-Pesa has confirmed the payment"*, or the failure with its reason from
the payout copy table. Deposits are deliberately left alone there, because their own sheet is
watching and would toast twice.

---

## 2. Tests

Ten new integration tests in `internal/service/payout_resolve_test.go`, and one browser case.

| Test | Pins |
|---|---|
| `TestResolvePayoutAsPaidMovesNoMoney` | the original row closes as completed, `settled_by = admin`, `admin_id` on the ledger, **no balance change**; audited once with the request id and outcome; a late failure result afterwards refunds nothing |
| `TestResolvePayoutRefundsAndRecordsWhichFindingItWas` | both refunding outcomes return the money and land on **different payout states**; a late "paid" result credits nothing |
| `TestResolvePayoutRequiresAReasonAndAValidOutcome` | validation on `reason` and `outcome`; nothing moved, nothing audited |
| `TestResolvePayoutRefusesAnythingButAnOpenWithdrawal` | a settled payout, a deposit, an unknown id |
| `TestResolvePayoutLosesToAResultThatLandsFirst` | a result settles behind the operator's back: `ErrPayoutSettled`, the balance is the result's, `settled_by` still `callback`, nothing audited |
| `TestResolvePayoutRefusesWithNoPayoutServiceWired` | an unwired console refuses rather than settling by another route |
| `TestPayoutListShowsAnOperatorWhatToActOn` | all four filters across three accounts; the row carries the request id, the identity, the ledger status and whether anything is still checking |
| `TestOperatorResolvesWhatThePassGaveUpOn` | CP3 and CP4 meeting: the pass gives up without refunding, the operator then refunds, and the given-up count returns to zero |
| `TestWithdrawalOutcomeIsAnnouncedToTheCustomer` | a paid withdrawal announces with its receipt and a failed one with a payout-vocabulary reason — the assertion that makes the toast's promise true |

`admin.spec.ts` — **"resolves a stuck payout and a late result credits nothing"**: a verified funded
user withdraws to the stuck number, the operator opens Payouts, reads the state's meaning and the
portal instruction, marks it as having no record, gives a reason, and sees the refund. Then the
result is replayed at `/mpesa/b2c/result` saying it was paid: the API answers 200 and the balance
does not move. Then the audit log under its own label.

Four of my own mistakes on the way to green, and the last is the one worth reading:

- the accounts endpoint returns `{items: […]}`, not an array;
- the balance assertions were absolute, and the seeded fixture already held money, so they are now
  relative to the balance before the withdrawal;
- the first version tried to verify a freshly registered user, which is impossible (see below);
- and **two unrelated tests in the same file started failing**, because two of the string
  replacements I used to adapt the new test were unanchored and reached into the deposit case and the
  token case. `ReferenceError: user is not defined` in both. Caught by the full CFD run rather than
  by the single-test run I had been using, repaired line by line, and then checked by listing every
  use of the new names against the test each sits in — all four now inside the payout case.

That last one is the same failure mode as the mutation-driver quoting bugs, in a different costume: a
text edit that lands somewhere I did not look. The general lesson this project keeps relearning is
that an edit's *blast radius* has to be verified, not assumed — and the cheap check is to run the
whole file, not the one test.

### A gap the browser test uncovered

**There is no user-facing KYC endpoint at all.** Only the admin approve and reject actions exist, and
they need a submission that only `cmd/seed` creates. So a freshly registered user can never become
verified through the API by any route — which is exactly what the sheet's *"Verification is not
available yet"* admits, but it had not been written down as a blocker.

It matters beyond the test: it blocks withdrawals for real customers as surely as the feature flag
does. The browser case therefore approves a seeded submission and skips when the queue is empty, the
convention the existing KYC case already uses. On the deferred list as the next thing to build if
withdrawals are ever to be turned on.

### Mutation checks

Six on `ResolvePayout`, each applied with the one-match assertion, built before its test ran, and
restored byte-identically.

| # | Mutation | Result |
|---|---|---|
| Q1 | resolve settles by its own route instead of through the payout service | **fail**: `resolve returned … "pending", want the original … completed` |
| Q4 | `paid` passed as not-paid, so it refunds | **fail**: `resolve returned … "failed", want … completed` |
| Q5 | `never_received` recorded as a plain failure | **fail**: `payout is "failed", want "never_received" — the two findings must be distinguishable in the record` |
| Q6 | the reason requirement dropped | **fail**: `short reason: <nil>, want a validation error on reason` |
| Q2 | the settle guard's result ignored | **pass** — the preconditions answer first |
| Q3a | only the payout-open precondition removed | **pass** — the ledger-status check answers first |
| Q3b | **both** preconditions removed | **pass** — the settle guard answers, correctly |
| Q2c | **all three** removed | **fail**: `resolving a settled payout: <nil>, want ErrPayoutSettled`, and the race test likewise |

Three layers, and the passes are the finding rather than the gap: the two preconditions are
courtesies that give a clean error without a wasted round trip, and the guarantee is the payout
service's guarded settle, which CP2 mutation-checked in its own right. This is the same shape as the
deposit side's R3 and it is worth stating plainly, because a check that removed one layer and
reported green would have certified a design that was only partly there.

**Q2 needed a second attempt** — removing the `if !won` block whole left `won` unused and the tree
would not compile. Fifth instance in this project.

### Re-running every earlier mutation

Twelve, after a checkpoint that added new guard logic to the money path:

| Mutation | Still fails? |
|---|---|
| CP0 M1 one-in-flight index, M3 fail-open, M4 compensating row | ✓ ✓ ✓ |
| CP1 M2 result code defaults to zero | ✓ |
| CP2 M1c both timeout rules, M2 ambiguous refund, M3c both refund guards, M4b both send guards, M5 collapsed states | ✓ ✓ ✓ ✓ ✓ |
| CP3 N1 give-up refunds, N5 horizon disabled, N7 `MarkQueried` unguarded | ✓ ✓ ✓ |

Twelve for twelve. This is the third checkpoint to run this check and the second to find nothing,
which is what it should look like when the layering holds.

---

## 3. Verification

| Command | Result |
|---|---|
| `go build ./...`, `gofmt -l`, `go vet` (both tag sets) | clean |
| `go test ./...` | ok |
| the ten new tests | pass |
| `go test -tags integration ./internal/service/ -count=1` | **ok, 58.2s** — whole suite |
| Q1–Q6 and Q2c/Q3a/Q3b | as tabled; each restored byte-identical |
| the twelve earlier mutations, re-run | all still fail |
| `npx tsc --noEmit -p tsconfig.app.json` | clean |
| `npm run lint` | no findings on any changed file |
| `npx vitest run` | **130 passed** |
| `admin.spec.ts` "resolves a stuck payout…" (desktop) | **1 passed, 5.4s** |
| `npx playwright test --project=mobile --project=desktop` | **29 passed, 4 skipped, 3.0m** — one more skip than before, the payout case standing aside with withdrawals off |
| `npx playwright test --project=digit-mobile --project=digit-desktop` | **20 passed, 2 skipped, 2.3m** |
| `python3 scripts/smoke.py` | **ALL CHECKS PASSED** |
| `python3 scripts/bot_check.py` | **AUTOMATED RUN VERIFIED** |

The browser case was run with `withdrawals_enabled` temporarily true, because it self-skips
otherwise; the flag was put back to **false** before the suites, which is how they are reported.
Flags left as found: `bot_enabled` and `digit_contracts_enabled` true, `bot_real_money_enabled` and
`withdrawals_enabled` false.

**Withdrawals are still off, and this checkpoint deliberately does not turn them on.** The flag is
now the last thing standing between the built flow and live payouts, and flipping it needs the four
`MPESA_*` payout values, a funded utility account, and the identity-verification gap above closed.
That is a product and operations decision, not an engineering one.

---

## 4. The plan, closed out

| Plan item | Where it landed |
|---|---|
| §5.2 CP0 — flag, one-in-flight constraint, ceilings, refund arithmetic, the zero tile | CP0 |
| §2.1–§2.3 CP1 — the client, the credential, status and balance, the allowlist and copy | CP1 |
| §2.5 CP2 — the payout table, the service, reserve→request→record, two routes, mock mode | CP2 |
| §3.2 CP3 — the pass, the give-up horizon, five metrics | CP3 |
| CP4 — the console's actions, the operator view, the copy | CP4 |
| §3.3 idempotency | the account-level index (CP0); the client key stays a shared ticket |
| §3.5 float | fed free from every successful payout (CP3) |
| the conservative rule | no resend anywhere; the probe gates that one optimisation |

**Open, and written down rather than implied:** whether Safaricom deduplicates a repeated request id;
a status-query code meaning "never seen", which would let the ambiguous case resolve without an
operator; the absence of any user-facing identity verification; and the three provisional schedules.
All on the deferred list with what each needs.

The feature is built and every part of it is reachable, tested and mutation-checked. What is left to
make it live is not code.
