package models

import (
	"fmt"
	"testing"

	"github.com/shopspring/decimal"
)

// The expected-return table, asserted against the pricing code rather than
// restated in prose. If someone changes the payout table these numbers change and
// this test is where that shows up — which is the point: the UI quotes these
// figures to users.
//
// Updated when Matches and Differs stopped being hand-picked round numbers and
// came onto the same house rule as Over/Under: Matches moved from −10.00% to
// −4.50% and Differs from −1.00% to −0.50%. See payout-table-status.md §5.
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
		{"Differs", DigitMatchesDiffers, DigitSelection{Mode: DigitModeDiffer, Digit: digit(7)}, "0.9", "-0.00496"},
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
		{"Matches (joint worst)", DigitMatchesDiffers, DigitSelection{Mode: DigitModeMatch, Digit: digit(3)}, "0.1", "-0.045"},
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

// TestSelectionsWithTheSameOddsArePricedTheSame is the property the table lacked.
//
// Matches and Over 8 are the same bet — one digit in ten. Differs and Under 9 are
// the same bet — nine digits in ten. Before the house rule was applied to all
// three families, each pair was priced differently: Matches cost 2.2× more than
// Over 8, and Differs 2× more than Under 9, for identical odds.
//
// Asserted as a general property over every selection rather than as two spot
// checks, so a future table that reintroduces a hand-picked number anywhere is
// caught wherever it is put.
func TestSelectionsWithTheSameOddsArePricedTheSame(t *testing.T) {
	digit := func(n int) *int { return &n }

	type selection struct {
		name string
		typ  string
		sel  DigitSelection
	}
	all := []selection{
		{"Matches", DigitMatchesDiffers, DigitSelection{Mode: DigitModeMatch, Digit: digit(3)}},
		{"Differs", DigitMatchesDiffers, DigitSelection{Mode: DigitModeDiffer, Digit: digit(3)}},
	}
	for b := 0; b <= 9; b++ {
		all = append(all,
			selection{fmt.Sprintf("Over %d", b), DigitOverUnder, DigitSelection{Side: DigitSideOver, Barrier: digit(b)}},
			selection{fmt.Sprintf("Under %d", b), DigitOverUnder, DigitSelection{Side: DigitSideUnder, Barrier: digit(b)}},
		)
	}

	// Even/Odd is deliberately excluded: 0.9520 is the benchmark figure, not a
	// number this rule produced, and it sits 0.0020 above what the rule would give
	// for a five-in-ten selection. That difference is intentional and documented.
	byProb := map[string][]selection{}
	for _, s := range all {
		if _, err := PayoutRatioFor(s.typ, s.sel); err != nil {
			continue // barriers that leave no winning digits
		}
		p, err := WinProbabilityFor(s.typ, s.sel)
		if err != nil {
			t.Fatalf("%s: probability: %v", s.name, err)
		}
		byProb[p.String()] = append(byProb[p.String()], s)
	}

	for prob, group := range byProb {
		if len(group) < 2 {
			continue
		}
		first, err := PayoutRatioFor(group[0].typ, group[0].sel)
		if err != nil {
			t.Fatalf("%s: ratio: %v", group[0].name, err)
		}
		for _, s := range group[1:] {
			got, err := PayoutRatioFor(s.typ, s.sel)
			if err != nil {
				t.Fatalf("%s: ratio: %v", s.name, err)
			}
			if !got.Equal(first) {
				t.Errorf("at win probability %s, %s pays %s but %s pays %s: "+
					"identical odds must not be priced differently",
					prob, group[0].name, first, s.name, got)
			}
		}
	}
}

// TestEveryEdgeFollowsTheHouseRule states the rule as arithmetic rather than as a
// table of remembered numbers: the edge is houseFactor × (1 − p) for everything
// the rule prices.
func TestEveryEdgeFollowsTheHouseRule(t *testing.T) {
	digit := func(n int) *int { return &n }
	one := decimal.NewFromInt(1)
	edgeFactor := one.Sub(houseFactor) // 0.05

	check := func(name, typ string, sel DigitSelection) {
		t.Helper()
		p, err := WinProbabilityFor(typ, sel)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ev, err := ExpectedReturnFor(typ, sel)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := edgeFactor.Mul(one.Sub(p)).Neg()
		// The stored ratio is rounded to 4dp, so allow a thousandth of a percent.
		if ev.Sub(want).Abs().GreaterThan(decimal.RequireFromString("0.0001")) {
			t.Errorf("%s: expected return %s, want about %s (5%% of the losing probability)",
				name, ev, want)
		}
	}

	check("Matches", DigitMatchesDiffers, DigitSelection{Mode: DigitModeMatch, Digit: digit(3)})
	check("Differs", DigitMatchesDiffers, DigitSelection{Mode: DigitModeDiffer, Digit: digit(3)})
	for b := 0; b <= 8; b++ {
		check(fmt.Sprintf("Over %d", b), DigitOverUnder, DigitSelection{Side: DigitSideOver, Barrier: digit(b)})
	}
	for b := 1; b <= 9; b++ {
		check(fmt.Sprintf("Under %d", b), DigitOverUnder, DigitSelection{Side: DigitSideUnder, Barrier: digit(b)})
	}
}
