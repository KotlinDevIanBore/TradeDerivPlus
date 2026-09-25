-- Reverse of 0010_kyc_documents.up.sql.
--
-- Note: repository.Migrate only reads *.up.sql (internal/repository/db.go:75), so
-- this file is never executed by the application. It exists for the same reason
-- 0003_admin.down.sql does — to state how the change reverses, and to be runnable
-- by hand against a database that needs rolling back.

DROP INDEX IF EXISTS idx_kyc_attempts_abandoned;
DROP INDEX IF EXISTS idx_kyc_attempts_open;
DROP TABLE IF EXISTS kyc_upload_attempts;

ALTER TABLE kyc_submissions DROP CONSTRAINT IF EXISTS kyc_submissions_status_check;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_kyc_status_check;

-- Back to the old default. Any row already written as 'unverified' would fail the
-- pre-0010 code's expectations, so a real rollback should also decide what those
-- users become; there is deliberately no UPDATE here, because the answer depends
-- on why the rollback is happening.
ALTER TABLE users ALTER COLUMN kyc_status SET DEFAULT 'pending';

ALTER TABLE kyc_submissions DROP COLUMN IF EXISTS size_bytes;
ALTER TABLE kyc_submissions DROP COLUMN IF EXISTS content_type;
ALTER TABLE kyc_submissions RENAME COLUMN document_key TO document_url;
