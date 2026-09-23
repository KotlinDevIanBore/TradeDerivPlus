import { dec, type Decimal } from '@/lib/money'
import type { DigitContract, Trade } from '@/types'

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

export interface DigitSessionStats {
  /** Settled contracts in this browser session (won + lost; voids counted separately). */
  contracts: number
  wins: number
  losses: number
  voids: number
  /** Sum of settled pnl since session start (voids are usually 0). */
  realised: Decimal
  open: number
}

/**
 * Same approximate, session-scoped honesty as sessionStats — from the settled
 * page already fetched, not a lifetime aggregate.
 */
export function digitSessionStats(
  settled: DigitContract[],
  openCount: number,
  since = SESSION_STARTED_AT,
): DigitSessionStats {
  const stats: DigitSessionStats = {
    contracts: 0,
    wins: 0,
    losses: 0,
    voids: 0,
    realised: dec(0),
    open: openCount,
  }

  for (const c of settled) {
    if (!c.settled_at) continue
    const at = new Date(c.settled_at).getTime()
    if (Number.isNaN(at) || at < since) continue

    stats.contracts += 1
    if (c.status === 'won') stats.wins += 1
    if (c.status === 'lost') stats.losses += 1
    if (c.status === 'void') stats.voids += 1
    if (c.pnl !== null) stats.realised = stats.realised.plus(dec(c.pnl))
  }

  return stats
}
