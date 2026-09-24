# Phase 2 · Checkpoint 1 — Types, data hooks, mode toggle

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Plan:** `docs/investigation-reports/auto-bot-plan.md` §5.1–5.2, §6 Phase 2
**Scope:** The plumbing and the switch. No `BotTicket`, no `BotRunCard`.

---

## 1. What was built

### Types (`web/src/types/index.ts`)

`BotRun`, `BotRunStatus`, `BotLimits`, and `BotStopReason` — the last as a union grouped by origin
(asked for / imposed / fault), because the UI treats those three differently: what the user asked for
reads as their own limit working, what the platform imposed needs explaining, and a fault needs
apologising for. Checkpoint 3's toast table is that grouping.

`bot_run_id: string | null` on `DigitContract`, and `bot_enabled` / `bot_real_money_enabled` on
`User`. `bot_enabled` is documented as already narrowed by the allowlist server-side, so a component
can render on it directly rather than combining two flags.

### API client and keys

`web/src/api/bot.ts` — `start`, `stop`, `get`, `list`, matching `digit.ts`'s shape and keeping money
as strings. `queryKeys.botRuns(accountId, status?)` and `queryKeys.botRun(id)`.

### Hooks (`useQueries.ts`)

- `useLiveBotRun(accountId)` — returns `{ run }`, a single value rather than a list, because the
  server's partial unique index means an account has at most one live run. It polls at 2s **only
  while a run is live**, as a safety net under the `bot_run` socket event that Checkpoint 3 adds; the
  poll only has to cover a dropped socket.
- `useBotRuns(accountId, status?)` — history, for the past-runs list.
- `useStartBotRun`, `useStopBotRun` — both invalidate through one `invalidateForRun` helper, which
  also invalidates the wallet and the contract list, since starting a run places a contract almost
  immediately and stopping one may have just settled the last.

### `TradingModeTabs` (§5.2)

A copy of `ContractTypeTabs`' hand-rolled `role="tablist"` pattern with `aria-label="Trading mode"`
and two tabs. Rendered only when `me.bot_enabled`; the surface is otherwise exactly as it was.

MANUAL takes a `manualDisabledReason`, shown both as a `title` and as a line under the tabs, so the
explanation is reachable rather than implied by a greyed-out control.

The label is AUTO, never "AI" — recorded in the component's own comment, since that is where someone
would change it.

### `?mode=` on the URL (§5.1)

Read as `mode=auto` → auto, anything else → manual, and written with `mergeParams` so switching mode
does not drop the symbol. Switching back to manual **removes** the parameter rather than writing
`mode=manual`, so the default state has one representation instead of two.

A live run forces `auto` and disables MANUAL with "Stop the run to trade manually." Leaving MANUAL
selectable while a run is placing would suggest it had stopped, and stopping is a deliberate act.

---

## 2. Deviations and corrections

**`DigitTicket` gained a `flushTop` prop.** On a phone the ticket is a sticky sheet with a rounded top
edge; putting the toggle above it inside the same sheet stacked two rounded edges. Rather than
duplicating the ticket's styling into the wrapper, the ticket drops its own top rounding when
something sits directly above it. One boolean, one call site, and the mobile screenshot below shows
a single edge.

**A fixture needed the new field.** `sessionStats.test.ts` builds a `DigitContract` literal, so
`bot_run_id: null` was added with a note saying these fixtures are human contracts and session stats
count a contract the same whoever placed it.

Nothing else deviates. No `BotTicket`, no `BotRunCard`, no WS event, no chips — all Checkpoints 2–3.

---

## 3. A problem worth recording: the browser was serving a stale build

The first verification run reported the toggle missing. It was not: **the process on :5173 is
`vite preview`, which serves a built `dist/`, not `vite dev` watching source.** The served
`DigitTradeScreen.tsx` had zero occurrences of `TradingModeTabs` while the file on disk had two.

This matters beyond this checkpoint, because `playwright.config.ts` sets
`webServer.reuseExistingServer: true` — so any Playwright run on this machine reuses that preview
server rather than starting `npm run dev`. **Browser tests here verify the last build, not the
working tree.** Phase 0's digit specs were valid because `npm run build` ran during that phase's
verification, but that was luck rather than design.

Handled for now by running `npm run build` before browser verification, which leaves the process
arrangement alone and makes what is served match the source. Worth deciding properly: either the
preview server should be a dev server, or the build should be part of the verification routine. CI is
unaffected — nothing is listening there, so Playwright starts its own `npm run dev`.

---

## 4. Verification

```bash
cd web
npm run typecheck        # clean
npm run test             # 82 tests, 9 files
npm run build            # so the preview server serves current code
npx playwright test --project=digit-mobile --project=digit-desktop
# 10 passed, 2 skipped by viewport — the existing digit suite is unaffected
```

A scratch Playwright script drove the toggle end to end at both widths (removed afterwards; its
assertions become part of `digit.auto.spec.ts` in Checkpoint 4):

```
[PASS] the trading-mode tablist renders
[PASS] it offers MANUAL
[PASS] it offers AUTO
[PASS] the desktop label is present
[PASS] MANUAL is selected with no mode in the URL
[PASS] and MANUAL is not disabled while no run is live
[PASS] clicking AUTO puts mode=auto on the URL
[PASS] and leaves the symbol alone
[PASS] AUTO is now the selected tab
[PASS] a reload keeps AUTO selected
[PASS] switching back removes the parameter
[PASS] and still leaves the symbol alone
[PASS] the toggle renders on a phone too
```

The two that matter most are the pair about the symbol: they are what proves `mergeParams` is doing
its job, and a mode switch that silently reset the instrument is exactly the bug the helper exists to
prevent.

Rendering was checked by eye at both widths. Desktop: a `TRADING MODE` label above the ticket in the
right column, MANUAL in the accent treatment. Mobile: the tabs at the top of the sticky sheet, one
rounded edge.

---

## 5. Not done in this checkpoint, by instruction

The toggle switches a value that nothing yet reads — `mode` drives only the tabs' own selected state,
and `DigitTicket` renders in both modes. That is deliberate: the plan asks for the switch to be
verified before the forms it switches between exist. Checkpoint 2 gives `auto` a `BotTicket` to show.
