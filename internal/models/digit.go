package models

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// LastDigit returns the last decimal digit of price when written at pipSize
// fractional places — the same formatting the feed uses when it rounds a tick.
//
// Rule (port this exactly to TypeScript for the digit-distribution row):
//  1. Format the price with exactly pipSize digits after the decimal point
//     (fixed-point, not scientific), e.g. pipSize 2 → "9449.94".
//  2. Walk the string from the end and take the first character that is a
//     digit 0–9. That character, as an integer, is the last digit.
//
// Examples: 9449.94 @ 2 → 4; 4185.8107 @ 4 → 7; 100 @ 0 → 0.
func LastDigit(price decimal.Decimal, pipSize int) int {
	if pipSize < 0 {
		pipSize = 0
	}
	s := price.StringFixed(int32(pipSize))
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c >= '0' && c <= '9' {
			return int(c - '0')
		}
	}
	return 0
}

// FormatLastDigitDoc is a stable description for API docs and the Phase 2
// TypeScript port — keep the Go and TS implementations in lockstep with this.
func FormatLastDigitDoc() string {
	return "lastDigit(price, pipSize): format price with StringFixed(pipSize), " +
		"then take the last character that is a digit 0-9"
}

// DigitsEven reports whether d is even (0,2,4,6,8).
func DigitEven(d int) bool { return d%2 == 0 }

// DigitWins evaluates a digit contract's selection against the settlement digit.
func DigitWins(contractType string, sel DigitSelection, settlementDigit int) (bool, error) {
	switch contractType {
	case DigitEvenOdd:
		switch sel.Parity {
		case DigitParityEven:
			return DigitEven(settlementDigit), nil
		case DigitParityOdd:
			return !DigitEven(settlementDigit), nil
		default:
			return false, fmt.Errorf("invalid parity %q", sel.Parity)
		}
	case DigitMatchesDiffers:
		if sel.Digit == nil {
			return false, fmt.Errorf("digit required")
		}
		match := settlementDigit == *sel.Digit
		switch sel.Mode {
		case DigitModeMatch:
			return match, nil
		case DigitModeDiffer:
			return !match, nil
		default:
			return false, fmt.Errorf("invalid mode %q", sel.Mode)
		}
	case DigitOverUnder:
		if sel.Barrier == nil {
			return false, fmt.Errorf("barrier required")
		}
		switch sel.Side {
		case DigitSideOver:
			return settlementDigit > *sel.Barrier, nil
		case DigitSideUnder:
			return settlementDigit < *sel.Barrier, nil
		default:
			return false, fmt.Errorf("invalid side %q", sel.Side)
		}
	default:
		return false, fmt.Errorf("unknown contract type %q", contractType)
	}
}
