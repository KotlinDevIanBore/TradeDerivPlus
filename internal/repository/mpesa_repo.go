package repository

import (
	"context"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type MpesaRepo struct{ db DBTX }

func (r *MpesaRepo) Insert(ctx context.Context, m *models.MpesaRequest) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO mpesa_requests (transaction_id, checkout_request_id, merchant_request_id, phone_number, amount, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at`,
		m.TransactionID, m.CheckoutRequestID, m.MerchantRequestID, m.PhoneNumber, m.Amount, m.Status,
	).Scan(&m.ID, &m.CreatedAt)
	return mapErr(err)
}

func (r *MpesaRepo) GetByCheckoutID(ctx context.Context, checkoutID string) (*models.MpesaRequest, error) {
	var m models.MpesaRequest
	err := r.db.QueryRow(ctx, `
		SELECT id, transaction_id, checkout_request_id, merchant_request_id, phone_number,
		       amount, status, result_code, result_desc, created_at
		FROM mpesa_requests WHERE checkout_request_id = $1`, checkoutID,
	).Scan(&m.ID, &m.TransactionID, &m.CheckoutRequestID, &m.MerchantRequestID, &m.PhoneNumber,
		&m.Amount, &m.Status, &m.ResultCode, &m.ResultDesc, &m.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &m, nil
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
