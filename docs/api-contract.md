# TradeDerivPlus — API contract (v1, as built)

> **Round 2.** Refresh tokens moved to httpOnly cookies, pagination moved to
> `page`, the account socket became a per-user channel, and the admin console
> was added. Those sections are marked **(R2)**.

Base URL: `http://localhost:8080/api/v1`

This is the implemented contract. It extends the original spec with the ten gaps
the UI/UX planning pass identified. Extensions are marked **(G*n*)** and listed
again at the bottom.

Authentication is a bearer access token: `Authorization: Bearer <access_token>`.
WebSockets take the same token as a `?token=` query parameter, because a browser
cannot set headers on the handshake.

**Refresh tokens are httpOnly cookies, not response fields (R2).** `login`,
`register` and `refresh` set `tdp_refresh` with `HttpOnly; Secure;
SameSite=Strict; Path=/api/v1/auth`. No endpoint ever returns a refresh token
in its body and `/auth/refresh` ignores one in a request body. Every browser
call must use `credentials: 'include'` or the cookie never travels.

The admin console uses `tdp_admin_refresh` at `Path=/api/v1/admin`, so the two
sessions cannot see each other's credential.

> **Deployment constraint.** `SameSite=Strict` means the cookie is only sent
> when the request's site matches the cookie's. Locally the web app on `:5173`
> and the API on `:8080` share a site, because the port is not part of one. In
> production the two must share a registrable domain, or sit behind one reverse
> proxy, or the refresh cookie is never sent and every session dies at the
> access token's TTL. Relaxing to `SameSite=None` reopens the CSRF exposure the
> cookie exists to close; fix the domain instead.

**Token scopes (R2).** A user token carries `scp: "user"` and an admin token
`scp: "admin"`. Each middleware requires its own, so neither opens the other's
routes even though both are signed with the same secret.

## Error shape

Every non-2xx response is:

```json
{ "error": { "message": "Stop loss must be below the current price for a buy",
             "code": "validation_failed",
             "field": "stop_loss" } }
```

`message` is safe to show a user directly. `code` is for branching in the
client. `field` is present on validation errors so the UI can put the message
under the right input.

| code | status | meaning |
|---|---|---|
| `validation_failed` | 422 | A field is wrong; `field` says which |
| `invalid_credentials` | 401 | Wrong phone or password |
| `unauthorized` | 401 | Missing or expired access token |
| `invalid_token` | 401 | Refresh token rejected |
| `phone_taken` / `email_taken` | 409 | Already registered |
| `account_exists` | 409 | User already has this account type |
| `forbidden` | 403 | Resource belongs to another user |
| `kyc_required` | 403 | Withdrawal attempted before verification |
| `not_found` | 404 | No such record |
| `insufficient_funds` | 400 | Stake or withdrawal exceeds balance |
| `real_account_required` | 400 | Deposit attempted on a demo account |
| `demo_not_allowed` | 400 | Withdrawal attempted on a demo account |
| `market_closed` | 400 | Symbol is inactive |
| `no_price` | 503 | No live tick yet for this symbol |
| `trade_not_open` | 409 | Position already closed |
| `account_frozen` | 403 | The account is frozen; only closing a position and admin adjustments still work |
| `password_change_required` | 403 | The admin session must change the default password first |
| `admin_exists` | 409 | Admin username taken |
| `password_unchanged` | 422 | New admin password equals the current one or the seeded default |
| `already_reviewed` | 409 | KYC submission already approved or rejected |
| `rate_limited` | 429 | Too many requests |
| `internal` | 500 | Server fault; detail is logged, never returned |

## Money representation

All monetary and price values cross the wire as **strings**, not JSON numbers,
so no precision is lost to float parsing. The client parses them with
`decimal.js`. Wallets are denominated in USD; M-Pesa amounts are KES.

---

## Auth

### `POST /auth/register`
Creates the user, a demo account, its wallet, and credits the demo starting
balance as **bonus** so it can never be withdrawn. One call, one transaction
**(G1)**.

```json
// request
{ "full_name": "Ian Bore", "phone_number": "0712345678",
  "email": "ian@example.com", "password": "hunter2024" }

// 201
{ "user":    { "id": "...", "phone_number": "254712345678", "kyc_status": "pending", ... },
  "account": { "id": "...", "account_number": "900001", "account_type": "demo", "currency": "USD" },
  "tokens":  { "access_token": "...", "refresh_token": "...", "expires_in": 900 } }
```

Phone numbers are normalised to `2547XXXXXXXX` on the way in; `07…`, `+254…`,
`254…` and `7…` all work. Password must be at least 8 characters and contain a
digit.

### `POST /auth/login`
`{ "phone_number", "password" }` → same shape as register, without `account`.

A wrong password and an unregistered number return the identical error, and the
unregistered path still runs a bcrypt comparison, so response timing does not
reveal whether a number exists.

### `POST /auth/refresh` **(R2)**
Takes no body. Reads the `tdp_refresh` cookie and returns
`{ "access_token", "expires_in" }`, rotating the cookie. A rejected token is
cleared from the browser rather than left to fail again. Presenting an access
token is rejected: the two kinds are distinguished by a claim.

### `POST /auth/logout` **(R2)**
Clears the refresh cookie. Without it a client can drop its in-memory access
token while the browser keeps presenting a valid refresh credential.

### `GET /auth/me` **(G9)**
Returns the authenticated user. The client needs `phone_number` to prefill the
deposit sheet and `kyc_status` to gate the withdraw button.

---

## Accounts

### `GET /accounts`
`{ "items": [ Account, ... ] }` — the user's demo and real accounts.

### `POST /accounts`
`{ "account_type": "real" | "demo" }` → 201 `Account`. One of each per user; a
second returns `account_exists`.

### `GET /accounts/:id/wallet`
```json
{ "account_id": "...", "real_balance": "100.00", "bonus_balance": "9875.00",
  "total": "9975.00", "updated_at": "..." }
```
`total` is precomputed so the hero balance needs no client-side arithmetic.

---

## Wallet

### `POST /wallet/deposit`
Real accounts only. Writes a **pending** ledger row and fires the STK push. No
money moves until the Daraja callback lands.

```json
// request
{ "account_id": "...", "amount_kes": "12950", "phone_number": "0712345678" }

// 202
{ "transaction_id": "...", "checkout_request_id": "ws_CO_...",
  "amount_kes": "12950", "amount_usd": "100", "status": "pending" }
```

### `POST /wallet/withdraw`
Real accounts only, and only after KYC verification. Debits immediately so the
same balance cannot be spent twice while a payout is in flight. Bonus balance is
never withdrawable.

`{ "account_id", "amount_usd", "phone_number" }` → 202 `Transaction`.

### `GET /wallet/transactions` **(G7, R2)**
Query: `account_id` (required), `page` (1-based, default 1), `limit` (default
25, max 100), and `type` which may repeat (`?type=deposit&type=withdrawal`).

`offset` is still accepted for one release so a client mid-rollout keeps
working; `page` wins when both are present.

```json
{ "items": [ Transaction, ... ], "total": 42, "limit": 25,
  "offset": 0, "page": 1, "pages": 2 }
```

### `GET /wallet/transactions/:id` **(G10)**
One transaction. This is the deposit screen's polling fallback when the account
socket is unavailable.

### `GET /wallet/rate` **(G8)**
`{ "kes_per_usd": 129.5 }` — drives the deposit sheet's "≈ $x USD" hint.

### `POST /mpesa/callback`
Public; Daraja calls it. Verified by looking the `CheckoutRequestID` up against
a request we actually made. Idempotent, and always answers 200 so Safaricom
stops retrying.

---

## Market

### `GET /symbols`
Every instrument, each with its latest price attached so the symbol picker can
show prices without opening N subscriptions.

```json
{ "items": [ { "id": "...", "code": "R_75", "display_name": "Volatility 75 Index",
               "category": "volatility", "pip_size": 4, "is_active": true,
               "price": "4685.559", "epoch": 1789405449 } ] }
```

`category` is `volatility` | `boom_crash` | `forex`. `pip_size` is the decimal
precision for display; prices are already rounded to it server-side.

### `GET /symbols/:code/ticks?minutes=5` **(G5)**
Buffered tick history so a chart draws immediately instead of waiting for live
ticks. `minutes` is 1–30, default 5. The server keeps the last 600 ticks per
symbol in memory.

`{ "symbol": "R_75", "items": [ { "price": "4685.559", "epoch": 1789405449 } ] }`

### `GET /signals` **(G3)**
Query: `symbol` (optional — **omit it for newest across all symbols**, which is
what the Home card needs), `limit` (default 20, max 100).

```json
{ "items": [ { "id": "...", "direction": "sell", "confidence": "68.40",
               "entry_price": "1.08460", "stop_loss": "1.08525", "take_profit": "1.08362",
               "strategy": "ema_rsi", "created_at": "...",
               "symbol_code": "EURUSD", "symbol_name": "EUR/USD", "pip_size": 5 } ] }
```

---

## Trading

### `POST /trades`
Entry price is taken from the live feed **on the server**, never from the
client, so a stale browser tab cannot open at an old price.

```json
{ "account_id": "...", "symbol": "R_75", "direction": "buy",
  "stake": "50", "stop_loss": "4600.0000", "take_profit": "4800.0000" }
```

`stop_loss` and `take_profit` are optional. A stop loss must sit on the losing
side of entry and a take profit on the winning side, or the request is rejected
with the offending `field`.

### `POST /trades/:id/close`
Closes at the current server price. Returns the closed trade with `exit_price`
and `pnl`. Closing an already-closed position returns `trade_not_open`.

### `GET /trades` **(G7)**
Query: `account_id` (required), `status` (`open` | `closed`, omit for both),
`limit`, `offset`. Open positions come back newest-opened first, closed ones
newest-closed first.

Each trade carries `symbol_code`, `symbol_name` and `pip_size` denormalised, so
the UI never joins client-side.

### `GET /trades/summary`
Backs the Portfolio summary bar in one call.

```json
{ "open_pnl": "-3.20", "realised_today": "18.44",
  "open_positions": 2, "prices_complete": true }
```

`prices_complete` is false when some open position's symbol has no live price
yet, which is the UI's cue to show the "stale" treatment.

---

## WebSockets

All three take `?token=<access_token>`. Every message is
`{ "type": string, "payload": object }`.

### `WS /ws/prices?symbols=R_75,BOOM500`
Pushes `{"type":"tick","payload":{"symbol","price","epoch"}}`. The last known
price for each requested symbol is sent immediately on connect.

Clients may change the set mid-connection without reconnecting:
```json
{ "action": "subscribe",   "symbols": ["EURUSD"] }
{ "action": "unsubscribe", "symbols": ["R_75"] }
```

### `WS /ws/signals`
Pushes `{"type":"signal","payload":Signal}` as the bot publishes.

### `WS /ws/account` **(G2, R2)**
The authenticated user's channel. **There is no `account_id` parameter**: the
channel is the caller's own, so there is nothing to forge, and one socket
serves every account they own. Switching between demo and real no longer
reconnects. Each payload names the account it concerns.

Pushes:

| type | payload | fires when |
|---|---|---|
| `wallet` | `Wallet` | any balance change |
| `transaction` | `{transaction_id, status, type, amount?, reason?, receipt?}` | a deposit or withdrawal changes state |
| `trade_opened` | `Trade` | a position opens |
| `trade_closed` | `{trade, reason}` | a position closes; `reason` is `manual`, `stop_loss` or `take_profit` |
| `kyc` **(R2)** | `{user_id, kyc_status}` | an admin approves or rejects verification, so the Withdraw button unlocks at once rather than after the profile's stale time |
| `account` **(R2)** | `{account_id, status, frozen_reason?}` | an admin freezes or unfreezes an account |

This is what lets the deposit pending screen resolve without polling, and what
tells the UI a position was auto-closed while the user was on another screen.

---

## Profit and loss **(G6)**

One definition, mirrored exactly in `internal/models/pnl.go` and
`web/src/lib/pnl.ts`:

```
buy:  pnl = stake * (current - entry) / entry
sell: pnl = stake * (entry - current) / entry
```

Rounded to 2 decimal places. A position can lose its stake but never more: the
close credits `max(0, stake + pnl)`.

The client computes this live for open positions; the server computes the same
thing on close. If the formula changes, change both in the same commit.

## Which balance funds a trade

Stakes are taken from **bonus balance first**, then real. The close credits the
same balance the open debited, so demo and promotional money can never turn
into withdrawable real money.

---

## Rate limits

| Routes | Limit |
|---|---|
| `/auth/*` | 1 per 3s, burst 10, per IP |
| `/wallet/deposit`, `/wallet/withdraw` | 1 per 2s, burst 5, per user |
| Everything else authenticated | 20/s, burst 60, per user |

Exceeding one returns 429 with code `rate_limited`.

---

## Gap resolutions at a glance

| # | Gap from the UI/UX plan | Resolution |
|---|---|---|
| G1 | Demo account on signup | `POST /auth/register` creates it and returns it |
| G2 | Live balance / transaction status | `WS /ws/account` |
| G3 | Latest signal across symbols | `GET /signals` with no `symbol` |
| G4 | KYC | `kyc_status` on `GET /auth/me`; withdrawal gated with `kyc_required`. No verification endpoint yet — the UI shows status and a placeholder CTA |
| G5 | Chart history | `GET /symbols/:code/ticks?minutes=` |
| G6 | PnL formula | Defined above; shared by `models/pnl.go` and `lib/pnl.ts` |
| G7 | Pagination | `limit`/`offset` on `/trades` and `/wallet/transactions`, plus repeating `type` filter |
| G8 | KES↔USD rate | `GET /wallet/rate`, and the deposit response returns both amounts |
| G9 | Current user | `GET /auth/me` |
| G10 | Single transaction status | `GET /wallet/transactions/:id` |
