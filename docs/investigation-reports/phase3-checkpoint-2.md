# Phase 3 · Checkpoint 2 — the dashboard

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** §5.4's dashboard half — the sixth tile and the kill switch. The runs page is
Checkpoint 3.

---

## 1. What was built

### The sixth tile

`Dashboard.LiveBotRuns` has been populated since Phase 1 and rendered nowhere. The tile grid goes
from five columns to six, and **Live auto runs** joins it, with "placing contracts now" under the
figure when it is above zero.

It is shown whether or not anything is running. Zero is information an operator wants — it is
how you tell "nobody is using this" from "the feature is off" — and a tile that appeared and
disappeared would make the grid jump.

### The kill switch

A danger-toned **Stop all auto runs** in the page header, through the existing `ReasonDialog`, on
`POST /admin/bot/kill`. The dialog states both halves of what the endpoint does, because the
second is the one an operator can miss:

> 2 live runs will stop after the contract each has in flight settles. Automated trading will
> also be turned off, so nobody can start a new run until it is turned back on.

Stopping the runs without the flag would leave users free to start again the moment the console's
back was turned, which is not what anyone reaching for a kill switch means. The sentence also
declines to promise immediacy the server does not offer: a run holding a contract ends when that
contract settles, not when the button is pressed.

Offered even at zero live runs — turning the feature off ahead of trouble is a legitimate use of
it, and the copy adapts ("No runs are live right now").

On success: a toast counting what stopped, and every `['admin']` query invalidated, since the
tile, the audit list and any runs table are all stale at once.

### Audit labels for the two bot actions

Not in the prompt, and worth the four lines. The dashboard's recent-actions list rendered the
console's own new rows as raw slugs — `bot_run_stop`, `bot_kill` — because `actionLabel` had no
entry for them. The plan (§5.4) said the audit log "renders any action string" and that was
acceptable while nothing in the console produced those rows; now that the console writes them,
reading its own output as an unformatted identifier is a defect. They are now "Stopped all auto
runs" and "Stopped an auto run", both in the accent tone the other destructive actions use.

Colour does not distinguish the two. The label does. Tone in that list is for what kind of day it
was, not for blast radius.

---

## 2. Deviations

**The tile does not link anywhere yet.** §5.4 asks for it to link to the runs page when the count
is above zero. That page arrives in Checkpoint 3, and shipping a link to a route that does not
exist is worse than shipping none. The link goes in with the page it points at, and the deferred
list now says so against that row rather than leaving it to memory.

---

## 3. Tests

`killConsequence` is extracted from the dialog and unit-tested, which is the whole of the new
logic: whether anything is running, and how to count it. Five cases in
`src/features/admin/screens/Dashboard.test.tsx` — the "also turned off" half is present at every
count, nothing is claimed to stop when nothing is live, one run is singular and seven are plural,
and a live run is described as stopping after its contract settles rather than at once.

The mutation wiring is deliberately not unit-tested. It would need a `QueryClientProvider`, which
this codebase's component tests do not use, and Checkpoint 4's `admin.spec.ts` case exercises the
same path against the real server — which is where a kill switch should be proven anyway.

---

## 4. Verified in the browser

Against the real stack, with two live runs started through the API first.

| Step | Observed |
|---|---|
| Dashboard, two runs live | six tiles; **Live auto runs 2**, "placing contracts now" |
| Press Stop all auto runs | dialog reads "2 live runs will stop after the contract each has in flight settles. Automated trading will also be turned off…"; confirm disabled, "10 more characters" |
| Confirm with a reason | toast "2 auto runs stopped / Automated trading is off until it is turned back on." |
| After | tile reads **0** and loses its sub-line; a "Stopped all auto runs" row appears in recent actions, just now |
| `app_settings` | `bot_enabled` = `false` — the flag really went with the runs, which is the half the copy promises |
| 375 px viewport | three rows of two tiles; the header button wraps without crowding the title |

`bot_enabled` was set back to `true` afterwards, since the check genuinely turned the feature off.

---

## 5. Commands run

| Command | Result |
|---|---|
| `npx tsc --noEmit` | clean |
| `npm run lint` | exit 0, warnings only and all pre-existing |
| `npx vitest run` | **12 files, 124 tests passed** (was 11 / 119) |

No backend change in this checkpoint, so no Go run; Checkpoint 1's suite stands.

A local `.claude/launch.json` was added so the browser preview can start the dev server. It is
gitignored, so it is a convenience on this machine rather than a change to the project.

---

## 6. State

The dashboard reports automated trading and can stop all of it. Nothing yet lists individual
runs, and the per-row stop built in Checkpoint 1 has no UI.

Checkpoint 3 not started.
