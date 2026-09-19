# TradeDerivPlus — Round 2 implementation plan

**Input:** [spec.md](spec.md) v2 against the round-one build described in
[implementation-report.md](implementation-report.md).
**Companion:** [ui-ux-plan.md](ui-ux-plan.md) Part 2 holds the admin screens.
**Audience:** the implementing session. This document says what to change, in what
order, and how to prove each step; it does not contain code.

## Context

Round one delivered the user-facing app and closed the ten API gaps. Spec v2 absorbs
those and adds four areas. It also tightens three things round one built slightly
differently. This plan reconciles the contract first, because everything after builds
on it, then adds the new areas in the order that keeps the money path safest.

The single most important item is not a feature: it is the concurrency integration
tests. The row locks in the money path are reasoned about but never exercised under
real concurrent load. Everything else in this round adds new ways to move money
(admin adjustments, freezes racing trades), so those tests must exist before the new
paths do, not after.

---

## 1. Contract reconciliation (before anything else)

Three places where the build and spec v2 disagree. Spec wins.

| # | Built | Spec v2 | Change |
|---|---|---|---|
| R1 | `?limit=&offset=` on `/trades` and `/wallet/transactions` | `?page=&limit=` | Handlers accept `page` (1-based) and compute offset. Keep accepting `offset` for one release so the current client does not break mid-deploy; the client switches to `page`. Response shape gains `page` and `pages`. |
| R2 | `WS /ws/account?account_id=` — one socket per account | Per-user channel, payload carries `account_id` | Topic becomes `user:<user_id>`; the handler authorises on the token alone, no query param. Every push already carries `account_id` (wallet) or `transaction.account_id`, so the client routes by that. The client opens **one** socket in the shell instead of one per active account, and switching account no longer reconnects. |
| R3 | Access and refresh tokens both persisted in `localStorage` | Access in memory only; refresh in an httpOnly, Secure, SameSite=Strict cookie set by the backend | See section 2. |

Also tighten: `ws/account` should push a `kyc` event `{ user_id, kyc_status }` on
approve or reject, so the user's Withdraw button unlocks the moment an admin acts
rather than on the next 300-second `me` refetch. Small addition to G2, worth doing
while R2 is open.

**Verify:** `smoke.py` updated for `page`; `wscheck` opens the user socket with no
`account_id`, then opens a trade on each of two accounts and asserts both events
arrive on the single socket.

---

## 2. Cookie-based refresh (R3)

**Backend**
- `login`, `register`, `refresh` set `Set-Cookie: tdp_refresh=<jwt>; HttpOnly; Secure;
  SameSite=Strict; Path=/api/v1/auth; Max-Age=<refresh TTL>`. The JSON body carries
  only `access_token` and `expires_in`; `refresh_token` is removed from the response.
- `POST /auth/refresh` reads the cookie, not the body. A body token is ignored.
- `POST /auth/logout` (new) clears the cookie. Without it the client cannot end a
  session on the server side.
- CORS: `AllowCredentials: true` is already set. The origin list must be exact, not
  wildcard, for credentialed requests to work.
- Admin gets the same treatment with cookie name `tdp_admin_refresh` and
  `Path=/api/v1/admin`, so the two sessions never see each other's cookie.

**The gotcha, flagged now so it is not discovered in staging.** `SameSite=Strict`
means the cookie is only sent when the request origin matches the cookie's site. In
development the app is on `localhost:5173` and the API on `localhost:8080`; same
host, different port, and for cookie purposes the port is **not** part of the site,
so this works locally. In production the web app and API must be served from the same
registrable domain (`app.example.com` and `api.example.com` both count as
`example.com`) or the refresh cookie is never sent and every session dies after
fifteen minutes. Put the API behind the same domain or a reverse proxy path
(`/api` on the web origin). Do not "fix" it by dropping to `SameSite=None`; that
reopens the CSRF exposure the cookie was meant to close.

**Frontend**
- `store/session.ts`: `partialize` persists only `user` and `activeAccountId`.
  `accessToken` is memory-only. `refreshToken` field is deleted.
- `api/client.ts`: every request uses `credentials: 'include'`. On 401, call
  `/auth/refresh` with no body; the cookie does the work. The single-in-flight refresh
  logic stays as is.
- On app boot with no access token in memory, attempt one silent refresh before
  redirecting to login. A reload should not sign the user out.
- `useLogout` calls `POST /auth/logout` then clears the store.

**Verify:** log in, reload the page, still signed in. Wait past the access TTL (set it
to 10s in a test env), make a request, it succeeds transparently. Inspect
`document.cookie` in the console: the refresh token is not there. Log out, refresh
fails with 401.

---

## 3. Admin backend

Follows the same layering: new files, no new rules.

**Migration `0003_admin.up.sql`**
- `admin_users`, `admin_audit_log`, `kyc_submissions` as specified.
- `ALTER TABLE accounts ADD status, frozen_reason`.
- Index `admin_audit_log(target_type, target_id, created_at DESC)` for the per-user
  audit trail, and `kyc_submissions(status, submitted_at)` for the queue.
- **Seed is not a migration.** Migrations run in every environment. The admin seed
  and the placeholder KYC rows go in `cmd/seed` (or a `-seed` flag on the API) that
  refuses to run unless `APP_ENV=development`. This is how "the default credential can
  exist in dev but never in prod" is actually enforced, rather than hoped for.

**Models:** `AdminUser`, `AuditEntry`, `KYCSubmission`; `Account` gains `Status` and
`FrozenReason`; `TxAdminAdjustment = "admin_adjustment"` joins the transaction types;
the user-facing `transactionTitle` formatter learns to render it ("Balance adjustment").

**Repository:** `admin_repo.go` (admin users), `audit_repo.go`, `kyc_repo.go`.
`AccountRepo` gains `SetStatus` and `GetForUpdate`. The user, transaction and trade
repos gain the cross-account search queries the admin list screens need; keep them in
the existing files, they are the same aggregates.

**Service: `admin_service.go`** — one file, because every admin action is the same
shape: authorise, act, audit, in one transaction.
- `Login`, `ChangePassword` — bcrypt, same timing-safe miss as user login. Refuses a
  new password equal to the current one or to the seeded default.
- `Freeze(adminID, accountID, reason)` / `Unfreeze` — inside `WithTx`: lock the
  account row, set status, write the audit row. Returns the updated account.
- `Adjust(adminID, accountID, amount, balanceType, reason)` — inside `WithTx`: lock
  the wallet, insert the `admin_adjustment` ledger row with `metadata {admin_id,
  reason}`, `Wallets.Adjust`, write the audit row with the resulting `transaction_id`.
  The existing non-negative guard in `Wallets.Adjust` refuses an over-debit. Pushes
  `wallet` and `transaction` on the user's socket so their balance updates live.
- `ApproveKYC` / `RejectKYC` — inside `WithTx`: update the submission, update
  `users.kyc_status`, write the audit row. Pushes the new `kyc` event.
- `Dashboard()` — one query per number; the reconciliation gauge is a single SQL
  statement joining `SUM(amount) FILTER (WHERE status='completed')` per account against
  `wallets`, returning the count of mismatches and their ids.
- List and search methods for users, transactions, KYC, audit log, all paginated with
  `page`.

**The freeze check goes where the spec says: inside `WithTx`, at the point the balance
is locked.** `TradingService.Open` and `WalletService.Deposit` / `Withdraw` call
`Accounts.GetForUpdate` immediately after `Wallets.GetForUpdate` and return
`ErrAccountFrozen` (new sentinel, 403, code `account_frozen`, message "This account is
frozen. Contact support.") if status is not `active`. Because the check and the debit
are in the same transaction, a freeze committed one millisecond earlier is seen.
Closing an open position on a frozen account is **allowed**, as the spec's freeze
dialog copy promises; only new exposure is blocked.

**Handlers and routes:** `admin_handler.go`, routes under `/api/v1/admin` behind a new
`middleware.AdminAuth` that checks the JWT `scope` claim is `admin`. The user
middleware is tightened to require `scope` absent or `user`, so an admin token cannot
call user routes either. Admin login is rate limited at 5 per minute per IP.

**Observability hook:** every audit write also increments a Prometheus counter
`admin_actions_total{action}` (section 5). Cheap, and it is the number an alert should
watch for "someone is doing a lot of adjustments at 3am".

**Verify:** a new `scripts/admin_check.py`:
- seeded admin logs in, is told to change password, changes it, old password refused.
- freeze a user's real account; that user's `POST /trades` returns `account_frozen`;
  their `POST /trades/:id/close` on an existing position still succeeds; unfreeze;
  trade succeeds.
- adjust +50 real; the user's wallet shows +50; the ledger has an `admin_adjustment`
  row with the admin's id; the audit log has a row pointing at that transaction.
  Adjust −1000 is refused; nothing changes.
- approve a seeded KYC submission; that user's `me` shows `verified`; their withdraw
  now passes the gate.
- a user token on `/admin/users` gets 401; an admin token on `/accounts` gets 401.
- dashboard reconciliation count is 0 after all of the above.

---

## 4. Admin frontend

Screens are specified in ui-ux-plan.md Part 2. Implementation notes only:

- `src/features/admin/` with `AdminShell`, `adminApi`, `adminSession` store, and the
  shared composites listed there (`DataTable`, `ReasonDialog`, `AuditRow`, badges).
- Routes mounted under `/admin` in `App.tsx`, lazily imported as one chunk so the user
  app's bundle does not grow.
- `DataTable` is the one new primitive worth building well: sticky header, keyboard
  row focus, pagination footer, and empty/error slots. Every admin list uses it.
- The adjust-balance dialog's confirmation sentence is rendered from the values, never
  typed as a template with placeholders, so it cannot drift from what is submitted.

**Verify:** the browser walkthrough in section 7.

---

## 5. Observability

**Sentry**
- Backend: `sentry-go` with the Gin integration. `middleware.Recovery` captures the
  panic; the existing `fail()` in handlers captures `internal` errors (the default
  case) as bugs and every sentinel error with tag `kind=business` so they are
  filterable out. A `BeforeSend` hook strips `Authorization`, `Cookie`, any field named
  `phone`, `amount`, `password`, and request bodies on `/auth/*` and `/wallet/*`.
  `account_id` and `user_id` are attached as tags.
- Frontend: `@sentry/react` with a root `ErrorBoundary` rendering the existing
  `ErrorState` with a "Reload" action. `@sentry/vite-plugin` uploads source maps on
  `npm run build` when `SENTRY_AUTH_TOKEN` is set, and is a no-op otherwise so local
  builds stay unchanged.
- Both DSNs come from config; absent DSN disables Sentry entirely. Development runs
  without it by default.

**Prometheus and Grafana**
- `docker-compose.yml` gains `prometheus` (scraping `api:8080/metrics` every 15s) and
  `grafana` (provisioned datasource and dashboards from `deploy/grafana/`, so the
  dashboards are in git, not clicked together).
- `GET /metrics` on the API, behind a `METRICS_TOKEN` bearer check or bound to an
  internal listener; it must not be public.
- Metrics, all with `prometheus/client_golang`:
  - `http_request_duration_seconds{route,method,status}` histogram.
  - `http_requests_total{route,method,status}` counter.
  - `ws_connections{kind=prices|signals|account}` gauge, from `hub.ClientCount()`
    split by topic prefix.
  - `deposits_total{status=completed|failed}` counter, incremented in
    `settleDeposit`. This is the Daraja health signal.
  - `trades_open` gauge, refreshed by the trading service's minute resync.
  - `ledger_drift_accounts` gauge, computed every 60s by the same query the admin
    dashboard uses. **Alert rule: fire if > 0 for 2 minutes.** This is the
    `sltp_check.py` invariant, watched continuously.
  - `admin_actions_total{action}` counter.
- Two dashboards: "API" (latency, error rate, connections) and "Business" (deposit
  success rate over time, open positions, ledger drift, admin actions).

**Verify:** `curl -H "Authorization: Bearer $METRICS_TOKEN" localhost:8080/metrics`
shows every metric above with plausible values after `smoke.py` runs. Grafana at
`:3000` renders both dashboards with data. Force a drift by running a direct `UPDATE
wallets` in psql; the gauge goes to 1 within a minute and the alert fires; correct it;
it returns to 0.

---

## 6. Testing

In priority order. The first item is a prerequisite for section 3, not a follow-up.

**6.1 Concurrency integration tests (Go, testcontainers-go)**
`internal/service/concurrency_test.go`, build tag `integration`, spins up
`postgres:16-alpine`, runs migrations, and exercises the real `Store`:
- Two goroutines call `Close` on the same open position. Exactly one succeeds; the
  other gets `ErrTradeNotOpen`; the wallet is credited exactly once; the ledger has
  exactly one `trade_close` row.
- A trade with a tight stop loss, and the tick watcher's `settle` racing a manual
  `Close`. Same assertions.
- `Freeze` racing `Open`: start `Open` (which locks the wallet), freeze from another
  goroutine, release; the trade must fail with `ErrAccountFrozen` if the freeze
  committed first, or succeed and then the account is frozen with the position still
  open. Either outcome is correct; a trade opening **after** a committed freeze is the
  bug being tested for.
- Twenty goroutines each try to open a $100 trade on a $1,000 balance. Exactly ten
  succeed; the balance is exactly zero; no `Adjust` ever wrote a negative.
- After every test, the ledger reconciliation query returns zero drifted accounts.

**6.2 Unit tests (Go, extend existing)**
Freeze status check in `Open`/`Deposit`/`Withdraw`; `Adjust` refuses over-debit;
audit row written on every admin action; admin `scope` claim enforced by both
middlewares; `ChangePassword` refuses the default.

**6.3 Frontend unit (Vitest + React Testing Library)**
- `lib/pnl.ts` against `scripts/pnl_vectors.json` (moves the parity check into the
  test runner so CI owns it).
- `OrderTicket`: wrong-sided SL/TP shows the right field error; stake above available
  disables submit; the CTA label tracks direction and stake.
- `DepositSheet`: amount → sending → pending → success and → failed transitions,
  with the polling fallback mocked.
- `ReasonDialog`: confirm disabled until reason is ≥ 10 chars.

**6.4 E2E (Playwright)** against the mock-mode stack in CI:
- register → trade → close, asserting the balance arithmetic.
- deposit → simulated callback → balance updated, on the pending screen.
- admin login → freeze → user's trade attempt shows "This account is frozen".
- admin approve KYC → user's Withdraw button enables without a reload (the `kyc`
  socket event).

**6.5 CI (GitHub Actions)**
One workflow, four jobs: `go` (unit + `-tags integration` with a Postgres service),
`web` (typecheck, Vitest, build), `e2e` (Playwright, after `go` and `web`), and a
`coverage` gate that fails if `internal/service` drops below 80% line coverage on
`wallet_service.go` and `trading_service.go` specifically. The gate is on those two
files, not the package, because the feed and signal services are dominated by
long-running loops that are legitimately hard to cover.

---

## 7. Build order

1. **Concurrency tests (6.1)** against the current code. They must pass before any
   money path is touched. If one fails, that is a round-one bug and fixing it comes
   first.
2. **Contract reconciliation (1)**: `page`, per-user socket, `kyc` event.
3. **Cookie refresh (2)**, backend then frontend, verified together.
4. **Admin migration, seed command, models, repos (3)**. Re-run 6.1: it must still pass
   with the new columns.
5. **Freeze check in the money path (3)**, then extend 6.1 with the freeze race.
6. **Admin service, handlers, routes, `admin_check.py` (3)**.
7. **Admin frontend (4)**.
8. **Observability (5)**. Last because it observes everything above.
9. **Remaining tests and CI (6.2–6.5)**.

Steps 1 and 2 are mechanical and low-risk. Step 5 is the one that changes existing
money code and is why step 1 comes first.

---

## 8. Verification for the round

Everything from round one still passes, plus:

```bash
go test -tags integration ./internal/service/...   # concurrency, real Postgres
python3 scripts/smoke.py                            # updated for page= and cookies
python3 scripts/admin_check.py                      # the admin path end to end
go run ./scripts/wscheck                            # one user socket, two accounts
cd web && npx vitest run && npx playwright test
curl -H "Authorization: Bearer $METRICS_TOKEN" localhost:8080/metrics | grep ledger_drift
```

Browser walkthrough, desktop width: admin login → forced password change → dashboard
shows zero drift → find a user → freeze → in a second browser the user's trade is
refused with the frozen message → unfreeze → adjust +50 with a reason → user's
balance ticks up live → approve their KYC → their Withdraw button enables → every one
of those actions is on the audit log with the reason typed.

---

## 9. Decisions made in this plan that the spec left open

Stated so the implementing session does not re-decide them.

- **Keep `offset` for one release** alongside `page`. Removing it in the same deploy
  as the client switch risks a broken window during rollout.
- **Admin refresh cookie has its own path.** Two sessions, two cookies, never shared.
- **Seed is a command, not a migration.** The only way to guarantee it cannot run in
  production.
- **Closing a position on a frozen account is allowed.** The freeze dialog promises it,
  and forcing a user to hold a losing position they cannot exit is not a support
  outcome anyone wants.
- **Admins cannot close positions this round.** Not in the spec's API; adding it means
  another money path and another test.
- **`/metrics` is authenticated.** A public metrics endpoint leaks deposit volumes.
- **Coverage gate is on two files, not the package.** The gate should measure what
  matters and not be gamed by padding elsewhere.
- **The `kyc` socket event** is a small addition to spec G2, made because the
  alternative is a five-minute wait for a button to enable after an admin approves.
