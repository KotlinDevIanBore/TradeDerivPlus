# Investigation: Missing Withdraw Button in Full-Screen Mode

**Date:** 26 September 2026 · **Branch:** `master` @ `a672bd7` · **Kind:** read-only. No code changed.
Every path below is under `web/` unless prefixed otherwise.

## Summary

Deposit and Withdraw are not rendered by the same component. Deposit has a dedicated button
in the top bar, which is present at every width; Withdraw's only chrome-level entry is the
navigation drawer, whose sole trigger — the hamburger — is `lg:hidden`, so at ≥1024px there
is no way to open the drawer at all. This is not a full-screen rendering bug and there is no
condition hiding a Withdraw button in the top bar: **there has never been one.** It is a
missing implementation left over from the one-screen migration, at a time when Withdraw was
a dead control because no user could become KYC-verified — a reason that stopped applying
when PR #13 shipped self-serve verification.

## Findings

### What "full screen" is in this app

There is no CSS Fullscreen API, no `isFullScreen`/`viewMode` flag, and no route named for it
(grep of `src/` for `fullscreen|full-screen|isExpanded|viewMode|requestFullscreen` returns
only two comments). The app has exactly one layout switch, Tailwind's `lg`:

- [`src/hooks/useIsDesktop.ts:4`](../web/src/hooks/useIsDesktop.ts) — `(min-width: 1024px)`,
  "where the app switches to its desktop layout".
- [`src/components/ui/Panel.tsx:10-15`](../web/src/components/ui/Panel.tsx) — below it a panel
  is "a full-screen sheet"; at or above it the same panel is a 300px column beside the chart.

"Full-screen view" therefore means the **desktop layout at ≥1024px**, where the chart takes
the whole window (`AppShell.tsx:210-213`). The findings below are about that breakpoint. (If
the report meant a phone with a panel open as a full-screen sheet, the same root cause
applies with less severity: the top bar above the sheet shows Deposit and not Withdraw, but
the hamburger is still visible, so Withdraw is two taps away rather than unreachable.)

### Where Deposit's visibility is determined

- [`src/features/shell/TopBar.tsx:128-131`](../web/src/features/shell/TopBar.tsx) — a
  `<Button onClick={onDeposit} aria-label="Deposit">` with **no conditional wrapper**. The only
  responsive logic swaps the word for an icon below 400px; the button itself always renders.
- Wired at [`src/features/shell/AppShell.tsx:178`](../web/src/features/shell/AppShell.tsx) as
  `onDeposit={overlay.deposit}`.

### Where Withdraw's visibility is determined

Withdraw is reachable from three places. Two are gated by width, one by panel state:

| Surface | File | Rendered when | Reachable at ≥1024px? |
|---|---|---|---|
| Navigation drawer | [`NavDrawer.tsx:55-56`](../web/src/features/shell/NavDrawer.tsx) — Deposit and Withdraw side by side, unconditional | drawer open | **No.** The drawer's only trigger is the hamburger at [`TopBar.tsx:48-55`](../web/src/features/shell/TopBar.tsx), `className="… lg:hidden"`. `overlay.menu` has exactly one caller (`AppShell.tsx:176`, that button). Nothing else opens it. |
| Balance card | [`BalanceCard.tsx:91-96`](../web/src/features/wallet/BalanceCard.tsx) — `{!isDemo ? <Button … onClick={onWithdraw} disabled={!canWithdraw}>Withdraw</Button> : null}` | History panel open, real account | Yes, but two clicks in: History → panel column → card. The card is rendered only by [`HistoryPanel.tsx:66,149`](../web/src/features/wallet/HistoryPanel.tsx). |
| Top bar | [`TopBar.tsx`](../web/src/features/shell/TopBar.tsx) | — | **There is no Withdraw button.** `git log -S'Withdraw' -- TopBar.tsx` is empty: the string has never appeared in the file. The component has an `onDeposit` prop and no `onWithdraw`. |

### The specific condition (or missing condition)

There is **no condition wrapping a Withdraw button that a Deposit button escapes.** The
asymmetry is structural:

1. `TopBar` renders Deposit and not Withdraw (`TopBar.tsx:128` vs. nothing).
2. The drawer, which renders both, is opened only by a control hidden at `lg`
   (`TopBar.tsx:52`).
3. The width switch is the *same* breakpoint at which the drawer becomes unreachable, so
   the desktop layout loses Withdraw's chrome entry precisely when it gains its full-screen
   chart.

Two further conditions apply to the one desktop path that does exist (the balance card in
History), and are worth ruling out because they are easy to mistake for the bug:

- **Demo account hides the button entirely** — `BalanceCard.tsx:91`, `!isDemo`. On a demo
  account there is no Withdraw anywhere on desktop.
- **Not verified, or withdrawals off, disables it** — `HistoryPanel.tsx:58-59`:
  `canWithdraw = user.withdrawals_enabled !== false && user.kyc_status === 'verified'`. In
  production `withdrawals_enabled` is `false` (see `docs/project-status.md` §7), so the button
  is present but disabled for every user, with the hint "Withdrawals are not available yet".

Neither of these affects the drawer's Withdraw, which is unconditional and lets `WithdrawSheet`
explain the gate ([`WithdrawSheet.tsx:89,106`](../web/src/features/wallet/WithdrawSheet.tsx)).
So the phone gives a user a Withdraw control that explains why it refuses; the desktop, for a
demo or unverified user, gives them nothing to click at all.

### Test coverage confirms the gap

The only spec that asserts a Withdraw control is
[`e2e/user.panels.spec.ts:178`](../web/e2e/user.panels.spec.ts), inside the drawer test — and
that test is `test.skip(isWide(page), …)` at `:154`. The `desktop` Playwright project runs the
same spec files and **never asserts that Withdraw is reachable at 1024px+**. Nothing would have
caught this.

## Root Cause

**Missing implementation**, not a bug in conditional logic and not a deliberate feature gate.
The history is unambiguous:

- **`4a179d5` (22 Sep) "Put navigation in the top bar and retire the sidebar"** created
  `TopBar.tsx` with a Deposit button and `overlays.ts` with both `deposit` and `withdraw`
  actions. The commit's diff mentions Withdraw only in `AppShell.tsx` (mounting the sheet) and
  `overlays.ts` — never in `TopBar.tsx`. The phase report written for it says, verbatim:
  *"The top bar now also carries the balance, a Deposit button, and the account switcher …
  On a phone, a hamburger opens a left drawer with Deposit, Withdraw, …"*
  (`docs/implementation-reports/one-screen-migration-phase-0-1.md:16`). Withdraw was placed in
  the drawer only, and the drawer was described as the phone's.
- **`a63f31c` (22 Sep) "make the bottom bar four panel toggles"** (Phase 4) reduced the drawer
  to "the three things that are not panels: Deposit, Withdraw, Log out"
  (`one-screen-migration-phase-4.md:13`) and `TabBar.tsx:14-15` still says so. At the same
  time the hamburger stayed `lg:hidden`. No desktop home for Withdraw was added.
- **`bd33593` (22 Sep) "delete Home"** removed the one screen where Withdraw had been a
  first-class button at every width; `BalanceCard` survived only inside the History panel.
- **Why it was left there:** the investigation that shaped this work flagged Withdraw as
  effectively dead — *"gated on `kyc_status === 'verified'`, and there is no way for a user to
  submit verification … Giving Withdraw more prominence without fixing that means giving a
  dead control more prominence"* (`docs/investigation-reports/nav-layout-investigation.md:49,
  307`). That was true on 22 September. It stopped being true on 25 September when PR #13
  merged self-serve KYC. The reason for the omission has lapsed; the omission has not.

Not a regression: there was never a desktop Withdraw entry in the top bar to lose. No TODO,
comment, or issue references it (grep of `TODO|FIXME` × `withdraw` in `src/` is empty).

## Recommended Fix

*Not applied — pending confirmation.* Two options, in order of preference.

**Option A — a Withdraw button beside Deposit in the top bar, desktop only.** In
`TopBar.tsx`, add an `onWithdraw` prop and, next to the Deposit button at `:128`, an outline
`<Button size="sm" variant="outline" onClick={onWithdraw} aria-label="Withdraw" className="hidden lg:inline-flex">`.
Wire `onWithdraw={overlay.withdraw}` in `AppShell.tsx:172-179`. This mirrors the benchmark's
desktop top bar (which carries Withdraw in its row, `nav-layout-investigation.md:129`), keeps
the phone unchanged (the drawer already has it and the bar is crowded below 400px), and lets
`WithdrawSheet` do the explaining for demo/unverified/off states exactly as it does on the
phone. Touches two files; no money logic. Under the standing verification rule this is a
build + `tsc` + one targeted check (open the app at 1280px, see Withdraw, click it, see the
sheet), plus one desktop assertion added to `user.panels.spec.ts` so the gap cannot reopen.

**Option B — make the drawer reachable on desktop** by removing `lg:hidden` from the
hamburger. Smallest possible change, but it reintroduces a menu whose three items are already
elsewhere on desktop (Deposit in the bar, Log out in the Account panel), so two of its three
entries would be redundant, and the Phase 4 report's rationale for the drawer being the
phone's chrome would need rewriting. Not recommended unless a desktop menu is wanted for
other reasons.

**Either way, one related decision.** `BalanceCard.tsx:91` hides Withdraw completely on a
demo account, while the drawer shows it and lets the sheet explain. Whichever surface
becomes the desktop entry should follow the drawer's behaviour — show, and let
`WithdrawSheet` refuse with a reason — so a demo user on desktop is told *why* rather than
left to wonder where the button went.
