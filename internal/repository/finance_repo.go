package repository

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// Aggregates for the admin finance screen: what was traded, what the house won
// or lost, and whether the realised edge matches the payout table.
//
// Three rules hold for every query in this file. They are the correctness risks
// docs/investigation-reports/financial-reporting-investigation.md section 4.3
// identified, turned into properties of the file:
//
//  1. Every query that reads MONEY out of digit_contracts is built on
//     financeScope below, and nothing else here may be. A second scope would
//     almost certainly omit the funding-balance join and count promotional
//     money as earnings, which is the mistake the investigation rated most
//     likely. The one other `FROM digit_contracts` in this file is
//     EarliestSettlement, which returns a timestamp and no money at all — it
//     cannot express the bug, and it deliberately has no funding join because
//     "when did anything first settle" is not a per-bucket question.
//  2. payout_ratio is never read. House P&L comes from the stored pnl column
//     only. Deriving payouts as stake x (1 + payout_ratio) disagrees with pnl by
//     the per-contract rounding in models.WinCredit (section 1.3).
//  3. bot_runs is never read. bot_runs.realised_pnl is a maintained total of the
//     same digit_contracts rows, so reading both double-counts (section 1.4e).
//     The bot split is dc.bot_run_id IS NOT NULL, inside the one scope.
//
// Sign convention: everything is house-side. house_pnl = -sum(pnl), so a
// positive number means the house is ahead.
//
// Scope: digit contracts only. The CFD `trades` table is a second source with
// the same stake/pnl shape and is deliberately out of v1 (plan decision D2); a
// trades aggregate would be one more function returning FinanceBucket.
type FinanceRepo struct{ db DBTX }

// financeScope assumes settled_at is always set for status IN ('won','lost','void').
// This holds today because Settle and Void are the only writers of status, and both
// set settled_at unconditionally in the same statement — see digit_contract_repo.go.
// It is an application guarantee, not a schema one (settled_at has no DEFAULT, no
// trigger). If a third void/settlement path is ever added, it must also set
// settled_at, or rows will silently drop out of every time-ranged finance query.
//
// The join is to digit_open ONLY. It is one row per contract and always present,
// and it carries the balance the stake was funded from — which digit_contracts
// itself does not record. Joining digit_settle as well would double every row.
// TransactionRepo.QueryDigitFundingBalance does the same lookup one row at a time.
//
// $1/$2 are the half-open range [from, to).
const financeScope = `
	FROM digit_contracts dc
	JOIN accounts a     ON a.id = dc.account_id
	JOIN transactions t ON t.reference = dc.id::text AND t.type = 'digit_open'
	WHERE dc.status IN ('won','lost','void')
	  AND dc.settled_at >= $1 AND dc.settled_at < $2`

// Funding buckets. Demo is excluded from every money figure and reported as a
// count; promotional is bonus-funded stakes on a REAL account, which is a real
// house cost but not trading performance (plan decision D1).
const (
	FundingReal        = "real"
	FundingPromotional = "promotional"
	FundingDemo        = "demo"
)

// fundingBucket classifies a contract by whose money and which balance funded it.
// Demo is tested first: a demo account's stake is bonus-funded by construction,
// and calling that "promotional" would mix practice money into a cost figure.
const fundingBucket = `
	CASE WHEN a.account_type = 'demo'   THEN 'demo'
	     WHEN t.balance_type  = 'bonus' THEN 'promotional'
	     ELSE 'real' END`

// FinanceBucket is one funding bucket's totals over a range.
type FinanceBucket struct {
	Contracts int `json:"contracts"`
	Wins      int `json:"wins"`
	Losses    int `json:"losses"`
	Voids     int `json:"voids"`
	// Stakes excludes voids: a voided contract is refunded in full and never
	// resolved, so its stake is not volume traded.
	Stakes decimal.Decimal `json:"stakes"`
	// HousePnL is -sum(pnl), from the pnl column only — never derived from
	// payout_ratio. See rule 2 in the file comment.
	HousePnL     decimal.Decimal `json:"house_pnl"`
	BotContracts int             `json:"bot_contracts"`
	BotHousePnL  decimal.Decimal `json:"bot_house_pnl"`
}

// The select list every bucket aggregate shares, so the three cannot drift.
const financeBucketColumns = `
	count(*),
	count(*) FILTER (WHERE dc.status = 'won'),
	count(*) FILTER (WHERE dc.status = 'lost'),
	count(*) FILTER (WHERE dc.status = 'void'),
	COALESCE(sum(dc.stake) FILTER (WHERE dc.status <> 'void'), 0),
	-COALESCE(sum(dc.pnl), 0),
	count(*) FILTER (WHERE dc.bot_run_id IS NOT NULL),
	-COALESCE(sum(dc.pnl) FILTER (WHERE dc.bot_run_id IS NOT NULL), 0)`

func scanFinanceBucket(row interface {
	Scan(...any) error
}, extra ...any) (FinanceBucket, error) {
	var b FinanceBucket
	dest := append(extra,
		&b.Contracts, &b.Wins, &b.Losses, &b.Voids,
		&b.Stakes, &b.HousePnL, &b.BotContracts, &b.BotHousePnL)
	return b, row.Scan(dest...)
}

// Totals is the headline, one row per funding bucket. Buckets with no contracts
// are absent from the map; the caller zero-values them.
func (r *FinanceRepo) Totals(ctx context.Context, from, to time.Time) (map[string]FinanceBucket, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+fundingBucket+` AS bucket,`+financeBucketColumns+
		financeScope+`
		GROUP BY bucket`, from, to)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := map[string]FinanceBucket{}
	for rows.Next() {
		var bucket string
		b, err := scanFinanceBucket(rows, &bucket)
		if err != nil {
			return nil, mapErr(err)
		}
		out[bucket] = b
	}
	return out, mapErr(rows.Err())
}

// FinanceTypeCell is one (contract type, funding bucket) pair. The service folds
// these into one row per contract type.
type FinanceTypeCell struct {
	ContractType string
	Bucket       string
	FinanceBucket
}

// ByContractType is Totals split by contract type as well — where a mispriced
// selection shows up.
func (r *FinanceRepo) ByContractType(ctx context.Context, from, to time.Time) ([]FinanceTypeCell, error) {
	rows, err := r.db.Query(ctx, `
		SELECT dc.contract_type, `+fundingBucket+` AS bucket,`+financeBucketColumns+
		financeScope+`
		GROUP BY dc.contract_type, bucket
		ORDER BY dc.contract_type`, from, to)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []FinanceTypeCell{}
	for rows.Next() {
		var c FinanceTypeCell
		b, err := scanFinanceBucket(rows, &c.ContractType, &c.Bucket)
		if err != nil {
			return nil, mapErr(err)
		}
		c.FinanceBucket = b
		out = append(out, c)
	}
	return out, mapErr(rows.Err())
}

// FinanceDayRow is one calendar day of the series.
type FinanceDayRow struct {
	Day                 time.Time       `json:"day"`
	Contracts           int             `json:"contracts"`
	RealStakes          decimal.Decimal `json:"real_stakes"`
	RealHousePnL        decimal.Decimal `json:"real_house_pnl"`
	PromotionalHousePnL decimal.Decimal `json:"promotional_house_pnl"`
	DemoContracts       int             `json:"demo_contracts"`
}

// FinanceDayCap bounds the series. A day per row means "all" on a busy platform
// would otherwise return an unbounded list to a chart with a fixed width.
const FinanceDayCap = 366

// ByDay is the daily series, built the way DepositsByHour is: generate_series
// left-joined to the real rows, so a day with no contracts is a zero rather than
// a gap. A chart that skips empty days misreads a quiet week as a short one.
//
// Returns the LAST FinanceDayCap days of the range when it is longer, and
// reports whether it truncated.
func (r *FinanceRepo) ByDay(ctx context.Context, from, to time.Time) ([]FinanceDayRow, bool, error) {
	rows, err := r.db.Query(ctx, `
		WITH days AS (
			SELECT generate_series(
				date_trunc('day', $1::timestamptz),
				date_trunc('day', $2::timestamptz - interval '1 microsecond'),
				interval '1 day') AS day
		),
		capped AS (
			SELECT day FROM days ORDER BY day DESC LIMIT $3
		),
		settled AS (
			SELECT date_trunc('day', dc.settled_at) AS day,
			       `+fundingBucket+` AS bucket,
			       dc.stake, dc.pnl, dc.status`+
		financeScope+`
		)
		SELECT c.day,
		       count(s.*),
		       COALESCE(sum(s.stake) FILTER (WHERE s.bucket = 'real' AND s.status <> 'void'), 0),
		       -COALESCE(sum(s.pnl)  FILTER (WHERE s.bucket = 'real'), 0),
		       -COALESCE(sum(s.pnl)  FILTER (WHERE s.bucket = 'promotional'), 0),
		       count(s.*) FILTER (WHERE s.bucket = 'demo')
		FROM capped c
		LEFT JOIN settled s ON s.day = c.day
		GROUP BY c.day
		ORDER BY c.day`, from, to, FinanceDayCap+1)
	if err != nil {
		return nil, false, mapErr(err)
	}
	defer rows.Close()

	out := []FinanceDayRow{}
	for rows.Next() {
		var d FinanceDayRow
		if err := rows.Scan(&d.Day, &d.Contracts, &d.RealStakes,
			&d.RealHousePnL, &d.PromotionalHousePnL, &d.DemoContracts); err != nil {
			return nil, false, mapErr(err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, false, mapErr(err)
	}

	// One over the cap was requested, so a full extra row means the range is
	// longer than the cap. Drop it and say so.
	capped := len(out) > FinanceDayCap
	if capped {
		out = out[len(out)-FinanceDayCap:]
	}
	return out, capped, nil
}

// EdgeCell is one distinct selection's realised performance, for comparison
// against what models.ExpectedReturnFor predicts for that same selection. The
// weighting is done in Go: the theoretical figure needs the typed selection, and
// SQL has only the JSONB.
//
// Voids are excluded — a refunded contract carries no edge either way.
type EdgeCell struct {
	ContractType string
	// Selection is the raw JSONB, for models.ParseDigitSelection.
	Selection []byte

	// All funding: the check on the game itself — the payout table, the feed's
	// digit uniformity, and the settlement code. Readable against demo volume.
	Contracts int
	Stakes    decimal.Decimal
	HousePnL  decimal.Decimal

	// Real-funded only: the same check restricted to money that matters.
	RealContracts int
	RealStakes    decimal.Decimal
	RealHousePnL  decimal.Decimal
}

func (r *FinanceRepo) EdgeCells(ctx context.Context, from, to time.Time) ([]EdgeCell, error) {
	rows, err := r.db.Query(ctx, `
		SELECT dc.contract_type, dc.selection,
		       count(*),
		       COALESCE(sum(dc.stake), 0),
		       -COALESCE(sum(dc.pnl), 0),
		       count(*)                   FILTER (WHERE `+fundingBucket+` = 'real'),
		       COALESCE(sum(dc.stake)  FILTER (WHERE `+fundingBucket+` = 'real'), 0),
		       -COALESCE(sum(dc.pnl)   FILTER (WHERE `+fundingBucket+` = 'real'), 0)`+
		financeScope+`
		  AND dc.status <> 'void'
		GROUP BY dc.contract_type, dc.selection`, from, to)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := []EdgeCell{}
	for rows.Next() {
		var c EdgeCell
		if err := rows.Scan(&c.ContractType, &c.Selection,
			&c.Contracts, &c.Stakes, &c.HousePnL,
			&c.RealContracts, &c.RealStakes, &c.RealHousePnL); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, c)
	}
	return out, mapErr(rows.Err())
}

// FinanceFlows is money in and out over the range, plus the liability that money
// covers. Net float is customer money held, NOT profit.
type FinanceFlows struct {
	Deposits       int             `json:"deposits"`
	DepositsUSD    decimal.Decimal `json:"deposits_usd"`
	Withdrawals    int             `json:"withdrawals"`
	WithdrawalsUSD decimal.Decimal `json:"withdrawals_usd"`
	NetFloatUSD    decimal.Decimal `json:"net_float_usd"`
	// CustomerLiabilityUSD is point-in-time, not ranged: the sum of every real
	// account's real balance as at now.
	CustomerLiabilityUSD decimal.Decimal `json:"customer_liability_usd"`
}

// Flows generalises TodayTotals to an arbitrary range.
//
// balanceAffectingRows is the shared predicate, concatenated rather than
// restated: "completed, or a pending withdrawal". A pending withdrawal is money
// already gone — the balance was debited up front — and counting only
// 'completed' once made the withdrawal tiles structurally zero while real money
// was leaving wallets. Using the same const as the drift gauge is what stops
// this screen and the dashboard disagreeing about what has moved.
func (r *FinanceRepo) Flows(ctx context.Context, from, to time.Time) (FinanceFlows, error) {
	var f FinanceFlows
	err := r.db.QueryRow(ctx, `
		SELECT
			count(*)                            FILTER (WHERE type = 'deposit'),
			COALESCE(sum(abs(amount)) FILTER (WHERE type = 'deposit'), 0),
			count(*)                            FILTER (WHERE type = 'withdrawal'),
			COALESCE(sum(abs(amount)) FILTER (WHERE type = 'withdrawal'), 0)
		FROM transactions
		WHERE type IN ('deposit','withdrawal')
		  AND `+balanceAffectingRows+`
		  AND created_at >= $1 AND created_at < $2`, from, to,
	).Scan(&f.Deposits, &f.DepositsUSD, &f.Withdrawals, &f.WithdrawalsUSD)
	if err != nil {
		return f, mapErr(err)
	}
	f.NetFloatUSD = f.DepositsUSD.Sub(f.WithdrawalsUSD)

	err = r.db.QueryRow(ctx, `
		SELECT COALESCE(sum(w.real_balance), 0)
		FROM wallets w
		JOIN accounts a ON a.id = w.account_id
		WHERE a.account_type = 'real'`).Scan(&f.CustomerLiabilityUSD)
	return f, mapErr(err)
}

// EarliestSettlement is when the first contract settled, for the "all" range.
// An index-end read on idx_digit_contracts_settled. Reports false when nothing
// has settled yet.
func (r *FinanceRepo) EarliestSettlement(ctx context.Context) (time.Time, bool, error) {
	var t *time.Time
	if err := r.db.QueryRow(ctx,
		`SELECT min(settled_at) FROM digit_contracts WHERE settled_at IS NOT NULL`,
	).Scan(&t); err != nil {
		return time.Time{}, false, mapErr(err)
	}
	if t == nil {
		return time.Time{}, false, nil
	}
	return *t, true, nil
}
