# STK reconciliation · CP4 — console resolve, and the copy

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** plan §2.8 and the last of §2.7 — `POST /admin/deposits/:id/resolve`, its audit action,
the console's transaction sheet, the "stuck?" marker, Adjust's note, the deposit sheet's timeout
copy now that its condition is met, the `admin.spec.ts` case, and the deferred list. The last
checkpoint of the plan.

---

## 1. What was built

### `ResolveDeposit` — the original row, through the callback's path

`AdminService.ResolveDeposit(ctx, adminID, txID, outcome, reason)`: reason required
(`requireReason`, the console's rule); outcome `completed` or `failed`; the row must be a
deposit and must be pending; its `checkout_request_id` is read from metadata; and then —
the whole design in one call — `MpesaService.ResolveByAdmin(ctx, checkoutID, received,
adminID)`, which is an adapter over `settle`, exactly as `HandleCallback` and the reconciler are.

So a resolve takes the same guarded `UpdateResult`, the same `settleDeposit` transaction, the
same transactions-row → wallets-row lock order. It takes **no wallet lock of its own and runs
in no transaction of its own**. That is the property the plan's D6 named and the reason
`Adjust` cannot do this job: an adjustment is a new row beside a deposit that stays pending, and
a late callback still credits the deposit it never knew about. `ResolveDeposit` settles the
row the callback would have settled, so the callback loses its guard and credits nothing.

`ResolveByAdmin` writes `ResultCode 0` for received. For not received it writes
`ResultCodeAdminFailed = -1` — negative so it can never be mistaken for a Daraja code — with
the user-facing text *"Closed by support as not received. If M-Pesa charged you, contact support
with your phone number."*, which is what the deposit sheet's failed step shows.

**The audit row is written after the settlement, in its own transaction**, under
`models.ActionDepositResolve` over `TargetTransaction`, carrying account number, checkout id,
outcome, amount and balance type. They cannot share a transaction without breaking D6, and the
ledger row itself records `settled_by = admin` and `admin_id` — the same key `Adjust` writes,
so the console's existing `LEFT JOIN admin_users` names the operator on a resolved deposit as it
does on an adjustment. A resolve that *loses* the guard (settled between the operator's read
and their confirm) returns `ErrDepositSettled` → HTTP 409 `deposit_settled`, writes nothing and
audits nothing: the row already says who did.

`DepositResult` gains `AdminID`; `settleDeposit` writes `admin_id` on both terminal branches when
it is set, and now includes `settled_by` in the socket event.

### The console

- **The transaction sheet** shows `Settled by` (M-Pesa callback / status query to M-Pesa / an
  operator), the M-Pesa receipt when there is one, and the M-Pesa request id always — the
  reference the customer can read off their own screen and the one a deposit settled without a
  receipt will ever have.
- **A pending deposit's sheet** states what the server is already doing — *asking M-Pesa about
  it on a schedule and will credit it if M-Pesa confirms* — and offers **Mark received** and
  **Mark not received**, each through `ReasonDialog`. The received consequence says the one
  thing that makes it safe: *if M-Pesa's callback arrives later it will be ignored — this row is
  settled, so nothing can credit it twice.* The not-received consequence says the opposite risk:
  a later callback saying paid is logged as an error and credits nothing, so be sure.
- **The "stuck?" marker** keeps its ten-minute threshold and gains a title, *Open this row to
  resolve it*; the row was already the way in, and now the sheet has the action.
- **Adjust's dialog** adds one sentence: for a deposit that never got its M-Pesa confirmation
  use Mark received on the transaction itself, because an adjustment leaves the deposit pending
  and a late confirmation would credit it a second time.
- `deposit_resolve` on the audit log with its own label and filter chip.

### The deposit sheet

The comment left in `e627765` said: *when the reconciliation pass exists this becomes a
different screen: "we're checking" will be true, and it should say so then.* It exists. The
timeout step now reads:

> We haven't had confirmation from M-Pesa yet. We'll keep checking with them and credit your
> balance if the payment went through — this can take a while. Don't pay again. If it isn't
> there within a day, contact support with your phone number.

Held to the CP2/CP3 standard: it says what happens and does not promise when — the check runs
on M-Pesa's clock, and *within a day* is the give-up horizon, which is the one number the
customer can act on. The comment naming the condition is removed because the condition is met.

Two more things, both cheap: **the sheet keeps polling after it gives up**, so a customer who
left it open sees the settlement land instead of staying on "still waiting"; and the success
step says *M-Pesa confirmed the payment when we checked* or *Our support team confirmed the
payment* when `settled_by` is `query` or `admin` — §10's nice-to-have, from a field the socket
event and the transaction already carry.

### A fixture, so a stuck deposit can be produced on demand

`service.MockStuckPhone = "254700000000"`: in mock mode the simulated callback is never sent
for this one number. Every other mock deposit still settles in 4–9s. Without it the browser test
below would be racing the mock's own callback, and an operator exercising the console by hand
would have nothing to resolve. Documented at the constant; covered by
`TestMockNeverCallsBackTheStuckPhone`.

---

## 2. Tests

### Group E — `internal/service/deposit_resolve_test.go`

| Test | Pins |
|---|---|
| `TestResolveSettlesTheOriginalRowAndALateCallbackCreditsNothing` | the returned row is the original, `completed`, `settled_by = admin`, `admin_id` set, no receipt; balance 100; mpesa row `completed`/`0`; one audit row with the reason and checkout id; the console list names the admin; **then the late callback is acknowledged, the balance is still 100, `settled_by` is still `admin`, no receipt landed**; reconciled |
| `TestResolveAsNotReceivedCreditsNothing` | `failed`, user-facing reason starts *Closed by support*, mpesa row `failed`/`-1`, balance 0, audited with outcome `failed`; a later callback saying paid credits nothing and the row stays `failed` |
| `TestResolveRequiresAReasonAndAValidOutcome` | validation errors on `reason` and `outcome`; row, balance and audit untouched |
| `TestResolveRefusesAnythingButAPendingDeposit` | a settled deposit → `ErrDepositSettled`; an adjustment row → validation on `transaction`; an unknown id → `ErrNotFound`; nothing audited |
| `TestResolveLosesToACallbackThatLandsFirst` | a resolver wrapper delivers the callback *inside* the resolve's window: `ErrDepositSettled`, balance exactly the callback's 100, `settled_by = callback`, no audit row |
| `TestMockNeverCallsBackTheStuckPhone` | the fixture: still pending at 10s, nothing credited |

**Lock order — by code review, as §8 group E allowed.** `ResolveDeposit` holds no transaction
when it calls `ResolveByAdmin`; `ResolveByAdmin` is `settleAs`, which is `settle` with an admin
id; the credit is `settleDeposit`'s existing `WithTx`, unchanged since CP0. The only transaction
`ResolveDeposit` opens is the audit write, after the settlement has committed, and it touches
`admin_audit_log` alone. There is no path from this checkpoint that takes a wallet lock before a
transaction-row lock.

### `admin.spec.ts` — "resolves a stuck deposit and a late callback credits nothing"

Registers a user and a real account through the API, starts a KES 12,950 deposit to the stuck
phone, logs into the console, finds the row **by searching for the checkout id** (CP0's
`SearchAll` change, exercised end to end), opens it, reads the *asking M-Pesa on a schedule*
line, clicks Mark received, reads the *callback will be ignored* consequence, gives a reason,
confirms, and sees the toast and *An operator* on the sheet. Then it **replays the callback**
with a `ResultCode 0` body straight at `/api/v1/mpesa/callback`, which answers 200 as it must,
and checks through the user's own API that the balance is exactly $100, the transaction is
`completed`, `settled_by` is `admin`, and no receipt landed. Then the audit log, under its own
label with the reason.

Two runs to get it green, both my own: the response body read twice (`.text()` for the
assertion message, then `.json()`), and the transaction sheet — a dialog — still covering the
nav link when the test went for the audit log. It dismisses the sheet first now.

### Mutation checks

Three, on `ResolveDeposit`, each applied with the one-match assertion, built with `go vet`
before the test, and restored byte-identically.

**R1 — the Adjust anti-pattern.** `ResolveByAdmin` replaced with a call to `Adjust` for the
deposit's amount, and `won := true`:
```
TestResolveSettlesTheOriginalRowAndALateCallbackCreditsNothing
    resolve returned a3e5681c… "pending", want the original a3e5681c… completed
```
The test fails on the earliest symptom — the deposit is still pending — before it reaches the
callback. So the consequence was run separately, under the same mutation, with a throwaway test
that resolves, replays the callback and reads the balance (deleted afterwards):
```
TestR1ProbeDoubleCredit
    balance 200 after an Adjust-shaped resolve and the late callback, want 100
```
That is the number the plan's §2.8 was written to prevent, produced by the one way an operator
could have "resolved" a deposit before this checkpoint.

**R2 — a resolve that lost the guard proceeds as if it had won:**
```
TestResolveLosesToACallbackThatLandsFirst
    resolve racing a callback: got <nil>, want ErrDepositSettled
```

**R3 — the `status != pending` pre-check removed:** `TestResolveRefusesAnythingButAPendingDeposit`
**passes**. This is the correct result and worth stating: the pre-check is a courtesy that
saves a round trip and gives a clean error; the guarantee is `settle`'s guard, which loses on
the settled row and returns `won = false`, and `ResolveDeposit` turns that into the same
`ErrDepositSettled`. It is the split `settleDeposit`'s own comment describes — *the status check
above is a fast path, not the guarantee* — and the same shape as CP3's M1/M2, where removing
one of two gates leaves the money safe.

---

## 3. Verification

| Command | Result |
|---|---|
| `go build ./...`, `gofmt -l`, `go vet` (both tag sets) | clean |
| `go test ./...` | ok |
| the six group E tests | pass |
| `go test -tags integration ./internal/service/ -count=1` | **ok, 52.9s** — whole suite |
| R1, R2 | fail as quoted; R3 passes for the reason given; restored |
| `npx tsc --noEmit -p tsconfig.app.json` | clean |
| `npm run lint` (oxlint) | four pre-existing warnings, none on a changed line |
| `npx vitest run` | **130 passed** |
| `admin.spec.ts` "resolves a stuck deposit…" (desktop) | **1 passed, 6.7s** |
| `npx playwright test --project=mobile --project=desktop` | **29 passed, 3 skipped, 3.4m** — CP0's 28 plus this checkpoint's case |
| `npx playwright test --project=digit-mobile --project=digit-desktop` | **20 passed, 2 skipped, 2.3m** — CP0's count exactly |
| `python3 scripts/smoke.py` | **ALL CHECKS PASSED** |
| `python3 scripts/bot_check.py` | **AUTOMATED RUN VERIFIED** |

The browser rows were run the way CP3 established: an API started with CI's two `AUTH_RATE_*`
values, `digit_contracts_enabled` off for the CFD pair and on for the digit pair, and both flags
left `true` afterwards, `bot_real_money_enabled` `false`. The API log for the whole run has no
ERROR line; it has 63 rate-limit refusals, 38 on login and 25 on the contract-placement bucket,
every one absorbed by the client's retries — both suites and both scripts passed over them. The
API on 8080 is the rebuilt one, restarted on the default settings once the suites had finished.

---

## 4. The plan, closed out

| Plan item | Where it landed |
|---|---|
| §2.1 client, allowlist | CP1 |
| §2.2 seam, §3.1–§3.3 schema/repo/config | CP2 |
| §2.3 one `settle()`, §3.4 preconditions | CP0 |
| §2.4 the pass, §2.5 schedule, §2.9 metrics | CP3 — with give-up taking the row off the schedule rather than the plan's `continue` |
| §2.6 mock mode runs the pass | CP2 stub, CP3 exit criterion |
| §2.7 no synthesised receipt, `settled_by`, searchable checkout id | CP0 (search, `settled_by`), CP4 (shown in the console) |
| §2.8 admin resolve, audit, Adjust's note, "stuck?" marker, ERROR on succeed-after-failed | CP0 (the log line), CP4 (the rest) |
| §5 CP4's copy and the `e627765` comment | CP4 |
| §7 scoped out | on the deferred list, each with where it lands |
| §10 answers | give-up 24h, no auto-ticket (CP3); D2 (CP3); "we confirmed your payment" (CP4); the probe — still the user's to run, and still changes only §2.5's numbers and CP1's fixtures |

**Open by construction, and written down rather than hidden:** the Adjust-then-callback
double credit. `admin_adjustment` rows carry no reference to a deposit, so `settleDeposit`
cannot know one exists, and refusing on "any adjustment exists" would block legitimate credits.
Resolve is the documented path; Adjust's dialog says so. On the deferred list under its own row,
beside the after-the-fact audit write, the idempotency key, B2C, "check now" and a stuck-deposits
screen.

The plan is implemented. What is left for the user: run `scripts/daraja_probe.py` when
convenient, and set the three `MPESA_RECONCILE_*` values on the deployment if the provisional
defaults are not wanted.
