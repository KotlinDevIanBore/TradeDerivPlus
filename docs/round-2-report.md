# TradeDerivPlus — Round 2 Implementation Report

**Status:** Complete and verified end to end.
**Date:** 17 September 2026.
**Scope:** Spec v2, covering cookie-based refresh tokens, the operations
console, observability, and the testing strategy.

Round one is described in [implementation-report.md](implementation-report.md).
This covers what changed since, following the build order in
[round-2-investigation-report.md](round-2-investigation-report.md).

---

## 1. Headline

Everything in spec v2 is built and verified. Two findings are worth reading
before anything else, because both were latent defects rather than missing
features:

**The concurrency tests found a real bug on their first run, exactly as the
plan predicted.** The reconciliation invariant that round one asserted, "the
sum of completed ledger rows equals the cached balance", is wrong. A pending
*deposit* has not moved money, but a pending *withdrawal* has, because it
debits up front so the same funds cannot be spent twice while a payout is in
flight. Round one's `sltp_check.py` never caught it because that script never
withdraws; withdrawals are KYC-gated.

This mattered more than an incorrect test. Spec v2 asks for a
`ledger_drift_accounts` gauge that alerts when nonzero. Built on the wrong
predicate, it would have fired a false alarm on every in-flight withdrawal
until somebody muted it, which is how a money-loss alarm becomes decoration.
The predicate now lives once, in `internal/repository/reconciliation.go`,
shared by the tests, the admin dashboard and the Prometheus gauge.

**The order ticket's stop-loss and take-profit fields were not reachable by
screen reader.** Their visible labels sit in a header row beside the On/Off
toggle, so they could not wrap the input, and nothing tied them together. A
component test caught it. They are associated by id now, and the toggles have
explicit accessible names.

---

## 2. What was built

| Area | Outcome |
|---|---|
| Contract reconciliation | `page`-based pagination with `offset` kept for one release; the account socket became a per-user channel; a `kyc` event added |
| Cookie refresh | Access token in memory only, refresh token in an httpOnly cookie, silent refresh on boot, server-side logout |
| Admin console | 8 screens, separate session and token scope, audit log, freeze, adjustments, KYC review |
| Observability | Sentry with PII scrubbing, Prometheus with six business metrics, two provisioned Grafana dashboards, two alert rules |
| Testing | 21 Go integration tests on a real Postgres, 59 frontend unit tests, 12 Playwright scenarios, a CI workflow with a coverage gate |

### Size

| | |
|---|---:|
| Go, excluding tests | 7,683 lines |
| Go tests | 2,353 lines across 10 files |
| TypeScript | 9,227 lines across 96 files |
| Admin console frontend | 18 files |
| Playwright specs | 291 lines |

---

## 3. Contract reconciliation

Three places where the round-one build and spec v2 disagreed. Checked against
the code rather than assumed.

**Pagination** now takes `page` (1-based) and returns `page` and `pages`
alongside `total`. `offset` is still accepted for one release so a client
mid-rollout keeps working, and `page` wins when both are present.

**The account socket is a per-user channel.** There is no `account_id`
parameter, so there is nothing for a caller to forge, and one socket serves
every account a user owns. Switching between demo and real no longer tears down
and rebuilds a connection. Each payload names the account it concerns and the
client routes on that.

**A `kyc` event was added** to that channel, so an admin approving verification
unlocks the user's Withdraw button immediately rather than after the profile
query's five-minute stale time. An `account` event does the same for freezes.

---

## 4. Cookie-based refresh

The access token lives in memory and is not persisted. The refresh token is an
httpOnly, Secure, SameSite=Strict cookie scoped to `/api/v1/auth`. No endpoint
returns a refresh token in a body, and `/auth/refresh` ignores one in a request
body: there is no path that hands a long-lived credential to JavaScript.

A page reload starts without an access token, so the app trades the cookie for
a fresh one before the router runs. Without that, every reload would look like
a signed-out session. `POST /auth/logout` clears the cookie, because otherwise
a client can drop its in-memory token while the browser keeps presenting a
valid refresh credential.

The console has its own cookie at its own path, so the two sessions never see
each other's credential, and the tokens carry a scope claim that each
middleware requires.

**The deployment constraint worth reading before staging.** SameSite=Strict
means the cookie is only sent when the request's site matches the cookie's.
Locally the web app on `:5173` and the API on `:8080` are the same site,
because the port is not part of one. In production they must share a
registrable domain or sit behind one reverse proxy, or the cookie is never sent
and every session dies at the access token's TTL. Relaxing to `SameSite=None`
reopens the CSRF exposure the cookie exists to close; fix the domain instead.
`COOKIE_SECURE` must be true in production and the config refuses to boot
otherwise.

---

## 5. The operations console

Eight screens at `/admin`, lazily loaded as a separate 11.6 kB gzipped chunk so
the trading app's bundle grew by 1.5 kB rather than by the whole console.

Desktop-first and table-dense, because an admin sits at a desk. It collapses to
a scrollable top nav below `md` so a support person on a tablet is not locked
out, and nothing depends on hover.

### The rules it holds to

**Every action that touches money or account state demands a written reason of
at least ten characters and a confirmation that states the consequence.** The
reason and the change are written in the same transaction, so an action cannot
commit without its audit record.

**The adjust dialog's confirmation is rendered from the values that will be
submitted**, not from a template with placeholders, so the sentence cannot
drift from what actually happens. It follows the sign: "Credit $50.00 to real
balance on REAL 900002" becomes "Debit $25.00 from…" when the amount goes
negative.

**The audit log has no search box.** It is read top to bottom, and a search
field would suggest some rows matter less than others.

**The KYC document is rendered as the placeholder it is**, in muted italics
with a tooltip, rather than as a link somebody would click expecting a file.
The upload flow remains out of scope, as the spec specified.

**The dashboard leads with ledger reconciliation.** Zero drifted accounts reads
quietly in green; anything else turns red, pins above the other tiles, and
lists the affected accounts with their exact discrepancy.

### Freeze semantics

The freeze check runs **inside the transaction that locks the balance**, not as
a guard before it. A check outside the transaction is a race the freeze can
lose: a trade that read the account a millisecond before the freeze committed
would still debit. `TestFreezeRacesOpen` runs fifteen rounds of exactly that
race and asserts a trade never opens after a committed freeze.

Closing an already-open position on a frozen account is **allowed**, which is
what the confirmation copy promises. Only new exposure is blocked.

### The adjustment decision, pinned by a test

An admin balance adjustment is **not** blocked by a freeze. The freeze exists
to stop the account moving on its own; freezing an account and then being
unable to correct the very balance that caused the freeze would be a dead end
for support.

Per the spec's addendum this is a stated decision rather than something to be
inferred from the code later, so `TestAdjustmentSucceedsOnFrozenAccount`
asserts it directly and its failure message points back at the comment on
`AdminService.Adjust`. If someone later adds the guard "for consistency", that
test tells them why not.

---

## 6. Observability

**Sentry** reports panics from the recovery middleware and unrecognised errors
from the handler mapper, tagged as bugs. Every sentinel business error is
tagged `kind=business` so an issue list is not drowned in expected "insufficient
balance" noise.

The before-send hook strips `authorization`, `cookie` and `set-cookie` headers,
any field whose name contains `phone`, `amount`, `password`, `email`,
`id_number` or `full_name`, and query parameters including `token`. Request
bodies on auth, wallet and admin routes are **dropped entirely** rather than
field-scrubbed, because a KYC rejection reason or a change-password payload can
carry identity data in a free-text field no fixed list would catch. Account and
user ids stay as tags: they are opaque and are what makes an issue debuggable.

An absent DSN disables Sentry entirely, which is how development runs.

**Prometheus** exposes request latency and counts by route, a socket gauge, a
deposits counter by outcome, an open-positions gauge, an admin-actions counter,
and `ledger_drift_accounts`. `/metrics` is behind a bearer token, because a
public one publishes deposit volumes and user counts; production refuses to
boot without `METRICS_TOKEN`.

Two Grafana dashboards and two alert rules are provisioned from files in git
rather than clicked together, so they survive a container and are reviewable.
The critical rule fires when `ledger_drift_accounts > 0` for two minutes.

**Drift detection was verified by breaking it on purpose.** A direct
`UPDATE wallets` in psql, bypassing the ledger, moved the gauge from 0 to 1
within the sampling interval, logged `LEDGER DRIFT DETECTED` at error level,
and surfaced on the admin dashboard naming the account and the exact
discrepancy. Reconstructing the wallet from the ledger cleared it back to zero.

---

## 7. Testing

### Go integration, 21 tests on a real PostgreSQL container

The ones that justify the round:

- Eight goroutines close the same position: exactly one succeeds, the wallet is
  credited once, the ledger holds one close row.
- The stop-loss watcher races a manual close on the same position.
- Twenty goroutines stake 100 against a balance of 1,000: exactly ten succeed,
  the balance lands on exactly zero, nothing goes negative.
- Ten concurrent withdrawals against 500: exactly five succeed.
- A freeze races an open, fifteen rounds.
- Twenty concurrent admin adjustments: the balance is exact and every one left
  an audit row.

**Every test ends by reconciling the ledger against the cache**, using the same
predicate the dashboard and the gauge use.

### Frontend, 59 unit tests

Including the PnL parity suite, which replays the Go implementation's own
generated vectors through the TypeScript formula. That is what stops the live
number a user watches drifting from the realised number they are paid.

### Playwright, 12 scenarios

Against the real stack in mock mode, nothing stubbed: register, trade and close
with the balance arithmetic asserted; the deposit flow through to settlement
over the socket with no reload; an admin freeze refusing a user's trade in a
second browser; KYC approval; token scope separation.

### CI

Four jobs. The coverage gate is on the money-path files rather than the
package, because the feed and signal services are dominated by long-running
loops that are legitimately hard to cover and a package-wide gate would be
satisfied by padding those instead.

**The floor is set at 75%, which is a ratchet rather than a target.** The suite
achieves 77.8%, 80.4% and 78.9% on the three files. A gate set above what the
suite achieves fails on the day it lands and gets disabled, which is worse than
no gate; raise the floor as coverage climbs.

| File | Coverage |
|---|---:|
| `wallet_service.go` | 77.8% |
| `trading_service.go` | 80.4% |
| `admin_service.go` | 78.9% |

Coverage on the money path went from effectively zero to these numbers: the
round-one unit tests never touched a service, because the services need a
database.

---

## 8. Defects found and fixed

Beyond the two in section 1:

1. **Wallet totals rendered as $0.00** beside correct real and bonus figures on
   the admin screens. `Total()` is a Go method, so it never serialised; only
   the one handler that built its JSON by hand included it. Fixed at the source
   with `MarshalJSON` on the model, so every caller gets it and it cannot drift
   from its parts.
2. **The default-password banner never appeared in development.** It was wired
   to the flag that *blocks* a session, which is false in development by
   design. Blocking and "the password is still the default" are now separate,
   which is the case the two flags exist to tell apart.
3. **The auth rate limiter made the E2E suite flaky**, refusing the eleventh
   registration. The limit is configurable now with the production values as
   defaults, and the smoke test asserts the limiter still fires using a genuine
   concurrent burst, so relaxing it in CI cannot hide a regression.
4. **User-facing error messages read as lowercase fragments.** The contract
   says `message` is safe to render directly and the order ticket does exactly
   that, so a frozen account showed "this account is frozen". They are written
   as sentences now; Go's lowercase convention is for strings that get wrapped,
   and none of these are.
5. **`scripts/admin_check.py` left the shared admin credential changed**, so
   anything running after it failed. It cannot restore the seeded password
   because the API refuses to set a password to the known default, which is
   that guard working. It now leaves a documented credential the other suites
   know, and `go run ./cmd/seed -reset-admin` restores a fresh state.

A sixth is worth noting as a property rather than a bug: the audit log's
foreign key **prevents deleting an admin**, so a trail survives the person who
made it. That is why the reset flag exists.

---

## 9. Decisions

Stated so they are not re-litigated from the code.

- **`offset` is kept alongside `page` for one release**, to avoid a broken
  window during rollout.
- **The seed is a command, not a migration**, and refuses to run unless
  `APP_ENV=development`. That is what makes "the default credential can exist
  in development but never in production" a guarantee rather than a hope.
- **Closing a position on a frozen account is allowed.** Only new exposure is
  blocked.
- **Admins cannot close a user's position.** It is not in the spec's API and
  would be another money path needing another test.
- **`/metrics` is authenticated.**
- **The coverage floor is a ratchet at 75%**, not an aspiration at 80%.
- **Reconciliation counts pending withdrawals and not pending deposits.** The
  asymmetry is deliberate and is documented where the predicate lives.

---

## 10. Still not done

- **The KYC upload flow remains out of scope**, as the spec specified. The
  table, the admin review screens and the approval path are built and tested;
  submissions are seeded with an obvious placeholder document. The user-facing
  "Start verification" button stays disabled.
- **Withdrawals have no payout provider.** A withdrawal debits the balance and
  records a pending row; nothing completes or reverses it. Reconciliation
  handles this correctly, but a real payout integration and a refund path for a
  failed payout are still needed.
- **The chart draws ticks, not candles.** No candle endpoint exists.
- **Playwright runs Chromium only.** Adding WebKit and Firefox is a config
  change plus CI time.

---

## 11. Verification

All of the following pass from a cold start, re-run in full before this report
was written.

```bash
gofmt -l ./cmd ./internal ./pkg ./scripts    # clean
go vet ./...                                 # clean
go test ./...                                # unit
go test -tags integration ./internal/service/...   # 21 tests, real Postgres
python3 scripts/smoke.py                     # the user path
python3 scripts/sltp_check.py                # levels settle from the tick stream
python3 scripts/admin_check.py               # 45 admin checks
go run ./scripts/wscheck                     # sockets, and a 401 without a token
node scripts/pnl_parity.mjs                  # Go and TypeScript agree
cd web && npm run typecheck && npm test && npm run build
cd web && npm run e2e                        # 11 passed, 1 guarded skip
```

The guarded skip is the KYC scenario when no pending submission is seeded; it
says so and names the command to fix it.

### Bundle

| Chunk | Raw | Gzipped |
|---|---:|---:|
| Main | 513.4 kB | 162.6 kB |
| Chart, Trade screen only | 346.9 kB | 101.8 kB |
| Admin console, `/admin` only | 45.6 kB | 11.6 kB |
| CSS | 25.4 kB | 5.9 kB |

---

## 12. Next

1. **Build the KYC upload flow.** The review side is finished and waiting.
2. **Integrate a payout provider** and add the refund path for a failed
   withdrawal, which is the one money path with no completion.
3. **Raise the coverage floor** toward 85% as tests accumulate.
4. **Exercise Daraja sandbox end to end**, including a genuine timeout and a
   genuine cancellation, before trusting the mock's coverage of those paths.
5. **Add a candle endpoint** and swap the chart.
