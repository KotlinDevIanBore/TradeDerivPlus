# Fix: the B2C status-result URL never reached the Daraja client

**Date:** 25 September 2026 · **Branch:** `fix/b2c-status-result-url` (off `master`, after PR #6 merged)
**Kind:** one-line bug fix with the test that should have caught it.

---

## 1. The symptom

Every Railway boot with `USE_MOCK_MPESA=false` failed, on repeat:

```
level=ERROR msg=fatal err="b2c is not configured: missing B2C status-result URL"
```

The crash survived setting all five `MPESA_*` payout variables in Railway and redeploying. It
was not stale: the error is timestamped after the redeploy, and it could not have been anything
else, because nothing that can be set in the environment would have cleared it.

## 2. The cause

`NewMpesaPayoutService` built the Daraja client's `Options` and omitted one field
(`internal/service/mpesa_payout_service.go:139`):

```go
InitiatorName:      cfg.MpesaInitiatorName,
SecurityCredential: cfg.MpesaSecurityCredential,
B2CResultURL:       cfg.MpesaB2CResultURL,
B2CTimeoutURL:      cfg.MpesaB2CTimeoutURL,
})                                            // cfg.MpesaB2CStatusResultURL never passed
```

`config.Load` reads `MPESA_B2C_STATUS_RESULT_URL` correctly (`config.go:199`), and its
all-or-none group check passes on it (`config.go:261`) — so the environment was right, the value
reached `cfg` intact, and it was then dropped at the one place that consumes it. `Options`'
`B2CStatusResultURL` field was assigned in exactly one file in the repository, and that file was
`pkg/mpesaclient/b2c_test.go`.

The fix is that line, restored:

```go
B2CStatusResultURL: cfg.MpesaB2CStatusResultURL,
```

## 3. The error message was accurate, and that mattered

`ValidateB2CConfig` (`pkg/mpesaclient/b2c.go:245`) appends a separate label per empty field and
joins them, so it names exactly what is missing and lists every absence. A standalone probe
removing each value in turn confirmed one label per field and no cross-talk. Two consequences:

- The message was never misleading. It was a true statement that the *client's* status-result URL
  was empty and the other four were populated — which is only possible via a wiring defect,
  since `config.Load` would have refused a partial environment with a different error.
- The error narrowing from five labels to one, as the variables were set, was itself evidence
  that the other four had taken effect. The last one could not.

## 4. Why four checkpoints missed it

`pkg/mpesaclient/b2c_test.go:283` already blanks each of the five values and asserts the refusal.
That test is sound and still passes. It builds `Options` by hand, so it proves the validator
works while never exercising the `config.Config` → `Options` path. Nothing anywhere asserted that
the constructor carries config through, so a compile-clean omission of one struct field was
invisible to the entire suite and became a production crash loop instead of a red test.

Validation did its job: this was caught at boot rather than at the first withdrawal. Had it not
been, the defect would have been quieter and worse — `QueryTransactionStatus` sends
`"ResultURL": c.b2cStatusResultURL` (`b2c.go:547`), so the reconciliation pass would have asked
Daraja to post every status answer to an empty URL, issuing queries that could never be answered
while the payouts they asked about stayed open.

## 5. The test

`internal/service/payout_config_test.go`, untagged so it runs in the plain unit suite and needs
no database:

| Test | What it pins |
|---|---|
| `TestPayoutConfigReachesTheClient` | a fully-populated `config.Config` passes `ValidateConfig()` — the assertion that was missing |
| `TestEveryPayoutConfigFieldIsCarried` | each of the five blanked in turn must be noticed, *and* the error must be the missing-configuration one |
| `TestPayoutConfigRefusesTwoChannelsOnOneURL` | a copy-paste pointing two callbacks at one URL is refused |
| `TestMockModeNeedsNoPayoutConfig` | mock mode still demands none of it, so local development is unaffected |

The per-field test exists because asserting only that a full configuration passes would catch a
*dropped* field but not one wired to the wrong source or to a constant — both of those still pass
with everything populated. Blanking each value in turn pins that what the client validates is
what config supplied.

The three callback URLs in the fixture are deliberately distinct, so the fixture itself cannot
mask the duplicate-URL refusal.

## 6. Mutation check

Each mutation was verified present in the file and `go vet`-clean before its result was read, and
the file restored byte-identically afterwards (`sha256sum -c`, twice).

| # | Mutation | Expected | Result |
|---|---|---|---|
| M1 | remove the fix — the bug exactly as deployed | the new test fails with the production error | **failed**: `a fully-populated payout configuration was refused at boot: b2c is not configured: missing B2C status-result URL` |
| M2 | `B2CStatusResultURL: cfg.MpesaB2CResultURL` — the copy-paste | refused as a duplicate URL | **failed**, in two tests |

M1's failure text is the Railway log line reproduced on demand, which is the evidence that the
test covers this bug and not merely its neighbourhood.

**What M1 did not catch, and why that is the point.** Under M1 the entire per-field test passed,
including its own `MPESA_B2C_STATUS_RESULT_URL` subtest: that subtest expects an error, and with
the field dropped it gets one — for the wrong reason. It cannot distinguish "config's value was
carried and is empty" from "no value is ever carried". Only the full-configuration test catches a
dropped field. This is the project's recurring lesson in a new place: a mutation a test survives
can mean the test cannot see the difference, not that the code is right. The two tests are
complementary, and neither alone is sufficient.

Conversely M2 was caught by the per-field status subtest *only* because of the
`strings.Contains(err.Error(), "b2c is not configured")` assertion. Without it, a wrong-but-set
value would have satisfied a bare "must error" check.

## 7. Verification

`gofmt` clean, `go build ./...`, `go vet ./...`, the unit suite (`go test ./...`) and the
integration suite (`go test -tags integration ./internal/...`) all pass. No behaviour changes
except that a correctly-configured deployment now boots.

## 8. Not in this change

`withdrawals_enabled` and `bot_real_money_enabled` both remain `false`; this changes no flag and
enables no feature. It makes a correct payout configuration usable, which is a prerequisite for
turning withdrawals on, not a step toward it. Everything in the deferred list stays deferred —
the Safaricom dedup question, the "never seen" status code, the absent user-facing verification
flow, and the three provisional schedules.
