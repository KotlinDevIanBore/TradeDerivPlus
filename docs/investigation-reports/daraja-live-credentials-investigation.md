# Investigation: switching to live Daraja credentials for manual testing

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Status:** read-only survey. Nothing was changed, and no credential was written anywhere.

Two findings dominate everything else and are stated here so they are not buried:

1. **Withdrawals never call M-Pesa.** `WalletService.Withdraw`
   (`internal/service/wallet_service.go:301-390`) debits the user's balance and writes a
   `pending` row. There is no B2C call anywhere in the codebase — `pkg/mpesaclient` implements
   STK push only. A "live withdrawal test" would take the money out of the app and pay nobody,
   with no code path that ever completes it. Only deposits can be tested live.
2. **The callback endpoint is unauthenticated and unrate-limited, and crediting is keyed solely
   on `CheckoutRequestID`** (`internal/routes/routes.go:77`,
   `internal/service/mpesa_service.go:101-136`). In sandbox that is harmless. In production the
   only thing between an anonymous POST and a real wallet credit is knowledge of a Safaricom-
   generated id.

---

## 1. Current Daraja integration surface

### Where the credentials live

Environment variables only — no secrets manager, no vault. Read once at boot in
`internal/config/config.go:128-134`, stored on the `Config` struct at `:56-62`, and used in one
place (`internal/service/mpesa_service.go:39-51`).

| Value | Env var | Default (`config.go`) | Notes |
|---|---|---|---|
| Base URL | `MPESA_BASE_URL` | `https://sandbox.safaricom.co.ke` (`:128`) | **Sandbox by default** |
| Consumer key | `MPESA_CONSUMER_KEY` | empty (`:129`) | |
| Consumer secret | `MPESA_CONSUMER_SECRET` | empty (`:130`) | |
| Shortcode | `MPESA_SHORTCODE` | `174379` (`:131`) | **Safaricom's shared sandbox till** |
| Passkey | `MPESA_PASSKEY` | empty (`:132`) | |
| Callback URL | `MPESA_CALLBACK_URL` | `http://localhost:8080/api/v1/mpesa/callback` (`:133`) | **Not reachable by Safaricom** |
| Mock switch | `USE_MOCK_MPESA` | `true` (`:134`) | Bypasses Daraja entirely |

Related money config, not Daraja but relevant to a live test: `KES_PER_USD` (129.50),
`MIN_DEPOSIT_KES` (50), `MAX_DEPOSIT_KES` (150000) at `config.go:136-138`.

`.env.example:33-40` documents the same set, all pointed at sandbox. The local `.env` is
gitignored (`.gitignore:2`) and currently has `USE_MOCK_MPESA=true`, so no live call can be made
from this machine as it stands.

### Is the sandbox URL hardcoded?

**No — this is a config change, not a code change.** `pkg/mpesaclient` takes `BaseURL` through
`Options` (`client.go:31-50`) and concatenates it at both call sites: `client.go:107` for OAuth
and `client.go:166` for the STK push. The only hardcoded sandbox values in the repository are the
two *defaults* in `config.go:128` and `:131`, and both are overridable.

`MPESA_SHORTCODE`'s default of `174379` deserves its own mention: it is Safaricom's shared
sandbox till, and it is a **default rather than a required value**. Setting live credentials while
forgetting the shortcode gives a live key signing a request for somebody else's sandbox till —
which fails, but fails confusingly rather than loudly.

### The callback flow

`Deposit` → `InitiateSTK` (`mpesa_service.go:61-96`) sends the push with `CallBackURL` set from
config, records a row in `mpesa_requests`, and returns. The wallet is **not** credited here. Money
moves only when Safaricom POSTs to `MPESA_CALLBACK_URL` → `WalletHandler.MpesaCallback`
(`internal/handlers/wallet_handler.go:143-157`) → `HandleCallback` → `settleDeposit`
(`wallet_service.go:226-289`).

**It must be publicly reachable over HTTPS.** Safaricom rejects non-HTTPS callback URLs and
cannot reach `localhost`. The current local setup therefore **cannot** complete a live deposit:
the STK push would arrive on the phone, the user would pay, and the wallet would never be
credited because the callback has nowhere to land. Testing locally requires a tunnel (ngrok,
Cloudflare Tunnel) with the HTTPS URL in `MPESA_CALLBACK_URL`; otherwise it requires a deployed
instance. The README already says this at `README.md:75`, without saying what breaks if it is
ignored — which is that the user is charged and the balance does not move.

---

## 2. What "live" actually changes

### Beyond the base URL

| Thing | Sandbox | Production | Does this code assume otherwise? |
|---|---|---|---|
| Base URL | `sandbox.safaricom.co.ke` | `api.safaricom.co.ke` | No — env-driven |
| Credentials | Sandbox app key/secret | **Different app**, different key/secret | No |
| Shortcode / passkey | Shared `174379` + shared passkey | **Your own** paybill/till and its own passkey | No, but see the default above |
| STK request shape | identical | identical | — |
| Callback payload | identical | identical | — |
| `TransactionType` | `CustomerPayBillOnline` (`client.go:157`) | **Depends on your shortcode type** | **Yes — see below** |

**The one real code-level assumption is `TransactionType`.** `client.go:157` hardcodes
`CustomerPayBillOnline`. If the live shortcode is a Buy Goods till rather than a paybill,
Safaricom expects `CustomerBuyGoodsOnline`, and the push is rejected. Which one is correct
depends on credentials this investigation cannot see, so it is a question for whoever owns the
Daraja app, not something the codebase can answer.

Two behavioural differences that are not shape differences but matter more at this stage:

- **Callback latency and delivery.** Sandbox callbacks arrive in seconds and always arrive.
  Production callbacks can be delayed, retried, or lost. There is **no status-query call** in
  `pkg/mpesaclient` — no `stkpushquery` — so a lost callback leaves a paid deposit stuck
  `pending` with no code path that can resolve it. The only recovery is a manual admin
  adjustment (`AdminService.Adjust`).
- **Result codes.** `humanResult` (`mpesa_service.go:174-196`) maps six codes and falls back to
  Safaricom's raw `ResultDesc` for the rest. Production returns codes sandbox never does; those
  users see raw Safaricom English. Cosmetic, not a money risk.

### Hardcoded sandbox-only values

A full grep for `174379`, `sandbox.safaricom` and the well-known sandbox passkey found exactly two
hits, both the config defaults above. **No test or script hardcodes a sandbox shortcode**, and no
sandbox test MSISDN (`254708374149`) appears anywhere. Phone numbers in tests are randomly
generated — which is its own problem, see §3.

---

## 3. Blast radius

### Everything that touches the deposit path

| Caller | Fires an STK push? | Why it is safe today |
|---|---|---|
| `scripts/smoke.py:203` | **Yes** — `POST /wallet/deposit`, KES 12,950 (≈ $100) | `USE_MOCK_MPESA=true` |
| `web/e2e/user.deposit.spec.ts` | **Yes** — drives the deposit sheet for KES 12,950, and CI runs it | `USE_MOCK_MPESA: "true"` at `ci.yml:159` |
| `internal/service/wallet_integration_test.go` | No | harness sets `UseMockMpesa: true` (`integration_setup_test.go:125`) |
| `cmd/seed/main.go:170-176` | No | writes ledger rows directly; also refuses to run outside development (`:53`) |
| `scripts/admin_check.py` | No | reads dashboard deposit totals only |

**The sharpest edge is `scripts/smoke.py`.** It registers with a **randomly generated Safaricom
number** (`smoke.py:65`, `f"07{random.randint(10_000_000, 99_999_999)}"`) and then requests a
KES 12,950 deposit to it. Against live credentials that sends a real STK push for about 12,950
shillings **to a stranger's phone** — a number nobody in this project owns. It would not charge
you; it would prompt a member of the public to pay you, repeatedly, once per run. The same is true
of `user.deposit.spec.ts`, which CI runs on every push.

Nothing in either script checks whether it is pointed at sandbox. Both would do this silently.

### Safeguards in the wallet and ledger

| Safeguard | Present? | Detail |
|---|---|---|
| Amount limits | **Yes** | `MIN_DEPOSIT_KES` / `MAX_DEPOSIT_KES`, enforced at `wallet_service.go:151-156`. Default ceiling **KES 150,000 per deposit** |
| Rate limit on deposit | **Yes, weak** | `money` bucket, one per 2s burst 5 (`routes.go:92-94`) — so 5 pushes immediately, then one every two seconds, indefinitely |
| Idempotency key | **No** | No idempotency anywhere in `internal/` (already a known gap for the trading path — `auto-bot-deferred.md`). Nothing stops a double-tapped Deposit from sending two real pushes for two real charges |
| Dry-run mode | **No** | `USE_MOCK_MPESA` is the only switch, and it is all-or-nothing per process |
| Per-user or per-day cap | **No** | Only the per-transaction ceiling |
| Freeze check | Yes | Under a row lock (`wallet_service.go:164-176`) |

### Duplicate and failed callbacks

**The intent is right and the implementation has a gap.** Three layers:

1. `mpesa_requests.checkout_request_id` is `UNIQUE` (`migrations/0001_init.up.sql:51`), so the
   same push cannot be recorded twice.
2. `HandleCallback` returns early when the request is not `pending` (`mpesa_service.go:111-114`).
3. `settleDeposit` returns early when the transaction is not `pending` (`wallet_service.go:232`).

Both guards are **unlocked read-then-write**. `HandleCallback` reads the status, then calls
`UpdateResult`, whose `UPDATE` has no `WHERE status = 'pending'`
(`internal/repository/mpesa_repo.go:34-39`). `settleDeposit` reads, then credits inside a
transaction whose `UpdateStatus` is likewise unguarded (`transaction_repo.go`). Two callbacks
arriving concurrently for one `CheckoutRequestID` could both pass their check and both reach
`Wallets.Adjust` (`wallet_service.go:263`) — **a double credit**.

How likely: Safaricom retries callbacks, and retries are exactly the case where two deliveries
overlap. The endpoint is unauthenticated and unrate-limited, so a retry storm is not throttled
either. It has never been observed because the mock delivers each callback exactly once, from one
goroutine (`mpesa_service.go:138-172`). **This is the single highest-value fix before real money**,
and it is small: a `WHERE status = 'pending'` on both updates, or a `SELECT … FOR UPDATE` on the
transaction row, so the loser of the race credits nothing.

Two lesser notes on the same path:

- The callback's own amount is never checked against the recorded one. `settleDeposit` credits
  `tx.Amount` — what we asked for — not what Safaricom says was paid. That is the safe direction
  against a forged callback, but a partial payment would credit in full.
- Drift is detectable after the fact: `ledger_drift_accounts` covers every account
  (`internal/repository/reconciliation.go:26-30`), and `DepositFailureRate` alerts above 50%
  failures (`deploy/prometheus/rules.yml:20-29`). Neither prevents a double credit; both would
  show it.

---

## 4. Isolation options

### What exists

**Almost nothing.** `APP_ENV` has exactly two meanings — `production` and everything else
(`config.go:104`, `:162-176`) — and it gates Gin's mode, `JWT_SECRET`, `COOKIE_SECURE` and
`METRICS_TOKEN`. There is no staging concept, no per-environment config file, and no deployment
manifest in the repository (`deploy/` holds only Grafana and Prometheus config).

What does isolate cleanly:

- **`USE_MOCK_MPESA` is per process.** CI sets it at `ci.yml:159` and `:267`; other developers'
  `.env` files set it locally. A separate deployment with live credentials does not touch any of
  them.
- **The database is per deployment.** A live-credential instance pointed at its own database
  cannot corrupt dev or CI data.
- **`cmd/seed` refuses to run outside development** (`main.go:53`), so seed data cannot be
  created against a production-flagged instance by accident.

### The gap

There is no way to have live credentials *and* keep the scripts safe in one process. `smoke.py`
and `user.deposit.spec.ts` decide nothing for themselves — they just call the API, and the API
does whatever `USE_MOCK_MPESA` says. If a live instance is ever pointed at by either script, it
sends real pushes. The protection has to be "never run those against that instance", which is a
habit rather than a guard.

A small, cheap guard would change that: have `smoke.py` read `USE_MOCK_MPESA` (or a
`/health`-style field reporting it) and refuse to run the deposit section against a live API.
That is a code change and out of scope here, but it is the one thing that would make the
separation enforced rather than remembered.

---

## 5. Gaps and open questions

### Needed for production Daraja, not for sandbox

These are Safaricom-side and cannot be answered from the codebase:

- **A production Daraja app**, approved and Go-Live completed — a separate app from the sandbox
  one, with its own consumer key and secret.
- **Your own shortcode** (paybill or till) and **its** passkey. Whether it is a paybill or a till
  determines whether `TransactionType` at `client.go:157` is correct as written.
- **A registered callback URL on an HTTPS domain.** Safaricom validates and, for some shortcode
  types, whitelists it. `http://localhost` cannot be used.
- **Possible IP whitelisting** on Safaricom's side, depending on the account. A tunnel's egress IP
  is not stable, which may matter.

### Unfinished or sandbox-shaped, now that real money is involved

| Gap | Where | Why it matters live |
|---|---|---|
| **No STK status query** | `pkg/mpesaclient` has `STKPush` only | A lost callback strands a paid deposit as `pending` forever. No reconciliation job, no retry, no manual resolution short of an admin adjustment |
| **No payout implementation** | `wallet_service.go:301-390` | Withdrawals debit and pay nobody. See the header |
| **Unauthenticated callback** | `routes.go:77` | Anyone who can guess or observe a `CheckoutRequestID` can trigger a wallet credit. No signature check (Safaricom offers no signing), no source-IP allowlist, no rate limit |
| **Unlocked duplicate guards** | `mpesa_service.go:111`, `wallet_service.go:232` | Double credit under concurrent retries, as in §3 |
| **No idempotency key** | deposit path | A double-tapped button is two real charges |
| **Daraja error text to Sentry** | `client.go:121`, `:182` wrap the response body; `fail`'s default branch calls `CaptureBug` | Response bodies, not credentials — but worth knowing they leave the process |
| **No pending-deposit timeout** | nothing expires a `pending` deposit | Sandbox always calls back, so this never showed |
| **`humanResult` covers six codes** | `mpesa_service.go:174-196` | Users see raw Safaricom English for anything else |

---

## Minimum safe path to a first live test

The smallest contained way to place one real transaction, touching nothing shared.

**Understand first:** only a **deposit** can be tested. Withdrawal is not implemented, so a live
withdrawal test would move your money out of the app balance and pay nothing back.

1. **Fix the double-credit race first.** It is a two-line change — `WHERE status = 'pending'` on
   `MpesaRepo.UpdateResult` and on the transaction update inside `settleDeposit` — and it is the
   one defect where a Safaricom retry, which is normal production behaviour, costs real money.
   Everything else on this list can be lived with for one manual test; this cannot.
2. **Use a separate deployed instance with its own database.** Not your laptop, and not the
   instance anyone else is using. It needs a public HTTPS URL for the callback, which rules out
   local unless you tunnel. Set `APP_ENV=production` (which forces `COOKIE_SECURE` and
   `METRICS_TOKEN`), `USE_MOCK_MPESA=false`, and all six `MPESA_*` values — including
   `MPESA_SHORTCODE`, which otherwise silently stays on Safaricom's sandbox till.
3. **Lower the ceiling before the first push.** Set `MAX_DEPOSIT_KES=20` on that instance. The
   default is 150,000, there is no per-day cap, and there is no idempotency — so the ceiling is
   the only thing bounding a mistake or a double tap. Raise it after the flow is proven.
4. **Use your own phone number**, and register the test user with it. Nothing in the app
   constrains this; it is the difference between charging yourself and prompting a stranger.
5. **Deposit the minimum once** — KES 50 at the default floor, or whatever `MIN_DEPOSIT_KES`
   allows. Then check, in order: the STK prompt arrives; the callback lands (the transaction
   leaves `pending`); the balance moves by exactly `amount_kes / KES_PER_USD`; the ledger row
   carries the real `receipt`; and `ledger_drift_accounts` is still 0.
6. **Never point `scripts/smoke.py` or the Playwright suites at that instance.** Both request
   KES 12,950 to a randomly generated Safaricom number. Neither checks what it is talking to.
7. **Leave CI and every other environment alone.** `USE_MOCK_MPESA=true` in `ci.yml:159` and
   `:267` and in each developer's `.env`. This investigation changed nothing; there is nothing to
   revert.

If the callback does not arrive, there is no query API to ask Safaricom what happened and no job
that will retry — the deposit stays `pending` and the money is gone from the payer's side. Know
that before sending the first push, not after.
