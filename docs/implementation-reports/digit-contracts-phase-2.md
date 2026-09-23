# Digit Contracts — Phase 2 Implementation Report

**Scope:** Trading surface only — `lastDigit` TS port, digit distribution row, contract-type tabs, digit order ticket, WS settlement toasts, stub replacement, Signals hidden when the flag is on, and a small chart entry marker. **Positions / History / SessionStrip digit rework is Phase 3.**

**Sources:** [`docs/investigation-reports/digit-contracts-investigation.md`](../investigation-reports/digit-contracts-investigation.md), [`docs/implementation-reports/digit-contracts-phase-0-1.md`](./digit-contracts-phase-0-1.md).

**Commits:** `d1842e9` → `ab8f490` (Step 1 → Step 7).

---

## 1. Summary

With `digit_contracts_enabled = true`, `/` mounts a real digit trading screen: volatility symbol picker, tick chart, 0–9 distribution row, Matches/Differs · Even/Odd · Over/Under tabs, and a stake-only ticket that opens contracts via `POST /digit-contracts`. Outcome buttons show snapshotted payouts from a **client mirror** of the server’s static table. On settlement, account-stream events `digit_opened` / `digit_settled` refresh queries and raise win/loss/void toasts.

**Still missing (expected until Phase 3):** open digit contracts are not listed in Positions; digit history is not in History; SessionStrip is still CFD-oriented. With the flag on those panels simply show no CFD activity. Signals is **hidden** from chrome (not deleted).

CFD `TradeScreen` is unchanged when the flag is off.

---

## 2. Per-step outcome

### Step 1 — Port `LastDigit` to TypeScript ✅

- `web/src/lib/lastDigit.ts` mirrors `models.LastDigit`: `toFixed(pipSize)`, walk from the end for the last `0–9` character.
- Unit tests match Go `TestLastDigit` cases.

### Step 2 — Digit distribution row ✅

- `DigitDistributionRow` counts last digits over the existing tick buffer (~600), shows `%` per digit, highlights the current price’s last digit.

### Step 3 — Contract-type tabs ✅

- `ContractTypeTabs`: Matches/Differs, Even/Odd, Over/Under. Switching tabs only changes ticket controls; chart and digit row stay shared.

### Step 4 — New order ticket ✅

- `DigitTicket` + `DigitTradeScreen`: stake field, `+5/+10/+50/Max`, available balance, disabled when feed is stale (same idea as CFD ticket).
- Per type: digit picker + Match/Differ buttons; Even/Odd buttons; barrier picker + Over/Under buttons.
- Submit → `digitApi.open` → toast + ticket remount for the next contract.
- **Deviation:** no separate Match/Differ “toggle” above the buttons — the two outcome buttons *are* the choice (same pattern as Even/Odd), which avoids a redundant control.

### Step 5 — Wire settlement events ✅

- `useAccountStream` types include `digit_opened` / `digit_settled`; invalidates digit contracts, wallet, transactions.
- `AppShell` toasts on `digit_settled` (won / lost / void). Temporary until Phase 3 panels exist.

### Step 6 — Replace stub, hide Signals ✅

- `MainSurface` → `DigitTradeScreen` when flag on; `DigitSurfaceStub` removed.
- `chromePanels()` omits Signals from TopBar / TabBar when flag on; `resolvePanel(..., hideSignals)` maps `?panel=signals` away. `SignalsPanel` and route map remain in the codebase.

### Step 7 — Chart annotation ✅

- Optional `entryPrice` on `PriceChart`: dashed “Entry” `ReferenceLine` for the first open digit contract on the selected symbol. Small, additive change only.

---

## 3. Files changed

| Area | Files |
|---|---|
| Last digit / payout | `web/src/lib/lastDigit.ts`, `lastDigit.test.ts`, `digitPayout.ts`, `digitPayout.test.ts` |
| API / hooks / types | `web/src/api/digit.ts`, `hooks/useQueries.ts`, `hooks/useAccountStream.ts`, `lib/queryKeys.ts`, `types/index.ts` |
| Surface | `DigitTradeScreen.tsx`, `DigitTicket.tsx`, `DigitDistributionRow.tsx`, `ContractTypeTabs.tsx`, `MainSurface.tsx`, `PriceChart.tsx`; deleted `DigitSurfaceStub.tsx` |
| Chrome | `panelItems.ts`, `TopBar.tsx`, `TabBar.tsx`, `usePanel.ts`, `usePanel.test.ts`, `AppShell.tsx` |

Backend Go files were **not** modified in this phase.

---

## 4. The payout-display mechanism (Step 4)

**Gap found:** Phase 0–1 snapshots payout server-side on open but does **not** expose a quote/table HTTP endpoint for the ticket to read before submit.

**Filled client-side (not a hardcoded one-off in the ticket):** `web/src/lib/digitPayout.ts` mirrors `internal/models/digit_payout.go` (Even/Odd `0.9520`, Match `8.0000`, Differ `0.1000`, Over/Under `0.95 × ((10/winCount) − 1)` at 4dp). Buttons show `winCredit(stake, ratio)` and a percent label. The server remains the source of truth for the stored `payout_ratio` / `potential_payout` on the opened contract.

**Follow-up for Phase 3+ (optional):** a thin `GET` quote or table endpoint would remove the dual-maintenance risk between Go and TS.

---

## 5. Anything discovered mid-implementation

1. **No payout quote API** — anticipated as a possible gap; confirmed and mirrored in TS (see §4).
2. **Running API binary was stale** during verification (`/tmp/tdp-api` from an earlier build lacked digit routes until rebuilt). Not a product bug; local process needed a rebuild after Phase 0–1.
3. **`windowChange.percent` is already ×100** (Decimal) — DigitTradeScreen uses `signedPercent` like CFD, not a second multiply.
4. **Digit contracts settle on the next tick** (~0.5–2s on mock feed) — open → settle toast cycle is fast; Positions would still help for mid-flight contracts.

---

## 6. Verification performed

### Automated

| Check | Result |
|---|---|
| `npm run typecheck` | Pass |
| `npm test` (83) | Pass (includes `lastDigit`, `digitPayout`, `resolvePanel` hide-Signals) |
| `npm run build` | Pass |

### Against mock feed (live API + DB flag)

1. Enabled flag: `UPDATE app_settings SET value = 'true' … WHERE key = 'digit_contracts_enabled'`.
2. Rebuilt/restarted API (`go build -o /tmp/tdp-api ./cmd/api`).
3. Registered a demo user; `/auth/me` reported `digit_contracts_enabled: true`.
4. **Opened and settled all three types** via `POST /digit-contracts` on `R_75`:

   | Type | Open payout (stake $10) | Final status (example run) |
   |---|---|---|
   | Even/Odd | `19.52` | `won` (settlement digit 4) |
   | Matches/Differs (match 5) | `90` | `lost` (settlement digit 4) |
   | Over/Under (over 4) | `19.5` | `won` (settlement digit 9) |

5. **Account WebSocket:** after open, received `digit_opened` then `digit_settled` with `{ contract, reason }` — the same events `AppShell` toasts on. (Browser toast appearance itself is the thin wrapper over this payload; UI chrome was not separately screenshot-tested in this pass.)

Vite (`:5173`) and API (`:8080`) were up with `USE_MOCK_FEED=true`.

---

## 7. Readiness for Phase 3

**Ready to proceed.** Backend open/settle/list and the trading surface are in place. Phase 3 should:

- Teach Positions / History (and SessionStrip if needed) to list digit contracts using existing `GET /digit-contracts` (+ summary if useful).
- Soften or replace the temporary settlement toasts once the panels show the same outcomes.
- Optionally add a payout quote API so the ticket can drop the TS mirror.

**No blockers** from this phase. Keep the dual Go/TS payout tables in lockstep until a quote endpoint exists.
