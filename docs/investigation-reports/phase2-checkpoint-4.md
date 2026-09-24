# Phase 2 · Checkpoint 4 — `digit.auto.spec.ts` and the exit criteria

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** the browser spec for AUTO mode, a pass over the deferred list for Phase 3's sake, and
an explicit reckoning against the plan's §6 exit criteria for Phase 2.

Phase 3 was not started.

---

## 1. What was built

### `web/e2e/digit.auto.spec.ts` — five tests, both projects

The scratch script's assertions are now a committed spec. Each test is one claim about the
feature, and the file's header says what the suite is and is not checking: not that a run makes
money — it cannot — but that it does what it was told, reports itself honestly while doing it,
and that the money adds up when it stops.

| Test | What it establishes |
|---|---|
| carries the mode on the URL, and leaves the instrument alone | `?mode=auto` merges into the query string rather than replacing it, survives a reload, and disappears again on the way back rather than being written as `mode=manual` |
| states what a run costs before it starts | the expected return comes from the quote (−2.40% on Even/Odd, −10.00% on Matches with its warning), the worst case is stated in money, and both the stake ceiling and the missing stop loss refuse a start before a request goes out |
| places, reports itself, stops on its limit, and the balance reconciles | the §6 criterion end to end |
| stops when told to, and says so | the other way a run ends: limits out of reach, so only the button can stop it |
| keeps reporting a run when the account socket drops | `routeWebSocket` refuses every `/ws/account` connection, so anything that moves is the 2 s poll |

### `web/e2e/helpers.ts`

- `startAutoRun(page, {stopLoss, maxContracts})` and `placedCount(page)` (from CP3's scratch work).
- `submitSignup` — see §3.1.

### `web/src/features/trading/botRunToast.ts`

An ending is announced once per run. See §3.2.

---

## 2. Phase 2's exit criteria, clause by clause

The plan, §6 Phase 2:

> **Exit:** `digit.auto.spec.ts` (mobile and desktop): switch to AUTO → start with stop loss $30
> → run card shows placements climbing → stop → toast → BOT chip on the settled rows → balance
> equals starting balance + realised. Component tests for `BotTicket` (validation, economics
> line, disabled reasons) and `BotRunCard` (stopping state) in the props-in/spies-out convention.

| Clause | Met | Where |
|---|---|---|
| mobile **and** desktop | yes | `digit-mobile` and `digit-desktop` both run all five tests; 20 passed / 2 skipped per project pair, twice in a row. The 2 skips are `digit.panels.spec.ts`'s viewport guards, which predate this work |
| switch to AUTO | yes | `startAutoRun` clicks the tab; the mode test proves the switch survives a reload and keeps the symbol |
| start with stop loss $30 | yes | `startAutoRun(page, { stopLoss: '30', maxContracts: '3' })` in the criterion test |
| run card shows placements climbing | yes | `placedCount` is read off the card twice and the second must exceed the first, inside `toPass` |
| stop | yes, **both ways** | the criterion test lets the run reach `max_contracts` and stop itself; "stops when told to" presses Stop with the limits out of reach. The second was added in this checkpoint — see §3.3 |
| toast | yes | `getByRole('status')` carries `Auto run stopped` and the realised figure over N contracts |
| BOT chip on the settled rows | yes | `Bot` chip asserted in the Positions panel's Closed tab and in History |
| balance equals starting balance + realised | yes | not a range: three $10 contracts can end in a handful of places, so the test enumerates them (`wins * 9.52 − losses * 10` for every split of 3) and requires the movement to be one of them |
| `BotTicket` component tests — validation, economics line, disabled reasons | yes | 24 tests in `BotTicket.test.tsx` across four groups: economics (7), validation (7), what it submits (3), when it cannot start (7), including the three real-money gating states |
| `BotRunCard` — stopping state | yes | `BotRunCard.test.tsx`: the server's `stopping` disables the button and says the run is winding down; a stop in flight cannot be sent twice |
| props-in/spies-out convention | yes | both files render with plain props and `vi.fn()` callbacks; no MSW, no `QueryClientProvider` |

**Phase 2's exit criteria are met.**

---

## 3. Deviations and what they cost

### 3.1 A shared limiter made the suite flaky, and it was not the suite's fault

Running the full digit suite end to end failed differently on each attempt — three tests one
run, two the next, never the same ones. The cause was `POST /auth/register` returning 429: the
auth limiter is keyed by client IP (`middleware/ratelimit.go`), and every user a browser suite
invents shares one, so twenty signups look to it like one person signing up twenty times. CI
already raises the limits for the digit job (`AUTH_RATE_EVERY: 200ms`, `AUTH_RATE_BURST: 25`);
a developer running the suite locally has no such thing and got a `waitForURL` that timed out
sixty seconds later with nothing to point at.

`submitSignup` now treats a refusal as what it is — the suite queueing behind itself — waits a
refill and submits again, up to four times, and rethrows anything else untouched so a signup
that breaks for a real reason still breaks the test. The failure message, when it does give up,
names the cause.

Two real findings came out of this and are recorded in `auto-bot-deferred.md` rather than fixed
here, because both are auth and neither is from this work:

- **A rate-limited refresh signs the user out.** `refreshAccessToken` clears the session on any
  non-2xx, so a 429 or a 502 on `POST /auth/refresh` is indistinguishable from a revoked token.
  Reproduced in the browser: a reload during a limiter burst landed on the login screen with a
  perfectly good session. Only 401/403 mean the credential is bad.
- **CORS preflights are charged to the auth limiter.** 54 preflights for 54 signup attempts in
  one run — every browser signup costs two tokens where a non-browser client pays one, which is
  the wrong way round.

### 3.2 One stop, two toasts

The user-stop test found a duplicate: pressing Stop on an idle run produced two identical
"Auto run stopped" toasts on desktop and one on mobile. Both come from the server — `StopRun`
finalises the row and broadcasts it, then the run's own goroutine reaches `finalise` and
broadcasts the same stopped row. `Finalise` is guarded so the row is written once; the broadcast
is not guarded at all, and which of the two lands first is a race, which is why one viewport saw
it and the other did not.

Fixed on the client, where the invariant belongs: `announceRunStopped` remembers the last run it
announced and ignores a repeat. A duplicate socket delivery must not double-toast regardless of
what caused it. One id rather than a set is enough — an account can only have one live run, so
endings cannot interleave.

The server-side duplicate is logged as its own item: the fix there is for `Finalise` to report
whether it wrote the ending, and for only the winner to broadcast.

### 3.3 A missing exit clause, added rather than argued away

§6 says "stop → toast", and the spec as first written only ever let runs stop themselves on
`max_contracts`. That is an ending, but it is not the one the user causes, and it is the path
that goes through `StopRun`, the `stopping` state and the ownership check. The fifth test
closes it. It is also what surfaced §3.2 — the self-stopping path never had the duplicate.

### 3.4 A wait that would have passed either way

The mode test waited for the manual ticket after reloading in AUTO mode. Both tickets carry a
button called "Even" — the manual ticket's side button and the bot ticket's `SideChoice` — so
the wait was satisfied by whichever mode came back, and the test would have passed even if the
reload had dropped the mode entirely. It now waits on the AUTO tab's `aria-selected` and the
Start button, which only one of the two modes can satisfy.

---

## 4. The deferred-items pass, and what Phase 3 assumes

Reading `§5.4` against the API rather than against the last checkpoint's summary turned up four
things the admin console would silently assume exist. They are now a table of their own in
`auto-bot-deferred.md` under **"Phase 3 would assume these exist. They do not."**:

1. `GET /admin/bot/runs?status=stopped` returns an **empty list by design** — `ListBotRuns`
   returns early with the comment "Not a use case the console has yet". §5.4 asks for a table of
   live **and recent** runs; recent runs are not reachable at all.
2. **No admin single-run stop.** There is `POST /admin/bot/kill` (everything) and
   `POST /bot/runs/:id/stop`, which is ownership-checked against a *user* id and so cannot be
   called by an admin. §5.4's "per-row stop" has no endpoint. An operator can stop every run or
   none.
3. **A run carries no owner identity.** `botRunSelect` joins `symbols` only; `BotRun` has an
   `account_id` and symbol fields, no account number and no user. §5.4's table columns are
   "user, account". `AdminUserRow` embeds `models.User` — that is the console's established
   shape, and a run has no equivalent.
4. **No audited action for stopping one run.** `models.ActionBotKill` is the only bot action.
   Every console action that touches money is audited; a per-row stop needs its own constant.

Also noted: the admin list is unpaginated (`{items, total}`, no page), which is fine for live
runs — `BOT_MAX_RUNS` caps them at 50 — and not fine for a history that grows without bound.

Nine open items were already on the list; two more were added this checkpoint (§3.1) plus the
broadcast duplicate (§3.2), and nothing was closed.

---

## 5. Commands run, and what they said

The API under test was started with the digit job's own limiter settings, which is the
environment the suite is specified against:

```
AUTH_RATE_EVERY=200ms AUTH_RATE_BURST=25 ./api
```

| Command | Result |
|---|---|
| `npx playwright test --project=digit-mobile --project=digit-desktop` | **20 passed, 2 skipped** — run twice back to back, exit 0 both times |
| `npx playwright test --project=digit-mobile --project=digit-desktop digit.auto.spec.ts` | **10 passed** (5 tests × 2 projects), exit 0 |
| `npx playwright test --project=mobile --project=desktop` | **25 passed, 3 skipped**, exit 0 — the CFD suite, run with `digit_contracts_enabled` flipped off and back, because `register` is shared by every spec and this checkpoint changed it |
| `npx vitest run` | **11 files, 119 tests passed** |
| `npx tsc --noEmit` | clean |
| `npm run lint` | exit 0; warnings only, all pre-existing (`OrderTicket`, `usePriceFeed`, `TradeScreen`, and `DigitTradeScreen`'s `waitedForPrice` effect, none of them bot code) |

The two skips in the digit suite are `digit.panels.spec.ts`'s viewport guards; the three in the
CFD suite are its own. No test of this phase's is skipped.

---

## 6. State at the end of Phase 2

A user for whom `bot_enabled` is on can switch the trade surface to AUTO, read what a run will
cost before starting one, start it, watch it place and settle from a card that reports the run's
own counters, stop it or let it stop itself, and see every contract it placed attributed as
`Bot` in Positions and History. Real-money runs remain gated: the UI is built and tested in all
three states and `bot_real_money_enabled` is still `false`.

Not built, by instruction: the admin console (Phase 3), which should not begin before the four
gaps in §4 are dealt with.
