import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { LineChart } from 'lucide-react'
import { ApiError } from '@/api/client'
import { ConnectionDot } from '@/components/ConnectionDot'
import { EmptyState } from '@/components/EmptyState'
import { PriceTag } from '@/components/PriceTag'
import { Button } from '@/components/ui/Button'
import { toast } from '@/components/ui/Toast'
import { useActiveAccount } from '@/features/shell/useActiveAccount'
import { useWatchSymbols } from '@/hooks/usePriceFeed'
import {
  useDigitQuote,
  useLiveBotRun,
  useMe,
  useOpenDigit,
  useOpenDigits,
  useBotRuns,
  useStartBotRun,
  useStopBotRun,
  useSymbols,
  useWallet,
} from '@/hooks/useQueries'
import { money, signedPercent } from '@/lib/format'
import { mergeParams } from '@/lib/searchParams'
import { useConnectionStatus, usePrice, useTickHistory } from '@/store/market'
import type { DigitContractType } from '@/types'
import { ChartRangeToggle } from './ChartRangeToggle'
import { windowChange, type ChartRange } from './chartRange'
import { ChartSkeleton } from './ChartSkeleton'
import { ContractTypeTabs } from './ContractTypeTabs'
import { DigitDistributionRow } from './DigitDistributionRow'
import { BotRunCard } from './BotRunCard'
import { BotTicket, type BotTicketConfig, type BotTicketSubmit } from './BotTicket'
import { DigitTicket, type DigitTicketSubmit } from './DigitTicket'
import { PriceChartLazy } from './PriceChartLazy'
import { useIsDesktop } from '@/hooks/useIsDesktop'
import { SessionStrip } from './SessionStrip'
import { digitSessionStats } from './sessionStats'
import { SymbolPicker } from './SymbolPicker'
import { TradingModeTabs, type TradingMode } from './TradingModeTabs'

const DEFAULT_SYMBOL = 'R_75'

/**
 * The digit-contracts trading surface. Mounted when digit_contracts_enabled is
 * on. CFD TradeScreen stays in the tree and returns when the flag is off.
 */
export function DigitTradeScreen() {
  const [params, setParams] = useSearchParams()
  const { accountId, isReal } = useActiveAccount()

  const symbolsQuery = useSymbols()
  const wallet = useWallet(accountId)
  const openDigits = useOpenDigits(accountId)
  const settledDigits = useOpenDigits(accountId, 'settled')
  const openDigit = useOpenDigit()

  const digitSession = useMemo(
    () => digitSessionStats(settledDigits.data?.items ?? [], openDigits.data?.items.length ?? 0),
    [settledDigits.data, openDigits.data],
  )

  const [range, setRange] = useState<ChartRange>(5)
  const [contractType, setContractType] = useState<DigitContractType>('even_odd')
  // On a phone the ticket sits in the page's flow and only its action buttons
  // are pinned, portaled into the bar rendered at the end of this screen. On a
  // desktop the ticket is a card in its own column and keeps its buttons: no
  // target, no portal. See docs/investigation-reports/mobile-trading-screen-layout.md §2.
  const desktop = useIsDesktop()
  const [actionBar, setActionBar] = useState<HTMLDivElement | null>(null)
  const actionsTarget = desktop ? null : actionBar

  const [ticketKey, setTicketKey] = useState(0)
  const [waitedForPrice, setWaitedForPrice] = useState(false)

  const me = useMe()
  const botEnabled = Boolean(me.data?.bot_enabled)
  const { run: liveRun } = useLiveBotRun(botEnabled ? accountId : null)

  // A live run forces AUTO. Leaving MANUAL selectable while one is placing would
  // suggest it had stopped, and it has not — stopping is a deliberate act.
  const requestedMode: TradingMode = params.get('mode') === 'auto' ? 'auto' : 'manual'
  const mode: TradingMode = liveRun ? 'auto' : requestedMode
  const manualLocked = liveRun ? 'Stop the run to trade manually.' : undefined

  // The bot ticket owns its selection and stake and reports them here, so the
  // screen can fetch the quote its economics line needs while the ticket stays a
  // component that can be rendered without a query client.
  const [botConfig, setBotConfig] = useState<BotTicketConfig | null>(null)
  const onBotConfigChange = useCallback((c: BotTicketConfig) => setBotConfig(c), [])
  const botQuote = useDigitQuote(
    botConfig?.contractType ?? contractType,
    botConfig?.selection ?? {},
    botConfig?.stake ?? '',
    mode === 'auto' && Boolean(botConfig),
  )
  const startRun = useStartBotRun()
  const stopRun = useStopBotRun()

  // The last run to finish, so the cell can report how it ended for a while
  // rather than going blank the moment it stops.
  const recentRuns = useBotRuns(botEnabled ? accountId : null, 'stopped', 1)
  const lastStopped = recentRuns.data?.items?.[0] ?? null

  // The run's own most recent settlement, for the card's last-contract line.
  const lastRunContract = useMemo(() => {
    if (!liveRun) return undefined
    return (settledDigits.data?.items ?? []).find((c) => c.bot_run_id === liveRun.id)
  }, [liveRun, settledDigits.data])

  // The Auto cell. Only rendered when the feature is on for this user, so a
  // surface without automated trading is exactly as it was.
  const autoCell = botEnabled
    ? liveRun
      ? {
          value: `${liveRun.status === 'stopping' ? 'Stopping' : 'Running'} · ${liveRun.contracts_placed}${
            liveRun.limits.max_contracts ? `/${liveRun.limits.max_contracts}` : ''
          }`,
          title: 'An automated run is placing contracts',
          className: liveRun.status === 'stopping' ? 'text-warning' : 'text-profit',
        }
      : lastStopped
        ? {
            value: `Stopped · ${stopReasonWords(lastStopped.stop_reason)}`,
            title: 'The most recent automated run',
            className: 'text-muted',
          }
        : { value: '—', title: 'No automated run', className: 'text-faint' }
    : undefined

  const startBotRun = (values: BotTicketSubmit) => {
    if (!accountId || !symbol) return
    startRun.mutate({
      account_id: accountId,
      symbol: symbol.code,
      contract_type: values.contractType,
      selection: values.selection,
      stake: values.stake,
      interval_ms: values.intervalMS,
      limits: values.limits,
    })
  }

  const selectMode = (next: TradingMode) =>
    // Merged rather than replaced, so switching mode does not drop the symbol.
    setParams(mergeParams({ mode: next === 'auto' ? 'auto' : null }))

  const symbolCode = params.get('symbol') ?? DEFAULT_SYMBOL
  const allSymbols = symbolsQuery.data?.items ?? []
  // Digit contracts are volatility-only on the server.
  const symbols = allSymbols.filter((s) => s.category === 'volatility')
  const symbol = symbols.find((s) => s.code === symbolCode) ?? symbols[0] ?? null
  const activeCode = symbol?.code ?? symbolCode

  useWatchSymbols([activeCode])

  const tick = usePrice(activeCode)
  const history = useTickHistory(activeCode)
  const status = useConnectionStatus()
  const stale = status === 'reconnecting' || status === 'offline'

  const change = useMemo(() => windowChange(history, range), [history, range])

  const openOnSymbol = (openDigits.data?.items ?? []).filter((c) => c.symbol_code === activeCode)
  const entryMarker = openOnSymbol[0]?.entry_price

  useEffect(() => {
    setWaitedForPrice(false)
    const id = setTimeout(() => setWaitedForPrice(true), 5000)
    return () => clearTimeout(id)
  }, [activeCode])

  // Keep the URL on a volatility symbol if the user somehow landed on FX.
  useEffect(() => {
    if (!symbol || symbol.code === symbolCode) return
    setParams(mergeParams({ symbol: symbol.code }), { replace: true })
  }, [symbol, symbolCode, setParams])

  const selectSymbol = (code: string) => setParams(mergeParams({ symbol: code }))

  const submit = (values: DigitTicketSubmit) => {
    if (!accountId || !symbol) return
    openDigit.mutate(
      {
        account_id: accountId,
        symbol: symbol.code,
        contract_type: values.contractType,
        selection: values.selection,
        stake: values.stake,
      },
      {
        onSuccess: (contract) => {
          setTicketKey((k) => k + 1)
          // Brief: the settlement toast follows one tick later and says what
          // actually happened, so this one only needs to confirm the tap landed.
          toast.success(
            `${labelSelection(contract.contract_type, contract.selection)} opened`,
            `Pays ${money(contract.potential_payout)} if it wins`,
            { duration: 2500 },
          )
        },
      },
    )
  }

  const error = openDigit.error as ApiError | null

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

        <div className="mb-3 lg:mb-4">
          <ContractTypeTabs value={contractType} onChange={setContractType} />
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
              {change ? (
                <span className="text-meta text-muted tnum">
                  {signedPercent(change.percent.toString(), 2)} past {range}m
                </span>
              ) : null}
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
                  hint="The chart draws itself as soon as the feed sends a tick."
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
              entryPrice={entryMarker}
              stale={stale}
            />
          )}
        </div>

        <DigitDistributionRow
          ticks={history}
          pipSize={symbol?.pip_size ?? 4}
          currentPrice={tick?.price}
        />

        <div className="mt-3 px-gutter lg:px-0">
          <SessionStrip
            digitSession={digitSession}
            auto={autoCell}
            isLoading={openDigits.isLoading && settledDigits.isLoading}
          />
        </div>
      </div>

      {/* In flow, not sticky. It was `sticky bottom-tabbar`: a 295-524px panel
          pinned above the tab bar, painting over a chart that is a fixed 240px,
          which left 0px of price line on a 375x667 phone with Over/Under
          selected. The page scrolls now; the buttons alone are pinned below. */}
      <div className="mt-4 lg:mt-0">
        {botEnabled ? (
          <div className="rounded-t-3xl border-t border-border bg-surface px-4 pt-4 lg:rounded-none lg:border-0 lg:bg-transparent lg:px-0 lg:pt-0">
            <p className="mb-1.5 hidden text-nano font-semibold uppercase tracking-wide text-faint lg:block">
              Trading mode
            </p>
            <TradingModeTabs
              value={mode}
              onChange={selectMode}
              manualDisabledReason={manualLocked}
            />
          </div>
        ) : null}
        {mode === 'auto' && liveRun ? (
          <BotRunCard
            run={liveRun}
            lastContract={lastRunContract}
            stopping={stopRun.isPending}
            onStop={() => stopRun.mutate(liveRun.id)}
            flushTop={botEnabled}
          />
        ) : mode === 'auto' ? (
          <BotTicket
            contractType={contractType}
            symbol={symbol}
            wallet={wallet.data}
            quote={botQuote.data}
            maxStake={me.data?.digit_max_stake}
            isReal={isReal}
            realMoneyEnabled={Boolean(me.data?.bot_real_money_enabled)}
            disabled={!tick || stale}
            disabledReason={
              !tick
                ? 'Waiting for a live price…'
                : stale
                  ? 'Market feed is reconnecting…'
                  : undefined
            }
            submitting={startRun.isPending}
            error={startRun.error as ApiError | null}
            onConfigChange={onBotConfigChange}
            onSubmit={startBotRun}
            flushTop={botEnabled}
            actionsTarget={actionsTarget}
          />
        ) : (
          <DigitTicket
            key={ticketKey}
            flushTop={botEnabled}
            contractType={contractType}
            symbol={symbol}
            wallet={wallet.data}
            disabled={!tick || stale}
            disabledReason={
              !tick
                ? 'Waiting for a live price…'
                : stale
                  ? 'Market feed is reconnecting…'
                  : undefined
            }
            submitting={openDigit.isPending}
            error={error}
            onSubmit={submit}
            actionsTarget={actionsTarget}
          />
        )}
      </div>

      {/* The pinned action bar. Sticky against the tab bar's real height
          (safe-area included), last in the flow so it sticks whenever the page
          extends past the fold and settles into place at the bottom. Hidden
          when nothing is portaled into it -- a live run's card has no button
          to pin -- and never on a desktop, where the column holds everything. */}
      <div
        ref={setActionBar}
        className="sticky bottom-[var(--tabbar-h)] z-20 border-t border-border bg-surface px-gutter py-3 empty:hidden lg:hidden"
      />
    </div>
  )
}

/** A stop reason in the fewest words that still say what happened. */
function stopReasonWords(reason: string | null): string {
  switch (reason) {
    case 'take_profit':
      return 'take profit'
    case 'stop_loss':
      return 'stop loss'
    case 'max_consecutive_losses':
      return 'losing streak'
    case 'max_contracts':
      return 'all contracts'
    case 'max_duration':
      return 'time limit'
    case 'user':
      return 'you stopped it'
    case 'insufficient_funds':
      return 'out of funds'
    case 'account_frozen':
      return 'account frozen'
    case 'feed_down':
      return 'feed down'
    case 'admin_kill':
    case 'disabled':
      return 'turned off'
    case 'restart':
    case 'shutdown':
      return 'service restart'
    default:
      return reason ?? 'unknown'
  }
}

function labelSelection(
  type: DigitContractType,
  selection: {
    parity?: string
    mode?: string
    side?: string
    digit?: number
    barrier?: number
  },
): string {
  switch (type) {
    case 'even_odd':
      return selection.parity === 'even' ? 'Even' : 'Odd'
    case 'matches_differs':
      return `${selection.mode === 'match' ? 'Match' : 'Differ'} ${selection.digit}`
    case 'over_under':
      return `${selection.side === 'over' ? 'Over' : 'Under'} ${selection.barrier}`
  }
}
