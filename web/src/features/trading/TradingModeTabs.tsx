import { cn } from '@/lib/cn'

export type TradingMode = 'manual' | 'auto'

const TABS: { mode: TradingMode; label: string }[] = [
  { mode: 'manual', label: 'MANUAL' },
  { mode: 'auto', label: 'AUTO' },
]

/**
 * Switches the ticket between placing contracts yourself and letting a run place
 * them. Chart, digit row and contract-type tabs stay put.
 *
 * It sits inside the trading controls rather than in the app's chrome because the
 * thing it switches is the ticket: a run replaces the manual buttons, so the
 * control belongs where those buttons are. The benchmark puts AUTO/MANUAL in the
 * same place for the same reason.
 *
 * The label is AUTO, never "AI". The bot reads a user's settings and places on
 * them; it predicts nothing, and a name implying otherwise would promise
 * intelligence this feature does not have.
 */
export function TradingModeTabs({
  value,
  onChange,
  /**
   * Why MANUAL cannot be chosen right now, if it cannot. Present while a run is
   * live: switching away would suggest the run had stopped when it has not.
   */
  manualDisabledReason,
}: {
  value: TradingMode
  onChange: (mode: TradingMode) => void
  manualDisabledReason?: string
}) {
  return (
    <div className="px-gutter lg:px-0">
      <div role="tablist" aria-label="Trading mode" className="flex gap-1">
        {TABS.map(({ mode, label }) => {
          const selected = value === mode
          const disabled = mode === 'manual' && Boolean(manualDisabledReason)
          return (
            <button
              key={mode}
              type="button"
              role="tab"
              aria-selected={selected}
              disabled={disabled}
              // The reason travels with the control, so it is reachable by anyone
              // wondering why the tab will not take a click.
              title={disabled ? manualDisabledReason : undefined}
              onClick={() => onChange(mode)}
              className={cn(
                'shrink-0 rounded-pill px-3 py-1.5 text-meta font-semibold tracking-wide transition-colors',
                selected
                  ? 'bg-accent-subtle text-accent'
                  : 'text-muted hover:bg-surface hover:text-text',
                disabled && 'cursor-not-allowed opacity-40 hover:bg-transparent hover:text-muted',
              )}
            >
              {label}
            </button>
          )
        })}
      </div>
      {manualDisabledReason ? (
        <p className="mt-1.5 text-nano text-muted">{manualDisabledReason}</p>
      ) : null}
    </div>
  )
}
