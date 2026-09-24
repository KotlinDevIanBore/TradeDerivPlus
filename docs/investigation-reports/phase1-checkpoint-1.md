# Phase 1 · Checkpoint 1 — Schema and provenance plumbing

**Date:** 24 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Plan:** `docs/investigation-reports/auto-bot-plan.md` §2.5
**Scope:** Schema, repository, provenance. No service or handler logic.

---

## What was built

### New files

| File | Contents |
|---|---|
| `migrations/0006_bot_runs.up.sql` | `bot_runs`, the partial unique index, `digit_contracts.bot_run_id` + its index. Taken verbatim from §2.5, comments included. |
| `internal/models/bot_run.go` | `BotRun`, `BotLimitsConfig`, the three statuses (`running`/`stopping`/`stopped`), fifteen stop-reason constants, `IsLive()`. |
| `internal/repository/bot_run_repo.go` | `BotRunRepo` with every method §2.5 lists, plus two additions (see Deviations). |

### Changed files

| File | Change |
|---|---|
| `internal/repository/digit_contract_repo.go` | `bot_run_id` through all four touchpoints; new `CountOpenByRun`. |
| `internal/repository/store.go` | `BotRuns *BotRunRepo` field and its `newStore` entry, so it participates in `WithTx`. |
| `internal/models/digit_contract.go` | `BotRunID *uuid.UUID` with a `bot_run_id` JSON tag. |
| `internal/service/digit_service.go` | `OpenDigitInput.BotRunID`; threaded onto the contract; `bot_run_id` added to the `digit_open` ledger metadata when set. |

### The four touchpoints, itemised

The plan singles out `GetOpenForUpdate` as the one most likely to be forgotten, so each is named
with the reason it matters:

1. **`digitSelect`** (`:17-23`) — added `c.bot_run_id`. Every read path that joins symbols.
2. **`scanDigit`** (`:25-46`) — added `&c.BotRunID`, positioned to match the select.
3. **`Create`** (`:48-62`) — added the column, a `$13` placeholder and `c.BotRunID`.
4. **`GetOpenForUpdate`** (`:73-95`) — **verified explicitly.** It has its own column list, separate
   from `digitSelect`, and it is the row the settlement path locks and reads. A contract missing its
   provenance *here* would settle without its run's counters ever being touched — which is precisely
   the failure Checkpoint 2 depends on not happening. A comment on the function now says so.

`bot_run_id` also reaches the ledger: the `digit_open` transaction's metadata carries it when set, so
"which of these debits was the bot's" is answerable from a statement without joining back to
`digit_contracts`.

---

## Deviations from the plan

Three, all additive; none changes a decision in §11.

**1. `CountOpenByRun` on `DigitContractRepo`** — not in §2.5's method list, but §2.3 requires the
guard ("before each placement, `CountOpenByRun(runID)`; if > 0, await that contract instead of
placing"). It is repo-layer work, so it belongs in this checkpoint rather than being bolted on in
Checkpoint 2. Served by `idx_digit_contracts_bot_run`.

**2. `GetLiveByAccount` on `BotRunRepo`** — also not in the list. Without it, `StartRun` can only
learn that a live run exists by *failing* on the unique index, which means the API cannot tell the
user which run is already live. One query, and it relies on the same partial index for its guarantee
of at most one row.

**3. A `liveStatuses` constant** rather than repeating `('running','stopping')` in five places. The
definition of "live" appears in the partial unique index, the restart sweep, the kill switch, the
capacity count and the account lookup; a disagreement between any two of those is a bug that only
shows up under load.

One correction worth recording: my first draft of `ListByAccount` called an `itoa` helper that does
not exist in this package. Replaced with the `fmt.Sprintf(" LIMIT $%d OFFSET $%d", …)` pattern
`DigitContractRepo.ListByAccount` already uses, so the two page the same way.

---

## Verification

Exact commands, in the order run.

### Compiles, vets, formats

```bash
gofmt -l ./cmd ./internal ./pkg      # no output
go build ./...
go vet ./...
go test ./...                        # unit suite, all packages ok
```

### Migration and the four touchpoints against real Postgres

The integration harness drops and recreates the schema, applies every migration, and round-trips
contracts through `digitSelect`, `scanDigit`, `Create` and `GetOpenForUpdate` — so this run is what
proves 0006 is valid SQL and that no touchpoint's column list drifted out of alignment with its scan.

```bash
go test -tags integration ./internal/service/... -count=1 -timeout 600s
# ok  github.com/KotlinDevIanBore/tradederivplus/internal/service  22.286s
```

### Migration applied to the dev database

```bash
go build -o /tmp/tdp-api ./cmd/api
# restart the dev API (migrations run at boot)
docker exec tradederivplus-db psql -U tdp -d tradederivplus \
  -c "SELECT version FROM schema_migrations ORDER BY version;"
```

`0006_bot_runs.up.sql` present, sorting last after `0005`.

```bash
docker exec tradederivplus-db psql -U tdp -d tradederivplus -c "\d bot_runs"
docker exec tradederivplus-db psql -U tdp -d tradederivplus -c "\d digit_contracts"
```

Confirms all 22 `bot_runs` columns, and on `digit_contracts`:

```
bot_run_id | uuid
"idx_digit_contracts_bot_run" btree (bot_run_id) WHERE bot_run_id IS NOT NULL
"digit_contracts_bot_run_id_fkey" FOREIGN KEY (bot_run_id) REFERENCES bot_runs(id)
```

### The partial unique index, probed directly

The schema's one real behavioural claim is that `stopping` holds an account's live-run slot. Asserted
against the live table rather than assumed:

```bash
# insert a live run, then attempt a second while running / stopping / after stopped
```

```
second live run while running:  rejected — Key (account_id)=(2ea199af…) already exists
second live run while stopping: rejected — Key (account_id)=(2ea199af…) already exists
new run after stopped:          INSERT 0 1
```

The middle line is the point: a second run cannot start while the first still has a contract in
flight. Test rows were deleted afterwards (`DELETE FROM bot_runs`), leaving the dev database as found.

---

## Not done in this checkpoint, by instruction

No `BotService`, no counters in the settlement transaction, no routes, no handlers, no flags, no
metrics. `bot_run_id` is written and read but nothing yet sets it, so every contract's provenance is
`NULL` — which is the correct state until Checkpoint 2 introduces something that places on a run's
behalf.
