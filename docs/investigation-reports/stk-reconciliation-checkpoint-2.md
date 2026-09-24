# STK reconciliation · CP2 — schema, repo, seam, config

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** plan §2.2, §3.1–§3.3 — everything the pass will stand on, and no pass. Nothing in
this checkpoint can move money or write an outcome; it decides what the reconciler will see and
what it may ask.

---

## 1. What was built

### Migration `0007_mpesa_reconcile.up.sql` (§3.1)

Two columns on `mpesa_requests` — `reconcile_attempts INTEGER NOT NULL DEFAULT 0` and
`next_query_at TIMESTAMPTZ` — a backfill that schedules every already-pending row at its own
`created_at`, and the partial index `idx_mpesa_requests_pending_next ON (next_query_at) WHERE
status = 'pending'`, the shape `idx_digit_contracts_open_void` set in 0005. Terminal rows are
left `NULL`; nothing will look at them. No down file, matching 0004–0006.

The migration's comment fixes one meaning the plan left implicit: **`NULL next_query_at` on a
pending row means "never scheduled".** The pass skips such a row (`ListPending` requires
`next_query_at <= now`), and the stale-deposit gauge still counts it, because
`CountPendingOlderThan` keys on `created_at` and not on the schedule. That is the net under the
pass — a row it cannot see is still a row an operator hears about.

### Model and repo (§3.2)

`models.MpesaRequest` gains `ReconcileAttempts int` and `NextQueryAt *time.Time`.
`internal/repository/mpesa_repo.go` now has one select list (`mpesaSelect`) and one scanner, so
the two new columns reached every reader at once instead of being added to `GetByCheckoutID` and
forgotten by the next method. Beside the unchanged `UpdateResult`:

| Method | Statement | Note |
|---|---|---|
| `Insert` | now writes `next_query_at` | from the model; the service sets it to `now + MinAge` |
| `ListPending(ctx, now, limit)` | `WHERE status='pending' AND next_query_at <= $1 ORDER BY next_query_at LIMIT $2` | the pass's whole view of the world |
| `MarkQueried(ctx, id, attempts, next)` | `UPDATE … WHERE checkout_request_id=$1 AND status='pending'` | guarded; a no-op on a settled row, not an error |
| `CountPendingOlderThan(ctx, age)` | `WHERE status='pending' AND created_at < $1` | the gauge; sees unscheduled rows |

`MarkQueried`'s guard is the one safety property in this checkpoint. A callback can settle a row
between the pass reading it and the pass writing to it; the write must then touch nothing. It is
the same reason `UpdateResult` is guarded, and its comment says so.

### The seam (§2.2)

```go
type stkQuerier interface {
    STKQuery(ctx context.Context, checkoutID string) (mpesaclient.QueryOutcome, error)
}
```

`*mpesaclient.Client` satisfies it with no wrapper — the test checks the interface holds the
very pointer `s.client` holds. Mock mode gets `unresolvedQuerier{}`, which answers *"the
callback is on its way"* for every id: `Unresolved`, nil error. The stub cannot settle anything;
what mock mode will exercise once the pass exists is listing, backoff and `MarkQueried`, the
parts most likely to rot unseen. `SetQuerierForTest(q)` follows `SetPollInterval` /
`SettleForTest`. `NewMpesaService`'s signature is unchanged; `main.go:91` and the harness are
untouched.

`InitiateSTK` sets `NextQueryAt = now + cfg.MpesaReconcileMinAge` before `Insert`, so a row is
never eligible before the callback has had its chance.

### Config (§3.3)

`MPESA_RECONCILE_EVERY` (1m), `MPESA_RECONCILE_MIN_AGE` (3m), `MPESA_RECONCILE_GIVE_UP` (24h),
each marked **provisional** in the struct comment and in `.env.example`, where they are
commented out beside the M-Pesa block. `config.MpesaReconcileMinAgeFloor = 60s` is applied in
`Load`: whatever is typed, `MinAge` is at least a minute. The boot log line now prints the three
effective values, so a deployment can be read off its first log line.

**Two validations beyond §3.3**, both refusals at boot rather than surprises later:
`EVERY <= 0` is an error (a zero ticker panics at `Start`, and there is no "off" in the plan);
`GIVE_UP <= MIN_AGE` is an error, because every row would then be abandoned before it was ever
queried. The floor participates in the second check — a typed `30s`/`45s` pair fails because
`30s` became `60s` — and that is a test case.

---

## 2. Tests

### Group C — `internal/service/mpesa_reconcile_repo_test.go` (integration)

| Test | Pins |
|---|---|
| `TestInsertSchedulesTheFirstQueryAfterMinAge` | a fresh row has 0 attempts and `next_query_at` = `created_at` + MinAge (±2s) |
| `TestListPendingReturnsOnlyDueRowsOldestFirst` | five deposits — due-later inserted *before* due-first, so the order seen is `next_query_at`'s and not `created_at`'s; a not-yet-due row, a settled row that was due, and a pending row with `NULL` schedule are all invisible; `limit 1`; the not-yet row appears last once time moves |
| `TestMarkQueriedIsANoopOnASettledRow` | on a pending row both fields move; on a settled row neither does and there is no error |
| `TestCountPendingOlderThanSeesEveryOldPendingRow` | of four backdated rows, the old pending and the old *unscheduled* count, the fresh and the settled do not |
| `TestMigration0007BackfillsPendingRows` | the real files 0001–0006 into a temp dir, seed pending/completed/failed rows on the pre-0007 schema, then `Migrate` from the real dir so only 0007 runs: pending rows scheduled at `created_at` with 0 attempts, terminal rows `NULL`, the index exists, `ListPending` sees exactly the two pending rows, a second `Migrate` is a no-op |

Two harness changes to make these possible, both small: `freshPool(t)` factors the
connect-and-reset out of `newHarness` so the migration test can apply a partial set; and the
harness now carries `pool`, for the two tests that need to backdate `created_at` or null a
schedule — states the repo API deliberately cannot produce. The harness config also carries the
three production defaults. **Load's floor is not applied there** — the struct is built directly —
so a CP3 test that wants the pass to reach a fresh deposit lowers `MinAge` itself, on purpose;
the comment in the harness says so.

### Unit — `mpesa_querier_test.go` (package `service`, no tag) and `config_test.go`

`TestMockModeQuerierNeverResolves`, `TestRealModeQuerierIsTheClient`,
`TestSetQuerierForTestReplacesTheQuerier`; `TestLoad_reconcileDefaults`,
`TestLoad_reconcileMinAgeIsFloored` (`5s`, `0s`, `59s` → `60s`; `10m` stays `10m` — a floor,
not a fixed value), `TestLoad_reconcileGiveUpMustOutlastMinAge`,
`TestLoad_reconcileEveryMustBePositive`.

### Mutation checks

No money moves in CP2, so these are the three places where the *next* checkpoint would be
misled if this one were wrong.

**1 — `MarkQueried` without its guard** (`AND status = 'pending'` removed):
```
TestMarkQueriedIsANoopOnASettledRow
    MarkQueried moved a settled row: attempts 0→7 next 2026-09-25 01:39:09 → 2026-09-25 02:36:09
```
`TestListPendingReturnsOnlyDueRowsOldestFirst` still passes under this mutation, as it should —
it marks its settled row *before* settling it. The guard has one test, and that test catches it.

**2 — no floor in `Load`:**
```
TestLoad_reconcileMinAgeIsFloored:        MIN_AGE=5s loaded as 5s, want the 1m0s floor
TestLoad_reconcileGiveUpMustOutlastMinAge: GIVE_UP=45s with a floored MIN_AGE of 60s loaded without error
```

**3 — the mock stub answers `Succeeded`:**
```
TestMockModeQuerierNeverResolves
    mock querier said succeeded for "ws_CO_MOCK_1", want unresolved — the stub must not be able to settle a deposit
```

All three restored and confirmed byte-identical to the pre-mutation copy.

**One mutation run was itself wrong the first time.** Mutation 1 was scripted as a chain of
`&&`-joined commands behind a `grep` that matched nothing, so the mutation never applied, the
tests ran on correct code, and the run printed `ok`. Read as evidence, that would have said "the
guard has no test". It was re-run on its own with the mutation *verified in the file* before the
tests ran (guard count 2 → 1 → 2), and the failure above is from that run. Recorded because a
mutation check that cannot tell "applied and survived" from "never applied" is the one failure
mode that makes the whole method worthless.

---

## 3. Verification

| Command | Result |
|---|---|
| `go build ./...`, `gofmt -l`, `go vet` (both tag sets) | clean |
| `go test ./...` | ok (`config`, `models`, `service`, `mpesaclient`) |
| `go test -tags integration ./internal/service/ -count=1` | **ok, 40.0s** — whole suite, including every CP0 and callback test on the changed `Insert` |
| the five group C tests | pass |
| mutations 1–3 | fail as quoted; restored |
| `python3 scripts/smoke.py` (rebuilt API, mock mode) | **ALL CHECKS PASSED** |
| `npx playwright test --project=mobile --project=desktop e2e/user.deposit.spec.ts` | **4 passed, 46.0s** |

Only the deposit spec was re-run from the browser suites: the served surface changed at one
place, the deposit insert, and that spec drives it end to end on both CFD projects. The digit
projects do not touch deposits.

### The exit criterion: boot against a database with pending rows

The local `tradederivplus-db` had 0001–0006 applied and 78 terminal `mpesa_requests` rows, none
pending. With the API stopped, a pending row was inserted on the pre-0007 schema, the API
rebuilt and started:

```
api listening port=8080 env=development mock_feed=true mock_mpesa=true
    mpesa_reconcile_every=1m0s mpesa_reconcile_min_age=3m0s mpesa_reconcile_give_up=24h0m0s
```

| Check | Result |
|---|---|
| `schema_migrations` | `0007_mpesa_reconcile.up.sql` present |
| the seeded pending row | `reconcile_attempts 0`, `next_query_at = created_at` **true** |
| terminal rows with a schedule | **0** of 78 |
| `idx_mpesa_requests_pending_next` | present |
| `/health` | `{"mock_mpesa":true,"status":"ok"}` |
| the first deposit `smoke.py` then made | `completed`, `next_query_at − created_at = 00:02:59.999401` |

That last figure is two clocks: `created_at` is the database's `now()`, `next_query_at` is the
API's `time.Now() + 3m` taken a few hundred microseconds earlier. `ListPending` compares against
the API's clock too, so the pass is consistent with itself; the gauge compares `created_at`
against a Go-side timestamp, and a sub-second skew against a 3-minute threshold does not matter.
Noted so nobody reads `2m59.999s` as a bug.

The seeded row was deleted afterwards; the database is as found plus 0007 and smoke's own rows.
The API left running on 8080 is the rebuilt one.

---

## 4. What CP3 inherits

- `ListPending` / `MarkQueried` / `CountPendingOlderThan` with their guards and their meanings
  tested, and `ReconcileAttempts` carried on every listed row so the pass can pick the next
  backoff without a second read.
- `s.querier` — the pass calls it and never `s.client`, so mock mode runs the same code.
  `SetQuerierForTest` takes anything with the one method; CP3's `stubQuerier` scripts outcomes
  per checkout id and counts calls.
- The harness has `pool` for the cases the API cannot produce, and no floor on `MinAge`, so
  "a fresh deposit is due now" is one config line in a test.
- Three config values, validated, printed at boot. `Every` is guaranteed positive, so
  `time.NewTicker` in `Start` cannot panic.
- What is still `NULL`-able: `next_query_at` on a row the service inserted without one. The
  service always sets it; the migration comment and `CountPendingOlderThan` are the two places
  that remember it might not be.

CP3 not started.
