package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type AccountRepo struct{ db DBTX }

const accountCols = `id, user_id, account_number, account_type, currency, status, frozen_reason, created_at`

// Create allocates the next account number from the sequence and inserts.
func (r *AccountRepo) Create(ctx context.Context, a *models.Account) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO accounts (user_id, account_number, account_type, currency)
		VALUES ($1, nextval('account_number_seq')::text, $2, $3)
		RETURNING id, account_number, created_at`,
		a.UserID, a.AccountType, a.Currency,
	).Scan(&a.ID, &a.AccountNumber, &a.CreatedAt)
	return mapErr(err)
}

func (r *AccountRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]models.Account, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+accountCols+` FROM accounts WHERE user_id = $1 ORDER BY account_type DESC, created_at`, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []models.Account{}
	for rows.Next() {
		var a models.Account
		if err := rows.Scan(&a.ID, &a.UserID, &a.AccountNumber, &a.AccountType, &a.Currency, &a.Status, &a.FrozenReason, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *AccountRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.Account, error) {
	var a models.Account
	err := r.db.QueryRow(ctx,
		`SELECT `+accountCols+` FROM accounts WHERE id = $1`, id,
	).Scan(&a.ID, &a.UserID, &a.AccountNumber, &a.AccountType, &a.Currency, &a.Status, &a.FrozenReason, &a.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

// CountByType is used to enforce one demo + one real account per user.
func (r *AccountRepo) CountByType(ctx context.Context, userID uuid.UUID, accountType string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM accounts WHERE user_id = $1 AND account_type = $2`, userID, accountType).Scan(&n)
	return n, mapErr(err)
}

// GetForUpdate locks the account row. The freeze check runs through this,
// inside the same transaction that locks the wallet, so a freeze committed a
// millisecond earlier is seen by a trade already in flight. A check outside
// the transaction would be a race the freeze could lose.
func (r *AccountRepo) GetForUpdate(ctx context.Context, id uuid.UUID) (*models.Account, error) {
	var a models.Account
	err := r.db.QueryRow(ctx,
		`SELECT `+accountCols+` FROM accounts WHERE id = $1 FOR UPDATE`, id,
	).Scan(&a.ID, &a.UserID, &a.AccountNumber, &a.AccountType, &a.Currency, &a.Status, &a.FrozenReason, &a.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

// SetStatus freezes or unfreezes. The reason is stored on the account for the
// UI and also written to the audit log by the service.
func (r *AccountRepo) SetStatus(ctx context.Context, id uuid.UUID, status string, reason *string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE accounts SET status = $2, frozen_reason = $3 WHERE id = $1`, id, status, reason)
	return mapErr(err)
}

// ListByUsers fetches accounts for many users at once, so the admin user list
// renders in one round trip instead of one query per row.
func (r *AccountRepo) ListByUsers(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID][]models.Account, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+accountCols+` FROM accounts WHERE user_id = ANY($1) ORDER BY account_type DESC`, userIDs)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := map[uuid.UUID][]models.Account{}
	for rows.Next() {
		var a models.Account
		if err := rows.Scan(&a.ID, &a.UserID, &a.AccountNumber, &a.AccountType, &a.Currency,
			&a.Status, &a.FrozenReason, &a.CreatedAt); err != nil {
			return nil, err
		}
		out[a.UserID] = append(out[a.UserID], a)
	}
	return out, rows.Err()
}
