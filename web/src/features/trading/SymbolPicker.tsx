import { useMemo, useState } from 'react'
import { ChevronDown, Search, Star } from 'lucide-react'
import { PriceTag } from '@/components/PriceTag'
import { Input } from '@/components/ui/Input'
import { Sheet } from '@/components/ui/Sheet'
import { Skeleton } from '@/components/ui/Skeleton'
import { EmptyState } from '@/components/EmptyState'
import { cn } from '@/lib/cn'
import { categoryLabel } from '@/lib/format'
import { useMarketStore } from '@/store/market'
import type { TradeSymbol } from '@/types'

const FAVOURITES_KEY = 'tdp.favourites'

function readFavourites(): string[] {
  try {
    return JSON.parse(localStorage.getItem(FAVOURITES_KEY) ?? '[]') as string[]
  } catch {
    return []
  }
}

const CATEGORY_ORDER = ['volatility', 'boom_crash', 'forex']

export function SymbolPicker({
  symbols,
  selected,
  onSelect,
  isLoading,
}: {
  symbols: TradeSymbol[]
  selected: TradeSymbol | null
  onSelect: (code: string) => void
  isLoading: boolean
}) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [favourites, setFavourites] = useState<string[]>(readFavourites)
  const prices = useMarketStore((s) => s.prices)

  const toggleFavourite = (code: string) => {
    const next = favourites.includes(code)
      ? favourites.filter((c) => c !== code)
      : [...favourites, code]
    setFavourites(next)
    try {
      localStorage.setItem(FAVOURITES_KEY, JSON.stringify(next))
    } catch {
      // A browser blocking storage should not break the picker.
    }
  }

  const grouped = useMemo(() => {
    const q = query.trim().toLowerCase()
    const matching = symbols.filter(
      (s) => !q || s.code.toLowerCase().includes(q) || s.display_name.toLowerCase().includes(q),
    )

    const groups = new Map<string, TradeSymbol[]>()
    const starred = matching.filter((s) => favourites.includes(s.code))
    if (starred.length) groups.set('favourites', starred)

    for (const category of CATEGORY_ORDER) {
      const items = matching.filter((s) => s.category === category)
      if (items.length) groups.set(category, items)
    }
    return groups
  }, [symbols, query, favourites])

  const totalMatching = [...grouped.values()].reduce((n, g) => n + g.length, 0)

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="flex items-center gap-2 rounded-pill border border-border bg-surface px-3 py-1.5 transition-colors hover:bg-elevated"
      >
        {isLoading ? (
          <Skeleton className="h-4 w-28" />
        ) : (
          <>
            <span className="text-body font-semibold">{selected?.display_name ?? 'Pick a symbol'}</span>
            {selected ? (
              <>
                {/* The code is what the order button and the positions list name
                    a position by, so it belongs in the header too. */}
                <span className="text-meta text-muted tnum">{selected.code}</span>
                <span className="rounded bg-elevated px-1.5 py-0.5 text-nano uppercase tracking-wider text-muted">
                  {selected.category.replace('_', ' ')}
                </span>
              </>
            ) : null}
          </>
        )}
        <ChevronDown className="h-4 w-4 text-muted" aria-hidden />
      </button>

      <Sheet open={open} onOpenChange={setOpen} title="Choose an instrument" full>
        <Input
          placeholder="Search instruments"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          prefix=""
          trailing={<Search className="h-4 w-4 text-faint" aria-hidden />}
          autoFocus
        />

        {totalMatching === 0 ? (
          <EmptyState title="No symbols match" hint="Try a different name or code." className="py-12" />
        ) : (
          <div className="mt-4 space-y-5 pb-4">
            {[...grouped.entries()].map(([category, items]) => (
              <section key={category}>
                <h3 className="mb-1.5 text-meta font-semibold uppercase tracking-wider text-faint">
                  {category === 'favourites' ? 'Favourites' : categoryLabel(category)}
                </h3>
                <ul className="space-y-1">
                  {items.map((symbol) => {
                    const tick = prices[symbol.code]
                    const isSelected = symbol.code === selected?.code
                    const starred = favourites.includes(symbol.code)

                    return (
                      <li key={`${category}-${symbol.code}`}>
                        <div
                          className={cn(
                            'flex items-center gap-2 rounded-xl border px-3 py-2.5 transition-colors',
                            isSelected ? 'border-accent bg-accent-subtle' : 'border-transparent bg-surface',
                            !symbol.is_active && 'opacity-50',
                          )}
                        >
                          <button
                            type="button"
                            onClick={() => toggleFavourite(symbol.code)}
                            className="shrink-0 p-1 text-faint transition-colors hover:text-warning"
                            aria-label={starred ? 'Remove from favourites' : 'Add to favourites'}
                          >
                            <Star className={cn('h-4 w-4', starred && 'fill-warning text-warning')} />
                          </button>

                          <button
                            type="button"
                            disabled={!symbol.is_active}
                            onClick={() => {
                              onSelect(symbol.code)
                              setOpen(false)
                            }}
                            className="flex min-w-0 flex-1 items-center justify-between gap-3 text-left disabled:cursor-not-allowed"
                          >
                            <span className="min-w-0">
                              <span className="block truncate text-body">{symbol.display_name}</span>
                              <span className="text-meta text-muted tnum">{symbol.code}</span>
                            </span>

                            {symbol.is_active ? (
                              <PriceTag
                                value={tick?.price ?? symbol.price}
                                pipSize={symbol.pip_size}
                                className="shrink-0 text-body"
                              />
                            ) : (
                              <span className="shrink-0 text-meta text-faint">Unavailable</span>
                            )}
                          </button>
                        </div>
                      </li>
                    )
                  })}
                </ul>
              </section>
            ))}
          </div>
        )}
      </Sheet>
    </>
  )
}
