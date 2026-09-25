# Implementation report: self-serve KYC document upload via Cloudflare R2

**Date:** 26 September 2026 · **Base:** `master` @ `358a45c` · **Branches:** `stage1/go-124`, then `feat/kyc-r2-upload`
**Plan:** `docs/kyc-r2-upload-plan.md` (as revised — see §2)
**Scope:** all six stages implemented, tested and committed. 45 files, +3,905/−65.

---

## 0. Where this ends up

A user can now verify their identity, which before this was impossible by any
route. `docs/kyc-status-report.md` recorded the consequence: the withdrawal gate
refused everyone, so `withdrawals_enabled` was never what was blocking
withdrawals. That blocker is gone.

**Nothing is switched on.** `withdrawals_enabled` and `bot_real_money_enabled`
are both still false, and `KYC_DOCUMENT_RETENTION_DAYS` is still `0`, which
means never delete. The retention period remains a compliance decision nobody
has made — §7 restates it, because it is the one thing in this report that can
still go wrong by being forgotten.

**Everything was verified against the in-memory stub store. No stage was
verified against a real R2 bucket**, because no R2 credentials exist in this
environment. §6 says exactly what that leaves unproven.

| Stage | Commit | Files | What landed |
|---|---|---|---|
| 1 | `f7b6dbb` | 2 | Go 1.24 toolchain bump, isolated |
| 2 | `35531ce` | 13 | Migration 0010, models, repo, config |
| 3 | `3c191e4` | 13 | `internal/storage`, presign endpoint |
| 4 | `25202fd` | 10 | Confirm endpoint, audited admin document view |
| 5 | `c70a674` | 19 | Verification sheet, admin viewer, a reachable stub |
| 6 | `f5dbb91` | 7 | Orphan sweep, retention mechanism, e2e |

---

## 1. Stage by stage

### Stage 1 — Go 1.24 (`f7b6dbb`)

`go.mod` `1.22.5` → `1.24.0`; `.github/workflows/ci.yml` `GO_VERSION` `"1.22"` → `"1.24"`.
No feature content, as the plan required, so it is revertible on its own.

`GOTOOLCHAIN=auto` meant the installed 1.22.5 self-fetched 1.24 — no manual install.

All green under 1.24: build, vet, gofmt, unit suite, integration suite (78s),
`tsc`, web unit suite (130 tests). Opened as **PR #12**.

**Not verifiable from here, and still open:** whether Railway's Nixpacks follows
`go.mod`. There is no `Dockerfile`, `nixpacks.toml` or `.tool-versions`, so the
build's Go version is inferred. See §6.

### Stage 2 — schema and config (`35531ce`)

Migration `0010_kyc_documents`: renames `document_url` → `document_key`; adds
`content_type` and `size_bytes`; `users.kyc_status` defaults to `unverified`;
`CHECK` constraints on both status columns (the plan left this optional — taken,
since the withdrawal gate's allowlist was otherwise the only thing between a
typo and a wrong answer); and the new `kyc_upload_attempts` table.

Config gains the four R2 credentials on the payout block's all-or-none rule,
plus `KYC_MAX_DOCUMENT_BYTES`, the two URL expiries, the abandoned-upload
window, the retention period and the sweep interval. Validation is extracted as
`validateKYCStorage` so a test reaches it with a `Config` literal — the direct
lesson of the B2C status-URL bug, where `Load` read a value correctly and a
constructor silently dropped it.

Verified: migration applied to a scratch database on top of 0001–0009 and
reversed with its down file. The rename lands, the default is `unverified`, the
`CHECK` refuses `'nonsense'` and accepts the default, both partial indexes are
created, and the down migration restores all of it.

### Stage 3 — storage and presign (`3c191e4`)

`internal/storage` lifts the neighbouring codebase's client construction
(account endpoint, `Region: "auto"`, static credentials, path-style) and
deliberately does not lift its multipart machinery, its cleanup worker, its
four-hour download expiry, or its environment-reading constructor.

`PresignPut` is new: it signs `Content-Type` **and** `Content-Length`, which is
the entire reason for choosing a single PUT. `TestPresignPutSignsTypeAndLength`
asserts both appear in `X-Amz-SignedHeaders`; without them a valid upload URL is
a licence to store anything of any size.

`PresignUpload` refuses cheapest-first, then writes the attempt row, then signs.
The attempt goes in *before* the URL is handed out — the reverse order is the
one that leaks a URL nothing recorded.

### Stage 4 — confirm and the admin view (`25202fd`)

`Submit` re-checks everything against the bucket: the attempt exists, is the
caller's and is unconsumed; no review pending; the object is there; its **actual**
size and type pass; and its first 512 bytes really are that type.

The sniff uses `http.DetectContentType` — verified during planning to cover all
three allowed types and to identify a renamed HTML file, so no dependency was
added.

`KYCDocumentURL` signs first, audits in a transaction, and returns the URL only
if that commit succeeded. It is a `POST` in the audited actions group, not a
`GET` among the reads, because it writes.

### Stage 5 — UI (`c70a674`)

`VerificationSheet` (form → uploading → submitted → failed), the two previously
disabled buttons wired, `KYCQueue`'s document cell turned into a fetch-on-click
button, `kyc_document_view` added to the audit filter and its badge maps.

The upload is a bare `fetch`, not the `api` wrapper, which JSON-stringifies
bodies and attaches the bearer token — neither belongs on a request to an object
store.

### Stage 6 — sweeps and e2e (`f5dbb91`)

One ticker, two jobs with separate switches: the orphan sweep always on, document
retention off until a period exists. `cmd/seed` stops writing a placeholder key.
The compliance blocker goes on the deferred list. `web/e2e/user.kyc.spec.ts`
walks the real flow.

---

## 2. The two requested changes, and where they differed

### 2.1 Skip the backfill — no difference

Implemented exactly as directed. `0010` sets the new default and contains no
`UPDATE`. Stage 2 was unblocked and went ahead.

One consequence, recorded rather than fixed: the two pre-existing users still
read `pending` with no submission behind them, so they appear in the admin
queue's pending filter as rows that cannot be opened (the document cell reads
"None"). With two rows that is a curiosity. Moving them is one `UPDATE` if it
ever becomes annoying.

### 2.2 Resumable uploads — three differences

**Separate table, not a status.** The direction allowed either. `kyc_upload_attempts`
was chosen, and the reason is specific rather than aesthetic: `KYCRepo.List`
filters `($1 = '' OR status = $1)`, the console's "All" filter sends
`status=all`, and `AdminHandler.KYCQueue` maps that to the empty string. An
`'uploading'` row would therefore have appeared in the review queue as a
submission to review, with a NULL ID number and possibly no object behind it.
Avoiding that would have meant auditing five readers and hoping none was missed.

**The attempt id became the submission id.** The plan spoke of a "submission id"
minted at presign, which under the original D8 named something that did not
exist yet. Rather than invent a second identifier, the attempt's id *becomes* the
submission's id at confirm, so one identifier covers the lifecycle and the object
key names the row it belongs to. This required `KYCRepo.Create` to take an
explicit id instead of generating one, which is why `cmd/seed` and one existing
test changed.

**Consumption is marked by `consumed_at`, not a `submission_id` column.** With
one shared id, a foreign key back to the submission would have been the same
value twice. A timestamp also records *when*, which the sweep's query wants.

Everything else matches the direction: the attempt captures user, key and
creation time; `kyc_status` still moves only at confirm; the UI offers "You
didn't finish verification — continue"; continuing starts a completely fresh
upload; and the sweep cleans unconsumed attempts older than 48 hours.

---

## 3. Deviations from the plan

| # | Deviation | Why |
|---|---|---|
| 1 | **The stub gained a real HTTP route** (`PUT /kyc/stub-upload/*key`) and `PUBLIC_API_URL` | The plan's stub signed URLs at an unreachable host, so only Go tests calling it directly could complete an upload. That would have left D14's promise — seed, CI and local dev work without a bucket — true only for Go tests, and the browser flow untestable. The route is registered **only** when no object store is configured, and is unauthenticated for the same reason a presigned URL is: the signature is the credential, and the stub verifies key, expiry, content type and length exactly as a real store would. |
| 2 | **No socket event on submit** | The plan's step 8 emitted a `kyc` event. The user's own client already invalidates on the 200, and `KYCService` has no hub; wiring one for multi-tab sync was not worth the coupling. The admin *decision* still emits the existing event, which is the one that matters — it is how the e2e test sees "verified" arrive without a reload. |
| 3 | **`GET /kyc/state` added** | Not in the plan's route list. The plan put verification state on `/auth/me`; a separate endpoint keeps the unfinished-attempt flag out of the response every screen fetches, and gives the sheet something to invalidate on its own. `/auth/me` is unchanged. |
| 4 | **`unverified` got its own badge** | Found in the browser: an unverified user's badge read "Pending", because the badge mapped everything non-verified and non-rejected onto `pending`. Now that the two are distinct, that told a user who had submitted nothing that we were reviewing something. |
| 5 | **`CHECK` constraints taken** | The plan left these optional and asked for an explicit answer. Taken. |
| 6 | **Repository tests live in `internal/service`** | The plan implied repository-level tests; `internal/repository` has no harness and building a second one to reach three functions was not worth it. The service harness already exposes `store` and `pool`. |
| 7 | **`cmd/seed` writes no document key** | The plan said the placeholder "follows Stage 6". What it became: nothing at all. The seed process has its own in-memory stub, separate from the API's, so any key it wrote would name an object the console could never fetch — a View button that always failed. Empty renders as "None", which is true. |

---

## 4. Test results

All suites run at each stage, not deferred to the end.

| Suite | Result |
|---|---|
| `go build ./...`, `go vet ./...`, `gofmt -l` | clean at every stage |
| Unit suite (`go test ./...`) | pass |
| Integration suite (`-tags integration`) | pass — 73–91s depending on stage |
| Web typecheck (`tsc -b --noEmit`) | pass |
| Web unit suite | 130 tests pass |
| New KYC e2e (`user.kyc.spec.ts`) | 2 pass |
| Full desktop e2e suite | 20 pass, 3 skipped |
| Migration up/down on a scratch database | verified both directions |

**New tests: 42.** 11 storage (presigning is local, so no network and no bucket),
4 config, 9 presign, 16 confirm and admin-view, 3 repository-level, 7 retention,
2 e2e.

### The Stage 4 mutation matrix

Each mutation was verified present in the file and `go vet`-clean before its
result was read, and both touched files restored byte-identically afterwards
(`sha256sum -c`, twice).

| # | Mutation | Result |
|---|---|---|
| M1 | disable the content sniff | **FAILS** — 2 tests |
| M2 | drop the actual-size cap at confirm | **FAILS** |
| M3 | drop the attempt-ownership check | **FAILS** |
| M4 | drop the consume guard | **SURVIVES** — covered, see below |
| M5 | drop the audit write | **FAILS** |
| M6 | return the URL despite an audit failure | initially **SURVIVED**, now FAILS |

**M4 survives, and the honest reason.** `Submit` prechecks both the attempt's
consumption and any pending submission, and the submission's primary key is the
attempt's id reused — so a duplicate confirm is refused by any of three layers.
A concurrency test with four racers passes either way: the diagnostic showed the
prechecks catching all three losers, so the race never reached the guard. Rather
than write a test that appears to isolate it but does not, the guard's contract
is pinned directly at the repository in `TestConsumeAttemptIsSingleUse`, and the
concurrency test states in its own comment what it does and does not prove. This
is the same pattern as the STK work's unreachable guards.

**M6 survived until a test existed for it.** Nothing forced an audit failure.
`admin_audit_log`'s foreign key on `admin_id` provides one deterministically — an
unknown admin cannot be audited — which reaches exactly the window the
presign-then-audit-then-return ordering exists to close. With
`TestDocumentViewWithholdsTheURLWhenTheAuditFails`, M6 fails.

### Verified by hand, over HTTP and in the browser

- The three-step flow by `curl`: presign → PUT → confirm, with the row landing
  at the **actual** content type and size.
- An HTML file uploaded as `image/jpeg`: the store accepts it (the signature
  covers the header, not the bytes) and confirm refuses it on the sniff with
  422, leaving the attempt open.
- All four UI states at 375×812: unverified → "Start verification"; abandoned →
  "Continue verification" with "You didn't finish verification"; pending → "Your
  documents are with us" and **no** button; and a full upload through the real
  file picker reaching "Document received". The network trace shows
  `upload-url` 200, `stub-upload` PUT 200, `submit` 201.
- Route registration and auth: all three user routes and the admin route answer
  401 anonymously, not 404.

---

## 5. Left undone

| Item | What it needs |
|---|---|
| **Railway Nixpacks Go version** | Merge PR #12, deploy, read the build log. If it does not follow `go.mod`, add a `nixpacks.toml` or `Dockerfile` pin. Not verifiable from here. |
| **A real R2 bucket** | §6. |
| **`scripts/admin_check.py`** | The plan mentioned extending it. Not done — it already consumes the seeded KYC queue before Playwright runs, which is the known order-dependence on the deferred list, and adding upload coverage there would deepen that coupling. The e2e spec covers the flow instead. |
| **CI environment for the new e2e** | The spec needs no bucket and no new variables, so it should run as-is, but it has not been run *in CI* — only locally against a local API. |
| **Merging anything** | All six commits are local. Stage 1 is PR #12; stages 2–6 are on `feat/kyc-r2-upload` and not yet pushed or opened. |

---

## 6. Stub versus real bucket — what is actually proven

**Every stage was verified against the in-memory stub. None was verified against
R2.** No credentials exist here, and none were invented.

What the stub does prove, because it enforces what a signature enforces — it
refuses an unsigned key, an expired URL, a mismatched content type and a
mismatched length: the service's ordering, every refusal, the sniff, the
transaction boundaries, the audit ordering, the sweeps, and the whole browser
flow including a genuine cross-origin PUT.

What only a real bucket can prove:

1. **That R2 honours the signed `Content-Length`.** The SDK puts it in
   `X-Amz-SignedHeaders` — asserted — but whether R2 rejects a mismatched body is
   Cloudflare's behaviour, not ours. If it does not, the confirm-time size check
   is the backstop, and that is tested.
2. **That the browser's automatic `Content-Length` matches the signature.** It
   cannot be set from script, so the client skips it and relies on the browser
   computing the file's real length. Correct in principle; unproven against a
   real signature check.
3. **`ResponseContentDisposition: inline`** actually rendering in the browser.
4. **Credentials, endpoint and bucket policy**: that the account endpoint
   resolves, path-style addressing works, and CORS allows `PUT` from the Vercel
   origin. The last is configured in the Cloudflare dashboard and cannot be
   asserted from this repo at all.

The first real-bucket upload should be watched for exactly these four.

---

## 7. The retention blocker — still unresolved

**This is the item most likely to be lost, so it is restated in full.**

The *mechanism* is built and tested. `KYC_DOCUMENT_RETENTION_DAYS` drives a sweep
that deletes the stored document and clears `document_key`, while the submission,
its decision, its reviewer, the user's verified status and the audit trail all
survive. Retention applies to the document, never to the record that a decision
was made — `TestRetentionDeletesTheDocumentAndKeepsTheDecision` asserts each of
those survivals individually.

**The period is not set, and nothing in this work set it.** The default is `0`,
which means never delete. `TestRetentionDoesNothingAtZero` exists so the default
cannot start deleting by accident, and a negative value is refused at boot as a
typo rather than a policy.

**Before self-serve verification is enabled for real customers, a retention
period must come from whoever owns legal and compliance for this business**,
together with an answer to whether approved and rejected submissions retain for
the same period. Kenya's Data Protection Act 2019 applies to this data.

Shipping the code with `0` is honest: nothing is destroyed under a period nobody
chose. **Enabling the feature for real customers while it is still `0` is not**
— it means accumulating identity documents indefinitely with no deletion policy.

Tracked on `docs/investigation-reports/auto-bot-deferred.md` as a launch blocker
separate from the engineering. The 48-hour orphan sweep for *abandoned* uploads
is unrelated and always on.

---

## 8. Conscious gaps, unchanged from the plan

- **No malware scanning.** Mitigations present: a three-type allowlist enforced
  by magic bytes rather than a client label, a 10 MiB cap, a private bucket, and
  short per-click URLs. If it is wanted later, confirm is where it belongs — it
  already reads the object's head bytes.
- **No liveness, authenticity or document forensics.** An admin looking at an
  image remains the entire verification control. This work gives that admin
  something to look at; it does not make the review stronger.
- **No AML or transaction monitoring**, including the finding from
  `docs/kyc-status-report.md` that nothing checks the withdrawal destination
  number belongs to the account holder. Identity verification is not AML.
- **No tiering**; verification stays binary and ceilings stay global.
- **No OCR or cross-check of `id_number`** against the document or any registry.

## 9. What this did not change

The withdrawal gate (`wallet_service.go:548-550`), the single payout path, the
ceilings, the `ReviewKYC` transaction and its audit row, and both feature flags.
A verified user still cannot withdraw until `withdrawals_enabled` is turned on
deliberately. This removed the blocker that made that flag meaningless, and
nothing more.
