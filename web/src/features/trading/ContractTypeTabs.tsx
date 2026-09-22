import { cn } from '@/lib/cn'
import type { DigitContractType } from '@/types'

const TABS: { type: DigitContractType; label: string }[] = [
  { type: 'matches_differs', label: 'Matches/Differs' },
  { type: 'even_odd', label: 'Even/Odd' },
  { type: 'over_under', label: 'Over/Under' },
]

/**
 * Switches which selection the digit ticket shows. Chart and digit row stay put.
 */
export function ContractTypeTabs({
  value,
  onChange,
}: {
  value: DigitContractType
  onChange: (type: DigitContractType) => void
}) {
  return (
    <div
      role="tablist"
      aria-label="Contract type"
      className="flex gap-1 overflow-x-auto px-gutter lg:px-0"
    >
      {TABS.map(({ type, label }) => {
        const selected = value === type
        return (
          <button
            key={type}
            type="button"
            role="tab"
            aria-selected={selected}
            onClick={() => onChange(type)}
            className={cn(
              'shrink-0 rounded-pill px-3 py-1.5 text-meta font-semibold transition-colors',
              selected
                ? 'bg-accent-subtle text-accent'
                : 'text-muted hover:bg-surface hover:text-text',
            )}
          >
            {label}
          </button>
        )
      })}
    </div>
  )
}
