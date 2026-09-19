# TradeDerivPlus — Technical Spec (v2)

Shared reference for planning (Fable) and implementation (Opus). Both should treat this
document as the source of truth — extend it, don't fork it.

> **Status note.** v2 absorbs the ten API gap resolutions from round one and adds four
> areas: cookie-based refresh tokens, an admin console, observability, and a testing
> strategy. [api-contract.md](api-contract.md) describes the API **as built after round
> one**; [round-2-plan.md](round-2-plan.md) is the plan for closing the distance between
> that and this document. Where they disagree, this document wins.

## Tech stack

| Layer | Choice | Why |
|---|---|---|
| Frontend | React + TypeScript + Tailwind + shadcn/ui | Full design control, no fighting a heavy component library |
| State | Zustand (client) + React Query (server state) | Simple, avoids Redux boilerplate; React Query handles caching/retries for price feeds |
| Backend | Go + Gin | Fast, explicit, easy to layer cleanly |
| DB | PostgreSQL | Relational integrity matters a lot for money |
| Realtime | WebSocket (gorilla/websocket) | Live prices, signal pushes, balance updates |
| Auth | JWT (access + refresh) | Stateless, works well with mobile clients later |

---

## Database schema (PostgreSQL)

```sql
-- Users
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone_number VARCHAR(15) UNIQUE NOT NULL,
    email VARCHAR(255) UNIQUE,
    password_hash TEXT NOT NULL,
    full_name VARCHAR(255),
    kyc_status VARCHAR(20) DEFAULT 'pending', -- pending | verified | rejected
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

-- Trading accounts (a user can have Demo + Real, matches the screenshot)
CREATE TABLE accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    account_number VARCHAR(20) UNIQUE NOT NULL, -- e.g. 900001
    account_type VARCHAR(10) NOT NULL, -- demo | real
    currency VARCHAR(5) DEFAULT 'USD',
    created_at TIMESTAMPTZ DEFAULT now()
);

-- Wallet cache (fast balance reads; source of truth is `transactions`)
CREATE TABLE wallets (
    account_id UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    real_balance NUMERIC(18,2) DEFAULT 0,
    bonus_balance NUMERIC(18,2) DEFAULT 0,
    updated_at TIMESTAMPTZ DEFAULT now()
);

-- Append-only ledger — never mutate, only insert
CREATE TABLE transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID REFERENCES accounts(id),
    type VARCHAR(20) NOT NULL, -- deposit | withdrawal | trade_open | trade_close | bonus_credit
    balance_type VARCHAR(10) NOT NULL, -- real | bonus
    amount NUMERIC(18,2) NOT NULL,
    status VARCHAR(20) DEFAULT 'pending', -- pending | completed | failed
    reference VARCHAR(100), -- e.g. Daraja CheckoutRequestID
    metadata JSONB,
    created_at TIMESTAMPTZ DEFAULT now()
);

-- M-Pesa deposit requests (STK push lifecycle)
CREATE TABLE mpesa_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID REFERENCES transactions(id),
    checkout_request_id VARCHAR(100) UNIQUE,
    merchant_request_id VARCHAR(100),
    phone_number VARCHAR(15),
    amount NUMERIC(18,2),
    status VARCHAR(20) DEFAULT 'pending',
    result_code INT,
    result_desc TEXT,
    created_at TIMESTAMPTZ DEFAULT now()
);

-- Symbols available to trade (Volatility indices, Boom/Crash, Forex)
CREATE TABLE symbols (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(20) UNIQUE NOT NULL, -- e.g. 'R_75', 'BOOM500', 'EURUSD'
    display_name VARCHAR(100),
    category VARCHAR(20), -- volatility | boom_crash | forex
    is_active BOOLEAN DEFAULT true
);

-- Open/closed positions
CREATE TABLE trades (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID REFERENCES accounts(id),
    symbol_id UUID REFERENCES symbols(id),
    direction VARCHAR(4) NOT NULL, -- buy | sell
    entry_price NUMERIC(18,5) NOT NULL,
    exit_price NUMERIC(18,5),
    stake NUMERIC(18,2) NOT NULL,
    stop_loss NUMERIC(18,5),
    take_profit NUMERIC(18,5),
    status VARCHAR(10) DEFAULT 'open', -- open | closed
    pnl NUMERIC(18,2),
    opened_at TIMESTAMPTZ DEFAULT now(),
    closed_at TIMESTAMPTZ
);

-- PnL formula (must match on both sides — see note below trades table)
-- For a still-open trade, the client estimates live PnL the same way the
-- backend computes it on close, so the number never jumps at close time:
--   buy:  pnl = (current_price - entry_price) * stake / entry_price
--   sell: pnl = (entry_price - current_price) * stake / entry_price
-- This is a linear-multiplier model, NOT how Deriv itself prices Boom/Crash
-- or Volatility contracts (those have per-contract payout curves). Since
-- this platform is its own broker with its own ledger, it defines its own
-- payout rule — the formula above is that rule. trading_service.go is the
-- single source of truth; the frontend's usePriceStream-based PnL preview
-- must import/mirror this exact formula, not approximate it.

-- Signal bot output
CREATE TABLE signals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    symbol_id UUID REFERENCES symbols(id),
    direction VARCHAR(4) NOT NULL, -- buy | sell
    confidence NUMERIC(5,2), -- 0-100
    entry_price NUMERIC(18,5),
    stop_loss NUMERIC(18,5),
    take_profit NUMERIC(18,5),
    strategy VARCHAR(50), -- e.g. 'ema_rsi'
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_transactions_account ON transactions(account_id);
CREATE INDEX idx_trades_account_status ON trades(account_id, status);
CREATE INDEX idx_signals_symbol_created ON signals(symbol_id, created_at DESC);
```

### Implementation deltas (carried in `migrations/`)

- `symbols.pip_size INT` — display precision per instrument. The server rounds ticks to it
  and the client formats from it.
- `account_number_seq` — sequence starting at 900001.

---

## API contract (v1)

```
Auth
POST   /api/v1/auth/register            -- also creates the user's demo account (see note)
POST   /api/v1/auth/login
POST   /api/v1/auth/refresh
GET    /api/v1/auth/me                  -- current user: id, phone, email, full_name, kyc_status

Accounts
GET    /api/v1/accounts                 -- list user's accounts (demo + real)
POST   /api/v1/accounts                 -- create a new account
GET    /api/v1/accounts/:id/wallet      -- balance

Deposits / Withdrawals
POST   /api/v1/wallet/deposit                    -- triggers STK push
POST   /api/v1/wallet/withdraw
GET    /api/v1/wallet/transactions               -- paginated: ?page=&limit=&type=
GET    /api/v1/wallet/transactions/:id           -- single transaction, for deposit-pending polling
POST   /api/v1/mpesa/callback                    -- Daraja calls this (public, verified)

Market data
GET    /api/v1/symbols
WS     /api/v1/ws/prices?symbols=R_75,BOOM500   -- live tick stream

Trading
POST   /api/v1/trades                   -- open position
POST   /api/v1/trades/:id/close
GET    /api/v1/trades?status=open&page=&limit=

Signals
GET    /api/v1/signals?symbol=R_75&limit=10      -- omit symbol for newest-across-all-symbols
WS     /api/v1/ws/signals                        -- push new signals live

Account/wallet events
WS     /api/v1/ws/account                        -- authenticated, per-user channel.
                                                     Pushes { type: 'wallet', account_id, real_balance, bonus_balance }
                                                     and { type: 'transaction', transaction } on any ledger change
                                                     (deposit confirmed, withdrawal processed, trade settled).
                                                     Frontend uses this to update balance/transaction views live,
                                                     with the existing polling (transactions/:id) as a fallback
                                                     if the socket is down.
```

**Register also creates a demo account.** `auth_service.Register` creates the user row, then
calls the same logic `account_service` uses for `POST /accounts` with `account_type: demo`,
and returns both the user and the demo account in the register response. The client should
not need a second round trip to get a usable account after signup.

**Token storage.** Access token lives in memory (Zustand, not persisted). Refresh token is
set as an httpOnly, secure, SameSite=Strict cookie by the backend on login/register/refresh
— the backend must support this (cookie-based refresh, not a token in the JSON body). This
avoids putting a long-lived credential in localStorage, which is the one auth decision worth
getting right before either side builds against it.

---

## Backend structure (Go + Gin)

```
/cmd/api/main.go
/internal
  /config
  /models          -- User, Account, Wallet, Transaction, Trade, Signal
  /repository       -- interfaces + Postgres impls, one file per aggregate
    user_repo.go
    account_repo.go
    wallet_repo.go
    trade_repo.go
    signal_repo.go
  /service           -- one purpose per file, no HTTP knowledge
    auth_service.go
    wallet_service.go     -- deposit/withdraw orchestration, ledger writes
    trading_service.go    -- open/close trades, PnL calc
    mpesa_service.go       -- Daraja client wrapper + callback handling
    deriv_feed_service.go  -- WS client, tick fan-out via channels
    signal_service.go      -- EMA/RSI strategy, scheduled runs
  /handlers          -- thin: parse request, call service, format response
  /routes
  /middleware         -- auth, logging, rate limit
  /pkg
    derivclient/      -- raw Deriv WS protocol
    mpesaclient/       -- raw Daraja HTTP protocol
    ws/                 -- generic hub/broadcast helper
/migrations
```

Rule: handlers never import `repository` directly, services never import `gin`. Each
service file does one job — `wallet_service.go` doesn't know how trades work, and vice
versa.

## Frontend structure (React + TS + Tailwind + shadcn/ui)

```
/src
  /api                -- one typed client file per domain, mirrors backend routes
    auth.ts  accounts.ts  wallet.ts  trades.ts  signals.ts
  /features
    /auth              components/ hooks/ types.ts
    /accounts           -- account switcher, balance card
    /wallet              -- deposit modal, transaction list
    /trading               -- chart, order form, open positions
    /signals                 -- signal bot card
  /components/ui       -- shadcn primitives (Button, Card, Dialog...)
  /components           -- shared composites (PriceTag, StatusBadge)
  /hooks                -- useAuth, usePriceStream, useSignals
  /store                -- Zustand: session, active account
  /lib                  -- formatters, validators
  /types                -- mirrors Go models
```

Component rule: one component = one visual responsibility. A `TradeForm` doesn't fetch
its own data — it receives props/callbacks from a feature-level container that talks to
`/api` and React Query. Keeps components pure and trivially testable.

---

## Admin console — schema additions

```sql
-- Admin auth is deliberately separate from the trading `users` table —
-- different login surface, different token scope, no phone/OTP overlap.
CREATE TABLE admin_users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(50) UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    role VARCHAR(20) DEFAULT 'admin', -- admin | support (room to split later)
    must_change_password BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT now()
);
-- Seed: username 'admin', password 'admin123', must_change_password = true.
-- The seed only runs when APP_ENV=development. In any other APP_ENV the
-- migration/seed step is skipped entirely, and the login handler refuses
-- any admin session where must_change_password is still true until a
-- POST /api/v1/admin/change-password call clears it — so the default
-- credential can exist in dev but can never be the credential in prod.

-- Every admin action that touches money or account state is logged here.
-- This table is what makes "full console" auditable instead of just powerful.
CREATE TABLE admin_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id UUID REFERENCES admin_users(id),
    action VARCHAR(50) NOT NULL, -- freeze_account | unfreeze_account | manual_adjustment | kyc_approve | kyc_reject
    target_type VARCHAR(20) NOT NULL, -- user | account | transaction
    target_id UUID NOT NULL,
    reason TEXT,
    metadata JSONB,
    created_at TIMESTAMPTZ DEFAULT now()
);

-- Account freeze state (blocks new deposits/trades, not existing ledger reads)
ALTER TABLE accounts ADD COLUMN status VARCHAR(20) DEFAULT 'active'; -- active | frozen
ALTER TABLE accounts ADD COLUMN frozen_reason TEXT;

-- KYC submissions — the piece "approve/reject" actually needs to operate on.
-- SCOPE DECISION: the user-facing submission flow (ID number + document upload) is
-- explicitly OUT of scope for this round. The table and admin approve/reject UI are
-- built now; rows are seeded manually (a seed script inserting a few pending
-- submissions with placeholder id_number/document_url) so the admin screens have
-- real data to act against. document_url should point at an obviously-fake path
-- (e.g. "PLACEHOLDER — no upload flow yet") so nobody mistakes it for a working link.
-- The actual submission screen (Profile → "Start verification") stays disabled with
-- its existing "Verification is not available yet" copy until a later round.
CREATE TABLE kyc_submissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id),
    id_number VARCHAR(30),
    document_url TEXT, -- object storage reference, not the file itself in Postgres
    status VARCHAR(20) DEFAULT 'pending', -- pending | approved | rejected
    reviewed_by UUID REFERENCES admin_users(id),
    review_reason TEXT,
    submitted_at TIMESTAMPTZ DEFAULT now(),
    reviewed_at TIMESTAMPTZ
);

-- Manual balance adjustments are a `transactions` row like any other, so the
-- ledger stays the single source of truth — never a side-channel balance edit.
-- type gains a new value: 'admin_adjustment', metadata carries { admin_id, reason }.
```

## Admin API contract

```
POST   /api/v1/admin/login                       -- separate from user auth
POST   /api/v1/admin/change-password             -- required before must_change_password clears

GET    /api/v1/admin/dashboard                   -- totals: users, deposits today, withdrawals today, open positions, active WS connections
GET    /api/v1/admin/users?search=&page=&limit=
GET    /api/v1/admin/users/:id                   -- accounts, wallets, recent transactions, KYC status
POST   /api/v1/admin/accounts/:id/freeze         -- { reason } -- writes audit log
POST   /api/v1/admin/accounts/:id/unfreeze
POST   /api/v1/admin/accounts/:id/adjust         -- { amount, balance_type, reason } -- writes transactions + audit log

GET    /api/v1/admin/transactions?search=&type=&status=&page=&limit=
GET    /api/v1/admin/kyc?status=pending
POST   /api/v1/admin/kyc/:id/approve
POST   /api/v1/admin/kyc/:id/reject              -- { reason }

GET    /api/v1/admin/audit-log?page=&limit=
```

A frozen account is checked at the same point balances are locked (`WithTx`), so a
freeze takes effect immediately even for a trade already in flight — the freeze check
and the balance debit happen inside the same transaction, not as a separate guard that
a race could slip past.

## Observability

**Error handling + Sentry**
- Backend: a recovery middleware wraps every request, capturing panics to Sentry with
  request path, method, and account_id (never phone numbers, amounts, or auth headers —
  scrub PII fields explicitly before capture). Business errors (the sentinel set in
  `internal/service/errors.go`) are captured with a tag distinguishing them from actual
  bugs, so Sentry's issue list isn't drowned in expected "insufficient balance" noise.
- Frontend: a root error boundary + Sentry's React SDK, source maps uploaded at build
  time so stack traces resolve to real file/line, not minified bundle offsets.

**Grafana + Prometheus**
- Add `prometheus` and `grafana` services to docker-compose.
- Instrument Gin with `prometheus/client_golang`: request duration histogram by route,
  error rate by status code, WebSocket connection gauge.
- Business dashboards, not just infra ones: deposit success/failure rate (surfaces a
  Daraja problem before users complain), open positions count, and a ledger
  reconciliation gauge — `SUM(transactions) - wallets.balance` per account, alerting if
  it's ever nonzero, which turns the `sltp_check.py` invariant into something watched
  continuously instead of checked once per run.

## Testing strategy

| Layer | Tool | Covers |
|---|---|---|
| Go unit | testify (existing) | PnL, indicators, phone normalisation — extend to freeze checks, audit log writes, admin auth |
| Go integration | testcontainers-go + real Postgres | The concurrency paths flagged as untested: two simultaneous closes on one position, a trade racing the stop-loss watcher, a freeze racing an in-flight trade |
| Frontend unit | Vitest + React Testing Library | Order ticket validation, deposit step transitions, PnL formula parity (already scripted, wire into CI) |
| E2E | Playwright | register → trade → close; deposit → confirm; admin freeze → user's trade attempt rejected; KYC submit → admin approve → withdrawal unlocked |
| CI gate | GitHub Actions | All of the above on every push; money-path packages (`wallet_service`, `trading_service`) held to a coverage threshold, not just "tests pass" |

The concurrency integration tests are the highest-priority addition — the row locks
are reasoned about in the implementation report but not yet exercised under actual
concurrent load, which is exactly the kind of bug that only shows up in production
under real traffic.

## Notes for the planning pass (Fable)

- Visual language to match the reference screenshot: near-black cards, one red accent
  (#E24B4A-ish), bold sans headings, generous whitespace.
- Key screens: Home (balance + accounts + signal card), Trade (chart + order ticket),
  Portfolio (open/closed positions), Deposit flow (phone number → STK push → pending →
  confirmed).
- Signal card needs: direction badge, confidence %, symbol, SL/TP, "updated Xm ago",
  CTA button — already well demonstrated by the reference image.
