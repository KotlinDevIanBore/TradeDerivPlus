import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Briefcase, History } from 'lucide-react'
import { EmptyState } from '@/components/EmptyState'
import { ErrorState } from '@/components/ErrorState'
import { Money } from '@/components/Money'
import { Button } from '@/components/ui/Button'
import { Sheet } from '@/components/ui/Sheet'
import { Skeleton } from '@/components/ui/Skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/Tabs'
import { toast } from '@/components/ui/Toast'
import { useActiveAccount } from '@/features/shell/useActiveAccount'
import { usePanel } from '@/features/shell/usePanel'
import { useWatchSymbols } from '@/hooks/usePriceFeed'
import { useCloseTrade, useTradeSummary, useTrades } from '@/hooks/useQueries'
import { useRelativeTime } from '@/hooks/useRelativeTime'
import { dayLabel, signedMoney } from '@/lib/format'
import { pnl } from '@/lib/pnl'
import { mergeParams } from '@/lib/searchParams'
import { useConnectionStatus, usePrice } from '@/store/market'
import type { Trade } from '@/types'
import { ClosedTradeRow, PositionCard } from './PositionCard'

/**
 * Three figures across the top on a phone, where the panel is the whole
 * screen, and three rows down a desktop panel, where 300px of column cannot
 * hold three money values side by side without truncating one of them.
 */
function SummaryTile({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <div className="flex-1 rounded-card border border-border bg-surface px-4 py-3 lg:flex lg:items-baseline lg:justify-between lg:gap-3 lg:py-2">
      <p className="text-meta text-muted">{label}</p>
      <p className="mt-1 text-card font-bold lg:mt-0">{children}</p>
    </div>
  )
}

/** Confirmation restates the position and its estimated result before closing. */
function CloseConfirm({
  trade,
  onConfirm,
  onCancel,
}: {
  trade: Trade | null
  onConfirm: () => void
  onCancel: () => void
}) {
  const tick = usePrice(trade?.symbol_code)
  if (!trade) return null

  const estimate = tick ? pnl(trade.direction, trade.entry_price, tick.price, trade.stake) : null

  return (
    <Sheet open onOpenChange={(open) => !open && onCancel()} title="Close position">
      <p className="text-body text-muted">
        Close <span className="font-semibold text-text">{trade.symbol_code}</span>{' '}
        {trade.direction.toUpperCase()}
        {tick ? (
          <>
            {' '}at <span className="tnum text-text">{tick.price}</span>
          </>
        ) : null}
        ?
      </p>

      {estimate ? (
        <p className="mt-2 text-body text-muted">
          Estimated result{' '}
          <span className={estimate.isNegative() ? 'text-loss' : 'text-profit'}>
            {signedMoney(estimate.toString())}
          </span>
          .
        </p>
      ) : null}

      <div className="mt-6 space-y-2">
        <Button size="lg" block variant="danger" onClick={onConfirm}>
          Close position
        </Button>
        <Button variant="ghost" block onClick={onCancel}>
          Keep it open
        </Button>
      </div>
    </Sheet>
  )
}

/**
 * Everything the Portfolio screen used to be, inside the panel that replaced
 * it. It supplies no padding and no title of its own: the panel around it owns
 * both, and owns them differently at each width.
 */
export function PositionsPanel() {
  const [params, setParams] = useSearchParams()
  const { close } = usePanel()
  const { accountId } = useActiveAccount()
  const tab = params.get('tab') === 'closed' ? 'closed' : 'open'

  const [confirming, setConfirming] = useState<Trade | null>(null)

  const summary = useTradeSummary(accountId)
  const openTrades = useTrades(accountId, 'open', 50)
  const closedTrades = useTrades(accountId, 'closed', 50)
  const closeTrade = useCloseTrade()

  const status = useConnectionStatus()
  const stale = status === 'reconnecting' || status === 'offline'

  const symbols = useMemo(
    () => [...new Set((openTrades.data?.items ?? []).map((t) => t.symbol_code))],
    [openTrades.data],
  )
  useWatchSymbols(symbols)
  useRelativeTime()

  // Group closed trades under sticky day headings.
  const closedByDay = useMemo(() => {
    const groups = new Map<string, Trade[]>()
    for (const trade of closedTrades.data?.items ?? []) {
      const key = dayLabel(trade.closed_at ?? trade.opened_at)
      groups.set(key, [...(groups.get(key) ?? []), trade])
    }
    return [...groups.entries()]
  }, [closedTrades.data])

  const confirmClose = () => {
    if (!confirming) return
    const trade = confirming
    setConfirming(null)

    closeTrade.mutate(trade.id, {
      onSuccess: (closed) => {
        toast.success(
          'Position closed',
          `${closed.symbol_code} realised ${signedMoney(closed.pnl)}`,
        )
      },
      onError: () => toast.error("Couldn't close the position", 'Try again in a moment.'),
    })
  }

  return (
    <div>
      <div className="flex gap-2 pb-3 lg:flex-col lg:gap-1.5">
        {summary.isLoading ? (
          <>
            <Skeleton className="h-[68px] flex-1 rounded-card" />
            <Skeleton className="h-[68px] flex-1 rounded-card" />
            <Skeleton className="h-[68px] flex-1 rounded-card" />
          </>
        ) : summary.isError ? (
          // Three dashes cannot tell a failed load from a flat day, so say which.
          <div className="flex-1 rounded-card border border-border bg-surface">
            <ErrorState error={summary.error} onRetry={() => summary.refetch()} compact />
          </div>
        ) : (
          <>
            <SummaryTile label="Open P&L">
              {summary.data ? (
                <Money
                  value={summary.data.open_pnl}
                  signed
                  stale={stale || !summary.data.prices_complete}
                />
              ) : (
                '—'
              )}
            </SummaryTile>
            <SummaryTile label="Today">
              {summary.data ? <Money value={summary.data.realised_today} signed /> : '—'}
            </SummaryTile>
            <SummaryTile label="Open">
              <span className="tnum">{summary.data?.open_positions ?? '—'}</span>
            </SummaryTile>
          </>
        )}
      </div>

      <Tabs
        value={tab}
        onValueChange={(v) => setParams(mergeParams({ tab: v }), { replace: true })}
      >
        <TabsList>
          <TabsTrigger value="open">Open</TabsTrigger>
          <TabsTrigger value="closed">Closed</TabsTrigger>
        </TabsList>

        <TabsContent value="open" className="space-y-3 py-4">
          {openTrades.isLoading ? (
            <>
              <Skeleton className="h-[180px] w-full rounded-card" />
              <Skeleton className="h-[180px] w-full rounded-card" />
            </>
          ) : openTrades.isError ? (
            <ErrorState error={openTrades.error} onRetry={() => openTrades.refetch()} />
          ) : (openTrades.data?.items.length ?? 0) === 0 ? (
            <EmptyState
              icon={Briefcase}
              title="No open positions"
              hint="Open one from the chart to see it here."
              action={
                // The chart is behind this panel now, not somewhere to go.
                <Button variant="outline" size="sm" onClick={close}>
                  Close
                </Button>
              }
            />
          ) : (
            openTrades.data!.items.map((trade) => (
              <PositionCard
                key={trade.id}
                trade={trade}
                stale={stale}
                closing={closeTrade.isPending && closeTrade.variables === trade.id}
                onClose={() => setConfirming(trade)}
                onSelect={() => setConfirming(trade)}
              />
            ))
          )}
        </TabsContent>

        <TabsContent value="closed" className="py-4">
          {closedTrades.isLoading ? (
            <div className="space-y-2">
              <Skeleton className="h-16 w-full rounded-card" />
              <Skeleton className="h-16 w-full rounded-card" />
            </div>
          ) : closedTrades.isError ? (
            <ErrorState error={closedTrades.error} onRetry={() => closedTrades.refetch()} />
          ) : closedByDay.length === 0 ? (
            <EmptyState icon={History} title="No closed trades yet" hint="Your history will build up here." />
          ) : (
            <div>
              {closedByDay.map(([day, trades]) => (
                <section key={day} className="mb-4">
                  {/* The panel scrolls itself, so a day heading sticks to the
                      top of the panel rather than to the top bar's height. */}
                  <h3 className="sticky top-0 z-10 bg-bg/90 py-2 text-meta font-semibold uppercase tracking-wider text-faint backdrop-blur">
                    {day}
                  </h3>
                  <ul className="divide-y divide-hairline rounded-card border border-border bg-surface">
                    {trades.map((trade) => (
                      <ClosedTradeRow key={trade.id} trade={trade} />
                    ))}
                  </ul>
                </section>
              ))}
            </div>
          )}
        </TabsContent>
      </Tabs>

      <CloseConfirm
        trade={confirming}
        onConfirm={confirmClose}
        onCancel={() => setConfirming(null)}
      />
    </div>
  )
}
