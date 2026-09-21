import { dec, type Decimal } from '@/lib/money'
import type { Trade } from '@/types'

/**
 * When this browser session began. A module constant rather than component
 * state, so walking to Portfolio and back does not restart the count; a reload
 * does, which is what "session" means here.
 */
export const SESSION_STARTED_AT = Date.now()

export interface SessionStats {
  trades: number
  wins: number
  losses: number
  /** Realised profit and loss across those trades. */
  realised: Decimal
}

/**
 * Session activity, counted from the closed trades the app has already
 * fetched. This is deliberately approximate: the closed list is one page, so a
 * session that closed more trades than that page holds is undercounted. An
 * exact figure needs an aggregate the API does not expose yet, which is why
 * nothing built on this may be labelled as a lifetime total.
 */
export function sessionStats(closed: Trade[], since = SESSION_STARTED_AT): SessionStats {
  const stats: SessionStats = { trades: 0, wins: 0, losses: 0, realised: dec(0) }

  for (const trade of closed) {
    if (!trade.closed_at) continue
    const at = new Date(trade.closed_at).getTime()
    if (Number.isNaN(at) || at < since) continue

    const pnl = dec(trade.pnl)
    stats.trades += 1
    if (pnl.isPositive() && !pnl.isZero()) stats.wins += 1
    if (pnl.isNegative()) stats.losses += 1
    stats.realised = stats.realised.plus(pnl)
  }

  return stats
}
