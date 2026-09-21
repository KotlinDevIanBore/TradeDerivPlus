import { Skeleton } from '@/components/ui/Skeleton'
import { cn } from '@/lib/cn'

/**
 * Stands in for the chart while the charting library loads or the first ticks
 * arrive. It sketches the shape that is coming -- a scale on the right and a
 * series across the plot -- so the screen does not flash from a grey slab to a
 * fully drawn chart.
 */
export function ChartSkeleton({ className }: { className?: string }) {
  return (
    <div
      aria-hidden
      className={cn('flex h-[240px] w-full gap-2 pl-2 pt-2 lg:h-[340px]', className)}
    >
      <div className="relative flex min-w-0 flex-1 flex-col justify-between">
        {/* Where the axis ticks will land. */}
        {[0, 1, 2, 3].map((i) => (
          <div key={i} className="h-px w-full bg-hairline" />
        ))}

        <div className="absolute inset-x-0 bottom-0 top-1/3">
          <Skeleton className="h-full w-full rounded-lg opacity-60" />
        </div>
      </div>

      {/* The price scale's labels. */}
      <div className="flex w-16 shrink-0 flex-col justify-between py-0">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-3 w-12 rounded" />
        ))}
      </div>
    </div>
  )
}
