package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type TransactionRepo struct{ db DBTX }

const txCols = `id, account_id, type, balance_type, amount, status, reference, metadata, created_at`

// Insert appends to the ledger. The ledger is never updated except for the
// pending -> completed/failed status transition on M-Pesa deposits.
func (r *TransactionRepo) Insert(ctx context.Context, t *models.Transaction) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO transactions (account_id, type, balance_type, amount, status, reference, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`,
		t.AccountID, t.Type, t.BalanceType, t.Amount, t.Status, t.Reference, t.Metadata,
	).Scan(&t.ID, &t.CreatedAt)
	return mapErr(err)
}

// UpdateStatusIfPending moves a transaction off 'pending' and reports whether
// this call was the one that moved it.
//
// For money that arrives asynchronously, the guard is the difference between
// crediting once and crediting per delivery: an M-Pesa callback can be retried,
// and a caller that checked the status in a separate statement can lose the race
// between the check and the write. Here the check and the write are the same
// statement, so exactly one caller sees true.
func (r *TransactionRepo) UpdateStatusIfPending(ctx context.Context, id uuid.UUID, status string, metadata map[string]any) (bool, error) {
	tag, err := r.db.Exec(ctx,
		`UPDATE transactions SET status = $2, metadata = COALESCE(metadata,'{}'::jsonb) || $3::jsonb
		 WHERE id = $1 AND status = 'pending'`,
		id, status, metadata)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *TransactionRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string, metadata map[string]any) error {
	_, err := r.db.Exec(ctx,
		`UPDATE transactions SET status = $2, metadata = COALESCE(metadata,'{}'::jsonb) || $3::jsonb WHERE id = $1`,
		id, status, metadata)
	return mapErr(err)
}

func (r *TransactionRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error) {
	var t models.Transaction
	err := r.db.QueryRow(ctx, `SELECT `+txCols+` FROM transactions WHERE id = $1`, id).
		Scan(&t.ID, &t.AccountID, &t.Type, &t.BalanceType, &t.Amount, &t.Status, &t.Reference, &t.Metadata, &t.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &t, nil
}

// ListByAccount returns one page of the ledger, newest first. types may be nil
// (all) or a filter set such as {"deposit"} for the Wallet screen's segments.
func (r *TransactionRepo) ListByAccount(ctx context.Context, accountID uuid.UUID, types []string, limit, offset int) ([]models.Transaction, int, error) {
	var total int
	countSQL := `SELECT count(*) FROM transactions WHERE account_id = $1 AND ($2::text[] IS NULL OR type = ANY($2))`
	if err := r.db.QueryRow(ctx, countSQL, accountID, types).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}

	rows, err := r.db.Query(ctx, `
		SELECT `+txCols+` FROM transactions
		WHERE account_id = $1 AND ($2::text[] IS NULL OR type = ANY($2))
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4`, accountID, types, limit, offset)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()

	out := []models.Transaction{}
	for rows.Next() {
		var t models.Transaction
		if err := rows.Scan(&t.ID, &t.AccountID, &t.Type, &t.BalanceType, &t.Amount, &t.Status,
			&t.Reference, &t.Metadata, &t.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// QueryFundingBalance finds which balance type paid for a trade's open, so the
// close can credit the same one.
func (r *TransactionRepo) QueryFundingBalance(ctx context.Context, reference string, out *string) error {
	return mapErr(r.db.QueryRow(ctx,
		`SELECT balance_type FROM transactions WHERE reference = $1 AND type = 'trade_open' LIMIT 1`,
		reference).Scan(out))
}

// QueryDigitFundingBalance is the digit-contract equivalent of QueryFundingBalance.
func (r *TransactionRepo) QueryDigitFundingBalance(ctx context.Context, reference string, out *string) error {
	return mapErr(r.db.QueryRow(ctx,
		`SELECT balance_type FROM transactions WHERE reference = $1 AND type = 'digit_open' LIMIT 1`,
		reference).Scan(out))
}

// AdminTransaction is a ledger row with the owning account and user attached,
// so the admin table renders without a join client-side.
type AdminTransaction struct {
	models.Transaction
	AccountNumber string  `json:"account_number"`
	AccountType   string  `json:"account_type"`
	UserID        string  `json:"user_id"`
	UserPhone     string  `json:"user_phone"`
	AdminUsername *string `json:"admin_username"`
}

// SearchAll backs the admin transaction list: cross-account, filterable, and
// searchable by reference, receipt, phone, account number or id.
func (r *TransactionRepo) SearchAll(ctx context.Context, query, txType, status string, limit, offset int) ([]AdminTransaction, int, error) {
	const from = `
		FROM transactions t
		JOIN accounts a ON a.id = t.account_id
		JOIN users u ON u.id = a.user_id
		LEFT JOIN admin_users ad ON ad.id = (t.metadata->>'admin_id')::uuid`

	const where = `
		WHERE ($2 = '' OR t.type = $2)
		  AND ($3 = '' OR t.status = $3)
		  AND ($1 = ''
		       OR t.reference ILIKE '%' || $1 || '%'
		       OR t.metadata->>'receipt' ILIKE '%' || $1 || '%'
		       OR u.phone_number ILIKE '%' || $1 || '%'
		       OR a.account_number ILIKE '%' || $1 || '%'
		       OR t.id::text = $1)`

	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*)`+from+where, query, txType, status).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}

	rows, err := r.db.Query(ctx, `
		SELECT t.id, t.account_id, t.type, t.balance_type, t.amount, t.status,
		       t.reference, t.metadata, t.created_at,
		       a.account_number, a.account_type, u.id, u.phone_number, ad.username`+
		from+where+` ORDER BY t.created_at DESC LIMIT $4 OFFSET $5`,
		query, txType, status, limit, offset)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()

	out := []AdminTransaction{}
	for rows.Next() {
		var t AdminTransaction
		if err := rows.Scan(&t.ID, &t.AccountID, &t.Type, &t.BalanceType, &t.Amount, &t.Status,
			&t.Reference, &t.Metadata, &t.CreatedAt,
			&t.AccountNumber, &t.AccountType, &t.UserID, &t.UserPhone, &t.AdminUsername); err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// TodayTotals powers two dashboard tiles in one query per type.
func (r *TransactionRepo) TodayTotals(ctx context.Context, txType string) (count int, total decimal.Decimal, err error) {
	var sum decimal.NullDecimal
	err = r.db.QueryRow(ctx, `
		SELECT count(*), sum(abs(amount))
		FROM transactions
		WHERE type = $1 AND status = 'completed' AND created_at >= date_trunc('day', now())`,
		txType).Scan(&count, &sum)
	if err != nil {
		return 0, decimal.Zero, mapErr(err)
	}
	if sum.Valid {
		total = sum.Decimal
	}
	return count, total, nil
}

// DepositsByHour is the dashboard's 24-hour success-versus-failure series. A
// Daraja outage shows up here before a user complains.
type HourlyDeposits struct {
	Hour      time.Time `json:"hour"`
	Completed int       `json:"completed"`
	Failed    int       `json:"failed"`
}

func (r *TransactionRepo) DepositsByHour(ctx context.Context) ([]HourlyDeposits, error) {
	rows, err := r.db.Query(ctx, `
		WITH hours AS (
			SELECT generate_series(date_trunc('hour', now()) - interval '23 hours',
			                       date_trunc('hour', now()), interval '1 hour') AS hour
		)
		SELECT h.hour,
		       count(*) FILTER (WHERE t.status = 'completed'),
		       count(*) FILTER (WHERE t.status = 'failed')
		FROM hours h
		LEFT JOIN transactions t
		  ON t.type = 'deposit' AND date_trunc('hour', t.created_at) = h.hour
		GROUP BY h.hour ORDER BY h.hour`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []HourlyDeposits{}
	for rows.Next() {
		var h HourlyDeposits
		if err := rows.Scan(&h.Hour, &h.Completed, &h.Failed); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
