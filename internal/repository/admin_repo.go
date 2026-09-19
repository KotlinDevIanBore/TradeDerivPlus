package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type AdminRepo struct{ db DBTX }

const adminCols = `id, username, password_hash, role, must_change_password, created_at`

func (r *AdminRepo) Create(ctx context.Context, a *models.AdminUser) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO admin_users (username, password_hash, role, must_change_password)
		VALUES ($1, $2, $3, COALESCE($4, true))
		RETURNING id, must_change_password, created_at`,
		a.Username, a.PasswordHash, a.Role, a.MustChangePassword,
	).Scan(&a.ID, &a.MustChangePassword, &a.CreatedAt)
	return mapErr(err)
}

func (r *AdminRepo) GetByUsername(ctx context.Context, username string) (*models.AdminUser, error) {
	var a models.AdminUser
	err := r.db.QueryRow(ctx, `SELECT `+adminCols+` FROM admin_users WHERE username = $1`, username).
		Scan(&a.ID, &a.Username, &a.PasswordHash, &a.Role, &a.MustChangePassword, &a.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

func (r *AdminRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.AdminUser, error) {
	var a models.AdminUser
	err := r.db.QueryRow(ctx, `SELECT `+adminCols+` FROM admin_users WHERE id = $1`, id).
		Scan(&a.ID, &a.Username, &a.PasswordHash, &a.Role, &a.MustChangePassword, &a.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &a, nil
}

// SetPassword also clears must_change_password: the two always move together,
// so there is no way to change the password and leave the flag set.
func (r *AdminRepo) SetPassword(ctx context.Context, id uuid.UUID, hash string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE admin_users SET password_hash = $2, must_change_password = false WHERE id = $1`,
		id, hash)
	return mapErr(err)
}

func (r *AdminRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM admin_users`).Scan(&n)
	return n, mapErr(err)
}
