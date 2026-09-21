import { Money } from '@/components/Money'
import { Skeleton } from '@/components/ui/Skeleton'
import { cn } from '@/lib/cn'
import type { TradeSummary } from '@/types'
import type { SessionStats } from './sessionStats'

/**
 * Where the account stands, without leaving the chart. Deliberately four small
 * figures rather than Portfolio's tiles: this sits beside the thing it comments
 * on and must not compete with the chart or the ticket for attention.
 */
export function SessionStrip({
  summary,
  session,
  isLoading,
  stale,
}: {
  summary?: TradeSummary
  session: SessionStats
  isLoading: boolean
  /** Dim the live figure when the feed is disconnected or a price is missing. */
  stale?: boolean
}) {
  if (isLoading) {
    return <Skeleton className="h-[58px] w-full rounded-card" />
  }

  // An open P&L computed while a position's symbol has no price is incomplete,
  // and the server says so rather than letting the UI guess.
  const incomplete = stale || summary?.prices_complete === false

  return (
    <section
      aria-label="Session activity"
      className="grid grid-cols-2 gap-x-4 gap-y-2 rounded-card border border-border bg-surface px-4 py-3 shadow-card sm:grid-cols-4"
    >
      <Cell label="Open P/L">
        {summary ? (
          <Money value={summary.open_pnl} signed stale={incomplete} className="text-body font-semibold" />
        ) : (
          <Dash />
        )}
      </Cell>

      <Cell label="Open">
        {summary ? (
          <span className="text-body font-semibold tnum">{summary.open_positions}</span>
        ) : (
          <Dash />
        )}
      </Cell>

      <Cell label="Session P/L" title="Realised since you opened the app">
        <Money value={session.realised.toString()} signed className="text-body font-semibold" />
      </Cell>

      <Cell
        label="Session trades"
        title="Closed since you opened the app, from the most recent page of closed trades"
      >
        <span className="text-body font-semibold tnum">
          {session.trades}
          {session.trades > 0 ? (
            <span className="ml-1.5 text-meta font-medium text-muted">
              {session.wins}W / {session.losses}L
            </span>
          ) : null}
        </span>
      </Cell>
    </section>
  )
}

function Cell({
  label,
  title,
  children,
  className,
}: {
  label: string
  title?: string
  children: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn('min-w-0', className)}>
      <p className="text-nano font-medium uppercase tracking-wider text-faint" title={title}>
        {label}
      </p>
      <div className="mt-0.5">{children}</div>
    </div>
  )
}

const Dash = () => <span className="text-body text-faint">—</span>
