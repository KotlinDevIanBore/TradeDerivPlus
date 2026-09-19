package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type KYCRepo struct{ db DBTX }

const kycSelect = `
	SELECT k.id, k.user_id, COALESCE(k.id_number, ''), COALESCE(k.document_url, ''),
	       k.status, k.reviewed_by, k.review_reason, k.submitted_at, k.reviewed_at,
	       u.phone_number, u.full_name, a.username
	FROM kyc_submissions k
	JOIN users u ON u.id = k.user_id
	LEFT JOIN admin_users a ON a.id = k.reviewed_by`

func scanKYC(row interface{ Scan(dest ...any) error }) (*models.KYCSubmission, error) {
	var k models.KYCSubmission
	err := row.Scan(&k.ID, &k.UserID, &k.IDNumber, &k.DocumentURL, &k.Status,
		&k.ReviewedBy, &k.ReviewReason, &k.SubmittedAt, &k.ReviewedAt,
		&k.UserPhone, &k.UserFullName, &k.ReviewerName)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (r *KYCRepo) Create(ctx context.Context, k *models.KYCSubmission) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO kyc_submissions (user_id, id_number, document_url)
		VALUES ($1, $2, $3) RETURNING id, status, submitted_at`,
		k.UserID, k.IDNumber, k.DocumentURL,
	).Scan(&k.ID, &k.Status, &k.SubmittedAt)
	return mapErr(err)
}

func (r *KYCRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.KYCSubmission, error) {
	k, err := scanKYC(r.db.QueryRow(ctx, kycSelect+` WHERE k.id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return k, nil
}

func (r *KYCRepo) List(ctx context.Context, status string, limit, offset int) ([]models.KYCSubmission, int, error) {
	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM kyc_submissions WHERE ($1 = '' OR status = $1)`, status).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}

	rows, err := r.db.Query(ctx,
		kycSelect+` WHERE ($1 = '' OR k.status = $1) ORDER BY k.submitted_at DESC LIMIT $2 OFFSET $3`,
		status, limit, offset)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()

	out := []models.KYCSubmission{}
	for rows.Next() {
		k, err := scanKYC(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *k)
	}
	return out, total, rows.Err()
}

func (r *KYCRepo) ListForUser(ctx context.Context, userID uuid.UUID) ([]models.KYCSubmission, error) {
	rows, err := r.db.Query(ctx, kycSelect+` WHERE k.user_id = $1 ORDER BY k.submitted_at DESC`, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []models.KYCSubmission{}
	for rows.Next() {
		k, err := scanKYC(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

// Review records the decision. Only a pending submission can be reviewed, so a
// second click on a stale screen changes nothing.
func (r *KYCRepo) Review(ctx context.Context, id, adminID uuid.UUID, status, reason string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE kyc_submissions
		SET status = $2, reviewed_by = $3, review_reason = $4, reviewed_at = now()
		WHERE id = $1 AND status = 'pending'`, id, status, adminID, reason)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PendingCount drives the sidebar badge.
func (r *KYCRepo) PendingCount(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM kyc_submissions WHERE status = 'pending'`).Scan(&n)
	return n, mapErr(err)
}
