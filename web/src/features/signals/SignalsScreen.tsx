import { useEffect, useMemo, useRef, useState } from 'react'
import { ArrowUp, Bot } from 'lucide-react'
import { EmptyState } from '@/components/EmptyState'
import { ErrorState } from '@/components/ErrorState'
import { PageHeader } from '@/features/shell/PageHeader'
import { useSignalStream } from '@/hooks/useSignalStream'
import { useSignals, useSymbols } from '@/hooks/useQueries'
import { useRelativeTime } from '@/hooks/useRelativeTime'
import { cn } from '@/lib/cn'
import { SignalCard, SignalCardSkeleton } from './SignalCard'

export function SignalsScreen() {
  const [symbol, setSymbol] = useState<string | undefined>(undefined)
  const [newCount, setNewCount] = useState(0)
  const scrolledRef = useRef(false)

  const symbolsQuery = useSymbols()
  const signals = useSignals(symbol, 30)
  useRelativeTime()

  // A push while the user is scrolled down becomes a pill rather than a jump.
  useSignalStream(() => {
    if (scrolledRef.current) setNewCount((n) => n + 1)
  })

  useEffect(() => {
    const onScroll = () => {
      scrolledRef.current = window.scrollY > 200
      if (window.scrollY <= 200) setNewCount(0)
    }
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  // Only offer chips for symbols the bot actually covers.
  const chips = useMemo(() => {
    const codes = new Set((signals.data?.items ?? []).map((s) => s.symbol_code))
    const all = symbolsQuery.data?.items ?? []
    return all.filter((s) => codes.has(s.code) || s.code === symbol)
  }, [signals.data, symbolsQuery.data, symbol])

  return (
    <div>
      <PageHeader title="Signals" />

      <div className="no-scrollbar flex gap-2 overflow-x-auto px-gutter pb-3 lg:px-6">
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

      {newCount > 0 ? (
        <div className="sticky top-16 z-20 flex justify-center px-gutter">
          <button
            type="button"
            onClick={() => {
              window.scrollTo({ top: 0, behavior: 'smooth' })
              setNewCount(0)
            }}
            className="flex items-center gap-1.5 rounded-pill bg-accent px-3 py-1.5 text-meta font-semibold text-white shadow-lg animate-fade-in"
          >
            <ArrowUp className="h-3.5 w-3.5" aria-hidden />
            {newCount} new signal{newCount > 1 ? 's' : ''}
          </button>
        </div>
      ) : null}

      <div className="space-y-3 px-gutter py-2 lg:grid lg:grid-cols-2 lg:gap-3 lg:space-y-0 lg:px-6">
        {signals.isLoading ? (
          Array.from({ length: 4 }).map((_, i) => <SignalCardSkeleton key={i} compact />)
        ) : signals.isError ? (
          <div className="lg:col-span-2">
            <ErrorState error={signals.error} onRetry={() => signals.refetch()} />
          </div>
        ) : (signals.data?.items.length ?? 0) === 0 ? (
          <div className="lg:col-span-2">
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
