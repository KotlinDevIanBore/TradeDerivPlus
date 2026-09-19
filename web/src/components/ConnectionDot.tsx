import { cn } from '@/lib/cn'
import { useConnectionStatus } from '@/store/market'
import type { ConnectionStatus } from '@/lib/socket'

const tone: Record<ConnectionStatus, string> = {
  idle: 'bg-faint',
  live: 'bg-profit',
  connecting: 'bg-warning',
  reconnecting: 'bg-warning',
  offline: 'bg-faint',
}

const label: Record<ConnectionStatus, string> = {
  idle: 'No instruments streaming',
  live: 'Live prices',
  connecting: 'Connecting to market',
  reconnecting: 'Reconnecting',
  offline: 'Market feed offline',
}

/** The price feed's health, shown in the top bar and beside the chart. */
export function ConnectionDot({
  className,
  withLabel,
  hideWhenIdle,
}: {
  className?: string
  withLabel?: boolean
  /** In the top bar there is nothing worth reporting while idle. */
  hideWhenIdle?: boolean
}) {
  const status = useConnectionStatus()

  if (hideWhenIdle && status === 'idle') return null

  return (
    <span className={cn('inline-flex items-center gap-1.5', className)} title={label[status]}>
      <span
        className={cn(
          'h-2 w-2 rounded-full',
          tone[status],
          status !== 'live' && status !== 'offline' && status !== 'idle' && 'animate-pulse',
        )}
      />
      <span className="sr-only">{label[status]}</span>
      {withLabel ? <span className="text-meta text-muted">{label[status]}</span> : null}
    </span>
  )
}
