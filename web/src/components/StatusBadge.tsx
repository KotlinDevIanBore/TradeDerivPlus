import { cn } from '@/lib/cn'
import type { TxStatus } from '@/types'

const styles: Record<string, string> = {
  pending: 'border-warning/40 text-warning',
  completed: 'border-profit/40 text-profit',
  failed: 'border-accent/40 text-accent',
  open: 'border-border text-muted',
  closed: 'border-border text-faint',
  // Automated runs. 'stopping' is the one worth a colour: it means a stop was
  // asked for and a contract is still in flight, which is neither state.
  running: 'border-profit/40 text-profit',
  stopping: 'border-warning/40 text-warning',
  stopped: 'border-border text-faint',
}

const labels: Record<string, string> = {
  pending: 'Pending',
  completed: 'Done',
  failed: 'Failed',
  open: 'Open',
  closed: 'Closed',
  running: 'Running',
  stopping: 'Stopping',
  stopped: 'Stopped',
}

export function StatusBadge({ status, className }: { status: TxStatus | string; className?: string }) {
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-pill border px-2 py-0.5 text-[11px] font-medium',
        styles[status] ?? 'border-border text-muted',
        className,
      )}
    >
      {labels[status] ?? status}
    </span>
  )
}
