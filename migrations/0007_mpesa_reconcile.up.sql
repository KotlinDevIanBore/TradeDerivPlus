-- Reconciliation state for STK pushes whose callback has not arrived.
--
-- On this table and not on the ledger: transactions is never updated except for
-- the pending -> terminal move (see transaction_repo.go), and an attempt counter
-- is not that. The reconciler keys on checkout_request_id, which is here.
--
-- reconcile_attempts counts the queries Daraja answered without resolving the
-- deposit. A transport failure is not an attempt. 0 means "never looked", which
-- the console can show against "looked, and Daraja can't tell yet".
--
-- next_query_at is when the pass may next ask. NULL on a pending row means the
-- row was never scheduled; the pass skips it and the stale-deposit gauge, which
-- keys on created_at, still counts it.
ALTER TABLE mpesa_requests
    ADD COLUMN IF NOT EXISTS reconcile_attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_query_at TIMESTAMPTZ;

-- Backfill: anything already pending has waited long enough and is eligible on
-- the first pass. Terminal rows are left NULL; nothing will look at them.
UPDATE mpesa_requests
SET next_query_at = created_at
WHERE status = 'pending' AND next_query_at IS NULL;

-- What the pass scans, in the order it scans it. Partial, like
-- idx_digit_contracts_open_void (0005): the pending set is small and the table
-- is not.
CREATE INDEX IF NOT EXISTS idx_mpesa_requests_pending_next
    ON mpesa_requests (next_query_at)
    WHERE status = 'pending';
