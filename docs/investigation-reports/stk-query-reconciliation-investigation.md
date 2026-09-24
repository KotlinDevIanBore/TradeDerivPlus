# Investigation: STK status query and pending-deposit reconciliation

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Follows:** `daraja-live-credentials-investigation.md` §5 and `mpesa-double-credit-fix.md` §6, which
both ended at this gap.
**Status:** read-only. Nothing was changed.

**A note on sourcing.** Everything in §2, §3 and most of §4 is verified against this codebase and
cited by file and line. §1 is not — it describes Safaricom's API, which cannot be verified from
here, and the public sources disagree with each other on the one code that matters most. That
disagreement is itself a finding; see §1.3.

---

## 1. What Daraja offers

### 1.1 The endpoint

`POST {base}/mpesa/stkpushquery/v1/query`, the Lipa na M-Pesa Online Query. Its inputs are the ones
this client already computes for the push:

| Field | Where this codebase already has it |
|---|---|
| `BusinessShortCode` | `Client.shortcode` (`pkg/mpesaclient/client.go:21`) |
| `Password` | same base64 of shortcode + passkey + timestamp built at `client.go:148` |
| `Timestamp` | same format string, `client.go:147` |
| `CheckoutRequestID` | stored per request at `mpesa_requests.checkout_request_id` (`migrations/0001_init.up.sql:51`) |

Authentication is the same OAuth bearer token the push uses, so `Client.accessToken`
(`client.go:100-138`) is reusable unchanged, cache and all. **Adding the query needs no new
credential, no new config value and no new stored field.** That is the cheap part.

### 1.2 What it returns

A successful query mirrors the callback's terminal fields — `ResultCode` and `ResultDesc` — with
the same vocabulary the callback uses, which `humanResult` (`internal/service/mpesa_service.go:183-203`)
already maps: `0` success, `1032` cancelled by user, `1037` DS timeout, `2001` wrong PIN, `1` /
`1001` unable to lock subscriber. So a query result can be fed through the existing translation
without inventing a second vocabulary.

Notably it does **not** return `MpesaReceiptNumber` the way the callback's `CallbackMetadata` does.
`Callback.ReceiptNumber()` (`client.go:88-98`) has no query-side equivalent, so a deposit resolved
by query would be credited **without a receipt** unless one is fetched another way. The ledger
currently records `receipt` in metadata on success (`wallet_service.go:267-269`), and support uses
it — `SearchAll` searches on `metadata->>'receipt'` (`internal/repository/transaction_repo.go:136`).
A query-credited deposit would be invisible to that search. Worth deciding deliberately rather than
discovering later.

### 1.3 The caveat that matters, and why it is not settled

While a transaction is still in flight, the query does **not** answer "pending" in the
`ResultCode` field. It returns an error envelope, and the code in that envelope is what
distinguishes *still working* from *definitively failed*. Getting that distinction wrong is the
difference between waiting and wrongly failing a deposit the customer has paid.

**The public sources consulted for this report disagree on that code.** For `500.001.1001`, one
source reads it as the still-processing signal that warrants continued polling, another as
"insufficient funds in the business account", and a third as an internal server error usually
caused by a malformed password. Those cannot all be right.

**This must be confirmed against Safaricom's own documentation, and observed in sandbox, before any
code credits money off a query result.** It is the single external unknown that gates the whole
design, and it is not a detail — treating a still-processing response as a failure would mark paid
deposits failed, and the failure branch of `settleDeposit` is terminal.

Two further caveats, also unconfirmed and both cheap to establish in sandbox: whether Safaricom
documents a minimum wait after the push before a query is meaningful, and whether repeated polling
is rate limited. The implementation should assume both exist until shown otherwise — §4.1.

### 1.4 Confirmed absent from this codebase

Verified directly. `pkg/mpesaclient` exports exactly six functions —
`New`, `Callback.ReceiptNumber`, `Client.accessToken`, `Client.STKPush`, `NormalizePhone`,
`ValidPhone` (`client.go:41,89,100,141,194,213`) — and the only Daraja path it builds besides OAuth
is `/mpesa/stkpush/v1/processrequest` (`client.go:166`). There is no query call, and no dead or
partial implementation of one.

---

## 2. What a stuck deposit looks like today

### 2.1 Nothing touches a deposit again

Every read and write of `mpesa_requests` in the whole codebase, tests aside:

| Site | Called from |
|---|---|
| `Mpesa.Insert` (`mpesa_service.go:88`) | `InitiateSTK`, once, at send |
| `Mpesa.GetByCheckoutID` (`:107`) | `HandleCallback` |
| `Mpesa.UpdateResult` (`:123`) | `HandleCallback` |

Three, and two of them are the callback. `MpesaRepo` offers no way to list pending requests at all
(`internal/repository/mpesa_repo.go` has `Insert`, `GetByCheckoutID`, `UpdateResult` and nothing
else), so **a reconciliation pass has no query to build on and would need one added.**

So a stuck deposit is: an `mpesa_requests` row `pending` with `result_code` and `result_desc` null
for ever, and a `transactions` row `pending` with metadata carrying `amount_kes`, `rate`, a masked
phone, and the `checkout_request_id` written at `wallet_service.go:207-208`. The link from ledger
row to Daraja is there. Nothing follows it.

### 2.2 No expiry concept anywhere

Confirmed against the schema and the service layer. Neither `transactions` nor `mpesa_requests`
has any expiry, TTL or deadline column (`migrations/0001_init.up.sql:48-59`), and no service
touches them on a timer.

The codebase does have the concept — for digit contracts. `digit_contracts.void_after`
(`migrations/0005_digit_contracts.up.sql:28`) with an index for finding expired rows (`:37`), and
`DigitService.sweep` (`internal/service/digit_service.go`) running every `DigitSweepEvery` = 5s
(`:23`) plus once at boot "to catch anything that expired while the process was down"
(`:69-86`). **That is this problem, already solved once, for a different kind of pending row.** It
is the pattern to copy, and §3.1 says so.

There is one place a deposit's age is judged, and it is in the console's browser code:
`STUCK_AFTER_MS = 10 * 60_000` in `web/src/features/admin/screens/Transactions.tsx:15`, which puts
a "stuck?" marker on a deposit pending more than ten minutes (`:17-23`). The notion of stuck
exists. It exists only as a label an operator might notice.

### 2.3 What manual resolution actually is

`AdminService.Adjust` (`internal/service/admin_service.go`) takes an account id, a signed amount, a
balance type and a reason of at least ten characters, and inside one transaction writes an
`admin_adjustment` ledger row, credits the wallet, and writes an audit row.

**It does not touch the stuck deposit.** The `pending` deposit row stays pending for ever; the money
arrives alongside it under a different type. In practice an operator must:

1. notice the "stuck?" marker, or take a complaint;
2. establish out-of-band — from the customer's M-Pesa SMS, or Safaricom's portal — whether the
   money actually moved, because nothing in this system can tell them;
3. read `metadata.amount_kes` off the pending row and convert it at the rate stored in the same
   metadata, since `KES_PER_USD` may have changed since;
4. adjust the account by that USD amount, with a reason;
5. leave the deposit pending, permanently.

Step 2 is the one with no tooling behind it at all, and it is exactly what the query API would
answer.

The ledger stays consistent through this, which is worth stating because it is easy to assume
otherwise: `balanceAffectingRows` counts `completed` rows plus pending *withdrawals*
(`internal/repository/reconciliation.go:27-30`), so a pending deposit contributes nothing and the
adjustment contributes once. `ledger_drift_accounts` will not fire. **The books balance; the record
is wrong.** A permanent `pending` deposit and an unexplained adjustment describe an event that did
not happen the way they say it did.

### 2.4 A hazard that exists today

If an operator adjusts for a stuck deposit and the callback **later arrives** — delayed rather than
lost — then `HandleCallback` finds `mpesa_requests` still `pending` and wins its guard, and
`settleDeposit` finds the transaction still `pending` and wins its guard, and **credits again.**

The double-credit fix does not prevent this and was never meant to: those guards protect one row
against repeated settlement of itself. They cannot see a compensating row of a different type
written by a different service. **The account is credited twice, and the ledger reconciles both
times**, because both rows are legitimate by the predicate.

This is a live hazard in the current code, not a hypothetical of the proposed design — and it is a
direct argument for a query-driven resolution that settles the *original* row, rather than an
operator adjusting around it.

---

## 3. Design shape

### 3.1 Where it lives: both, and the job is the one that matters

This codebase has a settled pattern for periodic work: a `Start(ctx)` that does one pass
immediately and then ticks, with the goroutine owned by the service and stopped by the root
context. `DigitService.Start` (`digit_service.go:69-86`) is the closest in purpose — boot sweep,
then every 5s — and `TradingService.Start` (`trading_service.go:44`) and `SignalService.Run`
(`signal_service.go:46`) are the same shape on a minute-scale ticker. All are launched from
`cmd/api/main.go:127-137`. A reconciliation pass belongs on `MpesaService` in that shape.

**The boot pass is the part not to skip.** A process restart is one of the ways a callback goes
missing — the API is down when Safaricom posts, Safaricom retries for a while, and if the window is
missed the deposit is stranded. `DigitService`'s comment at `:71` names exactly this reason for its
own boot sweep.

An on-demand query triggered by the user is worth having as well, but as a second thing, not the
first. It resolves the impatient case in the deposit sheet; it does nothing for the user who
closed the tab, which is most of them. The job covers everyone. If only one is built, build the job.

### 3.2 What each outcome should do

| Query says | Action |
|---|---|
| **Succeeded** (`ResultCode` 0) | Settle as the callback would: `UpdateResult` then `onResult` with `Success: true`. This is the money case — a credit written on our own initiative rather than on Safaricom's say-so. Receipt is the open question from §1.2 |
| **Definitively failed** (1032, 1037, 2001, 1/1001) | Settle failed, through the same `humanResult` copy the callback uses. The user is told why instead of waiting for ever |
| **Still processing** | Leave it alone and try again later. **This is the branch that depends on §1.3 being right.** Misreading it as failure marks paid deposits failed, and that branch is terminal |
| **Query itself errored** | Leave it alone. See §4.2 |

The right seam is `MpesaService`: it already owns the Daraja vocabulary and reports outcomes to the
wallet through `onResult` (`mpesa_service.go:54-57`), and `WalletService` never learns where a
result came from. A query-sourced settlement should arrive through that same channel, so the credit
path is the one already tested — not a second one.

### 3.3 The existing guards already cover the race — verified

If a query says "succeeded" and a late callback arrives for the same deposit, both would try to
settle. **The existing guards make that safe, provided the query path goes through
`HandleCallback`'s pattern rather than around it.**

- `MpesaRepo.UpdateResult` carries `WHERE checkout_request_id = $1 AND status = 'pending'` and
  returns whether it was the call that moved the row (`internal/repository/mpesa_repo.go:43-54`).
- `TransactionRepo.UpdateStatusIfPending` does the same for the ledger row
  (`internal/repository/transaction_repo.go:37-46`), and `settleDeposit` credits only on `true`
  (`wallet_service.go:270-282`).

Whichever of the two arrives second finds no pending row, is told `false`, and credits nothing.
That is the same property `TestConcurrentCallbacksCreditExactlyOnce`
(`internal/service/wallet_integration_test.go:426`) already pins, and a query-sourced settlement is
just another concurrent caller of the same path.

**What would break it:** a query path that credits the wallet directly instead of routing through
`UpdateResult` → `onResult` → `settleDeposit`. Nothing in the current code stops someone writing
that, so it is worth saying plainly in the plan: the guarded write is the contract, and there must
be exactly one way to settle a deposit.

One genuinely new case does need deciding: a query that says **failed** while the ledger row is
still pending, followed by a late callback saying **succeeded** — Safaricom contradicting itself.
The guards make the first writer win, which means the outcome depends on arrival order. Rare, but
it should be a decision in the plan rather than an accident of timing.

---

## 4. Gaps and open questions

### 4.1 How long before querying

Two constraints pull in the same direction. Safaricom's STK prompt has its own lifetime — a user
has roughly a minute to enter a PIN — so a query before that is answering a question still being
asked. And §1.3's unconfirmed rate limit argues for restraint.

The shape that fits the evidence: **do not query within the first couple of minutes**, then query
with a widening interval, and stop after some bound rather than polling a stranded row for ever.
The existing numbers to hang it on are the client's own: the deposit sheet gives up watching after
`GIVE_UP_AFTER_MS = 120_000` (`web/src/features/wallet/DepositSheet.tsx:23`), and the console calls
a deposit stuck at ten minutes (`Transactions.tsx:15`). A first query somewhere between those two
is defensible. **A number should be chosen against observed sandbox behaviour, not argued from
first principles.**

### 4.2 When the query itself fails

**Daraja being unreachable does not mean the payment did not happen.** A failed query is an absence
of information, and the only safe reading is "still pending, ask again". Anything else invents a
fact.

The consequence: a deposit can never be failed *because* querying failed, only because Safaricom
said it failed. Which means an unbounded stuck state is still possible when Daraja is down for a
long time — the job makes stuck rows rarer, not impossible, and the honest design keeps the manual
path for the remainder. A repeated-query-failure count on the row would at least let the console
distinguish "we have not looked" from "we have looked and cannot tell", which is the difference
between an operator waiting and an operator acting.

### 4.3 The UI is currently making a promise it cannot keep

This is the sharpest user-facing finding in the investigation.

After two minutes the deposit sheet moves to a `timeout` step whose copy reads
(`DepositSheet.tsx:290-296`):

> **Still waiting on M-Pesa** — We haven't heard back yet. If you completed the payment, your
> balance will update automatically.

With no query API and a lost callback, **the balance will never update automatically.** The
sentence is true when the callback is merely slow and false when it is lost, and the user cannot
tell which they are in. The same screen then offers **"Try again"** (`:298-300`) — inviting a
second payment from someone who may have already paid, with no idempotency key on the deposit path
to make that safe.

Building the reconciliation job is what makes that copy true. Until it exists, the copy is the
thing to change: it should say the deposit is being checked and support can help, not that the
balance will fix itself.

A "we're checking" state is worth having afterwards too, but it is second-order. The first-order
problem is that the current copy tells users to stop worrying about a deposit nobody is looking at.

---

## 5. What an implementation plan should cover, in order

Not the plan — the shape it should take, and the order the dependencies force.

1. **Settle §1.3 before writing any code.** Confirm against Safaricom's own documentation, and
   observe in sandbox, what a still-processing query returns and how it differs from a definitive
   failure. Also establish the minimum wait and any rate limit. Everything downstream is a guess
   until this is fact, and the public sources contradict each other.
2. **`Client.STKQuery`** in `pkg/mpesaclient`, reusing `accessToken` and the same password
   derivation as `STKPush`. Pure protocol, no domain types, testable against a stub — the package's
   existing boundary.
3. **`MpesaRepo.ListPending(olderThan)`.** It does not exist, and nothing can iterate stuck rows
   without it. An index on `(status, created_at)` belongs with it.
4. **The reconciliation pass on `MpesaService`**, in `DigitService.Start`'s shape: one pass at boot,
   then a ticker, root-context cancelled. Every outcome routed through the existing
   `UpdateResult` → `onResult` → `settleDeposit` path so the guarded writes and the credit logic are
   the ones already tested. **No second way to settle a deposit.**
5. **Decide the receipt question** from §1.2 — whether a query-credited deposit carries no receipt,
   a synthesised marker, or something fetched separately — before it becomes a support problem that
   `SearchAll` cannot answer.
6. **Tests, in the style the double-credit fix established:** a query-settled deposit credits once;
   a query and a late callback racing credit once between them; a still-processing answer changes
   nothing; a failed *query* changes nothing. Mutation-check each guard by removing it, including
   the case where two guards mask each other — that lesson is in `mpesa-double-credit-fix.md` §3.
7. **Fix the timeout copy** (§4.3). It is a one-line change and it should not wait for the rest;
   arguably it should go first, since it is the only item that is actively misleading users today.
8. **Then, optionally**, the on-demand query from the deposit sheet and a "we're checking" state.

Out of scope but worth naming in the same plan, because they are the other half of the same
problem: there is still **no idempotency key** on the deposit request, so "Try again" can charge
twice; and **withdrawals still never call M-Pesa at all**
(`daraja-live-credentials-investigation.md` §1).
