# TradeDerivPlus — Round 2 Investigation Report

**Scope:** Technical spec v2 measured against the round-one build.
**Date:** 17 September 2026.
**Outcome:** Three contract drifts found and reconciled; four new scope areas planned
(cookie-based refresh, admin console, observability, testing); a build order that
proves the existing money path under concurrency before adding new ways to move money.

This document consolidates the spec diff, the admin console UI/UX plan, and the round-two
implementation plan into one report. It stands alone: nothing in it depends on another
file.

---

## 1. Where round one left things

Round one delivered the full user-facing product and verified it from a cold start:

- Go + Gin + PostgreSQL backend with a transactional store, an append-only ledger, row
  locks on every balance change, and mock implementations of the Deriv feed and the
  M-Pesa lifecycle so the whole stack runs offline.
- React + TypeScript frontend: login, register, home, trade, portfolio, wallet, the
  four-step M-Pesa deposit, withdraw, signals, profile, account switcher, and the shell
  that owns the live sockets.
- The ten API gaps the original UI/UX plan identified were all closed.
- Verified by Go unit tests, a 44-assertion smoke test, a stop-loss auto-close check that
  reconciles the ledger to the wallet cache to the cent, WebSocket checks, and a
  Go-versus-TypeScript PnL parity check across 12 vectors.

One known weakness was recorded at the time: the row locks in the money path are
reasoned about but not exercised under actual concurrent load. That shapes the order
of this round.

---

## 2. What spec v2 changes

Spec v2 absorbs the round-one gap fixes (register creates the demo account, `GET
/auth/me`, `GET /wallet/transactions/:id`, `WS /ws/account`, signals across all
symbols, pagination) and adds four areas:

1. **Token storage.** Access token in memory only. Refresh token in an httpOnly, Secure,
   SameSite=Strict cookie set by the backend.
2. **Admin console.** Separate admin users, an audit log, account freeze, KYC review, and
   manual balance adjustments that are ordinary ledger rows.
3. **Observability.** Sentry on both sides with PII scrubbing; Prometheus and Grafana with
   business metrics including a continuously watched ledger reconciliation gauge.
4. **Testing strategy.** Concurrency integration tests on real Postgres, frontend unit
   tests, Playwright end-to-end, and a CI coverage gate on the two money-path files.

---

## 3. Findings: where the build and the spec disagree

Checked against the code, not from memory.

| # | Built | Spec v2 | Resolution |
|---|---|---|---|
| R1 | `?limit=&offset=` on `/trades` and `/wallet/transactions` | `?page=&limit=` | Accept `page` (1-based). Keep accepting `offset` for one release so the current client survives the deploy window. Response gains `page` and `pages`. |
| R2 | `WS /ws/account?account_id=`, one socket per account | Per-user channel; payload carries `account_id` | Topic becomes `user:<id>`, authorised on the token alone. Client opens one socket in the shell; switching account no longer reconnects. |
| R3 | Both tokens persisted in localStorage | Access in memory, refresh in httpOnly cookie | Section 5. |

One small addition proposed while R2 is open: a `kyc` event `{ user_id, kyc_status }` on
the user socket, so the Withdraw button unlocks the moment an admin approves rather
than on the next five-minute profile refetch.

Everything else in the v2 user-facing contract is already built and matches.

---

## 4. Contract reconciliation (first step of the round)

R1 and R2 are mechanical. The handlers accept `page` and compute the offset; the socket
handler drops the `account_id` parameter and subscribes to the user topic; every push
already carries the account id, so the client routes by that.

**Verify:** the smoke test updated for `page`; the WebSocket check opens the user socket
with no `account_id`, opens a trade on each of two accounts, and asserts both events
arrive on the single socket.

---

## 5. Cookie-based refresh (R3)

**Backend.** `login`, `register` and `refresh` set `tdp_refresh` as an httpOnly, Secure,
SameSite=Strict cookie scoped to `Path=/api/v1/auth`. The JSON body carries only
`access_token` and `expires_in`. `POST /auth/refresh` reads the cookie and ignores any
body token. A new `POST /auth/logout` clears the cookie. Admin gets the same treatment
under `tdp_admin_refresh` at `Path=/api/v1/admin`, so the two sessions never see each
other's cookie. CORS already allows credentials; the origin list must stay exact, not
wildcard.

**Frontend.** The session store persists only `user` and `activeAccountId`; the access
token is memory-only and the refresh token field is deleted. Every request uses
`credentials: 'include'`. On 401 the client calls refresh with no body. On app boot with
no access token, one silent refresh is attempted before redirecting to login, so a
reload does not sign the user out.

**A production trap, flagged now.** SameSite=Strict only sends the cookie when the
request origin matches the cookie's site. It works locally because the port is not part
of the site. In production the web app and API must share a registrable domain, or sit
behind one reverse proxy, or the cookie is never sent and every session dies after
fifteen minutes. The fix is the domain, not dropping to SameSite=None, which reopens the
CSRF exposure the cookie is meant to close.

**Verify:** log in, reload, still signed in. Set the access TTL to ten seconds, wait,
make a request, it succeeds transparently. `document.cookie` does not contain the
refresh token. Log out, refresh returns 401.

---

## 6. Admin console: backend

Same layering rules as round one: handlers never import the repository, services never
import Gin.

**Migration.** `admin_users`, `admin_audit_log`, `kyc_submissions` as specified;
`accounts` gains `status` and `frozen_reason`; indexes on the audit log by target and
on submissions by status. **The seed is a command, not a migration.** Migrations run
everywhere. The default admin and the placeholder KYC rows come from a seed command
that refuses to run unless `APP_ENV=development`. That is how "the default credential
can exist in dev but never in prod" is enforced rather than hoped for.

**Models.** `AdminUser`, `AuditEntry`, `KYCSubmission`. `Account` gains status and
frozen reason. `admin_adjustment` joins the transaction types and the user-facing
formatter renders it as "Balance adjustment".

**Service.** One file, `admin_service.go`, because every admin action has the same
shape: authorise, act, audit, in one transaction.

- Login and change-password with the same timing-safe miss as user login. A new
  password equal to the current one or to the seeded default is refused.
- Freeze and unfreeze lock the account row, set the status, and write the audit row.
- Adjust locks the wallet, inserts the `admin_adjustment` ledger row carrying the admin
  id and reason, applies the balance change through the existing non-negative guard,
  and writes the audit row with the resulting transaction id. Pushes `wallet` and
  `transaction` on the user's socket so their balance updates live.
- Approve and reject KYC update the submission and `users.kyc_status` together and
  push the new `kyc` event.
- The dashboard's reconciliation gauge is a single SQL statement comparing the sum of
  completed ledger rows per account against the wallet cache, returning the mismatch
  count and the affected ids.

**The freeze check goes inside the transaction, at the point the balance is locked.**
Open, deposit and withdraw lock the account row immediately after the wallet row and
return a new `account_frozen` error (403, "This account is frozen. Contact support.")
if status is not active. Because the check and the debit share a transaction, a freeze
committed one millisecond earlier is seen. **Closing an open position on a frozen
account is allowed.** The freeze dialog promises it, and forcing a user to hold a
losing position they cannot exit is not an outcome anyone wants.

**Routes** under `/api/v1/admin` behind a middleware that requires the JWT scope claim
to be `admin`. The user middleware is tightened to reject that scope, so neither token
opens the other's routes. Admin login is rate limited at five per minute per IP.

**Verify** with a new `scripts/admin_check.py`: seeded admin logs in, is forced to change
password, old password refused; freeze a user's real account, that user's trade is
refused with `account_frozen`, their close on an existing position still succeeds,
unfreeze, trade succeeds; adjust +50 real, user's wallet shows +50, ledger has the
adjustment row with the admin id, audit log points at that transaction, adjust −1000
is refused and nothing changes; approve a seeded KYC submission, user's profile shows
verified, their withdraw passes the gate; a user token on an admin route gets 401 and
vice versa; reconciliation count is zero after all of the above.

---

## 7. Admin console: screens

A separate route tree under `/admin` with its own session, login and layout. Same design
tokens and primitives as the user app, but **desktop-first and data-dense**: a fixed left
sidebar and tables rather than cards. Usable at tablet width; nothing depends on hover.

**Design rules specific to admin**

- Tables, not cards. 44px rows, hairline dividers, sticky header, right-aligned tabular
  numerals for money.
- The red accent is reserved for destructive and money-moving actions: Freeze, Adjust,
  Reject. Approve and Unfreeze are green outline.
- Every money-moving or state-changing action requires a typed reason and a
  confirmation that restates exactly what will happen to whom. The reason is written to
  the audit log.
- The admin's identity is always visible in the sidebar footer.
- A full-width amber banner on every screen while `must_change_password` is true.

**Shared composites:** `DataTable` (sticky header, empty and error slots, pagination
footer), `SearchInput` (debounced), `FilterChips`, `ReasonDialog` (required reason of at
least ten characters, confirm in the action's colour), `AuditRow`, `AccountStatusBadge`,
`KYCBadge`.

### Admin shell (`/admin/*`)
Left sidebar: Dashboard, Users, Transactions, KYC queue with a pending-count badge
refetched every 60 seconds, Audit log. Footer: username, role, change password, sign
out. Unauthenticated goes to `/admin/login`. A user token never opens an admin route.

### Admin login (`/admin/login`)
Username, password, sign in. Same error message and timing for a wrong username and a
wrong password. Rate limited. Outside development, a session with
`must_change_password` true lands on Change password with no way around it.

### Change password (`/admin/change-password`)
Current, new with the live checklist, confirm. Refuses the current password and the
seeded default. Success clears the banner.

### Dashboard (`/admin`)
Five stat tiles: users, deposits today, withdrawals today, open positions, live socket
connections. A sixth tile that matters more: **ledger reconciliation**, showing the
count of accounts where the ledger sum disagrees with the wallet cache. Zero is green;
nonzero turns red, pins to the top of the page, and links to the affected accounts. A
24-hour deposits chart of success versus failed per hour, which surfaces a Daraja
outage before a user complains. Recent audit rows. All from one dashboard call plus
one audit call; the dashboard never fans out.

### Users (`/admin/users?search=&page=`)
Search by phone, email, name or account number. Table: name, phone, KYC badge, account
pills with `FROZEN` inline where applicable, real balance, joined. Row click opens the
detail. Frozen state is visible in the list so a support agent sees it without opening
the row.

### User detail (`/admin/users/:id`)
The most carefully gated screen, because money-moving actions live here.

- Header: name, phone, email, joined, KYC badge, and a link to the pending submission
  if one exists.
- One card per account: pill, status badge, balances, the frozen reason with who froze
  it and when, and the action row: Freeze or Unfreeze, and Adjust balance.
- Recent transactions with an Admin column on adjustment rows.
- Open positions, read-only. Admins cannot close positions this round.
- The audit trail for this user.

Freeze opens a reason dialog whose consequence line reads: "The user will not be able
to trade, deposit, or withdraw from this account until it is unfrozen. Open positions
stay open and can still be closed." Adjust is a two-step dialog: amount, balance type
and reason, then a confirmation that renders the four facts in bold from the entered
values: "Credit **$50.00** to **real** balance on **REAL 900002**, owned by
**+254712…**. This writes a ledger row that cannot be undone; a mistake is corrected
with a second adjustment." An over-debit is refused by the server and shown under the
amount field. On success the card and the audit trail update from the response; on
failure the dialog stays open and nothing on the page changes.

### Transactions (`/admin/transactions?search=&type=&status=&page=`)
The first place support goes when a user says a deposit did not arrive. Search by
reference, receipt, phone, account number or id. Type and status chips. Table with
time, type, account pill, signed amount, status, truncated reference that copies on
click, and admin username on adjustments. Row click opens the same detail sheet as the
user app plus raw metadata and, for deposits, the linked M-Pesa request with its result
code and description. A deposit pending for more than ten minutes gets an amber edge
and a "Stuck?" tag so support can tell "never entered PIN" from "callback never came".

### KYC queue (`/admin/kyc?status=pending`)
Pending by default; approved and rejected as chips. Table: submitted, user, ID number,
document (in this round a literal placeholder rendered in muted italics with a "no
upload flow yet" tooltip, so nobody clicks expecting a file), status, reviewer. Approve
confirms with one line: "This enables withdrawals on their real account." Reject opens
a reason dialog. An empty pending queue shows a green check and "Nothing waiting for
review", because that is the good state and should look like it. Approval reaches the
user's app immediately through the `kyc` socket event.

### Audit log (`/admin/audit-log?page=`)
Read-only and always one click from the sidebar. Time, admin, action badge, target
link, full reason text, expandable details including the resulting transaction id on
adjustments. Optional chips by admin and by action. No search box: the audit log is
read top to bottom, and search would invite the idea that some rows matter less.

**Cross-cutting.** Two sessions, two cookies at different paths, two middlewares. Every
admin mutation also invalidates the audit log query so the trail is never stale on
screen. The admin route tree is lazily imported as one chunk so the user app's bundle
does not grow.

---

## 8. Observability

**Sentry.** Backend: the recovery middleware captures panics; the handler error mapper
captures unrecognised errors as bugs and every sentinel business error with a `kind=
business` tag so they filter out. A before-send hook strips authorization and cookie
headers, any field named phone, amount or password, and request bodies on auth and
wallet routes. Account and user ids are attached as tags. Frontend: the React SDK with
a root error boundary rendering the existing error state with a Reload action; source
maps upload at build time only when a token is present, so local builds are unchanged.
An absent DSN disables Sentry on either side.

**Prometheus and Grafana.** Both added to docker-compose, with the datasource and
dashboards provisioned from files in git rather than clicked together. A `/metrics`
endpoint behind a bearer token, because a public one leaks deposit volumes. Metrics:
request duration histogram and request counter by route and status; a socket
connection gauge split by kind; a deposits counter by outcome, which is the Daraja
health signal; an open positions gauge; an admin actions counter by action; and
`ledger_drift_accounts`, recomputed every minute by the same query the admin dashboard
uses, with an alert rule that fires if it is nonzero for two minutes. That turns the
round-one reconciliation check into something watched continuously.

**Verify.** After the smoke test, the metrics endpoint shows every metric with plausible
values and Grafana renders both dashboards. Force a drift with a direct wallet update
in psql: the gauge goes to one within a minute and the alert fires; correct it; it
returns to zero.

---

## 9. Testing

In priority order. The first item is a prerequisite for the admin work, not a
follow-up.

**9.1 Concurrency integration tests.** Go, `testcontainers-go`, a real `postgres:16-alpine`,
migrations applied, exercising the real store:

- Two goroutines close the same open position. Exactly one succeeds; the wallet is
  credited exactly once; the ledger has exactly one close row.
- The tick watcher's settle races a manual close on a tight stop loss. Same assertions.
- A freeze races an open. Either the trade fails with `account_frozen` because the
  freeze committed first, or it succeeds and the account is then frozen with the
  position still open. Both are correct. A trade opening **after** a committed freeze
  is the bug under test.
- Twenty goroutines each try a $100 trade on a $1,000 balance. Exactly ten succeed; the
  balance is exactly zero; nothing ever went negative.
- After every test the reconciliation query returns zero drifted accounts.

**9.2 Go unit tests**, extending the existing set: the freeze check in open, deposit and
withdraw; over-debit refused; an audit row on every admin action; scope enforced by
both middlewares; the default password refused.

**9.3 Frontend unit tests**, Vitest and React Testing Library: the PnL formula against
the Go-generated vectors, moving the parity check into the test runner so CI owns it;
order ticket validation and the CTA label; deposit sheet step transitions with the
polling fallback mocked; the reason dialog's minimum length.

**9.4 End-to-end**, Playwright against the mock-mode stack: register, trade, close with
balance arithmetic asserted; deposit through the simulated callback; admin freeze then
the user's trade refused; admin KYC approval then the user's Withdraw button enabling
without a reload.

**9.5 CI**, one GitHub Actions workflow with four jobs: Go unit and integration with a
Postgres service, web typecheck and tests and build, end-to-end after both, and a
coverage gate that fails if `wallet_service.go` or `trading_service.go` drops below
80% line coverage. The gate is on those two files, not the package, because the feed
and signal services are dominated by long-running loops that are legitimately hard to
cover and the gate should not be gameable by padding elsewhere.

---

## 10. Build order

1. Concurrency tests (9.1) against the current code. They must pass before any money
   path is touched. A failure is a round-one bug and is fixed first.
2. Contract reconciliation: `page`, per-user socket, the `kyc` event.
3. Cookie refresh, backend then frontend, verified together.
4. Admin migration, seed command, models, repositories. Re-run 9.1.
5. The freeze check in the money path. Extend 9.1 with the freeze race.
6. Admin service, handlers, routes, and the admin check script.
7. Admin frontend.
8. Observability, last, because it observes everything above.
9. Remaining tests and CI.

Steps 1 and 2 are low-risk. Step 5 is the one that changes existing money code, and is
why step 1 comes first.

---

## 11. Verification for the round

Everything from round one still passes, plus:

```bash
go test -tags integration ./internal/service/...   # concurrency on real Postgres
python3 scripts/smoke.py                            # updated for page= and cookies
python3 scripts/admin_check.py                      # the admin path end to end
go run ./scripts/wscheck                            # one user socket, two accounts
cd web && npx vitest run && npx playwright test
curl -H "Authorization: Bearer $METRICS_TOKEN" localhost:8080/metrics | grep ledger_drift
```

Browser walkthrough at desktop width: admin login, forced password change, dashboard
shows zero drift, find a user, freeze, in a second browser the user's trade is refused
with the frozen message, unfreeze, adjust +50 with a reason, the user's balance ticks
up live, approve their KYC, their Withdraw button enables, and every one of those
actions is on the audit log with the reason typed.

---

## 12. Decisions made here that the spec left open

Stated so the implementing session does not re-decide them.

- **Keep `offset` for one release** alongside `page`, to avoid a broken window during
  rollout.
- **The admin refresh cookie has its own path.** Two sessions, two cookies, never
  shared.
- **The seed is a command, not a migration.** The only way to guarantee it cannot run
  in production.
- **Closing a position on a frozen account is allowed.** Only new exposure is blocked.
- **Admins cannot close positions this round.** It is not in the spec's API, and it
  would be another money path needing another test.
- **`/metrics` is authenticated.** A public endpoint leaks deposit volumes.
- **The coverage gate is on two files, not the package.**
- **The `kyc` socket event** is a small addition to the account channel, because the
  alternative is a five-minute wait for a button to enable after an admin approves.
- **SameSite=Strict is kept.** The production deployment must put the API on the same
  registrable domain as the web app; the cookie attribute is not the thing to change.

---

## 13. Risks worth watching

- **The concurrency tests may find a round-one bug.** That is the point of running them
  first. If they do, it delays the round and that is the correct trade.
- **The cookie domain constraint** is the most likely thing to bite in staging if the
  deployment topology is decided without reading section 5.
- **Admin adjustments are irreversible by design.** The confirmation copy says so, and
  the audit log records who and why, but there is no undo. A second adjustment is the
  correction path.
- **The KYC document link is a placeholder this round.** The screen renders it as such;
  the risk is someone wiring a real upload later without revisiting the copy.
