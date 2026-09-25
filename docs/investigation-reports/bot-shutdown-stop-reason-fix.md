# Fix: a clean shutdown could be recorded as a run failure

**Date:** 25 September 2026 · **Branch:** `fix/shutdown-stop-reason` (off `master`)
**Kind:** bug fix, found through a CI failure on an unrelated branch.

---

## 1. Why this is being fixed now, having been deferred hours ago

This finding was logged on the deferred list earlier today with "don't fix it now", on the strength
of one CI failure and a passing re-run. That call was wrong, and the evidence that made it wrong
arrived immediately: the test has since failed **three of the last four CI runs, master included**.
It is not a rare flake, and master being red blocks every branch behind it.

The deferral's reasoning ("surfaced on an unrelated branch, so don't fold it into a routing config
fix") was right about *where* not to fix it. It was wrong about *how often* it happens, because a
single occurrence plus one green re-run cannot distinguish "rare" from "roughly even odds". Two
data points were treated as a rate.

## 2. The defect

Three sites in `BotService.loop` consult `externalStop` — which reports process shutdown by testing
`s.rootCtx.Err() != nil` and needs no database — and then run a database operation on `s.rootCtx`:

| Site | Operation |
|---|---|
| `bot_service.go:706` | `BotRuns.Get` before deciding whether to place |
| `bot_service.go:780` | the placement switch's `default:` branch |
| `bot_service.go:808` | `BotRuns.Get` after a settlement |

That is a check-then-act pair. A cancel landing in the window between the check passing and the
operation running makes the operation fail with `context canceled`, and all three sites then
finalised the run as `models.BotStopError` with that string as its `last_error`.

The loop's own comment already names this as the thing to avoid:

> On shutdown the root context is already cancelled, so the read below would fail — and finalising
> on that error would record a restart as a fault, with "context canceled" as its last_error, which
> is noise that reads like a bug.

The comment was right about the intent; the code narrowed the window rather than closing it.
`stop_reason` is operator-visible, so an orderly restart could report that a run died of an error.
No money is affected — the contract is voided and refunded on either path — but an operator reading
stop reasons during a real incident would be reading a fabricated fault.

`TestBotShutdownFinalisesRun` asserts the correct behaviour (`bot_lifecycle_test.go:345-347`) and so
failed whenever the race went the wrong way: `reason error, want shutdown`.

## 3. Why it passed locally and failed in CI

It never reproduced locally: 6/6, then 12/12 under `GOMAXPROCS=1`. CI fails it roughly half the
time. The difference is not machine speed in general but *where* the cancel lands relative to an
in-flight query, and CI runs the whole package — so this test runs after eighty-odd seconds of other
integration tests, against a database under load, with the loop far more likely to be inside a query
when `cancel()` fires. A local `-run` filter puts it in the quietest possible environment, which is
precisely the environment that cannot reproduce it.

Worth recording as a method note: `-count=N` on a filtered test measures the same conditions N
times. It is evidence about one environment, not about a race.

## 4. The fix

The decision is separated from the write, because the bug was entirely in the decision:

```go
func (s *BotService) finaliseFailure(runID uuid.UUID, live *liveRun, err error) {
	reason, lastError := failureReason(s.rootCtx.Err() != nil, err)
	s.finalise(runID, live, reason, lastError)
}

func failureReason(shuttingDown bool, err error) (string, *string) {
	if shuttingDown {
		return models.BotStopShutdown, nil
	}
	msg := err.Error()
	return models.BotStopError, &msg
}
```

All three sites now call `finaliseFailure`. Two decisions inside it are deliberate:

- **It asks the root context, not the error.** `errors.Is(err, context.Canceled)` would also have
  made the test pass, but it answers the wrong question: a per-operation timeout that happens to be
  a cancellation is a real problem, and disguising it as an orderly stop would hide exactly the kind
  of fault this column exists to report. The question is "are we shutting down", which is the same
  test `externalStop` and `stopReasonOf` already use.
- **It records no `last_error` on shutdown.** There is no fault to describe, and `context canceled`
  in that column is the noise the loop's comment objects to.

The panic path at `bot_service.go:682` deliberately still records `BotStopError` unconditionally: a
panic is a fault whether or not the process is also shutting down.

This makes the production data correct rather than only making the test green, which is the option
the deferred entry recommended.

## 5. Tests

`internal/service/bot_failure_reason_test.go`, untagged — it needs no database, so it runs in the
plain unit suite and a regression fails in seconds rather than in an 88-second integration run.

| Test | What it pins |
|---|---|
| `TestFailureReasonCallsAShutdownAShutdown` | shutting down → `shutdown`, and no `last_error` |
| `TestFailureReasonKeepsARealFailure` | healthy process → `error`, message preserved |
| `TestFailureReasonDoesNotTrustTheErrorAlone` | `context.Canceled` with a live root context is still `error` |

The third is the one that stops this fix from degenerating into "call everything a shutdown".

The existing `TestBotShutdownFinalisesRun` is unchanged and is now deterministic *by construction*
rather than by timing: whichever path wins the race — the `externalStop` check or the operation's
error — both now yield `shutdown`, so there is no longer a wrong way for the race to go.

## 6. Mutation check

Verified present in the file and `go vet`-clean before each result was read; restored
byte-identically after each (`sha256sum -c`, twice).

| # | Mutation | Expected | Result |
|---|---|---|---|
| M1 | remove the `shuttingDown` branch — the bug as it shipped | the shutdown test fails with CI's own message | **failed**: `reason "error" while shutting down, want "shutdown"` |
| M2 | `if shuttingDown \|\| true` — the over-broad fix | both converse tests fail | **failed**, in two tests |

M1's message is CI's failure reproduced deterministically in a test that runs in 4 ms, against a
race that never reproduced locally in 27 attempts.

**What the mutations do not cover, stated plainly.** These tests pin the decision, not the wiring.
A mutation reverting one of the three call sites to a direct
`s.finalise(runID, live, models.BotStopError, &msg)` would leave all three tests passing, because
the pure function would still be correct — only that site would stop consulting it. Site
`bot_service.go:706` is covered by `TestBotShutdownFinalisesRun`, but racily, which is the whole
problem being fixed; sites `:780` and `:808` have no coverage of their routing at all. Closing that
would need a seam forcing a cancel to land inside a specific in-flight operation, which is more
machinery than the defect warrants. Recorded so nobody later reads three green tests as proof the
wiring is pinned.

## 7. Verification

`gofmt` clean, `go build ./...`, `go vet ./...`, the unit suite, and the full integration suite all
pass. `TestBotShutdownFinalisesRun` run 15 consecutive times after the fix: clean.

## 8. Not in this change

No flag changes; `withdrawals_enabled` and `bot_real_money_enabled` both remain false. The deferred
list's entry for this item moves to Closed.
