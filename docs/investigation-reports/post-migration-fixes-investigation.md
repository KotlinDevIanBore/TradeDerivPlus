# Post-Migration Findings — Production Review

**Scope:** Investigation only. Two items noticed while reviewing the deployed Phase 4 build. No code changes in this pass.

**Context:** Phase 4 report (`docs/implementation-reports/one-screen-migration-phase-4.md`), section 5 finding #2 already flagged the mobile panel-switch problem. Item 1 was new from DevTools on the deployed site.

**How measured:** Reproduced against a production build (`vite build` + `vite preview` on `:5173`) with Playwright request listeners, idle windows of 45–90s, and a six-action panel-switching sequence. Mobile DOM inspected while History was open.

---

## 1. Summary

**Item 1 — the repeated `/trades` and `/summary` calls are expected polling, not a remount bug and not a duplicate-subscription bug.** `useTrades(..., 'open')` and `useTradeSummary` both set `refetchInterval: 15_000`. Those hooks live on `TradeScreen`, which is always mounted as the surface, so the pair fires every 15 seconds whether any panel is open or not. Opening and closing panels does **not** produce one request per action: six open/close actions over ~30s produced a single poll pair, timed on the same 15s cadence. The “about 8 calls across 6 actions” observed in production matches a short session that happened to overlap a few poll ticks, not a fetch-per-toggle.

**Item 2 — the mobile bottom bar is genuinely unreachable while a panel is open**, for two stacked reasons: Radix’s modal Dialog marks the app (including the bar) `aria-hidden` and `pointer-events: none`, and the panel sheet is `inset-0` with a full-viewport overlay above the bar’s z-index. Closing then reopening is the only path today. **Recommend option 5 below: keep the panel modal, but stop covering the bottom bar** — leave a `tabbar`-tall gap at the bottom of the panel sheet and overlay so the existing `TabBar` stays visible and, with `modal={false}` on the *panel* sheet only (or an equivalent carve-out), stays interactive. That preserves nested overlays (TransactionDetail, close confirmation) better than a fully non-modal panel, and avoids inventing a second nav inside the header.

---

## 2. Item 1 findings

### What the code does

| Hook | Used by | Network | Interval |
|---|---|---|---|
| `useTrades(accountId, 'open', 50)` | `TradeScreen` (always) and `PositionsPanel` (when that panel is mounted) | `GET /trades?...&status=open&limit=50` | **15s** when `status === 'open'` |
| `useTradeSummary(accountId)` | Same two | `GET /summary?account_id=...` | **15s always** |
| `useTrades(accountId, 'closed', 50)` | Same two | closed list | **no interval** |

Keys are shared (`queryKeys.trades(accountId, status)` and `queryKeys.tradeSummary(accountId)`). Limit is not part of the key; both call sites use `limit=50`, so they share one cache entry. React Query dedupes observers: two mounted hooks on the same key do not double the network traffic.

`TradeScreen` is the permanent surface after Phase 4. Its `SessionStrip` and open-position list need open trades and the summary even when Positions is closed. So the observers — and therefore the intervals — never tear down when the user closes a panel.

Global defaults in `App.tsx` set `staleTime: 30_000` and `refetchOnWindowFocus: false`. The 15s intervals on these two hooks override that for freshness on open positions. The account WebSocket also invalidates trades/summary on `trade_opened` / `trade_closed`; the interval is the documented “poll slowly as a fallback” in `useQueries.ts`.

### Reproduction results (production build, desktop 1280×900)

Timestamps are seconds from the start of each window. Gaps are between consecutive hits of the same kind.

**A. Idle, `?panel=none`, no interaction — 90s**

| Kind | Count | Gaps |
|---|---|---|
| `trades(open)` | 6 | **15.0s, 15.1s, 15.0s, 15.0s, 15.0s** |
| `summary` | 6 | same |

**B. Idle, Positions panel open, no interaction — 60s**

| Kind | Count | Gaps |
|---|---|---|
| `trades(open)` | 4 | **15.0s, 15.0s, 15.0s** |
| `summary` | 4 | same |

Same cadence as A. Mounting Positions does not add a second stream.

**C. Six panel open/close actions (Positions → close → History → close → Signals → close) — ~30s window**

| Kind | Count |
|---|---|
| `trades(open)` | **1** |
| `summary` | **1** |

One pair, on the 15s tick — not six. Panel remount/refetch-on-mount is **not** what DevTools was showing during that session.

**D. Idle again after the actions — 45s**

Again: one pair every 15s. Nothing “left running” beyond the same surface-owned interval.

### Which of the prompt’s two diagnoses?

**Neither “expected refetch-on-mount” nor “duplicate subscription / runaway re-subscribe”.**

It is **expected interval polling owned by the always-mounted surface**, with shared cache keys. Panel open/close is a red herring for the request volume: a screenshot session of ~90s with several toggles will show on the order of `2 × (90/15) ≈ 12` requests from idle alone, which lines up with “about 8 across 6 actions” when the session is shorter.

### Is it a bug?

**No functional bug.** It is intentional fallback polling next to the account socket. Whether 15s is the right rate, and whether summary should poll when there are zero open positions, are product/cost judgements — not defects introduced by the migration.

TanStack Query v5 pauses `refetchInterval` while the document is not focused unless `refetchIntervalInBackground` is set (it is not). A Playwright attempt to background the tab kept `visibilityState=visible`, so that pause was not measured here; the library behaviour is still the documented default.

### What would look different if it *were* remount-driven

You would see a trades+summary pair clustered on each panel open (especially Positions), not a metronomic 15s grid that continues with no panel and no clicks. That is not what was measured.

---

## 3. Item 2 options

### What’s actually broken (confirmed)

With History open on a Pixel-sized viewport:

- The bottom bar is still in the DOM and still has height, but its ancestor is `aria-hidden="true"` and computed `pointer-events: none`.
- Playwright’s accessibility tree reports **0** matches for `navigation "Bottom"` and **0** for the Account button.
- `elementFromPoint` over the first icon hits a covering `div` (the overlay / sheet stack), not the button. Overlay is `z-40`, bar is `z-30`, panel content is `z-50`, sheet is `inset-0`.

So this is not only an accessibility quirk: **a thumb tap cannot reach the bar either.** Close (×) → surface → tap next panel is the only path. Phase 4’s finding #2 was right; production review confirmed it feels like the bar “only works from home.”

Honest UX assessment: on a phone, closing a full-screen sheet then immediately opening another is two deliberate taps and a flash of the chart between them. It is not catastrophic for rare switches, but it **does feel broken** for the mental model Phase 4 sold — “four toggles, like the desktop top bar.” Desktop toggles switch in one press; mobile toggles only work when nothing is open. That asymmetry is the real complaint.

### Option 1 — Leave as is

| | |
|---|---|
| Complexity | None |
| Risk to Phases 2–3 | None |
| Effort | Documentation only |

**Why it was left:** modal sheets give focus trap, scroll lock, Escape-dismisses-topmost, and nested `TransactionDetail` / close-confirmation over a panel — all verified in Phases 2–3.

**Honest take:** acceptable as a temporary known limitation; **not** good enough as the long-term mobile model for a four-toggle bar whose whole job is switching panels. Recommend against leaving it unless shipping pressure says otherwise.

### Option 2 — Make the mobile panel non-modal (`modal={false}`)

| | |
|---|---|
| Complexity | Medium |
| Risk to Phases 2–3 | **High** if done alone |
| Effort | Real piece of work, plus re-verification of nested overlays |

`modal={false}` stops Radix applying `aria-hidden` / outside pointer-events / focus trap / body scroll lock. Nested sheets (`TransactionDetail`, close position) are separate `Dialog.Root`s and can still be modal on top — nesting itself need not die.

**But `modal={false}` alone does not restore the bar.** The panel is still `inset-0` and the overlay still covers the viewport above the bar. You would also need to stop covering the bar (gap + overlay insets) and explicitly decide focus/Escape behaviour for the chart and ticket behind the panel. Losing inertness is not “circular”: it mattered because the panel was a full-screen takeover; if the bar becomes a peer control, the chart behind the sheet should stay inert while the bar does not — which is a different shape than plain non-modal.

Phase 4 was right that this undoes the simple “everything behind is inert” story. Treat as incomplete unless paired with a layout carve-out (see option 5).

### Option 3 — Mini nav inside each panel header

| | |
|---|---|
| Complexity | Low–medium |
| Risk to Phases 2–3 | **Low** — modal/focus/nesting unchanged |
| Effort | Small follow-up |

Add icon buttons for the four panels in `Panel`’s header (mobile only), reading `PANEL_ITEMS` / `PANELS` and calling `usePanel().toggle` / `open`. Fits the existing registry cleanly; needs a small API surface on `Panel` (e.g. optional `headerExtra`, or panel-aware header built inside `Panel` when `side="panel"`).

**Downsides:** duplicates the bottom bar’s job; two chrome systems for the same four destinations; header row gets crowded next to the title and ×; `aria-pressed` on the *bottom* bar remains invisible while a panel is open (bar still inert). Works, but teaches “use the header to switch, use the bar to open” — awkward.

### Option 4 — Other ideas

**Swipe between panels.** Medium–high complexity; fights scroll inside History/Signals; easy to conflict with nested sheets. Not worth it as the primary fix.

**Portal the `TabBar` as a sibling of `Dialog.Content` inside the same portal.** Radix still treats outside content as inert in modal mode; a sibling in the portal is not automatically “inside” the modal content for a11y. Fragile; not recommended.

**Render a second copy of the bar inside `Dialog.Content`.** Same idea as option 3 but at the foot. Keeps modal behaviour. Duplicates the AppShell bar visually unless the AppShell bar is hidden while a panel is open (`lg` already hides it; on mobile you’d hide the outer bar and show an in-sheet bar). Cleaner than header icons for thumb reach; still two implementations unless the bar component is reused as a footer slot of `Sheet`.

**Persistent bar outside aria-hidden by keeping modal Dialog but carving the bar into content.** Effectively option 5.

### Option 5 — Keep modal semantics for the panel body; leave the bottom bar uncovered and interactive (recommended)

| | |
|---|---|
| Complexity | Medium |
| Risk to Phases 2–3 | Medium — nested overlays must be re-tested, not redesigned |
| Effort | Focused follow-up (one interaction change, not a nav rethink) |

Concrete shape:

1. On mobile, panel `Sheet` / overlay use `bottom: theme(spacing.tabbar)` instead of `inset-0` / full-bleed overlay — the existing bar stays visible in the gap (same token the ticket and toasts already respect).
2. Make the **panel** dialog non-modal *or* otherwise allow pointer/a11y access to the bar only (full `modal={false}` plus preventing interact-outside from closing when the target is the bar is the straightforward path).
3. Keep Deposit/Withdraw/detail sheets as modal as they are today.
4. Re-run the Phase 2–3 nested cases: TransactionDetail over History, close confirmation over Positions, Escape order, focus return.

Result: one press switches panels; `aria-pressed` on the bar becomes meaningful; the chart remains covered by the sheet; no second nav inventing itself in the header.

**Risks to verify, not assume:** Escape and outside-click with `modal={false}`; whether focus can leak into the ticket; whether a nested modal still stacks correctly when the parent panel is non-modal (expected yes — separate roots).

---

## 4. Recommendation

### Item 1 — do this

**No fix required for correctness.** Treat the DevTools noise as the 15s open-trades + summary poll owned by `TradeScreen`.

Optional follow-ups (separate, product-driven — not “bugs”):

- Raise the interval (e.g. 30–60s) now that the account socket is the primary path.
- Disable `useTradeSummary`’s interval when `open_count === 0` / no open trades, if summary is cheap enough to refresh only on invalidation.
- Document the poll in the ops/runbook so the next production review doesn’t re-litigate it.

### Item 2 — do this

**Implement option 5:** stop covering the bottom bar with the mobile panel sheet/overlay, and make that bar reachable while a panel is open, without abandoning nested modal sheets for details and confirmations.

**Do not** ship option 2 alone (non-modal without a layout carve-out — bar still covered).  
**Do not** prefer option 3 unless option 5 proves too costly against nested-overlay tests — header mini-nav is the safe fallback that preserves Phases 2–3 unchanged.  
**Do not** leave option 1 as the permanent answer if mobile panel switching is part of the product pitch.

---

## 5. Open questions

1. **Item 1 cost:** Is a 15s poll on every authenticated session acceptable against the real API, or should a follow-up tune it before or with Phase 5 cleanup?
2. **Item 2 Escape policy under option 5:** With a reachable bar, should Escape close the panel (today’s modal behaviour) or first move focus / ignore when focus is in the bar? Needs an explicit choice in the implementation prompt.
3. **Visual gap:** Leaving `tabbar` height of chart visible under a translucent overlay vs opaque sheet ending above the bar — design preference; does not change the interaction recommendation.
4. **Still outstanding from Phase 4 (unchanged):** deployed feed verification; 1280px chart floor; ultrawide aspect; phone vertical overflow. None of those are this investigation’s items, but they remain open.

---

*No implementation in this pass. Item 1 needs no code unless product asks to tune the interval. Item 2 needs its own implementation prompt if the client accepts option 5 (or the option 3 fallback).*
