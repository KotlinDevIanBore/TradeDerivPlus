# TradeDerivPlus

A derivatives trading app for Deriv synthetic indices and forex, with M-Pesa
deposits, an EMA/RSI signal bot, and an operations console for support.

Go + Gin + PostgreSQL on the back, React + TypeScript + Tailwind on the front,
WebSockets for live prices, signal pushes and balance updates.

## Running it

Everything runs offline by default. The market feed generates a local random
walk per instrument and the M-Pesa lifecycle is simulated, so you need no Deriv
or Safaricom credentials to develop against the full stack.

```bash
# 1. Database
docker-compose up -d

# 2. Configuration
cp .env.example .env
# Generate a secret and paste it into JWT_SECRET:
openssl rand -base64 48

# 3. API (applies migrations on boot, seeds instruments)
go run ./cmd/api

# 4. Development fixtures: the admin account and a few KYC submissions.
#    This refuses to run unless APP_ENV=development.
go run ./cmd/seed

# 5. Web app, in another terminal
cd web && npm install && npm run dev
```

The API listens on `:8080`, the web app on `:5173`. The trading app is at
`/` and the operations console at `/admin`.

The seeded console credential is `admin` / `admin123`, and the app nags until
it is changed. Outside development that credential cannot open a session at
all, and the seed command refuses to run, so it can exist in development but
never in production.

### Observability, optional

```bash
docker-compose --profile observability up -d
```

Prometheus on `:9090`, Grafana on `:3000` (`admin` / `admin`) with two
provisioned dashboards. Prometheus scrapes `/metrics` with the bearer token in
`deploy/prometheus/metrics_token`, which must match `METRICS_TOKEN` in `.env`.

## Verifying it

```bash
go test ./...                    # money and indicator logic
python3 scripts/smoke.py         # full path: register, trade, close, deposit
go run ./scripts/wscheck         # the three WebSocket endpoints
```

`scripts/smoke.py` asserts the wallet and the ledger agree at every step, that
one user cannot touch another's money, and that a simulated M-Pesa deposit
settles and credits the right balance.

## Going live

Flip these in `.env`:

| Variable | Effect |
|---|---|
| `USE_MOCK_FEED=false` | Connects to the real Deriv WebSocket feed |
| `USE_MOCK_MPESA=false` | Uses Daraja; requires consumer key, secret and passkey |
| `APP_ENV=production` | Release-mode Gin, info-level logs, refuses to boot without `JWT_SECRET` |

`MPESA_CALLBACK_URL` must be publicly reachable for real deposits to settle.

## Layout

```
cmd/api            entrypoint: config, wiring, graceful shutdown
internal/
  config           the only place that reads the environment
  models           domain types, and the single PnL definition
  repository       one file per aggregate, plus a transactional Store
  service          business logic; never imports gin
  handlers         thin: parse, call a service, format
  routes           wiring only
  middleware       auth, logging, rate limiting
pkg/
  derivclient      raw Deriv WebSocket protocol, plus a mock generator
  mpesaclient      raw Daraja HTTP protocol
  ws               generic topic-based broadcast hub
migrations         applied automatically on boot, in filename order
web/src
  api              one typed client per domain; money crosses the wire as strings
  features         one directory per screen area, each with its own container
  components       shared composites (PriceTag, Money, StatusBadge) + ui primitives
  hooks            React Query hooks and the three WebSocket streams
  store            Zustand: session and account context, live prices
  lib              formatters, the PnL mirror, the reconnecting socket
  types            mirrors the Go models
scripts            smoke tests and the PnL parity check
docs               spec, API contract, UI/UX plan
```

Two rules hold the layers apart: handlers never import `repository`, and
services never import `gin`.

## Auth

The access token lives in memory only. The refresh token is an httpOnly,
Secure, SameSite=Strict cookie the browser holds and sends on its own, so no
long-lived credential is reachable from JavaScript. A page reload trades the
cookie for a fresh access token before the router runs, which is why a reload
does not sign you out.

The console has its own cookie at its own path, so the two sessions never see
each other, and the tokens carry a scope claim that each middleware requires,
so neither opens the other's routes.

> **Deploying this.** `SameSite=Strict` means the cookie only travels when the
> request's site matches the cookie's. Locally the web app on `:5173` and the
> API on `:8080` are the same site, because the port is not part of one. In
> production they must share a registrable domain or sit behind one reverse
> proxy, or the cookie is never sent and every session dies at the access
> token's TTL. Do not reach for `SameSite=None` to fix that; it reopens the
> CSRF exposure the cookie exists to close.

## The operations console

At `/admin`, with its own login. Desktop-first and table-dense, because an
admin sits at a desk.

- **Every action that touches money or account state demands a written reason**
  and a confirmation that restates exactly what will happen. Both land in an
  append-only audit log alongside the change, in the same transaction, so an
  action cannot commit without its record.
- **Freezing** stops an account moving on its own. Closing an already-open
  position still works, which is what the confirmation promises, and only new
  exposure is blocked. The check runs inside the transaction that locks the
  balance, so a freeze committed a millisecond earlier is seen by a trade
  already in flight.
- **Adjustments are ordinary ledger rows**, so the ledger stays the single
  source of truth and there is no side channel that edits a balance. They are
  deliberately *not* blocked by a freeze: being unable to correct the very
  balance that caused a freeze would be a dead end for support. A test pins
  that decision so it is not "fixed" for consistency later.
- **The dashboard leads with ledger reconciliation.** Zero drifted accounts is
  the healthy state; anything else turns red and pins above everything else.

## Money handling

- `transactions` is an append-only ledger and the source of truth. `wallets` is
  a cache for fast balance reads.
- Every balance change writes the ledger row and updates the cache in one
  database transaction. Balances are read `FOR UPDATE` first, so two concurrent
  trades cannot both spend the same funds.
- Amounts cross the wire as strings, never JSON numbers, so nothing is lost to
  float parsing.
- Stakes are debited from bonus balance first, and a close credits whichever
  balance funded the open. Demo money can never become withdrawable.
- Entry and exit prices come from the server's live feed, never from the client.
- **Reconciliation has one definition**, in `internal/repository/reconciliation.go`,
  shared by the tests, the admin dashboard and the Prometheus gauge. A pending
  deposit has not moved money; a pending withdrawal has, because it debits up
  front. Getting that asymmetry wrong would make the drift alert cry wolf on
  every in-flight withdrawal until someone muted it.

## The frontend

Mobile-first and dark-only, matching [the UI/UX plan](docs/ui-ux-plan.md). Twelve
screens and flows: login, register, home, trade, portfolio, wallet, the four-step
M-Pesa deposit, withdraw, signals, profile, the account switcher, and the app
shell that owns account context and the live sockets.

Three rules the code holds to:

- **Components are pure.** A feature container talks to React Query and passes
  data and callbacks down. `OrderTicket` never fetches anything.
- **Colour never carries meaning alone.** Direction shows BUY or SELL as words,
  profit and loss always carry a sign, and balances live in `aria-live` regions.
- **Prices never reflow.** Tabular numerals plus a precision fixed by each
  instrument's pip size, so a tick changes the digits and nothing else.

The charting library is loaded only on the Trade screen, which keeps the other
screens roughly 100 kB lighter over the wire.

## Documentation

| File | What it is |
|---|---|
| [docs/implementation-report.md](docs/implementation-report.md) | Round 1: the trading app, how it was verified |
| [docs/round-2-report.md](docs/round-2-report.md) | Round 2: cookie auth, the console, observability, testing |
| [docs/spec.md](docs/spec.md) | The technical spec, v2 |
| [docs/round-2-investigation-report.md](docs/round-2-investigation-report.md) | Single consolidated report of the round-two investigation and plan |
| [docs/round-2-plan.md](docs/round-2-plan.md) | Round-two implementation plan: cookie auth, admin console, observability, testing |
| [docs/api-contract.md](docs/api-contract.md) | The API as built, including the ten gap resolutions |
| [docs/ui-ux-plan.md](docs/ui-ux-plan.md) | Screen-by-screen UI/UX plan |
