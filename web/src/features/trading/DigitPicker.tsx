import { cn } from '@/lib/cn'

/**
 * The 0–9 row used to choose a digit (Matches/Differs) or a barrier
 * (Over/Under). Shared by the manual ticket and the automated one so the two
 * cannot drift apart in a way that makes the same selection look different
 * depending on which is on screen.
 */
export function DigitPicker({
  label,
  value,
  onChange,
}: {
  label: string
  value: number
  onChange: (n: number) => void
}) {
  return (
    <div>
      <p className="text-meta font-medium text-muted">{label}</p>
      <div className="mt-1.5 flex flex-wrap gap-1" role="group" aria-label={label}>
        {Array.from({ length: 10 }, (_, d) => (
          <button
            key={d}
            type="button"
            onClick={() => onChange(d)}
            aria-pressed={value === d}
            className={cn(
              'flex h-9 w-9 items-center justify-center rounded-lg text-body font-semibold transition-colors',
              value === d ? 'bg-accent text-white' : 'bg-elevated text-muted hover:text-text',
            )}
          >
            {d}
          </button>
        ))}
      </div>
    </div>
  )
}
