package models

import "github.com/shopspring/decimal"

// PnL is the single definition of profit and loss for a position (spec gap G6).
//
//	buy:  pnl = stake * (current - entry) / entry
//	sell: pnl = stake * (entry - current) / entry
//
// The frontend mirrors this exactly in src/lib/pnl.ts so the live number on an
// open position matches the realised number written on close. If this formula
// changes, change it in both places in the same commit.
func PnL(direction string, entry, current, stake decimal.Decimal) decimal.Decimal {
	if entry.IsZero() {
		return decimal.Zero
	}
	delta := current.Sub(entry)
	if direction == DirectionSell {
		delta = entry.Sub(current)
	}
	return stake.Mul(delta).Div(entry).Round(2)
}

// HitsStopLoss / HitsTakeProfit decide whether an open position should be
// auto-closed at the incoming tick. Used by the trading service's tick watcher.
func HitsStopLoss(direction string, sl, current decimal.Decimal) bool {
	if direction == DirectionBuy {
		return current.LessThanOrEqual(sl)
	}
	return current.GreaterThanOrEqual(sl)
}

func HitsTakeProfit(direction string, tp, current decimal.Decimal) bool {
	if direction == DirectionBuy {
		return current.GreaterThanOrEqual(tp)
	}
	return current.LessThanOrEqual(tp)
}
