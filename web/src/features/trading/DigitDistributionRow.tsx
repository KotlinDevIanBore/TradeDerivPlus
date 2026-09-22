import { cn } from '@/lib/cn'
import { lastDigit } from '@/lib/lastDigit'
import type { Tick } from '@/types'

/**
 * Live frequency of last digits 0–9 over the tick buffer, with the current
 * last digit highlighted. Purely client-side — same ticks the chart already has.
 */
export function DigitDistributionRow({
  ticks,
  pipSize,
  currentPrice,
}: {
  ticks: Tick[]
  pipSize: number
  currentPrice?: string
}) {
  const counts = Array.from({ length: 10 }, () => 0)
  for (const t of ticks) {
    counts[lastDigit(t.price, pipSize)]++
  }
  const total = ticks.length
  const current = currentPrice !== undefined ? lastDigit(currentPrice, pipSize) : null

  return (
    <div
      role="group"
      aria-label="Last digit distribution"
      className="flex justify-between gap-1 px-gutter py-3 lg:px-0"
    >
      {counts.map((n, digit) => {
        const pct = total === 0 ? 0 : (n / total) * 100
        const active = current === digit
        return (
          <div
            key={digit}
            className="flex min-w-0 flex-1 flex-col items-center gap-1"
          >
            <span
              className={cn(
                'flex h-8 w-8 items-center justify-center rounded-full text-body font-semibold transition-colors',
                active
                  ? 'bg-text text-bg ring-2 ring-text ring-offset-2 ring-offset-bg'
                  : 'bg-elevated text-muted',
              )}
              aria-current={active ? 'true' : undefined}
            >
              {digit}
            </span>
            <span className="text-nano text-faint tnum">{pct.toFixed(1)}%</span>
          </div>
        )
      })}
    </div>
  )
}
