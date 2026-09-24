# Brief for the planning session (Fable): AUTO mode implementation plan

**Hand this to Fable.** It is the instruction set for producing the AUTO-mode implementation plan.
It is not the plan.

**Read first:** `docs/investigation-reports/auto-bot-investigation.md` (23 Sep 2026). That report is
the factual basis for this work and its citations are verified. **Do not re-investigate what it
already answers** — pipeline, payouts, feed, state, UI tree, rate limits, existing hooks. If you
believe a finding is wrong, say so explicitly rather than quietly working around it.

**Also useful, in this order:**
- `docs/investigation-reports/nav-layout-investigation.md` §125, §166, §221, §279 — the benchmark's
  AUTO/MANUAL and "TRADING MODE" UI, which is the reference design.
- `docs/investigation-reports/digit-contracts-investigation.md` §177 — already argues trading mode
  should be **global, not per-user**, with a user-ID allowlist for staged rollout.
- `docs/implementation-reports/digit-contracts-phase-0-1.md` — the phasing style this repo uses.

---

## 1. What you are planning

An automated ("AUTO") trading mode for digit contracts: a bot that places contracts on a
user-configured strategy and stop conditions, instead of a human pressing a button each time.

This is a **greenfield feature**. The investigation confirmed there is no existing scaffolding of
any kind — no toggle, no stub, no flag, no dead code, no table, no scheduling dependency.

---

## 2. Constraints that bind the plan — do not re-litigate these

These are established facts from the investigation. Build the plan on top of them.

**Economics**
- Every contract type has **negative expected value**, from **−0.50%** (Over 0 / Under 9) to
  **−10.00%** (Matches) of turnover per contract. The edge is not uniform; the contract chosen
  matters twentyfold.
- The payout table is **explicitly marked not final for real money**
  (`internal/models/digit_payout.go:9-14`).
- The simulated feed's digits are **uniform and independent** (verified, χ² across four symbols).
  **No strategy reading digit history can produce an edge.** Any plan implying otherwise is wrong.

**Safety gaps that already exist**
- `POST /digit-contracts` sits on the generic **20 req/s, burst 60** bucket, not the tight bucket
  that guards deposits and withdrawals.
- **No maximum stake**, no concurrent-contract cap, no placement cooldown, no idempotency key, and
  no large-stake confirmation on the digit surface.
- The `digit_contracts_enabled` flag gates **only the UI**; the API accepts contracts regardless.

**What is already sound — do not rebuild it**
- Server-side balance checking: wallet row and account row locked inside one transaction, ledger
  and balance committed together, SQL-level non-negative guard. A bot **cannot** overdraw.
- Settlement: next tick after entry, three redundant triggers, double-settle prevented by a row
  lock. Void after a 5s grace refunds the stake in full.

**Mechanical facts the design must respect**
- Contracts settle in roughly **500 ms** on the mock feed. There is **no duration parameter**.
- **Void is a third outcome**, not a loss. Streak logic that treats it as a loss will misfire.
- `FeedService.OnTick` observers run **synchronously on the single feed goroutine** and there is
  **no unregister**.
- The current "session" is a module constant that resets on reload and **under-counts past 50
  settled contracts**.
- CI runs **no browser test against the digit surface at all**
  (`DIGIT_CONTRACTS_ENABLED: "false"` in `.github/workflows/ci.yml:162-165`).

---

## 3. Decisions the plan must make

Make these, with reasoning. Do not leave them open.

1. **Where the loop runs.** The investigation recommends server-side, modelled on
   `SignalService.Run` (`internal/service/signal_service.go:46-69`), started at
   `cmd/api/main.go:103`. A client-side loop dies on tab blur, reload and token expiry. If you
   disagree, argue it.
2. **The v1 strategy model.** What can a user actually configure? Given §2, strategies cannot
   create an edge, so decide what they are *for* and keep v1 deliberately small. Say explicitly what
   is not in v1.
3. **Risk controls and their defaults.** Which controls, what default values, and where each is
   enforced. At minimum: stake ceiling, exposure cap, placement interval, session stop-loss and
   take-profit, max consecutive losses, max contracts per run. **These are server-side.** Client-side
   guards are bypassed by the bot itself.
4. **The run/session model.** Schema for a durable bot run, plus a provenance column on
   `digit_contracts` (today a bot's contract is indistinguishable from a human's). Include the kill
   switch and what happens to a run on process restart.
5. **Which contract types a bot may use**, and whether the UI surfaces the house edge.
6. **UI shape.** A `Panel` (four registries: `usePanel.ts:12`, `panelItems.ts:12-17`,
   `panelItems.ts:24-27`, `AppShell.tsx:33-38`) or an `Overlay` (`overlays.ts:11`). Note the Lucide
   `Bot` icon is already taken by Signals.
7. **Feature flag and rollout.** `app_settings` is a generic key/value store needing no migration.
   `digit-contracts-investigation.md:177` argues global with an allowlist.
8. **Observability.** What a bot running away looks like on the existing Prometheus surface.

---

## 4. Questions to escalate, not answer

Put these in an "Open questions for the client" section. They are product calls:

- Should a bot ship at all against a payout table marked "NOT final for real-money use"?
- Given every contract has negative EV, what is the bot's **honest value proposition**, and what
  does the UI say so it does not imply profit? This is a copy and compliance question as much as a
  design one.
- **Demo-only for v1, or real money?** Demo-first de-risks nearly everything in §2.
- Does a bot need any regulatory treatment the manual surface does not?

---

## 5. Non-negotiables for the plan

State these as constraints the implementation session must follow:

1. **Risk controls land before the loop exists**, not after. A loop shipped without server-side
   limits can place 20 contracts a second.
2. **The bot must not block the feed goroutine.** If it hooks `OnTick`, it hands off immediately.
3. **Start/stop is an internal flag**, because `OnTick` cannot be unregistered.
4. **Every bot-placed contract is attributable** in the database.
5. **Digit e2e coverage is a prerequisite, not a follow-up.** CI currently exercises none of the
   digit surface, so there is no regression net under the code the bot drives.
6. **Reuse `DigitService.Open`.** Do not create a second path into the money code. The locking,
   ledger and freeze checks there are correct and tested.

---

## 6. Shape of the plan you produce

Write to `docs/investigation-reports/auto-bot-plan.md`, following the house convention set by
`one-screen-migration-plan.md` and `digit-contracts-investigation.md`:

1. **Summary** — what is being built, and the decisions taken, in a page.
2. **Target architecture** — services, schema, API surface, where the loop lives, how it is
   supervised.
3. **Risk controls** — the full set, defaults, and enforcement point for each.
4. **Run/session model** — schema, lifecycle, restart behaviour, kill switch.
5. **UI** — screen-by-screen in the format of `ui-ux-plan.md`: purpose, components, data, states,
   visual notes. Include the AUTO/MANUAL toggle placement against the benchmark.
6. **Phased plan** — numbered phases, each independently shippable, each with an explicit exit
   criterion. Follow the digit-contracts phasing style.
7. **Breakage list** — what existing behaviour changes, and what must not.
8. **Test impact** — unit, integration (the concurrency suite already exists), and the digit e2e
   gap.
9. **Effort estimate** — per phase.
10. **Open questions for the client** — §4 above.
11. **Decisions taken in this plan** — so the implementation session does not reopen them.

**Out of scope for v1 unless you argue otherwise:** backtesting, strategy marketplace or sharing,
multi-symbol concurrent runs, martingale or any progressive staking, copy trading.

---

## 7. How to work

- Read-only investigation is already done. **Verify anything you intend to rely on**, but do not
  redo the survey.
- Cite file paths and line numbers as the investigation does. The implementation session should not
  need to rediscover anything.
- Where you make a judgement call, say so and give the reasoning, so it can be overturned
  deliberately rather than by accident.
- If the economics question in §4 makes you think the feature should not ship in its obvious form,
  **say that plainly in the Summary.** That is a legitimate outcome of planning.
