# Phase 3 · Checkpoint 4 — admin e2e and close-out

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** `admin.spec.ts`, Phase 3's §6 exit criteria, and the last pass over
`auto-bot-deferred.md` before Phase 4.

---

## 1. What the e2e found

**Checkpoint 1's broadcast fix silently switched off the kill switch's notification.**

CP1 made `Finalise` report whether it wrote the ending, so only the winner announces it. That is
right for a stop, where the request and the run's goroutine race to write the same ending. It is
wrong for the kill switch, whose ending is written by `StopAllLive` — one UPDATE inside the
console's transaction. No goroutine wins its `Finalise` afterwards, so with CP1's change **none of
them announced anything**: every live run ended and nobody was told.

This is exactly what §6's criterion is worded to catch — "and the user is told why" — and it only
failed once a browser was watching a user's screen while an operator threw the switch.

The rule was never "the winner announces". It is **whoever writes an ending owes the
announcement**, and for the kill switch that is the switch itself. `BotService.StopAll` now
signals each run and republishes it, the same shape as `SignalStop` for the console's per-row
stop; the two share an `announce` helper. Each loop's `finalise` still stays quiet, so it remains
exactly one announcement per run.

Guarded in Go as well as in the browser: `TestBotKillSwitchStopsAllRuns` now asserts each of its
three runs was broadcast as stopped exactly once. Checked by mutation — with the announcement
removed it fails with "ending broadcast 0 times, want 1".

---

## 2. `admin.spec.ts`

Two cases, in a `test.describe.serial` block, and the order is deliberate: **the kill switch
turns automated trading off and nothing in the console turns it back on**, so a case that needs to
start a run cannot follow it. The per-row stop goes first, the kill switch last, and the reason is
written where a reader will meet it rather than in a report. `startRunViaApi` also fails with an
explicit message naming that cause, because the symptom otherwise is an undecoded 403.

That constraint is itself a finding, and it is now on the deferred list — see §4.

### stops one run from the runs table, and only that one

Two users, a run each. The console finds the row by whose run it is, presses Stop, and checks the
confirmation names the owner — the misclick guard CP3's dialog decision was argued on. Confirm is
disabled until a reason is written. After it: the toast, the row leaving the live list (waited
for, because a run holding a contract ends when that contract settles), **the other user's run
still running**, the same run reachable under Finished with "Stopped here" and no Stop button, and
an audit entry under its own action with the reason attached.

"Only that one" is the assertion that separates this control from the one in the header.

### stops all auto runs and the user is told why

§6's case, verbatim in intent. The user is logged in and watching their own app when it happens.
The dashboard shows a run live, the switch is thrown with a reason, and then:

- the operator gets "2 auto runs stopped";
- **the user gets "Auto run stopped · Automated trading was turned off"** — told why, not merely
  that something happened;
- the tile loses its "placing contracts now" sub-line;
- the audit log carries "Stopped all auto runs" with the reason.

---

## 3. Phase 3's exit criteria, explicitly

The plan, §6 Phase 3:

> **Built:** dashboard tile and kill button, `/admin/bot-runs` page, `adminApi.botRuns` /
> `adminApi.killBots`, `admin.spec.ts` case "stops all auto runs and the user is told why".
> **Exit:** admin e2e green; a kill from the console stops a live run within a second and the user
> sees the `admin_kill` toast.

| Clause | Met | Where |
|---|---|---|
| dashboard tile | yes | sixth tile, "Live auto runs", linking to the runs page when above zero (CP2, linked in CP3) |
| kill button | yes | header, `ReasonDialog`, `POST /admin/bot/kill` (CP2) |
| `/admin/bot-runs` page | yes | nav entry, route, table, filter chips, pagination, per-row stop (CP3) |
| `adminApi.botRuns` | yes | paged, `status=live\|stopped\|''` |
| `adminApi.killBots` | yes | and `adminApi.stopBotRun`, which §6 predates |
| `admin.spec.ts` case "stops all auto runs and the user is told why" | yes | plus a second case for the per-row stop |
| **admin e2e green** | yes | `--project=desktop --project=mobile`: **27 passed, 3 skipped**, exit 0, in the CI configuration (`digit_contracts_enabled=false`) |
| **a kill stops a live run within a second** | yes | the switch commits the row update in its own transaction and signals the goroutines immediately after; the browser sees the tile and the user's toast follow within the test's ordinary timeouts. The run row is `stopped` before the endpoint returns |
| **the user sees the `admin_kill` toast** | yes | asserted in the browser on the user's own page, including its body — and this is the clause that caught §1's regression |

**Phase 3's exit criteria are met.**

One honest qualification on "within a second": a run that holds a contract in flight goes to
`stopping` and ends when that contract settles, which on the mock feed is about a second and on a
live feed is one to two. The row stops being live immediately; the contract is not abandoned. The
UI says so rather than claiming otherwise.

---

## 4. The deferred pass, for Phase 4

Two new items, both found this checkpoint, both placed at the top with the stop-vs-open race:

1. **The console can turn automated trading off and cannot turn it back on.** `POST /admin/bot/kill`
   sets `bot_enabled=false` as part of what it does, and no console action sets it back — restoring
   the feature means editing `app_settings` by hand, which is the kind of thing done badly at three
   in the morning. Found by the e2e, where it forces the test ordering in §2. Wants an audited
   `POST /admin/bot/enable` and a dashboard control that shows the current state. Not built here:
   it is a new audited write endpoint, which is Checkpoint 1 work arriving at the end of a phase.
2. **The e2e suite is not typechecked.** `tsconfig.app.json` includes `src` only, so nothing checks
   `web/e2e`. Playwright transpiles specs without checking them, so a wrong field on a fixture is a
   runtime `undefined` rather than a build error — which is how a spec can assert against nothing
   and pass. Hit while writing §2: `registerViaApi`'s declared type had no `phone_number`, and only
   reading the API's actual response confirmed the spec was right. The type is widened; the tooling
   gap stays open.

**The stop-vs-open race from Checkpoint 1 remains the first item**, with its reproduction, and the
list's preamble now says Phase 4 starts there.

Also closed this checkpoint: the admin e2e itself, and the kill-switch announcement regression.
The four Phase 3 prerequisites stay recorded as closed rather than deleted, because that table is
what the fixes were measured against.

---

## 5. Commands run, and what they said

| Command | Result |
|---|---|
| `npx playwright test --project=desktop --project=mobile` | **27 passed, 3 skipped**, exit 0 — with `digit_contracts_enabled=false`, which is the CI job's configuration and the one `admin.spec.ts` runs under |
| `npx playwright test --project=digit-mobile --project=digit-desktop` | **20 passed, 2 skipped**, exit 0 — the user-facing suite, re-run because `StopAll` changed |
| `go test -tags integration ./internal/service/ -count=1` | **ok, 36.5s** |
| `go test ./...` | ok |
| `go vet ./...`, `go vet -tags integration ./...`, `gofmt -l` | clean |
| mutation: `StopAll`'s announcement removed | `TestBotKillSwitchStopsAllRuns` fails, "ending broadcast 0 times, want 1"; restored |

The digit suite matters here: `StopAll` is the kill switch, but `announce` is now shared with the
per-run path, and a change to how runs are republished could have broken the user's own run card.
It did not.

Flags were restored afterwards (`bot_enabled` and `digit_contracts_enabled` both `true`), and no
run is left live.

---

## 6. State at the end of Phase 3

The console sees automated trading and can act on it: a tile that says how much is running, a
table of what is running and what has run, a stop for one run and a stop for all of them, every
action audited under its own name, and the person whose run it was told what happened and why.

Phase 4 not started. Its list is `auto-bot-deferred.md`, first row first.
