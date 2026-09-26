package service_test

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// The theoretical edge is weighted by STAKE, not by contract count. These tests
// pin that, because a count-weighted mean is the plausible wrong answer: it
// reports the average of the rates rather than the rate the house earned.

func cell(contractType string, selection string, n int, stakes, housePnL string) repository.EdgeCell {
	s := decimal.RequireFromString(stakes)
	p := decimal.RequireFromString(housePnL)
	return repository.EdgeCell{
		ContractType:  contractType,
		Selection:     []byte(selection),
		Contracts:     n,
		Stakes:        s,
		HousePnL:      p,
		RealContracts: n,
		RealStakes:    s,
		RealHousePnL:  p,
	}
}

func TestWeightedTheoreticalEdge(t *testing.T) {
	// Even/Odd pays 0.9520, so the customer's expected return is
	// 0.5 x 1.9520 - 1 = -0.024 and the house edge is +0.024.
	// Matches pays 8.5500: 0.1 x 9.5500 - 1 = -0.045, house edge +0.045.
	cells := []repository.EdgeCell{
		cell("even_odd", `{"parity":"even"}`, 10, "100", "2.40"),
		cell("matches_differs", `{"mode":"match","digit":7}`, 30, "300", "13.50"),
	}

	got := service.WeightTheoreticalEdge(cells, service.EdgeAllFunding)

	// (100 x 0.024 + 300 x 0.045) / 400 = (2.4 + 13.5) / 400 = 0.039750
	want := decimal.RequireFromString("0.039750")
	if !got.Theoretical.Equal(want) {
		t.Fatalf("theoretical edge = %s, want %s (stake-weighted, not count-weighted)",
			got.Theoretical, want)
	}
	// The count-weighted answer would be (10 x 0.024 + 30 x 0.045) / 40 = 0.03975
	// too, by coincidence of these numbers — so assert the realised side, where
	// the two differ, to make the weighting observable.
	if wantRealised := decimal.RequireFromString("0.039750"); !got.Realised.Equal(wantRealised) {
		t.Fatalf("realised edge = %s, want %s", got.Realised, wantRealised)
	}
	if got.SampleSize != 40 {
		t.Fatalf("sample size = %d, want 40", got.SampleSize)
	}
	if got.Unpriced != 0 {
		t.Fatalf("unpriced = %d, want 0", got.Unpriced)
	}
	if !got.TooFewToRead {
		t.Fatalf("40 contracts should be flagged too few to read (min %d)", service.FinanceMinEdgeSample)
	}
	if got.MinSample != service.FinanceMinEdgeSample {
		t.Fatalf("min sample = %d, want %d", got.MinSample, service.FinanceMinEdgeSample)
	}
}

// Stake weighting is only observable when the stakes are lopsided against the
// counts. One big contract on the expensive selection, many small ones on the
// cheap one.
func TestWeightedEdgeIsWeightedByStakeNotCount(t *testing.T) {
	cells := []repository.EdgeCell{
		// 100 contracts, $1 each: cheap selection.
		cell("over_under", `{"side":"over","barrier":0}`, 100, "100", "0.50"),
		// 1 contract, $100: expensive selection.
		cell("matches_differs", `{"mode":"match","digit":3}`, 1, "100", "4.50"),
	}

	got := service.WeightTheoreticalEdge(cells, service.EdgeAllFunding)

	// Over 0 is nine digits in ten: 0.9 x 1.1056 - 1 = -0.00496, house +0.00496.
	// Stake-weighted: (100 x 0.00496 + 100 x 0.045) / 200 = 0.024980
	// Count-weighted would be (100 x 0.00496 + 1 x 0.045) / 101 = 0.00540 — very
	// different, which is the point.
	want := decimal.RequireFromString("0.024980")
	if !got.Theoretical.Equal(want) {
		t.Fatalf("theoretical = %s, want %s — looks count-weighted", got.Theoretical, want)
	}
}

func TestWeightedEdgeCountsUnpricedRatherThanDroppingIt(t *testing.T) {
	cells := []repository.EdgeCell{
		cell("even_odd", `{"parity":"even"}`, 10, "100", "2.40"),
		// A selection the pricer rejects: even_odd with no parity.
		cell("even_odd", `{}`, 5, "50", "1.00"),
		// Malformed JSON, which ParseDigitSelection rejects before the pricer.
		cell("even_odd", `{"parity":`, 3, "30", "0.70"),
	}

	got := service.WeightTheoreticalEdge(cells, service.EdgeAllFunding)

	if got.Unpriced != 8 {
		t.Fatalf("unpriced = %d, want 8 (5 rejected by the pricer + 3 malformed)", got.Unpriced)
	}
	if got.SampleSize != 10 {
		t.Fatalf("sample size = %d, want 10 — unpriced cells must not count", got.SampleSize)
	}
	// The denominator is the priced stakes only: 2.40/100 = 0.024.
	if want := decimal.RequireFromString("0.024"); !got.Realised.Equal(want) {
		t.Fatalf("realised = %s, want %s — unpriced stakes leaked into the denominator", got.Realised, want)
	}
}

func TestWeightedEdgeWithNothingToWeigh(t *testing.T) {
	got := service.WeightTheoreticalEdge(nil, service.EdgeAllFunding)
	if !got.Realised.IsZero() || !got.Theoretical.IsZero() {
		t.Fatalf("empty input gave realised %s theoretical %s, want zeros", got.Realised, got.Theoretical)
	}
	if !got.TooFewToRead {
		t.Fatal("empty input must be flagged too few to read")
	}
}

// The real-only picker must be able to differ from the all-funding one: that is
// the whole point of carrying two populations through one query.
func TestWeightedEdgePickersSeeDifferentPopulations(t *testing.T) {
	c := cell("even_odd", `{"parity":"even"}`, 10, "100", "2.40")
	c.RealContracts, c.RealStakes, c.RealHousePnL = 2, decimal.RequireFromString("20"), decimal.RequireFromString("0.96")

	all := service.WeightTheoreticalEdge([]repository.EdgeCell{c}, service.EdgeAllFunding)
	real := service.WeightTheoreticalEdge([]repository.EdgeCell{c}, service.EdgeRealOnly)

	if all.SampleSize != 10 || real.SampleSize != 2 {
		t.Fatalf("sample sizes all=%d real=%d, want 10 and 2", all.SampleSize, real.SampleSize)
	}
	if want := decimal.RequireFromString("0.048"); !real.Realised.Equal(want) {
		t.Fatalf("real realised = %s, want %s (0.96/20)", real.Realised, want)
	}
	// Theoretical is a property of the selection, so it is the same either way.
	if !all.Theoretical.Equal(real.Theoretical) {
		t.Fatalf("theoretical differs by population: %s vs %s", all.Theoretical, real.Theoretical)
	}
}

func TestFinanceRange(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name     string
		wantFrom time.Time
	}{
		{"24h", now.Add(-24 * time.Hour)},
		{"7d", now.AddDate(0, 0, -7)},
		{"30d", now.AddDate(0, 0, -30)},
	} {
		from, to, err := service.FinanceRange(tc.name, now)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !from.Equal(tc.wantFrom) {
			t.Fatalf("%s: from = %s, want %s", tc.name, from, tc.wantFrom)
		}
		if !to.Equal(now) {
			t.Fatalf("%s: to = %s, want %s", tc.name, to, now)
		}
	}

	from, to, err := service.FinanceRange("all", now)
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if !from.IsZero() {
		t.Fatalf("all: from = %s, want the zero time (unbounded)", from)
	}
	if !to.Equal(now) {
		t.Fatalf("all: to = %s, want %s", to, now)
	}

	var ve *service.ValidationError
	if _, _, err := service.FinanceRange("last tuesday", now); !errors.As(err, &ve) || ve.Field != "range" {
		t.Fatalf("unknown range returned %v, want a validation error on field range", err)
	}
}
