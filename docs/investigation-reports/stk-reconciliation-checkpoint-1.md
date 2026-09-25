# STK reconciliation · CP1 — `Client.STKQuery`

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Scope:** plan §2.1 — the query call, its three-way outcome, and the allowlist that classifies it.
Pure protocol in `pkg/mpesaclient`, tested against an `httptest` stub. No service change, no
schema change, no wiring.

---

## 1. What was built

`Client.STKQuery(ctx, checkoutID) (QueryOutcome, error)` in `pkg/mpesaclient/client.go`, beside
`STKPush`. It sends the four fields Safaricom's own SDK sends — `BusinessShortCode`, `Password`,
`Timestamp`, `CheckoutRequestID` — deriving the password and timestamp exactly as `STKPush` does
and under the same cached bearer token. Nothing new is configured; the test proves the request
carries exactly those four fields and nothing else, and that the password decodes to
shortcode + passkey + the timestamp that was sent.

```go
type QueryStatus int  // QueryUnresolved | QuerySucceeded | QueryFailed
type QueryOutcome struct{ Status QueryStatus; ResultCode int; ResultDesc string }
var TerminalFailureCodes = map[int]bool{1: true, 1001: true, 1032: true, 1037: true, 2001: true}
```

### The classification, as implemented

| Order | Response | Result |
|---|---|---|
| 1 | transport failure; non-JSON body; JSON with neither `ResultCode` nor `errorCode` | **`error`** — Daraja said nothing |
| 2 | an `errorCode`/`errorMessage` envelope, **any HTTP status, any code** | `Unresolved`, nil error |
| 3 | `ResultCode == 0` | `Succeeded` |
| 4 | `ResultCode` in `TerminalFailureCodes` | `Failed` |
| 5 | any other `ResultCode` | `Unresolved` |

Three points about it:

- **The envelope is checked before the HTTP status.** Daraja's "being processed" is reported to
  arrive as HTTP 500 with `errorCode 500.001.1001`. Classifying by status first would turn the
  in-flight answer into a transport error, which the reconciler would then treat as "no attempt"
  and never back off from. So the body is parsed first, and an envelope is an answer whatever
  wrapped it.
- **`TerminalFailureCodes` is separate from `humanResult`**, per the plan's D1 and the review that
  argued it: one list answers "is it over", the other "what do we say". A comment on each names
  the other.
- **The asymmetry with `STKPush` is deliberate and documented at the function.** The push returns
  `error` for an envelope, because for a push an envelope means the request was refused. For a
  query the same envelope is how Daraja says "not yet".

### A tolerance the unconfirmed docs required

The callback carries `ResultCode` as a JSON number. The query response is reported — by
third-party sources only; Step 2 could not confirm the response shape first-party — to carry it as
a JSON *string* (`"0"`). Rather than pick one, `ResultCode` decodes through a small `flexInt`
that accepts either. Both shapes are test cases. A body with a `ResultCode` that is neither
(`"zero"`) is an `error`, not a guess.

### One robustness addition beyond §2.1

An HTTP 401 on the query drops the cached token, so the next call re-authenticates instead of
repeating the 401 for the rest of the cache window. The reconciler is the first long-running caller
of this client, and a token that stops being good mid-hour is a case a one-shot push never met.
Tested: after a 401, the next query authenticates again — auth hits go from 1 to 2 — and
succeeds.

---

## 2. Tests — `pkg/mpesaclient/query_test.go`

The stub is a fake Daraja: it issues one token, counts how often it is asked for one, records the
last query request verbatim, and answers the query endpoint with whatever the case scripts.

**`TestSTKQueryClassification`** — every branch of the table above, as sub-tests:

| Branch | Cases |
|---|---|
| Succeeded | `ResultCode` `0` as a number; as a string |
| Failed | each of `1`, `1001`, `1032`, `1037`, `2001`; `1032` again as a string |
| Unresolved by code | `9999` |
| Unresolved by envelope | **`500.001.1001` at HTTP 500** (the disputed one); `400.002.02` at HTTP 400; an envelope at HTTP 200 — both flavours §8 group B asked for, plus the status-does-not-matter case |
| error | HTTP 500 non-JSON; HTTP 502 `{}`; HTTP 200 non-JSON; HTTP 200 empty; `ResultCode: "zero"` |

Plus: **`TestTerminalFailureCodesAreExactlyTheAllowlist`** — the list is exactly those five and
`0` is not on it; **`TestSTKQuerySendsWhatThePushDerives`** — the wire shape;
**`TestSTKQueryReusesThePushToken`** — a push then two queries authenticate once;
**`TestSTKQueryDropsTheTokenOn401`**; **`TestSTKQueryTransportFailureIsAnError`** — token warmed
first so the *query* is what dies, which is the reconciler's case.

This is the repository's first use of `net/http/httptest`. Standard library; the plan accepted it
in advance.

### Mutation checks

The rule is the safety property, so the two ways of getting it wrong were each put back:

**A — the denylist reading: anything not `0` is a failure.**
```
TestSTKQueryClassification/unknown_ResultCode
    status failed, want unresolved (body {"ResultCode":9999,"ResultDesc":"Something new"})
```

**B — an envelope is an error, the way `STKPush` treats one.**
```
TestSTKQueryClassification/envelope_500.001.1001_at_HTTP_500
    unexpected error: daraja 500.001.1001: The transaction is being processed
TestSTKQueryClassification/envelope_with_another_code_at_HTTP_400
    unexpected error: daraja 400.002.02: Bad Request - Invalid CheckoutRequestID
TestSTKQueryClassification/envelope_at_HTTP_200
```

B is the one that matters most: under it, the reconciler would see Daraja's "being processed" as
"Daraja said nothing", never count it as an attempt, and never back off. Restored after each.

### One test rewritten before it counted

The transport test first closed the stub and called the query cold. It failed — correctly — with
`daraja auth: … connection refused`: with nothing cached, the *auth* call dies before the query
does. True, but the wrong branch for what the test claims. It now warms the token first and then
closes the server, so what is exercised is a query failing under a still-valid token — the
long-running reconciler's actual failure mode.

---

## 3. Verification

| Command | Result |
|---|---|
| `go test ./pkg/mpesaclient/ -count=1 -cover` | **ok**, 8 tests / 23 sub-tests; package **79.7 %**, `STKQuery` **94.1 %** |
| `go build ./...`, `gofmt -l`, `go vet` (both tag sets) | clean |
| `go test ./...` | ok |
| mutations A and B | fail as quoted; restored |

What the 94 % leaves out of `STKQuery`: the `http.NewRequestWithContext` error branch, which
cannot be reached with a valid base URL. `QueryStatus.String` shows 0 % because it is only
formatted into failure messages, and none fired.

The integration suite was not re-run: it constructs the service in mock mode, where the Daraja
client is nil, so nothing in it can reach `STKQuery`. That changes in CP2, when the querier seam
arrives.

---

## 4. What CP2 inherits

- `STKQuery` and `QueryOutcome` are the interface the `stkQuerier` seam will be typed on;
  `*mpesaclient.Client` satisfies it directly, no wrapper.
- The `error` / `Unresolved` distinction is what §2.4's "an error is not an attempt" rule depends
  on. It exists at the client so the service never has to reason about HTTP.
- The disputed code lands as `Unresolved` under any of its readings. When
  `scripts/daraja_probe.py` is run, the fixture bodies in `TestSTKQueryClassification` should be
  replaced with the observed ones — the classification will not change, only the evidence
  behind it.

CP2 not started.
