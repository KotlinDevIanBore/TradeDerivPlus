import { useEffect, useMemo, useState } from 'react'
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
import { useOpenDigit, useOpenDigits, useSymbols, useWallet } from '@/hooks/useQueries'
import { money, signedPercent } from '@/lib/format'
import { mergeParams } from '@/lib/searchParams'
import { useConnectionStatus, usePrice, useTickHistory } from '@/store/market'
import type { DigitContractType } from '@/types'
import { ChartRangeToggle } from './ChartRangeToggle'
import { windowChange, type ChartRange } from './chartRange'
import { ChartSkeleton } from './ChartSkeleton'
import { ContractTypeTabs } from './ContractTypeTabs'
import { DigitDistributionRow } from './DigitDistributionRow'
import { DigitTicket, type DigitTicketSubmit } from './DigitTicket'
import { PriceChartLazy } from './PriceChartLazy'
import { SymbolPicker } from './SymbolPicker'

const DEFAULT_SYMBOL = 'R_75'

/**
 * The digit-contracts trading surface. Mounted when digit_contracts_enabled is
 * on. CFD TradeScreen stays in the tree and returns when the flag is off.
 */
export function DigitTradeScreen() {
  const [params, setParams] = useSearchParams()
  const { accountId } = useActiveAccount()

  const symbolsQuery = useSymbols()
  const wallet = useWallet(accountId)
  const openDigits = useOpenDigits(accountId)
  const openDigit = useOpenDigit()

  const [range, setRange] = useState<ChartRange>(5)
  const [contractType, setContractType] = useState<DigitContractType>('even_odd')
  const [ticketKey, setTicketKey] = useState(0)
  const [waitedForPrice, setWaitedForPrice] = useState(false)

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
          toast.success(
            `${labelSelection(contract.contract_type, contract.selection)} opened`,
            `Pays ${money(contract.potential_payout)} if it wins`,
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
      </div>

      <div className="sticky bottom-tabbar z-20 mt-4 lg:static lg:mt-0">
        <DigitTicket
          key={ticketKey}
          contractType={contractType}
          symbol={symbol}
          wallet={wallet.data}
          disabled={!tick || stale}
          disabledReason={
            !tick ? 'Waiting for a live price…' : stale ? 'Market feed is reconnecting…' : undefined
          }
          submitting={openDigit.isPending}
          error={error}
          onSubmit={submit}
        />
      </div>
    </div>
  )
}

function labelSelection(
  type: DigitContractType,
  selection: { parity?: string; mode?: string; side?: string; digit?: number; barrier?: number },
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
