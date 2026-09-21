import { cn } from '@/lib/cn'
import type { ChartRange } from './chartRange'

export function ChartRangeToggle({
  range,
  onChange,
}: {
  range: ChartRange
  onChange: (range: ChartRange) => void
}) {
  return (
    <div
      className="flex shrink-0 items-center gap-0.5 rounded-pill border border-border bg-surface p-0.5"
      role="group"
      aria-label="Chart range"
    >
      {([1, 5, 15] as ChartRange[]).map((r) => (
        <button
          key={r}
          type="button"
          onClick={() => onChange(r)}
          aria-pressed={range === r}
          className={cn(
            'rounded-pill px-2.5 py-1 text-meta font-medium tabular-nums transition-colors',
            range === r ? 'bg-elevated text-text' : 'text-muted hover:text-text',
          )}
        >
          {r}m
        </button>
      ))}
    </div>
  )
}
