# Digit Contracts — Phase 0 + Phase 1 Implementation Report

**Scope:** Feature-flag plumbing and the backend vertical slice only. **No digit trading UI** beyond a placeholder on `/` when the flag is on. Phase 2 (ticket, digit row, chart wiring) is a separate prompt.

**Source of truth:** [`docs/investigation-reports/digit-contracts-investigation.md`](../investigation-reports/digit-contracts-investigation.md).

**Commits:** `ae46e25` → `ed1d0d6` (flag → stub → schema → service → tests).

---

## 1. Summary

You can now flip the app between the existing CFD surface and a **“Digit contracts coming soon”** stub without redeploying, and you can **open and auto-settle digit contracts over the API** against the mock feed: debit stake, wait for the next tick (or sweep), credit `stake × (1 + payout_ratio)` on a win or keep the debit on a loss, or **auto-void and refund** if no settlement tick arrives within five seconds.

There is still **no real digit order ticket, digit row, or Positions UI** for this product. CFD trading is unchanged when the flag is off.

---

## 2. Per-step outcome

### Step 1 — Flag storage and bootstrap ✅

- Migration `0004_app_settings.up.sql`: key/value `app_settings`.
- Env `DIGIT_CONTRACTS_ENABLED` (default `false`) seeds the row **only when missing**.
- `SettingsService` caches `digit_contracts_enabled` for **10 seconds**.
- `GET /api/v1/auth/me` returns `digit_contracts_enabled` alongside the user.

### Step 2 — Flip without redeploy ✅

Verified against a live API process (no restart):

| Moment | `digit_contracts_enabled` |
|---|---|
| After seed | `false` |
| Immediately after SQL `UPDATE … value = 'true'` | `false` (still in TTL cache) |
| After ~11s | `true` |
| Flip back + wait | `false` |

**Operational SQL** (until an admin UI exists):

```sql
-- Enable digit surface
UPDATE app_settings
SET value = 'true', updated_at = now()
WHERE key = 'digit_contracts_enabled';

-- Restore CFD surface
UPDATE app_settings
SET value = 'false', updated_at = now()
WHERE key = 'digit_contracts_enabled';
```

Wait up to ~10s (API cache) and ~10s (web `useMe` poll), or reload the app.

### Step 3 — Minimal frontend branch ✅

- `MainSurface` on `/` reads `useMe().digit_contracts_enabled`.
- On → `DigitSurfaceStub` (“Digit contracts coming soon”).
- Off → existing `TradeScreen` unchanged.
- `useMe` staleTime/refetchInterval **10s** so a SQL flip shows without rebuilding the web bundle.

### Step 4 — Schema ✅

`0005_digit_contracts.up.sql`: full field list from the investigation, plus `void_after` for the grace window. Ledger types `digit_open` / `digit_settle` (constants on `models`).

### Step 5 — `lastDigit` ✅

`models.LastDigit(price, pipSize)`: format with `StringFixed(pipSize)`, take the last `0–9` character. Documented in code for the Phase 2 TypeScript port. Unit-tested.

### Step 6 — Open ✅

`POST /api/v1/digit-contracts`: volatility-only, validates selection, snapshots payout from the static table, debits via the same bonus-then-real / freeze / `WithTx` pattern as CFD opens, records entry price/digit/epoch from `feed.Latest`.

### Step 7 — Tick settler ✅

`DigitService.Start` registers `OnTick`. Settles when `tick.Epoch > entry_tick_epoch` (next tick). Win credit = `WinCredit(stake, ratio)`. Idempotent `GetOpenForUpdate`. WS events: **`digit_opened`**, **`digit_settled`** (not `trade_closed`).

### Step 8 — Sweep + check-on-read + void ✅

- Sweep every **5s**; **immediate sweep on Start** (restart recovery).
- **Grace `DigitVoidGrace = 5s`**: if still open, no tick with epoch &gt; entry, and `now > void_after` → void, refund stake, `log.Warn` with contract id, symbol, waited duration.
- List/Get of open contracts call `EnsureSettled` (check-on-read).

### Step 9 — List/summary ✅

- `GET /digit-contracts?account_id=&status=&limit=&page=`
- `GET /digit-contracts/:id`
- `GET /digit-contracts/summary?account_id=` → open count, won/lost/void counts and session PnL over the last 24h.

### Step 10 — Tests ✅

Integration suite (`go test -tags integration ./internal/service/ -run Digit`): all cases below green. Full `./internal/service` integration package also green.

---

## 3. Files changed (high level)

| Area | Files |
|---|---|
| Migrations | `0004_app_settings.up.sql`, `0005_digit_contracts.up.sql` |
| Models | `digit.go`, `digit_contract.go`, `digit_payout.go`, `digit_test.go`, `models.go` (tx types) |
| Repo | `settings_repo.go`, `digit_contract_repo.go`, `store.go`, `transaction_repo.go` |
| Service | `settings_service.go`, `digit_service.go`, `deriv_feed_service.go` (InjectTickAt/Quiet) |
| HTTP | `auth_handler.go` (me), `digit_handler.go`, `response.go`, `routes.go`, `cmd/api/main.go` |
| Config | `config.go`, `.env.example` |
| Web stub | `MainSurface.tsx`, `DigitSurfaceStub.tsx`, `App.tsx`, `useQueries.ts`, `types` |
| Tests | `digit_integration_test.go`, harness + admin ledger-type check |

---

## 4. Placeholder payout table

**Win credit formula (locked):** `stake × (1 + payout_ratio)` total on win. Loss: stake remains debited; settle ledger row amount `0`.

| Contract | Selection | `payout_ratio` | Example $10 win credit | Status |
|---|---|---|---|---|
| Even/Odd | even or odd | **0.9520** | **$19.52** | Placeholder from benchmark screenshot |
| Matches/Differs | match | **8.0000** | $90.00 | **Placeholder** — needs real confirmation |
| Matches/Differs | differ | **0.1000** | $11.00 | **Placeholder** |
| Over/Under | over/under + barrier | `0.95 × ((10 / winCount) − 1)` rounded 4dp | varies | **Placeholder** formula |

**Do not use with real money until product replaces these numbers.** They live in `internal/models/digit_payout.go` as data, not scattered conditionals.

---

## 5. Settlement correctness verification

| Test | Result |
|---|---|
| **Win** (Even, settlement digit even) | Status `won`; bonus **+9.52** net; ledger reconciles |
| **Loss** (Odd, settlement digit even) | Status `lost`; bonus **−10**; reconciles |
| **Double-settle race** (20 concurrent `SettleForTest`) | **Exactly one** success; status `won`; reconciles |
| **Sweep after missed tick** (`InjectTickQuiet` then sweep) | Settles to `won` without tick observer |
| **Auto-void** (void_after in the past, no later epoch) | Status `void`; stake refunded; reconciles |
| **Restart recovery** (open + quiet tick, then `Start`) | Boot sweep settles to `won` |
| **Non-volatility reject** | Validation error on `EURUSD` |

Idempotency relies on `SELECT … FOR UPDATE` where `status = 'open'`, same idea as CFD `GetOpenForUpdate`.

---

## 6. Discovered mid-implementation

1. **Tick epoch is second-resolution** (`time.Now().Unix()` on `InjectTick`). Two ticks in the same second would not advance “next tick.” Added `InjectTickAt` / `InjectTickQuiet` for tests; production mock (~2 Hz) and live Deriv epochs are fine in practice. If live ever emits multiple ticks per second with the same epoch, settlement would need a sequence — flag for later if seen.
2. **`/digit-contracts/summary` must be registered before `/digit-contracts/:id`** in Gin, or `summary` is parsed as an id.
3. **Loss still writes a `digit_settle` ledger row with amount 0** so admin/history can see the settlement event without implying a credit.
4. Frontend login/register payloads still omit `digit_contracts_enabled`; the field is optional until `/me` runs — intentional.

---

## 7. Readiness for Phase 2

**Ready for the digit trading UI**, with these caveats:

- Wire UI to `POST/GET /digit-contracts*` and listen for `digit_settled` / `digit_opened` on the account socket (extend `useAccountStream` types).
- Port `LastDigit` to TypeScript using the Go comment/rule exactly.
- Keep CFD `TradeScreen` behind the flag; replace the stub with the real surface when the flag is on.

**Still open from the investigation (not resolved here):**

- Real payout table (replace placeholders before real money).
- Stake-vs-payout toggle.
- Admin UI to flip the flag (SQL works today).
- Demo vs real economics.
- Stuck-contract policy is implemented as **auto-void after 5s** — confirm that grace with product if needed.
- Symbol eligibility locked to **volatility** only.
- Expiry locked to **next tick only**.

**Not in this phase (intentionally):** Positions/History panel redesign, SessionStrip metrics for digits, Signals hiding polish, TARGET/SL/MULT, AUTO.
