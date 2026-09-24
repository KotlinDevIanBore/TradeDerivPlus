# Phase 1 · Checkpoint 3 — API, flags, metrics

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Plan:** `docs/investigation-reports/auto-bot-plan.md` §2.7–2.9, §3.1
**Scope:** HTTP surface, feature flags, observability, seed. No UI.

---

## 1. Rate buckets — the question asked before this checkpoint

**Confirmed: quote GETs stay in the loose group, as §3.1 specifies.** The placement bucket
(`rate.Every(500ms)`, burst 5) now holds four routes and nothing else:

```
placement.POST("/trades")              ← Phase 0
placement.POST("/digit-contracts")     ← Phase 0
placement.POST("/bot/runs")            ← this checkpoint
placement.POST("/bot/runs/:id/stop")   ← this checkpoint
```

Everything else about runs is a read and stays in the group's loose bucket, alongside
`GET /digit-contracts/quote`. Measured against the running API, one fresh user per burst:

| 30 concurrent requests | Result |
|---|---|
| `GET /digit-contracts/quote` | `{200: 30}` — none refused |
| `GET /bot/runs` | `{200: 30}` — none refused |
| `GET /bot/runs/:id` | `{404: 30}` — all reached the handler |
| `POST /bot/runs` | `{422: 5, 429: 25}` — exactly the burst of 5, then refused |

The 422s are deliberate: the burst posts a body with no `stop_loss`, so the bucket is consumed but
nothing is started. Five through and twenty-five refused is the bucket doing precisely its job.

Two notes on why this split is right. A ticket asks for a quote on **every keystroke** of the stake
field, so throttling it would blank a payout the user is reading. And a client polling a live run
must never be throttled into blindness — being unable to see that your bot is losing money is worse
than being unable to start another one. Stopping a run is in the tight bucket because it is a write,
but it is idempotent, so a refused retry costs nothing.

**One measurement mistake worth recording.** My first pass ran all three bursts against one user and
reported `GET /bot/runs → {200: 1, 429: 29}`, which looks like a misconfiguration. It was not: the
group bucket is per user and shared by every protected route, so the 60 requests of the first two
bursts had already drained it. Re-measured with a fresh user per burst, which is the only way to
attribute a 429 to the bucket under test.

---

## 2. What was built

### API (§2.7)

| Method | Path | Bucket | Notes |
|---|---|---|---|
| `POST` | `/bot/runs` | placement | 201 with the run |
| `POST` | `/bot/runs/:id/stop` | placement | idempotent; 200 with the row |
| `GET` | `/bot/runs?account_id=&status=live\|stopped` | group | paginated like the digit list |
| `GET` | `/bot/runs/:id` | group | ownership-checked |
| `GET` | `/admin/bot/runs` | admin read | live runs |
| `POST` | `/admin/bot/kill` | admin `actions` | reason ≥ 10 chars, audited |

`internal/handlers/bot_handler.go`. Money arrives as strings, never JSON numbers, matching the digit
request. `limitsRequest` distinguishes an absent `take_profit` (allowed — no take profit) from a
typed zero (a mistake the service names), which a plain `decimal` field could not.

Sentinels mapped in `fail`, completing what Checkpoint 2 deferred: `bot_disabled` 403,
`bot_real_money_disabled` 403, `bot_run_active` 409, `bot_capacity` 503.

`service.ValidationFor` was added as the exported form of `invalid`, for the two validations a
handler must do itself because they are about the request's shape rather than the domain (a stop loss
that is not a number).

### Flags (§2.8)

`/auth/me` now carries `bot_enabled` and `bot_real_money_enabled`. `bot_enabled` is **already
narrowed by the allowlist** — it answers "may this user start a run", not "is the feature switched on
somewhere", so the client never has to combine two flags to decide whether to show a control.

### Kill switch

`internal/service/admin_bot.go`, kept out of `admin_service.go` because everything there is about one
user's row and this is about the platform. `KillBots` does three things in **one transaction**: stop
every live run, write `bot_enabled=false`, write the audit row. After the commit, in this order: bust
the settings cache so nothing reads a stale "enabled", then stop the goroutines.

Turning the flag off is part of the kill deliberately. Stopping the runs alone would leave users free
to start new ones immediately, which is not what anyone reaching for a kill switch means.

`models.ActionBotKill` and `models.TargetPlatform` are new — the existing targets are all a single
user's row, and this action has no such target.

### Metrics (§2.9)

| Metric | Type |
|---|---|
| `bot_runs_live` | gauge, sampled from `BotRuns.CountLive` |
| `bot_contracts_total{outcome}` | counter — `placed`, then `won`/`lost`/`void` |
| `bot_runs_stopped_total{reason}` | counter |

`bot_runs_live` is sampled **from the table, not from `BotService`**, so a run this process has
orphaned still shows up — which is the case worth alerting on. `Dashboard.LiveBotRuns` was added as a
field only; the admin UI is Phase 3.

### Seed

`cmd/seed` sets `bot_enabled=true`, so a developer and CI can exercise runs without a SQL statement.
Unconditional there only because the command already refuses to run outside development.
**Real money stays off even in development**: a local run should not practise on the one path nobody
has reviewed.

---

## 3. Deviations from the plan

Only one, and it is a subtraction: **`?bot_run_id=` on the digit list was not added.** §2.7 lists it
as "one more predicate in `digitListWhere`", but its only consumer is a Phase 2 screen showing a run's
contracts, and adding a filter with no caller means shipping an untested query. The repo already has
`CountOpenByRun` for the loop, and `GET /bot/runs/:id` plus the existing account-scoped list cover
everything Phase 1 needs. It belongs with the screen that reads it.

Everything else in §2.7–2.9 is present. `web/src` was not touched at all: the `/auth/me` additions are
additive JSON that existing clients ignore, and the `AccountEvent` union is explicitly Phase 2.

---

## 4. Verification

### Build, vet, format, tests

```bash
gofmt -l ./cmd ./internal ./pkg      # no output
go build ./...
go vet ./...
go vet -tags integration ./internal/service/
go test ./...                                      # all ok
go test -tags integration ./... -count=1 -timeout 900s
# ok  internal/service  19.018s
```

### CI scripts

```bash
python3 scripts/smoke.py          # ALL CHECKS PASSED
python3 scripts/sltp_check.py     # STOP LOSS / TAKE PROFIT AUTO-CLOSE VERIFIED
python3 scripts/admin_check.py    # ADMIN CONSOLE VERIFIED
go run ./scripts/wscheck http://localhost:8080/api/v1 6   # all pass
```

`admin_check.py` first failed with "seeded admin cannot log in" — the credential drift this script
causes itself and documents (it leaves the password as `e2eConsoleAdmin1`). `go run ./cmd/seed
-reset-admin` then a re-run passes. Unrelated to this checkpoint.

### The flag gates the service, not just the surface

Before enabling anything, against a freshly bootstrapped database:

```
me flags -> {'digit_contracts_enabled': True, 'bot_enabled': False, 'bot_real_money_enabled': False}
start while disabled -> 403 {'code': 'bot_disabled'}
```

This is the difference from `digit_contracts_enabled`, which gates only the UI: a bot cannot be
started by going around the client.

### A full run over HTTP

```
start -> 201 running
second run -> 409 bot_run_active
   ('running', 0, 0, 0, 0, '0', None)
   ('running', 1, 0, 1, 0, '-10', None)
   ...
   ('stopped', 6, 3, 3, 0, '-1.44', 'max_contracts')
wallet -> 0 9998.56
contracts: 6 all attributed: True
```

10,000 − 1.44 = 9,998.56 exactly. The progression also shows the two-transaction sequence:
`contracts_placed` rises at open, the outcome lands at settle.

### Phase 1 exit criterion, checked verbatim

> *A manual curl-started run on the mock feed stops on its stop-loss with `ledger_drift_accounts` at 0.*

```
stopped -> stop_loss placed 3 W/L/V 0 3 0 realised -30
wallet bonus -> 9970 (started 10000)
```

And from `/metrics` (bearer token required, as it always has been):

```
bot_contracts_total{outcome="lost"} 6
bot_contracts_total{outcome="placed"} 9
bot_contracts_total{outcome="won"} 3
bot_runs_live 0
bot_runs_stopped_total{reason="max_contracts"} 1
bot_runs_stopped_total{reason="stop_loss"} 1
ledger_drift_accounts 0
```

Nine placed, nine outcomes, drift zero. The stop-loss run used `matches_differs` on one digit, which
wins about one time in ten, so the stop is reached quickly rather than depending on a coin landing the
same way repeatedly.

### Kill switch

```
admin sees live runs -> 200 1
kill without a real reason -> 422 validation_failed | Give a reason of at least 10 characters
kill -> 200 {'bot_enabled': False, 'runs_stopped': 1}
run after kill -> stopped admin_kill placed 0
bot_enabled now -> False
start after kill -> 403 bot_disabled
audit -> [('bot_kill', 'platform', 'Stopping automated trading for a payout table review',
           {'runs_stopped': 1})]
```

The run stopped immediately rather than after the settings cache's ten seconds, which is what
`BustCache` plus `StopAll` after the commit are for. `bot_enabled` was re-enabled afterwards
(`go run ./cmd/seed`) so the dev database is left usable.

---

## 5. Not done in this checkpoint, by instruction

No UI of any kind — no `bot_run` in the client's `AccountEvent` union, no types, no admin screens, no
dashboard tile rendering. The remaining §8 integration tests and `scripts/bot_check.py` are
Checkpoint 4.
