package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type PayoutRepo struct{ db DBTX }

// One column list, one scanner, so a column added to mpesa_payouts reaches every
// reader at once — the lesson mpesa_requests learned in STK CP2.
const payoutSelect = `
	SELECT id, transaction_id, originator_conversation_id, conversation_id,
	       phone_number, amount, status, requested_at, result_code, result_desc,
	       receipt, settled_by, reconcile_attempts, next_query_at, created_at, updated_at
	FROM mpesa_payouts`

func scanPayout(row interface{ Scan(dest ...any) error }) (*models.MpesaPayout, error) {
	var p models.MpesaPayout
	err := row.Scan(&p.ID, &p.TransactionID, &p.OriginatorConversationID, &p.ConversationID,
		&p.PhoneNumber, &p.Amount, &p.Status, &p.RequestedAt, &p.ResultCode, &p.ResultDesc,
		&p.Receipt, &p.SettledBy, &p.ReconcileAttempts, &p.NextQueryAt, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &p, nil
}

// Insert reserves a payout. Called in the same transaction as the debit and the
// ledger row: a debit whose payout row is missing has lost the destination
// number, and the customer's money with it.
func (r *PayoutRepo) Insert(ctx context.Context, p *models.MpesaPayout) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO mpesa_payouts
			(transaction_id, originator_conversation_id, phone_number, amount, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at`,
		p.TransactionID, p.OriginatorConversationID, p.PhoneNumber, p.Amount, models.PayoutPending,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return mapErr(err)
	}
	p.Status = models.PayoutPending
	return nil
}

func (r *PayoutRepo) GetByOriginatorID(ctx context.Context, originatorID string) (*models.MpesaPayout, error) {
	return scanPayout(r.db.QueryRow(ctx, payoutSelect+` WHERE originator_conversation_id = $1`, originatorID))
}

// GetByConversationID is the fallback lookup for a callback that carries only
// Safaricom's id. Every callback observed so far carries ours too, but a result
// that cannot be matched is a payout nobody can account for, so both work.
func (r *PayoutRepo) GetByConversationID(ctx context.Context, conversationID string) (*models.MpesaPayout, error) {
	return scanPayout(r.db.QueryRow(ctx, payoutSelect+` WHERE conversation_id = $1`, conversationID))
}

func (r *PayoutRepo) GetByTransactionID(ctx context.Context, txID uuid.UUID) (*models.MpesaPayout, error) {
	return scanPayout(r.db.QueryRow(ctx, payoutSelect+` WHERE transaction_id = $1`, txID))
}

// MarkRequested stamps requested_at and reports whether this call was the one
// that stamped it.
//
// This is the guard that makes exactly one send attempt per payout, and it is
// taken BEFORE the request leaves rather than after it returns. A caller that
// loses here must not send: something else already has, and a second request for
// one withdrawal is a second real payout that no API can reverse.
func (r *PayoutRepo) MarkRequested(ctx context.Context, originatorID string, at time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE mpesa_payouts SET requested_at = $2, updated_at = now()
		WHERE originator_conversation_id = $1 AND requested_at IS NULL AND status = $3`,
		originatorID, at, models.PayoutPending)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkSent records that Daraja accepted the request. Guarded on 'pending' so a
// callback that has already settled the row — Daraja can be quicker than our own
// next statement — is not walked backwards into 'sent'.
func (r *PayoutRepo) MarkSent(ctx context.Context, originatorID, conversationID string, nextQueryAt time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE mpesa_payouts
		SET status = $2, conversation_id = $3, next_query_at = $4, updated_at = now()
		WHERE originator_conversation_id = $1 AND status = $5`,
		originatorID, models.PayoutSent, conversationID, nextQueryAt, models.PayoutPending)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkUnknown records that a payout's outcome is not known: an ambiguous send, a
// queue timeout, or a result code this codebase will not call terminal.
//
// Nothing about the customer's money follows from this state. It exists so that
// the fact of the ambiguity is durable — an operator can see it, and the
// reconciler has something to ask about — rather than living only in a log line.
//
// Reachable from every OPEN state, not just 'pending'. The first version guarded
// on 'pending' alone, which quietly did nothing for the commonest case by far: a
// queue timeout arrives for a payout Daraja has already accepted, so the row is
// 'sent', and the ambiguity was logged but never recorded. It is guarded against
// the terminal states for the opposite reason — a timeout arriving after a payout
// has been settled must not reopen it.
func (r *PayoutRepo) MarkUnknown(ctx context.Context, originatorID, reason string, nextQueryAt time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE mpesa_payouts
		SET status = $2, result_desc = $3, next_query_at = $4, updated_at = now()
		WHERE originator_conversation_id = $1
		  AND status IN ('pending', 'sent', 'unknown')`,
		originatorID, models.PayoutUnknown, reason, nextQueryAt)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() > 0, nil
}

// Settle writes a payout's terminal outcome and reports whether this call was
// the one that wrote it.
//
// The guard is the whole point, as it is on the deposit side: a result callback
// and its retry, a timeout followed by a result, or a result racing an operator
// can all arrive at once, and exactly one of them may move money. status is one
// of completed, failed or never_received.
func (r *PayoutRepo) Settle(ctx context.Context, originatorID, status, resultCode, resultDesc, receipt, settledBy string) (bool, error) {
	var code, desc, rcpt *string
	if resultCode != "" {
		code = &resultCode
	}
	if resultDesc != "" {
		desc = &resultDesc
	}
	if receipt != "" {
		rcpt = &receipt
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE mpesa_payouts
		SET status = $2, result_code = $3, result_desc = COALESCE($4, result_desc),
		    receipt = $5, settled_by = $6, next_query_at = NULL, updated_at = now()
		WHERE originator_conversation_id = $1
		  AND status IN ('pending', 'sent', 'unknown')`,
		originatorID, status, code, desc, rcpt, settledBy)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() > 0, nil
}

// ListOpen is what an operator's view and the future reconciliation pass read:
// payouts whose outcome is undecided, oldest first.
func (r *PayoutRepo) ListOpen(ctx context.Context, limit int) ([]models.MpesaPayout, error) {
	rows, err := r.db.Query(ctx, payoutSelect+`
		WHERE status IN ('pending', 'sent', 'unknown')
		ORDER BY created_at ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []models.MpesaPayout{}
	for rows.Next() {
		p, err := scanPayout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// CountByStatus feeds the gauges. 'unknown' rising is the one an operator owes
// attention to: each is a customer debited with no answer either way.
func (r *PayoutRepo) CountByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := r.db.Query(ctx, `SELECT status, count(*) FROM mpesa_payouts GROUP BY status`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}
