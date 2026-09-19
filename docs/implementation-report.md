# TradeDerivPlus — Implementation Report

**Status:** Complete and verified end to end.
**Last verified:** 17 September 2026, from a cold start (database container, API, and
frontend build all brought up fresh and re-checked).

This report covers what was built against [the technical spec](spec.md) and
[the UI/UX plan](ui-ux-plan.md), how it was verified, what was found and fixed
along the way, and what is deliberately unfinished.

---

## 1. Summary

A derivatives trading application for Deriv synthetic indices and forex, with
M-Pesa deposits and an EMA/RSI signal bot. Go and Gin on the back, React and
TypeScript on the front, PostgreSQL for storage, WebSockets for live prices,
signal pushes and balance updates.

The whole stack runs offline. The market feed has a mock that generates a
plausible random walk per instrument, including the rare upward and downward
spikes that characterise Boom and Crash. The M-Pesa lifecycle is simulated,
failing roughly one deposit in six so the failure path is exercisable. Neither
Deriv nor Safaricom credentials are needed to develop or demo.

### Size

| Layer | Files | Lines |
|---|---:|---:|
| `internal/service` (business logic) | 8 | 1,853 |
| `internal/repository` (data access) | 10 | 756 |
| `internal/handlers` (HTTP) | 7 | 745 |
| `pkg` (Deriv, Daraja, WebSocket hub) | 3 | 667 |
| `internal/models`, `config`, `routes`, `middleware`, `cmd` | 8 | 736 |
| Go tests | 4 | 245 |
| `web/src` (React app) | 73 | 5,989 |
| Migrations, verification scripts | 7 | 711 |

---

## 2. Architecture

The spec's two layering rules are held throughout, with no exceptions:

- **Handlers never import `repository`.** They parse a request, call one service
  method, and format the response.
- **Services never import `gin`.** A service's entire vocabulary for failure is
  the sentinel error set in `internal/service/errors.go`, which the handler layer
  maps to HTTP statuses in one place.

Each service file does one job. `wallet_service.go` does not know how trades
work; `trading_service.go` does not know how deposits work. Where they would
otherwise have to know about each other, an interface sits between them:

- `Broadcaster` is the slice of the WebSocket hub the services need, so no
  service imports the transport layer.
- `MpesaService` reports deposit outcomes through an injected `OnResult`
  callback, which `WalletService` supplies. The M-Pesa service therefore knows
  nothing about wallets, and the wallet service knows nothing about Daraja.
- `derivclient.Feed` is satisfied by both the live client and the mock, so the
  feed service never learns which one it has.

### Transactional store

`repository.Store` bundles every repository and exposes `WithTx`, which hands
the callback a transactional copy of all of them. Every balance change goes
through it, so the ledger insert and the wallet cache update commit together or
not at all. `DBTX` is satisfied by both the connection pool and a transaction,
so no repository method is written twice.

---

## 3. Money handling

This is the part that most needed to be right, so the invariants are stated
explicitly.

- **The ledger is the source of truth.** `transactions` is append-only. The only
  mutation is the pending to completed or failed transition on an M-Pesa deposit.
  `wallets` is a cache for fast balance reads and is always written in the same
  transaction as the ledger row.
- **Balances are locked before they are changed.** Every balance-changing path
  reads the wallet `FOR UPDATE` first, so two concurrent trades cannot both spend
  the same funds. `WalletRepo.Adjust` additionally refuses any update that would
  drive a balance negative, as a last line of defence behind the service checks.
- **Amounts cross the wire as strings, never JSON numbers.** The client parses
  them with `decimal.js`. Nothing is lost to float parsing in either direction.
- **Prices come from the server.** Entry and exit are taken from the live feed
  inside the service, never from the request body, so a stale browser tab cannot
  open or close at an old price.
- **Bonus money can never become withdrawable.** A stake is debited from bonus
  balance first, then real. The close credits whichever balance funded the open,
  recovered from the `trade_open` ledger row. Withdrawals draw on real balance
  only, and the withdraw sheet says so rather than silently capping the amount.
- **A position can lose its stake but never more.** The close credits
  `max(0, stake + pnl)`.
- **Withdrawals debit immediately.** The balance leaves the wallet when the
  payout is requested, so the same funds cannot be spent while it is in flight.
- **The M-Pesa callback is idempotent.** It is verified by looking the
  `CheckoutRequestID` up against a request we actually made, ignores a repeat for
  an already-settled request, and always answers 200 so Safaricom stops retrying.

### One definition of profit and loss

```
buy:  pnl = stake * (current - entry) / entry
sell: pnl = stake * (entry - current) / entry
```

Rounded to two decimal places. It lives in `internal/models/pnl.go` and is
mirrored in `web/src/lib/pnl.ts`. The client computes it live for open positions;
the server computes the same thing on close. **The two are checked against each
other** by `scripts/pnl_parity.mjs` (see section 6), so the live number a user
watches cannot drift from the realised number they are paid.

---

## 4. Closing the ten API gaps

The UI/UX planning pass identified ten places where the screens needed something
the original contract did not offer. All ten are closed. The full contract as
built is in [api-contract.md](api-contract.md); this is the summary.

| # | Gap | Resolution |
|---|---|---|
| G1 | Demo account on signup | `POST /auth/register` creates the user, a demo account, its wallet and the starting bonus in one transaction, and returns the account. The client needs no second call. |
| G2 | Live balance and transaction status | `WS /ws/account` pushes `wallet`, `transaction`, `trade_opened` and `trade_closed` events. This is what makes the deposit screen resolve without polling, and what tells the UI a position was auto-closed while the user was elsewhere. |
| G3 | Latest signal across symbols | `GET /signals` with no `symbol` returns the newest across all instruments. |
| G4 | KYC | `kyc_status` on `GET /auth/me`; withdrawals gated with a `kyc_required` error. No verification endpoint exists, so the UI shows status and a disabled call to action, as the plan specified. |
| G5 | Chart history | `GET /symbols/:code/ticks?minutes=` serves a 600-tick in-memory buffer per symbol, so a chart draws immediately rather than waiting for live ticks. |
| G6 | PnL formula | Defined once, mirrored in both languages, and verified against itself. |
| G7 | Pagination | `limit` and `offset` on `/trades` and `/wallet/transactions`, plus a repeating `type` filter for the wallet's segment control. |
| G8 | KES to USD rate | `GET /wallet/rate`, and the deposit response returns both amounts. |
| G9 | Current user | `GET /auth/me`, needed for the deposit phone prefill and the withdraw gate. |
| G10 | Single transaction status | `GET /wallet/transactions/:id`, the deposit screen's polling fallback when the account socket is unavailable. |

### Two schema additions

Both are carried in `migrations/` and noted in the spec:

- `symbols.pip_size` — display precision per instrument (forex 5, volatility and
  Boom/Crash 2 to 4). The server rounds ticks to it before storing or
  broadcasting, so no digit nobody trades on ever reaches the UI, and the client
  formats from the same number.
- `account_number_seq` — a sequence starting at 900001, so account numbers match
  the reference screenshot without application-level allocation.

---

## 5. The frontend

All twelve screens and flows from the plan, mobile-first and dark-only. The
design tokens were confirmed against computed styles in a live browser: accent
`rgb(226, 75, 74)`, surface `#141416`, page `#0A0A0B`.

Login, register, home, trade, portfolio, wallet, the four-step M-Pesa deposit,
withdraw, signals, profile, the account switcher, and the app shell that owns
account context and the live sockets.

Three rules the code holds to:

- **Components are pure.** A feature container talks to React Query and passes
  data and callbacks down. `OrderTicket` receives the price, the balance and a
  submit callback; it fetches nothing.
- **Colour never carries meaning alone.** Direction shows BUY or SELL as words as
  well as colour, profit and loss always carry a sign, tap targets are at least
  44px, and balances and PnL sit in `aria-live` regions.
- **Prices never reflow.** Tabular numerals plus a precision fixed by each
  instrument's pip size, so a tick changes the digits and nothing else moves.

### Notable behaviours

- **Stale prices are visible, not silent.** When the price socket drops, the
  chart freezes under an amber overlay, the order ticket disables itself, and
  open-position PnL dims with a "stale" tag. A trade is never sent on a price the
  user cannot see is old.
- **The connection indicator distinguishes idle from broken.** Home with no open
  positions has no reason to hold a price socket, so it reports idle and the top
  bar hides the dot, rather than showing a permanent amber "connecting".
- **Cards load independently.** One failing query never blanks a screen. A failed
  balance read shows a dash and a retry while the deposit button stays enabled,
  because depositing does not require knowing the balance.
- **Large stakes confirm.** On a real account, a stake at or above 20% of balance
  opens a confirmation restating the amount and the share. Demo never confirms.
- **The charting library is code-split**, loaded only on the Trade screen. That
  keeps every other screen roughly 100 kB lighter over the wire, which matters on
  a mobile connection.

### Bundle

| Chunk | Raw | Gzipped |
|---|---:|---:|
| Main | 511.7 kB | 162.1 kB |
| Chart (Trade screen only) | 346.9 kB | 101.8 kB |
| CSS | 23.5 kB | 5.6 kB |

---

## 6. Verification

Everything below passes from a cold start. With the API running:

```bash
go test ./...                     # money and indicator logic
go vet ./... && gofmt -l .        # clean
python3 scripts/smoke.py          # register, trade, close, deposit, authorisation
python3 scripts/sltp_check.py     # stop loss and take profit settle from the tick stream
go run ./scripts/wscheck          # the three WebSocket endpoints
node scripts/pnl_parity.mjs       # Go and TypeScript agree on every PnL vector
cd web && npx tsc --noEmit -p tsconfig.app.json && npm run build
```

| Check | Result | What it establishes |
|---|---|---|
| Go unit tests | pass | PnL across both directions, profit, loss, flat, zero entry and five-decimal forex moves; stop-loss and take-profit trigger boundaries; EMA, RSI and standard deviation bounds; phone normalisation for `07…`, `+254…`, `254…` and `7…` forms |
| `go vet`, `gofmt` | clean | — |
| `smoke.py`, 44 assertions | all pass | The wallet and the ledger agree at every step; one user cannot read another's wallet or close another's position; an unauthenticated request is refused; a simulated M-Pesa deposit settles and credits the right balance; KES converts to USD correctly; business rules are enforced (KYC gate on withdrawal, demo account refuses deposits, oversized stake refused, wrong-sided stop loss refused, duplicate phone refused, second account of a type refused, closing twice refused) |
| `sltp_check.py` | pass | Positions auto-close from the live tick stream without a client involved, and **the append-only ledger sums exactly to the cached wallet balance** |
| `wscheck` | pass | Prices stream, account events push on a trade, and a socket without a token is refused with a 401 before the upgrade |
| `pnl_parity.mjs`, 12 vectors | agree exactly | The client's live PnL and the server's realised PnL cannot drift. Vectors are generated by the Go implementation and replayed through the TypeScript formula, including a sub-pip move on a one-million stake and a price of 0.00001 |
| TypeScript typecheck, production build | clean | — |
| Browser walkthrough | no console errors | All screens on 375px and at desktop width |

The ledger reconciliation in `sltp_check.py` is the strongest single check: it
reconstructs the balance by summing every completed ledger row and asserts it
equals the wallet cache to the cent.

### Manual browser walkthrough

Driven end to end in a real browser at 375px and at desktop width: register,
land on the demo account with its starting bonus, read the latest signal, open a
position and watch the balance debit and the toast fire, see it in Portfolio with
live PnL, create a real account from the switcher, deposit KES 12,950 and watch
it settle to $100.00 live over the account socket, and browse signals and profile.

---

## 7. Defects found and fixed during verification

Six real bugs surfaced only by running the app, not by typechecking:

1. **Radix `Slot` crash on every `asChild` button.** The loading spinner added a
   second child, which `Slot` cannot merge. Blanked the Home screen entirely.
   Fixed by branching: with `asChild`, the caller's element passes through alone.
2. **Infinite render loop on the Trade screen.** The `useTickHistory` selector
   returned a fresh `[]` when a symbol had no ticks, giving
   `useSyncExternalStore` a new snapshot every render. Fixed with a stable
   module-level constant.
3. **Permanently amber connection indicator.** Home with no open positions
   subscribes to nothing, so the status never left "connecting" and implied a
   fault. Fixed by adding an explicit idle state.
4. **Duplicate React keys in the signals list.** A socket push prepended a signal
   a refetch had already collected. Fixed by deduplicating on id.
5. **Wallet total blanked for a frame on every balance push.** The socket sends
   real and bonus but not the derived total, and the cache write was putting
   `undefined` in its place. Fixed by recomputing the total on write.
6. **Tick prices streamed at full float precision** (`4687.0036635171045`).
   Fixed by rounding to each symbol's pip size on ingest, server-side.

---

## 8. Deliberately unfinished

Three things, all flagged rather than hidden:

- **Identity verification has no endpoint.** The profile and withdraw screens
  show KYC status and a disabled "Start verification" button with the text
  "Verification is not available yet". This is exactly what the UI/UX plan
  specified for gap G4. Withdrawals are correctly gated behind it, so this is the
  one thing blocking a real payout path.
- **The chart draws ticks, not candles.** There is no candle endpoint, so the
  range toggle slices the tick buffer rather than refetching. Adding OHLC later
  is a backend addition plus a chart swap; nothing else changes.
- **Refresh tokens sit in `localStorage`.** Moving them to an httpOnly cookie is
  a backend change. It should be done before handling real money.

Also worth knowing:

- The signal bot warms up for 45 seconds after boot before its first run, because
  the slow EMA needs history. Signals then publish every two minutes by default.
- The mock M-Pesa lifecycle fails about one deposit in six on purpose, cycling
  through cancellation, insufficient balance and timeout, so the failure UI gets
  exercised. Set `USE_MOCK_MPESA=false` for real Daraja.

---

## 9. Running it

```bash
docker-compose up -d              # Postgres on 5434
cp .env.example .env              # then paste in: openssl rand -base64 48
go run ./cmd/api                  # migrations apply on boot, instruments seed
cd web && npm install && npm run dev
```

API on `:8080`, web app on `:5173`. The database container uses port 5434 to
avoid colliding with a local Postgres or another project's container.

To go live, flip three values in `.env`: `USE_MOCK_FEED=false` for the real Deriv
feed, `USE_MOCK_MPESA=false` with Daraja credentials, and `APP_ENV=production`,
which switches Gin to release mode and refuses to boot without a `JWT_SECRET`.
`MPESA_CALLBACK_URL` must be publicly reachable for real deposits to settle.

---

## 10. Suggested next steps

In the order I would take them:

1. **Build the KYC flow.** It is the only thing blocking real withdrawals, and
   the UI is already shaped around it.
2. **Move the refresh token to an httpOnly cookie** before real money moves.
3. **Add a candle endpoint** and swap the chart, so the Trade screen reads like a
   trading screen rather than a tick plot.
4. **Exercise Daraja sandbox end to end**, including a genuine timeout and a
   genuine user cancellation, before trusting the mock's coverage of those paths.
5. **Add integration tests against a throwaway database** for the concurrency
   paths specifically: two simultaneous closes on one position, and a trade
   racing the stop-loss watcher. The row locks are there and reasoned about, but
   they are not yet covered by an automated test.
