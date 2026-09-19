package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type UserRepo struct{ db DBTX }

const userCols = `id, phone_number, email, password_hash, full_name, kyc_status, created_at, updated_at`

func (r *UserRepo) Create(ctx context.Context, u *models.User) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO users (phone_number, email, password_hash, full_name)
		VALUES ($1, $2, $3, $4)
		RETURNING id, kyc_status, created_at, updated_at`,
		u.PhoneNumber, u.Email, u.PasswordHash, u.FullName,
	).Scan(&u.ID, &u.KYCStatus, &u.CreatedAt, &u.UpdatedAt)
	return mapErr(err)
}

func (r *UserRepo) GetByPhone(ctx context.Context, phone string) (*models.User, error) {
	var u models.User
	err := r.db.QueryRow(ctx,
		`SELECT `+userCols+` FROM users WHERE phone_number = $1`, phone,
	).Scan(&u.ID, &u.PhoneNumber, &u.Email, &u.PasswordHash, &u.FullName, &u.KYCStatus, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	var u models.User
	err := r.db.QueryRow(ctx,
		`SELECT `+userCols+` FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.PhoneNumber, &u.Email, &u.PasswordHash, &u.FullName, &u.KYCStatus, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

// Search backs the admin user list. It matches phone, email, full name or
// account number, so a support agent can paste whatever the user gave them.
func (r *UserRepo) Search(ctx context.Context, query string, limit, offset int) ([]models.User, int, error) {
	const where = `
		WHERE $1 = ''
		   OR u.phone_number ILIKE '%' || $1 || '%'
		   OR u.email ILIKE '%' || $1 || '%'
		   OR u.full_name ILIKE '%' || $1 || '%'
		   OR EXISTS (SELECT 1 FROM accounts a
		              WHERE a.user_id = u.id AND a.account_number ILIKE '%' || $1 || '%')`

	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM users u`+where, query).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}

	rows, err := r.db.Query(ctx,
		`SELECT u.id, u.phone_number, u.email, u.password_hash, u.full_name, u.kyc_status,
		        u.created_at, u.updated_at
		 FROM users u`+where+` ORDER BY u.created_at DESC LIMIT $2 OFFSET $3`,
		query, limit, offset)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()

	out := []models.User{}
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.PhoneNumber, &u.Email, &u.PasswordHash, &u.FullName,
			&u.KYCStatus, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

// SetKYCStatus is called by the admin review flow, inside the same
// transaction that records the decision.
func (r *UserRepo) SetKYCStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET kyc_status = $2, updated_at = now() WHERE id = $1`, id, status)
	return mapErr(err)
}

func (r *UserRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, mapErr(err)
}
