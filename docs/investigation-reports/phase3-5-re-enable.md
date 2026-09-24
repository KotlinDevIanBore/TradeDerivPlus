# Phase 3.5 — the way back

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** `POST /admin/bot/enable`, the dashboard's reading of the switch, and the e2e case that
proves the round trip. Closes the deferred item Checkpoint 4 opened.

---

## 1. The reason requirement — the decision

**Re-enabling requires a written reason, exactly as the kill switch does.**

The case against is real, and it is the stronger-sounding one at first. This is the restorative
direction. An operator doing it is ending an outage, probably at speed, probably at night, and
friction on a recovery path is friction at the worst possible moment. The console already has a
precedent for skipping the reason on a safe action: approving a KYC submission asks for none.

It loses on three counts.

**First, across these two actions the audit log's job is the incident, not the blame.** A kill
that says "halting automated trading pending a payout table review" followed by a restart that
says nothing leaves the only question anyone will actually ask — *what changed in between?* —
unanswerable forever. The reason on the way back is the single place that answer can live. An
outage the log never ends is not a log of the outage.

**Second, this is not obviously the safe direction.** The switch was thrown because something was
wrong. Turning it back on asserts that the thing is fixed, and that is a claim about the world.
Claims about the world are what an audit log is for. The nearest precedent is not KYC approval but
`Unfreeze`, which is equally restorative and which `requireReason` already guards — an account is
unfrozen because someone decided the suspicion was resolved, and the console makes them say so.

**Third, the KYC precedent does not transfer, because approving a submission leaves its own
trace.** The user becomes verified; the submission is stamped with who approved it and when. The
outcome is legible without a sentence. Re-enabling leaves no trace of its own: a boolean returns
to a value it already held. Without a written reason nothing distinguishes "the feed recovered"
from somebody clicking the wrong control, and both look identical in `app_settings` the next
morning.

Ten characters during an incident is not real friction next to being unable to explain the restart
afterwards.

The reasoning lives on `AdminService.EnableBots` as well as here, so whoever reopens it meets the
argument at the code rather than having to find this file.

---

## 2. What was built

### `POST /admin/bot/enable`

In the `actions` rate bucket beside the kill switch. Its own audited action —
`models.ActionBotEnable` (`bot_enable`) over `TargetPlatform` — not a reuse of `bot_kill` or
`bot_run_stop`, because an audit log that recorded the stop and the restart under one name could
not tell the two apart, which is the whole reason the row is worth writing.

The flag write and the audit row commit together, and the settings cache is busted afterwards for
the same reason `KillBots` busts it: a cached "off" outliving the decision would refuse a user for
up to ten seconds for no reason they could see.

Deliberately **not** symmetric in one respect: it does not resume anything. Runs never resume —
not across a service restart (plan §4.3) and not across the kill switch. What this restores is the
ability to start a run.

### The dashboard reads the switch

`Dashboard` gains `bot_enabled`, read from the `app_settings` row rather than through
`SettingsService`'s cache: the console is where the flag is changed, and an operator who has just
turned it off must not be shown "on" for another ten seconds.

The header control now reflects state rather than assuming it — danger "Stop all auto runs" when
on, safe-toned "Turn auto runs back on" when off — and the "Live auto runs" tile carries an amber
border and the sub-line "automated trading is off". That sub-line is the point of the tile change:
a zero means two different things, *nobody is running one* and *nobody can*, and without it the
console shows the same figure for both.

The dialog's copy states the one thing an operator is most likely to assume the opposite of:

> Users will be able to start auto runs again. Runs the kill switch stopped stay stopped —
> nothing resumes on its own.

`bot_enable` is labelled in the audit log as "Turned auto runs back on", in the profit tone rather
than the accent — reading down the log, it is the row that says the outage ended.

---

## 3. Tests

**Integration — `TestAdminReEnablesAutomatedTrading`.** Kill with a run live, confirm `StartRun`
is refused with `ErrBotDisabled`, confirm a one-word reason is refused, re-enable, and confirm a
run starts immediately — not when the settings cache next expires. Then: exactly one `bot_enable`
audit row with the reason and the platform target, exactly one `bot_kill` row beside it rather
than the two being conflated, and the dashboard reporting the flag correctly in both directions.

**Browser — `admin.spec.ts`, "turns automated trading back on, and runs can start again".** The
case the prompt asked for, in that order: the tile says the feature is off, a run really is
refused (403 straight at the API, so the page is not merely drawn that way), the control offers
the way back rather than the kill switch, the consequence copy is on screen, confirm is gated on a
reason, the toast lands, the tile and the control flip back, **a new run starts**, and both halves
of the incident are in the audit log under their own names.

The three admin bot cases now form one story, which is why they are `describe.serial`: the per-row
stop needs the feature on, the kill switch turns it off, and the re-enable needs it off to have
anything to turn on. Run as a block the suite now ends with the switch as it found it, which it
did not before — the previous residue is gone.

---

## 4. Commands run

| Command | Result |
|---|---|
| `npx playwright test --project=desktop --project=mobile` | **28 passed, 3 skipped**, exit 0, in the CI configuration; `bot_enabled` is `true` afterwards |
| `npx playwright test --project=digit-mobile --project=digit-desktop` | **20 passed, 2 skipped**, exit 0 |
| `go test -tags integration ./internal/service/ -count=1` | **ok, 32.3s** |
| `go test ./...` | ok |
| `go vet ./...`, `go vet -tags integration ./...`, `gofmt -l` | clean |
| `npx tsc --noEmit` | clean |
| `npm run lint` | exit 0, warnings only and all pre-existing |
| `npx vitest run` | **13 files, 130 tests passed** (was 13 / 128) |

Verified in the browser as well as in the suite: with the switch off, the tile carries the amber
border and "automated trading is off" and the header offers the green control; the dialog states
that nothing resumes; confirming gives "Automated trading is on · Users can start auto runs
again", and the dashboard returns to its on state with a green "Turned auto runs back on" row in
recent actions.

---

## 5. Closed

The deferred item from Checkpoint 4 — *the console can turn automated trading off and cannot turn
it back on* — is moved to Closed against **P3.5**, with what closed it.

Phase 4's list is unchanged otherwise, and still starts with the stop-vs-open race.
