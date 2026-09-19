-- TradeDerivPlus initial schema
-- Source of truth: docs/spec. Ledger (`transactions`) is append-only; `wallets` is a cache.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone_number VARCHAR(15) UNIQUE NOT NULL,
    email VARCHAR(255) UNIQUE,
    password_hash TEXT NOT NULL,
    full_name VARCHAR(255),
    kyc_status VARCHAR(20) DEFAULT 'pending',
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE IF NOT EXISTS accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    account_number VARCHAR(20) UNIQUE NOT NULL,
    account_type VARCHAR(10) NOT NULL,
    currency VARCHAR(5) DEFAULT 'USD',
    created_at TIMESTAMPTZ DEFAULT now()
);

-- Account numbers are handed out from a sequence starting at 900001 (matches the reference UI).
CREATE SEQUENCE IF NOT EXISTS account_number_seq START WITH 900001 INCREMENT BY 1;

CREATE TABLE IF NOT EXISTS wallets (
    account_id UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    real_balance NUMERIC(18,2) DEFAULT 0,
    bonus_balance NUMERIC(18,2) DEFAULT 0,
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE IF NOT EXISTS transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID REFERENCES accounts(id),
    type VARCHAR(20) NOT NULL,
    balance_type VARCHAR(10) NOT NULL,
    amount NUMERIC(18,2) NOT NULL,
    status VARCHAR(20) DEFAULT 'pending',
    reference VARCHAR(100),
    metadata JSONB,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE IF NOT EXISTS mpesa_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID REFERENCES transactions(id),
    checkout_request_id VARCHAR(100) UNIQUE,
    merchant_request_id VARCHAR(100),
    phone_number VARCHAR(15),
    amount NUMERIC(18,2),
    status VARCHAR(20) DEFAULT 'pending',
    result_code INT,
    result_desc TEXT,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE IF NOT EXISTS symbols (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(20) UNIQUE NOT NULL,
    display_name VARCHAR(100),
    category VARCHAR(20),
    pip_size INT DEFAULT 2,
    is_active BOOLEAN DEFAULT true
);

CREATE TABLE IF NOT EXISTS trades (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID REFERENCES accounts(id),
    symbol_id UUID REFERENCES symbols(id),
    direction VARCHAR(4) NOT NULL,
    entry_price NUMERIC(18,5) NOT NULL,
    exit_price NUMERIC(18,5),
    stake NUMERIC(18,2) NOT NULL,
    stop_loss NUMERIC(18,5),
    take_profit NUMERIC(18,5),
    status VARCHAR(10) DEFAULT 'open',
    pnl NUMERIC(18,2),
    opened_at TIMESTAMPTZ DEFAULT now(),
    closed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS signals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    symbol_id UUID REFERENCES symbols(id),
    direction VARCHAR(4) NOT NULL,
    confidence NUMERIC(5,2),
    entry_price NUMERIC(18,5),
    stop_loss NUMERIC(18,5),
    take_profit NUMERIC(18,5),
    strategy VARCHAR(50),
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_transactions_account ON transactions(account_id);
CREATE INDEX IF NOT EXISTS idx_transactions_account_created ON transactions(account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_trades_account_status ON trades(account_id, status);
CREATE INDEX IF NOT EXISTS idx_trades_account_closed ON trades(account_id, closed_at DESC);
CREATE INDEX IF NOT EXISTS idx_signals_symbol_created ON signals(symbol_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_signals_created ON signals(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_accounts_user ON accounts(user_id);
