-- A time-ranged aggregate over settled contracts, for the admin finance screen.
--
-- digit_contracts has four indexes (0005, 0006) and none covers a settlement time
-- range, so `WHERE settled_at >= $1 AND settled_at < $2` sequentially scans the
-- whole table. Fine at thousands of rows; an automated run placing a contract
-- every two seconds is 43,200 rows per account-day.
-- See docs/investigation-reports/financial-reporting-investigation.md section 4.1.
--
-- Partial, like idx_digit_contracts_open_symbol and idx_digit_contracts_open_void
-- above it: an open contract has no settled_at and does not belong in this index.
-- It also serves the min(settled_at) lookup the "all" range uses, as an index-end
-- read rather than a scan.
--
-- Plain CREATE INDEX, not CONCURRENTLY: repository.Migrate wraps each file in a
-- transaction (internal/repository/db.go:96), and CONCURRENTLY cannot run inside
-- one. At the current row count the lock is momentary. Whoever revisits this at
-- scale should build the index by hand, CONCURRENTLY, outside the migration.
CREATE INDEX IF NOT EXISTS idx_digit_contracts_settled
    ON digit_contracts (settled_at DESC)
    WHERE settled_at IS NOT NULL;
