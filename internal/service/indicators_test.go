package service_test

import (
	"math"
	"testing"

	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

func TestEMATracksTrend(t *testing.T) {
	rising := make([]float64, 40)
	for i := range rising {
		rising[i] = 100 + float64(i)
	}

	ema := service.EMA(rising, 9)
	if len(ema) == 0 {
		t.Fatal("EMA returned nothing for sufficient input")
	}
	// On a rising series the EMA must rise and lag the latest price.
	last := ema[len(ema)-1]
	if last <= ema[0] {
		t.Errorf("EMA did not rise: first %.2f last %.2f", ema[0], last)
	}
	if last >= rising[len(rising)-1] {
		t.Errorf("EMA %.2f should lag the latest price %.2f", last, rising[len(rising)-1])
	}
}

func TestEMAInsufficientData(t *testing.T) {
	if got := service.EMA([]float64{1, 2, 3}, 9); got != nil {
		t.Errorf("EMA with too few samples = %v, want nil", got)
	}
}

func TestRSIBounds(t *testing.T) {
	up := make([]float64, 30)
	for i := range up {
		up[i] = 100 + float64(i)*2
	}
	if r := service.RSI(up, 14); r < 95 {
		t.Errorf("RSI on a pure uptrend = %.2f, want near 100", r)
	}

	down := make([]float64, 30)
	for i := range down {
		down[i] = 200 - float64(i)*2
	}
	if r := service.RSI(down, 14); r > 5 {
		t.Errorf("RSI on a pure downtrend = %.2f, want near 0", r)
	}

	flat := make([]float64, 30)
	for i := range flat {
		flat[i] = 100
	}
	if r := service.RSI(flat, 14); math.Abs(r-50) > 0.01 {
		t.Errorf("RSI on a flat series = %.2f, want 50", r)
	}
}

func TestRSINeutralWithoutEnoughData(t *testing.T) {
	if r := service.RSI([]float64{1, 2}, 14); r != 50 {
		t.Errorf("RSI with too few samples = %.2f, want 50", r)
	}
}

func TestStdDev(t *testing.T) {
	if got := service.StdDev([]float64{2, 4, 4, 4, 5, 5, 7, 9}); math.Abs(got-2.138) > 0.01 {
		t.Errorf("StdDev = %.4f, want about 2.138", got)
	}
	if got := service.StdDev([]float64{5}); got != 0 {
		t.Errorf("StdDev of one sample = %.4f, want 0", got)
	}
}
