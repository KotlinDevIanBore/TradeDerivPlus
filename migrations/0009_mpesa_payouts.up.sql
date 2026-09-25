-- Payout requests: the outbound half of M-Pesa, and deliberately its own table.
--
-- NOT columns on mpesa_requests. That table is STK-shaped down to its column
-- names, and — the reason that matters — the deposit reconciler's ListPending
-- selects every pending row in it, STK-queries each, and on a successful answer
-- CREDITS the wallet. A payout row living there would be queried with the wrong
-- API and, on a parse that looked like success, would credit a customer for
-- their own withdrawal. There is no discriminator column to add that would make
-- that safe, because every reader would still have to learn about it.
--
-- One row per withdrawal, and one withdrawal per row: transaction_id is UNIQUE.
-- originator_conversation_id is ours, chosen and committed before the request is
-- sent, and UNIQUE because it is the key Daraja's callbacks and any status query
-- come back on.
CREATE TABLE IF NOT EXISTS mpesa_payouts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID NOT NULL UNIQUE REFERENCES transactions(id),

    -- The id we generate. Daraja's field for it exists "to avoid double
    -- disbursement"; whether Safaricom actually deduplicates on it is unproven
    -- (see the deferred list), so nothing here resends on the strength of it.
    -- It is still the only handle that exists before Daraja answers.
    originator_conversation_id VARCHAR(128) NOT NULL UNIQUE,
    -- Safaricom's own id, known only once a request has been accepted.
    conversation_id VARCHAR(128),

    -- The full MSISDN. The ledger row keeps only a masked form ("***678"), which
    -- is right for a ledger and useless to a payout: without this column the
    -- destination is lost the moment the request returns.
    phone_number VARCHAR(15) NOT NULL,
    amount NUMERIC(18,2) NOT NULL,

    -- pending        reserved alongside the debit; nothing sent yet
    -- sent           Daraja accepted it and gave us a ConversationID
    -- unknown        the send was ambiguous: Daraja MAY hold this request. The
    --                one state from which nothing may be assumed in either
    --                direction — no settling, and above all no refunding
    -- never_received established that Safaricom never had the request at all.
    --                Distinct from 'unknown' on purpose: it is the only state in
    --                which a refund is provably safe without Daraja having
    --                spoken about the payment, and the only one from which a
    --                resend could ever be safe if that is confirmed later
    -- completed      Daraja reported result code 0; the customer has the money
    -- failed         Daraja refused it, or reported a terminal failure code
    status VARCHAR(20) NOT NULL DEFAULT 'pending',

    -- Stamped before the request leaves, under a guard, so exactly one send is
    -- ever attempted per row and a crash mid-send cannot hide that we tried.
    requested_at TIMESTAMPTZ,

    -- Text, not an integer: B2C's documented codes include SFC_IC0003.
    result_code VARCHAR(32),
    result_desc TEXT,
    receipt VARCHAR(64),
    -- How the outcome was established: callback | timeout | query | admin.
    settled_by VARCHAR(16),

    -- Reconciliation state, the shape 0007 gave deposits. The pass itself is a
    -- later checkpoint; these exist so the rows it will read are already shaped
    -- for it.
    reconcile_attempts INTEGER NOT NULL DEFAULT 0,
    next_query_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- What a callback arrives on.
CREATE INDEX IF NOT EXISTS idx_mpesa_payouts_conversation
    ON mpesa_payouts (conversation_id) WHERE conversation_id IS NOT NULL;

-- What the reconciliation pass will scan: rows whose outcome is still open and
-- whose next look has come. Partial, like idx_mpesa_requests_pending_next.
CREATE INDEX IF NOT EXISTS idx_mpesa_payouts_open_next
    ON mpesa_payouts (next_query_at)
    WHERE status IN ('pending', 'sent', 'unknown');

-- What an operator needs to find: everything still open, oldest first. 'unknown'
-- and 'never_received' are the two an operator has to act on, so they are
-- reachable without a scan of the whole table.
CREATE INDEX IF NOT EXISTS idx_mpesa_payouts_status_created
    ON mpesa_payouts (status, created_at DESC);
