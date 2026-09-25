# Step 2 — what the STK Push Query actually returns

**Date:** 25 September 2026 · **Branch:** `digit-contracts-enabled-by-default`
**Purpose:** settle §1.3 of `stk-query-reconciliation-investigation.md` before any reconciliation
code is written. No reconciliation logic was written.

**Headline: the decisive question is not settled, and I could not settle it here.** The request
side is now confirmed first-party. The response side — specifically what a still-processing query
returns — is not available from any first-party source I can reach, and the observational half
needs sandbox credentials that do not exist in this repository. §4 says exactly what is blocked and
§5 hands over the instrument to unblock it.

There is a recommendation that survives the gap, in §3. It is deliberately built so that getting
the disputed code wrong in *either* direction cannot mark a paid deposit failed.

---

## 1. What is now confirmed, first-party

From Safaricom's own SDK (`safaricom/mpesa-node-library`, `src/endpoints/lipa-na-mpesa-query.js`),
which is first-party code rather than a third-party summary:

```js
return req.post('/mpesa/stkpushquery/v1/query', {
  'BusinessShortCode': _shortCode,
  'Password': password,          // base64(shortCode + passKey + timeStamp)
  'Timestamp': timeStamp,
  'CheckoutRequestID': checkoutRequestId
})
```

Corroborated by `safaricom/mpesa-api-C-Sharp` (`StkqueryApi.cs`), which posts to the same path with
the same fields.

This confirms the investigation's §1.1 claim exactly: **the query needs nothing this codebase does
not already have.** The path differs from the push only in its last segments, the password
derivation is the same concatenation `STKPush` already builds (`pkg/mpesaclient/client.go:148`),
the timestamp format is the same (`client.go:147`), and the bearer token is the same one
`accessToken` already caches (`client.go:100-138`). `CheckoutRequestID` is already stored per
request (`migrations/0001_init.up.sql:51`).

So `Client.STKQuery` is a small, well-specified addition — **on its input side**.

---

## 2. What is not confirmed, and why

**Neither Safaricom's SDKs nor their READMEs document the response at all.** The node library says
only "for the response structure check Axios documentation"; the C# sample reads the stream and
prints it. Both document what to send and neither documents what comes back.

**The developer portal could not be read.** `developer.safaricom.co.ke/apis/MpesaExpressQuery`
returns a JavaScript shell — the fetched HTML contains navigation, branding and the heading
"Mpesa Express Query", and no request or response specification. The older documentation URLs that
appear in search results (`/lipa-na-m-pesa-online/apis/post/stkpushquery/v1/query`,
`/content/lipa-na-m-pesa-query-request-api-...`) return **404**. The content exists behind the
portal's client-side rendering and, in places, behind a login.

**The third-party sources still disagree**, which is what the investigation flagged. On
`500.001.1001` specifically, of the sources consulted: one treats it as the still-processing signal
that warrants continued polling; one reads it as insufficient funds in the business account; one as
an internal server error usually caused by a malformed password. One general comparison piece notes
only that "the Query API can return 'pending' for extended periods during system congestion" while
giving no code for that state.

These cannot all be true of the same code, and **no amount of further third-party reading resolves
it** — the sources are downstream of each other. Only Safaricom's own documentation or direct
observation will.

### What is not in dispute

The terminal `ResultCode` values are consistent across every source, and are the same vocabulary
the callback uses and `humanResult` already maps
(`internal/service/mpesa_service.go:183-203`):

| Code | Meaning |
|---|---|
| `0` | Success — the customer paid |
| `1` / `1001` | Unable to lock subscriber; another transaction in progress |
| `1032` | Cancelled by user |
| `1037` | DS timeout — the customer could not be reached |
| `2001` | Wrong PIN |

Nothing suggests the query returns different codes from the callback for the same outcomes. The
dispute is entirely about the *non-terminal* state.

### Minimum wait and rate limits

**Not established.** No first-party statement was found on either, and no third-party source gave a
figure with a source behind it. The investigation's §4.1 reasoning still stands as reasoning; it is
not evidence.

---

## 3. Recommendation: a rule that does not depend on the disputed code

The investigation's §4.2 asked whether "an ambiguous or failed query is still pending, ask again,
never a failure" still holds. **It holds, and what I found makes me want to state it more strongly
than §4.2 did.**

§4.2 framed it as: a *failed query* means ask again. Given that the still-processing code is
genuinely unknown, the rule should be framed the other way round — as an allowlist, not a
denylist:

> **Only an explicitly recognised terminal `ResultCode` may settle a deposit. Everything else —
> every error envelope whatever its code, every unrecognised `ResultCode`, every transport
> failure, every malformed body — means "still pending, ask again".**

Concretely, `STKQuery` should return three outcomes and the caller should act on nothing else:

| Outcome | When |
|---|---|
| `Succeeded` | a well-formed response with `ResultCode == 0` |
| `Failed(code, desc)` | a well-formed response with `ResultCode` in the known terminal set above |
| `Unresolved` | **everything else**, including any `errorCode` envelope |

**Why this shape is the right one given the uncertainty.** If `500.001.1001` turns out to mean
still-processing, this handles it correctly, because an error envelope is `Unresolved`. If it turns
out to mean a malformed password, this *also* handles it correctly — a broken request is not a
failed payment, and marking deposits failed because our own password derivation is wrong would be a
catastrophe of exactly the kind this investigation exists to prevent. If it means business-account
insufficient funds, likewise: not this customer's deposit failing.

The rule is robust to being wrong about the code in every direction the sources suggest. That is
worth more than resolving the dispute, and it means **Step 3's plan does not have to block on §1.3
for its design** — only for its polling schedule and its test fixtures.

One cost, stated plainly: an unrecognised *terminal* code would be treated as unresolved and the
deposit would stay pending rather than being failed correctly. That is the safe direction — a
deposit wrongly stuck is a support ticket, a deposit wrongly failed is a customer who paid and was
told they did not — and it is visible, because such rows accumulate and can be alerted on.

---

## 4. What is blocked, and on what

**The observational half of Step 2 cannot be run from here: this repository has no sandbox
credentials.** Verified: `MPESA_CONSUMER_KEY`, `MPESA_CONSUMER_SECRET` and `MPESA_PASSKEY` are all
empty in `.env`, and `MPESA_SHORTCODE` is Safaricom's shared sandbox till `174379`, which is a
default rather than a configured value.

I have not asked for those credentials to be pasted, and should not: a consumer secret and a
passkey in a chat log are a secret in a chat log. They belong in the environment of whoever runs
the probe.

Two further things I could not have observed even with credentials, and which whoever runs it
should know:

- **The sandbox test MSISDN auto-completes.** `254708374149` resolves without a PIN prompt, so a
  default run shows the succeeded path and whatever precedes it — not a cancellation, and not a
  PIN timeout. Those need a real Safaricom number registered on the sandbox app, and a human to
  cancel the prompt.
- **The first query is the one that matters.** By the time a transaction has resolved, the query is
  uninteresting; the whole question is what it says in the seconds before that.

---

## 5. The instrument: `scripts/daraja_probe.py`

Written, not run. It is the experiment §2 of the prompt describes, packaged so it can be run by
someone holding sandbox credentials:

- takes credentials **from the environment only** — never arguments, never a file;
- sends one STK push for KES 1 to the sandbox test MSISDN;
- queries the resulting `CheckoutRequestID` at ~0s, 5s, 15s, 30s, 60s and 120s;
- prints every **raw** body, pretty-printed but unmodified, because the shape is the thing under
  test and a parsed summary would discard the evidence;
- **refuses to run against any base URL that is not sandbox** — it sends a real push, and against
  production credentials that is a real charge to a real person. Verified: pointed at
  `api.safaricom.co.ke` it exits 1 without sending anything;
- refuses to run without credentials rather than half-running. Verified.

It ends with notes for the operator: how to observe the cancellation and timeout paths, that the
first query is the important one, and to record any throttling — six queries in two minutes is
deliberately gentle, and if even that is refused the reconciliation interval must respect it.

```bash
export MPESA_CONSUMER_KEY=... MPESA_CONSUMER_SECRET=... MPESA_PASSKEY=...
python3 scripts/daraja_probe.py
```

**What to bring back:** the raw body of the first query, and of any query before the transaction
resolved — specifically whether it carries a `ResultCode` or an `errorCode`/`errorMessage`
envelope, and verbatim what that code is.

---

## 6. Recommended schedule — and why it is provisional

§2 of the prompt asked for a minimum wait and polling interval "based on what you actually saw, not
a guess". **I did not see anything, so what follows is explicitly a guess and is labelled as one.**
It should be replaced by observation before it reaches the plan's defaults.

The reasoning, from the codebase rather than from Daraja:

- The STK prompt has its own lifetime — a customer has roughly a minute to enter a PIN — so a query
  inside that window is asking about something still being asked.
- The deposit sheet stops watching at `GIVE_UP_AFTER_MS = 120_000`
  (`web/src/features/wallet/DepositSheet.tsx:23`), and the console calls a deposit stuck at ten
  minutes (`web/src/features/admin/screens/Transactions.tsx:15`). A first query between those two
  numbers is consistent with what the product already says.

| | Provisional value | Replace with |
|---|---|---|
| Minimum age before first query | 3 minutes | The earliest age at which the probe shows a resolved answer rather than the in-flight one |
| Interval thereafter | widening: 3m, 10m, 30m, 1h | Whatever the observed throttling permits |
| Stop querying after | 24 hours, then leave for an operator | A product decision, not an observational one |

The shape — **do not query immediately, widen the interval, stop eventually** — is what I would
recommend regardless of what the probe shows. Only the numbers are provisional.

---

## 7. Where this leaves Step 3

Two of Step 3's seven items depend on §1.3 and five do not.

**Not blocked.** `MpesaRepo.ListPending` and its index; the reconciliation pass's shape and
scheduling seam; the single-settlement-path rule; the receipt decision; the test matrix including
the mutation discipline; §2.4's manual-adjustment hazard; and the failed-then-succeeded ordering
decision. All of these are decided by this codebase, not by Safaricom.

**Blocked, or provisional.** `Client.STKQuery`'s classification of a non-terminal response — though
§3's allowlist makes the *design* safe without the answer, so what is really blocked is the
fixtures its tests use. And the polling schedule's numbers, per §6.

**My recommendation:** Step 3's plan can be written now, with §3's allowlist as its stated rule and
§6's numbers marked provisional in the plan itself. The probe's findings then change two constants
and a set of test fixtures rather than any architecture. That is a better use of the gap than
waiting, provided the plan is explicit that those two things are unconfirmed — which it will be.

But that is your call, and the prompt says stop here. Whichever way you want to go, the probe is
ready to run.

---

## Sources

First-party, and used for §1:

- [safaricom/mpesa-node-library — `src/endpoints/lipa-na-mpesa-query.js`](https://github.com/safaricom/mpesa-node-library/blob/master/src/endpoints/lipa-na-mpesa-query.js)
- [safaricom/mpesa-api-C-Sharp — `StkqueryApi.cs`](https://github.com/safaricom/mpesa-api-C-Sharp/blob/master/StkqueryApi.cs)
- [Daraja developer portal](https://developer.safaricom.co.ke/apis/MpesaExpressQuery) — heading only; the specification is client-side rendered and was not retrievable

Third-party, and cited in §2 only to document that they disagree:

- [mctaba.com — STK push timeout handling compared across providers](https://www.mctaba.com/learn/paystack/m-pesa-stk-push-timeout-handling-compared-across-providers)
- [KenZobe — M-Pesa Daraja API common errors and fixes](https://www.kenzobe.com/blog/mpesa-daraja-api-errors)
- [Tuma — Common M-Pesa Daraja API error codes](https://tuma.co.ke/common-mpesa-daraja-api-error-codes-explanation-and-mitigation/)
