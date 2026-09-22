import { useEffect, useMemo, useRef, useState } from 'react'
import { ArrowUp, Bot } from 'lucide-react'
import { EmptyState } from '@/components/EmptyState'
import { ErrorState } from '@/components/ErrorState'
import { usePanelScroll } from '@/components/ui/panelScroll'
import { useSignalStream } from '@/hooks/useSignalStream'
import { useSignals, useSymbols } from '@/hooks/useQueries'
import { useRelativeTime } from '@/hooks/useRelativeTime'
import { cn } from '@/lib/cn'
import { SignalCard, SignalCardSkeleton } from './SignalCard'

export function SignalsPanel() {
  const [symbol, setSymbol] = useState<string | undefined>(undefined)
  const [newCount, setNewCount] = useState(0)
  const scrolledRef = useRef(false)

  const symbolsQuery = useSymbols()
  const signals = useSignals(symbol, 30)
  useRelativeTime()

  // The panel scrolls itself, so this watches the panel. Watching the window
  // would mean watching something that never moves, and the pill would never
  // appear.
  const scroller = usePanelScroll()

  // A push while the user is scrolled down becomes a pill rather than a jump.
  useSignalStream(() => {
    if (scrolledRef.current) setNewCount((n) => n + 1)
  })

  useEffect(() => {
    if (!scroller) return

    const onScroll = () => {
      scrolledRef.current = scroller.scrollTop > 200
      if (scroller.scrollTop <= 200) setNewCount(0)
    }
    scroller.addEventListener('scroll', onScroll, { passive: true })
    return () => scroller.removeEventListener('scroll', onScroll)
  }, [scroller])

  // Only offer chips for symbols the bot actually covers.
  const chips = useMemo(() => {
    const codes = new Set((signals.data?.items ?? []).map((s) => s.symbol_code))
    const all = symbolsQuery.data?.items ?? []
    return all.filter((s) => codes.has(s.code) || s.code === symbol)
  }, [signals.data, symbolsQuery.data, symbol])

  return (
    <div>
      <div className="no-scrollbar flex gap-2 overflow-x-auto pb-3">
        <button
          type="button"
          onClick={() => setSymbol(undefined)}
          className={cn(
            'shrink-0 rounded-pill border px-3 py-1.5 text-meta font-medium transition-colors',
            symbol === undefined
              ? 'border-accent bg-accent-subtle text-text'
              : 'border-border text-muted hover:text-text',
          )}
        >
          All
        </button>
        {chips.map((s) => (
          <button
            key={s.code}
            type="button"
            onClick={() => setSymbol(s.code)}
            className={cn(
              'shrink-0 rounded-pill border px-3 py-1.5 text-meta font-medium transition-colors',
              symbol === s.code
                ? 'border-accent bg-accent-subtle text-text'
                : 'border-border text-muted hover:text-text',
            )}
          >
            {s.code}
          </button>
        ))}
      </div>

      {/* The pill sticks to the top of the panel, which is what scrolls. */}
      {newCount > 0 ? (
        <div className="sticky top-2 z-20 flex justify-center">
          <button
            type="button"
            onClick={() => {
              scroller?.scrollTo({ top: 0, behavior: 'smooth' })
              setNewCount(0)
            }}
            className="flex items-center gap-1.5 rounded-pill bg-accent px-3 py-1.5 text-meta font-semibold text-white shadow-lg animate-fade-in"
          >
            <ArrowUp className="h-3.5 w-3.5" aria-hidden />
            {newCount} new signal{newCount > 1 ? 's' : ''}
          </button>
        </div>
      ) : null}

      {/* One column at every width now: the two the page had do not fit in a
          column beside a chart. */}
      <div className="space-y-3 py-2">
        {signals.isLoading ? (
          Array.from({ length: 4 }).map((_, i) => <SignalCardSkeleton key={i} compact />)
        ) : signals.isError ? (
          <div>
            <ErrorState error={signals.error} onRetry={() => signals.refetch()} />
          </div>
        ) : (signals.data?.items.length ?? 0) === 0 ? (
          <div>
            <EmptyState
              icon={Bot}
              title={symbol ? `No signals for ${symbol} yet` : 'No signals yet'}
              hint="The bot posts a setup when the trend and momentum agree."
            />
          </div>
        ) : (
          signals.data!.items.map((signal) => (
            <SignalCard key={signal.id} signal={signal} compact />
          ))
        )}
      </div>
    </div>
  )
}
