package models

import (
	"testing"

	"github.com/shopspring/decimal"
)

// The expected-return table from the AUTO-mode investigation §3.1, asserted
// against the pricing code rather than restated in prose. If someone replaces
// the placeholder payout table, these numbers change and this test is where that
// shows up — which is the point: the UI quotes these figures to users.
func TestExpectedReturnMatchesTheInvestigationTable(t *testing.T) {
	digit := func(n int) *int { return &n }

	cases := []struct {
		name     string
		typ      string
		sel      DigitSelection
		wantProb string
		wantEV   string
	}{
		{"Over 0 (least bad)", DigitOverUnder, DigitSelection{Side: DigitSideOver, Barrier: digit(0)}, "0.9", "-0.00496"},
		{"Under 9", DigitOverUnder, DigitSelection{Side: DigitSideUnder, Barrier: digit(9)}, "0.9", "-0.00496"},
		{"Differs", DigitMatchesDiffers, DigitSelection{Mode: DigitModeDiffer, Digit: digit(7)}, "0.9", "-0.01"},
		{"Over 1", DigitOverUnder, DigitSelection{Side: DigitSideOver, Barrier: digit(1)}, "0.8", "-0.01"},
		// Six winning digits make the fair profit a recurring decimal, so
		// rounding the stored ratio to 4dp moves the return a thousandth off the
		// -2.00% the investigation's table shows at two decimal places. The
		// contract is priced from the rounded ratio, so this is the true figure.
		{"Over 3", DigitOverUnder, DigitSelection{Side: DigitSideOver, Barrier: digit(3)}, "0.6", "-0.02002"},
		{"Even", DigitEvenOdd, DigitSelection{Parity: DigitParityEven}, "0.5", "-0.024"},
		{"Odd", DigitEvenOdd, DigitSelection{Parity: DigitParityOdd}, "0.5", "-0.024"},
		{"Over 4", DigitOverUnder, DigitSelection{Side: DigitSideOver, Barrier: digit(4)}, "0.5", "-0.025"},
		{"Over 8", DigitOverUnder, DigitSelection{Side: DigitSideOver, Barrier: digit(8)}, "0.1", "-0.045"},
		{"Matches (worst)", DigitMatchesDiffers, DigitSelection{Mode: DigitModeMatch, Digit: digit(3)}, "0.1", "-0.1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prob, err := WinProbabilityFor(tc.typ, tc.sel)
			if err != nil {
				t.Fatalf("probability: %v", err)
			}
			if want := decimal.RequireFromString(tc.wantProb); !prob.Equal(want) {
				t.Errorf("win probability %s, want %s", prob, want)
			}

			ev, err := ExpectedReturnFor(tc.typ, tc.sel)
			if err != nil {
				t.Fatalf("expected return: %v", err)
			}
			if want := decimal.RequireFromString(tc.wantEV); !ev.Equal(want) {
				t.Errorf("expected return %s, want %s", ev, want)
			}

			// Whatever the table says, no selection may be worth taking. A
			// positive figure here would mean the house is paying out more than
			// the odds, which is a pricing bug and not a feature.
			if !ev.IsNegative() {
				t.Errorf("expected return %s is not negative", ev)
			}
		})
	}
}

// Every valid selection must price and quote, so the ticket can never render a
// blank where a number belongs.
func TestEverySelectionHasEconomics(t *testing.T) {
	for d := 0; d <= 9; d++ {
		digit := d
		for _, mode := range []string{DigitModeMatch, DigitModeDiffer} {
			sel := DigitSelection{Mode: mode, Digit: &digit}
			if _, err := ExpectedReturnFor(DigitMatchesDiffers, sel); err != nil {
				t.Errorf("%s %d: %v", mode, digit, err)
			}
		}
		for _, side := range []string{DigitSideOver, DigitSideUnder} {
			sel := DigitSelection{Side: side, Barrier: &digit}
			_, priceErr := PayoutRatioFor(DigitOverUnder, sel)
			_, evErr := ExpectedReturnFor(DigitOverUnder, sel)
			// Over 9 and Under 0 leave no winning digit and are refused by the
			// pricing function; the economics must refuse exactly the same set.
			if (priceErr == nil) != (evErr == nil) {
				t.Errorf("%s %d: price err %v but economics err %v", side, digit, priceErr, evErr)
			}
		}
	}
}

func TestWinProbabilityRejectsBadSelections(t *testing.T) {
	nine := 9
	zero := 0
	cases := []struct {
		name string
		typ  string
		sel  DigitSelection
	}{
		{"over 9 has no winning digit", DigitOverUnder, DigitSelection{Side: DigitSideOver, Barrier: &nine}},
		{"under 0 has no winning digit", DigitOverUnder, DigitSelection{Side: DigitSideUnder, Barrier: &zero}},
		{"missing parity", DigitEvenOdd, DigitSelection{}},
		{"missing digit", DigitMatchesDiffers, DigitSelection{Mode: DigitModeMatch}},
		{"unknown type", "roulette", DigitSelection{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := WinProbabilityFor(tc.typ, tc.sel); err == nil {
				t.Error("want an error, got none")
			}
		})
	}
}
