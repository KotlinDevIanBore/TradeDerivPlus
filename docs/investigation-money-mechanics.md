# Investigation: Deposit & Withdraw Money Mechanics

**Date:** 26 September 2026 · **Branch:** `master` @ `a672bd7` (PR #14 merged)
**Kind:** read-only audit. No code changed.
**Method:** every claim carries a `file:line`. Go paths are repo-relative; web paths are under `web/`.

---

## Summary

**The money core is in better shape than most of what surrounds it.** Balances are
`NUMERIC(18,2)` throughout, never floats; every balance change happens under
`SELECT … FOR UPDATE` inside a transaction; the two async settlement paths (deposit
callback, payout result) both write through a *single* guarded UPDATE that reports whether
this caller was the one that moved the row, so a retried Daraja delivery credits once;
a partial unique index makes a second in-flight withdrawal impossible in the database
rather than merely unlikely; and there are **88 integration tests** across the money
paths, including the concurrency cases that matter.

I could not construct a balance-draining exploit. The obvious attacks — concurrent
withdrawals, replayed callbacks, withdrawing from another user's account, negative
amounts, over-debit — are each closed, and in most cases closed twice.

**The real risks are at the edges, and they are of three kinds:**

1. **A refund gap that is architectural, not a bug.** When Daraja's outcome is genuinely
   unknown, the debit stays and the payout sits `unknown` awaiting a human. That is the
   correct choice, but it means **money sitting in a state only an operator can clear**,
   and nothing pages that operator.
2. **Observability that exists but is not deployed.** Good alert rules, including a
   critical ledger-drift alert, sit in `deploy/prometheus/rules.yml` behind a
   `docker-compose` profile. Production is Railway; nothing scrapes it, and there is no
   Alertmanager anywhere. **Drift today is discovered by looking.**
3. **A frontend gate the API does not mirror in the UI's favour** — `WithdrawSheet` has
   no demo-account state, so on a demo account it renders the full withdrawal form and
   lets the API refuse. Latent today only because the platform flag short-circuits first.

One thing worth stating plainly, since it bounds this entire audit: **`withdrawals_enabled`
is `false` and no production user is `verified`, so the withdrawal path has never run
against real money.** Everything below about withdrawals is a reading of code and tests,
not of production behaviour.

---

## System Map (entry points → API → ledger → provider)

### Entry points, and whether they converge

**They converge.** Every UI path reaches the same service method; there is no second
implementation of either flow.

| Surface | File | Deposit | Withdraw |
|---|---|---|---|
| Top bar | `web/src/features/shell/TopBar.tsx:130,141` | `overlay.deposit` | `overlay.withdraw` (desktop only, added in PR #14) |
| Nav drawer | `web/src/features/shell/NavDrawer.tsx:55-56` | `overlay.deposit` | `overlay.withdraw` |
| Balance card (History panel) | `web/src/features/wallet/BalanceCard.tsx:86,92` | `onDeposit` | `onWithdraw`, `disabled={!canWithdraw}` |
| Admin console | `internal/service/admin_service.go:585` | — | `Adjust`, `ResolveDeposit`, `ResolvePayout` |

All three user surfaces call the same `overlays.ts` store, which raises one sheet mounted
once by `AppShell` (`web/src/features/shell/overlays.ts:29-35`, `AppShell.tsx:231,242`).
`DepositSheet` → `POST /wallet/deposit`; `WithdrawSheet` → `POST /wallet/withdraw`. One
handler each (`internal/handlers/wallet_handler.go:29,66`), one service method each
(`internal/service/wallet_service.go:236,520`).

**Where they diverge — and it is not in business logic.** The divergence is entirely in
*whether the control is offered*, and the three surfaces disagree:

- `BalanceCard` **hides** Withdraw on a demo account (`:91`, `{!isDemo ? … : null}`) and
  **disables** it when `canWithdraw` is false (`HistoryPanel.tsx:58-59`).
- The drawer and the top bar **always show it** and let the sheet explain.

See **Finding 3**. At the API layer there is no divergence at all: demo is refused for
both flows (`wallet_service.go:242` `ErrRealOnly`, `:541` `ErrDemoNotAllowed`).

### The two paths, in order

**Deposit** (`wallet_service.go:236-323`) — money in, nothing credited until Daraja says so:

```
OwnedAccount → real-account check → min/max KES → phone normalise+validate
  → WithTx: Accounts.GetForUpdate → frozen check          [lock taken only to read the freeze]
  → Transactions.Insert (status=pending, amount=+USD)
  → mpesa.InitiateSTK                                      [network, outside the tx]
     └─ on error: UpdateStatus(failed)  — never left pending
  → MergeMetadata(checkout_request_id)                     [metadata only, deliberately]
  → notify(transaction pending)
… later, asynchronously …
POST /api/v1/mpesa/callback → HandleCallback → settle → UpdateResult (guarded)
  → settleDeposit: WithTx { UpdateStatusIfPending → Wallets.Adjust(+) } → notify
```

**Withdraw** (`wallet_service.go:520-683`) — money out, debited *up front*:

```
flags.WithdrawalsEnabled  ← platform gate, first, before anything else
  → OwnedAccount → real-account check → user.KYCStatus == verified
  → amount > 0 → amountKES ≤ MaxWithdrawKES → phone valid → payouts != nil
  → WithTx:
       Wallets.GetForUpdate                                [the lock]
       Accounts.GetForUpdate → frozen check                [under the same lock]
       RealBalance ≥ amount                                [bonus is never withdrawable]
       WithdrawnTodayUSD + amount ≤ MaxWithdrawDayKES      [read under the same lock]
       Transactions.Insert (pending, −USD) → ErrConflict ⇒ ErrWithdrawalInFlight
       Payouts.Insert (OriginatorConversationID, phone, amount.Ceil())
       Wallets.Adjust(−)                                   [non-negative guard]
  → COMMIT, then payouts.Send outside every lock
  → re-read tx and wallet (Send may already have settled it) → notify
```

The ordering in that transaction is the design: the ledger row, the payout reservation and
the debit all commit together, so a crash cannot leave a debit whose destination number is
lost — the ledger keeps only a masked phone (`:612`), the payout row keeps the real one.

---

## Eligibility & Gating Logic

### Withdrawal — every condition, and where it lives

| # | Condition | API (authoritative) | Frontend | Agree? |
|---|---|---|---|---|
| 1 | `withdrawals_enabled` platform flag | `wallet_service.go:523-533` — **first check**, before ownership | `WithdrawSheet.tsx:89` | ✅ |
| 2 | Settings read fails | `:528` — refuses, does **not** treat as disabled | — | ✅ fail-closed |
| 3 | No flag source wired | `:524` `s.flags == nil` ⇒ refuse | — | ✅ fail-closed |
| 4 | Account ownership | `OwnedAccount` `:535` | implicit | ✅ |
| 5 | Real account only | `:540-542` `ErrDemoNotAllowed` | **absent** | ❌ **Finding 3** |
| 6 | KYC `verified` | `:544-550` | `WithdrawSheet.tsx:106` | ✅ |
| 7 | Amount > 0 | `:552-554` | `:79` | ✅ |
| 8 | Per-transaction ceiling | `:558-561` `MaxWithdrawKES` (150 000) | — | ⚠️ API only, correctly |
| 9 | Phone valid Safaricom | `:562-565` | `:80` | ✅ |
| 10 | Payout service wired | `:567-571` ⇒ refuse | — | ✅ fail-closed |
| 11 | Account frozen | `:583-588` **under the lock** | — | ⚠️ API only, correctly |
| 12 | Sufficient *real* balance | `:591-593`, plus the SQL non-negative guard | `:68` | ✅ double |
| 13 | Daily cap | `:596-605` under the same lock | — | ⚠️ API only, correctly |
| 14 | One in flight per account | **unique index**, `migrations/0008` | — | ✅ DB-enforced |
| 15 | Rate limit | `routes.go:132` — `rate.Every(2s)`, burst 5 | — | ✅ |
| 16 | **Minimum withdrawal** | **none** | none | ❌ **Finding 4** |

**No frontend-only check is load-bearing.** I checked this specifically, and it is the
question that most often has a bad answer. Every gate the sheet applies is re-applied
server-side, and several gates exist *only* server-side, which is the right asymmetry.
Calling `POST /wallet/withdraw` directly with a valid token bypasses nothing except the
copy explaining why it will fail.

The one frontend check with no backend twin is the **9-digit phone regex**
(`WithdrawSheet.tsx:80`) — but the backend normalises and validates independently
(`mpesaclient.NormalizePhone`/`ValidPhone`), so it is defence in depth, not a gap.

### Deposit — limits

| Condition | Where |
|---|---|
| Real account only | `wallet_service.go:241-243` `ErrRealOnly` |
| Min 50 KES / max 150 000 KES | `:245-250`, `config.go:276-277` |
| Phone valid | `:252-255` |
| Frozen account refused | `:259-270`, under a lock |
| Rate limit | shared `money` bucket, `routes.go:132-133` |
| No daily cap on deposits | — (deliberate; money coming in) |

`DepositSheet` sends `amount_kes: amountValue.toFixed(0)` (`DepositSheet.tsx:130`) —
integer KES — and **always deposits into `realAccount`, never the active account**
(`:129`), which is why "Top up demo" on the balance card is coherent: it routes a demo
user to their real account rather than refusing.

---

## Ledger Integrity & Concurrency

### How balance is computed

**Stored, not derived.** `wallets.real_balance` / `bonus_balance`, `NUMERIC(18,2)`
(`migrations/0001_init.up.sql:31-32`). `GetWallet` reads the row
(`wallet_service.go:206-216`); nothing sums the ledger to answer a balance query.

**The ledger is the check on it, not the source.** `TransactionRepo.Drift`
(`internal/repository/reconciliation.go:68`) compares each wallet against the sum of its
balance-affecting rows, and feeds the `ledger_drift_accounts` gauge. The predicate is the
subtle part:

```sql
-- internal/repository/reconciliation.go:35-38
status = 'completed' OR (status = 'pending' AND type = 'withdrawal')
```

A pending withdrawal counts because the debit already happened. That is consistent with
`Withdraw` debiting up front, and `WithdrawnTodayUSD` uses the same predicate
(`transaction_repo.go:224`) so the daily cap counts in-flight withdrawals —
`TestWithdrawalDailyCapCountsPendingToo` pins it.

### Concurrency — what is actually prevented

| Race | Mechanism | Test |
|---|---|---|
| Two concurrent withdrawals draining one balance | Wallet row lock + partial unique index (`0008`) + SQL non-negative guard — **three layers** | `TestConcurrentWithdrawalsYieldExactlyOnePending` |
| Retried deposit callback crediting twice | `UpdateStatusIfPending` — check and write in **one** statement (`transaction_repo.go:37-41`) | `TestFailWithdrawalRefundsExactlyOnce`, `TestConcurrentRefundsCreditExactlyOnce` |
| Refund applied twice | Same guarded-UPDATE pattern via `failWithdrawal` | `TestConcurrentRefundsCreditExactlyOnce` |
| Freeze racing a money operation | `Accounts.GetForUpdate` inside the same tx as the balance read | `TestFreezeRacesOpen`, `TestFreezeBlocksDepositAndWithdrawal` |
| Concurrent admin adjustments | Wallet lock | `TestConcurrentAdjustmentsStayConsistent` |
| Over-debit past zero | `WHERE … + $2 >= 0` in `WalletRepo.Adjust` (`wallet_repo.go:59`) | covered above |

The daily cap is read **inside** the same lock as the balance (`:596-605`), with a comment
saying why: two concurrent withdrawals must not each see the other's headroom.

### Partial failure

| Scenario | Behaviour | Verdict |
|---|---|---|
| `InitiateSTK` fails after the ledger row exists | Row marked `failed` (`:293-297`) | ✅ never left pending |
| Deposit settles between `InitiateSTK` returning and the metadata write | `MergeMetadata` writes metadata **only** — it used to call `UpdateStatus` and re-assert `pending`, reverting a settlement that landed in the gap | ✅ **fixed, and the comment records the bug** (`:299-305`) |
| Withdrawal debited, then process dies before `Send` | Payout row exists with `next_query_at`; the reconciliation pass picks it up | ✅ recoverable |
| Daraja accepts, callback never arrives | Reconciliation queries by `OriginatorConversationID`, backoff `5m→15m→30m→1h` (`mpesa_payout_service.go:81`) | ✅ |
| Daraja's queue times out (timeout URL) | `MarkUnknown` — **no refund** | ⚠️ **Finding 1** |
| Payout rejected outright at send | Refund via `settlePayout` inside `Send` (`:399-470`) | ✅ |

**There is no outbox and no idempotency key on the outbound request** — but the
`OriginatorConversationID` is generated *before* sending, inside the same transaction as
the debit (`:634`), is `UNIQUE` in the schema (`0009:13`), and is the key every callback
and query matches on. That is the function an idempotency key serves. What it does not
establish is whether **Safaricom** deduplicates a repeated `OriginatorConversationID` —
unproven, which is why nothing ever resends a payout.

### Currency and precision

Fixed-point everywhere: `decimal.Decimal` in Go, `NUMERIC(18,2)` in Postgres. No `float64`
touches a balance. The one `float64` is the config rate `KESPerUSD` (`config.go:275`),
converted via `decimal.NewFromFloat` at each use (`:726-732`) and `Round(2)`.

Two rounding observations, both in **Finding 4**: the payout amount is `Ceil`ed to whole
KES (`:637`) while the debit is the exact USD, and the incoming `amount_usd` string is
never normalised to 2dp before that arithmetic.

---

## Provider Integration & Failure Modes

### Callback authentication — the honest answer

**There is no signature verification, because Daraja does not sign callbacks.** Neither
deposit nor payout callbacks carry a shared secret, an HMAC, or a token. What authenticates
them is *correlation*: a payload is only actionable if its identifier matches a request
this system made.

- Deposit: `CheckoutRequestID` must match an `mpesa_requests` row (`mpesa_service.go:358-366`).
- Payout: `OriginatorConversationID` must match an `mpesa_payouts` row.

**Replay protection is the guarded UPDATE, not a nonce.** A replayed callback for an
already-settled row loses the `WHERE status = 'pending'` race and changes nothing. That is
sound, and it is the same mechanism that makes retries safe.

**Residual exposure, stated plainly:** an attacker who *guesses or learns* a valid
`CheckoutRequestID` while the row is still pending can settle that deposit as successful.
The IDs are Daraja-generated and not enumerable from outside, and the window is the pending
lifetime — but the only thing standing between a forged callback and a credit is knowledge
of that ID. Callback endpoints are unauthenticated by necessity (`routes.go:90-96`). A
source-IP allowlist for Safaricom's ranges is the available mitigation and is not present.

Both callback routes answer `200` unconditionally and cap the body at 1 MiB
(`wallet_handler.go:187,201`), deliberately, to avoid inviting retry storms.

**The three-route separation is the strongest single piece of design here.** Result,
timeout and status-result are three distinct routes (`:172-197`) because *which URL Daraja
posted to* cannot be recovered from the body, and it decides whether an outcome may be
acted on. A payload on the timeout channel means the queue gave up before knowing what
happened — acting on its result code could refund a customer who was paid. The comment
notes a neighbouring production system routes both to one handler.

### Where a transaction waits

| State | While | Who resolves it |
|---|---|---|
| Deposit `pending` | STK push sent, customer at their handset | Callback, else reconciliation, else operator |
| Withdrawal `pending`, payout `sent` | Daraja processing | Result callback, else the query pass |
| Payout `unknown` | Outcome genuinely unknown | **An operator only** |
| Payout past give-up | Nothing is querying any more | **An operator only** |

`mpesa_payouts_unknown` and `mpesa_payouts_given_up` gauges exist for exactly these
(`observability/metrics.go:141,148`).

---

## Transaction State Machine

### Deposit / withdrawal ledger row

```
pending ──▶ completed        (settleDeposit; completeWithdrawal)
   │
   └──────▶ failed           (InitiateSTK error; refund path; operator)
```

`completed` and `failed` are terminal. **Enforced, not assumed:** every transition goes
through `UpdateStatusIfPending`, whose `WHERE id = $1 AND status = 'pending'` makes a
transition out of a terminal state a no-op returning `won = false`
(`transaction_repo.go:37-45`). There is no `reversed` or `disputed` state — a refund is a
*new* credit plus the original marked `failed`, which `balanceAffectingRows` accounts for.

### Payout row

```
pending ──▶ sent ──▶ completed
   │         │
   │         ├────▶ failed          (refund)
   │         └────▶ unknown ──▶ completed / failed / never_received   (operator or query)
   └──────────────▶ failed          (rejected at send ⇒ refund)
```

`models.PayoutIsOpen` gates operator action (`admin_service.go:435`), and `settle`
(`mpesa_payout_service.go:568`) is the single writer — every source of an outcome
(callback, query, operator) funnels through it. `never_received` exists specifically as
the state `unknown` is not: a refund there is provably safe because Safaricom never had
the request.

**One genuine invariant hole:** nothing in the *schema* constrains `transactions.status`
or `mpesa_payouts.status` to their valid sets. Migration 0010 added `CHECK` constraints to
both KYC status columns; the money status columns have none. Today the application is the
only writer and it writes only valid values, so this is latent — but it is the same class
of gap that 0010 judged worth closing for KYC. **Finding 6.**

---

## Security Findings

### AuthZ — can user A touch user B's money?

**No, and it is checked in the right place.** Every money and ledger method resolves
ownership through `OwnedAccount(ctx, userID, accountID)` before doing anything
(`wallet_service.go:192-204`), with `userID` taken from the **verified token**
(`middleware.UserID(c)`), never from the request body:

| Endpoint | Ownership check |
|---|---|
| `POST /wallet/deposit` | `:237` |
| `POST /wallet/withdraw` | `:535` |
| `GET /wallet/transactions` | `:699` |
| `GET /wallet/transactions/:id` | `:718` — fetches by id, **then** checks the owning account |
| `GET /accounts/:id/wallet` | `GetWallet` `:207` |

The `:id` case is the one worth calling out: a user passing another user's transaction UUID
gets the row looked up and then rejected on ownership, so the response is an authorization
error rather than data. No IDOR.

### Input validation on amounts

| Input | Handling | Verdict |
|---|---|---|
| Negative | Deposit: below-minimum check. Withdraw: `≤ 0` refused (`:552`) | ✅ |
| Zero | Same | ✅ |
| Non-numeric | `decimal.NewFromString` error ⇒ 400 (`wallet_handler.go:41,78`) | ✅ |
| Scientific notation (`1e9`) | **Parses** — `shopspring/decimal` accepts exponents | ✅ caught by min/max ceilings |
| Huge (`1e100`) | Parses, then fails the max check before touching the DB | ✅ |
| Excess precision (`10.00499`) | **Not normalised.** PG rounds the stored `NUMERIC(18,2)`; the KES arithmetic uses the unrounded value | ⚠️ **Finding 4** |
| Missing fields | `binding:"required"` ⇒ 400 | ✅ |
| Bad UUID | `uuid.Parse` ⇒ 400 | ✅ |

### Audit logging

**Admin money actions are audited in-transaction.** `Adjust` writes the ledger row, the
wallet change and the `admin_audit_log` row in **one** transaction
(`admin_service.go:601-650`), with account number, amount, balance type and a mandatory
reason. `ResolveDeposit` and `ResolvePayout` audit in a separate transaction *by necessity*
— the credit belongs to the settlement's lock order — and the comment at `:296-308`
explains that the ledger row itself records `admin_id` and `settled_by`, so money is
attributed even if the process dies between the two writes. That is a documented,
reasoned trade-off rather than an oversight.

**User-initiated money movement is not audited** — there is no `admin_audit_log` analogue
for a customer's own deposit or withdrawal. The ledger row *is* the record (actor is the
owning account, timestamp is `created_at`, amount is signed), but **no before/after
balance is captured on any row**, admin or user. Reconstructing a balance at a point in
time means replaying the ledger, which is possible but not the same as a recorded
before/after. **Finding 5.**

---

## Test & Observability Coverage

### Tests — genuinely strong

| File | Tests | Kind |
|---|---|---|
| `payout_test.go` | 15 | integration |
| `payout_reconcile_test.go` | 14 | integration |
| `wallet_integration_test.go` | 11 | integration |
| `mpesa_reconcile_test.go` | 11 | integration |
| `withdrawal_guards_test.go` | 9 | integration |
| `payout_resolve_test.go` | 9 | integration |
| `concurrency_test.go` | 8 | integration |
| `deposit_resolve_test.go` | 6 | integration |
| `deposit_settlement_test.go` | 5 | integration |
| **Total** | **88** | all against a real Postgres via testcontainers |

Money-path changes in this repo have also been mutation-checked — the discipline is
recorded in `docs/kyc-r2-implementation-report.md` and was applied again in PR #14.

**The gap is end-to-end.** `web/e2e/user.deposit.spec.ts` covers a deposit in the browser
(and refuses to run unless `/health` reports `mock_mpesa: true`). **There is no
user-facing withdrawal e2e test** — reasonable, since the flag is off and no e2e user can
be `verified` without the KYC flow, but it means the withdrawal UI has never been
exercised end to end in CI. The only withdrawal in e2e is an API call in
`admin.spec.ts:194` to create a row for the console to resolve.

### Observability — built, not deployed

**What exists** (`internal/observability/metrics.go`): `deposits_total` by outcome,
`mpesa_payouts_total`, `mpesa_payout_queries_total`, `mpesa_payouts_unknown`,
`mpesa_payouts_given_up`, `mpesa_deposits_pending_stale`, B2C float balance, and
`ledger_drift_accounts`. `/metrics` is token-authenticated (`routes.go:74`) because the
numbers include deposit volumes.

**Alert rules exist and are well chosen** (`deploy/prometheus/rules.yml`): `LedgerDrift`
(`> 0` for 2m, severity critical, "Stop and investigate before processing further
withdrawals"), `DepositFailureRate` (>50% for 10m), `HighErrorRate`, `APIDown`.

**And none of it runs.** Three things, each independently sufficient to mean nobody is
notified:

1. Prometheus and Grafana are `profiles: ["observability"]` in `docker-compose.yml:25` —
   opt-in, local.
2. The scrape target is `host.docker.internal:8080` (`deploy/prometheus/prometheus.yml`) —
   a developer's laptop, not the Railway deployment.
3. **There is no Alertmanager** anywhere in the repo. A firing rule reaches Prometheus's
   own UI and stops there.

`mpesa_payouts_unknown` and `mpesa_payouts_given_up` — the two gauges that count money
needing a human — have **no alert rule at all**, even locally. **Finding 2.**

---

## Risk-Ranked Findings

### Finding 1 — A payout of unknown outcome holds a customer's debit indefinitely, and nothing escalates it · **HIGH** (reliability / user harm)

**Evidence:** `mpesa_payout_service.go:454-458` (`MarkUnknown` on an ambiguous send),
`:490-492` (timeout channel), `:525` (query says unknown); `metrics.go:141`
(`mpesa_payouts_unknown`); `rules.yml` — no rule references it.

**Failure scenario:** a customer withdraws $100. Their balance is debited immediately
(by design). Daraja's queue times out, so the outcome is genuinely unknown and the code
correctly refuses to guess — no refund, because refunding a customer who *was* paid is
worse. The payout sits `unknown`. The reconciliation pass queries with backoff and
eventually passes the give-up horizon. **The customer is now $100 down with no payout and
no refund, and the only thing that will resolve it is an operator noticing.** Nothing
pages anyone; the gauge increments into a Prometheus that is not running (Finding 2).

**Not a code defect.** The refusal to guess is right, and `never_received` exists for the
provably-safe case. The gap is that an *unknown* has no owner, no SLA and no alarm. This is
the highest-impact item because it combines money already taken with a resolution path that
depends on someone looking.

### Finding 2 — Ledger-drift and stuck-payout alarms are configured but not deployed · **HIGH** (detection)

**Evidence:** `docker-compose.yml:25` (`profiles: ["observability"]`);
`deploy/prometheus/prometheus.yml` target `host.docker.internal:8080`; no Alertmanager in
the repo; `rules.yml` has no rule for `mpesa_payouts_unknown` or `mpesa_payouts_given_up`.

**Failure scenario:** a bug causes a wallet to be credited without a ledger row.
`ledger_drift_accounts` goes to 1. `LedgerDrift` is written, correct, severity critical,
and says to stop processing withdrawals. In production **it never evaluates**, because
nothing scrapes Railway. The drift is found when someone runs the drift query by hand, or
when a customer's balance is wrong. The alert rule's own text — "Stop and investigate
before processing further withdrawals" — describes a response nobody will be prompted to
take.

**Why this ranks with Finding 1:** the invariant the whole money design rests on has a
detector that is switched off in the only environment that matters.

### Finding 3 — `WithdrawSheet` has no demo-account state, so a demo user is offered a form the API will refuse · **MEDIUM** (user harm, newly reachable)

**Evidence:** `web/src/features/wallet/WithdrawSheet.tsx` — no occurrence of `demo`,
`isDemo` or `account_type` anywhere in the file. Gates are `withdrawals_enabled` (`:89`)
then `kyc_status` (`:106`), then the form. The submit sends `account_id: account.id`
(`:173`) — the **active** account, which may be the demo one. The API refuses with
`ErrDemoNotAllowed` (`wallet_service.go:541`) → 400/403 via `response.go:55`.

**Failure scenario:** with `withdrawals_enabled = true` and a verified user on their demo
account, clicking Withdraw in the top bar or drawer opens the full withdrawal form
prefilled with their phone number. They enter an amount, confirm, and get a raw API error.
Nothing told them a demo account cannot withdraw. Contrast `DepositSheet`, which handles
this well by routing the deposit to `realAccount` (`DepositSheet.tsx:129`).

**Currently latent**, because `withdrawals_enabled === false` short-circuits first — which
is precisely why it would surface on the day the flag is flipped.

**This is the deeper answer to the `BalanceCard` question in the prior report.** The
inconsistency is *not* mirrored at the API level: there, demo is refused for both deposit
(`ErrRealOnly`) and withdrawal (`ErrDemoNotAllowed`), consistently. It is purely a UI gap —
but `BalanceCard`'s hide-entirely is the only surface that currently papers over a missing
sheet state, and PR #14 removed that cover on desktop. So the three surfaces drift in what
they *offer*, while agreeing on what happens when you act.

### Finding 4 — No minimum withdrawal, combined with `Ceil` on the payout, biases every withdrawal in the customer's favour · **LOW** (money at risk, bounded)

**Evidence:** `config.go:172-173` defines `MaxWithdrawKES` and `MaxWithdrawDayKES` — **no
minimum**. `wallet_service.go:637`: `Amount: amountKES.Ceil()`. `:552` rejects only
`≤ 0`. The incoming `amount_usd` is never normalised to 2dp before the KES arithmetic
(`wallet_handler.go:78` parses, `:84` passes straight through).

**Failure scenario:** withdraw `$0.001`. The debit is $0.001 (stored as 0.00 after
`NUMERIC(18,2)` rounding). `amountKES` = 0.1295, `Ceil()` = **1 KES** actually sent.
The business pays ~1 KES and debits ~0. Repeat per account. Similarly `$10.004` debits
10.00 but sends `Ceil(1295.518)` = 1296 KES.

**Why LOW, honestly:** the partial unique index allows one in-flight withdrawal per
account, the rate limiter allows one request per 2s, each abuse cycle requires a real
payout to complete, and Daraja's B2C has its own minimum that would likely reject a 1 KES
transfer outright. Draining anything material needs thousands of accounts and thousands of
completed payouts. It is a systematic bias and a missing validation, not an exploit.

### Finding 5 — No before/after balance is recorded on any money row · **LOW** (auditability)

**Evidence:** `models.Transaction` carries `Amount` but no balance snapshot
(`models.go`); `admin_audit_log` metadata for `Adjust` records amount and balance type,
not resulting balance (`admin_service.go:645-650`).

**Failure scenario:** a dispute over a balance three weeks ago requires replaying every
ledger row for the account and trusting that the replay matches what the wallet actually
held at the time. If drift ever occurred, the replay and the historical truth diverge and
nothing recorded distinguishes them. The `Drift` query answers "do they agree *now*", not
"when did they stop agreeing".

### Finding 6 — Money status columns have no `CHECK` constraint · **LOW** (defence in depth)

**Evidence:** `migrations/0001_init.up.sql:41-54` — `transactions.status` is a bare
`VARCHAR`; `0009:45` — `mpesa_payouts.status VARCHAR(20) DEFAULT 'pending'`, no `CHECK`.
Compare `0010_kyc_documents.up.sql:34-39`, which added exactly this for both KYC status
columns, with a comment explaining why the allowlist belongs in the schema.

**Failure scenario:** a future code path, a migration, or a manual `UPDATE` writes
`status = 'complete'` (or `'Completed'`). Every guarded UPDATE keys on
`status = 'pending'`, and `balanceAffectingRows` keys on `status = 'completed'`, so the row
silently drops out of both the settlement path and the drift calculation — a balance change
with no matching ledger contribution, which is the one class of bug `LedgerDrift` exists
to catch and this typo would hide from it.

### Finding 7 — Callback endpoints rely on identifier secrecy with no network-level restriction · **LOW/INFORMATIONAL**

**Evidence:** `routes.go:90-96` — three unauthenticated POST routes; authentication is
correlation against `CheckoutRequestID` / `OriginatorConversationID`.

**Failure scenario:** an attacker who obtains a pending `CheckoutRequestID` can POST a
success callback and have that deposit credited without paying. Requires knowing a
Daraja-generated ID inside its pending window; not enumerable externally.

**Stated as informational because Daraja does not sign callbacks** — there is no signature
to verify. The available mitigation is a source-IP allowlist for Safaricom's ranges, which
is not implemented.

### Non-findings, checked and closed

Worth recording so the next audit does not re-derive them: concurrent-withdrawal drain
(three layers); replayed callback double-credit (single guarded UPDATE); IDOR on
transactions or wallets (`OwnedAccount` on every path, `userID` from the token); negative,
zero, huge or non-numeric amounts (all rejected); float precision (none used); bonus
balance withdrawn (only `RealBalance` is checked and debited); withdrawal while frozen
(checked under the lock); deposit crediting before the callback (nothing moves until
`settleDeposit`).

---

## Recommended Follow-ups

*Not applied. Grouped by priority.*

### Before `withdrawals_enabled` is turned on

1. **Give `unknown` payouts an owner and an alarm** (Finding 1). Add alert rules for
   `mpesa_payouts_unknown > 0` and `mpesa_payouts_given_up > 0`, and write down who
   responds, how they confirm against Safaricom's portal, and the target time to resolve.
   The console already has `ResolvePayout` with `paid` / `failed` / `never_received` — the
   missing piece is human, not code.
2. **Deploy the monitoring that already exists** (Finding 2). A hosted Prometheus scraping
   Railway with a bearer token, plus Alertmanager routing `LedgerDrift` somewhere a person
   sees. Until then, consider running the drift query on a schedule as a stopgap, because
   `LedgerDrift` is the one alarm that guards the core invariant.
3. **Add a demo-account state to `WithdrawSheet`** (Finding 3), before the flag flip makes
   it reachable. It should sit above the `withdrawals_enabled` gate and say a demo account
   cannot withdraw, ideally offering a switch to the real account the way `DepositSheet`
   does. Then `BalanceCard.tsx:91` can drop its `!isDemo` hide and all three surfaces agree.
4. **Set a minimum withdrawal** (Finding 4) — `MIN_WITHDRAW_KES`, validated in `Load`
   alongside the existing ceilings, at or above Daraja's own B2C minimum. Normalise
   `amount_usd` to 2dp at the service boundary in the same change, so the debit and the
   KES arithmetic agree on the number.

### Soon after

5. **Add `CHECK` constraints to `transactions.status` and `mpesa_payouts.status`**
   (Finding 6), following `0010`'s pattern — including the `SELECT DISTINCT` pre-flight
   that migration's deployment notes use.
6. **A user-facing withdrawal e2e test.** Now that self-serve KYC exists, an e2e user can
   reach `verified`, so the full path is testable behind a flag the test sets itself. This
   is also what would have caught Finding 3.

### Worth considering

7. **Record before/after balance** on transaction rows or audit entries (Finding 5).
8. **Source-IP allowlist on the three callback routes** (Finding 7), if Safaricom
   publishes stable ranges for the deployment's region.
9. **Resolve the `OriginatorConversationID` deduplication question** with Safaricom. It is
   the one fact that would let a payout be safely resent, and its absence is why an
   ambiguous send becomes Finding 1 rather than a retry.
10. **Timezone of the daily cap.** `WithdrawnTodayUSD` uses `date_trunc('day', now())`
    (`transaction_repo.go:225`), which is the database's timezone. If that is UTC, the cap
    resets at 3am Kenyan time. Harmless but worth being deliberate about.
