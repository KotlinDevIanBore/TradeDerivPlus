package models_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

func TestLastDigit(t *testing.T) {
	cases := []struct {
		price string
		pip   int
		want  int
	}{
		{"9449.94", 2, 4},
		{"4185.8107", 4, 7},
		{"100", 0, 0},
		{"1.0", 1, 0},
		{"1.25", 2, 5},
	}
	for _, tc := range cases {
		got := models.LastDigit(decimal.RequireFromString(tc.price), tc.pip)
		if got != tc.want {
			t.Errorf("LastDigit(%s, %d) = %d, want %d", tc.price, tc.pip, got, tc.want)
		}
	}
}

func TestDigitWinsEvenOdd(t *testing.T) {
	win, err := models.DigitWins(models.DigitEvenOdd, models.DigitSelection{Parity: models.DigitParityEven}, 4)
	if err != nil || !win {
		t.Fatalf("even@4: win=%v err=%v", win, err)
	}
	win, err = models.DigitWins(models.DigitEvenOdd, models.DigitSelection{Parity: models.DigitParityOdd}, 4)
	if err != nil || win {
		t.Fatalf("odd@4: win=%v err=%v", win, err)
	}
}

func TestWinCredit(t *testing.T) {
	// Benchmark: $10 × (1 + 0.9520) = $19.52
	got := models.WinCredit(decimal.NewFromInt(10), decimal.RequireFromString("0.9520"))
	want := decimal.RequireFromString("19.52")
	if !got.Equal(want) {
		t.Fatalf("WinCredit = %s, want %s", got, want)
	}
}
