-- Admin console. Admin auth is deliberately separate from the trading `users`
-- table: different login surface, different token scope, no phone/OTP overlap.

CREATE TABLE IF NOT EXISTS admin_users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(50) UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    role VARCHAR(20) DEFAULT 'admin', -- admin | support
    must_change_password BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT now()
);

-- Every admin action that touches money or account state lands here. This is
-- what makes a powerful console an auditable one.
CREATE TABLE IF NOT EXISTS admin_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id UUID REFERENCES admin_users(id),
    action VARCHAR(50) NOT NULL,
    target_type VARCHAR(20) NOT NULL, -- user | account | transaction
    target_id UUID NOT NULL,
    reason TEXT,
    metadata JSONB,
    created_at TIMESTAMPTZ DEFAULT now()
);

-- Freeze blocks the account from moving on its own. It does not block an
-- admin adjustment, and it does not block closing an already-open position.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'active';
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS frozen_reason TEXT;

CREATE TABLE IF NOT EXISTS kyc_submissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id),
    id_number VARCHAR(30),
    document_url TEXT,
    status VARCHAR(20) DEFAULT 'pending', -- pending | approved | rejected
    reviewed_by UUID REFERENCES admin_users(id),
    review_reason TEXT,
    submitted_at TIMESTAMPTZ DEFAULT now(),
    reviewed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_audit_target ON admin_audit_log(target_type, target_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_created ON admin_audit_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_kyc_status ON kyc_submissions(status, submitted_at DESC);
CREATE INDEX IF NOT EXISTS idx_kyc_user ON kyc_submissions(user_id);

-- Cross-account admin search needs these; the user-facing queries are all
-- account-scoped and already covered.
CREATE INDEX IF NOT EXISTS idx_transactions_created ON transactions(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_transactions_reference ON transactions(reference);
CREATE INDEX IF NOT EXISTS idx_users_phone_trgm ON users(phone_number varchar_pattern_ops);
