package repository

import (
	"context"
	"time"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type MpesaRepo struct{ db DBTX }

// mpesaSelect is the one column list every read of mpesa_requests uses, so a
// column added to the table is added to every reader at once.
const mpesaSelect = `
	SELECT id, transaction_id, checkout_request_id, merchant_request_id, phone_number,
	       amount, status, result_code, result_desc, created_at,
	       reconcile_attempts, next_query_at
	FROM mpesa_requests`

func scanMpesa(row interface{ Scan(dest ...any) error }) (*models.MpesaRequest, error) {
	var m models.MpesaRequest
	err := row.Scan(&m.ID, &m.TransactionID, &m.CheckoutRequestID, &m.MerchantRequestID, &m.PhoneNumber,
		&m.Amount, &m.Status, &m.ResultCode, &m.ResultDesc, &m.CreatedAt,
		&m.ReconcileAttempts, &m.NextQueryAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &m, nil
}

// Insert records a push Daraja accepted. NextQueryAt is written as given: the
// service sets it to now + the reconciliation min-age, so the first query never
// lands before the callback has had its chance.
func (r *MpesaRepo) Insert(ctx context.Context, m *models.MpesaRequest) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO mpesa_requests (transaction_id, checkout_request_id, merchant_request_id, phone_number, amount, status, next_query_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`,
		m.TransactionID, m.CheckoutRequestID, m.MerchantRequestID, m.PhoneNumber, m.Amount, m.Status, m.NextQueryAt,
	).Scan(&m.ID, &m.CreatedAt)
	return mapErr(err)
}

func (r *MpesaRepo) GetByCheckoutID(ctx context.Context, checkoutID string) (*models.MpesaRequest, error) {
	return scanMpesa(r.db.QueryRow(ctx, mpesaSelect+` WHERE checkout_request_id = $1`, checkoutID))
}

// UpdateResult records a callback's outcome, and reports whether this call was
// the one that recorded it.
//
// The status guard is what makes that answer trustworthy, and it is why this
// returns a boolean rather than just an error: Daraja retries callbacks, so two
// deliveries for one push can arrive at once, and a caller that read 'pending'
// before either wrote would otherwise settle the same deposit twice. The
// database decides the winner; the loser gets false and credits nothing.
func (r *MpesaRepo) UpdateResult(ctx context.Context, checkoutID, status string, resultCode int, resultDesc string) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE mpesa_requests SET status = $2, result_code = $3, result_desc = $4
		WHERE checkout_request_id = $1 AND status = 'pending'`,
		checkoutID, status, resultCode, resultDesc)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() > 0, nil
}

// ListPending is what the reconciliation pass works from: pending rows whose
// next_query_at has come, oldest due first, at most limit of them so one pass
// is bounded. A pending row with no next_query_at is never listed — it was
// never scheduled — but CountPendingOlderThan still sees it.
func (r *MpesaRepo) ListPending(ctx context.Context, now time.Time, limit int) ([]models.MpesaRequest, error) {
	rows, err := r.db.Query(ctx, mpesaSelect+`
		WHERE status = 'pending' AND next_query_at <= $1
		ORDER BY next_query_at ASC
		LIMIT $2`, now, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []models.MpesaRequest{}
	for rows.Next() {
		m, err := scanMpesa(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// MarkQueried records that Daraja answered without resolving the deposit, and
// when to ask again.
//
// Guarded on 'pending' for the same reason UpdateResult is: a callback can
// settle the row between the pass reading it and the pass writing to it, and
// this write must not touch a row that is no longer the reconciler's. It is a
// no-op then, not an error — the row got what it was waiting for.
func (r *MpesaRepo) MarkQueried(ctx context.Context, checkoutID string, attempts int, next time.Time) error {
	_, err := r.db.Exec(ctx, `
		UPDATE mpesa_requests SET reconcile_attempts = $2, next_query_at = $3
		WHERE checkout_request_id = $1 AND status = 'pending'`,
		checkoutID, attempts, next)
	return mapErr(err)
}

// MarkGivenUp takes a pending row off the pass's schedule for good: the
// callback never came, the queries never resolved it, and after the give-up
// horizon it is a person's to settle. next_query_at goes NULL, so ListPending
// never lists it again and it is not re-counted every pass; created_at is
// untouched, so CountPendingOlderThan still shows it to the operator. Guarded on
// 'pending' like every other reconciler write.
func (r *MpesaRepo) MarkGivenUp(ctx context.Context, checkoutID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE mpesa_requests SET next_query_at = NULL
		WHERE checkout_request_id = $1 AND status = 'pending'`, checkoutID)
	return mapErr(err)
}

// CountPendingOlderThan feeds the stale-deposit gauge. It keys on created_at,
// not next_query_at, so a row the pass cannot see still counts.
func (r *MpesaRepo) CountPendingOlderThan(ctx context.Context, age time.Duration) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT count(*) FROM mpesa_requests
		WHERE status = 'pending' AND created_at < $1`,
		time.Now().Add(-age)).Scan(&n)
	if err != nil {
		return 0, mapErr(err)
	}
	return n, nil
}
