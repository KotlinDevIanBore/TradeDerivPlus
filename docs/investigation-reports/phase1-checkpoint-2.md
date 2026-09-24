# Phase 1 · Checkpoint 2 — BotService and settlement-transaction counters

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Plan:** `docs/investigation-reports/auto-bot-plan.md` §2.2–2.4, §2.6, §4.1
**Scope:** The loop, the counters, supervision. No routes, no handlers.

---

## 0. Answer to the question asked before this checkpoint

**Is `GetLiveByAccount` scoped to the caller's own `account_id` wherever it is used?**

Yes. The query is `WHERE r.account_id = $1 AND r.status IN ('running','stopping')`, and it now has
exactly one call site: `BotService.StartRun`, at `bot_service.go`. That call passes `in.AccountID`
**after** the ownership check at the top of the same function:

```go
account, err := s.store.Accounts.GetByID(ctx, in.AccountID)
...
if account.UserID != in.UserID {
    return nil, ErrForbidden
}
```

So an account id that is not the caller's is refused before the lookup happens. The same pattern
guards every other public method added in this checkpoint — `StopRun`, `Get`, `List` each resolve the
run's account and compare `account.UserID` to the caller's before returning anything, which is how
`DigitService.Get` and `List` already do it. When Checkpoint 3 adds handlers, the check stays in the
service rather than moving to the transport, so an internal caller cannot bypass it.

---

## 1. The counters-in-transaction change (§2.4)

Three call sites, all *inside* transactions that already existed. Nothing new commits on its own.

| Where | Call | Notes |
|---|---|---|
| `Open`'s `WithTx`, after `DigitContracts.Create` | `BotRuns.IncrementPlaced` | A placement that rolls back is never counted; one that commits is never missed. |
| `settle`'s `WithTx`, after `DigitContracts.Settle` | `BotRuns.ApplySettlement(status, pnl)` | Run id read from the **locked** row, not the caller's copy. |
| `voidContract`'s `WithTx`, after `DigitContracts.Void` | `BotRuns.ApplySettlement(void, 0)` | Void is a third outcome, not a loss. |

`settle` previously locked the row and threw it away (`_ = locked`). That discarded value is now the
source of the run id, which is the correct one to use: it is the version this transaction holds a
lock on. `voidContract` discarded its locked row entirely (`if _, err := …`) and now keeps it.

The streak arithmetic lives in one SQL statement (`BotRunRepo.ApplySettlement`) rather than in Go, so
it is atomic with the settlement:

- `consecutive_losses` — `+1` on a loss, `0` on a win, **untouched by a void**. A feed hiccup is not
  a losing streak.
- `consecutive_voids` — `+1` on a void, `0` on any other settlement. This is the feed-death detector,
  and it has to be, because `Open` cannot report a stalled feed: a stale last price still prices a
  contract, which then voids.

**Why this design and not the simpler one.** Had the loop maintained its own totals, a contract
settling after its run stopped would be invisible to them, and restart would need a reconciliation
pass whose result races the boot sweep (which at that moment has no ticks yet and can only void).
With the counters in the settlement transaction, `BotService.Start` is one `UPDATE` and a straggler
still lands on the right row.

---

## 2. BotService (§2.2–2.3)

`internal/service/bot_service.go`. One goroutine per run, one contract in flight. It does **not**
register a tick observer.

Every §2.3 rule, and where it is:

| Rule | Implementation |
|---|---|
| No retry on an unknown error | `loop`'s `default:` case finalises `error` and returns. An unknown error may have committed its contract and lost the reply; retrying that is the one way a sequential loop double-places. |
| `CountOpenByRun` guard | Checked before every placement. If a contract of ours is already open, `awaitRunSettled` waits for it instead of placing. |
| `ErrDigitNotOpen` is a re-read | `awaitSettlement` treats it as "lost the settle race to onTick/sweep/another reader", not a failure. |
| Panic recovery | `launch`'s deferred `recover` logs and finalises `error`. `middleware.Recovery` wraps HTTP only; nothing supervises a bare service goroutine, so a panic here would take down the API. |
| WaitGroup + cancel map | `s.wg` and `s.runs`, with the goroutine deleting its own map entry in a `defer` so no finished run leaves an entry behind. |
| `Open` never on a cancellable per-run context | `place` uses `context.WithTimeout(s.rootCtx, 10s)`. A stop is a flag the loop consults, never a cancellation of work in flight. |
| Polling, not a callback | `awaitSettlement` polls `digits.Get` every 250 ms (5 ms in tests). The poll's own `EnsureSettled` can settle the contract on the bot's goroutine, which takes work *off* the feed goroutine. |
| Void counts toward `max_contracts` | `shouldStop` compares `ContractsPlaced`, which `IncrementPlaced` advances at open — so a stalled feed cannot place without bound. |
| Feed death = 3 consecutive voids | `shouldStop` checks `ConsecutiveVoids`; `ErrNoPrice` retries are cold-start only (5 attempts). |
| Detached context for finalisation | `finalise` swaps to `context.WithTimeout(context.Background(), 5s)` when the root context is already cancelled, as `onTick` does. Otherwise a shutdown write fails silently and the row stays live forever. |

Stop precedence is in one place, `shouldStop`, in this order: external stop request → process
shutdown → the row was ended underneath us → global flag off → the user's own limits → feed death.

`Shutdown(ctx)` asks every run to stop and waits on the WaitGroup, bounded by the caller's context.
`Start(ctx)` issues the restart sweep (§4.3): one `UPDATE … WHERE status IN ('running','stopping')`.
Runs do not resume — a run silently restarting after a deploy and placing money on settings its user
has forgotten is the wrong default.

`main.go` wires it after `digits.Start(ctx)` and calls `bots.Shutdown(shutdownCtx)` beside
`srv.Shutdown`, with a comment saying why a bot run is the one thing here that must be drained before
the pool closes.

---

## 3. Deviations from the plan

**1. §2.8's SettingsService work landed here instead of Checkpoint 3.** `BotService` cannot be
constructed without a flags implementation, and `main.go` cannot be wired — which Checkpoint 2 owns,
because the shutdown hook lives there. So the generalised cache and the three flag accessors
(`BotEnabled`, `BotAllowlist`, `BotRealMoneyEnabled`, plus the composed `BotEnabledFor`) are in this
checkpoint. **Still in Checkpoint 3:** exposing them on `/auth/me`, and the routes.

**2. Sentinels added now, `fail` mapping deferred.** `ErrBotDisabled`, `ErrBotRealMoneyDisabled`,
`ErrBotRunActive` and `ErrBotCapacity` exist because `BotService` returns them. Their HTTP mapping in
`handlers/response.go` is Checkpoint 3 work per §2.7. Nothing reaches HTTP yet, so an unmapped
sentinel cannot currently produce a wrong status.

**3. Metrics definitions added now, sampler deferred.** `main.go` wires `bots.OnContract` and
`bots.OnStop`, so `RecordBotContract` and `RecordBotRunStopped` had to exist. The `bot_runs_live`
gauge and its `Sampler` method are Checkpoint 3.

**4. One integration test written early.** §8 assigns the test list to Checkpoint 4, and it stays
there — but handing over the highest-risk checkpoint on a compile check alone is not a verification.
`TestBotRunPlacesSequentiallyWithProvenance` is written now, and the harness is wired for the rest
(`bots`, `settings`, a `captureBroadcaster` for WS assertions). The other ~18 named tests are
Checkpoint 4 and none is skipped.

**5. `SettingsDefaults` replaced the bare `digitDefault bool`.** Four flags through a single boolean
parameter was not extensible, and `newSettingsService`'s signature is the seam the existing unit test
uses. `settings_service_test.go` was updated: four call sites now pass
`SettingsDefaults{DigitContractsEnabled: …}`, and the line that busted the cache by assigning
`s.digitAt = time.Time{}` now calls `s.bustCache()`, since the per-key cache has no such field. Test
behaviour is unchanged.

**6. `BustCache` is exported.** The admin kill switch (Checkpoint 3) turns `bot_enabled` off and must
not then leave a stale "enabled" in the cache for its 10-second TTL.

---

## 4. A bug found and fixed while writing this

My first draft computed the duration deadline as `run.StartedAt.Add(duration)` with the duration
defaulting to zero when unset. A deadline equal to the start instant is already in the past, so
**every run with no duration limit would have stopped immediately on `max_duration`**. Fixed by
leaving `deadline` as the zero `time.Time` when `MaxDurationSeconds` is nil and guarding on
`!deadline.IsZero()`. Recorded here because it is exactly the class of mistake the checkpoint's test
was meant to catch, and it was caught by writing the test.

---

## 5. Verification

Exact commands.

### Build, vet, format

```bash
gofmt -l ./cmd ./internal ./pkg       # no output
go build ./...
go vet ./...
go vet -tags integration ./internal/service/
```

### Unit and integration suites

```bash
go test ./...
# ok  internal/config · internal/models · internal/service · pkg/mpesaclient

go test -tags integration ./... -count=1 -timeout 900s
# ok  internal/service  18.720s   (everything else ok)
```

The pre-existing digit and concurrency tests passing is the evidence that adding three counter calls
inside the money transactions did not disturb settlement, freezing, or reconciliation.

### The new test

```bash
go test -tags integration ./internal/service/ \
  -run 'TestBotRunPlacesSequentiallyWithProvenance' -count=1 -v
# --- PASS: TestBotRunPlacesSequentiallyWithProvenance (0.34s)
```

It asserts, on a real database: never more than one contract in flight (sampled by a concurrent
watcher, because the per-account cap of five would not catch a second one); the run stopped on
`max_contracts` with exactly 4 placed; every contract carries the right `bot_run_id`; every
`digit_open` ledger row carries it in metadata; the run's `wins`/`losses`/`voids` and `realised_pnl`
equal what the contracts say; `assertReconciled`; and a `bot_run` event was broadcast.

### Mutation check — does the assertion have teeth?

A passing test proves nothing unless it can fail. The `ApplySettlement` call in `settle` was
temporarily replaced with `_ = locked` and the test re-run:

```bash
# with the counters-in-tx call disabled:
--- FAIL: TestBotRunPlacesSequentiallyWithProvenance (0.34s)
    bot_integration_test.go:179: contracts say 4W/0L/0V, run says 0W/0L/0V
```

Restored (`grep -c ApplySettlement internal/service/digit_service.go` → 3) and re-run green. So the
checkpoint's central change is genuinely covered rather than incidentally passing.

---

## 6. Not done in this checkpoint, by instruction

No routes, no handlers, no `fail` mapping, no `/auth/me` fields, no `bot_runs_live` sampler, no
`cmd/seed` change, no UI. `BotService` is reachable only from Go — which means nothing user-facing has
changed, and `bot_enabled` defaults to `false` in any case.
