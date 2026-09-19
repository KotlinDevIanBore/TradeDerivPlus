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
    <div className="flex gap-1" role="group" aria-label="Chart range">
      {([1, 5, 15] as ChartRange[]).map((r) => (
        <button
          key={r}
          type="button"
          onClick={() => onChange(r)}
          className={cn(
            'rounded-md px-2 py-1 text-meta font-medium transition-colors',
            range === r ? 'bg-elevated text-text' : 'text-muted hover:text-text',
          )}
        >
          {r}m
        </button>
      ))}
    </div>
  )
}
