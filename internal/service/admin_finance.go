package service

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

// The admin finance report: volume, house P&L, and the realised edge against
// what the payout table predicts.
//
// Sign convention, stated once and applied everywhere below: all figures are
// HOUSE-side. house_pnl = -sum(contract pnl); realised edge = house_pnl / stakes;
// theoretical edge = -(stake-weighted ExpectedReturnFor). A positive number means
// the house is ahead. The customer-side numbers are the negatives of these, and
// nothing in this file or the screen shows them, so the two can never be mixed.
//
// Scope: digit contracts only (plan decision D2). Surface is carried in the
// response so the omission is visible to any client rather than implied.

// FinanceSurface names what the report covers. A CFD `trades` report would be a
// second value here rather than silently folded into this one.
const FinanceSurface = "digit_contracts"

// FinanceMaxSpan bounds a custom range. The enum ranges are all well inside it;
// this exists so that a from/to API added later cannot ask for a decade.
const FinanceMaxSpan = 400 * 24 * time.Hour

// FinanceMinEdgeSample is where a realised edge starts being worth reading.
//
// A contract's outcome has a standard deviation of roughly one stake, so the
// standard error of the realised edge is about 1/sqrt(n). At n = 1000 that is
// +/-3.2 percentage points against house edges of 0.5 to 4.5 points — the noise
// still exceeds the signal, but a gross mispricing would show. Below this the
// figure is noise dressed as a measurement, which is the presentation risk the
// investigation rated most likely to cause a wrong decision, so the report says
// so rather than leaving the reader to infer it.
const FinanceMinEdgeSample = 1000

// EdgeCheck compares what the house actually won against what the payout table
// says it should win.
type EdgeCheck struct {
	SampleSize int `json:"sample_size"`
	// Realised and Theoretical are fractions, not percentages: 0.024 is 2.4%.
	Realised    decimal.Decimal `json:"realised"`
	Theoretical decimal.Decimal `json:"theoretical"`
	// TooFewToRead is computed here rather than on the client so every consumer
	// agrees about when the comparison means something.
	TooFewToRead bool `json:"too_few_to_read"`
	MinSample    int  `json:"min_sample"`
	// Unpriced counts contracts whose selection ExpectedReturnFor rejected. It
	// should always be zero; a non-zero value means the pricer and the stored
	// contracts disagree about what a valid selection is, which is worth seeing
	// rather than silently dropping.
	Unpriced int `json:"unpriced"`
}

// FinanceTypeRow is one contract type across all funding buckets.
type FinanceTypeRow struct {
	ContractType string `json:"contract_type"`
	// Contracts counts every funding bucket — what was played, not what it cost.
	Contracts   int                      `json:"contracts"`
	Real        repository.FinanceBucket `json:"real"`
	Promotional repository.FinanceBucket `json:"promotional"`
	Edge        EdgeCheck                `json:"edge"`
}

// FinanceReport is the whole screen in one response.
type FinanceReport struct {
	Surface     string    `json:"surface"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	GeneratedAt time.Time `json:"generated_at"`

	// Real is the business number: real accounts, real-funded stakes.
	Real repository.FinanceBucket `json:"real"`
	// Promotional is bonus-funded stakes on REAL accounts — a win there pays
	// withdrawable value out of money the house gave away. A real cost, but not
	// trading performance, so it is never added to Real (plan decision D1).
	Promotional repository.FinanceBucket `json:"promotional"`
	// DemoContracts is a count only. Demo money is not money and no demo figure
	// leaves this service.
	DemoContracts int `json:"demo_contracts"`

	// Edge covers every funding bucket: the check on the game itself — the
	// payout table, the feed's digit uniformity, the settlement code. It is
	// readable against demo volume, which is the only volume there is today.
	Edge EdgeCheck `json:"edge"`
	// EdgeReal is the same check restricted to real-funded stakes.
	EdgeReal EdgeCheck `json:"edge_real"`

	ByType      []FinanceTypeRow           `json:"by_type"`
	ByDay       []repository.FinanceDayRow `json:"by_day"`
	ByDayCapped bool                       `json:"by_day_capped"`
	Flows       repository.FinanceFlows    `json:"flows"`
}

// FinanceHeadline is the dashboard's two tiles. Two queries over 24 hours, not
// the whole report: the dashboard already runs eleven and is loaded constantly.
type FinanceHeadline struct {
	HousePnL24h  decimal.Decimal `json:"house_pnl_24h"`
	Stakes24h    decimal.Decimal `json:"stakes_24h"`
	Contracts24h int             `json:"contracts_24h"`
	Edge         EdgeCheck       `json:"edge"`
}

// FinanceRange maps the query enum onto a half-open [from, to).
//
// A zero `from` means unbounded — the caller resolves it against the earliest
// settlement rather than guessing an epoch.
func FinanceRange(name string, now time.Time) (from, to time.Time, err error) {
	to = now
	switch name {
	case "24h":
		return now.Add(-24 * time.Hour), to, nil
	case "7d":
		return now.AddDate(0, 0, -7), to, nil
	case "30d":
		return now.AddDate(0, 0, -30), to, nil
	case "all":
		return time.Time{}, to, nil
	default:
		return time.Time{}, time.Time{}, invalid("range", "Range must be 24h, 7d, 30d or all")
	}
}

// Finance builds the report for [from, to). A zero `from` means "everything",
// resolved here against the first settlement on record.
func (s *AdminService) Finance(ctx context.Context, from, to time.Time) (*FinanceReport, error) {
	if to.IsZero() {
		return nil, invalid("to", "A range end is required")
	}
	if !from.IsZero() {
		if !from.Before(to) {
			return nil, invalid("from", "The range must start before it ends")
		}
		if to.Sub(from) > FinanceMaxSpan {
			return nil, invalid("from", "That range is too long to report on at once")
		}
	} else {
		earliest, ok, err := s.store.Finance.EarliestSettlement(ctx)
		if err != nil {
			return nil, err
		}
		if ok {
			from = earliest
		} else {
			// Nothing has ever settled. A day of empty buckets renders as an
			// empty screen rather than an error.
			from = to.Add(-24 * time.Hour)
		}
		if to.Sub(from) > FinanceMaxSpan {
			from = to.Add(-FinanceMaxSpan)
		}
	}

	r := &FinanceReport{
		Surface:     FinanceSurface,
		From:        from,
		To:          to,
		GeneratedAt: time.Now().UTC(),
		ByType:      []FinanceTypeRow{},
		ByDay:       []repository.FinanceDayRow{},
	}

	totals, err := s.store.Finance.Totals(ctx, from, to)
	if err != nil {
		return nil, err
	}
	r.Real = totals[repository.FundingReal]
	r.Promotional = totals[repository.FundingPromotional]
	r.DemoContracts = totals[repository.FundingDemo].Contracts

	cells, err := s.store.Finance.ByContractType(ctx, from, to)
	if err != nil {
		return nil, err
	}

	days, capped, err := s.store.Finance.ByDay(ctx, from, to)
	if err != nil {
		return nil, err
	}
	r.ByDay, r.ByDayCapped = days, capped

	edges, err := s.store.Finance.EdgeCells(ctx, from, to)
	if err != nil {
		return nil, err
	}
	r.Edge = WeightTheoreticalEdge(edges, EdgeAllFunding)
	r.EdgeReal = WeightTheoreticalEdge(edges, EdgeRealOnly)

	if r.Flows, err = s.store.Finance.Flows(ctx, from, to); err != nil {
		return nil, err
	}

	r.ByType = foldTypeCells(cells, edges)
	return r, nil
}

// foldTypeCells turns one row per (type, bucket) into one row per type, with the
// per-type edge attached.
func foldTypeCells(cells []repository.FinanceTypeCell, edges []repository.EdgeCell) []FinanceTypeRow {
	order := []string{}
	byType := map[string]*FinanceTypeRow{}

	for _, c := range cells {
		row, ok := byType[c.ContractType]
		if !ok {
			row = &FinanceTypeRow{ContractType: c.ContractType}
			byType[c.ContractType] = row
			order = append(order, c.ContractType)
		}
		row.Contracts += c.Contracts
		switch c.Bucket {
		case repository.FundingReal:
			row.Real = c.FinanceBucket
		case repository.FundingPromotional:
			row.Promotional = c.FinanceBucket
		}
		// Demo contributes to Contracts and nothing else.
	}

	for _, t := range order {
		typeEdges := []repository.EdgeCell{}
		for _, e := range edges {
			if e.ContractType == t {
				typeEdges = append(typeEdges, e)
			}
		}
		byType[t].Edge = WeightTheoreticalEdge(typeEdges, EdgeAllFunding)
	}

	out := make([]FinanceTypeRow, 0, len(order))
	for _, t := range order {
		out = append(out, *byType[t])
	}
	return out
}

// EdgePicker chooses which population of an EdgeCell to weigh: every funding
// bucket, or real-funded stakes only.
type EdgePicker func(repository.EdgeCell) (n int, stakes, housePnL decimal.Decimal)

// EdgeAllFunding weighs every contract — the check on the game itself.
func EdgeAllFunding(c repository.EdgeCell) (int, decimal.Decimal, decimal.Decimal) {
	return c.Contracts, c.Stakes, c.HousePnL
}

// EdgeRealOnly weighs real-funded stakes — the check on the money.
func EdgeRealOnly(c repository.EdgeCell) (int, decimal.Decimal, decimal.Decimal) {
	return c.RealContracts, c.RealStakes, c.RealHousePnL
}

// WeightTheoreticalEdge compares realised house performance against the payout
// table's prediction, weighted by stake.
//
// Weighted by stake and not by contract count, because the edge is a fraction OF
// the stake: a hundred $1 contracts on a 0.5% selection and one $100 contract on
// a 4.5% one are equal volume and unequal cost, and a count-weighted mean would
// report the average of the two rates rather than the rate the house actually
// earned.
//
// A selection ExpectedReturnFor rejects is counted in Unpriced and excluded from
// both sums. Dropping it silently would quietly change the denominator and make
// the comparison look better or worse than it is.
func WeightTheoreticalEdge(cells []repository.EdgeCell, pick EdgePicker) EdgeCheck {
	out := EdgeCheck{MinSample: FinanceMinEdgeSample}

	weighted := decimal.Zero
	totalStakes := decimal.Zero
	totalHousePnL := decimal.Zero

	for _, c := range cells {
		n, stakes, housePnL := pick(c)
		if n == 0 {
			continue
		}
		sel, err := models.ParseDigitSelection(c.Selection)
		if err != nil {
			out.Unpriced += n
			continue
		}
		er, err := models.ExpectedReturnFor(c.ContractType, sel)
		if err != nil {
			out.Unpriced += n
			continue
		}
		out.SampleSize += n
		totalStakes = totalStakes.Add(stakes)
		totalHousePnL = totalHousePnL.Add(housePnL)
		// ExpectedReturnFor is customer-side and negative; the house edge is its
		// negation.
		weighted = weighted.Add(stakes.Mul(er.Neg()))
	}

	out.TooFewToRead = out.SampleSize < FinanceMinEdgeSample
	if totalStakes.IsZero() {
		return out
	}
	out.Theoretical = weighted.Div(totalStakes).Round(6)
	out.Realised = totalHousePnL.Div(totalStakes).Round(6)
	return out
}

// financeHeadline is the dashboard's 24-hour summary: two queries, not the five
// the full report runs. The dashboard is loaded constantly and already issues
// eleven; this is the narrowest thing that answers "is the house ahead today".
func (s *AdminService) financeHeadline(ctx context.Context) (*FinanceHeadline, error) {
	to := time.Now().UTC()
	from := to.Add(-24 * time.Hour)

	totals, err := s.store.Finance.Totals(ctx, from, to)
	if err != nil {
		return nil, err
	}
	real := totals[repository.FundingReal]

	cells, err := s.store.Finance.EdgeCells(ctx, from, to)
	if err != nil {
		return nil, err
	}

	return &FinanceHeadline{
		HousePnL24h:  real.HousePnL,
		Stakes24h:    real.Stakes,
		Contracts24h: real.Contracts,
		Edge:         WeightTheoreticalEdge(cells, EdgeAllFunding),
	}, nil
}
