# B2C withdrawals · CP0 — make today honest, and close the cheap holes

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** `b2c-withdrawals-investigation.md` §5.2 CP0. No Daraja call, no credential, no payout.
Everything here is worth doing whether or not a payout is ever built, and three of the five items
are live defects rather than preparation.

---

## 0. Before CP1: what Sesame Bot already proves

Asked to check the neighbouring project rather than re-discover its answers. **B2C is live there,
in production, and it settles most of CP1's open questions.** Read-only; env var *names* only, no
credential values, and nothing was copied out of that repo into this one.

| Question the investigation left open | What `/home/ian/sesame-bot` does | Effect on CP1 |
|---|---|---|
| **v1 or v3?** | **v3**, in production: `https://api.safaricom.co.ke/mpesa/b2c/v3/paymentrequest` (`internal/payments/mpesa.go:271`), with `OriginatorConversationID` on every request | **Settled.** Build v3 only. The design's spine — our own request id — is the one already in production use |
| The security credential's algorithm | RSA **PKCS#1 v1.5** against the public key in a Safaricom `.cer`, base64 (`internal/payments/security_credential.go`) — exactly what the SDK reading predicted | **Settled.** The investigation's §1.2 is confirmed by working production code |
| Where the credential lives at runtime | **Generated once, offline**, by `cmd/gencred`, and stored as `MPESA_SECURITY_CREDENTIAL`. The app never holds the plaintext initiator password and never reads the certificate at runtime | **Simpler than planned.** CP1 does not need the cert in the serving path — one `gencred`-style command and one env var. The investigation assumed per-request encryption |
| The certificate | `certs/mpesa_production.cer`, PEM, committed; its comment notes the cert is stable across password rotations | A cert file is needed for the generator only |
| Shortcode | `PartyA` is the **same `MPESA_SHORTCODE`** the STK push uses — one shortcode both collects and disburses | **Settled.** §4.1's "which shortcode disburses" needs no Safaricom conversation |
| Initiator | `MPESA_INITIATOR_NAME`, an env var; the API user exists on the M-PESA portal already | **Settled** operationally |
| The two callback URLs | **One handler for both**: `ResultURL` and `QueueTimeOutURL` are both `<base>/mpesa/b2c/result` (`routes.go:87`) | A choice to make deliberately, not to copy — see below |
| `CommandID` | `BusinessPayment` | Settled |
| The result payload | Top-level `Result`, with a fallback to `Body.Result`; correlated by `OriginatorConversationID`; receipt read from `ResultParameters.ResultParameter[]` where `Key == "TransactionReceipt"` | **Confirms** the investigation's §1.4 against production traffic, including the `Key`/`Value` shape |
| Transaction Status, Account Balance | **Not implemented there** | Still unproven. The probe keeps a job |

**Two things not to copy, and the reason CP1's probe still matters.**

1. **`QueueTimeOutURL` points at the same handler as `ResultURL`, and that handler treats any
   non-zero `ResultCode` as a failure** (`internal/services/withdrawal.go:146-170`). A queue
   timeout is Daraja saying *"we do not know"*, not *"it failed"* — this is the P3 hazard the
   investigation named in §2.4, and their code would refund on it. It works for them because
   their state machine and their exposure differ; it is not what this codebase should do, where
   §3.2's rule is that money never moves on an ambiguous answer. CP2 will use two distinct routes,
   or one handler that branches on which URL was hit.
2. **`ResultCode` is parsed by an `intValue` helper that accepts `float64`/`int`/`int64` and
   returns 0 for anything else** (`withdrawal.go:248-259`). A string `"1"` would therefore read as
   `0` — success. The STK work already met this and answered it with `flexInt`; CP1's B2C decoder
   must do the same, and must not default to zero.

Neither is a criticism of a system that is working; both are the kind of thing only a second
reading finds, and both are cheap to get right here. **What the probe is still for**: whether
Safaricom actually deduplicates on a repeated `OriginatorConversationID` (Sesame Bot never sends
one twice, so production has not answered it), and the Transaction Status and Account Balance
payloads, which nothing here has ever called.

§5.1's gate is closed: B2C is enabled, the shortcode disburses, the initiator exists, the cert is
in hand. That removes the long pole from the plan entirely.

---

## 1. What was built

### 1.1 `withdrawals_enabled`, seeded off

`app_settings.withdrawals_enabled`, through the same machinery as the bot flags: seeded once from
`WITHDRAWALS_ENABLED` (default **false**), never overwritten afterwards, read through the 10-second
settings cache so a flip takes effect without a deploy.

The gate is **first** in `Withdraw`, before ownership, account type or KYC: while withdrawals are
off, nothing about the request can make one happen, and a user who is told "verify your identity"
is being promised something that would not help. `WalletService` reads it through a one-method
`withdrawalFlags` seam wired by `SetWithdrawalFlags` — the constructor's signature is used in four
places and worth keeping, the same reasoning as the STK querier seam. **Nil means refuse**: a
wallet service with no flag source must not decide that the feature guarding real payouts is on.

`/auth/me` carries `withdrawals_enabled`, and the web reads it in both places that matter: the
balance card's Withdraw button is disabled with *"Withdrawals are not available yet"*, and the
sheet — reachable directly by URL — says so plainly rather than showing a form.

**Why a flag at all, when nothing can pay out anyway.** Because the only thing standing between a
real customer and a withdrawal that takes money and pays nobody is that identity verification is
admin-only and has no user flow. That is an accident of an unfinished feature, not a guard, and it
is one console click from untrue.

### 1.2 One withdrawal in flight per account

Migration `0008_withdrawal_guards.up.sql`: a **partial unique index** on
`transactions (account_id) WHERE type = 'withdrawal' AND status = 'pending'`, the shape
`idx_bot_runs_one_live_per_account` uses for the same "one live thing per account" rule. `Withdraw`
maps the resulting conflict to `ErrWithdrawalInFlight` → HTTP 409.

This is the cheapest of §3.3's three layers and the strongest: today a double-tap makes two
harmless debits, but the moment B2C exists it makes two real payouts, and cash paid twice to a
customer cannot be recovered by any Daraja API — Safaricom's own documentation says B2C reversals
are manual. The database now makes it impossible rather than the code making it unlikely, so no
future caller can forget.

### 1.3 Ceilings

`MAX_WITHDRAW_KES` (150,000, mirroring the deposit ceiling) per transaction and
`MAX_WITHDRAW_DAY_KES` (300,000) per account per day, both in KES — the currency the customer is
paid in and the one Daraja's limits are written in — and both validated before anything is
debited. Before this a withdrawal's only bounds were "greater than zero" and the balance, while
B2C's own limit rules would have refused the payout *after* the debit. `Load` refuses a
configuration where either is non-positive or the daily cap is below the per-transaction one.

The daily total counts pending alongside completed, through the same `balanceAffectingRows`
predicate the ledger uses. Both figures are provisional and a product decision; they are marked so
in the config comment, in `.env.example` and on the deferred list.

### 1.4 The refund arithmetic — option A, and the comment that contradicted it

The investigation found `balanceAffectingRows` carrying two incompatible descriptions of a refund:
its comment said *"a failed withdrawal is refunded by a compensating row rather than by deleting
this one"*, while the predicate itself stops counting a row the moment it turns `failed` — which
hands the amount back to the ledger total on its own. Doing both would credit the ledger twice and
raise the drift alarm the predicate feeds.

`WalletService.FailWithdrawal` is now the only refund path, and it is exactly two writes: the
guarded `UpdateStatusIfPending` to `failed`, then `Wallets.Adjust(+amount)`, in one transaction.
**No third row.** The predicate's comment now says the same thing from the other side, and points
at the function.

Two properties it inherits deliberately:

- **Transactions-row then wallets-row**, matching `settleDeposit`. `Withdraw` itself takes the
  wallet lock first and is legal only because its ledger write is an `INSERT` of a new row;
  copying that order here — the instinct, since `Withdraw` is the nearest code — would make this
  the first wallets-then-transactions path in the codebase and deadlock it against every deposit
  settlement. The function's comment says so.
- **The guard decides, not a read.** It reports whether *this* call performed the refund, so a
  payout result and its retry, or a result and an operator, cannot both credit.

### 1.5 The dashboard tile that was always zero

`TodayTotals` filtered `status = 'completed'`, and nothing in this codebase can move a withdrawal
off `pending` — so `withdrawals_today` and `withdrawals_today_usd` were **structurally zero while
real balances were being debited**, shipped on the admin dashboard. It now counts
`balanceAffectingRows`: for deposits the two predicates agree, for withdrawals they do not, and
one definition means the dashboard cannot disagree with the ledger or the drift gauge about what
has moved.

---

## 2. Tests

### `internal/service/withdrawal_guards_test.go` (new, integration)

| Test | Pins |
|---|---|
| `TestWithdrawalsAreRefusedWhileTheFeatureIsOff` | flag off → `ErrWithdrawalsDisabled`, balance untouched, zero ledger rows; an unverified demo user gets the same platform answer rather than a hint about themselves; flag on → the request reaches the real gates |
| `TestWithdrawalRefusedWithNoFlagSourceWired` | a wallet service with no flag source refuses; fail closed |
| `TestWithdrawalCeilings` | over the per-transaction ceiling → validation error on `amount`, nothing written; exactly at the ceiling is allowed |
| `TestWithdrawalDailyCapCountsPendingToo` | a pending $100 plus a requested $100 breaches a $150 cap; a $10 request has headroom and meets the index instead; a refunded withdrawal stops counting |
| `TestOnlyOneWithdrawalInFlightPerAccount` | the second is refused, the balance moved once, one row exists; settling frees the slot; another account is unaffected |
| `TestFailWithdrawalRefundsExactlyOnce` | `failed`, reason and `settled_by` on the row, balance restored, **no extra ledger row**, reconciled; a second refund is an acknowledged no-op that credits nothing and does not rewrite the row |
| `TestConcurrentRefundsCreditExactlyOnce` | eight concurrent refunds across five withdrawals: exactly one claims the credit each time, balance exact, reconciled |
| `TestTodayTotalsCountsPendingWithdrawals` | a pending withdrawal counts; a refunded one stops; deposits unchanged |

### The concurrency test that had to change its mind

`TestConcurrentWithdrawalsCannotOverdraw` asserted that ten concurrent $100 withdrawals against a
$500 balance produced **exactly five** successes — the overdraw guard holding under contention.
With the one-in-flight index that race is unreachable, and the old assertion is now an assertion
that the constraint is *absent*: under mutation M1 it passes with five successes, and with the
constraint it sees one.

It is rewritten as `TestConcurrentWithdrawalsYieldExactlyOnePending`, asserting one success and a
single debit. **The overdraw guard has not stopped mattering, it has stopped being reachable from
there**, so the test now also drains the balance sequentially — refunding between rounds so the
index is free — and asserts that the attempt which would take the account negative is refused with
`ErrInsufficientFunds` and that no balance ever goes negative. Losing coverage of a money guard
because a new constraint masks it is exactly the failure the double-credit fix warned about.

### Mutation checks

Seven, each applied by a script that asserts exactly one match and prints `APPLIED`, each built
with `go vet` before its test ran, each restored byte-identically.

| # | Mutation | Result |
|---|---|---|
| M1 | the unique index made non-unique | `TestOnlyOneWithdrawalInFlightPerAccount`: `second withdrawal returned <nil>, want ErrWithdrawalInFlight`; and `TestConcurrentWithdrawalsYieldExactlyOnePending`: **`5 of 10 concurrent withdrawals succeeded, want exactly 1`** — the old test's exact expectation, which is the clearest evidence that the old assertion measured the absence of this constraint |
| M2 | the platform flag gate removed | `withdrawal with the feature off returned <nil>, want ErrWithdrawalsDisabled` |
| M3 | fail **open** when no flag source is wired | `unwired wallet returned <nil>, want ErrWithdrawalsDisabled` |
| M4 | **option C** — the refund also inserts a compensating row | `the refund wrote 1 extra ledger rows; a compensating row would credit the ledger twice` |
| M5 | the refund ignores the guard | `8 of 8 refunds claimed to have credited, want exactly 1`, and the duplicate-refund assertion |
| M6 | `TodayTotals` back to `status = 'completed'` | `a pending withdrawal shows as 0 totalling 0, want 1 totalling 100` |
| M7 | the daily cap counts only completed | **survived the first time** — see below |

**M7 is the one worth reading.** It passed, and the reason was the test, not the code. The first
version of `TestWithdrawalDailyCapCountsPendingToo` flipped its pending withdrawal to `completed`
by hand to free the one-in-flight index — so the row counted under either reading and the test
could not tell them apart. A test that cannot fail on the defect it names is not evidence, so it
was rewritten rather than recorded as a pass.

What distinguishes the two readings is the **order** of the two refusals: the cap is checked before
the `INSERT` the index guards, so with $100 pending and a $150 cap a second $100 request is over
the cap if pending counts, and merely a second in flight if it does not. Two different errors, and
the strengthened test asserts the first. M7 re-run against it:

```
a second $100 against a $150 daily cap with $100 already pending returned
A withdrawal is already on its way. Wait for it to finish before starting another,
want a validation error on amount — the cap is not counting the pending withdrawal
```

Also recorded: **M3's first form did not compile** — it called a helper that does not exist — and
was restored and rewritten build-safe before being trusted. That is the third instance in this
project of a mutation that looked like evidence and was not, and the `go vet`-before-test step is
what caught it.

---

## 3. Verification

| Command | Result |
|---|---|
| `go build ./...`, `gofmt -l`, `go vet` (both tag sets) | clean |
| `go test ./...` | ok |
| the eight new tests + the rewritten concurrency test | pass |
| `go test -tags integration ./internal/service/ -count=1` | **ok, 48.7s** — whole suite |
| M1–M7 | as tabled; each restored byte-identical |
| `npx tsc --noEmit -p tsconfig.app.json` | clean |
| `npm run lint` | one pre-existing warning in `WithdrawSheet.tsx:43` (the phone-prefill effect), untouched by this work |
| `python3 scripts/smoke.py` | **ALL CHECKS PASSED** |
| `npx playwright test --project=mobile --project=desktop` | **29 passed, 3 skipped, 4.2m** |
| `npx playwright test --project=digit-mobile --project=digit-desktop` | **20 passed, 2 skipped, 2.4m** |
| `python3 scripts/bot_check.py` | **AUTOMATED RUN VERIFIED** |

### Against the live API

Rebuilt and booted on the existing development database:

```
seeded app setting key=withdrawals_enabled value=false
```

| Check | Result |
|---|---|
| migration 0008 applied, index present | `idx_transactions_one_pending_withdrawal` |
| the flag seeded on a database with history | `withdrawals_enabled false`, other four flags untouched |
| `/auth/me` | `withdrawals_enabled: false` alongside the existing flags |
| `POST /wallet/withdraw`, flag off | **403 `withdrawals_disabled`** |
| flag flipped true, no restart, 11s later | **403 `kyc_required`** — the flip took effect through the cache, and the ordering is as designed: the platform answers first, then the user's own gates |
| flag restored | `false` |

Both browser pairs match the counts they have had since STK CP4, run the way that checkpoint
established: CI's two `AUTH_RATE_*` values, and `digit_contracts_enabled` off for the CFD pair and
on for the digit pair. Flags left as found: `bot_enabled` and `digit_contracts_enabled` true,
`bot_real_money_enabled` and `withdrawals_enabled` false. The API on 8080 is the rebuilt one.

**One migration-safety note.** The unique index cannot be created on a database where any account
already has two pending withdrawals, and nothing has ever been able to settle one. The development
database has no such account (checked: zero accounts with more than one pending withdrawal), but a
deployment with history might, and the migration would fail there rather than silently pick a
winner. That is the right failure — it needs a person to decide which withdrawal is real — but it
should be checked before deploying:

```sql
SELECT account_id, count(*) FROM transactions
WHERE type = 'withdrawal' AND status = 'pending'
GROUP BY account_id HAVING count(*) > 1;
```

Two harness notes, both the same shape as CP2's min-age floor: the harness builds `config.Config`
directly, so `Load`'s validation never runs on it and the new ceilings had to be set there
explicitly (they defaulted to zero, which refused every withdrawal and was how the omission
surfaced); and the harness seeds `withdrawals_enabled` **true**, because the existing withdrawal
tests are about KYC, overdraw and freezing, all of which the platform flag would short-circuit.

**`smoke.py` changed.** Its one withdrawal check asserted `kyc_required`; with the platform gate
first, a smoke run against a database where withdrawals are off gets `withdrawals_disabled`. It now
accepts either and names which it saw. The KYC gate keeps its coverage in the integration suite,
where the flag can be turned on — a better home for it than a script that cannot control flags.

---

## 4. What CP1 inherits

- **v3, confirmed in production**, with the `OriginatorConversationID` design already proven next
  door. CP1 builds no v1 path.
- **The credential is an offline artefact**: a `gencred`-style command plus one env var, not
  per-request RSA in the serving path. The certificate belongs to the generator.
- **One shortcode** collects and disburses, so no new shortcode configuration.
- `FailWithdrawal` is the refund CP2 will call on an explicit B2C failure — already written,
  already the right lock order, already mutation-checked. CP2 adds the success path, which moves
  no money, and the payout table.
- Two hazards to design against rather than inherit: the timeout-as-failure collapse, and a result
  code parsed with a zero default.
- What the probe is still for: whether Safaricom deduplicates a repeated
  `OriginatorConversationID`, and the Transaction Status and Account Balance payloads.

CP1 not started.
