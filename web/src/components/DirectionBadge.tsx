import { cn } from '@/lib/cn'
import type { Direction } from '@/types'

/**
 * Direction always carries its word as well as its colour, so it survives a
 * colour-blind reader and a greyscale screenshot.
 */
export function DirectionBadge({
  direction,
  className,
  size = 'md',
}: {
  direction: Direction
  className?: string
  size?: 'sm' | 'md'
}) {
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-md font-bold uppercase tracking-wide',
        direction === 'buy' ? 'bg-profit text-black' : 'bg-accent text-white',
        size === 'sm' ? 'px-1.5 py-0.5 text-nano' : 'px-2 py-1 text-[11px]',
        className,
      )}
    >
      {direction}
    </span>
  )
}
