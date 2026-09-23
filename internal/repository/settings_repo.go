package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const SettingDigitContractsEnabled = "digit_contracts_enabled"

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
