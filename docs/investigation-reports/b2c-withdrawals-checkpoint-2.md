# B2C withdrawals · CP2 — the payout service

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** `b2c-withdrawals-investigation.md` §5.2 CP2 — the payout table, the service that owns it,
`Withdraw` refactored into reserve → request → record, the two callback routes, mock mode, and the
config. Built on the **conservative rule**: nothing ever resends, because whether Safaricom
deduplicates a repeated request id is unproven.

---

## 1. The conservative rule, and why it costs nothing

Approved before this checkpoint started: no resend, ever, until the probe says otherwise. The
argument is asymmetry, and it is worth restating because every design decision below follows from it.

- If Safaricom **does** deduplicate, a resend path is addable later as a new branch. Nothing built
  here changes.
- If it **does not**, building resend first would have been a double-payment bug found by paying
  someone twice — and B2C payouts cannot be reversed through any API.

Corroboration found while looking for the answer: the production system on this same account
reached the identical rule independently. Its payout path never resends, and on an ambiguous send it
logs, alerts an operator and leaves the debit reserved — with a comment saying Daraja may have
accepted the payout even though the response was not received, and that reversing there could leave
the user holding both the restored balance and the payout. That is this rule, written by someone
else, under real stakes.

So the probe gates an optimisation. It is on the deferred list as such, with what it needs.

## 2. The two states, made real

The requirement was that "sent, outcome unknown" and "definitively never received" be genuinely
distinct and operator-visible, not described that way. They are two of six values of
`mpesa_payouts.status`:

| State | Meaning | May money move? |
|---|---|---|
| `pending` | reserved in the same transaction as the debit; nothing sent | no |
| `sent` | Daraja accepted it and returned a `ConversationID` | no; waiting |
| **`unknown`** | the send was ambiguous, Daraja's queue timed out, or a code arrived that this codebase will not call terminal. Daraja **may** hold the request | **no — and above all no refund** |
| **`never_received`** | established that Safaricom never had the request at all | **yes: a refund is provably safe, because nothing was queued that could pay** |
| `completed` | result code 0; the customer has the money | the ledger row closes; no balance change |
| `failed` | refused at the door, or a terminal failure code | refund |

What makes them distinct rather than cosmetic:

- They are separate rows in the database, separate constants, and `PayoutIsOpen` counts `unknown`
  as open while `never_received` is terminal with its schedule cleared.
- **They behave differently.** `MarkNeverReceived` settles and refunds; nothing refunds out of
  `unknown`. `TestNeverReceivedIsADistinctStateThatRefunds` asserts the transition, the refund, the
  clearing of `next_query_at`, and that a late "paid" result afterwards credits nothing. Mutation M5
  collapses the two, and that test fails.
- **Nothing reaches `never_received` automatically yet**, and the report says so rather than
  implying the loop is closed. Establishing it needs a status-query answer meaning "no such
  transaction", and no such code is identified — a non-zero code on a status query classifies
  Unresolved, deliberately. So today it is an operator's conclusion from the M-PESA portal. The
  state exists now so that the transition is already correct and already refunds when something can
  reach it, and so a resend path has exactly one state it may start from. Both gaps are on the
  deferred list.

## 3. What was built

### 3.1 `mpesa_payouts` (migration 0009), and why not `mpesa_requests`

Its own table. The reason is not tidiness: the deposit reconciler's `ListPending` selects every
pending row in `mpesa_requests`, STK-queries each, and on an answer that parses as success
**credits the wallet**. A payout row there would be queried with the wrong API and, on a parse that
looked like success, would credit a customer for their own withdrawal. No discriminator column
fixes that, because every existing reader would have to learn about it.

`transaction_id` is UNIQUE (one payout per withdrawal, one withdrawal per payout).
`originator_conversation_id` is UNIQUE — it is the key every callback and query comes back on. The
**full MSISDN** is here because the ledger row keeps only `***678`, which is right for a ledger and
useless to a payout. Three indexes: the callback lookup, the reconciler's partial index over open
rows, and a status/created index so an operator can find the `unknown` and `never_received` rows
without a table scan.

### 3.2 Reserve → request → record

`Withdraw` now:

1. **Reserve**, in one transaction: lock the wallet, check the freeze, the balance and the daily cap,
   insert the ledger row, **insert the payout row** with a freshly minted request id and the full
   number, debit the wallet. Commit.
2. **Request**, outside every lock: `MarkRequested` (guarded on `requested_at IS NULL`), then
   `SendB2C`.
3. **Record**: accepted → `sent` with Safaricom's id; refused → settle failed and refund; anything
   else → `unknown`.

Two things about step 1. The payout row is in the **same** transaction as the debit because a debit
whose payout row is missing has lost the destination number and the request id — and a crash between
two transactions is not hypothetical. And the send is **outside** the locks because holding a wallet
row across a network call blocks every other writer on that account for as long as Daraja takes.

The send is synchronous within the HTTP request, like `Deposit`'s STK push. That means a customer
learns immediately when a payout is refused, rather than sitting on "processing" for an outcome we
already have.

### 3.3 One `settle`, and the settlement handler

`MpesaPayoutService.settle` is the only writer of a payout outcome: the guarded UPDATE over the open
states, then one call to `onResult`. Every source is an adapter over it — the result callback, a
rejection at the door, `MarkNeverReceived`, `SettleByAdmin`. It refuses when no handler is
registered, before consuming the guard, for the reason the deposit side learned in STK CP0: a payout
row gone terminal with the ledger pending is unrepairable, because every later attempt loses the
guard this one burned.

`WalletService.settlePayout` is the handler, and it is where money moves:

- **paid** → `completeWithdrawal`: the ledger row goes `pending → completed` with the receipt, and
  **no balance changes**, because the debit happened when the withdrawal was requested. Guarded, so a
  repeated result cannot announce twice.
- **not paid** → the CP0 refund path, unchanged: guarded flip to `failed` then the wallet credit, in
  transactions-row then wallets-row order.

`SetPayouts` wires both directions at once — this service sends through the payout service, and the
payout service reports back here — so the two halves cannot be wired half way.

### 3.4 Two routes, because the channel is the information

`POST /api/v1/mpesa/b2c/result` and `POST /api/v1/mpesa/b2c/timeout`, public, and both answering 200
always — Daraja retries anything else, and a retry storm against a payout callback is the last thing
worth inviting. Which route received the payload is passed into the parser, because the body does not
say and it decides whether the outcome may be acted on. `Load` refuses a configuration where the two
URLs are equal, and `ValidateConfig` is called at boot, so the collapse is a failed start.

### 3.5 Mock mode

`mockPayoutSender` accepts the request and then, after a short delay, delivers a result **through
`HandleResult` on the result channel** — the real entry point and the real classifier, so mock mode
exercises production classification rather than a shortcut. One in six fails, cycling real B2C codes
a customer can cause, so the refund path and its copy are exercisable. A payout to `MockStuckPhone`
is accepted and never reported on, which is how a stuck payout is produced on demand for CP4's
operator tooling.

---

## 4. Tests

Fifteen new integration tests in `internal/service/payout_test.go`, plus one added to CP0's file.

| Test | Pins |
|---|---|
| `TestWithdrawalReservesThePayoutBeforeItSends` | the row reaches `sent`; the **full** MSISDN on it and only the mask on the ledger; both ids; `requested_at` and `next_query_at` stamped; the id sent is the id stored; whole shillings; one send |
| `TestOnePayoutRequestPerWithdrawalEvenUnderConcurrency` | eight concurrent resends of one payout produce **one** request in total |
| `TestRejectedPayoutRefundsImmediately` | a refusal at the door refunds at once, `settled_by = rejection`, the code recorded, and the copy is not the deposit's |
| `TestAmbiguousSendLeavesTheMoneyOutAndSaysSo` | withdrawal stays pending, balance stays debited, row is `unknown` with a reason and still open; a later result settles it normally |
| `TestNeverReceivedIsADistinctStateThatRefunds` | the transition, the refund, no longer open, schedule cleared; a late paid result afterwards credits nothing |
| `TestPayoutResultSettlesTheWithdrawal` | completed with the receipt on the ledger row, and **no balance change** |
| `TestPayoutFailureRefundsWithCopyThatDoesNotBlameTheCustomer` | code 1 refunds, and the copy is not "your M-Pesa balance was too low" |
| `TestQueueTimeoutSettlesNothing` | a `2040` on the timeout channel — terminal on the result channel — settles nothing and refunds nothing; the real result, including "paid", still settles afterwards |
| `TestUnrecognisedResultCodeLeavesThePayoutOpen` | an unknown code refunds nothing |
| `TestConcurrentPayoutResultsSettleExactlyOnce` | five withdrawals × eight callers, half on each channel: one refund each, `assertReconciled` every round |
| `TestPayoutSettleRefusesWithNoResultHandler` | refuses before the guard; the row stays `sent`; the wired service then settles it |
| `TestStuckPhonePayoutStaysSent` | the fixture |
| `TestOpenPayoutsAreVisibleToAnOperator` | `ListOpen` returns exactly the open row; `CountByStatus` for the gauges |
| `TestMockPayoutSenderSettlesThroughTheRealPath` | mock mode's own sender, both outcomes, reconciled |
| `TestMockPayoutSenderNeverReportsOnTheStuckNumber` | fifteen times the simulator's delay later, still `sent` |
| `TestWithdrawalRefusedWithNoPayoutServiceWired` | the other half of fail-closed (see §5) |

**The harness stopped using mock mode's simulator.** It installs a sender that accepts and never
reports back, and tests deliver results explicitly. The first version left the simulator in place
with a 50 ms delay and every CP0 test passed — by timing luck. Any assertion about a withdrawal
still in flight was racing that timer, and would have passed or failed by how fast the machine was.
What is under test is the classification and the guards, not the scheduler.

**One real bug the tests caught.** `MarkUnknown` was guarded on `status = 'pending'` alone, so a
queue timeout arriving for a payout Daraja had already accepted — the commonest case by far, since
the row is `sent` by then — matched nothing. The ambiguity was logged and never recorded, which
would have made this checkpoint's central claim false: the state would not have been durable. It is
now reachable from every open state and still refused from the terminal ones, so a late timeout
cannot reopen a settled payout.

### Mutation checks

Every result was re-read against what the test actually asserts before being believed, which is how
three of the six turned out to need a second form.

| # | Mutation | Result |
|---|---|---|
| M2 | an ambiguous send refunds | **fail**: `withdrawal is "failed" after an ambiguous send, want pending — nothing is known either way` |
| M5 | `never_received` collapsed into `unknown` | **fail**: `payout is "unknown", want "never_received" — the two states must be distinguishable in the database` |
| M6 | a payout completion also credits the wallet | **fail**: `balance 500 after the payout completed, want 400` |

And the three that needed the both-guards treatment:

| # | Mutation | Result |
|---|---|---|
| M1a | only the **service's** timeout check removed | **pass** — the client's rule still forces Unresolved |
| M1b | only the **client's** timeout rule removed | **pass** — the service still returns early on the channel |
| M1c | **both** removed — the collapse as the neighbouring system has it | **fail**: `withdrawal is "failed" after a queue timeout, want pending — a timeout is not an outcome` |
| M4a | only `requested_at IS NULL` removed from the send guard | **pass** — the `status = 'pending'` clause still holds |
| M4b | **both** clauses removed | **fail**: `9 requests after 8 concurrent resends of one payout, want 1 — each extra one is a real payout no API can reverse` |
| M3a | the payout settle guard ignored | **pass** — the ledger's `UpdateStatusIfPending` still holds |
| M3b | only the **ledger** guard removed | **pass** — the payout guard still holds |
| M3c | **both** removed | **fail**: `balance 1300 after 8 concurrent results for one payout, want 1000` |

That last figure is the payout mirror of the deposit side's `$900 where $200 is correct`: a triple
refund on one withdrawal, from traffic Daraja generates by retrying.

**The single-guard passes are the finding, not the gap.** Each of the three properties is held in
two independent places — the client refuses to classify a timeout as terminal *and* the service
refuses to act on one; the send guard checks both the stamp and the status; the payout row and the
ledger row each guard their own write. A check that removed one and reported green would have
certified a design that was half present, which is the lesson the double-credit fix recorded and the
reason each was taken to both.

**M3's first form did not compile** — replacing `if !won {` with `if false {` left `won` unused —
and was rewritten build-safe before being trusted. Fourth instance in this project of a mutation
that looked like evidence and was not.

### A test that CP2 silently weakened, and the mutation that proved it

Adding the "no payout service means refuse" check gave `Withdraw` two independent grounds to refuse
with the same error. `TestWithdrawalRefusedWithNoFlagSourceWired` builds a wallet service missing
**both**, so it kept passing — and CP0's fail-open mutation, re-run, **stopped failing**. The flag
check could have been deleted unnoticed.

Fixed by wiring the payout service into that test's lone wallet, leaving the flag as the only thing
missing; CP0's mutation fails again, quoted in the verification table. A converse test now covers
the new check on its own. This is the same class as CP0's M7 and it was found the same way: by not
trusting a passing test after changing the code around it.

---

## 5. Verification

| Command | Result |
|---|---|
| `go build ./...`, `gofmt -l`, `go vet` (both tag sets) | clean |
| `go test ./...` | ok |
| the fifteen new tests | pass |
| `go test -tags integration ./internal/service/ -count=1` | **ok, 53.9s** — whole suite |
| M1a–M6 | as tabled; each restored byte-identical |
| CP0's fail-open mutation, re-run after the test fix | **fail**: `unwired wallet returned <nil>, want ErrWithdrawalsDisabled` |
| boot with equal result and timeout URLs | **refused**: `MPESA_B2C_RESULT_URL and MPESA_B2C_TIMEOUT_URL must differ…` |
| boot with a partial payout configuration | **refused**: `payouts need all of … or none of them` |
| migration 0009 on the existing database | applied; six indexes present; `payouts_configured=true` in the boot line |
| `POST /mpesa/b2c/result` and `/timeout`, with an unmatchable payout and with garbage | **200** on all four, as Daraja requires |
| `python3 scripts/smoke.py` | **ALL CHECKS PASSED** |
| `npx playwright test --project=mobile --project=desktop` | **29 passed, 3 skipped, 3.0m** |
| `npx playwright test --project=digit-mobile --project=digit-desktop` | **20 passed, 2 skipped, 2.1m** |
| `python3 scripts/bot_check.py` | **AUTOMATED RUN VERIFIED** |

Browser rows were produced the way STK CP4 established: CI's two `AUTH_RATE_*` values, and the digit
flag off for the CFD pair and on for the digit pair. Flags left as found, `withdrawals_enabled` still
false — so nothing in the browser suites exercises a payout, and `smoke.py` still sees the platform
refusal.

---

## 6. What CP3 inherits

- `mpesa_payouts` already carrying `reconcile_attempts` and `next_query_at`, with the partial index
  the pass will scan, and every send scheduling its first look.
- `payoutQuerier`, declared but unused, for the status query the pass will issue; `ParseStatusCallback`
  from CP1 for the answer, which arrives on the same two routes.
- `ListOpen` and `CountByStatus` for the pass and the gauges. The gauge worth alerting on is the
  count of `unknown`: each one is a customer debited with no answer either way.
- `MarkNeverReceived`, waiting on a status-query code that means "no such transaction" — which is
  what would make the ambiguous case resolve without an operator, and is on the deferred list.

CP3 not started.
