-- Automated runs. A run is one durable AUTO session on one account; the contracts it
-- places point back via digit_contracts.bot_run_id. The totals on this row are maintained
-- inside the contract open/settle transactions, so they cannot drift from the contracts
-- they summarise.

CREATE TABLE IF NOT EXISTS bot_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id),
    symbol_id UUID NOT NULL REFERENCES symbols(id),
    -- running | stopping | stopped. stopping = stop requested, one contract still in flight.
    status VARCHAR(10) NOT NULL DEFAULT 'running',
    -- user | admin_kill | disabled | stop_loss | take_profit | max_consecutive_losses |
    -- max_contracts | max_duration | insufficient_funds | account_frozen | market_closed |
    -- feed_down | restart | shutdown | error
    stop_reason VARCHAR(32),
    last_error TEXT,
    -- Strategy (v1: fixed). Same shape as digit_contracts so the row reads like the
    -- contracts it places.
    contract_type VARCHAR(32) NOT NULL,
    selection JSONB NOT NULL,
    stake NUMERIC(18,2) NOT NULL,
    interval_ms INTEGER NOT NULL,
    -- Stop conditions: stop_loss, take_profit, max_consecutive_losses, max_contracts,
    -- max_duration_s. Absent key = not set. stop_loss is always present.
    limits JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Informational: real + bonus at start. Stop-loss is judged on realised_pnl, never on
    -- balance, because a human may trade the same account meanwhile.
    starting_balance NUMERIC(18,2) NOT NULL,
    contracts_placed INTEGER NOT NULL DEFAULT 0,
    wins INTEGER NOT NULL DEFAULT 0,
    losses INTEGER NOT NULL DEFAULT 0,
    voids INTEGER NOT NULL DEFAULT 0,
    -- Reset by a win, untouched by a void.
    consecutive_losses INTEGER NOT NULL DEFAULT 0,
    -- Reset by any settlement. Three in a row means the feed is not delivering.
    consecutive_voids INTEGER NOT NULL DEFAULT 0,
    realised_pnl NUMERIC(18,2) NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    stopped_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One live run per account. 'stopping' counts as live so a second run cannot start while
-- the first still has a contract in flight.
CREATE UNIQUE INDEX IF NOT EXISTS idx_bot_runs_one_live_per_account
    ON bot_runs (account_id) WHERE status IN ('running', 'stopping');
CREATE INDEX IF NOT EXISTS idx_bot_runs_account_started
    ON bot_runs (account_id, started_at DESC);

-- Provenance: which run placed a contract. NULL means a human did.
ALTER TABLE digit_contracts ADD COLUMN IF NOT EXISTS bot_run_id UUID REFERENCES bot_runs(id);
CREATE INDEX IF NOT EXISTS idx_digit_contracts_bot_run
    ON digit_contracts (bot_run_id) WHERE bot_run_id IS NOT NULL;
