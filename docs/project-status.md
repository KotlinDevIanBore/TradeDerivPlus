# Where this project is

**Date:** 26 September 2026 · **Default branch:** `master` @ `358a45c` (PR #10 merged)
**Production:** up — `GET /health` returns `{"mock_mpesa":false,"status":"ok"}`

A single page for picking the thread back up: what is merged, what is waiting,
what is broken, and what needs a decision from someone rather than more code.

---

## 1. The one-line summary

Deposits reconcile, withdrawals have a payout path, and users can now verify
their identity — but **no real customer can withdraw yet**, because
`withdrawals_enabled` is off and the retention period for identity documents has
not been set. Both are deliberate. Neither is an engineering task.

---

## 2. Merged and live

| PR | What it did |
|---|---|
| #6 | M-Pesa deposit reconciliation (STK) and B2C withdrawals — the two large features |
| #7 | Fixed a boot crash: the B2C status-result URL was read from config and silently dropped by the constructor |
| #8 | Fixed Vercel serving its own 404 for every client-side route — deep links and refreshes were broken app-wide, not just `/admin` |
| #9 | Recorded #8's verification; logged the bot stop-reason defect |
| #10 | Fixed that defect: a cancelled root context is now recorded as a shutdown, not a run failure |

**Production state:** the API boots with real Daraja credentials and passes B2C
validation. `/admin/login` is reachable. Deposits reconcile on a schedule.

---

## 3. Open, waiting on you

| PR | State | What it needs |
|---|---|---|
| [#11](https://github.com/KotlinDevIanBore/TradeDerivPlus/pull/11) | Docs only, one row | Merge whenever. Moves the bot stop-reason item to Closed, since #10 fixed it |
| [#12](https://github.com/KotlinDevIanBore/TradeDerivPlus/pull/12) | **All six checks green** | Merge, deploy, then **check the Railway build log picked Go 1.24**. There is no `Dockerfile` or `nixpacks.toml`, so the build's Go version is inferred — this is the one thing about it that cannot be verified from here |
| [#13](https://github.com/KotlinDevIanBore/TradeDerivPlus/pull/13) | KYC complete; Go tests intermittently red (§5) | Based on #12. Merge that first and this retargets to `master` cleanly |

---

## 4. What #13 contains

Self-serve identity verification, in six stages. Before it, **no production user
could ever reach `verified` by any route**, so the withdrawal gate refused
everyone and `withdrawals_enabled` was never the real blocker. That is fixed.

- Migration 0010: `document_url` → `document_key`, `content_type`/`size_bytes`,
  `kyc_status` defaults to `unverified`, `CHECK` constraints on both status
  columns, and a `kyc_upload_attempts` table.
- `internal/storage`: an R2 client whose presigned PUT signs **both**
  `Content-Type` and `Content-Length`, plus an in-memory stub with a real HTTP
  route so the whole flow works with no bucket.
- Three-step upload: presign → the browser PUTs straight to the store → confirm.
  Confirm re-checks the object's **actual** size and type and sniffs its first
  512 bytes, so a label cannot stand in for the bytes.
- Admin document viewing, audited: the URL is signed first, the audit row written
  in a transaction, and the URL returned only if that commit succeeded.
- An abandoned upload is remembered, so the user is offered "continue" rather
  than silently reset, and a 48-hour sweep cleans what nobody finished.

42 new tests. The flow verified in a real browser end to end. Full detail in
[`docs/kyc-r2-implementation-report.md`](kyc-r2-implementation-report.md).

---

## 5. What is actually broken: the bot integration tests

**This is the live problem, and it is not KYC.**

Two different bot tests have failed intermittently in CI on #13, neither on #12:

- `TestBotKillSwitchStopsAllRuns` — `ending broadcast 0 times, want 1`
- `TestAdminStopsOneRunAndRecordsIt` — `ledger 1047.6 disagrees with wallet cache 1037.6`, exactly one $10 stake

Both pass locally, repeatedly. #13 touches no bot code — but it adds 42 tests to
the same integration package, so the suite runs longer and under more load, and
that is exposing timing races that were already there.

**The honest reading:** these are pre-existing races in the bot tests, surfaced
rather than caused by #13. The earlier instinct to call the first one a flake was
wrong — the same inference was wrong about the shutdown race fixed in #10, which
turned out to be a real defect that only CI's timing exposed. A ledger
disagreeing with a wallet cache is exactly the shape of thing not to wave away.

**Recommendation:** treat this as its own piece of work rather than re-running CI
on #13 until it goes green. The second failure in particular — a settled stake
missing from one of the two views of the same money — deserves looking at on its
own terms, not as an obstacle to merging a feature.

---

## 6. Decisions that are not engineering's

### 6.1 The retention period — launch blocker

`KYC_DOCUMENT_RETENTION_DAYS` is `0`, which means **never delete**.

The mechanism is built and tested: when set, a sweep deletes the stored document
and clears `document_key` while the submission, its decision, its reviewer, the
user's verified status and the audit trail all survive. Retention applies to the
document, never to the record that a decision was made.

The number is not set, and this work did not set one. **Before self-serve
verification is enabled for real customers, a period must come from whoever owns
legal and compliance**, together with an answer to whether approved and rejected
submissions retain for the same period. Kenya's Data Protection Act 2019 applies
to this data.

Shipping at `0` is honest — nothing is destroyed under a period nobody chose.
Enabling the feature while it is still `0` means accumulating identity documents
indefinitely with no deletion policy.

### 6.2 Turning withdrawals on

`withdrawals_enabled` is `false`. Flipping it needs, in order:

1. The four `MPESA_*` payout values set in Railway (initiator, credential, and
   the three distinct callback URLs) — already validated at boot.
2. A funded M-Pesa Utility account.
3. The retention decision above, because withdrawals require verification and
   verification now means storing ID documents.
4. `UPDATE app_settings SET value='true' WHERE key='withdrawals_enabled';` —
   takes effect within 10 seconds, no restart. There is still no console control
   for it, deliberately.

### 6.3 Still unproven: a real R2 bucket

Every KYC stage was verified against the in-memory stub. **None was verified
against R2**, because no credentials exist in this environment. Four things only
a real bucket can settle:

1. Whether R2 honours the signed `Content-Length`.
2. Whether the browser's automatic `Content-Length` matches the signature.
3. `Content-Disposition: inline` rendering in the browser.
4. Credentials, endpoint, and a bucket CORS policy allowing `PUT` from the Vercel
   origin — the last is a Cloudflare dashboard setting this repo cannot assert.

Watch the first real upload for exactly those.

---

## 7. Current flags

| Flag | Value |
|---|---|
| `bot_enabled` | `true` |
| `bot_real_money_enabled` | **`false`** |
| `digit_contracts_enabled` | `false` (set locally for the CFD e2e projects; production may differ) |
| `withdrawals_enabled` | **`false`** |
| `KYC_DOCUMENT_RETENTION_DAYS` | **`0`** — never delete |

---

## 8. Known gaps, consciously accepted

- **No AML or transaction monitoring.** Identity verification is not AML. Most
  pointedly, nothing checks that a withdrawal's destination M-Pesa number belongs
  to the account holder — the customer supplies an arbitrary MSISDN per request.
- **No malware scanning** on uploaded documents.
- **No liveness or document-authenticity check.** An admin looking at an image is
  the entire verification control.
- **No KYC tiering**; withdrawal ceilings are global.
- **Whether Safaricom deduplicates a repeated `OriginatorConversationID`** is
  still unproven, so nothing ever resends a payout. Conservative and correct
  either way; the probe needs sandbox credentials and a callback catcher.
- The full list lives in
  [`docs/investigation-reports/auto-bot-deferred.md`](investigation-reports/auto-bot-deferred.md).

---

## 9. Reading order for the detail

| Document | What it covers |
|---|---|
| [`kyc-status-report.md`](kyc-status-report.md) | KYC as it stood before this work — the audit that found the gap |
| [`kyc-r2-upload-investigation.md`](kyc-r2-upload-investigation.md) | The R2 comparison against the neighbouring production codebase |
| [`kyc-r2-upload-plan.md`](kyc-r2-upload-plan.md) | The staged plan, including the retention section |
| [`kyc-r2-implementation-report.md`](kyc-r2-implementation-report.md) | What was built, eight deviations and why, the mutation matrix |
| [`investigation-reports/b2c-withdrawals-investigation.md`](investigation-reports/b2c-withdrawals-investigation.md) | The payout design |
| [`investigation-reports/auto-bot-deferred.md`](investigation-reports/auto-bot-deferred.md) | Everything open, with why |
