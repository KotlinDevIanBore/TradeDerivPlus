package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type WalletRepo struct{ db DBTX }

func (r *WalletRepo) Create(ctx context.Context, accountID uuid.UUID, real, bonus decimal.Decimal) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO wallets (account_id, real_balance, bonus_balance) VALUES ($1, $2, $3)`,
		accountID, real, bonus)
	return mapErr(err)
}

func (r *WalletRepo) Get(ctx context.Context, accountID uuid.UUID) (*models.Wallet, error) {
	var w models.Wallet
	err := r.db.QueryRow(ctx,
		`SELECT account_id, real_balance, bonus_balance, updated_at FROM wallets WHERE account_id = $1`, accountID,
	).Scan(&w.AccountID, &w.RealBalance, &w.BonusBalance, &w.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &w, nil
}

// GetForUpdate takes a row lock. Call it inside WithTx before any balance
// change so two concurrent trades cannot both read the same stale balance.
func (r *WalletRepo) GetForUpdate(ctx context.Context, accountID uuid.UUID) (*models.Wallet, error) {
	var w models.Wallet
	err := r.db.QueryRow(ctx,
		`SELECT account_id, real_balance, bonus_balance, updated_at
		 FROM wallets WHERE account_id = $1 FOR UPDATE`, accountID,
	).Scan(&w.AccountID, &w.RealBalance, &w.BonusBalance, &w.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &w, nil
}

// Adjust moves `amount` (signed) on the given balance type. It refuses to let a
// balance go negative, which is the last line of defence behind the service
// layer's own checks.
func (r *WalletRepo) Adjust(ctx context.Context, accountID uuid.UUID, balanceType string, amount decimal.Decimal) (*models.Wallet, error) {
	col := "real_balance"
	if balanceType == models.BalanceBonus {
		col = "bonus_balance"
	}

	var w models.Wallet
	err := r.db.QueryRow(ctx, `
		UPDATE wallets
		SET `+col+` = `+col+` + $2, updated_at = now()
		WHERE account_id = $1 AND `+col+` + $2 >= 0
		RETURNING account_id, real_balance, bonus_balance, updated_at`,
		accountID, amount,
	).Scan(&w.AccountID, &w.RealBalance, &w.BonusBalance, &w.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &w, nil
}
