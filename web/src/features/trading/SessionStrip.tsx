import { Money } from '@/components/Money'
import { Skeleton } from '@/components/ui/Skeleton'
import { cn } from '@/lib/cn'
import type { TradeSummary } from '@/types'
import type { DigitSessionStats, SessionStats } from './sessionStats'

/** What the Auto cell says, decided by the screen that knows about runs. */
export interface AutoCell {
  value: string
  title?: string
  className?: string
}

/**
 * Where the account stands, without leaving the chart. Deliberately four small
 * figures rather than Portfolio's tiles: this sits beside the thing it comments
 * on and must not compete with the chart or the ticket for attention.
 */
export function SessionStrip({
  summary,
  session,
  digitSession,
  auto,
  isLoading,
  stale,
}: {
  summary?: TradeSummary
  session?: SessionStats
  /** When set, show digit-mode metrics instead of CFD Open P/L. */
  digitSession?: DigitSessionStats
  /** The automated run's one-line state, when the feature is on for this user. */
  auto?: AutoCell
  isLoading: boolean
  /** Dim the live figure when the feed is disconnected or a price is missing. */
  stale?: boolean
}) {
  if (isLoading) {
    return <Skeleton className="h-[58px] w-full rounded-card" />
  }

  if (digitSession) {
    return (
      <section
        aria-label="Session activity"
        className="grid grid-cols-2 gap-x-4 gap-y-2 rounded-card border border-border bg-surface px-4 py-3 shadow-card sm:grid-cols-4"
      >
        <Cell label="Session P/L" title="Settled digit P/L since you opened the app">
          <Money
            value={digitSession.realised.toString()}
            signed
            className="text-body font-semibold"
          />
        </Cell>

        <Cell label="Pending">
          <span className="text-body font-semibold tnum">{digitSession.open}</span>
        </Cell>

        <Cell
          label="Session"
          title="Settled since you opened the app, from the most recent page of settled contracts"
          className={auto ? undefined : 'sm:col-span-2'}
        >
          <span className="text-body font-semibold tnum">
            {digitSession.contracts}
            {digitSession.contracts > 0 ? (
              <span className="ml-1.5 text-meta font-medium text-muted">
                {digitSession.wins}W / {digitSession.losses}L
                {digitSession.voids > 0 ? ` / ${digitSession.voids}V` : ''}
              </span>
            ) : null}
          </span>
        </Cell>

        {/* The fourth cell the digit grid already had room for. Present only when
            automated trading is on for this user, so nothing changes for anyone
            it is off for. */}
        {auto ? (
          <Cell label="Auto" title={auto.title}>
            <span className={cn('text-body font-semibold', auto.className)}>{auto.value}</span>
          </Cell>
        ) : null}
      </section>
    )
  }

  const stats = session!
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
          <Money
            value={summary.open_pnl}
            signed
            stale={incomplete}
            className="text-body font-semibold"
          />
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
        <Money value={stats.realised.toString()} signed className="text-body font-semibold" />
      </Cell>

      <Cell
        label="Session trades"
        title="Closed since you opened the app, from the most recent page of closed trades"
      >
        <span className="text-body font-semibold tnum">
          {stats.trades}
          {stats.trades > 0 ? (
            <span className="ml-1.5 text-meta font-medium text-muted">
              {stats.wins}W / {stats.losses}L
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
