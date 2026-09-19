import { Loader2 } from 'lucide-react'
import { cn } from '@/lib/cn'

export function Spinner({ className, label }: { className?: string; label?: string }) {
  return (
    <span className="inline-flex items-center gap-2" role="status">
      <Loader2 className={cn('h-4 w-4 animate-spin text-muted', className)} aria-hidden />
      {label ? <span className="text-meta text-muted">{label}</span> : <span className="sr-only">Loading</span>}
    </span>
  )
}
