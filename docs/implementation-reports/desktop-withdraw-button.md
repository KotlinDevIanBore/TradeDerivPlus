# Desktop Withdraw Button — Implementation Report

**Scope:** Option A of [`docs/investigation-withdraw-button-fullscreen.md`](../investigation-withdraw-button-fullscreen.md) — a desktop-only Withdraw button in the top bar. UI wiring only.
**Date:** 26 September 2026 · **Branch:** `master` @ `a672bd7` · **Working tree, not yet committed.**
**Not in scope:** Option B (the hamburger/drawer), and any payment or withdrawal business logic.

---

## 1. Summary

**Withdraw is now reachable at every width.** At ≥1024px the app's only chrome entry to
Withdraw was the navigation drawer, whose sole trigger is a hamburger carrying `lg:hidden` —
so the one breakpoint where the chart goes full-screen was the breakpoint with no way to open
the drawer at all. Deposit has had its own button in the top bar since the sidebar was retired;
Withdraw never did. It does now, beside it, desktop-only.

**The button is shown unconditionally, and that is the design decision worth reading.** A demo
account, an unverified user and the platform's `withdrawals_enabled` switch are all states
`WithdrawSheet` already explains in words. `BalanceCard` takes the other approach — it removes
the button entirely on a demo account — and the result is a user who cannot tell whether
withdrawal is unavailable or whether they simply cannot find it. A control that is missing
explains nothing, so this one follows the drawer's behaviour rather than the balance card's.

**Three files, 45 lines added, nothing removed or changed.** The mobile layout is untouched by
construction: the new markup is `hidden lg:inline-flex`.

---

## 2. What changed

| File | Change |
|---|---|
| [`web/src/features/shell/TopBar.tsx`](../../web/src/features/shell/TopBar.tsx) | `onWithdraw: () => void` added to the props type and destructure; an outline `<Button>` after the Deposit button, `className="hidden lg:inline-flex"` |
| [`web/src/features/shell/AppShell.tsx`](../../web/src/features/shell/AppShell.tsx) | `onWithdraw={overlay.withdraw}` on the `TopBar` render, beside the existing `onDeposit` |
| [`web/e2e/user.panels.spec.ts`](../../web/e2e/user.panels.spec.ts) | New `Money on a desktop` describe: one desktop-only test |

```tsx
<Button
  size="sm"
  variant="outline"
  onClick={onWithdraw}
  aria-label="Withdraw"
  className="hidden lg:inline-flex"
>
  Withdraw
</Button>
```

No new state, no new store, no new query. `overlay.withdraw` and the single `WithdrawSheet`
mount in `AppShell` have existed since `4a179d5`; this is the caller that was missing.

### 2.1 Two details that are load-bearing

**`variant="outline"` is not decoration.** Deposit is the primary action and stays solid;
withdrawal is the rarer, heavier one. The same pairing already exists in `BalanceCard`
(`:86` solid Deposit, `:92` outline Withdraw), so the two surfaces now agree.

**`hidden lg:inline-flex` works because `cn` is `twMerge`.** The `Button` base class includes
`inline-flex`, which would otherwise conflict with `hidden`. `cn` merges through
`tailwind-merge` ([`web/src/lib/cn.ts`](../../web/src/lib/cn.ts)), so the later utility from
`className` wins and the element is genuinely `display: none` below `lg` — not an
`inline-flex` element fighting a `hidden` class. Verified by the test skipping on the mobile
project rather than finding a hidden-but-present button.

---

## 3. The test

One test, desktop-only, in the file that already owns this app's navigation coverage:

```ts
test.skip(!isWide(page), 'the phone reaches Withdraw through the drawer, covered above')
```

It asserts both money actions are in the banner, clicks Withdraw, and expects the sheet. The
dialog matcher is deliberately either-or:

```ts
page.getByRole('dialog', { name: /Withdraw to M-Pesa|Verification required/ })
```

`withdrawals_enabled` is `false` by default ([`internal/config/config.go:280`](../../internal/config/config.go)),
so today the sheet opens on "Not available yet" under the `Withdraw to M-Pesa` title. With the
flag on, an unverified user gets "Verification required" instead. **Matching either means the
test does not start failing the day that flag is flipped** — it asserts that the button reaches
the sheet, which is this change's actual contract, rather than pinning a platform state that
has nothing to do with the button.

**Why this gap existed unnoticed:** the only prior assertion of a Withdraw control lives inside
the phone drawer test, which is `test.skip`ped at wide viewports (`:154`). The `desktop`
Playwright project ran the same file and never checked Withdraw was reachable. That is now
closed from the other side.

---

## 4. Verification

Classified as **not a money-path change** under the standing rule — no ledger, balance, stake,
payout or transaction logic is touched — so: build, typecheck, and a targeted check of the
specific fix, rather than the full suites.

| Check | Result |
|---|---|
| `npm run typecheck` | exit 0 |
| `npm run build` | exit 0 |
| `npm run lint` (oxlint) | nothing in the three changed files |
| New test, `--project=desktop` | **passed**, 3.2s |
| New test, `--project=mobile` | **skipped**, as intended |
| Full `user.panels.spec.ts`, mobile + desktop | **15 passed, 3 skipped** |
| `npx vitest run` | 130 passed, 13 files |

Run against a throwaway database with `DIGIT_CONTRACTS_ENABLED=false` — what the
`mobile`/`desktop` projects require — mirroring the `e2e` job's environment. API stopped,
database dropped, ports confirmed free afterwards.

**Mobile is unchanged**, and the evidence is the whole spec passing on the `mobile` project
including the drawer test, which still finds Deposit, Withdraw and Log out in the drawer.

### 4.1 Not verified

- **Crowding at exactly 1024px.** The `desktop` project runs at 1280px. At 1024 the bar carries
  the nav row, account pill, balance, Deposit, Withdraw and the account icon. The button group
  is `shrink-0`, so the account pill compresses before anything wraps — but this has not been
  looked at, and 1024 is the narrowest width at which the new button appears.
- **Formatting.** All three files already failed `prettier --check` on `master` before this
  change, so they were left as they are rather than mixing an unrelated reformat into the diff.
  CI runs `typecheck`, `test` and `build` only; nothing gates on prettier.

---

## 5. One thing left open

`BalanceCard.tsx:91` still hides Withdraw completely on a demo account, while the drawer and now
the top bar both show it and let the sheet refuse with a reason. **Three surfaces, two
behaviours.** This change deliberately did not touch the balance card — it is outside the
stated scope — but the inconsistency is now more visible than it was, and the balance card is
the odd one out. Worth a follow-up that makes it match, not a reason to have made this button
behave the other way.
