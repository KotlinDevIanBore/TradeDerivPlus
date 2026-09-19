import { cn } from '@/lib/cn'

/**
 * Every skeleton should occupy the same box as the content it stands in for, so
 * the page does not jump when data lands.
 */
export function Skeleton({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      aria-hidden
      className={cn(
        'animate-pulse rounded-lg bg-elevated',
        className,
      )}
      {...props}
    />
  )
}

export function SkeletonText({ className }: { className?: string }) {
  return <Skeleton className={cn('h-4 w-24', className)} />
}
