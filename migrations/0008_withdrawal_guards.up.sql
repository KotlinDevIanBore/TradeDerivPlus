-- One withdrawal in flight per account.
--
-- A withdrawal debits the balance immediately and waits on a payout that does
-- not exist yet (see docs/investigation-reports/b2c-withdrawals-investigation.md
-- §3.3). Once it does, a double-submitted request is two real payouts, and cash
-- leaving the business twice cannot be reversed by any Daraja API. The cheapest
-- and strongest guard is to make a second in-flight withdrawal impossible in the
-- database rather than careful in the code, so no future caller can forget it.
--
-- Partial and UNIQUE, like idx_bot_runs_one_live_per_account (0006), which
-- enforces the same "one live thing per account" rule for automated runs.
-- Settled withdrawals are not covered, so an account may withdraw again as soon
-- as the previous payout resolves.
CREATE UNIQUE INDEX IF NOT EXISTS idx_transactions_one_pending_withdrawal
    ON transactions (account_id)
    WHERE type = 'withdrawal' AND status = 'pending';
