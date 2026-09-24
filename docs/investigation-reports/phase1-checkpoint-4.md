# Phase 1 · Checkpoint 4 — Test suite

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Plan:** `docs/investigation-reports/auto-bot-plan.md` §8, §6 Phase 1 exit criteria
**Scope:** Every remaining named test, `scripts/bot_check.py`, CI wiring. Phase 1 closes here.

---

## 1. What was built

### Integration tests — all 18 of §8's named tests

| Test | File | What it forces |
|---|---|---|
| `TestBotRunPlacesSequentiallyWithProvenance` | `bot_integration_test.go` | one in flight, provenance, totals ≡ contracts (Checkpoint 2) |
| `TestBotStopLossHaltsRun` | `bot_limits_test.go` | 3 losses → −30 past a −25 floor |
| `TestBotTakeProfitHaltsRun` | `bot_limits_test.go` | 2 wins → 19.04 past a 15 target |
| `TestBotVoidIsNotALoss` | `bot_limits_test.go` | void with `max_consecutive_losses=1` must **not** stop the run |
| `TestBotConsecutiveVoidsStopFeedDown` | `bot_limits_test.go` | 3 voids → `feed_down`, nothing lost |
| `TestBotMaxConsecutiveLossesResetsOnWinNotVoid` | `bot_limits_test.go` | L,L,W,L,V,L,L → stops at exactly 7 placements |
| `TestBotMaxContractsCountsVoids` | `bot_limits_test.go` | 2 voids hit a limit of 2 |
| `TestBotMaxDuration` | `bot_limits_test.go` | backdated `started_at` → `max_duration` |
| `TestBotUserStopWaitsForInFlight` | `bot_lifecycle_test.go` | `stopping`, slot held, in-flight contract counted |
| `TestBotKillSwitchStopsAllRuns` | `bot_lifecycle_test.go` | 3 runs, flag off, audit row, restart refused |
| `TestBotRestartMarksRunsStoppedAndTotalsReconcile` | `bot_lifecycle_test.go` | `restart`, then a straggler still reaches its stopped run |
| `TestBotShutdownFinalisesRun` | `bot_lifecycle_test.go` | `shutdown` on a detached context; contract left for the sweep |
| `TestBotAndHumanConcurrentOpensNeverOverdraw` | `bot_lifecycle_test.go` | bot + 20 concurrent human opens, never negative |
| `TestBotRefusesSecondRunOnSameAccount` | `bot_lifecycle_test.go` | refused while running **and** while stopping |
| `TestBotStartGates` | `bot_lifecycle_test.go` | ten sub-tests, one per rule |
| `TestBotStopsOnInsufficientFunds` | `bot_lifecycle_test.go` | balance removed mid-run |
| `TestBotFreezeStopsRun` | `bot_lifecycle_test.go` | `account_frozen`, with no bot-specific code |
| `TestBotLosesSettleRaceGracefully` | `bot_lifecycle_test.go` | 12 racers, exactly one settlement, counted once |

Nothing was skipped or simplified. **63 integration assertions pass** (counting sub-tests), and the
suite was run three times in a row (`-count=3`) and under `-race` with no failure and no race report.

### Test fixture

`bot_driver_test.go` — a driver that makes outcomes a property of the test rather than of a random
walk. A digit contract settles on the first tick with a greater epoch, so a test that owns every
injected tick owns every outcome. It names the three things that can happen (`win`, `lose`, `void`)
and plays sequences: `d.resolve(run.ID, Lost, Lost, Won, Lost, Void, Lost, Lost)` is the
streak test's history, readable as the thing being asserted.

A void needs no tick at all: `void_after` is backdated and the next check-on-read refunds. That the
*ordering* inside `EnsureSettled` makes this work — later tick first, then expiry — is itself relied
on and therefore covered.

### Unit tests

- `bot_service_test.go` — `botLimitReached` as a pure function: **17 cases** covering every stop
  reason, both sides of each threshold, and `TestBotLimitPrecedence` pinning the order so a run that
  hit its stop loss is never reported as merely out of contracts. Plus `TestBotLimitsDefaults`,
  asserting a zero or negative bound takes the default rather than meaning "unlimited".
- `settings_service_test.go` — the three new flags: defaults off, `Bootstrap` seeds all four keys and
  never overwrites a stored value, `BustCache`, an unparseable value falling back, and **nine
  allowlist cases** including the deliberate choice that empty means everyone.
- `digit_payout_test.go` — from Phase 0, unchanged.

### `scripts/bot_check.py`

Follows `sltp_check.py`'s skeleton. Six sections: flags, refusals, a run, provenance, totals, money,
and stopping. It exercises the refusals before the happy path, because a run that cannot be
misconfigured is most of what makes the feature safe.

It **exits 0 with an explanation when `bot_enabled` is off** rather than failing — the flag being off
is the shipped default, not a broken feature. To stop that becoming a silent skip in CI, the workflow
sets `BOT_ENABLED: "true"` in the job environment as well as relying on `cmd/seed`, so the check
cannot quietly pass by doing nothing. Wired into the existing smoke step beside `sltp_check.py`.

---

## 2. Two real defects found by writing these tests

Both were in `BotService`, both in shutdown handling, and neither was reachable before there was a
test that cancelled a context mid-run.

**1. A shutdown was recorded as a user stop.** `sleep` returns false on either a stop request or root
context cancellation, and the caller then asked `stopReasonOf`, which defaulted to `user` when no
explicit reason had been set. So a deploy landing while a run was between contracts would tell an
operator their users had quit. Fixed by having `stopReasonOf` consult the root context before
falling back.

**2. A shutdown was recorded as an internal error.** With that fixed, the test still failed — reason
`error`. The loop's first act each iteration is to read the run row, and on a cancelled context that
read fails, so the run finalised as a fault with `context canceled` as its `last_error`. Fixed by
extracting `externalStop`, which touches no database, and consulting it **before** the read at both
points in the loop where one happens.

The second is the more interesting one: the first fix made the test fail differently rather than
pass, which is what a test earning its keep looks like.

---

## 3. Deviations and corrections

**The `?bot_run_id=` filter, deferred in Checkpoint 3, is now present** — implemented on disk between
checkpoints, through the repo, service and handler. It was live code with no test, which is exactly
the concern that prompted deferring it, so `TestDigitContractsFilterByBotRun` now covers it: two runs
plus a human contract on one account, asserting each filter returns only its own run's contracts and
that the human contract belongs to none of them. **This closes the Phase 2 note from Checkpoint 3 —
nothing is outstanding there.**

**Two small refactors, both for testability and both improvements on their own terms.**

- `botLimitReached(run, now)` extracted from `shouldStop`. The user's own conditions now depend on
  nothing but the row and the clock, which is what makes 17 exhaustive cases possible without a
  database — and §8 asked for exactly that ("a pure function over a run row").
- The duration deadline is derived from the row inside that function rather than captured when the
  loop starts. One source of truth, and it makes `max_duration` testable by backdating `started_at`
  instead of waiting a minute. The comment records why: an earlier draft captured it once and
  defaulted to `StartedAt`, which is a deadline already in the past.

**Four test bugs of my own, fixed rather than worked around.** Recorded because they say what the
service's rules actually are:

1. A stop loss may not exceed the balance, so the concurrency test's `stop_loss: 500` on a $100
   account was refused. Sized to the account.
2. `TestBotStopsOnInsufficientFunds` could not be configured as written: a stake must be within a
   tenth of the balance and a stop loss within the balance, which together mean **a run cannot be set
   up to exhaust its own funds before its stop loss fires**. That is a good property, so the test now
   removes the money mid-run with an admin adjustment — which is also the realistic case.
3. `newDemoAccount` funds its wallet through `WalletService`, so creating a second one conflicted.
4. `TestBotVoidIsNotALoss` compared wallet balances and raced the loop's next placement. It now
   asserts on the voided contract's own ledger rows summing to zero, which is race-free and says the
   thing more precisely.

**One extra test beyond §8:** `TestBotStopRunIsIdempotent`, since the handler advertises that
behaviour and §2.7 requires it.

---

## 4. Verification

```bash
gofmt -l ./cmd ./internal ./pkg        # no output
go build ./...
go vet ./...
go vet -tags integration ./internal/service/

go test ./... -count=1                 # all packages ok
go test -tags integration ./... -count=1 -timeout 1200s
# ok  internal/service  26.044s

# Stability: the concurrency and goroutine tests, three times over
go test -tags integration ./internal/service/ -run 'TestBot|TestDigit' -count=3
# ok  internal/service  38.650s

# Exactly what CI runs, including the race detector and the coverage gate
go test -tags integration -race -count=1 -coverprofile=cov.out -covermode=atomic ./internal/service/...
# ok  internal/service  37.437s  coverage: 69.1% of statements
```

No race reports. Coverage gate:

```
wallet_service.go:     77.8% (floor 75%)
trading_service.go:    81.5% (floor 75%)
admin_service.go:      78.9% (floor 75%)
gate exit: 0
```

New files, for information (not gated): `bot_service.go` 64.8%, `admin_bot.go` 71.4%,
`digit_service.go` 73.2%, `settings_service.go` 93.7%. The uncovered part of `bot_service.go` is
mostly the error branches of database reads inside the loop.

Scripts and browser suites:

```bash
python3 scripts/smoke.py          # ALL CHECKS PASSED
python3 scripts/sltp_check.py     # STOP LOSS / TAKE PROFIT AUTO-CLOSE VERIFIED
python3 scripts/bot_check.py      # AUTOMATED RUN VERIFIED
python3 scripts/admin_check.py    # ADMIN CONSOLE VERIFIED
go run ./scripts/wscheck http://localhost:8080/api/v1 6   # PASS

cd web && npm run typecheck && npm run test    # 82 tests, 9 files
npx playwright test --project=digit-mobile --project=digit-desktop
# 10 passed, 2 skipped by viewport
```

---

## 5. Phase 1 exit criteria, confirmed

§6 Phase 1 lists four. Each with its evidence.

**1. `go build`, `go vet`, `gofmt` clean.** ✅ Commands and output above. `gofmt -l` prints nothing
across `./cmd ./internal ./pkg`.

**2. Full integration suite green against real Postgres.** ✅ `go test -tags integration ./...`
passes at 26.0s on a testcontainers Postgres 16, and again under `-race` with coverage at 37.4s. Run
three times over for the bot tests specifically, with no flake.

**3. `bot_check.py` passes in CI.** ✅ Passes locally against the live API (full output in
Checkpoint 3's report format; every section PASS), and is wired into the `e2e` job's smoke step with
`BOT_ENABLED: "true"` in the job environment so it cannot skip itself silently.

**4. A manual curl-started run on the mock feed stops on its stop-loss with `ledger_drift_accounts`
at 0.** ✅ Demonstrated in Checkpoint 3 and now enforced by two independent mechanisms:

- `TestBotStopLossHaltsRun` forces it deterministically and ends on `assertReconciled`, which is the
  same ledger-versus-cache invariant `ledger_drift_accounts` samples.
- `bot_check.py` asserts the balance moved by exactly the run's realised P/L and that the ledger sum
  agrees with the wallet cache, on every CI run.

From the live API during this checkpoint:

```
bot_contracts_total{outcome="lost"} 6
bot_contracts_total{outcome="placed"} 9
bot_contracts_total{outcome="won"} 3
bot_runs_live 0
bot_runs_stopped_total{reason="max_contracts"} 1
bot_runs_stopped_total{reason="stop_loss"} 1
ledger_drift_accounts 0
```

**Phase 1 is complete.** All four criteria met.

---

## 6. State of the work

Twelve new files, seventeen changed; 1,216 insertions across the phase. No UI: `web/src` carries only
the two additive `DigitQuote` fields from Phase 0, and the `AccountEvent` union, types and screens are
Phase 2 as planned.

`bot_enabled` ships **false**. It is on in development (via `cmd/seed`) and in CI (via the job
environment) and nowhere else, and `bot_real_money_enabled` is false everywhere including
development. The open questions in §10 of the plan — whether to ship against a placeholder payout
table, and what the UI says about negative expected value — are unchanged and still product's to
answer before that flag moves.
