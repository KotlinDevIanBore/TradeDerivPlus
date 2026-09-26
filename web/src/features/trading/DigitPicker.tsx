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
      {/* A ten-column grid, not a wrapping flex row. Ten fixed 36px buttons plus
          gaps needed 396px, and the ticket's content box is 343px on a 375px
          phone and 380px on a 412px one -- so "8 9" wrapped onto a second row on
          every phone, not only the narrow ones. The grid divides whatever width
          there is: ~31px a button at 375, ~34px at 412, 36px+ on a desktop card.
          Height stays 36px for the tap target. */}
      <div className="mt-1.5 grid grid-cols-10 gap-1" role="group" aria-label={label}>
        {Array.from({ length: 10 }, (_, d) => (
          <button
            key={d}
            type="button"
            onClick={() => onChange(d)}
            aria-pressed={value === d}
            className={cn(
              'flex h-9 items-center justify-center rounded-lg text-body font-semibold transition-colors',
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
