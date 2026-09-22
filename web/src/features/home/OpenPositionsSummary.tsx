import { Link } from 'react-router-dom'
import { ChevronRight } from 'lucide-react'
import { DirectionBadge } from '@/components/DirectionBadge'
import { Money } from '@/components/Money'
import { usePanel } from '@/features/shell/usePanel'
import { usePrice } from '@/store/market'
import { pnl } from '@/lib/pnl'
import type { Trade } from '@/types'

function Row({ trade }: { trade: Trade }) {
  const tick = usePrice(trade.symbol_code)
  const live = tick ? pnl(trade.direction, trade.entry_price, tick.price, trade.stake) : null

  return (
    <li className="flex items-center justify-between gap-3 py-2.5">
      <span className="flex min-w-0 items-center gap-2">
        <DirectionBadge direction={trade.direction} size="sm" />
        <span className="truncate text-body">{trade.symbol_code}</span>
      </span>
      {live ? (
        <Money value={live.toString()} signed className="text-body font-semibold" />
      ) : (
        <span className="text-body text-faint tnum">—</span>
      )}
    </li>
  )
}

/**
 * The three most recent open positions with live profit and loss. Hidden
 * entirely when there are none, rather than showing an empty shell.
 */
export function OpenPositionsSummary({ trades }: { trades: Trade[] }) {
  const { href } = usePanel()

  if (trades.length === 0) return null

  return (
    <section className="rounded-card border border-border bg-surface px-5 py-4" aria-label="Open positions">
      <div className="flex items-center justify-between">
        <h2 className="text-card font-semibold">Open positions</h2>
        <Link
          to={href('positions')}
          className="flex items-center gap-0.5 text-meta font-medium text-accent hover:underline"
        >
          View all
          <ChevronRight className="h-3.5 w-3.5" aria-hidden />
        </Link>
      </div>

      <ul className="mt-1 divide-y divide-hairline">
        {trades.slice(0, 3).map((trade) => (
          <Row key={trade.id} trade={trade} />
        ))}
      </ul>
    </section>
  )
}
