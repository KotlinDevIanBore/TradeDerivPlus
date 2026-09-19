import { useEffect, useRef, useState } from 'react'
import { cn } from '@/lib/cn'
import { price as fmtPrice } from '@/lib/format'
import { dec } from '@/lib/money'

type Flash = 'up' | 'down' | null

/**
 * A live price. The value flashes green or red for 300ms on a change and then
 * settles; nothing about the layout moves, because the digits are tabular and
 * the precision is fixed by the instrument's pip size.
 */
export function PriceTag({
  value,
  pipSize = 4,
  className,
  stale,
}: {
  value: string | undefined
  pipSize?: number
  className?: string
  /** Dim the value when the feed is disconnected. */
  stale?: boolean
}) {
  const [flash, setFlash] = useState<Flash>(null)
  const previous = useRef<string | undefined>(undefined)

  useEffect(() => {
    if (value === undefined) return
    const before = previous.current
    previous.current = value
    if (before === undefined || before === value) return

    setFlash(dec(value).greaterThan(dec(before)) ? 'up' : 'down')
    const id = setTimeout(() => setFlash(null), 300)
    return () => clearTimeout(id)
  }, [value])

  if (value === undefined) {
    return <span className={cn('tnum text-faint', className)}>—</span>
  }

  return (
    <span
      className={cn(
        'tnum tabular-nums transition-opacity',
        flash === 'up' && 'animate-flash-up',
        flash === 'down' && 'animate-flash-down',
        stale && 'opacity-50',
        className,
      )}
    >
      {fmtPrice(value, pipSize)}
    </span>
  )
}
