import { cn } from '@/lib/cn'
import { dec } from '@/lib/money'

/**
 * Signal confidence. The colour rule is shared by the Home card and the Signals
 * list: red below 50, amber to 70, green above.
 */
export function ConfidenceBar({ confidence, className }: { confidence: string; className?: string }) {
  const value = dec(confidence).toNumber()
  const clamped = Math.max(0, Math.min(100, value))

  const tone =
    clamped > 70 ? 'bg-profit' : clamped >= 50 ? 'bg-warning' : 'bg-accent'

  return (
    <div
      className={cn('h-1 w-full overflow-hidden rounded-full bg-border', className)}
      role="progressbar"
      aria-valuenow={Math.round(clamped)}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-label="Signal confidence"
    >
      <div className={cn('h-full rounded-full transition-all', tone)} style={{ width: `${clamped}%` }} />
    </div>
  )
}
