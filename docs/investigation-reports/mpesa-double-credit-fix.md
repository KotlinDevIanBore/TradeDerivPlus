# M-Pesa: crediting a deposit once, however many callbacks arrive

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default` · **Commit:** `1070d22`
**Origin:** §3 of `daraja-live-credentials-investigation.md`, which found this while surveying what
switching to live Daraja credentials would require.
**Scope:** the double-credit race, and a guard on the two scripts that would put a real charge on
somebody. No deployment config was touched and no credential was written anywhere.

---

## 1. The defect

A deposit is credited when Safaricom POSTs a callback. Two guards stood between a repeated
callback and a repeated credit, and **both were an unlocked read followed by a separate write**:

- `MpesaService.HandleCallback` read `mpesa_requests.status`, returned early if it was not
  `pending`, and otherwise called `UpdateResult` — whose `UPDATE` had no status predicate.
- `WalletService.settleDeposit` read `transactions.status`, returned early if it was not
  `pending`, and otherwise credited the wallet inside a transaction whose `UpdateStatus` was
  likewise unpredicated.

Two callbacks for one push, arriving together, could both read `pending` before either wrote. Both
then proceeded, and both reached `Wallets.Adjust`.

**This is not a hypothetical concurrency shape.** Daraja retries callbacks, and a retry is
precisely the case where two deliveries overlap. The endpoint that receives them
(`internal/routes/routes.go`) is unauthenticated and unrate-limited, so a retry storm is not
throttled either.

It had never been observed because the mock delivers each callback exactly once, from a single
goroutine (`internal/service/mpesa_service.go:146-179`). The existing test for duplicates fired
them **sequentially** (`wallet_integration_test.go`, "Daraja retries. A repeated callback must not
credit twice") — which the unlocked read handles perfectly well. The gap was only ever visible
under concurrency, and nothing ran the callbacks concurrently.

---

## 2. The fix

The check and the write became one statement, in both places.

| Method | Now |
|---|---|
| `MpesaRepo.UpdateResult` (`internal/repository/mpesa_repo.go:43`) | `WHERE checkout_request_id = $1 AND status = 'pending'`, returning `(bool, error)` |
| `TransactionRepo.UpdateStatusIfPending` (`internal/repository/transaction_repo.go:37`) | new; `WHERE id = $1 AND status = 'pending'`, returning `(bool, error)` |

The boolean is the point, not the predicate alone. A guarded `UPDATE` that still let its caller
continue would prevent the second status write and not the second credit. So each caller now acts
only on `true`:

- `HandleCallback` (`internal/service/mpesa_service.go:123-130`) no longer decides from its
  earlier read. It writes, and calls `onResult` only if the write was its own.
- `settleDeposit` (`internal/service/wallet_service.go:237-244` for the failure branch,
  `:264-292` for the credit) moves the ledger row and credits the wallet in one transaction, with
  the move conditional on the row still being pending. A second delivery that got past the read
  finds nothing to move and credits nothing.

`TransactionRepo.UpdateStatus` is left in place unguarded (`transaction_repo.go:48`) and still
used by the pre-callback paths in `Deposit`, which set a row's initial state rather than
racing for it.

### Two guards, deliberately

Either one alone is sufficient today — §3 shows that directly. Both are kept because this is
money, and because the two protect different things: the first stops a second *settlement* being
announced to the user, the second stops a second *credit*. A future caller of `settleDeposit` that
does not come through `HandleCallback` would be covered by the second and not the first.

The fast-path read at `wallet_service.go:232` survives as a fast path and is commented as one. It
skips work in the common case and guarantees nothing.

---

## 3. Verification

### The tests

Two, in `internal/service/wallet_integration_test.go`:

- **`TestConcurrentCallbacksCreditExactlyOnce` (`:426`)** — eight goroutines released together on
  one `CheckoutRequestID`, repeated over five successive deposits. Asserts the balance is exactly
  `$100 × deposits` rather than eight times it, that the transaction reached `completed`, and
  `assertReconciled` after each — so a credit with no matching ledger row would be caught here
  even if the balance happened to look right.
- **`TestConcurrentFailureCallbacksSettleOnce` (`:483`)** — the same race on the failure path.
  No money can be created there, so what is counted is settlement notifications: one per deposit,
  not one per delivery.

Both release their callers from a shared channel so they contend rather than queue.

### Mutation, three ways

| Mutation | Result |
|---|---|
| `mpesa_repo`'s guard removed only | **pass** — the transaction guard covers it |
| `transaction_repo`'s guard removed only | **pass** — the mpesa guard covers it |
| **both removed — the code as it was** | **fail**: `after 8 concurrent callbacks on deposit 2 the balance is 900, want 200` |

$900 where $200 is correct: a 4.5× overcredit, from traffic Safaricom generates on its own.

**The first two rows are worth as much as the third.** A mutation check that removed one gate would
have reported the tests green and certified a fix that was half present. This is the same lesson as
the kill switch in Phase 3 CP4, where removing only the goroutine's broadcast left the test passing
while the user was still told twice — and it is why the check has to reproduce the *original*
state, not merely perturb the new one.

### The failure-path test had to be strengthened

Its first version used a single deposit and passed under full mutation — eight callers on one short
code path did not reliably open the window. Looped over five deposits it fails with
`26 settlement notifications for 5 failed deposits, want one each`. A test that cannot fail on the
defect it names is not evidence, and it was rewritten rather than kept as decoration.

---

## 4. The scripts that would have charged somebody

The same investigation found that `scripts/smoke.py` and `web/e2e/user.deposit.spec.ts` both
request a **KES 12,950** deposit against a phone number they generate at random. Against live
Daraja credentials that is an STK push to a stranger for about thirteen thousand shillings — it
would not charge the operator, it would prompt a member of the public to pay them — once per run,
and `user.deposit.spec.ts` runs on every CI push.

Neither could tell the difference. The API and the UI are identical in both modes; only the
`ws_CO_MOCK_` prefix on the returned `CheckoutRequestID` gives it away, and by then the push has
been sent.

**`/health` now reports `mock_mpesa`** (`internal/routes/routes.go:55-63`). Both scripts **ask the
target** rather than reading local environment, which says nothing about a remote API, and both
**fail closed**: unreachable, or any answer other than a plain `true`, is treated as live.

- `smoke.py` probes in `mpesa_is_mocked()` (`:61`) and gates the push at `:229`. The rejection
  checks either side of it still run — a demo account, a below-minimum amount and a non-Safaricom
  number are all refused before `InitiateSTK`, so none of them reaches Daraja. The refusal records
  a failure, so the run exits non-zero: a script that quietly passes by testing nothing is the
  outcome this was written to avoid.
- `requireMockMpesa` (`web/e2e/helpers.ts:198`) does the same for Playwright, at file scope in
  `user.deposit.spec.ts:6` so it fails the suite before any test registers a user.

On the disclosure question: `/health` is unauthenticated, and the field says whether M-Pesa is
simulated. On a real deployment the answer is `false`, which is what anyone would assume; on a
development one it reveals that it is a development one. The same fact is already observable to any
authenticated user from the `ws_CO_MOCK_` prefix, and the API already logs it at boot.

### Verified against a live-configured API

An API was started on a spare port with `USE_MOCK_MPESA=false`, obviously fake credentials, and
`MPESA_BASE_URL` left at sandbox — so that even a failure of the guard could not have reached
production Daraja.

```
$ curl localhost:8081/health
{"mock_mpesa":false,"status":"ok"}
```

- `smoke.py` → `[STOP] this API is not in mock M-Pesa mode, so the deposit was not sent.` and the
  number it would have pushed to, named. Exit code **1**.
- `user.deposit.spec.ts` → the suite fails before its first test, naming the cause and the fix.
- **That API's log shows nothing reached Daraja at all** — no STK attempt, no auth attempt.

---

## 5. Commands

| Command | Result |
|---|---|
| `go test -tags integration ./internal/service/ -count=1` | **ok, 36.3s** |
| `go test ./...` | ok |
| `go vet ./...`, `go vet -tags integration ./...`, `gofmt -l` | clean |
| `python3 scripts/smoke.py` (mock target) | **ALL CHECKS PASSED** |
| `python3 scripts/smoke.py` (live-configured target) | refused, exit 1, nothing sent |
| `npx playwright test --project=mobile --project=desktop` | **28 passed, 3 skipped** |
| `npx playwright test --project=digit-mobile --project=digit-desktop` | **20 passed, 2 skipped** |
| `npx tsc --noEmit`, `npm run lint` | clean, exit 0 |
| mutation, three ways | §3 |

---

## 6. What this does not fix

From the same investigation, still open and more important than anything above before real money
moves:

1. **Withdrawals never call M-Pesa.** `WalletService.Withdraw` debits the balance and writes a
   `pending` row; there is no B2C call anywhere, and `pkg/mpesaclient` implements STK push only. A
   live withdrawal takes money out of the app and pays nobody.
2. **There is no STK status query.** A lost callback strands a paid deposit as `pending` for ever —
   no reconciliation job, no retry, no resolution short of a manual admin adjustment. This fix
   makes a *repeated* callback safe; it does nothing for an *absent* one, which is the other half
   of the same production behaviour.
3. **No idempotency key on the deposit request.** A double-tapped button is still two real charges.
   The guard here is between Safaricom and the ledger, not between the user and Safaricom.
4. **The callback endpoint is still unauthenticated and unrate-limited**, and crediting is still
   keyed solely on `CheckoutRequestID`.

(2) is the one to do next. Everything in this report concerns a callback arriving more than once;
none of it helps when one never arrives at all.
