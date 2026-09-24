# AUTO Mode — Phase 0 Implementation Report

**Date:** 23 September 2026 · **Branch:** `digit-contracts-enabled-by-default` @ `4d425e0` + this work
**Plan:** `docs/investigation-reports/auto-bot-plan.md` §6, Phase 0
**Scope:** Safety net and global guards. **No bot code.**

---

## 1. Summary

Phase 0 is the part of the AUTO-mode plan that ships before any bot exists, on the plan's first
non-negotiable: risk controls land before the loop. Two things were built.

**A regression net under the digit surface.** CI ran no browser test against digit contracts at
all (`DIGIT_CONTRACTS_ENABLED: "false"`, `ci.yml`), even though that is the production default —
so the app's actual trading screen had no end-to-end coverage. There are now ten digit browser
tests in their own CI job.

**Three server-side limits that apply to everyone**, not just to whatever drives the API later:
a maximum stake per contract, a cap on contracts awaiting settlement per account, and a
dedicated rate bucket for placing a position. All three are enforced in the service and the
transaction, not the client, because the client's own checks are advice.

One thing the plan asked for turned out to be worth correcting rather than implementing as
written: see §6.

Everything green: `gofmt`, `go vet`, `go test ./...`, `go test -tags integration ./...`,
`npm run typecheck`, `npm run test` (82 tests), `npm run build`, all four CI scripts, and both
Playwright modes.

---

## 2. Per-step outcome

### Step 1 — Digit e2e in CI ✅

Two new Playwright projects and a second CI job.

- `web/playwright.config.ts` — `digit-mobile` and `digit-desktop` projects matching
  `/digit\..*\.spec\.ts/`; `testIgnore` added to `desktop`, which previously had no `testMatch`
  and so would have swept the new specs into the CFD run.
- `.github/workflows/ci.yml` — a new `e2e-digit` job with its own Postgres service and
  `DIGIT_CONTRACTS_ENABLED: "true"`. **It has to be a separate job, not a second step:**
  `app_settings.digit_contracts_enabled` is seeded once and never overwritten
  (`settings_service.go` `Bootstrap`), so one database cannot serve both modes and restarting
  the API against the first job's database would keep whichever value it seeded.
- `web/e2e/helpers.ts` — `waitForDigitSurface`, `waitForDigitTicket`, `pillAmount`. The digit
  waits are 45s because the flag arrives on `/auth/me` and the client polls it every ten
  seconds, so a client that loaded first shows the CFD ticket until the next poll.
- `web/e2e/digit.trading.spec.ts`, `web/e2e/digit.panels.spec.ts` — six specs, ten runs across
  the two viewports.

The headline assertion: after opening Even at a $10 stake, the balance must land on exactly one
of **10,009.52** (won, at the quoted 95.2%), **9,990.00** (lost) or **10,000.00** (voided and
refunded). A digit contract settles with no further action, so those are the only three ends it
can have, and anything else is a settlement bug.

### Step 2 — Maximum stake and open-contract cap ✅

`internal/service/digit_service.go`. `NewDigitService` takes a new `DigitLimits{MaxStake,
MaxOpenPerAccount}`. A zero field takes the default rather than meaning "unlimited", because the
dangerous direction of a mistake here is upwards.

- **Max stake** ($100) is checked beside the existing `> 0` check, as a validation error on the
  `stake` field — so it renders under the Stake input, which `DigitTicket.tsx` already does for
  any `field === 'stake'` error.
- **Open cap** (5) is counted **inside** `Open`'s transaction, after `Wallets.GetForUpdate`.
  That placement is the whole point: the wallet lock serialises every `Open` on an account, so
  two concurrent callers cannot both read a count below the cap and both insert. Checking before
  the transaction would have been a race. New repo method `CountOpenByAccount`, served by the
  existing `idx_digit_contracts_account_status`.
- New sentinel `ErrTooManyOpen` → 409 `too_many_open`, message "Wait for your open contracts to
  settle before opening another".

### Step 3 — Placement rate bucket ✅

`internal/routes/routes.go`. `POST /trades` and `POST /digit-contracts` move from the group's
loose 20/s bucket to a new `placement` group at `rate.Every(500ms), burst 5` — the same
treatment deposits and withdrawals already get, since opening a position also moves money.

**Closing is deliberately not in the bucket.** A user getting out of a position should never
meet a limiter. Quote reads stay in the loose group too.

### Step 4 — Quote economics, and the wrong comment ✅

`DigitQuote` gains `win_probability` and `expected_return`, served rather than computed
client-side so the figure shown to a user cannot drift from the table that prices the contract.
New pure functions `models.WinProbabilityFor` and `models.ExpectedReturnFor`, both validating
through `PayoutRatioFor` so the three can never disagree about a well-formed selection.

The comment at `digit_payout.go` claiming "house edge ~5%" is corrected. The 0.95 factor applies
to the *profit*, so the realised edge is 5% × (1 − p) and ranges from 0.50% to 4.50% across
barriers — not a flat 5%.

### Step 5 — Config ✅

`DIGIT_MAX_STAKE` (100) and `DIGIT_MAX_OPEN_PER_ACCOUNT` (5), wired in `cmd/api/main.go`.

### Step 6 — Smoke coverage for the bucket ✅

`scripts/smoke.py` gains a concurrent placement burst, mirroring the auth-limiter check it
already does. It uses a **deliberately invalid stake**: the bucket is consumed before the
handler reads the body, so every request that gets through is refused by validation and no money
moves. Because the group's own bucket is 20/s burst 60 — well above the burst — a 429 here can
only have come from the placement bucket, which makes the assertion specific rather than
incidental.

---

## 3. Files changed

| File | Change |
|---|---|
| `internal/models/digit_payout.go` | `WinProbabilityFor`, `ExpectedReturnFor`; corrected edge comment |
| `internal/models/digit_payout_test.go` | **new** — the investigation's edge table asserted against the code |
| `internal/models/digit_contract.go` | `DigitQuote.WinProbability`, `.ExpectedReturn` |
| `internal/service/digit_service.go` | `DigitLimits`, max stake, open cap inside the tx, quote economics, `MaxStake()` |
| `internal/service/errors.go` | `ErrTooManyOpen` |
| `internal/handlers/response.go` | `too_many_open` → 409 |
| `internal/repository/digit_contract_repo.go` | `CountOpenByAccount` |
| `internal/routes/routes.go` | `placement` rate bucket |
| `internal/config/config.go` | `DigitMaxStake`, `DigitMaxOpenPerAccount` |
| `cmd/api/main.go` | wires the limits |
| `internal/service/integration_setup_test.go` | harness states the production limits |
| `internal/service/digit_integration_test.go` | two new guard tests |
| `scripts/smoke.py` | placement-bucket burst |
| `web/src/types/index.ts` | the two quote fields |
| `web/playwright.config.ts` | digit projects; `testIgnore` on `desktop` |
| `web/e2e/helpers.ts` | `waitForDigitSurface`, `waitForDigitTicket`, `pillAmount` |
| `web/e2e/digit.trading.spec.ts`, `web/e2e/digit.panels.spec.ts` | **new** |
| `.github/workflows/ci.yml` | `e2e-digit` job; corrected stale comment on the CFD job |

No migration. No change to `Open`'s locking order, the ledger writes, settlement, or the CFD
path.

---

## 4. Verification

| Gate | Result |
|---|---|
| `gofmt -l`, `go vet ./...` | clean |
| `go test ./...` | pass |
| `go test -tags integration ./...` | pass (21.5s) |
| `npm run typecheck`, `npm run build` | pass |
| `npm run test` | 82 passed, 9 files |
| `smoke.py`, `sltp_check.py`, `admin_check.py`, `wscheck` | all pass |
| `--project=digit-mobile --project=digit-desktop` | **10 passed**, 2 skipped by viewport |
| `--project=mobile --project=desktop` | 25 passed, 1 pre-existing failure (§7) |

Guards confirmed against the running API, not only in tests:

```
stake 101 -> 422 {'message': 'The most you can stake on one contract is $100.00',
                  'code': 'validation_failed', 'field': 'stake'}
stake 100 -> 201 ok
quote even_odd       -> payout_ratio 0.952  win_probability 0.5  expected_return -0.024
quote matches_differs-> payout_ratio 8      win_probability 0.1  expected_return -0.1
burst                -> 201,201,201,429,429,429,429,429   (placement bucket)
```

New integration tests:

- `TestDigitMaxStakeRejected` — 101 refused on `stake` with a balance of 100,000, so only the
  ceiling can be refusing it; 100 opens, so the message and the rule agree.
- `TestDigitOpenCapIsRaceFree` — ten concurrent opens behind a start gate: exactly five succeed,
  five get `ErrTooManyOpen`, and the table is checked directly rather than trusting the callers'
  count. Ends on `assertReconciled`.

---

## 5. Discovered mid-implementation

**The expected-return table is very slightly off at one barrier.** Over 3 / Under 6 returns
−2.002%, not the −2.00% the investigation's table shows: six winning digits make the fair profit
a recurring decimal, and rounding the stored ratio to 4dp moves the result a thousandth. The
contract is priced from the rounded ratio, so −2.002% is the true figure. Recorded in
`digit_payout_test.go` rather than left to be rediscovered.

**The placement bucket hides the open cap over HTTP.** Once placement is limited to 2/s, a
browser cannot get five contracts in flight before the limiter refuses it — the 429 arrives
first. This is fine (both limits are doing their job) but it means a browser test named "the cap
holds" would pass on the limiter instead. See §6.

**The auth limiter is the binding constraint on a local e2e run.** It is keyed per IP on an
unauthenticated route with a production burst of ten, and a suite that registers a user per test
reaches that partway through. The new CI job carries the same `AUTH_RATE_EVERY`/`AUTH_RATE_BURST`
loosening the existing one documents.

---

## 6. Changed from the plan

**The cap's browser test was dropped, not written.** The plan listed a digit e2e case for the
open-contract cap. Over HTTP the placement bucket refuses a rapid burst before five contracts can
be in flight, so that test would have passed on the rate limiter and reported a green cap it had
never exercised — worse than no test, because it would be believed. The cap is instead asserted
deterministically by `TestDigitOpenCapIsRaceFree`, which drives the service directly and can
force the race; the bucket is asserted by the `smoke.py` burst. Both behaviours are covered, each
where it can be forced. A comment in `digit.trading.spec.ts` records why there is no third case
there.

**The ticket does not pre-empt the max stake.** The plan's breakage table says "inline error on
the ticket". It is inline, but from the server's message rather than a client-side copy of the
limit: the client has no way to learn the ceiling in Phase 0, and hardcoding 100 would put the
number in two places to drift. The server's message names the figure and lands under the Stake
input. The limit moves onto `/auth/me` in Phase 1, at which point the ticket can refuse before
the round trip.

**`Quote` is not bounded by the max stake**, deliberately. A quote is informational, and refusing
it would blank the payout on the buttons and disable submission — leaving the user unable to find
out why. Letting the quote answer and `Open` refuse produces a clear message instead.

---

## 7. Pre-existing failure, not from this work

`web/e2e/admin.spec.ts:55` ("reviews a KYC submission and the user becomes verified") fails in a
full local suite run. **It fails identically on unmodified baseline code** — verified by stashing
this work, rebuilding, and reproducing — so it is not from Phase 0. It is order-dependent: it
passes alone, self-skips once the queue is empty (`test.skip(count === 0)`), and fails after
`admin_check.py` has run against the same shared fixture. `cmd/seed` creates exactly one pending
submission and refuses to add more, so the test competes with the scripts for one row. Flagged as
its own task; the fix is for the test to seed its own submission.

---

## 8. Readiness for Phase 1

Phase 1 can start. What it depends on from here is in place:

- The digit surface has a regression net, so the code an AUTO loop will drive is now covered.
- `DigitService.Open` already enforces a stake ceiling and an open-contract cap inside its
  transaction, so a bot calling it inherits both without a second code path.
- `DigitLimits` is a constructor parameter, so `BotService` can be given tighter bounds than a
  human without touching the money code.
- The quote already serves `expected_return`, which the bot ticket needs to state what a run
  costs per contract.

Unchanged and still ahead of Phase 1: the `bot_runs` migration, provenance on `digit_contracts`,
counters inside the settlement transaction, `BotService`, the three flags, and the shutdown hook
`cmd/api/main.go` still lacks.
