# Mobile trading screen — layout fixes: checkpoint report

**Scope:** every step of the brief against [`mobile-trading-screen-layout.md`](mobile-trading-screen-layout.md), in that report's own §5 order.
**Date:** 26 September 2026 · **Branch:** `master` @ `a672bd7` · **Working tree, not yet committed.**
**Classification:** UI and layout. Not a money path — no mutation matrix; build, unit tests, the new viewport assertions, and the full e2e suite on every project including the two new 375px ones.

---

## 1. Summary

Six steps, in the order the investigation asked for, each landing before the next changed what it measured.

| Step | Finding | What it is now |
|---|---|---|
| 1 | Only 412px was ever tested | Two 375×667 Playwright projects and a four-assertion layout spec that **failed 5 of 8 before any fix** (§3) |
| 2 | `cn()` dropped every custom text size → 16px | `extendTailwindMerge` with all seven tokens, pinned by 11 unit tests; **26 sites change size** (§4) |
| 3 | Digit picker wrapped to two rows | Ten-column grid; one row at every width |
| 4 | Toasts anchored 16px inside the ticket | Under the header at every width; repeated signals collapse to one row; "opened" lasts 2.5s |
| 5 | Ticket sticky over a fixed 240px chart → 0px chart at 375×667 | Ticket in flow; **only the buttons are pinned**, portaled into a bar the screen owns (§6) |
| 6 | No safe-area handling anywhere | `viewport-fit=cover`, a `--tabbar-h` that includes the inset, every bottom offset expressed against it |

**The headline number:** with Over/Under selected at 375×667, the chart went from **0px visible to 240px** — all of it — and the two outcome buttons stay on screen without scrolling.

**Two things the tests found that the investigation had wrong.** The digit picker wraps at **412px too**, not only at 375 — the report's claim that it fit on a Pixel 7 was inferred from one measurement at 375 and was false (ten 36px buttons plus gaps need 396px; the 412 box is 380). And the toast overlap could break the *buttons*, not only the stake input: the pre-fix run at 412×915 failed "buttons reachable" because three signal toasts were sitting on them. Both are now covered by assertions rather than by a paragraph.

---

## 2. What changed, by file

| File | Step | Change |
|---|---|---|
| [`web/playwright.config.ts`](../../web/playwright.config.ts) | 1 | `mobile-narrow` and `digit-narrow`: iPhone 13 Mini descriptor, 375×667, Chromium |
| [`.github/workflows/ci.yml:204,291`](../../.github/workflows/ci.yml) | 1 | Both e2e jobs run the new project alongside the old |
| [`web/e2e/digit.layout.spec.ts`](../../web/e2e/digit.layout.spec.ts) | 1 | **New.** Tab row fits; digits on one row; ≥160px of chart with Over/Under; both buttons reachable at scroll 0 |
| [`web/src/lib/cn.ts`](../../web/src/lib/cn.ts) | 2 | `extendTailwindMerge`, `font-size` group = `nano meta body card section title hero` |
| [`web/src/lib/cn.test.ts`](../../web/src/lib/cn.test.ts) | 2 | **New**, 11 tests, one of which reads the Tailwind config and fails if the list drifts |
| [`web/src/components/ui/Input.tsx:45`](../../web/src/components/ui/Input.tsx) | 2 | `text-body` → `text-card`: **inputs stay 16px on purpose** (§4.1) |
| [`web/src/features/trading/DigitPicker.tsx`](../../web/src/features/trading/DigitPicker.tsx) | 3 | `flex flex-wrap` + fixed `w-9` → `grid grid-cols-10`, height kept at 36px |
| [`web/src/components/ui/Toast.tsx`](../../web/src/components/ui/Toast.tsx) | 4 | Toaster `top-[topbar+8px]` (mobile), `sm:top-[topbar+16px]` (desktop); `collapse` option; `duration` on the helpers |
| [`web/src/features/shell/AppShell.tsx`](../../web/src/features/shell/AppShell.tsx) | 4, 6 | Signal toasts collapse under key `signal` → "N new signals"; `main` padding uses `--tabbar-h` |
| [`web/src/features/trading/DigitTradeScreen.tsx`](../../web/src/features/trading/DigitTradeScreen.tsx) | 4, 5 | "opened" toast 2.5s; wrapper no longer sticky; owns the pinned action bar and passes it to the tickets |
| [`web/src/features/trading/TradeScreen.tsx`](../../web/src/features/trading/TradeScreen.tsx) | 5 | Same for the CFD surface — it had the identical sticky pattern |
| [`web/src/features/trading/DigitTicket.tsx`](../../web/src/features/trading/DigitTicket.tsx) | 5 | `actionsTarget` prop; the two outcome buttons portal into it |
| [`web/src/features/trading/BotTicket.tsx`](../../web/src/features/trading/BotTicket.tsx) | 5 | Same for "Start auto run"; the `max-h-[58vh] overflow-y-auto` cap removed (§6.2) |
| [`web/src/features/trading/OrderTicket.tsx`](../../web/src/features/trading/OrderTicket.tsx) | 5 | Same for the Buy/Sell button |
| [`web/index.html`](../../web/index.html) | 6 | `viewport-fit=cover` |
| [`web/src/index.css`](../../web/src/index.css) | 6 | `--tabbar-h: calc(theme('spacing.tabbar') + env(safe-area-inset-bottom, 0px))` |
| [`web/src/features/shell/TabBar.tsx`](../../web/src/features/shell/TabBar.tsx) | 6 | `h-[var(--tabbar-h)] pb-[env(safe-area-inset-bottom)]` |

---

## 3. Step 1 — the failing run, on record

The spec was written and run **before any fix**, against the unfixed source:

| Assertion | digit-mobile (412×915) | digit-narrow (375×667) |
|---|---|---|
| Tab row fits | ✓ | ✘ `scrollWidth 411 > 375` |
| Digits on one row | **✘ wrapped onto 2 rows** | ✘ wrapped onto 2 rows |
| ≥160px of chart | ✓ | ✘ **0px** |
| Buttons reachable | **✘** — three signal toasts on them | ✓ (the sticky ticket put them on screen) |

Five failures across the two projects, three of them at the width CI had never run. After the fixes: **8 of 8 pass** on both.

The chart assertion hit-tests down the chart's centre line with `document.elementFromPoint` and counts pixels where the hit lands *inside* the chart — so it measures what a user sees regardless of what is on top, and it is what caught the toasts.

---

## 4. Step 2 — the `cn()` fix and its visual consequences

Stock `tailwind-merge` filed `text-meta` and the other custom sizes under text *colour*, so in any `cn()` call where a colour came later the size was dropped and the element fell back to `font-size: 100%` = 16px. Parsing every `cn(` call for a custom size and a colour together: **30 sites in 23 files**; in **28** the colour came later and the size was lost; **26** of those are visibly different now (the other two are `text-card`, which is 16px anyway).

Verified live after the fix by computed style: contract tabs **12px** (was 16), trading-mode tabs **12px**, digit buttons **14px**, desktop nav items **14px**, admin table headers **12px**.

### 4.1 The one place the old size was kept on purpose

`components/ui/Input.tsx:45` is on the list — `text-body`, so every text input in the app would have gone from 16px to 14px. **iOS Safari zooms the page when a field under 16px takes focus.** The accidental 16px was doing a job nobody had assigned it. The brief said to report cosmetic changes rather than fix them; a page that zooms on every tap into the stake field is not cosmetic, on exactly the platform this work is for. So the input is set to `text-card` — the 16px token — with a comment saying why. This *preserves* current behaviour rather than choosing a new size, which is the narrowest thing that avoids the regression. The same applies, less urgently, to `admin/components/ReasonDialog.tsx:74` (a textarea, admin-only, desktop): left at 14px and listed below.

### 4.2 Everything that now renders smaller — reported, not changed

| Site | Was → now | Where a user sees it |
|---|---|---|
| `components/AccountPill.tsx:23` | 16 → **10px** | The DEMO / REAL pill label, everywhere. The largest single drop; legible in the screenshots, worth a look on a real phone |
| `components/ui/Tabs.tsx:18` | 16 → 14 | Open / Closed tabs in Positions, admin tabs |
| `components/ui/Card.tsx:21` | 16 → 12 | Card subtitles |
| `features/shell/TopBar.tsx:18` | 16 → 14 | Desktop nav row: Positions, History |
| `features/trading/ContractTypeTabs.tsx:35` | 16 → 12 | The tab row this whole report is about |
| `features/trading/TradingModeTabs.tsx:53` | 16 → 12 | MANUAL / AUTO |
| `features/trading/ChartRangeToggle.tsx:23` | 16 → 12 | 1m / 5m / 15m |
| `features/trading/DigitDistributionRow.tsx:40` | 16 → 14 | The 0–9 distribution row under the chart |
| `features/trading/DigitPicker.tsx:34` | 16 → 14 | Digit / barrier buttons |
| `features/trading/BotTicket.tsx:251,322` | 16 → 12 | Interval chips, side choice |
| `features/trading/BotTicket.tsx:399` | 16 → 14 | Back: Over / Under choice |
| `features/trading/OrderTicket.tsx:319` | 16 → **10px** | A nano label on the CFD ticket |
| `features/trading/OrderTicket.tsx:347` | 16 → 14 | Level fields |
| `features/portfolio/PositionCard.tsx:65` | 16 → 12 | Position card meta |
| `features/signals/SignalsPanel.tsx:55,69` | 16 → 12 | Signals panel filters |
| `features/wallet/HistoryPanel.tsx:167` | 16 → 12 | History filter chips |
| `features/auth/RegisterScreen.tsx:154` | 16 → 12 | Register screen helper text |
| `features/admin/AdminShell.tsx:117,170` | 16 → 14 / 12 | Admin sidebar nav, footer |
| `features/admin/components/DataTable.tsx:72` | 16 → 12 | Every admin table header |
| `features/admin/components/ReasonDialog.tsx:74` | 16 → 14 | Reason textarea (see §4.1) |
| `features/admin/screens/ChangePassword.tsx:90` | 16 → 12 | Password rules copy |
| `features/admin/screens/UserDetail.tsx:353` | 16 → 14 | User detail rows |

In the screenshots (trading at 375 and 1280, admin users table) none of these reads as wrong — the uppercase 12px table headers and the 12px pill tabs are the design the tokens describe. The two 10px results are the ones to eyeball on a device.

---

## 5. Steps 3, 4 and 6

**Digit picker.** A ten-column grid divides whatever width the ticket has: ~31px buttons at 375, ~34px at 412, 36px+ on a desktop card. Height stays 36px for the tap target. One row at every width the suite runs.

**Toasts.** Under the header at every width — the bottom of a phone is where the controls live, and the old anchor was 16px *inside* the ticket. On desktop, below the header rather than `top-6`, which fixes the top-bar coverage the Phase 3 report had noted. Signal toasts share a `collapse` key: a second one replaces the first as "2 new signals" with a fresh timer. The "opened" toast lasts 2.5s; the settlement toast one tick later carries the outcome. Trade-off stated plainly: on a phone the toasts now cover the contract-type tabs and price header briefly instead of the stake input and buy buttons. Tabs are touched once a session; the stake field, every trade.

**Safe areas.** Project-wide by nature, kept small: one CSS variable, three consumers (tab bar, `main` padding, the action bar), one meta attribute. Nothing in this pass can verify it — emulation reports `env(safe-area-inset-bottom)` as 0 — so it is stated from the spec, not measured. **It should be checked once on a notched iPhone before this is called done**, and it is reasonable to treat as its own follow-up if that device is not to hand.

---

## 6. Step 5 — the structural change

### 6.1 Shape (A), as specified

The ticket sits in the page's flow below the session strip. The screen renders one sticky bar at the end of its root — `sticky bottom-[var(--tabbar-h)] z-20 … empty:hidden lg:hidden` — and hands it to the tickets as `actionsTarget`. Each ticket builds its action element once and either portals it into the bar (phone) or renders it inline (desktop, where `useIsDesktop()` passes `null`). `empty:hidden` means a live run's card, which has nothing to pin, leaves no empty strip.

**Why a portal rather than making the buttons sticky inside the ticket.** A sticky element is confined to its parent's box. With the ticket in flow at 375×667, the ticket's top edge lands around y≈550 — below the 535px where a 68px bar would need to sit to clear the tab bar — so buttons sticky *inside* the ticket would sit just below the fold on the smallest phone until the user scrolled. The brief's requirement was reachable without scrolling; the portal puts the bar's containing block at the page, where it always sticks.

### 6.2 What it removed

`BotTicket` carried `max-h-[58vh] overflow-y-auto` — a height cap and an inner scroll region, added because it was a 524px sticky sheet that covered the tabs and the digit row. In flow, an inner scroll inside a scrolling page is a trap on a phone; both are gone, and the comment explains the history.

### 6.3 The numbers

Over/Under, manual, page at scroll 0, measured by hit-testing:

| Viewport | Chart visible before | Chart visible now | Buttons reachable |
|---|---|---|---|
| 375×667 | **0px** | **240px** | yes |
| 412×915 | 237px | 240px | yes (was blocked by toasts) |

AUTO mode at 375×667: was 3px of chart under a 524px sheet; now the full chart with "Start auto run" alone pinned.

Desktop at 1280×800: the ticket computes `position: static` in its grid column with its buttons inline, as before.

---

## 7. Verification

| Check | Result |
|---|---|
| `npm run typecheck` | exit 0 |
| `npm run build` | exit 0 |
| `oxlint` on the touched files | nothing new (three pre-existing warnings in OrderTicket/TradeScreen effects) |
| `npx vitest run` | **145 passed, 15 files** (was 134/14: +11 `cn` tests) |
| `digit.layout.spec.ts` before fixes | 5 failed / 8, as intended |
| `digit.layout.spec.ts` after fixes | **8 / 8** on digit-mobile and digit-narrow |
| Full digit suite, `digit-mobile` + `digit-narrow` + `digit-desktop` | **38 passed, 0 failed**, 7 skipped (the cross-breakpoint skips), 4.3m — `digit-narrow` runs every digit spec at 14 ✓, same as Pixel 7 |
| Full CFD suite, `mobile` + `mobile-narrow` + `desktop` | **47 passed, 0 failed**, 7 skipped, 5.1m — `mobile-narrow` runs every `user.*` spec at 13 ✓, same as Pixel 7; `admin.spec` and `user.deposit` included |
| `admin.spec.ts` at CI's limiter values, after the helper fix | **7/7** |
| Admin 429 branch, bucket drained | **recovers in 1615ms**; the old behaviour fails in 304ms with CI's message (§7.2) |
| Visual pass | Screenshots at 375×667 (top, bottom, AUTO), 1280×800, register, admin dashboard/users/transactions; computed sizes read back (§4) |

Exit codes read directly. Screenshots were taken with headless Chromium driven directly, because the desktop app's browser pane was hidden and does not paint while hidden.

### 7.2 A CI failure this PR did not cause, and the helper fix folded in

The first CI run on this branch failed one test — `admin.spec.ts:386`, in 199ms, at
`loginAdmin` with **"no known admin password works"**. Go, Web and the digit e2e job all
passed, and the CFD suite had been 47/47 locally an hour earlier.

**It was the rate limiter, not the credential.** `POST /admin/login` shares a per-IP bucket
with `/admin/refresh` and `/admin/logout` ([`routes.go:175`](../../internal/routes/routes.go) —
`rate.Every(AUTH_RATE_EVERY*4)`, `AUTH_RATE_BURST/2`; in CI, **burst 12 refilling at
1.25/s**). Each `loginAdmin` spends at least two tokens — an API probe plus the form submit —
the console spends more refreshing, and `admin.spec.ts` signs in eight times. `loginAdmin`
treated *any* non-OK as a wrong password and moved to the next candidate, so a 429 on both
exhausted the list and blamed the credential, pointing the reader at `-reset-admin`, which
would not have helped.

**Shown, not asserted.** A temporary spec drained the bucket (`[401,…,429,429,…]`) and then
called the helper:

| Code | Result |
|---|---|
| Fixed helper | **Recovered after 1615ms** — one backoff cycle — token returned |
| 429 handling mutated out (the old behaviour) | **Failed in 304ms** with the CI message verbatim: *"no known admin password works. Reset the console credential with:"* |

304ms against CI's 199ms, same message. That is the failure reproduced and then fixed. The
mutation was restored and hash-verified byte-identical.

`signInAdmin` now treats a 429 as what it is: it stops probing a bucket that is already
empty, retries with growing backoff (1.5s, 3s, 4.5s), and if it still cannot get in, says
**rate limiting** and names the shared bucket rather than sending anyone to reset a password
that was never wrong. It also caches the password that worked, so a run does not spend a
refused attempt rediscovering it.

**The probe spec was deleted rather than kept.** It deliberately drains a limiter the rest of
the suite shares, and `workers: 1` means whatever runs next inherits an empty bucket. The
evidence is above; the standing cost is not worth it.

Nothing in this PR caused the failure — no change here touches the admin login path — but the
fix belongs with it, because the next occurrence would be read the same wrong way.

### 7.1 Not verified

- **Safe-area insets on a real device** (§5). Emulation cannot produce a non-zero `env()`.
- **A real mobile browser's collapsing URL bar.** Playwright's 667 is a bare viewport; Safari's toolbar takes ~80–100px until it collapses. With the chart now fully in view at 667, there is ~90px of headroom before the floor of 160 is threatened, which the old layout did not have.
- **Prettier.** As before: the repo has no prettier config, so `--check` fails on unrelated files and `--write` would reformat to a foreign style. Not run.

---

## 8. Still open

- A follow-up visual pass on a device for the two 10px results (`AccountPill`, `OrderTicket:319`) and the safe-area behaviour.
- The CFD `TradeScreen` got the structural fix but no layout spec of its own; `user.*` specs on `mobile-narrow` cover it indirectly.
- `admin/components/ReasonDialog.tsx:74` textarea at 14px (§4.1) — harmless on desktop; decide whether admin is ever used from a phone.
