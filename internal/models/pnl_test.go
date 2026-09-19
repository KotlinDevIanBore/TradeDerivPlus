package models_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestPnL(t *testing.T) {
	tests := []struct {
		name      string
		direction string
		entry     string
		current   string
		stake     string
		want      string
	}{
		{"buy in profit", models.DirectionBuy, "100", "110", "50", "5"},
		{"buy at a loss", models.DirectionBuy, "100", "90", "50", "-5"},
		{"sell in profit", models.DirectionSell, "100", "90", "50", "5"},
		{"sell at a loss", models.DirectionSell, "100", "110", "50", "-5"},
		{"flat price", models.DirectionBuy, "100", "100", "50", "0"},
		{"five decimal forex move", models.DirectionBuy, "1.08540", "1.08640", "1000", "0.92"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := models.PnL(tt.direction, d(tt.entry), d(tt.current), d(tt.stake))
			if !got.Equal(d(tt.want)) {
				t.Fatalf("PnL = %s, want %s", got, tt.want)
			}
		})
	}
}

// A zero entry price must not panic with a division by zero.
func TestPnLZeroEntry(t *testing.T) {
	if got := models.PnL(models.DirectionBuy, decimal.Zero, d("100"), d("10")); !got.IsZero() {
		t.Fatalf("PnL with zero entry = %s, want 0", got)
	}
}

func TestStopLossAndTakeProfitTriggers(t *testing.T) {
	tests := []struct {
		name      string
		direction string
		level     string
		price     string
		sl, tp    bool
	}{
		{"buy stop hit", models.DirectionBuy, "95", "94", true, false},
		{"buy stop not hit", models.DirectionBuy, "95", "96", false, true},
		{"sell stop hit", models.DirectionSell, "105", "106", true, false},
		{"sell stop not hit", models.DirectionSell, "105", "104", false, true},
		{"exactly at level counts as hit", models.DirectionBuy, "95", "95", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := models.HitsStopLoss(tt.direction, d(tt.level), d(tt.price)); got != tt.sl {
				t.Errorf("HitsStopLoss = %v, want %v", got, tt.sl)
			}
			if got := models.HitsTakeProfit(tt.direction, d(tt.level), d(tt.price)); got != tt.tp {
				t.Errorf("HitsTakeProfit = %v, want %v", got, tt.tp)
			}
		})
	}
}
