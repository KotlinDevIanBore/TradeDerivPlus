# Phase 2 · Checkpoint 3 — BotRunCard and the live state

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Plan:** `docs/investigation-reports/auto-bot-plan.md` §5.2, §5.3
**Scope:** The run card, the `bot_run` socket event, the toasts, the Auto cell, the BOT chips.

---

## 0. Deferred-items tracking

`docs/investigation-reports/auto-bot-deferred.md` now holds every consciously-deferred item with
where it came from and where it is meant to land — open and closed. Seeded from the prior reports, so
things like `digit_max_stake` (promised in Phase 0, forgotten by Phase 1, noticed by a failing browser
check in Phase 2) no longer depend on anyone rereading the set.

Nine open items; four closed. Three of the open ones are the §10 product questions, which is where
they should be: unanswered, and visible.

---

## 1. What was built

| Piece | Where |
|---|---|
| `BotRunCard` | `features/trading/BotRunCard.tsx` — header, totals, stop-loss bar, last contract, Stop CTA |
| `bot_run` event | `hooks/useAccountStream.ts` — cache write, not invalidation |
| Stop toasts | `features/trading/botRunToast.ts` — a reason→tone table |
| Auto cell | `features/trading/SessionStrip.tsx` — the fourth cell the digit grid already had room for |
| BOT chips | `features/portfolio/DigitContractRows.tsx` — both the open card and the settled row |

**The card reports the run row rather than counting.** The server writes those totals inside the
transaction that settles each contract, so they agree with the contracts and the ledger by
construction; counting again on the client could only introduce a disagreement.

**The stop-loss bar measures losses only.** A run in profit sits at zero rather than showing a bar
creeping backwards, and it does not run past full once the floor is reached. Both pinned by tests.

**Two meanings of "stopping" are kept apart**: `run.status === 'stopping'` is the server's state — a
stop was asked for and a contract is still in flight — while the `stopping` prop is the request being
sent. The card disables on either and only says "Stopping after the current contract settles." for
the former, because that is the one that is actually true.

**Toast tones are grouped by where the ending came from**, since the three groups mean different
things to whoever started the run: a limit they set is their own instruction being honoured, something
imposed needs explaining, a fault needs owning. Only `take_profit` is the success tone. `stop_loss`
is deliberately the error tone — the limit worked, but money was lost, and a green tick over a loss
would read as a taunt. Every ending carries the realised figure, because that is what happened.

**The BOT chip is neutral, not coloured.** Whose hand placed a contract says nothing about how it
went, and the row already uses colour for that. It sits in the settled row's existing chip line and
in the open card's footer, and since Positions and History share both components, one change covers
both surfaces.

---

## 2. A problem this phase introduced, found by looking at it

The first screenshot of a working run showed the card behaving correctly — and a stack of toasts
reading *"R_75 won"*, *"R_75 lost"*, arriving every couple of seconds.

Those are the existing `digit_settled` toasts, which were right for manual trading: one deliberate
action, one confirmation. Under an automated run they become a stream, and a run lasting its full
half hour would produce hundreds — burying everything that *is* news, including the toast saying the
run stopped.

Per-contract toasts are now suppressed when `contract.bot_run_id` is set. The card is on screen
showing the outcome, the counters and the last result already; the toast adds noise and nothing else.
Caught by reading the screenshot rather than by a test, which is the argument for taking them.

---

## 3. Verifying both live paths, separately

The card looks identical whether the socket or the poll is feeding it, so "it updates" proves
nothing. Each path was isolated.

**The socket path** — two independent proofs, because update *frequency* cannot distinguish them (the
card cannot change more often than contracts settle, so a 2s poll and a socket look the same at a
3s-per-contract cadence; my first attempt asserted exactly that and was wrong to):

1. Frames observed on the wire. Playwright listens to the account WebSocket and asserts
   `"type":"bot_run"` frames arrive carrying `contracts_placed` and `"status":"running"`.
2. The stop toast. It is raised only from the socket handler — **no poll path produces one** — so a
   toast appearing is proof the socket is driving the UI, not just arriving.

**The poll path** — the account socket is blocked outright (`page.routeWebSocket` closing every
attempt), and then:

```
[PASS] the account socket was blocked for this run   (2 attempts refused)
[PASS] the run card still appears without a socket
[PASS] the poll alone still advances the card        (0 → 2 with no socket)
[PASS] the ticket returns when the run ends, socket or not
```

So the fallback is a fallback in fact, not just in intent.

---

## 4. Corrections

**A real bug: the Auto cell said "—" after a run stopped.** `useBotRuns(…, 'stopped')` is cached and
had nothing to refetch it, so the cell could not report the ending it was meant to. The `bot_run`
handler now invalidates the `botRuns` prefix on top of writing the live entry — the prefix covers
every status, and the 'live' entry it just wrote simply refetches to the same value.

**A duplicate I wrote and then removed.** `runSelectionLabel` in `BotRunCard` reimplemented
`selectionLabel` from `digitLabels.ts`. Deleted in favour of the existing one, which also makes the
card's header read identically to the contract rows. Lint caught it as an export warning; the
duplication was the real problem.

**Two test-harness fixes of mine:** the Positions Closed tab needed a proper click and a wait rather
than a swallowed `.catch()`, and `user.click` on a disabled button does not take a
`pointerEventsCheck` option in this version.

---

## 5. Verification

```bash
cd web
npm run typecheck                    # clean
npm run test                         # 119 tests, 11 files (was 106 / 10)
npm run lint                         # zero warnings in the files this checkpoint touched
npx playwright test --project=digit-desktop --project=digit-mobile
# exit 0 — 10 passed, 2 skipped

cd .. && gofmt -l ./cmd ./internal ./pkg && go build ./... && go vet ./... && go test ./...
python3 scripts/bot_check.py         # AUTOMATED RUN VERIFIED
```

`BotRunCard.test.tsx` — 13 tests in three groups: what it reports (header, placements against the
limit, W/L/V kept separate because a refund is not a loss, the Over/Under selection, the last
contract, and its absence before anything settles); the stop-loss bar (measured, zero while in
profit, capped at full); and stopping (reports once, shows the winding-down state on the server's
`stopping`, refuses a second stop while one is in flight, and says the run stops on its own
otherwise).

In the browser, 20 checks across both paths — see §3. The screenshot shows the card mid-run with
`PLACED 3/6`, `W/L/V 1/1/0`, `REALISED -$0.48`, the stop-loss sliver at `$0.48 of $40.00`, the last
contract line, MANUAL greyed with its reason, the Auto cell reading `Running · 3/6`, and the BOT chip
on the open contract in Positions.

---

## 6. Not done in this checkpoint

`digit.auto.spec.ts` is Checkpoint 4 — the scratch script that verified all this is deliberately not
committed, and its assertions become that spec. No admin UI (Phase 3). `useBotRuns` is now used for
the Auto cell's "last run" but there is still no screen listing past runs, which stays on the
deferred list for Phase 4.
