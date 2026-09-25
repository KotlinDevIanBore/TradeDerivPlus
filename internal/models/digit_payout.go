package models

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// Payout ratios for v1. STILL NOT FINAL FOR REAL-MONEY USE — product has not
// confirmed the table (digit-contracts-investigation.md §9 item 1, and
// auto-bot-plan.md §10 item 1). What changed is that the numbers are now
// internally consistent; who chose them has not.
//
// Every selection except Even/Odd is priced by one rule, houseRatio: fair odds
// for however many of the ten digits win it, with a 5% house factor applied to
// the profit. Previously Matches and Differs were round numbers picked by hand
// (8.0000 and 0.1000) while Over/Under was computed, which priced identical bets
// differently — Matches and Over 8 are both one-digit-in-ten, and Matches cost
// 2.2× more (−10.00% against −4.50%); Differs and Under 9 are both
// nine-digits-in-ten, and Differs cost 2× more. See payout-table-status.md §5.
//
// Even/Odd keeps 0.9520 deliberately. It is the one figure with an external
// basis — read off a benchmark screenshot, where $10 at 95.20% returns $19.52 —
// rather than a number this codebase invented, so it is left for product to
// confirm or replace rather than quietly rounded to the house rule's 0.9500.
//
// payout_ratio means: win credit = stake × (1 + ratio). Loss forfeits stake.

// houseFactor is applied to the PROFIT, not the payout. The realised house edge
// is therefore 5% × (1 − p) and varies with the selection — 0.50% where nine
// digits win, 4.50% where one does. It is not a flat 5%; ExpectedReturnFor
// computes the true figure for a given selection.
var houseFactor = decimal.RequireFromString("0.95")

// benchmarkEvenOdd is the one ratio not derived from houseRatio. See above.
var benchmarkEvenOdd = decimal.RequireFromString("0.9520")

// houseRatio prices any selection from how many of the ten digits win it.
//
// One rule for Matches, Differs and every Over/Under barrier, so two selections
// with the same win probability cannot be priced differently. That was not true
// before: the rule existed only inside the Over/Under branch, and the other two
// families were constants nobody rechecked against it.
func houseRatio(winCount int) decimal.Decimal {
	fair := decimal.NewFromInt(10).Div(decimal.NewFromInt(int64(winCount))).Sub(decimal.NewFromInt(1))
	return fair.Mul(houseFactor).Round(4)
}

// PayoutRatioFor returns the static ratio for a contract type + selection.
func PayoutRatioFor(contractType string, sel DigitSelection) (decimal.Decimal, error) {
	switch contractType {
	case DigitEvenOdd:
		if sel.Parity != DigitParityEven && sel.Parity != DigitParityOdd {
			return decimal.Zero, fmt.Errorf("parity must be even or odd")
		}
		return benchmarkEvenOdd, nil
	case DigitMatchesDiffers:
		if sel.Digit == nil || *sel.Digit < 0 || *sel.Digit > 9 {
			return decimal.Zero, fmt.Errorf("digit must be 0–9")
		}
		switch sel.Mode {
		case DigitModeMatch:
			// Exactly one digit in ten wins.
			return houseRatio(1), nil
		case DigitModeDiffer:
			// Nine digits in ten win.
			return houseRatio(9), nil
		default:
			return decimal.Zero, fmt.Errorf("mode must be match or differ")
		}
	case DigitOverUnder:
		if sel.Barrier == nil || *sel.Barrier < 0 || *sel.Barrier > 9 {
			return decimal.Zero, fmt.Errorf("barrier must be 0–9")
		}
		if sel.Side != DigitSideOver && sel.Side != DigitSideUnder {
			return decimal.Zero, fmt.Errorf("side must be over or under")
		}
		// Over barrier B → digits B+1..9; Under → 0..B-1.
		winCount := 0
		if sel.Side == DigitSideOver {
			winCount = 9 - *sel.Barrier
		} else {
			winCount = *sel.Barrier
		}
		if winCount <= 0 {
			return decimal.Zero, fmt.Errorf("that barrier leaves no winning digits")
		}
		return houseRatio(winCount), nil
	default:
		return decimal.Zero, fmt.Errorf("unknown contract type")
	}
}

// WinProbabilityFor is the chance the selection wins, given that last digits are
// uniform and independent on this feed (verified: see the auto-bot investigation
// §2.5). It is a property of the selection alone — no price, no history.
func WinProbabilityFor(contractType string, sel DigitSelection) (decimal.Decimal, error) {
	// Validate through the pricing function so the two can never disagree about
	// what a well-formed selection is.
	if _, err := PayoutRatioFor(contractType, sel); err != nil {
		return decimal.Zero, err
	}
	ten := decimal.NewFromInt(10)
	switch contractType {
	case DigitEvenOdd:
		// Five of ten digits either way.
		return decimal.NewFromInt(5).Div(ten), nil
	case DigitMatchesDiffers:
		if sel.Mode == DigitModeMatch {
			return decimal.NewFromInt(1).Div(ten), nil
		}
		return decimal.NewFromInt(9).Div(ten), nil
	case DigitOverUnder:
		winCount := *sel.Barrier
		if sel.Side == DigitSideOver {
			winCount = 9 - *sel.Barrier
		}
		return decimal.NewFromInt(int64(winCount)).Div(ten), nil
	default:
		return decimal.Zero, fmt.Errorf("unknown contract type")
	}
}

// ExpectedReturnFor is the expected profit per unit staked: p × (1 + ratio) − 1.
// Every contract type returns a negative number, from about −0.005 (Over 0 /
// Under 9, and Differs, which is the same bet) to −0.045 (Over 8 / Under 1, and
// Matches, likewise the same bet). It is surfaced on the quote so the UI states
// the cost of a contract rather than implying a profit.
func ExpectedReturnFor(contractType string, sel DigitSelection) (decimal.Decimal, error) {
	ratio, err := PayoutRatioFor(contractType, sel)
	if err != nil {
		return decimal.Zero, err
	}
	p, err := WinProbabilityFor(contractType, sel)
	if err != nil {
		return decimal.Zero, err
	}
	return p.Mul(decimal.NewFromInt(1).Add(ratio)).Sub(decimal.NewFromInt(1)).Round(6), nil
}
