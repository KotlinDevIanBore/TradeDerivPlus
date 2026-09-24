# Phase 2 · Checkpoint 2 — BotTicket

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Plan:** `docs/investigation-reports/auto-bot-plan.md` §5.2
**Scope:** The idle/config ticket, its validation, and its economics. No `BotRunCard`.

---

## 0. The preview-server problem, fixed properly

Asked for before Checkpoint 4; done now, because this checkpoint's visual work depends on it.

**Two changes, in `web/playwright.config.ts`:** the suite now runs `npm run dev` **on its own port**
(5174) rather than `reuseExistingServer` adopting whatever sits on 5173.

- **A dev server, because it serves the working tree.** `vite preview` serves a built `dist/`, which
  is how a whole phase of browser runs verified a stale bundle.
- **Its own port, because `reuseExistingServer` cannot tell what it is reusing.** On 5173 it adopts a
  developer's server, whatever that is; on 5174 it adopts only a previous test server, which serves
  the same source and is safe to keep. Reuse stays — runs are still fast — without the trust problem.

That required one backend change: `CORS_ORIGIN` is now comma-separated (`getList`), defaulting to
`http://localhost:5173,http://localhost:5174`. The `CORSOrigins` field was already a `[]string`; only
the env parsing was single-value. `.env` and `.env.example` updated to match, since `.env` pinned
5173 and would otherwise override the new default.

Verified: `OPTIONS` from 5173 → 204, from 5174 → 204, from an unknown origin → 403. The digit suite
passes on the new port.

**Verification is now standing rather than remembered:** browser checks point at a dev server that
cannot be stale, and no `npm run build` step is involved. CI was never affected — nothing listens
there, so Playwright always started its own.

---

## 1. What was built

`web/src/features/trading/BotTicket.tsx`, plus `DigitPicker.tsx` extracted from `DigitTicket` so the
manual and automated tickets share one 0–9 row rather than two that can drift.

**It takes its quote as a prop rather than fetching one.** That is what makes it testable without a
query client, which is the convention `OrderTicket` set and the reason the manual `DigitTicket` has
no component test. The ticket owns its selection and stake and reports them up through
`onConfigChange`; `DigitTradeScreen` fetches the quote and passes it back down.

Per §5.2's field list: a **Back** choice (Even/Odd, Matches/Differs, Over/Under — a run picks one
side once, where a person picks per contract), the digit or barrier picker, stake, interval chips
(1s · 2s · 5s, default 2s), and four stop conditions under "Stop the run when" — *I have lost*
(required), *I am up* (optional), *Losses in a row*, *Contracts placed*. Duration is a fixed 30
minutes with the copy saying so.

### The economics, stated rather than implied

The whole point of §10's resolved default. The panel shows what a win pays and how likely it is,
then the line that matters:

```
Pays $90.00 on a win · 10% chance
Expected return -10.00% per contract
This contract costs the most per play.
```

Both figures come from the API's `win_probability` and `expected_return`, so they cannot drift from
the table that prices the contract. The warning threshold is −5%, which falls in the gap between
Matches (−10%) and the next worst (−4.5%) rather than through a cluster.

The worst case is **stop loss plus one stake**, not the stop loss: the contract in flight when the
floor trips still has to settle. A test pins that arithmetic, and the copy explains it inline.

The CTA is "Start auto run" over "Places your settings for you until a stop condition is met." The
words *AI*, *smart*, *strategy* and *profit* appear nowhere, and a test asserts the absence.

### Real-money gating, built but not opened

A real account with `bot_real_money_enabled` false gets a disabled CTA and "Automated runs are
available on demo accounts for now." The flag stays **false**; the gating is the deliverable. A test
covers all three states — real+off refused, real+on allowed, demo unaffected — so flipping the flag
later is a configuration change with its behaviour already proven.

---

## 2. Deviations

**One backend change during a UI checkpoint: `/auth/me` now carries `digit_max_stake`.**

§5.2 asks for the stake input to show "the server's maximum". The client had no way to learn it. The
options were to hardcode 100 (a second copy of a limit, free to drift) or to send it. Phase 0's
report had already flagged this exact gap and deferred it — *"the limit moves onto `/auth/me` in
Phase 1, at which point the ticket can refuse before the round trip"* — and Phase 1 did not pick it
up. So it lands here: one field on `meResponse`, sent as a string like every other amount.

The ticket now renders "Up to $100.00 a contract" as a hint and refuses 101 before a request goes
out, rather than relying on a 422. Caught by the browser check, which failed on exactly this before
the field existed.

Everything else is within §5.2. No `BotRunCard`, no WS event, no chips, no toasts — Checkpoint 3.

---

## 3. Mistakes made and corrected

**I reformatted five files with the wrong style.** I ran `npx prettier --write` without checking for
a config; there is none, and prettier's defaults (double quotes, semicolons) are the opposite of this
codebase's. It rewrote `DigitTradeScreen.tsx` into a 209-line diff of pure churn.

Restored by finding the options that match untouched files — `--single-quote --no-semi
--print-width 100`, verified by checking `ContractTypeTabs.tsx` and `queryKeys.ts` come back clean —
and re-running. The diff dropped to 115/17, and every remaining deletion is a line I actually
changed. **Worth considering a `.prettierrc`** so the house style is expressed rather than
remembered; not added here because it changes the project's tooling posture, which is not mine to
decide mid-phase.

**Four of my own test bugs**, all in the first run of `BotTicket.test.tsx`:

1. `ApiError`'s constructor is `(status, body)`; I called it `(message, {…, status})`.
2. `getByRole(...)` is not chainable — `within()` is how a query gets scoped.
3. A `loading` Button adds a visually-hidden "Working", so its accessible name is no longer exactly
   its label and needs a regex.
4. The browser check asserted "the manual Even button is gone", but **Even still exists** — as the
   side to back. Rewritten to assert the manual ticket's own stake field is absent, which is what
   "the manual ticket is gone" actually means.

---

## 4. Verification

```bash
cd web
npm run typecheck                     # clean
npm run test                          # 106 tests, 10 files (was 82 / 9)
npm run lint                          # no new warnings from these files
npx playwright test --project=digit-desktop --project=digit-mobile
# exit 0 — 10 passed, 2 skipped

cd .. && gofmt -l ./cmd ./internal ./pkg && go build ./... && go vet ./... && go test ./...
python3 scripts/smoke.py              # ALL CHECKS PASSED
python3 scripts/bot_check.py          # AUTOMATED RUN VERIFIED
```

`/auth/me` now returns:

```
{'digit_contracts_enabled': True, 'bot_enabled': True,
 'bot_real_money_enabled': False, 'digit_max_stake': '100.00'}
```

### Component tests — 24, in four groups

*The economics it states* (7): payout and probability rendered; the expected return stated in full and
negative; the Matches warning present at −10% and absent at −2.4%; the worst case as stop loss plus
one stake; a placeholder rather than stale numbers while the quote loads; and no "AI" or "profit"
anywhere.

*Validation* (7): the per-contract ceiling and the tenth-of-balance rule each refuse and block submit;
a missing stop loss; a stop loss over the balance; an empty take profit accepted as "not set" while a
typed zero is a mistake; and server errors surfaced both against a named field and above the button.

*What it submits* (3): selection, stake, interval and limits — including `max_duration_s: 1800`; the
digit carried for Matches/Differs; and the configuration reported so the quote can follow it.

*When it cannot start* (7): missing price, reconnecting feed, quote not yet loaded, a start in
flight, and the three real-money states.

### In the browser, at both widths

19 checks passed against the dev server on 5174, including the three that had failed before
`digit_max_stake` existed. The Matches screenshot shows the amber warning, the `$60.00` worst case,
and the `Up to $100.00 a contract` hint.

---

## 5. Not done in this checkpoint, by instruction

Starting a run from the ticket calls `useStartBotRun`, which works — but nothing yet renders the run
it creates, so the surface would sit on the ticket with a live run behind it. `BotRunCard`, the
`bot_run` socket event, the toasts, the SessionStrip cell and the BOT chips are all Checkpoint 3,
which is what makes a started run visible.
