# Phase 4 · Checkpoint 1 — the stop-vs-open race

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** the first item on `auto-bot-deferred.md`. A change to the money path, treated the way
Phase 1 CP2's counters-in-transaction change was.

---

## 1. The race, stated exactly

A stop decides between `stopping` and `stopped` by counting the run's open contracts
(`CountOpenByRun`). A count cannot see an `INSERT` whose transaction has not committed.

So a stop arriving while a placement was in flight read zero, took the idle branch, and wrote the
ending. The placement then committed — a contract belonging to a run that was already `stopped`.
Two things followed:

- the account's **one-live-run slot was released while one of its contracts was still open**, so a
  second run could start alongside the first's in-flight contract;
- the run's own counters kept moving after it had ended, which is correct by design for a
  straggler after a restart but is not what "stopped" should mean a second later.

The money was never wrong — the ledger writes are inside the same transaction as the wallet
adjustment, and the straggler settles and folds into its run's totals exactly as a post-restart
contract does. What was wrong was the guarantee.

---

## 2. The fix

CP1's own diagnosis was "make the decision under the same lock the placement takes". The lock the
placement takes is the **wallet** row — but that alone is not sufficient, because a stop that wins
the wallet lock still lets the placement proceed afterwards and open a contract under a stopped
run. The lock has to be one both sides respect **and** the placement has to re-read the run's state
while holding it.

So the lock is the **run's own row**, and both sides take it:

- `DigitService.Open`, when `BotRunID` is set: `BotRuns.GetForUpdate` inside the existing
  transaction, after the wallet and account locks, before the contract is inserted. If the run is
  not `running`, the placement returns `ErrBotRunNotLive` and the transaction rolls back.
- `applyStop` — new, and shared by the user's stop and the console's — takes the same
  `GetForUpdate` before it counts.

That leaves exactly two orderings, and both are now correct:

| Order | What happens |
|---|---|
| Placement first | It holds the run row. The stop blocks, then counts the committed contract and writes `stopping`; the run keeps the account's slot until that contract settles. |
| Stop first | It holds the run row, sees nothing open, writes the ending. The placement then takes the lock, reads `stopped`, and refuses. No contract, no counter movement, and the slot was free because nothing was in flight. |

Two details that matter:

**Only `running` may place, not `running` or `stopping`.** `stopping` means a stop is waiting on a
contract; another contract is precisely what it is waiting not to have.

**Lock order is unchanged and still consistent.** `Open` takes wallet → account → bot_runs;
`settle` and `voidContract` take contract → wallet → bot_runs (`ApplySettlement` is an UPDATE on
that row). Nothing takes bot_runs before a wallet, so there is no cycle. The run lock went
*after* the wallet lock in `Open` deliberately, to keep that true.

**`applyStop` takes a transaction-scoped store rather than opening its own**, so the console's stop
can still commit its audit row in the same transaction as the status change — the invariant Phase 3
CP1 established.

### The loop

A refused placement is not a fault. `ErrBotRunNotLive` gets its own case in the run loop, which
finalises with the external stop's reason; `Finalise`'s guard means it loses, so nothing is
announced or counted twice. Without that case it would have fallen to `default` and recorded
`error` — putting a fault on a run a person deliberately stopped.

---

## 3. Verification

### The two regression tests

`internal/service/bot_stop_race_test.go` pins both orderings. Neither depends on goroutine
timing: **the locks themselves are the synchronisation**, taken by a transaction the test owns. No
bot loop runs in either — a run row is created directly and `Open` is called by hand — so what is
under test is the two operations and nothing else.

- `TestPlacementRefusedOnceItsRunIsStopped` — the test holds the **wallet** row, so a real `Open`
  blocks inside its transaction before inserting anything: exactly the window the bug lived in. It
  proves the placement is genuinely stuck (it has not returned after 300 ms), stops the run, then
  releases. The placement must come back `ErrBotRunNotLive`, with nothing open and nothing counted.
- `TestStopWaitsForAPlacementAlreadyHoldingTheRun` — the test holds the **run** row, stands in for
  a placement mid-transaction, and proves the stop blocks rather than deciding around it. It then
  commits a contract from inside the holding transaction and releases; the stop must come back with
  the run in `stopping`, having counted what it waited for.

### Mutation, both directions

| Mutation | Result |
|---|---|
| `Open`'s run lock and status check removed | `TestPlacementRefusedOnceItsRunIsStopped` fails: *"placement returned `<nil>`, want ErrBotRunNotLive — a stopped run must not place"* |
| `applyStop`'s `GetForUpdate` removed | `TestStopWaitsForAPlacementAlreadyHoldingTheRun` fails: *"status `stopped` with a contract in flight, want stopping"* |

The second message is the original bug in one line. Both were restored and the suite re-run.

### Everything the change could reach

`Open` is the money path, so the whole of it was re-run rather than the stop tests alone.

| Command | Result |
|---|---|
| the named stop tests — `TestAdminStopsOneRunAndRecordsIt`, `TestAdminStopWaitsForAnInFlightContract`, `TestBotUserStopWaitsForInFlight`, `TestBotStopRunIsIdempotent`, `TestARunStopIsAnnouncedOnce`, `TestBotKillSwitchStopsAllRuns` | all pass |
| `go test -tags integration ./internal/service/ -count=1` | **ok, 37.8s** — the whole suite, including the concurrency and reconciliation tests |
| `go test ./...` | ok |
| `go vet ./...`, `go vet -tags integration ./...`, `gofmt -l` | clean |
| `python3 scripts/bot_check.py` | **AUTOMATED RUN VERIFIED** — counters, ledger and balance all agree |
| `npx playwright test --project=digit-mobile --project=digit-desktop` | **20 passed, 2 skipped**, exit 0 |
| `npx playwright test --project=mobile --project=desktop` | **28 passed, 3 skipped**, exit 0, in the CI configuration |

---

## 4. Scope

One new sentinel (`ErrBotRunNotLive`, 409 `bot_run_not_live`) that no person can meet: only
`BotService` sets `BotRunID`. No schema change, no API change a client would notice, no UI change.

`stopRun`'s fast paths — a stopped run returns its row, a `stopping` run signals and returns —
still read without the lock. They only skip work, and `applyStop` is correct if they are wrong, so
the lock is not made to do anything it does not need to.

---

## 5. State

The deferred list's first item is closed against **P4 CP1**. What remains open there is a product
decision, a tooling decision, or a ticket of its own; nothing blocks the rest of Phase 4.

Checkpoint 2 (load) not started.
