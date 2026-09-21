import { ArrowRight } from 'lucide-react'
import { DirectionBadge } from '@/components/DirectionBadge'
import { Money } from '@/components/Money'
import { PriceTag } from '@/components/PriceTag'
import { Button } from '@/components/ui/Button'
import { cn } from '@/lib/cn'
import { money, price as fmtPrice, relativeTime, signedPercent } from '@/lib/format'
import { pnl, pnlPercent } from '@/lib/pnl'
import { usePrice } from '@/store/market'
import type { Trade } from '@/types'

/**
 * An open position with live profit and loss. The PnL is the loudest thing on
 * the card, in colour and with a sign, and it is dimmed with a "stale" tag when
 * the price feed drops rather than silently showing an old number.
 */
export function PositionCard({
  trade,
  stale,
  closing,
  onClose,
  onSelect,
}: {
  trade: Trade
  stale: boolean
  closing: boolean
  onClose: () => void
  onSelect: () => void
}) {
  const tick = usePrice(trade.symbol_code)
  const live = tick ? pnl(trade.direction, trade.entry_price, tick.price, trade.stake) : null
  const livePct = tick ? pnlPercent(trade.direction, trade.entry_price, tick.price, trade.stake) : null
  const priceStale = stale || !tick

  return (
    <div className="relative rounded-card border border-border bg-surface p-4">
      {closing ? (
        <div className="absolute inset-0 z-10 flex items-center justify-center rounded-card bg-surface/70">
          <span className="text-meta text-muted">Closing…</span>
        </div>
      ) : null}

      <button type="button" onClick={onSelect} className="block w-full text-left">
        <div className="flex items-start justify-between gap-3">
          <span className="min-w-0">
            <span className="flex items-center gap-2">
              <DirectionBadge direction={trade.direction} size="sm" />
              <span className="truncate text-card font-semibold">{trade.symbol_code}</span>
            </span>
            <span className="mt-0.5 block text-meta text-muted tnum">
              {money(trade.stake)} stake
            </span>
          </span>

          <span className="shrink-0 text-right">
            {live ? (
              <>
                <Money
                  value={live.toString()}
                  signed
                  stale={priceStale}
                  className="block text-title font-bold"
                />
                <span
                  className={cn(
                    'text-meta tnum',
                    livePct?.isNegative() ? 'text-loss' : 'text-profit',
                    priceStale && 'opacity-50',
                  )}
                >
                  {livePct ? signedPercent(livePct.toString()) : ''}
                </span>
              </>
            ) : (
              <span className="text-title font-bold text-faint">—</span>
            )}
          </span>
        </div>

        <div className="mt-3 flex items-center gap-2 text-meta">
          <span className="tnum text-muted">{fmtPrice(trade.entry_price, trade.pip_size)}</span>
          <ArrowRight className="h-3 w-3 text-faint" aria-hidden />
          <PriceTag value={tick?.price} pipSize={trade.pip_size} stale={priceStale} className="text-muted" />
          {priceStale ? (
            <span className="rounded bg-elevated px-1.5 py-0.5 text-nano uppercase text-warning">
              stale
            </span>
          ) : null}
        </div>

        <div className="mt-3 flex flex-wrap items-center gap-2">
          <Chip label="SL" value={trade.stop_loss} pipSize={trade.pip_size} />
          <Chip label="TP" value={trade.take_profit} pipSize={trade.pip_size} />
          <span className="ml-auto text-meta text-faint">
            Opened {relativeTime(trade.opened_at)}
          </span>
        </div>
      </button>

      <Button variant="danger" size="sm" block className="mt-3" onClick={onClose} disabled={closing}>
        Close position
      </Button>
    </div>
  )
}

function Chip({ label, value, pipSize }: { label: string; value: string | null; pipSize: number }) {
  return (
    <span className="rounded-md bg-elevated px-2 py-0.5 text-meta text-muted tnum">
      {label} {value ? fmtPrice(value, pipSize) : '—'}
    </span>
  )
}

export function ClosedTradeRow({ trade }: { trade: Trade }) {
  return (
    <li className="flex items-center gap-3 px-4 py-3">
      <DirectionBadge direction={trade.direction} size="sm" />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-body">{trade.symbol_code}</span>
        <span className="text-meta text-muted tnum">
          {money(trade.stake)} · {fmtPrice(trade.entry_price, trade.pip_size)} →{' '}
          {trade.exit_price ? fmtPrice(trade.exit_price, trade.pip_size) : '—'}
        </span>
      </span>
      <span className="shrink-0 text-right">
        <Money value={trade.pnl} signed className="block text-body font-semibold" />
        <span className="text-meta text-faint">{relativeTime(trade.closed_at)}</span>
      </span>
    </li>
  )
}
