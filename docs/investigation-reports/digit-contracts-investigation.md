# Digit Contracts Feature — Investigation & Plan

**Type:** Investigation and planning only. No implementation code, styling, or component changes in this pass.

**Date:** 23 September 2026

**Prior reading (date order, as instructed):**  
[`ui-polish-investigation.md`](./ui-polish-investigation.md) → [`ui-polish-implementation.md`](../implementation-reports/ui-polish-implementation.md) → [`nav-layout-investigation.md`](./nav-layout-investigation.md) → [`one-screen-migration-plan.md`](./one-screen-migration-plan.md) → Phase 0–1 / 2 / 3 / 4 implementation reports → [`post-migration-fixes-investigation.md`](./post-migration-fixes-investigation.md).  
Also: Pass 1 / migration confirmation that P/L is proportional (`internal/models/pnl.go`), no fixed payout, no expiry, no contract type; contract tabs / digit row / AUTO–MANUAL repeatedly ruled out until this feature.

**Confirmed decisions this plan is built on (not re-litigated):**

- Digit contracts replace the **primary trading surface** behind a **feature flag**; the proportional CFD UI and logic stay in the repo, not deleted, reachable only when the flag is off.
- AUTO / MANUAL, bot execution, Live Chat, and contract types beyond Matches/Differs, Even/Odd, Over/Under remain out of scope.
- The one-screen shell (top bar, bottom panel toggles, drawer, `?panel=`, Positions / History / Signals / Account) stays; this feature changes what the surface and those panels *mean*, not whether they exist.

**Benchmarks:** [`docs/reference/tnnbinary-trade-benchmark.png`](../reference/tnnbinary-trade-benchmark.png), [`docs/reference/tnnbinary-mobile-trade.jpeg`](../reference/tnnbinary-mobile-trade.jpeg).

---

## 1. Summary

### Plain language

Today TradeDerivPlus is a **continuous CFD**: you stake, price moves, profit is `stake × Δprice / entry`, and the position ends when you close it (or SL/TP hits). The benchmark’s digit product is a **binary contract**: you stake, pick a last-digit outcome, wait for a **fixed expiry** (usually the next tick or a short tick count), and either receive a **fixed payout** or lose the stake. Those are different products sharing a wallet and a chart.

This is closer in size to the **one-screen migration** than to Pass 1 polish. Expect **new backend tables and settlement**, a **new order ticket**, and **reworked open/closed lists** — not a skin on `OrderTicket.tsx`.

### Rough cost

**High overall**, deliverable in phases (section 7). The risky middle is the same shape as migration Phase 2: first end-to-end path (open → auto-settle → wallet credit) must be correct before UI polish. Frontend shell work is medium; the payout/settlement rules are where silent money bugs live.

### Biggest risk

**Settlement that fails quietly.** A digit contract that never resolves leaves the user’s stake debit outstanding with no credit path. The proportional model only settles on an explicit close or an SL/TP tick handler that already exists; digit contracts *must* settle on a future tick the feed may drop. Design must include a **primary tick-driven settler** plus a **safety net** (check-on-read and/or periodic sweep). That is the single failure mode that can lose money or trust.

### Second risk (product, not engineering)

**How payout percentages are set.** The benchmark shows e.g. **$19.52 / 95.20%** on a $10 Even stake. That number is not implied by our codebase and is not a free parameter — it is the house edge. Building a live “odds engine” from the digit-distribution row would be easy to get wrong and easy to misrepresent. This plan recommends a **transparent fixed payout table for v1**, with the digit row as **informational statistics only**, not the price of the contract. The client must still approve the table (open question in section 9).

### What we are not building in v1

- AUTO trading, TARGET / STOP LOSS / MULT fields on the ticket (benchmark chrome for the bot; out of scope with AUTO).
- Extra contract types beyond the three named.
- Deleting CFD code paths.
- Using empirical digit frequencies as the *price* of a contract without an explicit product decision.

---

## 2. Backend contract model recommendation

### 2.1 Extend `trades` vs parallel table

**Recommend: parallel model** — new table (working name `digit_contracts`) and parallel service/API, reusing wallet/ledger patterns, not columns bolted onto `trades`.

| Approach | Pros | Cons |
|---|---|---|
| **Extend `trades`** | One list API; one Positions panel data shape | Two settlement semantics in one row; nullable digit/barrier/expiry/payout fields forever; `pnl.go` and SL/TP watcher become conditional nightmares; summary “open_pnl” becomes meaningless for half the rows |
| **Parallel** | Clear types; CFD path untouched when flag is off; settlement job scoped to one table | Second list/summary API (or a facade); Positions/History UI branches on flag; some duplicated open/debit plumbing |

“Extend” is how two products become one untestable `settle()` forever. The migration already taught us that **one screen with two meanings** is cheaper when the *shell* is shared and the *domain objects* stay separate. Same here: share auth, accounts, wallets, feed; do not share the trade row.

### 2.2 Proposed data model (design only)

Conceptual fields (names illustrative):

| Field | Role |
|---|---|
| `id`, `account_id`, `symbol_id` | Identity / linkage (same as trades) |
| `contract_type` | `matches_differs` \| `even_odd` \| `over_under` |
| `selection` | Typed payload: for Matches/Differs → `{ mode: match\|differ, digit: 0–9 }`; Even/Odd → `{ parity: even\|odd }`; Over/Under → `{ side: over\|under, barrier: 0–9 }` |
| `stake` | Debited at open (same wallet rules as trades: bonus-then-real, frozen gate) |
| `payout_ratio` | Snapshot **at entry** (e.g. `0.9520` meaning win credits `stake × (1 + ratio)` — confirm exact formula with client; see §2.3) |
| `potential_payout` | Denormalised money amount shown on the button at entry (auditability) |
| `entry_price`, `entry_digit`, `entry_tick_epoch` (or sequence) | What the market looked like when the stake was taken |
| `ticks_to_expiry` or `expires_at` | v1 recommendation: **tick count** (typically `1` = next tick after entry), not wall-clock, matching “1T” in the benchmark |
| `status` | `open` \| `won` \| `lost` \| `void` (void for feed gap / admin cancel — product decision) |
| `settlement_price`, `settlement_digit`, `settled_at` | Filled by settler |
| `pnl` | Money result: on win `potential_payout − stake` (or total credit − stake); on loss `−stake`; stored for History/session |

Ledger: prefer **new transaction types** `digit_open` / `digit_settle` (or `digit_win` / `digit_loss`) so admin/history filters and `QueryFundingBalance`-style lookups do not confuse CFD closes with digit settlements. Reuse `Wallets.Adjust`, `WithTx`, account freeze checks, and account WS events (`wallet` + a new `digit_settled` or reuse `trade_closed` with a discriminant — prefer a **distinct event type** so the UI does not assume proportional PnL in the payload).

### 2.3 Payout approach

**Recommend for v1: static payout table per contract family (and selection class), snapshotted onto the contract at open.**

| Approach | Verdict |
|---|---|
| **Live odds engine** from digit-distribution frequencies | **Not for v1.** Frequencies over ~minutes of ticks are **not** the true probability of the next tick on synthetic volatility indices; pricing off them invites edge cases, user confusion (“why did payout move?”), and regulatory/trust risk if presented as fair odds. |
| **Fixed table** (Even/Odd → e.g. 95.2%; Match digit → high payout; Differ → low; Over/Under → depends on barrier width) | **Yes for v1.** Simple, auditable, matches “percentage on the button,” server is source of truth, client only displays. |
| Hybrid later | Optional Phase N+: adjust table by symbol/volatility band without claiming the digit row *is* the price. |

**Illustration from the benchmark (not a commitment):** $10 stake → $19.52 shown with 95.20% suggests total credit on win ≈ `stake × (1 + 0.952)` = $19.52 (stake returned + 95.2% profit). Loss = stake already taken, credit $0. **Client must confirm** win credit formula and the exact percentages per type (section 9).

Even/Odd near 100% is economically “coin flip minus house.” Matches on a single digit fair multiple is ~9× profit if digits were uniform; Differ and Over/Under need barrier-aware rows in the table. Engineering can ship a config struct / DB seed; **product owns the numbers**.

### 2.4 Settlement approach

**Primary: tick-driven settlement in the feed consumer path** (same place SL/TP auto-close already runs for CFDs in `trading_service` / open-by-symbol cache). On each tick for a symbol:

1. Load open digit contracts for that symbol whose expiry condition is met (e.g. `ticks_seen_since_entry >= ticks_to_expiry`, or `tick.epoch > entry_tick_epoch` for “next tick”).
2. Derive settlement digit from the tick’s price at the symbol’s `pip_size` (define “last digit” once — recommended: last character of the price formatted to `pip_size` decimal places).
3. Evaluate win/loss from `contract_type` + `selection`.
4. Credit wallet inside a transaction; mark status; emit WS event.

**Safety net (required):**

- **Check-on-read:** `List` / `Get` of open contracts attempts settle if expiry is already past according to `Feed.Latest` (handles missed WS events).
- **Periodic sweep** (e.g. every 5–15s): all open contracts past expiry — catches feed stalls and process gaps.
- **Restart behaviour:** open contracts must live in Postgres, not only in memory (unlike the tick buffer). On boot, sweep immediately.

**Cheapest correct combo:** tick handler + sweep. Check-on-read alone is too late if the user never opens Positions. Sweep alone can delay settlement by the interval (bad UX on 1-tick contracts). Tick + sweep is the minimum.

**Silent-fail risks to design against:**

| Risk | Mitigation |
|---|---|
| Tick dropped / consumer behind | Sweep + check-on-read |
| Process crash between debit and insert | Single DB transaction for open (same as trades today) |
| Process crash between win decision and credit | Settler must be **idempotent** (`GetOpenForUpdate` pattern already used on trades) |
| Symbol unsubscribed / no ticks | Sweep marks `void` or holds `open` with UI warning — **client decision** (section 9) |
| Ambiguous last digit (forex vs synthetic) | Restrict digit contracts to volatility-style symbols in v1, or define digit extraction per category |

**Expiry primitive for v1:** **tick count**, default **1** (next tick), optional small set (1 / 2 / 5) if product wants it later. Wall-clock expiry is harder with gappy feeds and is not what “1T” suggests.

---

## 3. Digit-distribution data plan

### What the benchmark shows

A 0–9 row under the chart: empirical share of recent last digits, with the **current** last digit highlighted. Separate from the stake/payout controls.

### Buffer adequacy

Server: `tickBufferSize = 600` per symbol, **in-memory only**, ~5 minutes at mock ~2 Hz (Pass 1 / nav investigation). Client market store keeps a similar ring.

**For a UI frequency display: 600 ticks is enough** to show stable-looking percentages that move as ticks arrive. It is **not** enough for multi-hour statistics, and it **resets on API restart** — acceptable for v1 chrome if we label it as “recent” implicitly by behaviour (optional copy later).

**Do not** require a new ticks table for v1 distribution UI. Persistence becomes necessary only if product wants durable history, post-restart continuity, or server-authoritative distribution for *pricing* (which we are not doing in v1).

### Where to compute

**Recommend: client-side from ticks the shell already streams**, with the same last-digit rule as settlement.

| | Client-derived | Server endpoint / WS field |
|---|---|---|
| Pros | No new API; uses existing `/ws/prices` + seed `/ticks`; works offline-of-API for display once ticks are in memory | One definition shared with settlement; easier to test parity |
| Cons | Must share digit-extraction helper with… nothing on server unless duplicated | Extra load; still based on same 600 buffer |

**Pragmatic split:** implement `lastDigit(price, pipSize)` in **one shared rule documented in the API contract**, implemented in Go for settlement and TypeScript for the row (mirror like `pnl.go` / `pnl.ts`). Optionally add `digit_histogram` on an existing ticks response later if drift appears in QA — not a Phase 0 blocker.

**Server-side histogram** is justified if settlement and UI must never disagree on “current digit” highlighting under race conditions; low priority if both use the same tick stream the user sees.

---

## 4. Feature flag design

### Requirement

Flippable **without a full redeploy** when the client wants the old CFD surface back quickly; global for everyone (confirmed decision).

### Recommendation

| Layer | Mechanism |
|---|---|
| **Source of truth** | Postgres `app_settings` (or single-row `feature_flags`) key `digit_contracts_enabled` boolean |
| **Bootstrap default** | Env `DIGIT_CONTRACTS_ENABLED` (same style as `USE_MOCK_FEED`) applied on first boot / migration seed if the row is missing |
| **Read path** | API caches the flag (e.g. 5–30s) and exposes it on a tiny public-authenticated bootstrap — e.g. field on `GET /me` or `GET /config` — so the web app does not need its own secret |
| **Flip without redeploy** | Admin SQL, or a minimal admin console toggle (admin already exists) that updates the row; web clients pick it up on next poll / focus / reload |
| **Web build-time flag alone** | **Insufficient** — needs redeploy of the static host to switch back |

`USE_MOCK_FEED`-only would fail the “no full redeploy” bar for the *frontend* host and for operators who only control the API. Env-only on the API still requires API restart. **DB (or equivalent runtime store) + short TTL cache** meets the requirement.

### Global vs per-user

**Global is fine.** Nothing in auth/accounts requires per-user trading-mode: one user has demo/real accounts, not per-account product modes. Per-account would complicate Positions (“which product is this account on?”) for no confirmed benefit. Revisit only if a staged rollout to staff accounts is needed — then a allowlist of user IDs is a one-column extension, not the default design.

### What the flag gates

| Gated | Not gated |
|---|---|
| Which **main surface** `/` renders (digit ticket + digit row vs CFD `TradeScreen` / `OrderTicket`) | Shell: TopBar, TabBar, NavDrawer, `?panel=`, overlays (deposit/withdraw/switcher) |
| Which **APIs the UI calls** for open/list/summary of positions | Auth, wallet, symbols list, price WS (shared) |
| **Positions / History / SessionStrip** content shape (contract cards vs CFD cards) | Account panel, KYC, admin (except optional flag toggle) |
| **Signals** visibility (recommend hide when digit mode on — §5) | Routing table of redirects (`/trade` → `/`, etc.) — keep; both modes use `/` |

Backend: **keep CFD trade endpoints alive** whenever the flag is off; when on, UI simply does not call them. Optionally reject `POST /trades` with `403/503 feature disabled` when the flag is on to prevent stale clients — nice hardening, not the core design.

Chart component can stay mounted in both modes; only annotations differ (§5).

---

## 5. Reuse / rebuild table

Same disposition style as the migration plan.

| Piece | Under digit flag ON | Reuse? | Notes |
|---|---|---|---|
| **Shell (TopBar, TabBar, drawer, panels host)** | Unchanged | **Reuse as-is** | Four panels remain the chrome model |
| **`PriceChart` / range toggle / price scale** | Keep | **Reuse with small additions** | Pass 1 chart is fine. Optional: mark entry tick / expiry cue. No candle work. “1T” may replace or sit beside 1m/5m/15m — product choice; tick window is more honest for digit play |
| **Digit distribution row** | New, under chart | **New** | Client histogram; highlight current last digit |
| **Contract-type tabs** | New on surface (Matches/Differs, Even/Odd, Over/Under) | **New** | Mobile: under top bar or above chart (benchmark). Desktop: above ticket or under symbol header |
| **Order ticket** | Replaced | **Rebuild; steal stake UX only** | Reuse patterns: stake field, +/- , quick chips, available balance, disabled when feed stale. **Do not** reuse Buy/Sell, SL/TP, projected `pnl()`. New: type tabs, selection (digit / parity / barrier), stake↔payout display, two outcome buttons (Even/Odd, Match/Differ, Over/Under) with snapshotted payout |
| **TARGET / SL / MULT** | Absent in v1 | **Skip** | Benchmark bot controls; out of scope with AUTO |
| **AUTO / MANUAL** | Absent or Manual-only disabled stub | **Skip build** | Confirmed out of scope |
| **Positions panel** | Open = pending contracts | **Heavy rework / parallel panel content** | No live unrealized P/L. Show type, selection, stake, potential payout, countdown (ticks remaining), status pending. Close action: **usually none** (auto-settle); void/cancel only if product allows |
| **History (closed tab / panel)** | Settled contracts | **Rework list row** | Win/loss badge, payout or −stake, settlement digit — not a continuous PnL line |
| **SessionStrip** | Redefine metrics | **Rework** | Drop “Open P/L” as primary. Prefer: session settled P/L (sum of digit pnls since load or since UTC day), W/L counts, open (pending) count. Mirror benchmark session line where it fits |
| **Trade summary API** | Digit-aware summary or parallel | **New or branched** | CFD `open_pnl` marking all opens is wrong for binaries |
| **Signals panel + strip** | **Hide when flag on** | Keep code | EMA/RSI entry/SL/TP signals target the CFD ticket (`signalHref`). They do not select digits. Showing them would deep-link into a hidden CFD surface or a no-op |
| **Account / wallet / deposit / withdraw / auth** | Unchanged | **Reuse as-is** | Ledger types add rows; BalanceCard still valid |
| **`lib/pnl.ts` / `OrderTicket` CFD** | Unused in UI when flag on | **Keep in tree** | Flag off restores them |
| **Price / account WS** | Extend events | **Reuse + extend** | Need settlement push so Positions updates without 15s poll alone |

---

## 6. What gets hidden (flag ON) — and confirmation nothing is deleted

UI **hidden or not mounted**, code **retained**:

| UI / entry | Hidden when digit ON? | Still in codebase? |
|---|---|---|
| CFD `TradeScreen` composition (Buy/Sell ticket, SL/TP, projected move copy) | Yes — not rendered | Yes |
| `OrderTicket.tsx` (CFD) | Yes | Yes |
| Latest-signal strip on the surface | Yes (with Signals) | Yes |
| Signals panel content / top-bar & bottom-bar Signals toggle | **Recommend yes** | Yes — panel registry entry can remain but `open('signals')` no-ops or tab removed from `PANEL_ITEMS` when flag on |
| Continuous open P/L on position cards | Yes (cards replaced) | Old card components kept or behind flag branch |
| Manual “Close position” as the normal end of a trade | Yes for digit opens | CFD close path kept |
| Routes `/trade`, `/portfolio`, … | Still redirects; surface is digit | Unchanged redirects |

**Not hidden:** TopBar (minus Signals if hidden), Deposit, account pill, TabBar Positions/History/Account (and Signals if kept), drawer Deposit/Withdraw/Log out, History/Account panels, chart, symbol picker.

**Explicit non-goals:** no `git rm` of Pass 1/migration trading files in this feature’s delivery phases.

---

## 7. Phased plan

Scoped like the one-screen migration: each phase shippable and reviewable.

### Phase 0 — Rules & flag plumbing

- Document last-digit extraction; payout table stub (config); win credit formula agreed with client (or clearly marked TBD with safe defaults for demo only).
- `app_settings` + env bootstrap + API exposure of the flag.
- Web: branch `/` on flag (digit shell stub vs existing `TradeScreen`) without new trading yet — proves flip without redeploy.

**Exit:** Flag flips surface placeholder; CFD still works when off.

### Phase 1 — Backend digit contracts vertical slice

- Migration: `digit_contracts` + ledger types.
- Open + tick settler + sweep + idempotent settle.
- WS event on settle; list open/closed; summary for session.
- Integration tests: win, loss, double-settle race, sweep after missed tick.

**Exit:** API-only round trip on mock feed; no polished UI required.

### Phase 2 — Digit trading surface

- Contract tabs, digit row, new ticket (manual only), wire to Phase 1 APIs.
- Chart reuse; hide CFD ticket and signal strip when flag on.
- Mobile + desktop layouts inside existing one-screen grid (panel column unchanged).

**Exit:** User can open Even/Odd (and stubs or full three types) and see settlement.

### Phase 3 — Positions, History, SessionStrip

- Panel content for pending/settled contracts; session metrics redefined.
- Hide or disable Signals in chrome when flag on.
- E2E: open → settle → balance + history row.

**Exit:** Panels match the new product meaning.

### Phase 4 — Hardening & ops

- Reject or no-op stale CFD opens when flag on (optional).
- Admin visibility of open digit contracts / void tool if product wants it.
- Payout table editable without code change (config/DB).
- Load/fail tests on settler; document silent-fail runbook.

**Exit:** Client can flip flag and operate either product with known ops procedures.

*(Phase 5+ only if needed: barrier UX polish for Over/Under, longer digit history, staff allowlist.)*

---

## 8. Effort estimate

| Piece | Effort | Notes |
|---|---|---|
| Feature flag (DB + API + web branch) | **Low–medium** | Small; unlocks safe rollback |
| Parallel schema + open/debit | **Medium** | Copy patterns from `trading_service` carefully |
| Tick settler + sweep + idempotency | **High** | Highest risk; needs concurrency tests |
| Static payout table + snapshot | **Low** | Once formula is decided |
| Digit row (client histogram) | **Low** | |
| New order ticket + contract tabs | **High** | Most of the visible UI |
| Chart reuse + minor expiry cue | **Low** | |
| Positions / History / SessionStrip rework | **Medium–high** | New cards and summary semantics |
| Signals hide + deep-link cleanup | **Low** | |
| E2E + settlement probes | **Medium** | |
| **Overall** | **High** | Migration-class, not polish-class |

---

## 9. Open questions for the client

These are product decisions; engineering should not invent them silently.

1. **Win credit formula and payout table**  
   Confirm: on win, is credit `stake × (1 + ratio)` (benchmark-shaped $19.52 on $10 at 95.20%)? Provide ratios for Even, Odd, Match, Differ, Over, Under (per barrier if needed). Are ratios identical on all symbols?

2. **Expiry**  
   Confirm v1 = **next tick only**, or selectable 1/2/5 ticks? Any wall-clock contracts?

3. **Symbol eligibility**  
   All symbols, or volatility indices only (cleaner last-digit story than FX)?

4. **Stuck contracts**  
   If the feed dies mid-contract: auto-**void** (refund stake), leave **open** until ticks resume, or force settle against last tick?

5. **Signals**  
   Confirm hide when digit mode is on (recommended), vs keep as unrelated education.

6. **TARGET / STOP LOSS / MULT**  
   Confirm omit entirely for v1 (recommended), even as disabled UI.

7. **Stake vs Payout toggle**  
   Benchmark lets you enter desired payout and backs into stake. Needed in v1, or stake-only?

8. **Flag ownership**  
   Who may flip the runtime flag — eng via SQL only, or a button in the admin console?

9. **Demo economics**  
   Same payout table on demo as real, or more generous demo table?

10. **Regulatory / copy**  
    Digit-row percentages: label as “recent frequency” so they are not read as the contract’s odds (recommended if the row ships beside fixed payouts).

---

## Appendix — Alignment with prior reports (no contradictions)

| Prior finding | This plan |
|---|---|
| No fixed payout / no expiry / proportional `pnl.go` | Remains true of the **CFD** product; digit contracts are a **second** product behind a flag |
| Contract tabs / digit row / AUTO out of scope in migration | Digit tabs + row **in scope now**; AUTO still out |
| One-screen shell, panels, `?panel=` | Reused; not rebuilt |
| 600-tick in-memory buffer | Enough for digit **UI** stats; not a pricing oracle; not durable across restart |
| Shell-owned price WS | Digit settlement hooks into the same feed path CFD SL/TP already uses |
| Post-migration: 15s trades poll | Digit mode should prefer WS settlement events; don’t copy CFD open_pnl polling semantics blindly |

---

*No code changes in this pass. Implementation should start only after section 9 answers (especially payout formula and stuck-contract policy) are locked.*
