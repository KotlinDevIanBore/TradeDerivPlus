import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { LineChart } from 'lucide-react'
import { ApiError } from '@/api/client'
import { ConnectionDot } from '@/components/ConnectionDot'
import { DirectionBadge } from '@/components/DirectionBadge'
import { EmptyState } from '@/components/EmptyState'
import { Money } from '@/components/Money'
import { PriceTag } from '@/components/PriceTag'
import { Button } from '@/components/ui/Button'
import { Sheet } from '@/components/ui/Sheet'
import { toast } from '@/components/ui/Toast'
import { useActiveAccount } from '@/features/shell/useActiveAccount'
import { usePanel } from '@/features/shell/usePanel'
import { signalHref } from '@/features/signals/SignalCard'
import { useWatchSymbols } from '@/hooks/usePriceFeed'
import {
  useCloseTrade,
  useOpenTrade,
  useSignals,
  useSymbols,
  useTradeSummary,
  useTrades,
  useWallet,
} from '@/hooks/useQueries'
import { useRelativeTime } from '@/hooks/useRelativeTime'
import { cn } from '@/lib/cn'
import { money, percent, price as fmtPrice, relativeTime, signedPercent } from '@/lib/format'
import { dec } from '@/lib/money'
import { pnl } from '@/lib/pnl'
import { mergeParams } from '@/lib/searchParams'
import { useConnectionStatus, usePrice, useTickHistory } from '@/store/market'
import { ChartRangeToggle } from './ChartRangeToggle'
import { windowChange, type ChartRange, type WindowChange } from './chartRange'
import { ChartSkeleton } from './ChartSkeleton'
import { PriceChartLazy } from './PriceChartLazy'
import { OrderTicket, type TicketValues } from './OrderTicket'
import { SessionStrip } from './SessionStrip'
import { sessionStats } from './sessionStats'
import { SymbolPicker } from './SymbolPicker'

const DEFAULT_SYMBOL = 'R_75'
/** A stake this large relative to the balance asks for confirmation on a real account. */
const CONFIRM_THRESHOLD = 0.2

export function TradeScreen() {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const { open: openPanel } = usePanel()
  const { accountId, isDemo } = useActiveAccount()

  const symbolsQuery = useSymbols()
  const wallet = useWallet(accountId)
  const openTrades = useTrades(accountId, 'open', 50)
  const summary = useTradeSummary(accountId)
  // Same query key Portfolio uses, so the two screens share one fetch.
  const closedTrades = useTrades(accountId, 'closed', 50)
  const openTrade = useOpenTrade()
  const closeTrade = useCloseTrade()

  const [range, setRange] = useState<ChartRange>(5)
  const [ticket, setTicket] = useState<TicketValues | null>(null)
  const [pendingConfirm, setPendingConfirm] = useState<TicketValues | null>(null)
  const [waitedForPrice, setWaitedForPrice] = useState(false)

  const symbolCode = params.get('symbol') ?? DEFAULT_SYMBOL
  const symbols = symbolsQuery.data?.items ?? []
  const symbol = symbols.find((s) => s.code === symbolCode) ?? null

  const signals = useSignals(symbolCode, 1)
  const latestSignal = signals.data?.items?.[0]

  // Keep this symbol plus every symbol with an open position streaming, so the
  // positions list under the chart stays live too.
  const streamed = useMemo(() => {
    const codes = new Set<string>([symbolCode])
    for (const t of openTrades.data?.items ?? []) codes.add(t.symbol_code)
    return [...codes]
  }, [symbolCode, openTrades.data])
  useWatchSymbols(streamed)
  useRelativeTime()

  const tick = usePrice(symbolCode)
  const history = useTickHistory(symbolCode)
  const status = useConnectionStatus()
  const stale = status === 'reconnecting' || status === 'offline'

  // Movement across the window the chart is drawing, not a daily change: the
  // server keeps a short rolling tick buffer, so there is no daily open price.
  const change = useMemo(() => windowChange(history, range), [history, range])

  const session = useMemo(
    () => sessionStats(closedTrades.data?.items ?? []),
    [closedTrades.data],
  )

  // After five seconds with no tick, say so rather than spinning forever.
  useEffect(() => {
    setWaitedForPrice(false)
    const id = setTimeout(() => setWaitedForPrice(true), 5000)
    return () => clearTimeout(id)
  }, [symbolCode])

  const prefill = useMemo(() => {
    const direction = params.get('direction')
    if (!direction) return undefined
    return {
      direction: direction === 'sell' ? ('sell' as const) : ('buy' as const),
      stopLoss: params.get('sl'),
      takeProfit: params.get('tp'),
    }
  }, [params])

  const expiredSignal = params.get('expired') === '1'

  const selectSymbol = (code: string) => {
    // Changing symbol drops any signal prefill; those levels were for the old
    // one. Anything else on the URL belongs to somebody else and is kept.
    setParams(
      mergeParams({ symbol: code, direction: null, sl: null, tp: null, expired: null }),
      { replace: true },
    )
  }

  const submit = (values: TicketValues) => {
    if (!accountId || !symbol) return

    const balance = dec(wallet.data?.real_balance).plus(dec(wallet.data?.bonus_balance))
    const share = balance.isZero() ? 0 : dec(values.stake).dividedBy(balance).toNumber()

    // Demo never asks; a large stake on real money does.
    if (!isDemo && share >= CONFIRM_THRESHOLD) {
      setPendingConfirm(values)
      return
    }
    place(values)
  }

  const place = (values: TicketValues) => {
    if (!accountId || !symbol) return
    setPendingConfirm(null)

    openTrade.mutate(
      {
        account_id: accountId,
        symbol: symbol.code,
        direction: values.direction,
        stake: dec(values.stake).toFixed(2),
        stop_loss: values.stopLoss ?? undefined,
        take_profit: values.takeProfit ?? undefined,
      },
      {
        onSuccess: () => {
          // Positions is a panel over this screen now, so the offer is to open
          // it rather than to go somewhere.
          toast.action('Position opened', 'View positions', () => openPanel('positions'), 'success')
        },
      },
    )
  }

  const symbolTrades = (openTrades.data?.items ?? []).filter((t) => t.symbol_code === symbolCode)
  const error = openTrade.error as ApiError | null
  const balance = dec(wallet.data?.real_balance).plus(dec(wallet.data?.bonus_balance))
  const confirmShare = pendingConfirm
    ? dec(pendingConfirm.stake).dividedBy(balance).times(100).toFixed(0)
    : '0'

  return (
    <div className="lg:grid lg:grid-cols-[1fr_380px] lg:gap-6 lg:px-6 lg:py-6">
      <div className="min-w-0">
        <div className="flex items-center justify-between gap-3 px-gutter py-3 lg:px-0">
          <SymbolPicker
            symbols={symbols}
            selected={symbol}
            onSelect={selectSymbol}
            isLoading={symbolsQuery.isLoading}
          />
          <ConnectionDot />
        </div>

        <div className="px-gutter lg:px-0">
          <div className="flex items-end justify-between gap-3">
            <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
              <PriceTag
                value={tick?.price}
                pipSize={symbol?.pip_size ?? 4}
                stale={stale}
                className="text-title font-bold"
              />
              <WindowChangeTag
                change={change}
                pipSize={symbol?.pip_size ?? 4}
                range={range}
                stale={stale}
              />
            </div>
            <ChartRangeToggle range={range} onChange={setRange} />
          </div>
        </div>

        <div className="relative mt-2">
          {history.length === 0 ? (
            waitedForPrice ? (
              <div className="flex h-[240px] items-center justify-center lg:h-[340px]">
                <EmptyState
                  icon={LineChart}
                  title="No price data yet"
                  hint={
                    symbol
                      ? `Nothing has arrived for ${symbol.code} yet. The chart draws itself as soon as the feed sends a tick.`
                      : 'The chart draws itself as soon as the feed sends a tick.'
                  }
                  action={
                    <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
                      Try again
                    </Button>
                  }
                />
              </div>
            ) : (
              <ChartSkeleton />
            )
          ) : (
            <PriceChartLazy
              ticks={history}
              pipSize={symbol?.pip_size ?? 4}
              range={range}
              stopLoss={ticket?.stopLoss}
              takeProfit={ticket?.takeProfit}
              stale={stale}
            />
          )}

          {stale ? (
            <div className="absolute inset-0 flex items-center justify-center bg-bg/40">
              <span className="rounded-pill border border-warning/40 bg-elevated px-3 py-1.5 text-meta text-warning">
                {status === 'offline' ? 'Market feed offline' : 'Reconnecting…'}
              </span>
            </div>
          ) : null}
        </div>

        {latestSignal ? (
          <button
            type="button"
            onClick={() => navigate(signalHref(latestSignal))}
            className="mx-gutter mt-3 flex w-[calc(100%-2*theme(spacing.gutter))] items-center gap-2 rounded-xl border border-border bg-surface px-3 py-2 text-left transition-colors hover:bg-elevated lg:mx-0 lg:w-full"
          >
            <span className="text-meta text-muted">Signal</span>
            <DirectionBadge direction={latestSignal.direction} size="sm" />
            <span className="text-meta tnum">{percent(latestSignal.confidence, 0)}</span>
            <span className="text-meta text-faint">{relativeTime(latestSignal.created_at)}</span>
            <span className="ml-auto text-meta font-semibold text-accent">Apply</span>
          </button>
        ) : null}

        <div className="mt-3 px-gutter lg:px-0">
          <SessionStrip
            summary={summary.data}
            session={session}
            isLoading={summary.isLoading}
            stale={stale}
          />
        </div>

        {symbolTrades.length > 0 ? (
          <section className="mt-4 px-gutter lg:px-0" aria-label="Open positions on this symbol">
            <h2 className="mb-2 text-card font-semibold">
              Your positions on {symbol?.code}
            </h2>
            <ul className="divide-y divide-hairline rounded-card border border-border bg-surface">
              {symbolTrades.map((trade) => {
                const live = tick
                  ? pnl(trade.direction, trade.entry_price, tick.price, trade.stake)
                  : null
                return (
                  <li key={trade.id} className="flex items-center gap-3 px-4 py-3">
                    <DirectionBadge direction={trade.direction} size="sm" />
                    <span className="text-meta text-muted tnum">{money(trade.stake)}</span>
                    {live ? (
                      <Money value={live.toString()} signed className="text-body font-semibold" stale={stale} />
                    ) : (
                      <span className="text-body text-faint">—</span>
                    )}
                    <Button
                      variant="danger"
                      size="sm"
                      className="ml-auto"
                      loading={closeTrade.isPending && closeTrade.variables === trade.id}
                      onClick={() => closeTrade.mutate(trade.id)}
                    >
                      Close
                    </Button>
                  </li>
                )
              })}
            </ul>
          </section>
        ) : null}
      </div>

      {/* Sticky at the bottom on mobile, a right column on desktop. */}
      <div className="sticky bottom-tabbar z-20 mt-4 lg:static lg:mt-0">
        <OrderTicket
          symbol={symbol}
          currentPrice={tick?.price}
          wallet={wallet.data}
          disabled={!tick || stale || !symbol?.is_active}
          disabledReason={
            !symbol?.is_active
              ? 'This market is unavailable right now'
              : stale
                ? 'Waiting for a live price before you can trade'
                : !tick
                  ? 'Connecting to the market…'
                  : undefined
          }
          submitting={openTrade.isPending}
          error={error}
          onSubmit={submit}
          onChange={setTicket}
          initial={prefill}
          expiredSignal={expiredSignal}
          isDemo={isDemo}
        />
      </div>

      <Sheet
        open={pendingConfirm !== null}
        onOpenChange={(open) => !open && setPendingConfirm(null)}
        title="Confirm this trade"
      >
        {pendingConfirm ? (
          <>
            <p className="text-body text-muted">
              {pendingConfirm.direction === 'buy' ? 'Buy' : 'Sell'}{' '}
              <span className="font-semibold text-text">{symbol?.code}</span> for{' '}
              <span className="font-semibold text-text">{money(pendingConfirm.stake)}</span>. That's{' '}
              {confirmShare}% of your balance.
            </p>

            <div className="mt-6 space-y-2">
              <Button
                size="lg"
                block
                variant={pendingConfirm.direction === 'buy' ? 'buy' : 'sell'}
                onClick={() => place(pendingConfirm)}
              >
                Yes, place it
              </Button>
              <Button variant="ghost" block onClick={() => setPendingConfirm(null)}>
                Cancel
              </Button>
            </div>
          </>
        ) : null}
      </Sheet>
    </div>
  )
}

/**
 * Price movement across the visible chart window. The window is named in the
 * label because this is not a daily change and must not be read as one; the
 * sign carries the direction, so colour is never the only signal.
 */
function WindowChangeTag({
  change,
  pipSize,
  range,
  stale,
}: {
  change: WindowChange | null
  pipSize: number
  range: ChartRange
  stale: boolean
}) {
  if (!change) return null

  const flat = change.percent.isZero()
  const down = change.percent.isNegative()
  // fmtPrice carries its own minus sign, so only a rise needs one added.
  const absolute = (down ? '' : '+') + fmtPrice(change.absolute.toString(), pipSize)

  return (
    <span className={cn('flex items-baseline gap-1.5 text-meta tnum', stale && 'opacity-50')}>
      <span className={flat ? 'text-muted' : down ? 'text-loss' : 'text-profit'}>
        {absolute} ({signedPercent(change.percent.toString(), 2)})
      </span>
      <span className="text-faint">past {range}m</span>
    </span>
  )
}
