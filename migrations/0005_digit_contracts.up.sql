-- Parallel digit-contract product. Distinct from trades: fixed payout, tick
-- expiry, binary win/loss/void — not proportional CFD settlement.

CREATE TABLE IF NOT EXISTS digit_contracts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id),
    symbol_id UUID NOT NULL REFERENCES symbols(id),
    -- matches_differs | even_odd | over_under
    contract_type VARCHAR(32) NOT NULL,
    -- Typed JSON per contract type (see models.DigitSelection).
    selection JSONB NOT NULL,
    stake NUMERIC(18,2) NOT NULL,
    -- Snapshot at open from the static payout table (placeholders until product confirms).
    payout_ratio NUMERIC(10,6) NOT NULL,
    potential_payout NUMERIC(18,2) NOT NULL,
    entry_price NUMERIC(18,5) NOT NULL,
    entry_digit SMALLINT NOT NULL CHECK (entry_digit BETWEEN 0 AND 9),
    entry_tick_epoch BIGINT NOT NULL,
    -- open | won | lost | void
    status VARCHAR(10) NOT NULL DEFAULT 'open',
    settlement_price NUMERIC(18,5),
    settlement_digit SMALLINT CHECK (settlement_digit IS NULL OR settlement_digit BETWEEN 0 AND 9),
    settled_at TIMESTAMPTZ,
    pnl NUMERIC(18,2),
    opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- After this instant, an still-open contract past expiry with no usable
    -- settlement tick is auto-voided and the stake refunded.
    void_after TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_digit_contracts_account_status
    ON digit_contracts (account_id, status);
CREATE INDEX IF NOT EXISTS idx_digit_contracts_open_symbol
    ON digit_contracts (symbol_id)
    WHERE status = 'open';
CREATE INDEX IF NOT EXISTS idx_digit_contracts_open_void
    ON digit_contracts (void_after)
    WHERE status = 'open';
