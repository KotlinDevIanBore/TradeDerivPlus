-- Self-serve KYC document upload: Stage 2 of docs/kyc-r2-upload-plan.md.
--
-- Schema only. No R2 client, no endpoints, nothing user-visible yet.

-- `document_url` has only ever held a placeholder string, because no upload flow
-- existed. It will now hold a Cloudflare R2 object key, which is not a URL, and
-- a column named for the wrong thing is how a reader ends up trusting it as one:
-- the admin console currently decides whether to render a link by testing
-- `startsWith('http')`. Renaming removes that as a security boundary.
--
-- Stays TEXT and stays nullable. Nullable matters: document retention nulls this
-- column after deleting the object, keeping the decision and losing the document.
ALTER TABLE kyc_submissions RENAME COLUMN document_url TO document_key;

-- What was actually confirmed present in the bucket, rather than what the browser
-- claimed at presign time. Nullable because rows predating this migration have
-- neither, and because retention clears the key without rewriting history.
ALTER TABLE kyc_submissions ADD COLUMN IF NOT EXISTS content_type VARCHAR(100);
ALTER TABLE kyc_submissions ADD COLUMN IF NOT EXISTS size_bytes BIGINT;

-- A user who has submitted nothing was recorded as 'pending', which is also what
-- a user awaiting review is recorded as. Once uploads exist that collision is
-- actively misleading, so new users start 'unverified' and 'pending' comes to mean
-- exactly one thing: documents are in, a person has not looked yet.
--
-- Deliberately no backfill of existing rows (plan section 4.1): this project has
-- two users and no real submissions, so there is nothing for a backfill to fix.
ALTER TABLE users ALTER COLUMN kyc_status SET DEFAULT 'unverified';

-- Neither status column was constrained, so the withdrawal gate's allowlist test
-- (`!= 'verified'`) was the only thing standing between a typo and a wrong answer.
-- Cheap to state, and it makes an invalid write fail where it happens.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_kyc_status_check;
ALTER TABLE users ADD CONSTRAINT users_kyc_status_check
    CHECK (kyc_status IN ('unverified', 'pending', 'verified', 'rejected'));

ALTER TABLE kyc_submissions DROP CONSTRAINT IF EXISTS kyc_submissions_status_check;
ALTER TABLE kyc_submissions ADD CONSTRAINT kyc_submissions_status_check
    CHECK (status IN ('pending', 'approved', 'rejected'));

-- An upload attempt, written when a presigned URL is issued and consumed when the
-- submission is confirmed (plan section 4.2, revising D8).
--
-- Its own table rather than an 'uploading' status on kyc_submissions, because
-- every reader of that table treats a row as something an admin can act on, and
-- KYCRepo.List filters on `($1 = '' OR status = $1)` while the console's "All"
-- filter sends a status the handler maps to the empty string. An 'uploading' row
-- would therefore appear in the review queue with no ID number and possibly no
-- object behind it. A separate table cannot leak into that queue by construction.
--
-- `id` is minted at presign, embedded in the object key, and becomes the
-- submission's own id at confirm — one identifier for the whole lifecycle, so a
-- key names the submission it will become rather than something unrelated.
CREATE TABLE IF NOT EXISTS kyc_upload_attempts (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    object_key TEXT NOT NULL UNIQUE,
    content_type VARCHAR(100) NOT NULL,
    size_bytes BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Set in the same transaction that inserts the submission, so an attempt
    -- cannot be consumed by a submission that did not commit.
    consumed_at TIMESTAMPTZ
);

-- "Has this user an unfinished upload?" — the question the account screen asks to
-- decide between "Start verification" and "You didn't finish verification".
CREATE INDEX IF NOT EXISTS idx_kyc_attempts_open
    ON kyc_upload_attempts (user_id, created_at DESC) WHERE consumed_at IS NULL;

-- The orphan sweep's query: abandoned attempts past the cutoff. Distinct from
-- document retention, and needing no compliance input — a 48-hour abandoned
-- upload is not a record of anything.
CREATE INDEX IF NOT EXISTS idx_kyc_attempts_abandoned
    ON kyc_upload_attempts (created_at) WHERE consumed_at IS NULL;
