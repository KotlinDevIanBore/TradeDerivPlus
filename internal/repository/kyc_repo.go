package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type KYCRepo struct{ db DBTX }

const kycSelect = `
	SELECT k.id, k.user_id, COALESCE(k.id_number, ''), COALESCE(k.document_key, ''),
	       COALESCE(k.content_type, ''), COALESCE(k.size_bytes, 0),
	       k.status, k.reviewed_by, k.review_reason, k.submitted_at, k.reviewed_at,
	       u.phone_number, u.full_name, a.username
	FROM kyc_submissions k
	JOIN users u ON u.id = k.user_id
	LEFT JOIN admin_users a ON a.id = k.reviewed_by`

func scanKYC(row interface{ Scan(dest ...any) error }) (*models.KYCSubmission, error) {
	var k models.KYCSubmission
	err := row.Scan(&k.ID, &k.UserID, &k.IDNumber, &k.DocumentKey,
		&k.ContentType, &k.SizeBytes, &k.Status,
		&k.ReviewedBy, &k.ReviewReason, &k.SubmittedAt, &k.ReviewedAt,
		&k.UserPhone, &k.UserFullName, &k.ReviewerName)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// Create inserts a submission. The id is supplied by the caller rather than
// generated here: it was minted at presign, it is embedded in the object key, and
// reusing it means one identifier covers the attempt and the submission it became.
func (r *KYCRepo) Create(ctx context.Context, k *models.KYCSubmission) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO kyc_submissions (id, user_id, id_number, document_key, content_type, size_bytes)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, status, submitted_at`,
		k.ID, k.UserID, k.IDNumber, k.DocumentKey, k.ContentType, k.SizeBytes,
	).Scan(&k.ID, &k.Status, &k.SubmittedAt)
	return mapErr(err)
}

// PendingForUser reports whether this user already has a submission awaiting
// review. One open submission per user, the same reasoning as one in-flight
// withdrawal: a second is never what the person meant, and it doubles an
// operator's work.
func (r *KYCRepo) PendingForUser(ctx context.Context, userID uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM kyc_submissions WHERE user_id = $1 AND status = 'pending')`,
		userID).Scan(&exists)
	return exists, mapErr(err)
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

const attemptCols = `id, user_id, object_key, content_type, size_bytes, created_at, consumed_at`

func scanAttempt(row interface{ Scan(dest ...any) error }) (*models.KYCUploadAttempt, error) {
	var a models.KYCUploadAttempt
	if err := row.Scan(&a.ID, &a.UserID, &a.ObjectKey, &a.ContentType,
		&a.SizeBytes, &a.CreatedAt, &a.ConsumedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

// CreateAttempt records that a presigned upload URL was issued.
func (r *KYCRepo) CreateAttempt(ctx context.Context, a *models.KYCUploadAttempt) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO kyc_upload_attempts (id, user_id, object_key, content_type, size_bytes)
		VALUES ($1, $2, $3, $4, $5) RETURNING created_at`,
		a.ID, a.UserID, a.ObjectKey, a.ContentType, a.SizeBytes,
	).Scan(&a.CreatedAt)
	return mapErr(err)
}

// OpenAttempt returns this user's most recent unconsumed attempt, or ErrNotFound.
// It is what the account screen reads to offer "continue" rather than "start".
func (r *KYCRepo) OpenAttempt(ctx context.Context, userID uuid.UUID) (*models.KYCUploadAttempt, error) {
	a, err := scanAttempt(r.db.QueryRow(ctx,
		`SELECT `+attemptCols+` FROM kyc_upload_attempts
		 WHERE user_id = $1 AND consumed_at IS NULL
		 ORDER BY created_at DESC LIMIT 1`, userID))
	if err != nil {
		return nil, mapErr(err)
	}
	return a, nil
}

// GetAttempt loads one attempt by the id minted at presign.
func (r *KYCRepo) GetAttempt(ctx context.Context, id uuid.UUID) (*models.KYCUploadAttempt, error) {
	a, err := scanAttempt(r.db.QueryRow(ctx,
		`SELECT `+attemptCols+` FROM kyc_upload_attempts WHERE id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return a, nil
}

// ConsumeAttempt marks an attempt spent, guarded on it still being open.
//
// Guarded rather than unconditional, and reporting whether it won, for the same
// reason every settlement in this codebase is: two confirms racing for one
// attempt must not both produce a submission. The caller runs this inside the
// transaction that inserts the submission, so losing the guard means rolling that
// insert back.
func (r *KYCRepo) ConsumeAttempt(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := r.db.Exec(ctx,
		`UPDATE kyc_upload_attempts SET consumed_at = now()
		 WHERE id = $1 AND consumed_at IS NULL`, id)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}

// ListAbandonedAttempts returns unconsumed attempts older than the cutoff — the
// orphan sweep's input. Nothing here is a record of anything: an upload nobody
// finished is not evidence, so this is unrelated to document retention.
func (r *KYCRepo) ListAbandonedAttempts(ctx context.Context, before time.Time, limit int) ([]models.KYCUploadAttempt, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+attemptCols+` FROM kyc_upload_attempts
		 WHERE consumed_at IS NULL AND created_at < $1
		 ORDER BY created_at LIMIT $2`, before, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []models.KYCUploadAttempt{}
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// DeleteAttempt removes an attempt row once its object is gone.
func (r *KYCRepo) DeleteAttempt(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM kyc_upload_attempts WHERE id = $1`, id)
	return mapErr(err)
}

// ListDocumentsForRetention returns reviewed submissions still holding an object
// older than the cutoff. Its caller deletes the object and clears the key,
// keeping the decision.
func (r *KYCRepo) ListDocumentsForRetention(ctx context.Context, reviewedBefore time.Time, limit int) ([]models.KYCSubmission, error) {
	rows, err := r.db.Query(ctx, kycSelect+`
		WHERE k.document_key IS NOT NULL AND k.document_key <> ''
		  AND k.reviewed_at IS NOT NULL AND k.reviewed_at < $1
		ORDER BY k.reviewed_at LIMIT $2`, reviewedBefore, limit)
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

// ClearDocumentKey forgets the object without touching the decision, the
// reviewer, or the audit trail. Retention applies to the document, never to the
// record that a decision was made.
func (r *KYCRepo) ClearDocumentKey(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE kyc_submissions SET document_key = NULL WHERE id = $1`, id)
	return mapErr(err)
}
