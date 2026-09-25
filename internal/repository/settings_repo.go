package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const SettingDigitContractsEnabled = "digit_contracts_enabled"

// Automated-trading flags. Unlike the digit flag, which only chooses which
// trading surface the UI renders, bot_enabled gates the service itself: turning
// it off refuses new runs and stops live ones.
const (
	SettingBotEnabled = "bot_enabled"
	// SettingBotAllowlist is a comma-separated list of user IDs. Empty means
	// everyone, so it is a narrowing tool for a staged rollout rather than a
	// second switch that has to be remembered.
	SettingBotAllowlist = "bot_allowlist"
	// SettingBotRealMoneyEnabled keeps v1 demo-only by configuration rather than
	// by code, so shipping to real money is a decision someone makes rather than
	// a release someone cuts.
	SettingBotRealMoneyEnabled = "bot_real_money_enabled"

	// SettingWithdrawalsEnabled gates the withdrawal path. Seeded FALSE: a
	// withdrawal debits the balance and waits on an M-Pesa payout that does not
	// exist yet, so until one does, offering the feature is offering to take
	// money and pay nobody. The only thing standing between a real customer and
	// that today is that identity verification is admin-only and has no user
	// flow — an accident, not a guard. This is the guard.
	SettingWithdrawalsEnabled = "withdrawals_enabled"
)

// SettingsRepo is a small key/value store for runtime flags. The DB row is the
// source of truth after bootstrap; env only seeds a missing key.
type SettingsRepo struct{ db DBTX }

func (r *SettingsRepo) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := r.db.QueryRow(ctx,
		`SELECT value FROM app_settings WHERE key = $1`, key,
	).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return value, err
}

// Set inserts or updates a key. used by bootstrap and by operators flipping
// flags with SQL (or a future admin button).
func (r *SettingsRepo) Set(ctx context.Context, key, value string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE
		SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		key, value, time.Now().UTC(),
	)
	return err
}
