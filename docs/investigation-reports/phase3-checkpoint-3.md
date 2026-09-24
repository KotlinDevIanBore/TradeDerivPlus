# Phase 3 · Checkpoint 3 — the auto runs page

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** `/admin/bot-runs` — the table §5.4 asks for, the per-row stop, and the dashboard
tile's link to it.

---

## 1. The confirmation-dialog decision

**A per-row stop goes through `ReasonDialog`, exactly like the kill switch.** Not a lighter
confirm, not a one-click button.

The argument for something lighter is real: stopping one run is far less consequential than
stopping every run and turning the feature off, and a modal for a single row is friction on a
page an operator might use repeatedly.

It loses on three counts.

1. **The reason is not ceremony, it is the record.** `POST /admin/bot/runs/:id/stop` requires a
   reason of at least ten characters and writes it to the audit log in the same transaction as
   the status change (P3 CP1). A one-click stop would have to either send nothing — a 422 — or
   invent a reason on the operator's behalf, which makes the audit trail a fabrication. An audit
   log that records "Stopped an auto run · reason: stopped from the console" is worse than none,
   because it looks like evidence.
2. **The asymmetry would teach the wrong thing.** If the kill switch demands a written reason and
   a single stop does not, operators learn that single stops are casual. They are not casual to
   the person whose run it is: someone's money stops moving because a stranger clicked a row.
   Every other console action that reaches into one user's account — freeze, unfreeze, adjust,
   reject KYC — goes through this dialog. This is one of those.
3. **Misclicks are the actual risk on this screen.** The rows are near-identical: same symbol,
   same stake, same contract, differing only by a phone number and an account number. A
   confirmation that *names whose run it is* is the guard against acting on the row above the one
   you meant. That is why the copy repeats the user and the account rather than saying "this
   run".

So the dialog stays, and its copy follows the standard set by the kill switch's — say exactly
what happens, and do not promise an immediacy the server does not offer:

> 254712345678's run on account 900002 will stop after any contract it has in flight settles.
> Automated trading stays on, and they can start another run.

The second sentence is the one that earns its place: it is the difference between this control
and the one in the header, stated at the moment an operator could still be reaching for the wrong
one. A run already in `stopping` says so instead, rather than offering to stop what is already
stopping.

---

## 2. What was built

### `/admin/bot-runs`

A sixth nav entry (**Auto runs**, Lucide `Bot` — free in the admin shell, unlike the user app
where Signals has it), a route, and a `DataTable` in the pattern `Transactions` established:
filter chips, URL-backed state, and the shared pagination footer.

Filter chips are **Live / Finished / All**. Finished is the tab Checkpoint 1 existed for; before
it, a run disappeared from the console the moment it ended.

Live runs refetch every 10 seconds; a page of history does not refetch at all. Ten rather than
two: an operator reading a row should not have the table move under their cursor, and nothing on
this screen is a decision that turns on the last two seconds.

Per row: started, user (linked to their detail page), account, contract, stake, placed, realised,
status, and — while it is live — a Stop button.

### The per-row stop

`adminApi.stopBotRun`, the dialog above, and a toast that distinguishes the two outcomes the
server can return: "Run stopped" with what it ended up doing, or "Run stopping · It ends when
the contract it has in flight settles."

### The dashboard tile's link

Now that the page exists, the tile links to it — and only when the count is above zero, so it
never invites a click that lands on an empty table. That closes the deferred item CP2 opened.

### `StatusBadge` learns the run statuses

`running`, `stopping`, `stopped`. Only `stopping` gets a colour of its own: it is the state that
means a stop was asked for and a contract is still in flight, which is neither of the other two,
and an operator who cannot see it will press Stop again.

---

## 3. Deviations from §5.4

§5.4 lists the columns as "user, account, symbol, contract, stake, placed, realised, status,
reason". Built as specified except for two merges, both forced by the same thing — §5.4 named
nine columns and also asked for a per-row stop, which is a tenth, and the stop button ended up
off the right edge of a 1440 px screen.

- **Symbol folded into Contract.** "R_75 Even/Odd Even" is one fact about a run, not three.
- **Reason folded into Status.** On the Live tab — where most of the looking happens — a Reason
  column is empty on every row. Merged, a finished run reads "Stopped · Stop loss" and a live one
  reads "Running".

Both were made after seeing the table in a browser, not before. The first attempt rendered
faithfully to the column list and put the page's primary action behind a horizontal scrollbar,
which is a worse failure than departing from a column count.

Stop reasons are also given console labels rather than the user-facing sentences
(`Stop loss`, `Kill switch`, `Feed down`, `Stopped here`). The user is told "Auto run hit its
stop loss"; an operator scanning a column wants the two words that distinguish this row from the
one above it. Faults — `feed_down`, `error`, `insufficient_funds`, `account_frozen` — are in the
accent colour, so a screen full of honoured limits reads differently from a screen full of things
going wrong.

A real-money account is marked in the row. While `bot_real_money_enabled` is off, a real account
should never appear here at all, and making the wrong one obvious costs one badge.

---

## 4. Tests

`stopConsequence` is extracted and unit-tested the way `killConsequence` was: the sentence is the
part with decisions in it. Four cases — it names the user and the account (the misclick guard),
it says a contract in flight still has to settle, it says the feature stays on and they can start
another, and a run already stopping says so rather than being offered a stop.

The table and the mutation are left to Checkpoint 4's `admin.spec.ts`, which drives the real
server.

---

## 5. Verified in the browser

Against the real stack, with two live runs of different contract types started through the API.

| Step | Observed |
|---|---|
| Nav → Auto runs | table of both runs, newest first; placements climbing on each refetch |
| Columns | user phone linked, account number, `R_75 Over/Under Over 4`, `$10.00 /2s`, `30/200 16/13`, realised signed and coloured, `Running` |
| Width at 1440 px | no horizontal scrollbar; Stop reachable without scrolling (it was not, before the two merges) |
| Press Stop | dialog names the user and the account, confirm disabled at "10 more characters" |
| Confirm with a reason | toast "Run stopped · 40 contracts placed, $9.50 realised"; the row leaves the Live tab |
| Finished tab | 81 runs, newest first, reasons as console labels — "Stopped here", "Kill switch", "Contract limit", "User stopped"; no Stop button on any of them |
| Pagination | "Showing 1–25 of 81", and `?status=stopped&page=2` reloads to "Showing 26–50 of 81" |
| Dashboard tile | reads **1** in accent while a run is live, and clicking it lands on the page |

Runs started for the check were stopped afterwards; no live run is left behind.

---

## 6. Commands run

| Command | Result |
|---|---|
| `npx tsc --noEmit` | clean |
| `npm run lint` | exit 0, warnings only and all pre-existing |
| `npx vitest run` | **13 files, 128 tests passed** (was 12 / 124) |

No backend change in this checkpoint.

---

## 7. State

§5.4 is built. The console reports automated trading, can stop one run or all of them, and can
see what has run as well as what is running.

What is left in Phase 3 is Checkpoint 4: the `admin.spec.ts` cases and the §6 exit criteria.

Checkpoint 4 not started.
