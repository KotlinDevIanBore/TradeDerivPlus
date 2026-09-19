import type { LucideIcon } from 'lucide-react'
import { cn } from '@/lib/cn'

export function EmptyState({
  icon: Icon,
  title,
  hint,
  action,
  className,
}: {
  icon?: LucideIcon
  title: string
  hint?: string
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn('flex flex-col items-center justify-center px-6 py-10 text-center', className)}>
      {Icon ? <Icon className="mb-3 h-8 w-8 text-faint" aria-hidden /> : null}
      <p className="text-body font-medium text-text">{title}</p>
      {hint ? <p className="mt-1 max-w-[32ch] text-meta text-muted">{hint}</p> : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  )
}
