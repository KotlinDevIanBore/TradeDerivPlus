-- Runtime settings. DIGIT_CONTRACTS_ENABLED (default true when unset) seeds
-- digit_contracts_enabled on first boot when the row is missing; after that
-- the DB row is the source of truth so the flag can flip without restarting.

CREATE TABLE IF NOT EXISTS app_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
