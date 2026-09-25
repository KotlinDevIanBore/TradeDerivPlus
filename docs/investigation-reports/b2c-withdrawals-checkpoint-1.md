# B2C withdrawals · CP1 — the payout client, and the probe

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** `b2c-withdrawals-investigation.md` §5.2 CP1 — `pkg/mpesaclient`'s payout side, the
credential, the two neighbouring endpoints, the B2C vocabulary, and the sandbox probe. Pure
protocol: no service wires it, no route serves it, no config reads it. That is CP2's.

---

## 1. What was built

### 1.1 `SendB2C` — v3, and no way to send anything else

`Client.SendB2C` posts to `/mpesa/b2c/v3/paymentrequest` with the ten fields v3 takes, under the
push's cached bearer token. There is no v1 path and no version switch: v3 is what accepts
`OriginatorConversationID`, the whole design rests on that id, and it is what is already in
production next door. The test stub registers v3 and deliberately does **not** register v1, so a
request to the older endpoint would 404 in the suite rather than quietly work.

**The id is the caller's, and this package will not invent one.** `SendB2C` refuses a blank
`OriginatorConversationID` rather than generating one, because an id generated here is lost exactly
when it is needed — when the process dies between sending and recording. CP2 persists it before it
sends.

Three outcomes, and the distinction between the last two is the most consequential thing in the
file:

| Outcome | Meaning | What the caller may do |
|---|---|---|
| `*B2CAccepted`, nil error | queued, with a `ConversationID` to correlate by | wait for the result callback |
| `*PayoutRejection` (`errors.Is ErrPayoutRejected`) | Daraja refused to queue it | **may refund** — nothing was queued, so nothing can pay |
| any other error | unknown; the request may have reached Safaricom | **must not refund**; reconcile by status query |

A rejection is recognisable by carrying no `ConversationID`, which is the argument that makes
refunding safe there. A transport failure returning a rejection would tell a caller it may refund a
payout that might already be in flight, so that is mutation M5.

An acknowledgement with `ResponseCode 0` but **no** `ConversationID` is an error, not an
acceptance: a payout in flight that no callback could ever be matched to is worse than a failed
send.

### 1.2 The result code that is not a number

`flexCode` keeps a result code's canonical **text** plus whether one was present, and the one thing
it refuses to do is default to zero.

This is not defensive decoration. Zero is success — it is the code that releases money — and B2C's
own documented codes include `SFC_IC0003`, which is not a number at all. A parser that returns an
`int` therefore has to do something with it, and the neighbouring production system's helper
returns `0` from its default branch: under it, `SFC_IC0003` ("the recipient's number does not
exist") reads as **paid**. `flexCode` makes that unrepresentable — an absent, null, empty or
unreadable code is an error, never an outcome.

The allowlist is keyed by text for the same reason:

```go
var PayoutFailureCodes = map[string]string{
    "1": …, "2": …, "3": …, "4": …, "8": …, "11": …, "21": …,
    "2001": …, "2006": …, "2028": …, "2040": …, "8006": …, "SFC_IC0003": …,
}
```

An allowlist, not a denylist, for the same reason the STK side uses one: everything unrecognised is
`Unresolved`, which the caller reads as "ask again", never as "refund". Marking a payout failed
wrongly means refunding a customer who was in fact paid, and Safaricom's own documentation says B2C
reversals are performed by hand on the M-PESA portal — there is no API to take it back.

`PayoutOursCodes` marks the seven codes that mean the fault is the business's: float, account
state, initiator authorisation, credentials, product assignment, locked user. A refund makes the
customer whole, but the next payout fails the same way, so CP3 can page someone on those and not
on a wrong phone number.

### 1.3 The two callback URLs, kept apart

`Options` takes `B2CResultURL` and `B2CTimeoutURL`, and `ValidateB2CConfig` **refuses a
configuration where they are equal** — as well as refusing empty ones, so a misconfiguration is a
failed boot rather than a failed withdrawal. `SendB2C` and both query methods call it too, so the
refusal does not depend on anyone remembering the boot check.

`ParsePayoutCallback(body, arrival)` takes which channel the payload arrived on, and the caller
must supply it from the route because the body does not reliably say. The rule:

> A payload that arrived on the queue-timeout URL is **always** `Unresolved`, whatever code it
> carries, including 0.

The timeout channel means Daraja's queue gave up before it knew the outcome. Reading its non-zero
code as a failure refunds a customer who may have been paid; reading a 0 there as success settles a
payout that may never have happened. Both are worse than waiting, and the status query exists for
exactly this case. This is the hazard CP0's report flagged in the neighbouring system, where both
URLs point at one handler that treats any non-zero code as failure — and it is mutation M1.

### 1.4 Transaction Status and Account Balance

Both authenticate as the same API user and both are **asynchronous**: the call returns an
acknowledgement and the answer arrives later on the result URL. So a payout reconciler is
request-then-wait, not the STK pass's list-query-classify — the classification lives in the callback
handler.

`QueryTransactionStatus` asks by `OriginalConversationID` — the spelling the documentation uses on
this endpoint, which differs from the B2C request's `OriginatorConversationID`, and one of the
things the probe confirms — or by `TransactionID`, and refuses when given neither.

`ParseStatusCallback` classifies into the same three statuses so a reconciler has one vocabulary,
with one rule a shared classifier would lose: **a non-zero result code on a status query means the
query failed, not that the payout failed.** A `2001` there is our credential being wrong, and says
nothing whatever about the customer's money. That is mutation M6. Within a successful query, the
`TransactionStatus` parameter is matched against small allowlists — `Completed` succeeds,
`Cancelled`/`Declined`/`Failed`/`Expired` fail, and `Pending`, an unknown word or an absent
parameter are all `Unresolved`.

`QueryAccountBalance` and `ParseBalanceCallback` exist for the float. Daraja delivers balances as
one pipe-and-ampersand delimited string; `UtilityAvailable` picks out the account B2C pays from.
Every successful payout result also carries `B2CUtilityAccountAvailableFunds`, so CP3's float gauge
has a free source on the happy path and this endpoint for when there has not been one lately.

### 1.5 The credential, generated once and offline

`EncryptInitiatorPassword(password, certPEM)` is RSA **PKCS#1 v1.5** against the certificate's
public key, base64 — confirmed first-party against Safaricom's own node library, and matching the
production implementation next door. `cmd/gencred` wraps it: the password comes from the
environment (never a flag, so it stays out of shell history and the process list), only the
credential is printed on stdout so it can be piped into a secret store, and nothing is written to
disk.

The server therefore never holds the plaintext initiator password and never reads a certificate at
request time. That is a smaller attack surface than per-request encryption and it is how the
working system does it; the ciphertext is still a secret, just a less dangerous one to have in a
deployment's environment. Rotating the password is a re-run of the generator.

**No certificate is committed.** Sandbox and production use different ones, the right choice
depends on where you are pointing, and using the wrong one produces a credential Daraja rejects as
`2001` — the same code as a wrong password, which is why the function's comment says to suspect the
certificate first. The path is a flag.

### 1.6 The copy table, and the collision it exists for

`service.humanPayoutResult` is a separate table from `humanResult`, in the service layer where the
deposit copy lives, with the client's allowlist as its counterpart. The split is the STK plan's D1:
whether a code is final and what we say about it are different questions, and a code must never
wait on someone writing copy before it can be treated as terminal.

The reason it cannot be a branch inside `humanResult` is arithmetic on meanings:

| code | deposit (`humanResult`) | payout (`humanPayoutResult`) |
|---|---|---|
| 1 | the customer's M-Pesa balance is too low | **our** utility float is empty |
| 2001 | the customer entered a wrong PIN | **our** initiator credential is wrong |

Rendered through the deposit table, a payout failure tells a customer their balance was too low
when ours was, and that they mistyped a PIN they were never asked for. The test asserts the two
tables disagree rather than describing it, and mutation M7 quotes what delegation actually produces.

The copy splits by fault: a customer-actionable failure says what to change, an "ours" failure says
nothing was taken and that we are on it, and an unrecognised code falls back to a sentence true of
every failure. Daraja's own `ResultDesc` is kept for the ledger and the log and never shown — it is
written for operators ("DebitParty in invalid state").

### 1.7 `scripts/daraja_b2c_probe.py`

Sandbox-only by refusal, like `daraja_probe.py`, and it refuses three ways: a non-sandbox base URL,
a missing credential, and — fittingly — a result URL equal to the timeout URL. Credentials come from
the environment and are never printed.

Its first job is the question production traffic next door cannot answer, because that system never
sends the same id twice: **does Safaricom actually deduplicate a repeated
`OriginatorConversationID`?** Step 1 sends a payout; step 2 sends the identical request again. The
script says plainly what each answer means, including the trap: a second clean acceptance is *not*
evidence that resending is safe, because only the Result callbacks can say whether it paid once or
twice, so it instructs the reader to check the test number before trusting any resend.

It also records a deliberate rejection, what v1 does by comparison (so the report can say v1 was
observed rather than assumed absent), a status query by our own id, and an account balance. What it
cannot see is the Result payloads, because it is not a server — it prints where to collect them and
what to update when they arrive.

---

## 2. Tests — `pkg/mpesaclient/b2c_test.go`, `security_test.go`, `internal/service/payout_copy_test.go`

| Test | Pins |
|---|---|
| `TestSendB2CSendsV3WithOurOwnRequestID` | the v3 path, all ten fields, the bearer, `PartyB` normalised from `07…`, `CommandID BusinessPayment`, `PartyA` the shortcode, the two URLs going out **different**, and no empty `Occasion` |
| `TestSendB2CRefusesWhatItCannotSafelySend` | blank id, zero and negative amounts, a non-Safaricom number — and in each case that **nothing reached Daraja** |
| `TestSendB2CRejectionIsDistinctFromNotKnowing` | envelope at 400 and at 200, and a non-zero `ResponseCode`, all rejections with their codes; `ResponseCode 0` with no `ConversationID` an error but *not* a rejection; a closed server an error and not a rejection |
| `TestValidateB2CConfigRefusesOneURLForBothChannels` | equal URLs refused with an explanation, at boot **and** at send; each of the four values missing refused; a correct one accepted |
| `TestParsePayoutCallbackClassification` | 18 cases: 0 as number and string; every allowlist member; `SFC_IC0003`; unknown numeric and unknown string codes as `Unresolved`; `OursToFix` per code |
| `TestTimeoutArrivalIsNeverTerminal` | six codes, each classified on both channels: always `Unresolved` on timeout, and still classifying on result — so the distinction is load-bearing, not cosmetic |
| `TestResultCodeNeverDefaultsToZero` | absent, null, empty, an STK-shaped body, non-JSON and empty body: each an error, and never `Succeeded` |
| `TestParsePayoutCallbackExtractsWhatAnOperatorNeeds` | receipt, recipient name, completion time, utility float; `ResultParameter` as a single object as well as an array; a success with no receipt stays a success and nothing is synthesised |
| `TestPayoutFailureCodesAreTheDocumentedSet` | exactly thirteen, `0` absent, and every "ours" code is also a failure code |
| `TestQueryTransactionStatusAsksByTheIDWeChose` | endpoint, `OriginalConversationID`, command shape, API-user auth, distinct URLs, no empty `TransactionID` alongside; by receipt too; neither id an error |
| `TestParseStatusCallbackSeparatesQueryFailureFromPayoutFailure` | nine cases, including a **failed query with a code that is terminal for a payout** classifying `Unresolved`; the timeout rule; a missing code an error |
| `TestAccountBalanceAsksAndParses` | endpoint and shape; four accounts out of the delimited string; `UtilityAvailable`; a non-zero result code and a missing code both errors |
| `TestPayoutsShareThePushTokenAndDropItOn401` | one authentication across two payout calls; a 401 drops the cached token |
| `TestStatusAndBalanceAcknowledgementsClassifyTheSameWay` | the shared decoder's four branches on both endpoints |
| `TestPayoutDiagnosticStrings` | the strings that only appear in failure messages, so a diagnostic is never the broken thing |
| `TestEncryptInitiatorPasswordRoundTrips` | a throwaway 2048-bit certificate, encrypt, then **decrypt with the private half** — PKCS#1 v1.5, base64, exact password |
| `TestEncryptInitiatorPasswordIsRandomisedNotDeterministic` | two runs differ; Daraja decrypts rather than compares, so both work |
| `TestEncryptInitiatorPasswordRefusesBadInput` | empty and whitespace passwords, non-PEM, unparseable certificate, and a password too long for the key with a message that names the cause |
| `TestPayoutCopyDoesNotReuseTheDepositVocabulary` | codes 1 and 2001 render differently on the two paths; the payout text blames neither the customer's balance nor a PIN |
| `TestEveryTerminalPayoutCodeHasSafeCopy` | every allowlist code has copy, none leaks Daraja's wording, none is ambiguous about whether money moved; the fallback reassures about the balance |
| `TestPayoutCopySplitsFaultCorrectly` | an "ours" failure says so, a wrong-number failure does not, and the helper agrees with the client's list |

### Mutation checks

Seven, each applied by a script asserting exactly one match, each built with `go vet` before its
test ran, each restored byte-identically.

| # | Mutation | Result |
|---|---|---|
| M1 | **the timeout channel collapsed into the result channel** (the neighbouring system's wiring) | `code 0 on the timeout channel classified succeeded — a timeout means 'we do not know', and settling or refunding on it is how a paid customer gets refunded too` |
| M2 | **an unreadable result code defaults to zero** (the `intValue` bug) | three cases: `accepted, as status succeeded — an unreadable result must never be a success` |
| M3 | the allowlist read as a denylist | `status failed, want unresolved` on both unknown codes |
| M4 | one URL accepted for both channels | `one URL for both channels was accepted` |
| M5 | a transport failure reported as a rejection | `a transport failure classified as a rejection — a caller would refund a payout that may have been sent` |
| M6 | a failed status **query** read as a failed payout | `status failed, want unresolved — a failed QUERY says nothing about the payout` |
| M7 | the copy table delegates to the deposit one | `code 1 renders identically on both paths: "Your M-Pesa balance was too low to complete this deposit"`, plus eight codes leaking Daraja's operator wording |

M1 and M2 are the two this checkpoint exists for: both are live behaviours in a working production
system, and both are the kind that pay out or refund wrongly rather than crash. M7's output is
worth reading literally — that sentence is what a customer would have been shown when the business's
own float ran dry.

**Every mutation result was treated as a claim to check, not a result to record.** None survived
this time, but M7 in CP0 is the reason the seven were each re-read against what the test actually
asserts before being believed.

---

## 3. Verification

| Command | Result |
|---|---|
| `go build ./...`, `gofmt -l`, `go vet` (both tag sets) | clean |
| `go test ./...` | ok (`config`, `models`, `service`, `mpesaclient`) |
| `go test ./pkg/mpesaclient/ -cover` | **ok, 87.2%** of the package; `ParsePayoutCallback` and `ValidateB2CConfig` 100%, `SendB2C` 91%, `EncryptInitiatorPassword` 94% |
| `go test -tags integration ./internal/service/ -count=1` | **ok, 46.9s** — whole suite, unchanged |
| M1–M7 | as tabled; each restored byte-identical |
| probe refusals: production base URL, missing credential, collapsed URLs | all three refuse with an explanation |
| `python3 -c "ast.parse(...)"` on the probe | parses |

What the remaining coverage leaves out: the request-construction error branches, which need an
invalid base URL to reach, and the two query methods' token-failure paths. No browser suite and no
smoke run: nothing served changed, nothing is wired, and the API surface is identical — the same
reasoning STK CP1 gave, and the integration suite was still run because a new file landed in
`internal/service`.

**One thing removed rather than shipped.** An early `flexCode.Numeric()` helper had no caller. Dead
code in a package that authorises payouts is worse than a missing convenience, so it is gone; if CP2
needs it, it comes back with a test. Coverage of the new files rose from 82.7% to 87.2% on that plus
the two tests that close the shared decoder's branches.

**The probe has not been run.** It needs sandbox credentials, an initiator, the sandbox
certificate, and somewhere to catch the Result callbacks. Until it runs, `PayoutFailureCodes` and
the test fixtures rest on the documentation mirror described in the investigation's §1.6 — which is
internally consistent with Safaricom's SDKs and with a captured production payload, and is the most
that can be said for it. Running it changes the code only if an observed code is missing from the
allowlist; it changes the **design** only in one place, and that is the next item.

---

## 4. What CP2 inherits, and the one question still open

- `SendB2C`, `QueryTransactionStatus`, `QueryAccountBalance` and the three parsers, with the
  arrival distinction as a parameter CP2 must supply **from the route** — which means two routes,
  not one handler.
- `ValidateB2CConfig` to call at boot, so a collapsed or incomplete payout configuration is a
  failed start.
- `PayoutFailureCodes` and `PayoutOursCodes` for the refund decision and the operator alert;
  `humanPayoutResult` for what the customer sees; `FailWithdrawal` from CP0 for the refund itself.
- `cmd/gencred` and one env var, with no certificate in the serving path.

**Still open, and it changes CP2's design rather than its constants:** whether Safaricom
deduplicates a repeated `OriginatorConversationID`. If it does, CP2 may resend after an ambiguous
send. If it does not, resending is forbidden and the only recovery is a status query. The client
supports both — it neither resends nor forbids resending, it just refuses to invent an id — so CP2
can be written either way, but it cannot be written *well* until the probe answers. That is the one
thing worth running the probe for before CP2 rather than after.

CP2 not started.
