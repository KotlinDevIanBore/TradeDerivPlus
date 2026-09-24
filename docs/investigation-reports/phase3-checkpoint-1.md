# Phase 3 · Checkpoint 1 — the backend the console assumes

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** the four gaps from `auto-bot-deferred.md`, plus the broadcast duplicate found in
Phase 2 CP4 §3.2. No UI.

---

## 1. What was built

### Finished runs are reachable

`AdminService.ListBotRuns` used to return an empty slice for `status=stopped` with the comment
"Not a use case the console has yet", so a run became invisible to the console the moment it
ended — which is when an operator wants to look at it. It now goes through a new
`BotRunRepo.ListAll(status, limit, offset)`: `live`, `stopped`, or both, newest first.

Paged, unlike the user's own list. `BOT_MAX_RUNS` bounds what can be live at any moment;
nothing bounds what has ever run, so a screen that asked for all of it would get slower every
day it was used. Same convention as the other admin tables — `clampInt` + `resolvePage`, and a
body of `{items, total, limit, offset, page, pages}`.

### Owner identity, on the admin path only

`repository.AdminBotRun` embeds `models.BotRun` and adds `account_number`, `account_type`,
`user_id` and `user_phone` — the shape `AdminTransaction` already uses to answer the same
question for the ledger table, so the console's rows read alike.

**Deliberately not on `botRunSelect`.** The prompt left this open, and the deciding fact is that
`Get` and `GetLiveByAccount` are polled every two seconds by every user with a live run. Two
extra joins on that path would be paid continuously to tell a user something they already know —
whose run it is. `ListAll` carries its own column list and its own joins.

### A per-run stop

`POST /admin/bot/runs/:id/stop` → `AdminService.StopBotRun`, in the `actions` rate bucket with
the freezes and adjustments, because it reaches into one user's account the way they do.

It is **not** a narrower `bot_kill`: automated trading stays on and every other run keeps going.
So it has its own action, `models.ActionBotRunStop`, over a new `models.TargetBotRun`, and its
own stop reason, `models.BotStopAdminStop` — a user whose single run was stopped is being told
something different from a user caught by the kill switch, and an audit log that filed both
under one name could not tell them apart.

The status change and the audit row commit together, for the reason `KillBots` does the same:
the console must not be able to stop a run without recording who did it and why. The run row is
taken `FOR UPDATE` for the decision, so a user stopping their own run at the same moment cannot
have both callers read `running` and both decide they ended it. A run already stopped is
returned unchanged and writes **no** audit row — an audit entry for an action that changed
nothing would be a false record of an operator having stopped something.

Afterwards, outside the transaction, `BotService.SignalStop` tells the run's goroutine to stop
and republishes the row. That is new, and it is the narrow thing the console needs: the loop
noticing now rather than on its next check, and the user being told. Neither belongs inside the
caller's transaction.

`StopRun`'s body was factored into `stopRun` plus `runAndOwner` so the ownership check is the
only thing the user-facing path adds. Nothing about the decision changed.

### One ending, announced once

`BotRunRepo.Finalise` now returns whether *this* call wrote the ending. Two parties race to write
it — whoever asked for the stop, and the run's own goroutine on its way out — and the row guard
already made the write idempotent. The boolean extends that to everything that follows a write:
only the winner broadcasts `bot_run`, and only the winner increments
`bot_runs_stopped_total`. Before this the counter double-counted a user stop as well as
double-toasting it, which the Phase 2 report only caught the visible half of.

### Client

Two lines, so the client can name the new reason rather than falling through to its generic
ending: `'admin_stop'` on the `BotStopReason` union and one entry in `ENDINGS`
("Stopped by support"). No UI.

---

## 2. Deviations from the prompt

**Owner identity went on the admin read path, not on `botRunSelect`.** The prompt allowed
either; §1 says why this one.

**`BotStopAdminStop` is a new stop reason.** The prompt did not ask for one, but the alternative
is recording a per-run stop as `admin_kill`, which is the exact conflation the new audit action
exists to avoid. It needed no migration — the column is `VARCHAR(32)`, not an enum.

---

## 3. What the tests found

### A stopped run placed one more contract

`TestAdminStopsOneRunAndRecordsIt` failed on its first run with "a stopped run placed 1 more
contracts". Reproduced deliberately: a stop that lands while an `Open` transaction is still
uncommitted sees `CountOpenByRun == 0`, so the run is finalised outright rather than held in
`stopping`; the contract then commits under a stopped run.

The money is fine — the contract settles and folds into its run's totals by the same mechanism
that handles a straggler after a restart — but the account's live-run slot was released while
one of its contracts was still open, so a second run can start alongside it.

**Not from this work, and not fixed here.** The user-facing stop has the identical window and
predates Phase 3; the fix is for the decision to be made under the lock the placement itself
takes, which is a change to the money path. It is on the deferred list with the reproduction.

What changed instead is the tests: `startIdleRun` starts a run on a 2-second interval and waits
for its first contract to settle, so a stop lands on a loop that is demonstrably parked between
contracts. Every other bot test uses a 1 ms interval, where the loop is essentially never idle —
which is why this window had not been seen before.

### The announce-once test earns its place

Checked by mutation, the way the Phase 1 counters were. With both broadcast gates removed — the
code as it stood before this checkpoint — `TestARunStopIsAnnouncedOnce` fails with "the run's
ending was broadcast 2 times, want 1". With one gate removed it still passes, which is worth
recording: the duplicate needs both writers, and a test that only removed the goroutine's gate
would have reported a fix that was not there.

---

## 4. Tests

`internal/service/admin_bot_test.go`, following the existing admin and bot conventions
(`newHarness`, `newBotDriver`, `assertStopped`, `h.assertReconciled`):

| Test | What it establishes |
|---|---|
| `TestAdminListsLiveAndFinishedRunsWithTheirOwners` | finished runs are reachable at all; live still are; no status means both, newest first; each row carries the account and user its own account row says it has; history pages, with a total that counts everything rather than the page |
| `TestAdminStopsOneRunAndRecordsIt` | a short reason is refused; the run ends as `admin_stop` and not as `admin_kill`; the other user's run and the global flag are untouched; exactly one `bot_run_stop` audit row against the run, and no `bot_kill` row; a second stop changes nothing and writes no second audit row; an unknown run is `ErrNotFound` |
| `TestAdminStopWaitsForAnInFlightContract` | the console's stop respects `stopping`: the in-flight contract is counted before the run ends |
| `TestARunStopIsAnnouncedOnce` | a run that stops once is announced once |

---

## 5. Commands run, and what they said

| Command | Result |
|---|---|
| `go build ./...` | clean |
| `go vet ./...`, `go vet -tags integration ./...` | clean |
| `gofmt -l ./cmd ./internal ./pkg` | no output |
| `go test ./...` | ok — config, models, service, mpesaclient |
| `go test -tags integration ./internal/service/ -count=1` | **ok, 33.9s** — the whole suite, not just the new file |
| mutation check: both broadcast gates removed | `TestARunStopIsAnnouncedOnce` fails, 2 endings; restored |
| `npx tsc --noEmit` (web) | clean |
| `npx vitest run` | 11 files, 119 tests passed |

And over HTTP against a running API, because the service tests do not exercise the routes —
a scratch script driving the real endpoints:

```
  [PASS] a run starts                      [PASS] a short reason is refused
  [PASS] admin logs in                     [PASS] the run stops
  [PASS] live runs list                    [PASS] it reaches stopped
  [PASS] rows carry the owner              [PASS] with its own reason
  [PASS] the page is paged                 [PASS] automated trading is still on
  [PASS] finished runs are reachable       [PASS] audited as bot_run_stop
  [PASS] limit is honoured                 [PASS] against the run
  [PASS] an unknown run is 404             [PASS] a user token cannot stop runs from the console
```

The last one matters: `POST /admin/bot/runs/:id/stop` sits behind `AdminAuth` and
`RequireFullAdmin`, and a user's own token is refused rather than being allowed through to an
ownership check that would pass.

---

## 6. State

Every endpoint Checkpoint 2 and Checkpoint 3 need now exists and is tested. Nothing in the
console renders any of it yet: `Dashboard.LiveBotRuns` is still unrendered and there is no
`/admin/bot-runs` page.

Checkpoint 2 not started.
