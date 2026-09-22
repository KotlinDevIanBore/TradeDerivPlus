# Digit Contracts — Phase 3 Implementation Report

**Scope:** Payout quote endpoint (single source of truth), Positions/History/SessionStrip digit branches, visual settlement-toast verification. Closes the gaps Phase 2 left open.

**Sources:** [`digit-contracts-investigation.md`](../investigation-reports/digit-contracts-investigation.md), [`digit-contracts-phase-0-1.md`](./digit-contracts-phase-0-1.md), [`digit-contracts-phase-2.md`](./digit-contracts-phase-2.md).

**Commits:** `99bad47` → `cf9dd24` (quote → panels → strip → toast soften), plus this report.

**Screenshots:** [`assets/digit-phase-3/`](./assets/digit-phase-3/).

---

## 1. Summary

A user can now walk a digit contract’s full lifecycle in the UI: pick a type on the ticket (payouts from the **server** quote), open it, see it briefly as pending in Positions (or an empty pending state if the next tick already settled), then find the won/lost/void row in History and Positions → Closed, with SessionStrip updating session P/L and W/L. Settlement still raises a short toast. With the flag off, CFD Positions/History/SessionStrip and Signals chrome behave exactly as before.

---

## 2. Per-step outcome

### Step 1 — Payout quote endpoint ✅ (done first)

- `GET /api/v1/digit-contracts/quote?contract_type=&stake=&parity=|mode=&digit=|side=&barrier=`
- Reads `models.PayoutRatioFor` + `WinCredit` — same path Open snapshots.
- `DigitTicket` uses `useDigitQuote` for both outcome buttons.
- **Deleted** `web/src/lib/digitPayout.ts` and its test. No remaining client payout table.
- Route registered **before** `/digit-contracts/:id`.

### Step 2 — Positions (pending) ✅

- Flag on → `DigitPositionsPanel`: open list via `status=open`, no close button, “Settles next tick”.
- Closed tab also lists settled contracts (same rows as History) for convenience.
- Flag off → previous CFD `PositionsPanel` unchanged.

### Step 3 — History (settled) ✅

- Flag on → settled digit contracts (`status=settled` = won|lost|void), with **Void** badge distinct from Lost (“Refunded” vs signed P/L).
- Flag off → CFD transaction History unchanged.
- Repo: `ListByAccount` accepts `status=settled`.

### Step 4 — SessionStrip digit metrics ✅

- `digitSessionStats()` mirrors Pass 1’s approximate, session-scoped honesty.
- Digit strip: Session P/L, Pending, Session W/L(/V). **No Open P/L.**
- Mounted on `DigitTradeScreen`. CFD strip unchanged when `digitSession` is absent.

### Step 5 — Toast verification ✅

- Softened copy (panels carry detail); **kept** brief toasts — next-tick settles can finish before the user opens Positions, so a toast still confirms the outcome.
- Visual proof: win and loss toasts in screenshots (see §5). Void not visually forced on the live mock feed (always settles before the 5s grace); void toast code path remains.

### Step 6 — End-to-end walkthrough ✅

- Flag on: opened Even/Odd, Matches/Differs, Over/Under; History showed outcomes; SessionStrip updated; Signals absent from chrome.
- Flag off: CFD Buy ticket, Signals in top bar, Open P/L SessionStrip (`cfd-flag-off.png`, `cfd-positions.png`).

---

## 3. Files changed

| Area | Files |
|---|---|
| Quote API | `digit_handler.go`, `digit_service.go`, `digit_contract.go` (`DigitQuote`), `routes.go` |
| List settled | `digit_contract_repo.go` (`status=settled`) |
| Web ticket | `api/digit.ts`, `useQueries.ts`, `queryKeys.ts`, `types`, `DigitTicket.tsx`; **deleted** `digitPayout.ts(.test)` |
| Panels | `PositionsPanel.tsx`, `HistoryPanel.tsx`, `DigitContractRows.tsx`, `digitLabels.ts` |
| Session | `sessionStats.ts(.test)`, `SessionStrip.tsx`, `DigitTradeScreen.tsx` |
| Toasts | `AppShell.tsx` |
| Shots helper | `web/e2e/digit-phase3-shots.mjs` (optional runner) |

---

## 4. Confirmation the payout duplication is closed

1. Baseline quote (Even, stake 10): `payout_ratio: "0.952"`, `potential_payout: "19.52"`.
2. Changed **only** Go `placeholderEvenOdd` to `"1.2345"`, rebuilt API, **no TypeScript edit**.
3. Quote returned `payout_ratio: "1.2345"`, `potential_payout: "22.35"`.
4. Restored Go table to `0.9520`.

Ticket buttons now only display `quote.potential_payout` / ratio from that endpoint. `digitPayout.ts` is gone.

---

## 5. Toast verification (Step 5)

| Shot | What it shows |
|---|---|
| [`toast-settled.png`](./assets/digit-phase-3/toast-settled.png) | “Even opened…” + **“R_75 won · +$9.52”**; SessionStrip `+$9.52` / `1W` |
| [`toast-lost.png`](./assets/digit-phase-3/toast-lost.png) | “Match 0 opened…” + **“R_75 lost · -$10.00”**; SessionStrip `1W / 1L` |
| [`history-settled.png`](./assets/digit-phase-3/history-settled.png) | Won/Lost badges, settlement digit, P/L in History |

**Decision:** keep soft toasts. Panels are authoritative for history; toast covers the sub-second settle window.

---

## 6. Anything discovered mid-implementation

1. **Pending row rarely visible** on mock feed (~0.5–2s next tick). Empty “No open contracts” is normal; Entry chart marker / toast / History still prove the open.
2. **`status=settled`** needed — Phase 0–1 only filtered a single status.
3. Quote must be registered before `:id` or Gin treats `quote` as a UUID.
4. Stale API binary still a local footgun after Go changes (rebuild `/tmp/tdp-api`).

---

## 7. State of the digit contracts feature overall

**Planned build work from the investigation is effectively complete** for a demo/mock-feed product: flag, open/settle, trading surface, quote, panels, session metrics.

**Remaining product decisions before real money** (unchanged from the investigation):

| Open question | Status |
|---|---|
| **Real payout numbers** | Still PLACEHOLDER in `digit_payout.go` — now the *only* table |
| **Stake-vs-payout toggle** | Deferred; stake-only v1 |
| **Admin UI for the flag** | SQL/`DIGIT_CONTRACTS_ENABLED` seed only |
| **Demo vs real economics** | Same placeholders; no separate real-money table |
| **Void UX in the browser** | Rare with a healthy feed; History distinguishes void when it happens |

Optional polish (not blockers): quote cache/ETag, Positions “catch” of pending mid-flight on slower feeds, admin settings UI.
