-- Reverse of 0011_digit_contracts_settled_index.up.sql.
--
-- Note: repository.Migrate only reads *.up.sql (internal/repository/db.go:75), so
-- this file is never executed by the application. It exists for the same reason
-- 0010_kyc_documents.down.sql does — to state how the change reverses, and to be
-- runnable by hand against a database that needs rolling back.

DROP INDEX IF EXISTS idx_digit_contracts_settled;
