package models

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// Placeholder payout ratios for v1. NOT final for real-money use — replace
// once product confirms the table. Even/Odd match the benchmark screenshot
// (95.20% → $10 stake wins $19.52 total credit). Match/Differ/Over/Under are
// illustrative placeholders consistent with the investigation §2.3 sketch.
//
// payout_ratio means: win credit = stake × (1 + ratio). Loss forfeits stake.

var (
	placeholderEvenOdd = decimal.RequireFromString("0.9520") // PLACEHOLDER (benchmark)
	placeholderMatch   = decimal.RequireFromString("8.0000") // PLACEHOLDER ~9x-ish stake return as credit
	placeholderDiffer  = decimal.RequireFromString("0.1000") // PLACEHOLDER
)

// PayoutRatioFor returns the static ratio for a contract type + selection.
func PayoutRatioFor(contractType string, sel DigitSelection) (decimal.Decimal, error) {
	switch contractType {
	case DigitEvenOdd:
		if sel.Parity != DigitParityEven && sel.Parity != DigitParityOdd {
			return decimal.Zero, fmt.Errorf("parity must be even or odd")
		}
		return placeholderEvenOdd, nil
	case DigitMatchesDiffers:
		if sel.Digit == nil || *sel.Digit < 0 || *sel.Digit > 9 {
			return decimal.Zero, fmt.Errorf("digit must be 0–9")
		}
		switch sel.Mode {
		case DigitModeMatch:
			return placeholderMatch, nil
		case DigitModeDiffer:
			return placeholderDiffer, nil
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
		// PLACEHOLDER: scale by how many digits win.
		// Over barrier B → digits B+1..9; Under → 0..B-1.
		//
		// The 0.95 factor below applies to the *profit*, not the payout, so the
		// realised house edge is 5% × (1 − p) and varies with the barrier: from
		// 0.50% at Over 0 / Under 9 up to 4.50% at Over 8 / Under 1. It is not a
		// flat 5%. ExpectedReturnFor computes the figure for a given selection.
		winCount := 0
		if sel.Side == DigitSideOver {
			winCount = 9 - *sel.Barrier
		} else {
			winCount = *sel.Barrier
		}
		if winCount <= 0 {
			return decimal.Zero, fmt.Errorf("that barrier leaves no winning digits")
		}
		// Fair profit ≈ (10/winCount) - 1; apply 0.95 house factor.
		fair := decimal.NewFromInt(10).Div(decimal.NewFromInt(int64(winCount))).Sub(decimal.NewFromInt(1))
		return fair.Mul(decimal.RequireFromString("0.95")).Round(4), nil
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
// Every contract type on the placeholder table returns a negative number, from
// about −0.005 (Over 0 / Under 9) to −0.10 (Matches). It is surfaced on the
// quote so the UI states the cost of a contract rather than implying a profit.
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
