# Money Mechanics Pre-Launch Fixes — Implementation Report

**Scope:** Findings 1–4 of [`docs/investigation-money-mechanics.md`](investigation-money-mechanics.md), the four items that audit lists as blockers before `withdrawals_enabled` is turned on.
**Date:** 26 September 2026 · **Branch:** `master` @ `a672bd7` · **Working tree, not yet committed.**
**Not in scope:** Findings 5–7 and the Safaricom deduplication question (§7).

> Style note: this follows `docs/implementation-reports/desktop-withdraw-button.md`, which is
> where that report actually lives — the brief referred to it as
> `docs/implementation-desktop-withdraw-button.md`.

---

## 1. Classification under the standing verification rule

**This is a money-path change, and was verified as one.** Finding 4 alters the arithmetic that
decides how much is debited from a customer and how much Daraja is asked to send — a ledger
write and a payout amount. That puts the whole pass on the full standard regardless of
Findings 1–3 being config and UI:

- Full Go integration suite (86s, not a filtered run).
- **Mutation checks on both new guards**, each proven applied, compiled, and killing a test
  that reproduces the exact bug from the audit (§6.2).
- A third mutation on the UI change, because a test that passes without the code it tests is
  not evidence (§6.3).

---

## 2. Summary

Four findings, four fixes, one of them arithmetic and three of them the machinery around it.

| # | Finding | What it is now |
|---|---|---|
| 1 | Unknown payouts had no alarm | Two `critical` Prometheus rules, plus a documented response procedure (§4.1) |
| 2 | Alerting configured but not deployed | Off the compose profile, Alertmanager added and **proven to route**, production scrape job, plus a scheduled GitHub Actions check that needs no host (§4.2) |
| 3 | `WithdrawSheet` had no demo state | Demo branch above every other gate; `BalanceCard`'s hide-entirely removed; 4 new tests |
| 4 | No withdrawal floor, `Ceil` bias | `MIN_WITHDRAW_KES`, validated at boot; the amount normalised to cents **before** any KES arithmetic |

**The one thing to read if you read nothing else:** the alert rules and the Alertmanager added
here still only run while something is running them, and production is Railway. The piece that
actually closes Finding 2 today is [`.github/workflows/money-watch.yml`](../.github/workflows/money-watch.yml),
which runs on GitHub's schedule and needs no host — and it is a **stopgap, not the target
state**. §4.2 and §8 say so in detail.

---

## 3. Finding 4 — the withdrawal floor and the rounding bias

### 3.1 The minimum, and the number I could not confirm

**The brief asked me to confirm Daraja's documented B2C minimum rather than guess. I could
not confirm it, so I have not pretended to.** Safaricom's public B2C API page returns 404 and
the developer portal requires an account. What the public record does give:

| Figure | Source |
|---|---|
| KES 250,000 max per transaction, KES 500,000 per day | [Safaricom M-PESA tariffs & limits](https://www.safaricom.co.ke/main-mpesa/m-pesa-for-you/tariffs-limits/consumer-tariffs-limits), [paybillke summary](https://paybillke.com/guides/mpesa-transaction-limits-2026) |
| KES 50 minimum agent withdrawal | as above |
| Daraja accepts integer amounts only | [django-daraja B2C docs](https://django-daraja.readthedocs.io/en/latest/pages/apis/b2c_payment.html) |
| No B2C-specific **minimum** stated anywhere public | — |

So `MIN_WITHDRAW_KES` defaults to **50**, chosen to match this project's existing
`MIN_DEPOSIT_KES` and the documented KES 50 minimum agent withdrawal, and labelled
**PROVISIONAL** in the code the way the existing ceilings already are
([`internal/config/config.go:174-187`](../internal/config/config.go)). The comment says
explicitly that the B2C figure is unconfirmed and must be checked with Safaricom before
launch. It is one environment variable to change.

### 3.2 What changed

| File | Change |
|---|---|
| [`internal/config/config.go:187`](../internal/config/config.go) | `MinWithdrawKES float64` with the provenance comment above it |
| `internal/config/config.go:297` | `getFloat("MIN_WITHDRAW_KES", 50)` |
| `internal/config/config.go:391-399` | Validation: **refuses** `<= 0`, and refuses a floor above `MaxWithdrawKES` |
| [`internal/service/wallet_service.go:552-571`](../internal/service/wallet_service.go) | Normalise to cents, then the floor check, then the existing ceiling |
| `internal/service/wallet_service.go:602,622,658` | Balance check, ledger amount and debit all switched to the rounded figure |
| [`.env.example`](../.env.example) | `MIN_WITHDRAW_KES=50` with the same provenance caveat |
| `internal/service/integration_setup_test.go` | Harness default `MinWithdrawKES: 50` |

**A zero floor is refused at boot rather than defaulted.** `MIN_WITHDRAW_KES=0` is not "no
minimum" — it is the guard silently absent, which is the state Finding 4 described. The
validation message says so.

### 3.3 The rounding fix, precisely

The bug was that two figures were derived from *different* inputs:

```go
// before: the debit rounded to cents by NUMERIC(18,2); the KES amount did not
amountKES := s.usdToKES(in.AmountUSD)   // $10.004 -> 1295.518 -> Ceil -> 1296 KES sent
// ...
Amount: in.AmountUSD.Neg(),              // stored as -10.00 by the column
```

```go
// after: one rounded figure feeds everything
amountUSD := in.AmountUSD.Round(2)       // $10.004 -> $10.00
amountKES := s.usdToKES(amountUSD)       // -> 1295.00 -> Ceil -> 1295 KES sent
```

All five uses of `in.AmountUSD` below the normalisation were converted — the zero check, the
KES conversion, the balance comparison, the ledger row and the wallet debit. I grepped to
confirm none survives.

**One honest detail about the floor's scope.** The audit's headline case — withdraw `$0.001`
— is actually caught by the *normalisation*, not the floor: it rounds to `$0.00` and the
pre-existing `<= 0` check refuses it. This is visible in the mutation result (§6.2, M1),
where removing the floor left the `$0.001` assertion passing and only the `$0.38` one failed.
The floor's real job is the band between "rounds to something" and "too small for the `Ceil`
to be immaterial".

---

## 4. Findings 1 and 2 — the alarm and its delivery

Implemented together, as the brief required: a rule with nowhere to go is not an alarm.

### 4.1 The rules, and who answers them

Two rules in [`deploy/prometheus/rules.yml:27-52`](../deploy/prometheus/rules.yml), matching
`LedgerDrift`'s `severity: critical`:

| Alert | Expression | `for:` |
|---|---|---|
| `PayoutOutcomeUnknown` | `mpesa_payouts_unknown > 0` | **none** |
| `PayoutGivenUp` | `mpesa_payouts_given_up > 0` | **none** |

**Neither has a settling period, deliberately**, and that is a difference from the existing
rules. `LedgerDrift` waits 2m to let a sampling race settle and `DepositFailureRate` waits
10m; these two cannot resolve themselves at all — the code refuses to guess an unknown
outcome by design — so there is no period worth waiting out.

**The response procedure.** The brief asked for this in the report even though the on-call
arrangement is a decision outside the codebase. Stating it as what the code supports:

1. **Who responds:** whoever holds operations for payouts. There is no rota today, and this
   pass does not create one — that is the open decision in §8. Until it exists, the
   alert's destination is the Slack channel in §4.2 and its owner is whoever reads it.
2. **How they confirm:** take the `OriginatorConversationID` from the payout row (the admin
   console shows it on the withdrawal; `mpesa_payouts.originator_conversation_id` otherwise)
   and look it up in the **Safaricom B2C web portal** under Transaction Status. The portal is
   the authority, not this system — the whole reason the state is `unknown` is that this
   system could not get an answer.
3. **Then resolve it in the console**, which writes through the same guarded path a callback
   would use: `paid`, `failed`, or `never_received`. `never_received` is for the case where
   Safaricom never had the request, and is the only one where a refund is provably safe.
   The distinction is load-bearing — `ResolvePayout` refuses a non-open payout, and the
   alert's own description says "do not guess: a wrong refund pays twice".
4. **Target resolution time: same business day**, and this is a proposal rather than an
   agreed SLA. The reasoning: the customer's balance is already debited, so every hour is an
   hour they are short with no explanation in the app. Nothing in the code enforces this.

### 4.2 Delivery, and exactly how far it actually goes

| Change | File |
|---|---|
| Prometheus off `profiles: ["observability"]` | [`docker-compose.yml:22-34`](../docker-compose.yml) |
| Grafana off the profile too, for a coherent stack | `docker-compose.yml:42-45` |
| **Alertmanager service added** (was absent entirely) | `docker-compose.yml:60-79` |
| Alertmanager routing config | [`deploy/alertmanager/alertmanager.yml`](../deploy/alertmanager/alertmanager.yml) |
| `alerting:` block pointing Prometheus at it | [`deploy/prometheus/prometheus.yml`](../deploy/prometheus/prometheus.yml) |
| **Production scrape job** against Railway over HTTPS with bearer auth | `deploy/prometheus/prometheus.yml` |
| Webhook URL gitignored, `.example` committed | [`.gitignore`](../.gitignore), `deploy/alertmanager/slack_webhook_url.example` |
| **Scheduled invariant check needing no host** | [`.github/workflows/money-watch.yml`](../.github/workflows/money-watch.yml) |

**Slack was chosen over PagerDuty and email** because it is the least infrastructure that puts
a critical money alert in front of a person: one incoming-webhook URL, no account
provisioning, no SMTP relay, no paid subscription, no rota to configure first. The receiver is
one block to swap if a pager is wanted later.

**Two things I got wrong on the first attempt and fixed, both found by running it:**

1. I passed the webhook as `SLACK_WEBHOOK_URL: ${SLACK_WEBHOOK_URL:-}`. **Alertmanager does no
   `${VAR}` expansion in its config, and an empty `api_url` is a load-time error** — the
   container exited 1 with `unsupported scheme "" for URL`. It now uses `api_url_file`,
   reading `deploy/alertmanager/slack_webhook_url`, which is the same convention this repo
   already uses for `deploy/prometheus/metrics_token`. Because the file is read at
   notify-time, a missing one leaves Alertmanager **running and showing alerts in its own UI
   while unable to deliver them** — a visibly half-configured state rather than a dead one.
2. Mounting a single missing file would have made Docker create a directory in its place, so
   the whole `deploy/alertmanager` directory is mounted read-only instead.

**How far this actually goes — the part not to gloss over.** A compose stack alerts only while
someone is running it, and production is Railway. Moving Prometheus off the profile makes it
start by default *locally*; it does not deploy anything. That is why
`.github/workflows/money-watch.yml` exists: every 30 minutes it reads the authenticated
production `/metrics`, and **fails the job** if `ledger_drift_accounts`,
`mpesa_payouts_unknown` or `mpesa_payouts_given_up` is above zero. A failed scheduled workflow
emails the repository owner by default, so it is a real notification path with no server.

Three deliberate choices in that workflow:
- **A missing `METRICS_TOKEN` secret fails the job**, loudly, rather than skipping quietly. An
  alarm that passes while not monitoring is the Finding 2 problem wearing a different hat.
- **An absent metric is a warning, not a pass.** A series that was never set is not zero, and
  reporting all-clear for it would be a lie.
- It says in its own header comment that it is a stopgap and should be deleted when hosted
  monitoring exists.

---

## 5. Finding 3 — the demo-account state

| File | Change |
|---|---|
| [`web/src/features/wallet/WithdrawSheet.tsx:104-126`](../web/src/features/wallet/WithdrawSheet.tsx) | Demo branch **above** the `withdrawals_enabled` and KYC gates; new `realAccount` / `onSwitchToReal` props |
| [`web/src/features/shell/AppShell.tsx:242-252`](../web/src/features/shell/AppShell.tsx) | Passes both, reusing the `realAccount` it already computes for `DepositSheet` |
| [`web/src/features/wallet/BalanceCard.tsx:91-99`](../web/src/features/wallet/BalanceCard.tsx) | `!isDemo` hide-entirely removed |
| `web/src/features/wallet/WithdrawSheet.test.tsx` | **New**, 4 tests |

**It offers the switch; it never performs the withdrawal elsewhere.** `DepositSheet` routes a
demo deposit to `realAccount` automatically, which is right for money coming *in*. For money
going *out*, silently retargeting to an account the user was not looking at is not a courtesy,
so the sheet explains and offers a button. With no real account to switch to, it explains and
offers Close.

**One consequence of removing the hide that needed handling.** `BalanceCard` suppresses its
`withdrawHint` on demo (`:101`, pre-existing). Had I only deleted the `!isDemo` wrapper, a
demo user would have got a *disabled* Withdraw button with no hint beside it — a dead control
explaining nothing, which is the exact failure being removed, just relocated. So on demo the
button is never disabled (`disabled={!isDemo && !canWithdraw}`): demo reaches the sheet, and
the sheet says why. Real accounts keep their existing disabled-plus-hint behaviour unchanged.

---

## 6. Verification

### 6.1 Results

| Check | Result |
|---|---|
| `gofmt -l internal/ cmd/` | clean |
| `go build ./...` | exit 0 |
| `go vet ./...` and `go vet -tags integration ./internal/service/` | exit 0 |
| Go unit suite, `go test ./...` | **exit 0**, 5 packages ok |
| **Go integration suite, `go test -tags integration ./internal/...`** | **exit 0**, service package 86s, no FAIL |
| `TestWithdrawalFloor` (new) | PASS |
| `TestWithdrawalDebitAndPayoutAgreeOnTheAmount` (new) | PASS |
| `TestWithdrawalCeilings`, `TestWithdrawalDailyCapCountsPendingToo` (existing, adjacent) | PASS |
| `npm run typecheck` | exit 0 |
| `npm run build` | exit 0 |
| `npx vitest run` | **134 passed, 14 files** (was 130/13) |
| `WithdrawSheet.test.tsx` (new) | 4 passed |
| YAML validity: `rules.yml`, `alertmanager.yml`, `prometheus.yml`, `money-watch.yml`, `docker-compose.yml` | all parse |
| `bash -n` on the workflow's script | ok |

Exit codes were read directly, not inferred from a truncated tail.

Also worth recording: the two bot integration tests that were intermittently red in CI on
PR #13 (`TestBotKillSwitchStopsAllRuns`, `TestAdminStopsOneRunAndRecordsIt`) **passed** in this
run. That is one data point, not a fix, and they remain open work.

### 6.2 Mutation checks on the money-path guards

Both proven applied, compiled (`go vet`) before running, and restored byte-identically
(`sha256sum -c` against a baseline taken first).

| # | Mutation | Test | Result |
|---|---|---|---|
| M1 | Delete the `MinWithdrawKES` check | `TestWithdrawalFloor` | **KILLED** — `withdrawing $0.38 (KES 49.21) returned <nil>, want a validation error on amount` |
| M2 | Derive `amountKES` from the unrounded `in.AmountUSD` | `TestWithdrawalDebitAndPayoutAgreeOnTheAmount` | **KILLED** — `payout is KES 1296, want KES 1295 — the customer is sent a different amount than was debited` |

M2 reproduces the audit's arithmetic exactly: 1296 against 1295 on a `$10.004` request. After
restoring, both tests plus the two adjacent existing guard tests were re-run together and pass.

### 6.3 The UI test was checked the same way

M3: `if (isDemo)` → `if (false && isDemo)`. Result: **3 of 4 tests failed** (the three demo
assertions), and the real-account test still passed — so the test measures the demo branch
and not something incidental. Restored and hash-verified.

### 6.4 Local dev, verified by running it

`docker-compose config` validates, then `docker-compose up -d` with the real binary
(Compose v5.1.1):

- All four containers start; **no `profiles` remain** on any service.
- Prometheus loaded **all six rules**, the two new ones among them, read back from
  `/api/v1/rules` with `severity: critical`.
- Prometheus **discovered Alertmanager**: `/api/v1/alertmanagers` shows
  `http://alertmanager:9093/api/v2/alerts` active, none dropped.
- **The delivery path was proven end to end**, not assumed: a synthetic
  `PayoutOutcomeUnknown` posted to Alertmanager's v2 API came back `state: active`, routed to
  receiver `money-critical`. It was then expired (`endsAt` in the past) and confirmed gone —
  0 active alerts.
- The gauge-parsing logic from the workflow was run against a realistic `/metrics` sample:
  `0` → ok, `2` → error and non-zero exit, absent → warning. A counter with labels
  (`deposits_total{status="completed"}`) does not false-match.
- Afterwards the observability trio was stopped, leaving `tradederivplus-db` running as I
  found it. They start with `docker-compose up` by design now.

**The typechecker caught something vitest could not.** My first `WithdrawSheet.test.tsx`
fixture omitted `Wallet.total`. All 4 tests passed at runtime — vitest does not typecheck —
and `npm run typecheck` failed, which CI runs. Fixed before this report.

### 6.5 What was NOT verified

- **The production scrape does not authenticate yet.** `tradederivplus-api-production` shows
  **401 Unauthorized**: `deploy/prometheus/metrics_token` holds a development token, not
  Railway's `METRICS_TOKEN`. The job is wired and the endpoint is reachable — the 401 is
  itself confirmation that production `/metrics` is correctly protected — but until the real
  token is in that file and in the `METRICS_TOKEN` repo secret, **neither the Prometheus job
  nor the GitHub Actions check can see production**. This is the single highest-value
  follow-up action and it is one value in two places.
- **No alert has been delivered to Slack**, because no webhook URL exists here. Routing is
  proven to the receiver; the final hop is not.
- **`money-watch.yml` has never run**, since it only fires on GitHub's schedule or a manual
  dispatch on the default branch. Its script was syntax-checked and its parsing logic tested
  locally against sample metrics.
- **No real B2C payout was exercised.** `withdrawals_enabled` is still `false`, no production
  user is `verified`, and no Daraja payout credentials exist here. Everything about the
  withdrawal arithmetic is verified against the integration suite and a mock sender.
- **`MIN_WITHDRAW_KES=50` is unconfirmed against Safaricom's B2C minimum** (§3.1).
- **Prettier.** The touched web files already failed `prettier --check` on `master`, and there
  is no prettier config in the repo, so its defaults disagree with the house style — running
  `--write` converted single quotes to double throughout one file and I reverted it. CI runs
  `typecheck`, `test` and `build` only.

### 6.6 Flags untouched

`withdrawals_enabled` and `bot_real_money_enabled` remain `false`. Nothing in this pass reads
or writes either.

---

## 7. Explicitly still open, from the audit

Out of scope for this pass, unchanged, and each carrying its evidence in
[`investigation-money-mechanics.md`](investigation-money-mechanics.md):

| Finding | Item | Why it was deferred |
|---|---|---|
| 5 | **Before/after balance snapshots** on money rows | A schema and write-path change across every money path; reconstructing balances is possible today by replaying the ledger |
| 6 | **`CHECK` constraints** on `transactions.status` and `mpesa_payouts.status` | Needs a migration plus the `SELECT DISTINCT` pre-flight that 0010's deployment notes use; latent while the application is the only writer |
| 7 | **Source-IP allowlist** on the three callback routes | Depends on Safaricom publishing stable ranges; Daraja does not sign callbacks, so correlation plus the guarded UPDATE remains the control |
| — | **`OriginatorConversationID` deduplication**, confirmed with Safaricom | Needs sandbox credentials and a callback catcher. It is the fact that would let a payout be safely resent, and its absence is *why* an ambiguous send becomes Finding 1 rather than a retry |

---

## 8. What someone still has to do

Ordered by what blocks what. Items 1–2 are the difference between this pass having closed
Finding 2 and merely having prepared to.

1. **Put Railway's `METRICS_TOKEN` in two places**: `deploy/prometheus/metrics_token` and the
   `METRICS_TOKEN` GitHub repository secret. Until then nothing watches production (§6.5).
2. **Create a Slack incoming webhook** and write it to
   `deploy/alertmanager/slack_webhook_url` (gitignored; copy the `.example`). Then fire a test
   alert to confirm the last hop.
3. **Confirm `MIN_WITHDRAW_KES` against Safaricom's B2C minimum** and set it deliberately (§3.1).
4. **Decide who answers a `PayoutOutcomeUnknown`** and agree the resolution target. §4.1 is a
   proposal; nothing in the code enforces it and no rota exists.
5. **Replace the stopgap with hosted monitoring** — Grafana Cloud, or Prometheus deployed
   beside the API — and delete `money-watch.yml` when it exists.
6. Then, separately, the remaining pre-launch items from `kyc-r2-deployment-readiness.md`:
   the document retention period, the R2 bucket, and a production admin account.
