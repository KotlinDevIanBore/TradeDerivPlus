import { cn } from '@/lib/cn'

/**
 * One labelled figure. Shared by the dashboard and the finance screen so the two
 * cannot drift into looking like different products.
 */
export function Tile({
  label,
  value,
  sub,
  className,
}: {
  label: string
  value: React.ReactNode
  sub?: string
  className?: string
}) {
  return (
    <div className={cn('rounded-card border border-border bg-surface px-4 py-3', className)}>
      <p className="text-meta text-muted">{label}</p>
      <p className="mt-1 text-title font-bold tnum">{value}</p>
      {sub ? <p className="mt-0.5 text-meta text-faint tnum">{sub}</p> : null}
    </div>
  )
}
