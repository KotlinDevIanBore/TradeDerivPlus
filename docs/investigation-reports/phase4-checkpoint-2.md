# Phase 4 · Checkpoint 2 — load

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** §6 Phase 4's load run — 50 concurrent runs, 10 minutes, mock feed — plus the
`bot_runs` lock added in Checkpoint 1, which did not exist when this was scoped.

Numbers, not verdicts. The verdicts are at the end and each cites the number it rests on.

---

## 1. What was run

50 runs, one per account, $1 stake, 2 s interval, limits set out of reach so nothing stopped
itself. `BOT_MAX_RUNS` is 50, so this is the cap, not a sample of it.

Alongside them, real WebSocket clients: 10 on `/ws/prices?symbols=R_75` and 50 on `/ws/account`,
one per run's owner, held for the duration. Without them the fan-out has nothing to fan out to and
the most-cited risk would have gone unmeasured.

Three passes in all: the 10-minute run, a 3-minute repeat that fixed the fan-out measurement (see
§4), and a 2-minute pass dedicated to sampling locks at 100 ms.

---

## 2. Throughput and settlement

| | |
|---|---|
| Runs live | **50**, from t=61 s to the end of the run, at every sample |
| Contracts placed (counter delta) | **10,166** |
| Contracts settled in the window (from the rows) | **10,411** |
| Steady state | **1,000 per minute — 16.7/s** |
| Voids | **0** |
| `ledger_drift_accounts` | **0** |
| Contracts left open at the end | **0** |

Settlement latency, measured as `settled_at − opened_at` over the 10,411 contracts:

| avg | p50 | p90 | p99 | max |
|---|---|---|---|---|
| 0.909 s | 0.927 s | 0.986 s | **1.006 s** | **1.013 s** |

**The floor is about 1 s and the p99 is sitting on it.** The mock feed ticks every 500 ms and
stamps whole-second epochs, and a contract settles on the first epoch strictly greater than its
own — so ~1 s is what a contract costs with nothing in its way. A p99 of 1.006 s and a maximum of
1.013 s over ten thousand contracts means there was essentially nothing in the way.

Per minute, which is what says whether it degrades:

| minute | placed | p50 | p99 | max |
|---|---|---|---|---|
| 1 (ramping) | 445 | 0.870 | 1.003 | 1.004 |
| 2 | 1000 | 0.918 | 1.004 | 1.006 |
| 3 | 1001 | 0.941 | 1.005 | 1.007 |
| 4 | 1000 | 0.937 | 1.009 | 1.013 |
| 5 | 1000 | 0.930 | 1.008 | 1.012 |
| 6 | 1000 | 0.929 | 1.005 | 1.011 |
| 7 | 1000 | 0.930 | 1.007 | 1.012 |
| 8 | 1000 | 0.929 | 1.009 | 1.013 |
| 9 | 1000 | 0.912 | 1.003 | 1.009 |
| 10 | 1000 | 0.928 | 1.004 | 1.006 |

Flat. Exactly 1,000 a minute, p99 between 1.003 and 1.009 throughout, no trend.

### Resources

| | start | end |
|---|---|---|
| API process CPU | 3.3 % | 4.4 % |
| API process RSS | 38.1 MB | 38.2 MB |
| API OS threads | 14 | 14 |
| Postgres CPU | 10 % | 7.8–18.6 % across samples |
| Postgres memory | 111 MB | 111 MB |
| Connection pool (max 20) | **20 idle / 1 active at every single sample** | |

The pool line is the one that answers §2.3's open question about polling. 50 runs polling every
250 ms is 200 primary-key reads a second, and the pool was never more than one connection busy
when sampled.

---

## 3. The `bot_runs` lock

Checkpoint 1 put a row lock on every placement and every stop. This did not exist when the load
run was scoped, so it got its own measurement rather than being folded into "settlement latency".

| Sampling | Lock waits seen | Ungranted `bot_runs` locks |
|---|---|---|
| Every 15 s, across both long runs (≈ 50 samples) | **0** | **0** |
| **Every 100 ms, 1,253 samples over 120 s at full load** | **0** | **0** |
| **Every 100 ms, 903 samples across the mass-stop of all 50 runs** | **0** | **0** |
| `pg_stat_database.deadlocks` | **0** | |

A zero is only worth reading if the instrument can produce a non-zero, so the sampler was checked
against a deliberate wait: a transaction holding one run's row for six seconds while the API was
asked to stop that run.

> `samples=59 lock_wait_observations=138 botrun_ungranted_observations=41 longest_wait_s=5.903`

It sees waits. Under load there were none.

**Why that is structural, not luck.** The lock is on the run's *own row*. Fifty concurrent runs
contend on fifty different rows — they cannot block each other at all. The only contention the
design admits is between a single run's placement and a stop *of that same run*, and a run places
sequentially, so at most two parties ever want one row. That is exactly the serialisation the lock
was added for, and it is the two-party case the Checkpoint 1 tests pin.

This also means the lock's cost does not grow with the number of runs, which is the property worth
having before this scales past 50.

---

## 4. The fan-out, and a correction to how the plan proposed measuring it

**§6 asks to watch `http_request_duration_seconds` on the `/ws/prices` fan-out. That metric cannot
answer the question.** The handler returns as soon as the hub has registered the connection — the
socket is then served by the hub's own goroutines — so the histogram holds *upgrade handshakes*:

> 74 observations, 16.19 ms total — **0.22 ms mean**, every one under the 1 s bucket.

True and healthy, and it says nothing about whether ticks arrive on time. So the fan-out was
measured where it actually happens, at the clients: 10 sockets timestamping every tick frame.

| tick frames | p50 gap | p90 | p99 | max |
|---|---|---|---|---|
| 3,420 | **0.500 s** | 0.505 s | **0.532 s** | 0.991 s |

The feed ticks every 500 ms. The median gap is exactly that. p99 is 32 ms of jitter, and the
single 0.991 s maximum is one interval's worth of coalescing in 3,420 frames.

This is the plan's stated biggest risk (§1: *"the settlement hot path… every bot contract is
settled by `DigitService.onTick` on the single feed goroutine"*), and it is the number that
retires it at this scale: with 16.7 contracts a second settling on that goroutine, the ticks it
also has to broadcast were late by tens of milliseconds at the 99th percentile.

The per-user side was exercised too — **46,708 `/ws/account` frames** delivered across 50 clients
during the 10-minute run, with `ws_connections` reporting 60.

The first attempt at this measured zero tick frames, because a `/ws/prices` client that sends no
`symbols` is subscribed to nothing. That was my error in the harness, not the server's, and it is
the reason there was a second pass.

---

## 5. The `OnSettled` hook — not added

§2.3 left it open, to be added "only if the poll shows up in the profile". It does not:

- the connection pool was **20 idle / 1 active** at every sample, against 200 polls a second;
- settlement p99 was **1.006 s** against a **~1 s floor**, so there is no queue for a callback to
  drain;
- Postgres sat between 8 % and 19 % of one core and the API below 5 %.

Adding it now would be speculative complexity on the money path's neighbour, and it would still
need the poll as a fallback for lost wakeups — the reason §2.3 gave for not starting with it.
**Revisit at a few hundred runs**, as the plan says, and the number to watch is active pool
connections rather than latency: the poll will exhaust connections before it slows anything down.

---

## 6. Two things the load run found

Neither affects the numbers above; both are on the deferred list.

1. **`bot_contracts_total` counts every contract twice** — once as `outcome="placed"`, once as its
   real outcome. Measured: 10,166 placed against 4,905 won plus 5,278 lost. Summing the metric
   across labels therefore doubles the true count, and the plan's own runaway alert
   (§2.9, `rate(bot_contracts_total[1m]) > bot_runs_live * 60`) does exactly that — it would fire
   at half the rate it was designed for.
2. **`bot_placement_errors_total` was never built.** It is in §2.9's table and absent from
   `metrics.go`. Nothing alerts on placement failures by code. This run produced none, so it cost
   nothing here, but it is the metric that would distinguish a run that cannot place from a run
   that simply is not placing.

---

## 7. Verdicts

- **50 concurrent runs is comfortable on this hardware.** Settlement p99 is at its theoretical
  floor, throughput is exactly what the cadence predicts, and nothing degraded across ten minutes.
- **Checkpoint 1's lock costs nothing measurable**, and cannot cost more as runs are added,
  because it is per-run.
- **The settlement hot path is not the bottleneck it was flagged as** — at this scale. The plan
  was right to flag it and right to cap runs at 50; what the cap is actually protecting is not
  visible yet at the cap.
- **Nothing here displaces Checkpoint 3.** The prompt asked me to say so explicitly if the load
  numbers suggested something more urgent than the Runs tab. They do not: the two findings in §6
  are observability tickets, not fires, and the profile is quiet. Checkpoint 3 remains a
  worthwhile optional piece rather than something being done instead of real work.

### What was not measured

A real Deriv feed (1–2 s ticks rather than 500 ms), more than 50 runs, a cold cache, or a database
under concurrent load from anything else. The mock feed's tick cadence sets the floor every number
here sits on, and on a live feed that floor roughly doubles while the work per contract stays the
same.

**Checkpoint 4 remains gated on the §10 answers, which have not been given. Nothing in these
numbers bears on that decision** — the question there is whether a bot should trade real money
against a placeholder payout table, and a healthy profile is not an argument about it.

---

## 8. Commands

The harness is in the session scratchpad rather than in `scripts/`: it is a measurement, not a
check that should run in CI, and it takes ten minutes.

| | |
|---|---|
| `bot_load.py` | registers N users, starts a run each, samples `/metrics` and `pg_stat_activity` every 15 s, then stops every run |
| `wsload/` (Go, `gorilla/websocket`) | holds the price and account sockets and reports tick inter-arrival percentiles |
| `lockwatch.sh` | samples lock waits and ungranted `bot_runs` locks every 100 ms |

After every pass: all runs stopped, no contract left open, `ledger_drift_accounts` 0.
