package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type DigitContractRepo struct{ db DBTX }

const digitSelect = `
	SELECT c.id, c.account_id, c.symbol_id, c.contract_type, c.selection, c.stake,
	       c.payout_ratio, c.potential_payout, c.entry_price, c.entry_digit, c.entry_tick_epoch,
	       c.status, c.settlement_price, c.settlement_digit, c.settled_at, c.pnl,
	       c.opened_at, c.void_after,
	       s.code, s.display_name, s.pip_size
	FROM digit_contracts c JOIN symbols s ON s.id = c.symbol_id`

func scanDigit(row interface {
	Scan(dest ...any) error
}) (*models.DigitContract, error) {
	var c models.DigitContract
	var raw []byte
	err := row.Scan(
		&c.ID, &c.AccountID, &c.SymbolID, &c.ContractType, &raw, &c.Stake,
		&c.PayoutRatio, &c.PotentialPayout, &c.EntryPrice, &c.EntryDigit, &c.EntryTickEpoch,
		&c.Status, &c.SettlementPrice, &c.SettlementDigit, &c.SettledAt, &c.PnL,
		&c.OpenedAt, &c.VoidAfter,
		&c.SymbolCode, &c.SymbolName, &c.PipSize,
	)
	if err != nil {
		return nil, err
	}
	sel, err := models.ParseDigitSelection(raw)
	if err != nil {
		return nil, err
	}
	c.Selection = sel
	return &c, nil
}

func (r *DigitContractRepo) Create(ctx context.Context, c *models.DigitContract) error {
	raw, err := c.Selection.Marshal()
	if err != nil {
		return err
	}
	return mapErr(r.db.QueryRow(ctx, `
		INSERT INTO digit_contracts (
			account_id, symbol_id, contract_type, selection, stake, payout_ratio, potential_payout,
			entry_price, entry_digit, entry_tick_epoch, status, void_after
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id, status, opened_at`,
		c.AccountID, c.SymbolID, c.ContractType, raw, c.Stake, c.PayoutRatio, c.PotentialPayout,
		c.EntryPrice, c.EntryDigit, c.EntryTickEpoch, models.DigitOpen, c.VoidAfter,
	).Scan(&c.ID, &c.Status, &c.OpenedAt))
}

func (r *DigitContractRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.DigitContract, error) {
	c, err := scanDigit(r.db.QueryRow(ctx, digitSelect+` WHERE c.id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return c, nil
}

// GetOpenForUpdate locks an open row so tick + sweep cannot double-settle.
func (r *DigitContractRepo) GetOpenForUpdate(ctx context.Context, id uuid.UUID) (*models.DigitContract, error) {
	var c models.DigitContract
	var raw []byte
	err := r.db.QueryRow(ctx, `
		SELECT id, account_id, symbol_id, contract_type, selection, stake, payout_ratio, potential_payout,
		       entry_price, entry_digit, entry_tick_epoch, status, settlement_price, settlement_digit,
		       settled_at, pnl, opened_at, void_after
		FROM digit_contracts WHERE id = $1 AND status = 'open' FOR UPDATE`, id,
	).Scan(
		&c.ID, &c.AccountID, &c.SymbolID, &c.ContractType, &raw, &c.Stake, &c.PayoutRatio, &c.PotentialPayout,
		&c.EntryPrice, &c.EntryDigit, &c.EntryTickEpoch, &c.Status, &c.SettlementPrice, &c.SettlementDigit,
		&c.SettledAt, &c.PnL, &c.OpenedAt, &c.VoidAfter,
	)
	if err != nil {
		return nil, mapErr(err)
	}
	sel, err := models.ParseDigitSelection(raw)
	if err != nil {
		return nil, err
	}
	c.Selection = sel
	return &c, nil
}

func (r *DigitContractRepo) Settle(ctx context.Context, id uuid.UUID, status string, settlementPrice decimal.Decimal, settlementDigit int, pnl decimal.Decimal) error {
	_, err := r.db.Exec(ctx, `
		UPDATE digit_contracts
		SET status = $2, settlement_price = $3, settlement_digit = $4, pnl = $5, settled_at = $6
		WHERE id = $1 AND status = 'open'`,
		id, status, settlementPrice, settlementDigit, pnl, time.Now().UTC())
	return mapErr(err)
}

func (r *DigitContractRepo) Void(ctx context.Context, id uuid.UUID, pnl decimal.Decimal) error {
	_, err := r.db.Exec(ctx, `
		UPDATE digit_contracts
		SET status = $2, pnl = $3, settled_at = $4
		WHERE id = $1 AND status = 'open'`,
		id, models.DigitVoid, pnl, time.Now().UTC())
	return mapErr(err)
}

func (r *DigitContractRepo) ListByAccount(ctx context.Context, accountID uuid.UUID, status string, limit, offset int) ([]models.DigitContract, int, error) {
	var total int
	if err := r.db.QueryRow(ctx, `
		SELECT count(*) FROM digit_contracts
		WHERE account_id = $1 AND ($2 = '' OR status = $2)`, accountID, status,
	).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}

	order := ` ORDER BY c.opened_at DESC`
	if status != "" && status != models.DigitOpen {
		order = ` ORDER BY c.settled_at DESC NULLS LAST`
	}

	rows, err := r.db.Query(ctx,
		digitSelect+` WHERE c.account_id = $1 AND ($2 = '' OR c.status = $2)`+order+` LIMIT $3 OFFSET $4`,
		accountID, status, limit, offset)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()

	out := []models.DigitContract{}
	for rows.Next() {
		c, err := scanDigit(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *c)
	}
	return out, total, rows.Err()
}

// ListAllOpen feeds the tick settler and the sweep.
func (r *DigitContractRepo) ListAllOpen(ctx context.Context) ([]models.DigitContract, error) {
	rows, err := r.db.Query(ctx, digitSelect+` WHERE c.status = 'open'`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []models.DigitContract{}
	for rows.Next() {
		c, err := scanDigit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// ListOpenBySymbol is the hot path for onTick.
func (r *DigitContractRepo) ListOpenBySymbol(ctx context.Context, symbolID uuid.UUID) ([]models.DigitContract, error) {
	rows, err := r.db.Query(ctx, digitSelect+` WHERE c.status = 'open' AND c.symbol_id = $1`, symbolID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []models.DigitContract{}
	for rows.Next() {
		c, err := scanDigit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// Summary aggregates settled session stats for an account.
type DigitSummary struct {
	OpenCount     int             `json:"open_count"`
	WonCount      int             `json:"won_count"`
	LostCount     int             `json:"lost_count"`
	VoidCount     int             `json:"void_count"`
	SessionPnL    decimal.Decimal `json:"session_pnl"`
	SessionSince  time.Time       `json:"session_since"`
}

func (r *DigitContractRepo) Summary(ctx context.Context, accountID uuid.UUID, since time.Time) (DigitSummary, error) {
	var s DigitSummary
	s.SessionSince = since
	err := r.db.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE status = 'open'),
			count(*) FILTER (WHERE status = 'won' AND settled_at >= $2),
			count(*) FILTER (WHERE status = 'lost' AND settled_at >= $2),
			count(*) FILTER (WHERE status = 'void' AND settled_at >= $2),
			COALESCE(sum(pnl) FILTER (WHERE status IN ('won','lost','void') AND settled_at >= $2), 0)
		FROM digit_contracts WHERE account_id = $1`, accountID, since,
	).Scan(&s.OpenCount, &s.WonCount, &s.LostCount, &s.VoidCount, &s.SessionPnL)
	return s, mapErr(err)
}

// Ensure selection round-trips as JSON for callers that only have a map.
func DigitSelectionJSON(sel models.DigitSelection) json.RawMessage {
	b, _ := json.Marshal(sel)
	return b
}
