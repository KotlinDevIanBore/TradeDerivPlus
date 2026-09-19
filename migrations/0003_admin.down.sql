DROP TABLE IF EXISTS kyc_submissions;
DROP TABLE IF EXISTS admin_audit_log;
DROP TABLE IF EXISTS admin_users;
ALTER TABLE accounts DROP COLUMN IF EXISTS status;
ALTER TABLE accounts DROP COLUMN IF EXISTS frozen_reason;
