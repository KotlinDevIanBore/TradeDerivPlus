# KYC document upload: implementation status and deployment readiness

**Date:** 26 September 2026 · **Branch:** `feat/kyc-r2-upload` ([PR #13](https://github.com/KotlinDevIanBore/TradeDerivPlus/pull/13))
**Depends on:** [PR #12](https://github.com/KotlinDevIanBore/TradeDerivPlus/pull/12) (Go 1.24), all checks green

---

## 1. Verdict

**The code is complete and safe to deploy. The feature is not ready to be used by
real customers, and deploying it does not switch it on.**

Those are two different statements and the difference is the whole point of this
page:

| Question | Answer |
|---|---|
| Can this be merged and deployed? | **Yes** — with no R2 credentials it refuses uploads with 503 and changes nothing else |
| Will deploying it expose anything to customers? | **No** — the sheet appears, the endpoints answer 503, nothing is stored |
| Can a customer verify their identity after deploying? | **Only once an R2 bucket is configured** (§4) |
| Can a customer withdraw after that? | **No** — `withdrawals_enabled` is still false, and it should stay false until §5 is done |
| Is it lawful to turn on for real customers? | **Not yet.** No retention period has been set (§5.1) |

---

## 2. What is implemented

Six stages, all committed and tested. Before this, no production user could ever
reach `verified`, so the withdrawal gate refused everyone.

| Stage | Commit | Content |
|---|---|---|
| 1 | `f7b6dbb` | Go 1.24 toolchain (PR #12, separate) |
| 2 | `35531ce` | Migration 0010, models, repository, config |
| 3 | `3c191e4` | `internal/storage`, presign endpoint |
| 4 | `25202fd` | Confirm endpoint with content sniffing, audited admin document view |
| 5 | `c70a674` | Verification sheet, admin viewer, browser-reachable stub |
| 6 | `f5dbb91` | Orphan sweep, retention mechanism, e2e |
| — | `daa3e3d`, `c39ce45` | Python scripts corrected for the new field and default |
| — | `ec120fd` | **Production fails closed with no bucket** (§3.1) |

**Tests: 42 new.** Unit, integration, web unit, and two e2e. Full detail and the
Stage 4 mutation matrix in
[`kyc-r2-implementation-report.md`](kyc-r2-implementation-report.md).

---

## 3. Deployment safety

### 3.1 The one thing that was wrong, and is now fixed

An earlier revision of this branch fell back to the **in-memory stub whenever R2
was unconfigured, regardless of environment** — and the comment above it claimed
it answered 503 in production.

Deployed to production without credentials, that would have: accepted identity
documents into memory; told the customer their documents were submitted; shown an
operator a submission with nothing behind it; lost everything on the next
restart; and registered `PUT /kyc/stub-upload/*key`, which is public and
unauthenticated by design because the stub's signature is meant to be the only
credential.

Now three outcomes rather than two:

| `APP_ENV` | R2 configured | Behaviour |
|---|---|---|
| any | yes | Real client |
| not production | no | In-memory stub; the development upload route is registered |
| **production** | **no** | **No store. Uploads answer 503. The development route does not exist** |

Verified with `APP_ENV=production` and no credentials: boot logs an ERROR naming
the four variables, `POST /kyc/upload-url` returns `503 kyc_storage_unavailable`,
and `PUT /kyc/stub-upload/*` returns 404.

### 3.2 What a deploy actually changes

- **Migration 0010 runs at boot.** It renames `document_url` → `document_key`,
  adds two nullable columns, changes a default, adds two `CHECK` constraints and
  creates one table. Verified both directions against a scratch database. No
  backfill, no data rewrite. Existing rows are untouched.
- **Three new user routes and one admin route** appear. All authenticated. The
  user routes answer 503 until a bucket exists.
- **A sweep goroutine starts.** Its orphan half has nothing to find; its retention
  half does nothing at all while `KYC_DOCUMENT_RETENTION_DAYS` is `0`.
- **The account screen and withdraw sheet change copy**, and the previously
  disabled "Start verification" buttons become live. With no bucket they open a
  sheet that fails with "Identity verification is not available right now".
- **No flag changes.** `withdrawals_enabled` and `bot_real_money_enabled` stay
  false.

### 3.3 The `CHECK` constraints — the one migration risk

Migration 0010 adds `CHECK` constraints to `users.kyc_status` and
`kyc_submissions.status`. **If production holds any value outside the allowed
sets, the migration fails and the API will not boot.**

Allowed: `unverified | pending | verified | rejected` for users;
`pending | approved | rejected` for submissions.

Only `ReviewKYC` ever writes these, and it writes only those values, so this
should be safe. Confirm before deploying anyway — it costs one query:

```sql
SELECT DISTINCT kyc_status FROM users;
SELECT DISTINCT status FROM kyc_submissions;
```

### 3.4 Rollback

The Go change reverts by deploying the previous image. The migration does not
auto-revert — `repository.Migrate` only runs `*.up.sql`. To roll the schema back,
run `migrations/0010_kyc_documents.down.sql` by hand; it restores the old column
name, drops the new columns, constraints and table, and puts the default back.

Rolling the code back **without** the schema is safe in one direction only: old
code reading `document_url` would break against the renamed column. Roll both or
neither.

---

## 4. To make verification work: the R2 setup

None of this is in the repository, and none of it can be done from here.

### 4.1 Cloudflare

1. Create a bucket **dedicated to this project**. Do not reuse the neighbouring
   project's bucket — that would put trading identity documents in the same
   namespace as another product's public content.
2. Leave the **public bucket URL off** and attach **no custom domain**. R2 has no
   bucket ACLs, so "private" is a dashboard state this code cannot assert or
   verify.
3. Create an **API token scoped to that bucket only**, with object read/write.
4. Set a **CORS policy** allowing `PUT` from the Vercel origin. This is what makes
   the browser upload possible and it is **not** configurable from this repo:

```json
[
  {
    "AllowedOrigins": ["https://trade-deriv-plus.vercel.app"],
    "AllowedMethods": ["PUT"],
    "AllowedHeaders": ["*"],
    "MaxAgeSeconds": 3600
  }
]
```

### 4.2 Railway variables

Required — all four or none; a partial set is refused at boot:

```
R2_ACCOUNT_ID
R2_ACCESS_KEY_ID
R2_SECRET_ACCESS_KEY
R2_BUCKET_NAME
```

Optional, with the defaults shown:

```
R2_ENDPOINT                  (derived from the account id)
KYC_MAX_DOCUMENT_BYTES=10485760
KYC_UPLOAD_URL_EXPIRY=10m
KYC_REVIEW_URL_EXPIRY=5m
KYC_ABANDONED_UPLOAD_AFTER=48h
KYC_RETENTION_EVERY=1h
KYC_DOCUMENT_RETENTION_DAYS=0    ← see §5.1 before changing
```

`PUBLIC_API_URL` is **not** needed in production: it only builds URLs for the
development stub, which production never uses.

### 4.3 Watch the first real upload for these four

Every stage was verified against the in-memory stub. **Nothing was verified
against R2**, because no credentials exist in this environment. The stub enforces
what a signature enforces — it refuses an unsigned key, an expired URL, a
mismatched type and a mismatched length — so the service logic is well covered.
What it cannot prove:

1. **That R2 honours the signed `Content-Length`.** The SDK puts it in
   `X-Amz-SignedHeaders` (asserted by a test); whether R2 rejects a mismatched
   body is Cloudflare's behaviour. If it does not, the confirm-time size check is
   the backstop, and that is tested.
2. **That the browser's automatic `Content-Length` matches the signature.** It
   cannot be set from script, so the client omits it and relies on the browser
   computing the file's real length.
3. **`Content-Disposition: inline`** actually rendering rather than downloading.
4. **Credentials, endpoint resolution, path-style addressing, and the CORS
   policy.** A CORS mistake shows up as the PUT failing in the browser while
   everything else works.

A single upload through the UI exercises all four at once.

---

## 5. Before real customers use it

### 5.1 ⚠️ The retention period — the blocker

`KYC_DOCUMENT_RETENTION_DAYS` is `0`, which means **never delete**.

The mechanism is built and tested: when set, a sweep deletes the stored document
and clears `document_key`, while the submission, its decision, its reviewer, the
user's verified status and the audit trail all survive. Retention applies to the
document, never to the record that a decision was made.

**The number is not set, and no part of this work set one.** It must come from
whoever owns legal and compliance for this business, together with an answer to
whether approved and rejected submissions retain for the same period. Kenya's
Data Protection Act 2019 applies to this data.

Deploying with `0` is fine — nothing is destroyed under a period nobody chose.
**Inviting real customers to upload identity documents while it is still `0` means
accumulating them indefinitely with no deletion policy**, which is the thing to
avoid.

`TestRetentionDoesNothingAtZero` exists so the default cannot start deleting by
accident, and a negative value is refused at boot as a typo rather than a policy.

### 5.2 Operational readiness

- **Someone has to review submissions.** Verification is a manual decision; a
  queue with nobody watching it is a customer waiting indefinitely. The console
  shows a pending count in the sidebar.
- **An admin account must exist in production.** Separately tracked: nothing in
  the deploy path creates one, `cmd/seed` refuses outside development, and there
  is no env-var bootstrap. It needs one hand-written `INSERT`.
- **Document views are audited** under `kyc_document_view`, visible in the console's
  audit log. Worth knowing that trail exists before operators start using it.

### 5.3 Then, separately, withdrawals

Verification unlocks the *gate*; it does not open withdrawals. In order:

1. The four `MPESA_*` payout values in Railway (already validated at boot).
2. A funded M-Pesa Utility account.
3. §5.1 resolved.
4. `UPDATE app_settings SET value='true' WHERE key='withdrawals_enabled';` —
   effective within 10 seconds, no restart, no console control by design.

---

## 6. Known gaps in this feature, consciously accepted

- **No malware scanning.** Mitigated by a three-type allowlist enforced by magic
  bytes rather than a client label, a 10 MiB cap, a private bucket and
  short-lived per-click URLs. If wanted, confirm is where it belongs — it already
  reads the object's first bytes.
- **No liveness or document-authenticity check.** An admin looking at an image is
  the entire verification control. This work gives them something to look at; it
  does not make the judgement stronger.
- **No OCR or cross-check of `id_number`** against the document or any registry.
- **No tiering.** Verification is binary; withdrawal ceilings stay global.
- **No AML.** Unchanged and still absent — including that nothing checks a
  withdrawal's destination number belongs to the account holder. Identity
  verification is not AML.

---

## 7. Deployment checklist

**Deploy the code**

- [ ] Merge PR #12; confirm from the Railway build log that Nixpacks selected Go 1.24
- [ ] `GET /health` still returns `{"mock_mpesa":false,"status":"ok"}`
- [ ] Run the two `SELECT DISTINCT` queries in §3.3
- [ ] Merge PR #13; watch the boot log for the migration and for the object-storage line
- [ ] With no bucket yet, confirm `POST /api/v1/kyc/upload-url` returns 503

**Enable verification**

- [ ] Bucket created, public URL off, no custom domain
- [ ] Scoped API token issued
- [ ] CORS policy set for the Vercel origin
- [ ] Four `R2_*` variables set in Railway; boot log reads `object storage ready`
- [ ] One upload through the UI end to end, watching §4.3's four
- [ ] An admin account exists and someone is watching the queue

**Before inviting real customers**

- [ ] **Retention period obtained from legal/compliance and set** (§5.1)
- [ ] Decide whether approved and rejected submissions retain for the same period

**Withdrawals — later, separately**

- [ ] `MPESA_*` payout values, funded Utility account
- [ ] `withdrawals_enabled` flipped deliberately
