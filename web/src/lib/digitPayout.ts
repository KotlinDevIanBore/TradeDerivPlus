import { dec, Decimal } from '@/lib/money'
import type { DigitContractType, DigitSelection } from '@/types'

/**
 * Client mirror of internal/models/digit_payout.go.
 *
 * PLACEHOLDER ratios — not for real money until product confirms. Kept here
 * because Phase 0–1 did not expose a quote API; the ticket needs numbers
 * before submit. Must stay in lockstep with the Go table (see Phase 2 report).
 *
 * payout_ratio means: win credit = stake × (1 + ratio).
 */
const EVEN_ODD = '0.9520'
const MATCH = '8.0000'
const DIFFER = '0.1000'

export function payoutRatioFor(
  contractType: DigitContractType,
  selection: DigitSelection,
): Decimal | null {
  switch (contractType) {
    case 'even_odd':
      if (selection.parity !== 'even' && selection.parity !== 'odd') return null
      return dec(EVEN_ODD)
    case 'matches_differs': {
      const d = selection.digit
      if (d === undefined || d < 0 || d > 9) return null
      if (selection.mode === 'match') return dec(MATCH)
      if (selection.mode === 'differ') return dec(DIFFER)
      return null
    }
    case 'over_under': {
      const b = selection.barrier
      if (b === undefined || b < 0 || b > 9) return null
      if (selection.side !== 'over' && selection.side !== 'under') return null
      const winCount = selection.side === 'over' ? 9 - b : b
      if (winCount <= 0) return null
      // fair = 10/winCount - 1; × 0.95 house; round 4dp — same as Go.
      return dec(10).div(winCount).minus(1).times('0.95').toDecimalPlaces(4)
    }
    default:
      return null
  }
}

/** Total wallet credit on a win: stake × (1 + ratio), 2dp. */
export function winCredit(stake: Decimal, ratio: Decimal): Decimal {
  return stake.times(dec(1).plus(ratio)).toDecimalPlaces(2)
}

export function payoutPercentLabel(ratio: Decimal): string {
  return `${ratio.times(100).toFixed(2)}%`
}
