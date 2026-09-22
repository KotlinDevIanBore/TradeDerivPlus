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
		// PLACEHOLDER: scale by how many digits win (house edge ~5%).
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
		// Fair profit ≈ (10/winCount) - 1; apply 0.95 house factor.
		fair := decimal.NewFromInt(10).Div(decimal.NewFromInt(int64(winCount))).Sub(decimal.NewFromInt(1))
		return fair.Mul(decimal.RequireFromString("0.95")).Round(4), nil
	default:
		return decimal.Zero, fmt.Errorf("unknown contract type")
	}
}
