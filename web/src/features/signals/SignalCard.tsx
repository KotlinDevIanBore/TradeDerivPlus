import { Link } from 'react-router-dom'
import { Bot } from 'lucide-react'
import { ConfidenceBar } from '@/components/ConfidenceBar'
import { DirectionBadge } from '@/components/DirectionBadge'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { cn } from '@/lib/cn'
import { percent, price, relativeTime } from '@/lib/format'
import type { Signal } from '@/types'

/** A signal older than this is stale enough that its entry price is no longer useful. */
const EXPIRY_MINUTES = 30

export function isExpired(signal: Signal): boolean {
  return Date.now() - new Date(signal.created_at).getTime() > EXPIRY_MINUTES * 60_000
}

/**
 * Deep link to the Trade screen with the signal applied. An expired signal
 * passes its levels but not its entry price, so the ticket prices from the
 * live feed and warns the user.
 */
export function signalHref(signal: Signal): string {
  const params = new URLSearchParams({
    symbol: signal.symbol_code,
    direction: signal.direction,
  })
  if (signal.stop_loss) params.set('sl', signal.stop_loss)
  if (signal.take_profit) params.set('tp', signal.take_profit)
  if (isExpired(signal)) params.set('expired', '1')
  return `/trade?${params.toString()}`
}

function Level({ label, value, pipSize }: { label: string; value: string | null; pipSize: number }) {
  return (
    <div>
      <p className="text-meta text-muted">{label}</p>
      <p className="mt-0.5 text-body tnum text-text">{value ? price(value, pipSize) : '—'}</p>
    </div>
  )
}

/**
 * The signal card, used on Home as a single hero card and on the Signals screen
 * in a compact tappable variant.
 */
export function SignalCard({
  signal,
  compact,
  className,
}: {
  signal: Signal
  compact?: boolean
  className?: string
}) {
  const expired = isExpired(signal)

  const body = (
    <>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <DirectionBadge direction={signal.direction} size={compact ? 'sm' : 'md'} />
            <span className="truncate text-card font-semibold">{signal.symbol_name}</span>
          </div>
          <p className="mt-0.5 text-meta text-muted tnum">{signal.symbol_code}</p>
        </div>

        <div className="shrink-0 text-right">
          <p className="text-card font-bold tnum">{percent(signal.confidence, 0)}</p>
          <p className="text-meta text-muted">confidence</p>
        </div>
      </div>

      <ConfidenceBar confidence={signal.confidence} className="mt-3" />

      <div className="mt-4 grid grid-cols-3 gap-3">
        <Level label="Entry" value={signal.entry_price} pipSize={signal.pip_size} />
        <Level label="Stop loss" value={signal.stop_loss} pipSize={signal.pip_size} />
        <Level label="Take profit" value={signal.take_profit} pipSize={signal.pip_size} />
      </div>

      <div className="mt-4 flex items-center justify-between gap-2">
        <span className="text-meta text-muted">
          {expired ? 'Expired' : `Updated ${relativeTime(signal.created_at)}`}
        </span>
        <span className="rounded-md bg-elevated px-1.5 py-0.5 text-nano font-medium uppercase tracking-wider text-muted">
          {signal.strategy.replace('_', '+')}
        </span>
      </div>
    </>
  )

  if (compact) {
    return (
      <Link
        to={signalHref(signal)}
        className={cn(
          'block rounded-card border border-border bg-surface p-4 transition-colors hover:bg-elevated',
          expired && 'opacity-60',
          className,
        )}
      >
        {body}
      </Link>
    )
  }

  return (
    <Card className={className}>
      {body}
      <Button asChild block className="mt-4" disabled={expired}>
        <Link to={signalHref(signal)}>Trade this signal</Link>
      </Button>
    </Card>
  )
}

/** Shown on Home before the bot has published anything. */
export function SignalCardEmpty({ className }: { className?: string }) {
  return (
    <Card className={cn('flex flex-col items-center py-8 text-center', className)}>
      <Bot className="mb-3 h-8 w-8 text-faint" aria-hidden />
      <p className="text-body font-medium">The bot hasn't published a signal yet</p>
      <p className="mt-1 max-w-[34ch] text-meta text-muted">
        It studies the market continuously and posts a setup when one is worth taking.
      </p>
    </Card>
  )
}

export function SignalCardSkeleton({ compact }: { compact?: boolean }) {
  return (
    <div
      className={cn(
        'rounded-card border border-border bg-surface',
        compact ? 'h-[168px] p-4' : 'h-[244px] p-5',
      )}
    >
      <div className="h-full w-full animate-pulse rounded-lg bg-elevated/60" />
    </div>
  )
}
