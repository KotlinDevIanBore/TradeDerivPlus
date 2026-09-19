import { dec, Decimal } from './money'
import type { Direction } from '@/types'

/**
 * The single client-side definition of profit and loss, mirroring
 * internal/models/pnl.go exactly:
 *
 *   buy:  pnl = stake * (current - entry) / entry
 *   sell: pnl = stake * (entry - current) / entry
 *
 * The live number on an open position must agree with the realised number the
 * server writes on close. If this changes, change the Go side in the same
 * commit.
 */
export function pnl(
  direction: Direction,
  entry: string | Decimal,
  current: string | Decimal,
  stake: string | Decimal,
): Decimal {
  const e = dec(entry.toString())
  if (e.isZero()) return new Decimal(0)

  const c = dec(current.toString())
  const delta = direction === 'sell' ? e.minus(c) : c.minus(e)
  return dec(stake.toString()).times(delta).dividedBy(e).toDecimalPlaces(2)
}

/** PnL as a percentage of the stake, for the secondary line on a position. */
export function pnlPercent(
  direction: Direction,
  entry: string | Decimal,
  current: string | Decimal,
  stake: string | Decimal,
): Decimal {
  const s = dec(stake.toString())
  if (s.isZero()) return new Decimal(0)
  return pnl(direction, entry, current, stake).dividedBy(s).times(100).toDecimalPlaces(2)
}

/** Validate a stop loss against entry for a direction. Returns an error message or null. */
export function validateStopLoss(direction: Direction, entry: Decimal, sl: Decimal): string | null {
  if (direction === 'buy' && sl.greaterThanOrEqualTo(entry)) {
    return 'Stop loss must be below the current price for a buy'
  }
  if (direction === 'sell' && sl.lessThanOrEqualTo(entry)) {
    return 'Stop loss must be above the current price for a sell'
  }
  return null
}

/** Validate a take profit against entry for a direction. */
export function validateTakeProfit(direction: Direction, entry: Decimal, tp: Decimal): string | null {
  if (direction === 'buy' && tp.lessThanOrEqualTo(entry)) {
    return 'Take profit must be above the current price for a buy'
  }
  if (direction === 'sell' && tp.greaterThanOrEqualTo(entry)) {
    return 'Take profit must be below the current price for a sell'
  }
  return null
}
