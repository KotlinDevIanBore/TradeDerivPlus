# STK Reconciliation — Implementation Plan

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Basis:** `stk-query-reconciliation-investigation.md` (§1–§5) and `stk-query-step2-findings.md`
(§3 allowlist rule, §6 provisional schedule). Nothing here re-derives what those answer.
**For:** the implementation session. Decisions in §11 are taken; reopen them deliberately.

---

## 1. Summary

### Plain language

When a customer pays by M-Pesa and Safaricom's confirmation never reaches us, the deposit sits
`pending` and nobody is looking. This plan makes the server look: a periodic pass finds deposits
that have been pending too long, asks Daraja what happened to each, and — **only when Daraja says
plainly that the customer paid** — credits the wallet through exactly the code path a callback
would have used. It never marks a deposit failed on the strength of a query, because that is the
mistake that costs a paying customer money. It also gives an operator a way to resolve the
original deposit row (rather than adjusting around it), so a late callback cannot credit a second
time.

### What is built

- **`Client.STKQuery`** in `pkg/mpesaclient` — pure protocol, same auth/password/timestamp as the
  push, returning a three-way `QueryOutcome` (Succeeded / Failed / Unresolved) under an explicit
  allowlist.
- **One settlement function** on `MpesaService` — `settle(...)` — that the callback handler, the
  reconciler and the admin resolve action all call. There is no second way to settle a deposit.
- **Two columns and one partial index** on `mpesa_requests` (`reconcile_attempts`,
  `next_query_at`) plus `MpesaRepo.ListPending` / `MarkQueried`.
- **The reconciliation pass**, in `DigitService.Start`'s shape: one pass at boot, then a ticker,
  root-context cancelled, widening backoff, give-up horizon.
- **`POST /admin/deposits/:id/resolve`**, audited, settling the original row through `settle()`.
- **Three precondition fixes** found in review (§3.4): an unconditional status write in `Deposit`
  that can overwrite a settlement, a nil-`onResult` window at boot, and `SearchAll` not matching
  the id the customer can see.

### Cost

Five checkpoints. **Medium.** The protocol client and the repo work are small; the delicate parts
are the `settle()` extraction on the money path and the test matrix, which is deliberately large.

### Biggest risk

Crediting off a query result is writing money on our own initiative rather than on Safaricom's
say-so. Everything in §2.3, §3.1 and §8 exists to make that write exactly as guarded as the
callback's, and to make a *wrong* answer from Daraja land as "wait" rather than as "failed".

---

## 2. Target architecture

### 2.1 `pkg/mpesaclient` — the query, and the allowlist

`Client.STKQuery(ctx, checkoutID string) (QueryOutcome, error)`, beside `STKPush`
(`pkg/mpesaclient/client.go:141`). It reuses `accessToken` (`:100-138`) and builds the password
and timestamp exactly as `STKPush` does (`:147-148`), posting to
`{base}/mpesa/stkpushquery/v1/query` with `BusinessShortCode`, `Password`, `Timestamp`,
`CheckoutRequestID` — the four fields Safaricom's own SDK sends (Step 2 §1).

```go
type QueryStatus int
const (
    QueryUnresolved QueryStatus = iota // Daraja answered, but not with a terminal outcome
    QuerySucceeded                     // ResultCode 0
    QueryFailed                        // ResultCode in TerminalFailureCodes
)
type QueryOutcome struct {
    Status     QueryStatus
    ResultCode int
    ResultDesc string
}
```

**Classification, in this order:**

1. Transport error, non-2xx that is not a Daraja envelope, non-JSON body → **`error`**. Daraja
   said nothing. The caller must not count this as an attempt (§2.4).
2. A Daraja `errorCode`/`errorMessage` envelope, **whatever the code** → `Unresolved`, nil error.
   This is where the disputed `500.001.1001` lands, and it lands safely whichever of its three
   readings is right (Step 2 §3).
3. A well-formed body with `ResultCode == 0` → `Succeeded`.
4. A well-formed body with `ResultCode` in `TerminalFailureCodes` → `Failed`.
5. Any other `ResultCode` → `Unresolved`.

`TerminalFailureCodes = {1, 1001, 1032, 1037, 2001}` lives in `pkg/mpesaclient` next to
`STKQuery`, **separate from `humanResult`** (`internal/service/mpesa_service.go:183-203`). They
answer different questions — "is this code final" versus "what do we tell the user" — and
coupling them would mean adding user copy before a code could be treated as terminal. A comment on
each names the other. (Review D1.)

Note the asymmetry with `STKPush`, which returns `error` for an `errorCode` envelope (`:183-186`):
for a push, an envelope means the request was refused; for a query, the same envelope is how Daraja
reports "still processing". Documented at the type.

### 2.2 The seam

An unexported interface on `MpesaService`:

```go
type stkQuerier interface {
    STKQuery(ctx context.Context, checkoutID string) (mpesaclient.QueryOutcome, error)
}
```

`*mpesaclient.Client` satisfies it directly — no wrapper. In real mode it is assigned inside the
existing `if !cfg.UseMockMpesa` block (`mpesa_service.go:41-50`); in mock mode a stub that always
returns `Unresolved` is assigned (§2.6). `SetQuerierForTest(q stkQuerier)` follows the
`SetPollInterval` / `SettleForTest` convention. `NewMpesaService(store, cfg, log)` keeps its
signature, so `cmd/api/main.go:91` and `integration_setup_test.go:140` are untouched.
(Review D2.)

### 2.3 One settlement function

Extract the body of `HandleCallback` from the `success :=` line through `onResult`
(`mpesa_service.go:112-139`) into:

```go
// settle is the only way a deposit's outcome is written. The callback, the
// reconciler and the console all come through here, so the guarded write at
// UpdateResult and the credit at settleDeposit are the ones every path shares.
func (s *MpesaService) settle(ctx context.Context, checkoutID string, resultCode int,
    resultDesc, receipt, source string) (won bool, err error)
```

`HandleCallback` becomes a three-line adapter passing `source="callback"` and
`cb.ReceiptNumber()`. The reconciler passes `source="query"` and an empty receipt; the admin
action passes `source="admin"`. `source` travels on `DepositResult` (new field) so `settleDeposit`
can write `metadata.settled_by` (§2.7).

**`settle` refuses when `onResult` is nil** — returns an error rather than winning the guard with
nowhere to send the result. Today `HandleCallback` silently skips (`:132`), which is a
mpesa-row-completed / ledger-pending / never-credited outcome if anything settles before
`NewWalletService` registers the handler (`wallet_service.go:34-40`). (Review, missed hazard 3.)

**Guards stay as they are and stay separate transactions.** `UpdateResult`
(`mpesa_repo.go:43`) is an autocommit statement; `settleDeposit`'s credit is its own `WithTx`
(`wallet_service.go:264-292`) ordering transactions-row → wallets-row. Every other writer takes
wallets first but only *inserts* ledger rows (`admin_service.go:372,389`; `Withdraw`
`wallet_service.go:347,382`), never locks an existing one, so there is no cycle. **Do not wrap the
two into one transaction, and do not let any new path take the wallet lock before
`UpdateStatusIfPending`** — that would be the first W→T path in the codebase. (Review D4, D6.)

### 2.4 The reconciliation pass

On `MpesaService`, in `DigitService.Start`'s shape (`internal/service/digit_service.go:69-86`):

```
Start(ctx):
    reconcile(ctx)                      // boot pass — catch what a restart stranded (:71)
    go ticker(MPESA_RECONCILE_EVERY) → reconcile(ctx), until ctx.Done()

reconcile(ctx):
    rows := Mpesa.ListPending(ctx, now, limit)      // next_query_at <= now, oldest first
    for row in rows:
        if now - row.CreatedAt > GIVE_UP: continue   // left for an operator; not queried
        out, err := querier.STKQuery(ctx, row.CheckoutRequestID)
        switch:
          err != nil:            log WARN; do NOT MarkQueried   // Daraja said nothing
          Succeeded:             settle(ctx, id, 0, out.ResultDesc, "", "query")
          Failed, Unresolved:    Mpesa.MarkQueried(ctx, id, attempts+1, now+backoff(attempts))
```

**`Failed` from a query is not written.** The reconciler settles only `Succeeded` (§11 D2). A
failed query result advances the backoff like `Unresolved` and the row waits for the callback, or
for give-up. This removes the "query says failed, callback says succeeded" conflict class entirely
rather than adjudicating it (§4 D8, review D7).

**`error` from the querier does not count as an attempt.** A Daraja outage must not burn
`reconcile_attempts` toward give-up (review D1).

`MarkQueried` is `WHERE status = 'pending'` so it can never touch a row a concurrent callback just
settled (review D4). `ListPending` takes a `limit` (100) so one pass is bounded.

Wiring: `mpesa.Start(ctx)` in `cmd/api/main.go` beside `digits.Start(ctx)` (`:129`), **after**
`wallet := NewWalletService(...)` (`:92`) so `onResult` is registered before the boot pass; `ctx`
is the root context from `:46` so the ticker dies on SIGTERM.

### 2.5 Schedule — provisional

| Setting | Env | **Provisional** default | Replace with |
|---|---|---|---|
| Pass interval | `MPESA_RECONCILE_EVERY` | 1m | — (operational) |
| Min age before first query | `MPESA_RECONCILE_MIN_AGE` | **3m** | earliest age at which `scripts/daraja_probe.py` shows a resolved answer |
| Backoff by attempt | fixed in code | **3m, 10m, 30m, then 1h** | whatever observed throttling permits |
| Give-up | `MPESA_RECONCILE_GIVE_UP` | **24h** | a product decision |

**These numbers are guesses and are labelled as such in the code and in `.env.example`.** The
shape — do not query immediately, widen, stop eventually — stands regardless. Running
`scripts/daraja_probe.py` (Step 2 §5) changes the two constants and the `STKQuery` test fixtures;
it changes nothing in §2.1–§2.4. **Hard floor: `MIN_AGE` is clamped to ≥ 60s** in `config.Load`,
so in mock mode the pass can never reach a deposit `simulateCallback` will settle at 4–9s
(`mpesa_service.go:147`) — this is what keeps `smoke.py` and `user.deposit.spec.ts` untouched
(review D9).

### 2.6 Mock mode

The pass **runs** in mock mode, with the querier stub returning `Unresolved` for everything. Dev
and CI then exercise listing, backoff and `MarkQueried` continuously — the parts most likely to
rot — while settlement stays with `simulateCallback`. Nothing can settle from the stub, and the
60s floor keeps the pass off any deposit the mock is about to resolve.

### 2.7 Receipt and searchability

A query-settled deposit has no `MpesaReceiptNumber`. **No receipt is synthesised** — a fake
receipt that support pastes into Safaricom's portal is worse than none (review D5). Instead:

- `settleDeposit` writes `metadata.settled_by` ∈ `callback | query | admin` on every completed
  deposit, and `metadata.receipt` only when one exists (already conditional,
  `wallet_service.go:267-269`).
- `SearchAll` (`internal/repository/transaction_repo.go:131-140`) gains
  `OR t.metadata->>'checkout_request_id' ILIKE '%' || $1 || '%'`. `Deposit` already writes that
  id into metadata (`wallet_service.go:205-206`), the customer can see it on the pending screen
  (`DepositSheet.tsx:242` shows the ref), and today it is not searchable — verified, `reference`
  is nil for deposits (only `CreateAccount`'s bonus credit at `:86` and adjustments set one).
- The admin transaction sheet shows `settled_by` alongside the receipt.

### 2.8 Admin resolve — closing §2.4 of the investigation

`POST /admin/deposits/:id/resolve` body `{outcome: "completed"|"failed", reason}` in the `actions`
group (`internal/routes/routes.go`, beside `bot/kill`). `AdminService.ResolveDeposit` requires a
reason (`requireReason`), looks the transaction up, reads its `checkout_request_id` from metadata,
and calls **`settle()`** with `resultCode` 0 or a synthetic terminal code, `source="admin"`, and the
admin id — so it inherits both guards and the T→W order. It must **not** be written as its own
`WithTx` that takes the wallet lock first (review D6). Audited as `models.ActionDepositResolve`
over `TargetTransaction`, `settleDeposit` records `admin_id` so the existing
`LEFT JOIN admin_users` (`transaction_repo.go:129-130`) surfaces the operator.

**What this closes and what it does not.** An operator who uses `resolve` settles the original
row; a late callback then loses its guard and credits nothing — closed. An operator who instead
uses `Adjust` leaves the deposit pending, and a late callback still credits — **open by
construction**, because `admin_adjustment` rows carry no reference to the deposit
(`admin_service.go:376-388`), so `settleDeposit` cannot know one exists, and refusing on "any
adjustment exists" would block legitimate credits. The plan does not pretend otherwise: `resolve`
becomes the documented path for stuck deposits, the console's "stuck?" marker
(`Transactions.tsx:15-23`) links to it, and `Adjust`'s dialog says it is for balance corrections,
not deposits.

Residual: a callback with `ResultCode 0` arriving on a row an operator resolved as **failed**.
`HandleCallback` currently logs every losing write as "duplicate" at INFO (`mpesa_service.go:128`).
It becomes: if `!won` and the incoming code is 0, re-read the row, and if its status is `failed`,
log at **ERROR** with checkout id and receipt. No metric — this is a rare, operator-visible event,
not a rate (review D7).

### 2.9 Observability

| Metric | Type | Fed by |
|---|---|---|
| `mpesa_reconcile_total{outcome}` | counter | the pass, per row: `succeeded` / `unresolved` / `failed_ignored` / `error` / `gave_up` |
| `mpesa_deposits_pending_stale` | gauge | new `Sampler.StaleDeposits()` → `Mpesa.CountPendingOlderThan(MIN_AGE)`, sampled with the others (`internal/observability/metrics.go:145-148`) |

`deposits_total{status}` (`metrics.go:35`, via `OnDepositSettled`) already counts query-settled
deposits because they go through `settleDeposit`. What a stuck queue looks like:
`mpesa_deposits_pending_stale` rising while `mpesa_reconcile_total{outcome="error"}` rises is a
Daraja outage; rising while `{outcome="unresolved"}` rises is Daraja saying "still processing" for
a long time; rising with neither is the pass not running.

---

## 3. Schema and preconditions

### 3.1 Migration `0007_mpesa_reconcile.up.sql`

```sql
-- Reconciliation state for STK pushes whose callback has not arrived. On this
-- table and not on the ledger: transactions is never updated except for the
-- pending -> terminal transition (see transaction_repo.go), and the reconciler
-- keys on checkout_request_id, which is here.
ALTER TABLE mpesa_requests
    ADD COLUMN IF NOT EXISTS reconcile_attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_query_at TIMESTAMPTZ;

-- Backfill: anything already pending is eligible now.
UPDATE mpesa_requests SET next_query_at = created_at WHERE status = 'pending' AND next_query_at IS NULL;

-- What the pass scans. Partial, like idx_digit_contracts_open_void (0005).
CREATE INDEX IF NOT EXISTS idx_mpesa_requests_pending_next
    ON mpesa_requests (next_query_at)
    WHERE status = 'pending';
```

`next_query_at` rather than `last_queried_at`: the pass filters on it directly, so the index
serves the query; and `reconcile_attempts = 0` gives the console "not looked" versus "looked, can't
tell" for free (review D3). `Insert` sets `next_query_at = now() + MIN_AGE`. 0004–0006 have no down
files; none here.

`models.MpesaRequest` (`internal/models/models.go:126-137`) gains `ReconcileAttempts int` and
`NextQueryAt *time.Time`; `GetByCheckoutID`'s select list and scan include them.

### 3.2 Repo

- `ListPending(ctx, now time.Time, limit int) ([]models.MpesaRequest, error)` — `WHERE status =
  'pending' AND next_query_at <= $1 ORDER BY next_query_at ASC LIMIT $2`. Mirrors
  `DigitContractRepo.ListAllOpen` (`digit_contract_repo.go:186`) with a bound.
- `MarkQueried(ctx, checkoutID string, attempts int, next time.Time) error` — `UPDATE … SET
  reconcile_attempts = $2, next_query_at = $3 WHERE checkout_request_id = $1 AND status =
  'pending'`.
- `CountPendingOlderThan(ctx, age time.Duration) (int, error)` for the gauge.

### 3.3 Config

`MpesaReconcileEvery`, `MpesaReconcileMinAge`, `MpesaReconcileGiveUp` via `getDuration`
(`internal/config/config.go:210`), with the 60s floor on `MinAge` applied in `Load`. Documented
in `.env.example` beside the M-Pesa block, each marked provisional.

### 3.4 Preconditions — fix before the reconciler exists

These are defects in the current callback path that the reconciler would make more likely to
bite. All three verified against the code in review.

1. **`Deposit` overwrites a settlement.** `wallet_service.go:205-206` runs an unconditional
   `UpdateStatus(tx.ID, StatusPending, {checkout_request_id})` *after* `InitiateSTK` returns. A
   settlement landing in that window — a fast live callback today, a boot-pass query tomorrow —
   is written back to `pending` with the wallet already credited. `balanceAffectingRows`
   (`reconciliation.go:27-30`) then counts 0 for it: a **permanent drift alarm that nothing can
   repair**, because every later settle attempt loses `UpdateResult` (mpesa row is already
   completed). Mock mode's 4–9s delay hides it. **Fix:** write `checkout_request_id` into
   `Metadata` before `Transactions.Insert` (`:192`) — in mock mode the id is generated locally
   anyway (`mpesa_service.go:64-66`), in real mode `InitiateSTK` must return it before the insert,
   which means reordering: initiate, then insert with the id — or, minimally, make `:205` a
   metadata-only update that does not touch `status`. The plan takes the minimal form: a new
   `Transactions.MergeMetadata(ctx, id, meta)` that never writes `status`.
2. **Nil `onResult` at boot** — §2.3, `settle()` refuses.
3. **`SearchAll` cannot find a deposit by the id the customer sees** — §2.7.

---

## 4. Decisions with reasoning

**D1 — allowlist, not denylist.** Only `ResultCode 0` settles succeeded and only
`TerminalFailureCodes` classify as failed; every envelope and every unknown code is Unresolved.
Correct under all three public readings of `500.001.1001` (Step 2 §3). User-mandated.

**D2 — a query never writes Failed.** Wrong-Succeeded costs the house money it can claw back;
wrong-Failed costs a paying customer money they must chase, and the failure codes are the least
reliably documented part. Failed deposits therefore stay pending until the callback or give-up.
The customer-facing pending state already exists (`DepositSheet.tsx`, `timeout` step). This
eliminates the failed-then-succeeded conflict class instead of adjudicating it. (Answers Step 3
item 7.)

**D3 — transport error ≠ Unresolved.** `STKQuery` returns `error` for "Daraja said nothing" and
`Unresolved` for "Daraja said in-flight". Only the latter counts as an attempt.

**D4 — one `settle()`.** Callback, reconciler and console are adapters over it. "No second way to
settle a deposit" is enforced by there being one function, not by convention.

**D5 — state on `mpesa_requests`, not `transactions.metadata`.** The ledger is append-only except
the pending→terminal move (`transaction_repo.go:17-18`); attempt counters are not that.

**D6 — no synthesised receipt.** `settled_by` + searchable `checkout_request_id` instead.

**D7 — `resolve` settles the original row.** `Adjust` remains for corrections and is documented
as not for deposits. The Adjust-then-callback double credit stays open by construction and the
plan says so.

**D8 — mock mode runs the pass** with an Unresolved stub and a 60s min-age floor.

**D9 — schedule numbers are provisional** and marked so in code, `.env.example` and here.

**D10 — the three preconditions ship first** (§3.4), in their own checkpoint, because the first
is a live drift-alarm defect regardless of whether the reconciler is ever built.

---

## 5. Checkpoints

Stop after each for review, as in every prior phase. Each writes
`docs/investigation-reports/stk-reconciliation-checkpoint-N.md`.

### CP0 — Preconditions (no reconciler)

`Transactions.MergeMetadata` and the `Deposit` fix (§3.4.1); `settle()` extracted with the
`onResult` refusal (§2.3); `source`/`settled_by` threaded through `DepositResult` and
`settleDeposit`; `SearchAll` matches `checkout_request_id`; the ERROR log for a succeeding
callback on a failed row (§2.8).

Exit: existing wallet suite green including `TestConcurrentCallbacksCreditExactlyOnce`
(`wallet_integration_test.go:426`); new tests §8 group A pass; `smoke.py` and both e2e jobs
unchanged.

### CP1 — `Client.STKQuery`

§2.1 in full, `QueryOutcome`, `TerminalFailureCodes`, and the `httptest` unit suite (§8 group B).
No service change. First `httptest` use in the repo — standard library, acceptable.

Exit: `go test ./pkg/mpesaclient/` covers every classification branch.

### CP2 — Schema, repo, seam, config

Migration 0007, model fields, `ListPending` / `MarkQueried` / `CountPendingOlderThan`, the
`stkQuerier` seam and mock stub, `SetQuerierForTest`, config with the 60s floor, `.env.example`.

Exit: repo tests §8 group C; `go run ./cmd/api` boots against a database with pending rows and
backfills `next_query_at`.

### CP3 — The pass

`MpesaService.Start`, `reconcile`, backoff, give-up, metrics and sampler, `main.go` wiring after
wallet construction. Integration tests §8 group D **including the mutation matrix**.

Exit: every group-D test passes; each single-guard mutation and the both-guards mutation
reproduce the documented failure; `scripts/bot_check.py`, `smoke.py`, both e2e jobs green; a run
with `USE_MOCK_MPESA=true` shows `mpesa_reconcile_total{outcome="unresolved"}` advancing and
`deposits_total` unchanged.

### CP4 — Console resolve, and the copy

`ResolveDeposit`, route, audit action and label, `ReasonDialog` on the transaction sheet for a
stuck deposit, `Adjust`'s copy note, the "stuck?" marker linking to it; `admin.spec.ts` case
"resolves a stuck deposit and a late callback credits nothing"; the deposit sheet's timeout copy
updated to "we're checking" now that it is true (`DepositSheet.tsx`, the comment left there in
`e627765`); deferred list updated.

Exit: §8 group E; the `e627765` comment removed because its condition is met.

---

## 6. Breakage list

| Change | Who notices | Mitigation |
|---|---|---|
| `Deposit` no longer writes `status` after `InitiateSTK` | nobody — same observable state on the happy path | the drift case in §3.4.1 stops being possible |
| `settle()` errors when `onResult` is nil | any code constructing `MpesaService` without a `WalletService` | harness always builds both (`integration_setup_test.go:140-141`); `main.go` order enforced in CP3 |
| `mpesa_requests` gains two columns | nobody | additive, defaulted, backfilled |
| `SearchAll` matches `checkout_request_id` | console search returns more | intended |
| completed deposits gain `metadata.settled_by` | nobody | additive |
| `HandleCallback` logs ERROR on a succeed-after-failed | operators | intended; was INFO "duplicate" |
| three new env vars | deployments | defaults are safe; documented as provisional |
| `/health` | unchanged | — |

**Must not change:** the guarded writes at `mpesa_repo.go:43` and `transaction_repo.go:37`;
`settleDeposit`'s T→W order; `balanceAffectingRows`; the callback route's contract of always
answering 200 (`wallet_handler.go:143-157`).

---

## 7. Scoped out — tracked elsewhere, named here

- **Idempotency key on `POST /wallet/deposit`.** "Try again" can still charge twice. Own ticket.
- **B2C withdrawals.** `Withdraw` still calls no M-Pesa API. Own ticket.
- **On-demand query from the deposit sheet** ("check now"). Optional after CP4; the pass covers
  the user who closed the tab, which is most of them.
- **A stuck-deposits list in the console.** The gauge and the "stuck?" marker are enough to
  start; a dedicated screen is a later P3.5-style item.

---

## 8. Test impact

Conventions: `newHarness`, `h.pendingDeposit` (`integration_setup_test.go:289`),
`darajaCallback` (`:303`), `h.wallet_` (`:323`), `h.assertReconciled`; concurrency shape from
`TestConcurrentCallbacksCreditExactlyOnce` (`wallet_integration_test.go:426`). A `stubQuerier`
in the test package records calls and returns a scripted `QueryOutcome`/`error` per checkout id.

**A — CP0, callback path**
- `TestDepositMetadataWriteCannotRevertASettlement` — settle between `InitiateSTK` and the
  metadata write (drive via a querier stub or a direct `settle`); status stays `completed`,
  reconciled.
- `TestSettleRefusesWithoutAResultHandler` — `MpesaService` alone; `settle` errors, row untouched.
- `TestSearchFindsADepositByCheckoutID`.
- `TestSucceedingCallbackOnAFailedRowIsLoggedNotCredited`.

**B — CP1, `pkg/mpesaclient`, `httptest`**
- `ResultCode 0` → Succeeded; each `TerminalFailureCodes` member → Failed; unknown `ResultCode` →
  Unresolved; `errorCode` envelope (`500.001.1001` and an arbitrary code) → Unresolved, nil error;
  HTTP 500 non-envelope → error; non-JSON → error; token reuse across a push then a query.

**C — CP2, repo**
- `ListPending` returns only pending rows with `next_query_at <= now`, oldest first, bounded.
- `MarkQueried` is a no-op on a settled row.
- Backfill sets `next_query_at` on pre-existing pending rows.

**D — CP3, integration, stub querier**
- `TestQuerySettledDepositCreditsOnce`.
- `TestQueryAndLateCallbackCreditExactlyOnce` — 8 goroutines, mixed callback and pass, one credit
  between them, not zero, not two; `assertReconciled`.
- `TestCallbackThenQueryIsANoop` — callback first, then a Succeeded query: `UpdateResult` loses.
- `TestUnresolvedAdvancesBackoffAndNothingElse` — attempts increment, `next_query_at` moves,
  balance unchanged; a second pass inside the backoff does not call the querier (stub counts).
- `TestQuerierErrorIsNotAnAttempt` — attempts unchanged, balance unchanged.
- `TestQueryFailedIsNotWritten` — `Failed` outcome: row stays pending, backoff advances.
- `TestGiveUpRowIsNotQueried`.
- `TestBootPassSettlesAStrandedDeposit` — pending row, fresh `MpesaService` from the same store,
  `Start`, credited once.
- `TestPassNeverTouchesAMockDeposit` — mock mode, 60s floor, `simulateCallback` settles it.
- **Mutation matrix**, per `mpesa-double-credit-fix.md` §3: remove the `mpesa_repo` guard only →
  `TestQueryAndLateCallbackCreditExactlyOnce` must still pass (transaction guard covers); remove
  the transaction guard only → still passes (mpesa guard covers); remove both → fails with a
  double credit. Remove `MarkQueried`'s guard → `TestCallbackThenQueryIsANoop` variant fails
  (row resurrected). Each recorded in the checkpoint report with the exact failure text.

**E — CP4**
- `TestResolveSettlesTheOriginalRowAndALateCallbackCreditsNothing`.
- `TestResolveRequiresAReasonAndIsAudited`.
- `TestResolveDoesNotTakeTheWalletLockFirst` — assert via the lock-order tests' pattern, or by
  code review noted in the report.
- `admin.spec.ts`: "resolves a stuck deposit and a late callback credits nothing" — seeds a
  pending deposit via the API, resolves from the console, replays the callback with `curl`,
  balance unchanged.

---

## 9. Effort

| Checkpoint | Effort |
|---|---|
| CP0 preconditions | Low–medium — touches the money path, so tested like one |
| CP1 client | Low |
| CP2 schema/repo/seam | Low |
| CP3 the pass | **Medium** — the test matrix is most of it |
| CP4 console + copy | Low–medium |

---

## 10. Open questions for the client

1. **Give-up horizon.** 24h is provisional. After it, rows are left for an operator; is there a
   customer-facing consequence you want (a notification, a support ticket auto-raised)?
2. **D2 acceptability.** A query never marks Failed, so a cancelled or timed-out deposit whose
   callback was lost stays pending until give-up, then needs an operator. Acceptable versus the
   alternative of risking a wrong Failed?
3. **Should a query-settled credit tell the user differently** ("we confirmed your payment") from
   a callback-settled one? The socket event already carries the receipt when there is one.
4. **When will `scripts/daraja_probe.py` be run?** Not blocking; it refines §2.5's two constants
   and group B's fixtures.

---

## 11. Decisions taken in this plan

1. Allowlist classification; every envelope is Unresolved (§2.1, D1).
2. A query never writes Failed (§2.4, D2).
3. Transport failure is `error`, not an attempt (D3).
4. One `settle()`; callback, reconciler and console are adapters over it (§2.3, D4).
5. `settle()` refuses without `onResult`; `Start` runs after wallet construction (§2.3).
6. Reconciliation state on `mpesa_requests`, `next_query_at` + partial index (§3.1, D5).
7. No synthesised receipt; `settled_by` + searchable `checkout_request_id` (§2.7, D6).
8. `resolve` through `settle()`, T→W preserved; Adjust-then-callback stays open by construction
   and is documented, not hidden (§2.8, D7).
9. Mock mode runs the pass with an Unresolved stub; 60s min-age floor (§2.6, D8).
10. Schedule numbers provisional, marked in code and docs (§2.5, D9).
11. Preconditions ship first as CP0 (§3.4, D10).
12. Idempotency key and B2C are out of scope and tracked (§7).
