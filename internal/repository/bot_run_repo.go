package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

type BotRunRepo struct{ db DBTX }

const botRunSelect = `
	SELECT r.id, r.account_id, r.symbol_id, r.status, r.stop_reason, r.last_error,
	       r.contract_type, r.selection, r.stake, r.interval_ms, r.limits,
	       r.starting_balance, r.contracts_placed, r.wins, r.losses, r.voids,
	       r.consecutive_losses, r.consecutive_voids, r.realised_pnl,
	       r.started_at, r.stopped_at, r.updated_at,
	       s.code, s.display_name
	FROM bot_runs r JOIN symbols s ON s.id = r.symbol_id`

// liveStatuses is the definition of "live" in one place, because it appears in
// the partial unique index, the restart statement, the kill switch and the
// capacity count — and a disagreement between any two of those is a bug that
// only shows up under load.
const liveStatuses = `('running', 'stopping')`

func scanBotRun(row interface{ Scan(dest ...any) error }) (*models.BotRun, error) {
	var r models.BotRun
	var selRaw, limitsRaw []byte
	err := row.Scan(
		&r.ID, &r.AccountID, &r.SymbolID, &r.Status, &r.StopReason, &r.LastError,
		&r.ContractType, &selRaw, &r.Stake, &r.IntervalMS, &limitsRaw,
		&r.StartingBalance, &r.ContractsPlaced, &r.Wins, &r.Losses, &r.Voids,
		&r.ConsecutiveLosses, &r.ConsecutiveVoids, &r.RealisedPnL,
		&r.StartedAt, &r.StoppedAt, &r.UpdatedAt,
		&r.SymbolCode, &r.SymbolName,
	)
	if err != nil {
		return nil, err
	}
	sel, err := models.ParseDigitSelection(selRaw)
	if err != nil {
		return nil, err
	}
	r.Selection = sel
	if err := json.Unmarshal(limitsRaw, &r.Limits); err != nil {
		return nil, err
	}
	return &r, nil
}

// Create inserts a run. The partial unique index on a live status means a second
// live run on the same account fails here rather than being prevented by a check
// the caller could race, so ErrConflict is the expected answer and not a defect.
func (r *BotRunRepo) Create(ctx context.Context, run *models.BotRun) error {
	selRaw, err := run.Selection.Marshal()
	if err != nil {
		return err
	}
	limitsRaw, err := json.Marshal(run.Limits)
	if err != nil {
		return err
	}
	return mapErr(r.db.QueryRow(ctx, `
		INSERT INTO bot_runs (
			account_id, symbol_id, status, contract_type, selection, stake, interval_ms,
			limits, starting_balance
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, status, started_at, updated_at`,
		run.AccountID, run.SymbolID, models.BotRunning, run.ContractType, selRaw,
		run.Stake, run.IntervalMS, limitsRaw, run.StartingBalance,
	).Scan(&run.ID, &run.Status, &run.StartedAt, &run.UpdatedAt))
}

func (r *BotRunRepo) Get(ctx context.Context, id uuid.UUID) (*models.BotRun, error) {
	run, err := scanBotRun(r.db.QueryRow(ctx, botRunSelect+` WHERE r.id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	return run, nil
}

// GetForUpdate locks a run row for a status transition, so a user stop and an
// admin kill landing together cannot both decide they are the one that stopped it.
func (r *BotRunRepo) GetForUpdate(ctx context.Context, id uuid.UUID) (*models.BotRun, error) {
	var run models.BotRun
	var selRaw, limitsRaw []byte
	err := r.db.QueryRow(ctx, `
		SELECT id, account_id, symbol_id, status, stop_reason, last_error,
		       contract_type, selection, stake, interval_ms, limits,
		       starting_balance, contracts_placed, wins, losses, voids,
		       consecutive_losses, consecutive_voids, realised_pnl,
		       started_at, stopped_at, updated_at
		FROM bot_runs WHERE id = $1 FOR UPDATE`, id,
	).Scan(
		&run.ID, &run.AccountID, &run.SymbolID, &run.Status, &run.StopReason, &run.LastError,
		&run.ContractType, &selRaw, &run.Stake, &run.IntervalMS, &limitsRaw,
		&run.StartingBalance, &run.ContractsPlaced, &run.Wins, &run.Losses, &run.Voids,
		&run.ConsecutiveLosses, &run.ConsecutiveVoids, &run.RealisedPnL,
		&run.StartedAt, &run.StoppedAt, &run.UpdatedAt,
	)
	if err != nil {
		return nil, mapErr(err)
	}
	sel, err := models.ParseDigitSelection(selRaw)
	if err != nil {
		return nil, err
	}
	run.Selection = sel
	if err := json.Unmarshal(limitsRaw, &run.Limits); err != nil {
		return nil, err
	}
	return &run, nil
}

// GetLiveByAccount returns the account's live run, or ErrNotFound. The partial
// unique index guarantees there is at most one.
func (r *BotRunRepo) GetLiveByAccount(ctx context.Context, accountID uuid.UUID) (*models.BotRun, error) {
	run, err := scanBotRun(r.db.QueryRow(ctx,
		botRunSelect+` WHERE r.account_id = $1 AND r.status IN `+liveStatuses, accountID))
	if err != nil {
		return nil, mapErr(err)
	}
	return run, nil
}

// ListByAccount pages a user's runs. status is "live", "stopped", or "" for all.
func (r *BotRunRepo) ListByAccount(ctx context.Context, accountID uuid.UUID, status string, limit, offset int) ([]models.BotRun, int, error) {
	where, args := botRunWhere(accountID, status, "")
	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM bot_runs WHERE `+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}

	listWhere, listArgs := botRunWhere(accountID, status, "r")
	limitIdx := len(listArgs) + 1
	offsetIdx := len(listArgs) + 2
	queryArgs := append(append([]any{}, listArgs...), limit, offset)
	rows, err := r.db.Query(ctx,
		botRunSelect+` WHERE `+listWhere+
			fmt.Sprintf(` ORDER BY r.started_at DESC LIMIT $%d OFFSET $%d`, limitIdx, offsetIdx),
		queryArgs...)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()

	out := []models.BotRun{}
	for rows.Next() {
		run, err := scanBotRun(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *run)
	}
	return out, total, rows.Err()
}

func botRunWhere(accountID uuid.UUID, status, alias string) (string, []any) {
	p := ""
	if alias != "" {
		p = alias + "."
	}
	switch status {
	case "live":
		return p + `account_id = $1 AND ` + p + `status IN ` + liveStatuses, []any{accountID}
	case models.BotStopped:
		return p + `account_id = $1 AND ` + p + `status = 'stopped'`, []any{accountID}
	default:
		return p + `account_id = $1`, []any{accountID}
	}
}

// ListLive is what the admin console reads and what the kill switch acts on.
func (r *BotRunRepo) ListLive(ctx context.Context) ([]models.BotRun, error) {
	rows, err := r.db.Query(ctx, botRunSelect+` WHERE r.status IN `+liveStatuses+` ORDER BY r.started_at`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []models.BotRun{}
	for rows.Next() {
		run, err := scanBotRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

// AdminBotRun is a run with its owner attached, which the console needs and the
// user-facing paths do not: a user polling their own live run every two seconds
// already knows whose it is, and two extra joins on that path would be paid for
// nothing. The shape follows AdminTransaction, which answers the same question
// for the ledger table.
type AdminBotRun struct {
	models.BotRun
	AccountNumber string `json:"account_number"`
	AccountType   string `json:"account_type"`
	UserID        string `json:"user_id"`
	UserPhone     string `json:"user_phone"`
}

// ListAll pages every run on the platform, live or finished, for the console.
//
// Unlike ListLive this has no natural ceiling — BOT_MAX_RUNS caps what can be
// running at once, but nothing caps what has ever run — so it pages like the
// other admin tables rather than returning everything.
func (r *BotRunRepo) ListAll(ctx context.Context, status string, limit, offset int) ([]AdminBotRun, int, error) {
	const from = `
		FROM bot_runs r
		JOIN symbols s ON s.id = r.symbol_id
		JOIN accounts a ON a.id = r.account_id
		JOIN users u ON u.id = a.user_id`

	where := ` WHERE true`
	switch status {
	case "live":
		where = ` WHERE r.status IN ` + liveStatuses
	case models.BotStopped:
		where = ` WHERE r.status = 'stopped'`
	}

	var total int
	if err := r.db.QueryRow(ctx, `SELECT count(*)`+from+where).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}

	rows, err := r.db.Query(ctx, `
		SELECT r.id, r.account_id, r.symbol_id, r.status, r.stop_reason, r.last_error,
		       r.contract_type, r.selection, r.stake, r.interval_ms, r.limits,
		       r.starting_balance, r.contracts_placed, r.wins, r.losses, r.voids,
		       r.consecutive_losses, r.consecutive_voids, r.realised_pnl,
		       r.started_at, r.stopped_at, r.updated_at,
		       s.code, s.display_name,
		       a.account_number, a.account_type, u.id, u.phone_number`+
		from+where+` ORDER BY r.started_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()

	out := []AdminBotRun{}
	for rows.Next() {
		var row AdminBotRun
		var selRaw, limitsRaw []byte
		err := rows.Scan(
			&row.ID, &row.AccountID, &row.SymbolID, &row.Status, &row.StopReason, &row.LastError,
			&row.ContractType, &selRaw, &row.Stake, &row.IntervalMS, &limitsRaw,
			&row.StartingBalance, &row.ContractsPlaced, &row.Wins, &row.Losses, &row.Voids,
			&row.ConsecutiveLosses, &row.ConsecutiveVoids, &row.RealisedPnL,
			&row.StartedAt, &row.StoppedAt, &row.UpdatedAt,
			&row.SymbolCode, &row.SymbolName,
			&row.AccountNumber, &row.AccountType, &row.UserID, &row.UserPhone,
		)
		if err != nil {
			return nil, 0, err
		}
		sel, err := models.ParseDigitSelection(selRaw)
		if err != nil {
			return nil, 0, err
		}
		row.Selection = sel
		if err := json.Unmarshal(limitsRaw, &row.Limits); err != nil {
			return nil, 0, err
		}
		out = append(out, row)
	}
	return out, total, rows.Err()
}

// CountLive backs the global run cap and the bot_runs_live gauge.
func (r *BotRunRepo) CountLive(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM bot_runs WHERE status IN `+liveStatuses).Scan(&n)
	return n, mapErr(err)
}

// IncrementPlaced is called inside the transaction that opens a contract, so the
// count cannot include a contract that failed to commit.
func (r *BotRunRepo) IncrementPlaced(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE bot_runs
		SET contracts_placed = contracts_placed + 1, updated_at = now()
		WHERE id = $1`, id)
	return mapErr(err)
}

// ApplySettlement folds one settled contract into its run's totals, inside the
// same transaction that settles the contract and moves the money. That is what
// makes the counters trustworthy: they cannot drift from the contracts they
// summarise, and a contract settling after its run has stopped still lands here.
//
// A void is a third outcome, not a loss: it leaves consecutive_losses alone,
// because a feed hiccup is not a losing streak. It does advance
// consecutive_voids, which is how a stalled feed is detected — Open cannot report
// one, since a stale last price still prices a contract that then voids.
func (r *BotRunRepo) ApplySettlement(ctx context.Context, id uuid.UUID, status string, pnl decimal.Decimal) error {
	_, err := r.db.Exec(ctx, `
		UPDATE bot_runs SET
			wins   = wins   + CASE WHEN $2 = 'won'  THEN 1 ELSE 0 END,
			losses = losses + CASE WHEN $2 = 'lost' THEN 1 ELSE 0 END,
			voids  = voids  + CASE WHEN $2 = 'void' THEN 1 ELSE 0 END,
			realised_pnl = realised_pnl + $3,
			consecutive_losses = CASE
				WHEN $2 = 'lost' THEN consecutive_losses + 1
				WHEN $2 = 'won'  THEN 0
				ELSE consecutive_losses
			END,
			consecutive_voids = CASE WHEN $2 = 'void' THEN consecutive_voids + 1 ELSE 0 END,
			updated_at = now()
		WHERE id = $1`, id, status, pnl)
	return mapErr(err)
}

// SetStopping records that a stop was asked for while a contract is still in
// flight. It holds the account's live-run slot until that contract settles, so a
// second run cannot start into the gap.
func (r *BotRunRepo) SetStopping(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE bot_runs SET status = 'stopping', updated_at = now()
		WHERE id = $1 AND status = 'running'`, id)
	return mapErr(err)
}

// Finalise ends a run, and reports whether this call was the one that ended it.
//
// The status guard makes it idempotent: whoever stops a run first owns the
// reason, so a shutdown racing a stop-loss cannot rewrite history. The boolean
// is what lets the rest of the ending happen once too — a stop can be written by
// the request that asked for it and then attempted again by the run's own
// goroutine on its way out, and counting or announcing that twice would say the
// run stopped twice when it stopped once.
func (r *BotRunRepo) Finalise(ctx context.Context, id uuid.UUID, reason string, lastError *string) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE bot_runs
		SET status = 'stopped', stop_reason = $2, last_error = $3,
		    stopped_at = now(), updated_at = now()
		WHERE id = $1 AND status <> 'stopped'`, id, reason, lastError)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() > 0, nil
}

// StopAllLive is the kill switch and the restart sweep, and returns how many runs
// it ended. One statement rather than a loop, so there is no window in which some
// runs are stopped and others are not.
func (r *BotRunRepo) StopAllLive(ctx context.Context, reason string) (int, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE bot_runs
		SET status = 'stopped', stop_reason = $1, stopped_at = now(), updated_at = now()
		WHERE status IN `+liveStatuses, reason)
	if err != nil {
		return 0, mapErr(err)
	}
	return int(tag.RowsAffected()), nil
}
