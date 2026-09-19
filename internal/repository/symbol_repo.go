package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type SymbolRepo struct{ db DBTX }

const symbolCols = `id, code, display_name, category, pip_size, is_active`

func (r *SymbolRepo) List(ctx context.Context, activeOnly bool) ([]models.Symbol, error) {
	sql := `SELECT ` + symbolCols + ` FROM symbols`
	if activeOnly {
		sql += ` WHERE is_active = true`
	}
	sql += ` ORDER BY category, code`

	rows, err := r.db.Query(ctx, sql)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []models.Symbol{}
	for rows.Next() {
		var s models.Symbol
		if err := rows.Scan(&s.ID, &s.Code, &s.DisplayName, &s.Category, &s.PipSize, &s.IsActive); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *SymbolRepo) GetByCode(ctx context.Context, code string) (*models.Symbol, error) {
	var s models.Symbol
	err := r.db.QueryRow(ctx, `SELECT `+symbolCols+` FROM symbols WHERE code = $1`, code).
		Scan(&s.ID, &s.Code, &s.DisplayName, &s.Category, &s.PipSize, &s.IsActive)
	if err != nil {
		return nil, mapErr(err)
	}
	return &s, nil
}

func (r *SymbolRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.Symbol, error) {
	var s models.Symbol
	err := r.db.QueryRow(ctx, `SELECT `+symbolCols+` FROM symbols WHERE id = $1`, id).
		Scan(&s.ID, &s.Code, &s.DisplayName, &s.Category, &s.PipSize, &s.IsActive)
	if err != nil {
		return nil, mapErr(err)
	}
	return &s, nil
}
