package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type TradeRepo struct{ db DBTX }

// Every read joins symbols so the API can hand the UI a ready-to-render trade.
const tradeSelect = `
	SELECT t.id, t.account_id, t.symbol_id, t.direction, t.entry_price, t.exit_price,
	       t.stake, t.stop_loss, t.take_profit, t.status, t.pnl, t.opened_at, t.closed_at,
	       s.code, s.display_name, s.pip_size
	FROM trades t JOIN symbols s ON s.id = t.symbol_id`

func scanTrade(row interface {
	Scan(dest ...any) error
}) (*models.Trade, error) {
	var t models.Trade
	err := row.Scan(&t.ID, &t.AccountID, &t.SymbolID, &t.Direction, &t.EntryPrice, &t.ExitPrice,
		&t.Stake, &t.StopLoss, &t.TakeProfit, &t.Status, &t.PnL, &t.OpenedAt, &t.ClosedAt,
		&t.SymbolCode, &t.SymbolName, &t.PipSize)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *TradeRepo) Create(ctx context.Context, t *models.Trade) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO trades (account_id, symbol_id, direction, entry_price, stake, stop_loss, take_profit)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, status, opened_at`,
		t.AccountID, t.SymbolID, t.Direction, t.EntryPrice, t.Stake, t.StopLoss, t.TakeProfit,
	).Scan(&t.ID, &t.Status, &t.OpenedAt)
	return mapErr(err)
}

func (r *TradeRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.Trade, error) {
	t, err := scanTrade(r.db.QueryRow(ctx, tradeSelect+` WHERE t.id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return t, nil
}

// GetOpenForUpdate locks the row so two concurrent close requests (user tap and
// the SL/TP watcher) cannot both settle the same trade.
func (r *TradeRepo) GetOpenForUpdate(ctx context.Context, id uuid.UUID) (*models.Trade, error) {
	var t models.Trade
	err := r.db.QueryRow(ctx, `
		SELECT id, account_id, symbol_id, direction, entry_price, exit_price, stake,
		       stop_loss, take_profit, status, pnl, opened_at, closed_at
		FROM trades WHERE id = $1 AND status = 'open' FOR UPDATE`, id,
	).Scan(&t.ID, &t.AccountID, &t.SymbolID, &t.Direction, &t.EntryPrice, &t.ExitPrice, &t.Stake,
		&t.StopLoss, &t.TakeProfit, &t.Status, &t.PnL, &t.OpenedAt, &t.ClosedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &t, nil
}

func (r *TradeRepo) Close(ctx context.Context, id uuid.UUID, exit, pnl decimal.Decimal) error {
	_, err := r.db.Exec(ctx, `
		UPDATE trades SET status = 'closed', exit_price = $2, pnl = $3, closed_at = now()
		WHERE id = $1 AND status = 'open'`, id, exit, pnl)
	return mapErr(err)
}

func (r *TradeRepo) ListByAccount(ctx context.Context, accountID uuid.UUID, status string, limit, offset int) ([]models.Trade, int, error) {
	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM trades WHERE account_id = $1 AND ($2 = '' OR status = $2)`,
		accountID, status).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}

	// Open positions read best oldest-first is wrong; the UI wants newest first
	// in both tabs, ordered by the timestamp that matters for that tab.
	order := ` ORDER BY t.opened_at DESC`
	if status == models.TradeClosed {
		order = ` ORDER BY t.closed_at DESC`
	}

	rows, err := r.db.Query(ctx,
		tradeSelect+` WHERE t.account_id = $1 AND ($2 = '' OR t.status = $2)`+order+` LIMIT $3 OFFSET $4`,
		accountID, status, limit, offset)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()

	out := []models.Trade{}
	for rows.Next() {
		t, err := scanTrade(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *t)
	}
	return out, total, rows.Err()
}

// ListAllOpen feeds the SL/TP watcher: every open position across all accounts.
func (r *TradeRepo) ListAllOpen(ctx context.Context) ([]models.Trade, error) {
	rows, err := r.db.Query(ctx, tradeSelect+` WHERE t.status = 'open'`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []models.Trade{}
	for rows.Next() {
		t, err := scanTrade(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// RealisedToday powers the Portfolio summary bar's second tile.
func (r *TradeRepo) RealisedSince(ctx context.Context, accountID uuid.UUID, sinceHours int) (decimal.Decimal, error) {
	var sum decimal.NullDecimal
	err := r.db.QueryRow(ctx, `
		SELECT sum(pnl) FROM trades
		WHERE account_id = $1 AND status = 'closed' AND closed_at > now() - make_interval(hours => $2)`,
		accountID, sinceHours).Scan(&sum)
	if err != nil {
		return decimal.Zero, mapErr(err)
	}
	if !sum.Valid {
		return decimal.Zero, nil
	}
	return sum.Decimal, nil
}

// CountOpen is a dashboard tile: open positions across the whole platform.
func (r *TradeRepo) CountOpen(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM trades WHERE status = 'open'`).Scan(&n)
	return n, mapErr(err)
}
