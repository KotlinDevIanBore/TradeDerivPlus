# STK reconciliation · CP0 — preconditions

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** the three defects in the current callback path from plan §3.4, plus the `settle()`
extraction (§2.3) and `settled_by` (§2.7) that the later checkpoints build on. No reconciler, no
query, no schema change.

---

## 1. What was built

### The drift defect (§3.4.1)

`Deposit` writes `checkout_request_id` onto the ledger row after `InitiateSTK` returns. That write
went through `UpdateStatus` and re-asserted `pending` as a side effect
(`wallet_service.go`, formerly `:205-206`). A settlement landing in the gap between the push being
accepted and that write was reverted to `pending` — wallet credited, ledger pending — and it was
unrepairable: the `mpesa_requests` row was already terminal, so every later settle attempt lost
the guard the first one had consumed.

**Fixed with a method that cannot move status.** `TransactionRepo.MergeMetadata`
(`internal/repository/transaction_repo.go`) writes metadata and nothing else; `Deposit` calls it
instead. The comment on the method records why it exists.

### `settle()` — one way to write an outcome (§2.3)

The body of `HandleCallback` is now `MpesaService.settle(ctx, checkoutID, resultCode, resultDesc,
receipt, source) (won bool, err error)` (`internal/service/mpesa_service.go`). `HandleCallback` is
a four-line adapter passing `SettledByCallback`. The reconciler and the console will be two more
adapters. The guarded `UpdateResult` and the `onResult` → `settleDeposit` credit are untouched and
remain separate transactions; the T→W lock order in `settleDeposit` is unchanged.

**`settle` refuses when `onResult` is nil** — before touching the guard. `HandleCallback` used to
skip silently at that point, *after* winning `UpdateResult`, which left the mpesa row terminal and
the ledger pending for ever. The window is real at boot, between `NewMpesaService` and
`NewWalletService` registering itself (`wallet_service.go:34-40`).

### The rest

- `DepositResult.Source`, and `settleDeposit` writes `metadata.settled_by` on both terminal
  branches. Only `callback` exists today; `query` and `admin` arrive with CP3 and CP4.
- `SearchAll` matches `metadata->>'checkout_request_id'` — the reference the customer can read
  off the pending screen, and the only one a deposit settled without a receipt will ever have.
- A `ResultCode 0` arriving on a row already `failed` logs at **ERROR** with checkout id, receipt
  and transaction; every other losing write still logs the routine "duplicate" at INFO.
- `SetAfterInitiateForTest` — a hook run synchronously at the end of `InitiateSTK`, nil in
  production, so a test can land a settlement inside `Deposit`'s window rather than hope to race
  it. Same convention as `SettleForTest` / `SetPollInterval`.

---

## 2. Mutation checks

Both fixes, checked the way the double-credit fix was: put the bug back, watch the test fail with
the bug's own symptom, restore.

### Mutation 1 — `Deposit`'s write re-asserts `pending` (the code as it was)

```
deposit_settlement_test.go:56: deposit is "pending" after a settlement landed inside Deposit's
window, want completed — the follow-up metadata write reverted it
```

And with the status assertion bypassed, so the failure that surfaces is the ledger's:

```
status after Deposit: "pending"
ledger 0 disagrees with wallet cache 100
```

That second line is the drift alarm `ledger_drift_accounts` would raise in production — a
credited wallet against a ledger row `balanceAffectingRows` does not count. It is what the mock's
four-second callback delay has been hiding.

### Mutation 2 — `settle` skips silently instead of refusing

```
deposit_settlement_test.go:89: a settlement with no result handler was accepted
```

Restored after each; the five CP0 tests and both concurrent-callback tests re-run green on the
restored files.

---

## 3. Tests — `internal/service/deposit_settlement_test.go`

| Test | Pins |
|---|---|
| `TestDepositMetadataWriteCannotRevertASettlement` | a settlement inside `Deposit`'s window stays `completed`, the id and the receipt are both on the row, and the ledger reconciles |
| `TestSettleRefusesWithoutAResultHandler` | a service with no handler errors, the mpesa row is still `pending` — the guard was **not** consumed — and the real service then settles the same callback normally |
| `TestSearchFindsADepositByCheckoutID` | `AdminService.Transactions` finds a deposit by its checkout id |
| `TestSucceedingCallbackOnAFailedRowIsLoggedNotCredited` | the guard holds, nothing is credited, the row stays `failed`; the ERROR line is by inspection — the harness logs to `io.Discard` |
| `TestSettledDepositsRecordTheirSource` | `settled_by = callback` on a completed row and on a failed one |

---

## 4. Verification

| Command | Result |
|---|---|
| CP0 tests + `TestConcurrentCallbacksCreditExactlyOnce` + `TestConcurrentFailureCallbacksSettleOnce` | all pass |
| `go test -tags integration ./internal/service/ -count=1` | **ok, 38.6s** |
| `go test ./...`, `go vet` (both tag sets), `gofmt -l` | clean |
| `python3 scripts/smoke.py` (rebuilt API, mock mode) | **ALL CHECKS PASSED** |
| `npx playwright test --project=mobile --project=desktop` | **28 passed, 3 skipped** |
| `npx playwright test --project=digit-mobile --project=digit-desktop` | **20 passed, 2 skipped** |
| mutation 1 / mutation 2 | fail as quoted in §2; restored |

Flags left as found: `bot_enabled`, `digit_contracts_enabled` both `true`,
`bot_real_money_enabled` `false`.

---

## 5. Noted, not changed

`Deposit` still returns `DepositResponse{Status: pending}` and notifies `pending` even when a
settlement landed inside its window. The database is right and the client's 3s poll picks the
truth up within a beat; the response is stale for that one call. Cosmetic, and out of CP0's scope,
which was the ledger.

CP1 (`Client.STKQuery`) not started.
