package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type AuditRepo struct{ db DBTX }

const auditSelect = `
	SELECT l.id, l.admin_id, l.action, l.target_type, l.target_id,
	       COALESCE(l.reason, ''), l.metadata, l.created_at, a.username
	FROM admin_audit_log l JOIN admin_users a ON a.id = l.admin_id`

func scanAudit(row interface{ Scan(dest ...any) error }) (*models.AuditEntry, error) {
	var e models.AuditEntry
	err := row.Scan(&e.ID, &e.AdminID, &e.Action, &e.TargetType, &e.TargetID,
		&e.Reason, &e.Metadata, &e.CreatedAt, &e.AdminUsername)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// Write appends an audit row. Every admin action calls this inside the same
// transaction as the change it describes, so an action can never commit
// without its audit trail.
func (r *AuditRepo) Write(ctx context.Context, e *models.AuditEntry) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO admin_audit_log (admin_id, action, target_type, target_id, reason, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at`,
		e.AdminID, e.Action, e.TargetType, e.TargetID, e.Reason, e.Metadata,
	).Scan(&e.ID, &e.CreatedAt)
	return mapErr(err)
}

func (r *AuditRepo) List(ctx context.Context, adminID *uuid.UUID, action string, limit, offset int) ([]models.AuditEntry, int, error) {
	const where = ` WHERE ($1::uuid IS NULL OR l.admin_id = $1) AND ($2 = '' OR l.action = $2)`

	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM admin_audit_log l`+
			` WHERE ($1::uuid IS NULL OR l.admin_id = $1) AND ($2 = '' OR l.action = $2)`,
		adminID, action).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}

	rows, err := r.db.Query(ctx,
		auditSelect+where+` ORDER BY l.created_at DESC LIMIT $3 OFFSET $4`,
		adminID, action, limit, offset)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()

	out := []models.AuditEntry{}
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *e)
	}
	return out, total, rows.Err()
}

// ListForUser returns every audit row touching a user or any of their
// accounts, for the user detail screen's trail.
func (r *AuditRepo) ListForUser(ctx context.Context, userID uuid.UUID, limit int) ([]models.AuditEntry, error) {
	rows, err := r.db.Query(ctx, auditSelect+`
		WHERE l.target_id = $1
		   OR l.target_id IN (SELECT id FROM accounts WHERE user_id = $1)
		   OR l.target_id IN (SELECT t.id FROM transactions t
		                      JOIN accounts ac ON ac.id = t.account_id WHERE ac.user_id = $1)
		ORDER BY l.created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []models.AuditEntry{}
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}
