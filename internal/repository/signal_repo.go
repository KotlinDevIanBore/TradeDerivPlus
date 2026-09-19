package repository

import (
	"context"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type SignalRepo struct{ db DBTX }

const signalSelect = `
	SELECT sg.id, sg.symbol_id, sg.direction, sg.confidence, sg.entry_price,
	       sg.stop_loss, sg.take_profit, sg.strategy, sg.created_at,
	       s.code, s.display_name, s.pip_size
	FROM signals sg JOIN symbols s ON s.id = sg.symbol_id`

func scanSignal(row interface {
	Scan(dest ...any) error
}) (*models.Signal, error) {
	var s models.Signal
	err := row.Scan(&s.ID, &s.SymbolID, &s.Direction, &s.Confidence, &s.EntryPrice,
		&s.StopLoss, &s.TakeProfit, &s.Strategy, &s.CreatedAt,
		&s.SymbolCode, &s.SymbolName, &s.PipSize)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SignalRepo) Insert(ctx context.Context, s *models.Signal) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO signals (symbol_id, direction, confidence, entry_price, stop_loss, take_profit, strategy)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`,
		s.SymbolID, s.Direction, s.Confidence, s.EntryPrice, s.StopLoss, s.TakeProfit, s.Strategy,
	).Scan(&s.ID, &s.CreatedAt)
	return mapErr(err)
}

// List returns newest signals. An empty symbolCode means "across all symbols",
// which is what the Home screen's single signal card asks for (spec gap G3).
func (r *SignalRepo) List(ctx context.Context, symbolCode string, limit int) ([]models.Signal, error) {
	rows, err := r.db.Query(ctx,
		signalSelect+` WHERE ($1 = '' OR s.code = $1) ORDER BY sg.created_at DESC LIMIT $2`,
		symbolCode, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []models.Signal{}
	for rows.Next() {
		s, err := scanSignal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// LatestForSymbol is used by the signal bot to avoid emitting duplicate
// same-direction signals within the cooldown window.
func (r *SignalRepo) LatestForSymbol(ctx context.Context, symbolCode string) (*models.Signal, error) {
	s, err := scanSignal(r.db.QueryRow(ctx,
		signalSelect+` WHERE s.code = $1 ORDER BY sg.created_at DESC LIMIT 1`, symbolCode))
	if err != nil {
		return nil, mapErr(err)
	}
	return s, nil
}
