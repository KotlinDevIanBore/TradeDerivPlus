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

func (r *MpesaRepo) UpdateResult(ctx context.Context, checkoutID, status string, resultCode int, resultDesc string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE mpesa_requests SET status = $2, result_code = $3, result_desc = $4
		WHERE checkout_request_id = $1`, checkoutID, status, resultCode, resultDesc)
	return mapErr(err)
}
