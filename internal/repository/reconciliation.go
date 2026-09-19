package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// balanceAffectingRows is the single definition of which ledger rows have
// actually moved money, shared by the reconciliation check, the admin
// dashboard and the Prometheus drift gauge. Defining it once is the point:
// three copies of this predicate would drift apart and the alert built on it
// would cry wolf until someone muted it.
//
// The asymmetry between deposits and withdrawals is deliberate, not an
// oversight:
//
//   - A pending DEPOSIT has not moved money. The balance is credited only when
//     Daraja confirms, so a pending deposit must not count.
//   - A pending WITHDRAWAL has moved money. The balance is debited the moment
//     the payout is requested, so the same funds cannot be spent twice while it
//     is in flight. A pending withdrawal must count.
//
// A failed row never counts: a failed deposit was never credited, and a failed
// withdrawal is refunded by a compensating row rather than by deleting this one.
const balanceAffectingRows = `(
	status = 'completed'
	OR (status = 'pending' AND type = 'withdrawal')
)`

// AccountDrift is one account whose ledger and cached balance disagree.
type AccountDrift struct {
	AccountID     uuid.UUID       `json:"account_id"`
	AccountNumber string          `json:"account_number"`
	LedgerTotal   decimal.Decimal `json:"ledger_total"`
	CachedTotal   decimal.Decimal `json:"cached_total"`
	Difference    decimal.Decimal `json:"difference"`
}

// LedgerTotal reconstructs one account's balance from the append-only ledger.
func (r *TransactionRepo) LedgerTotal(ctx context.Context, accountID uuid.UUID) (decimal.Decimal, error) {
	var sum decimal.NullDecimal
	err := r.db.QueryRow(ctx,
		`SELECT sum(amount) FROM transactions WHERE account_id = $1 AND `+balanceAffectingRows,
		accountID).Scan(&sum)
	if err != nil {
		return decimal.Zero, mapErr(err)
	}
	if !sum.Valid {
		return decimal.Zero, nil
	}
	return sum.Decimal, nil
}

// Drift returns every account whose ledger sum disagrees with its wallet cache.
// In a healthy system this is always empty; anything else means a balance
// changed outside a transaction that also wrote the ledger, which is the one
// class of bug that silently destroys money.
func (r *TransactionRepo) Drift(ctx context.Context, limit int) ([]AccountDrift, int, error) {
	const driftQuery = `
		WITH ledger AS (
			SELECT account_id, sum(amount) AS total
			FROM transactions
			WHERE ` + balanceAffectingRows + `
			GROUP BY account_id
		)
		SELECT a.id, a.account_number,
		       COALESCE(l.total, 0) AS ledger_total,
		       w.real_balance + w.bonus_balance AS cached_total
		FROM accounts a
		JOIN wallets w ON w.account_id = a.id
		LEFT JOIN ledger l ON l.account_id = a.id
		WHERE COALESCE(l.total, 0) <> (w.real_balance + w.bonus_balance)`

	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM (`+driftQuery+`) d`).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}

	rows, err := r.db.Query(ctx, driftQuery+` ORDER BY a.account_number LIMIT $1`, limit)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()

	out := []AccountDrift{}
	for rows.Next() {
		var d AccountDrift
		if err := rows.Scan(&d.AccountID, &d.AccountNumber, &d.LedgerTotal, &d.CachedTotal); err != nil {
			return nil, 0, err
		}
		d.Difference = d.LedgerTotal.Sub(d.CachedTotal)
		out = append(out, d)
	}
	return out, total, rows.Err()
}
