import { cn } from '@/lib/cn'
import { money, signedMoney } from '@/lib/format'
import { dec } from '@/lib/money'

/**
 * A money value. When `signed` is set it is treated as profit and loss: the
 * sign is always shown and the colour follows it, which is the only place
 * colour carries meaning about an amount.
 */
export function Money({
  value,
  signed,
  className,
  stale,
}: {
  value: string | number | null | undefined
  signed?: boolean
  className?: string
  stale?: boolean
}) {
  const d = dec(value as string)
  const colour = signed
    ? d.isZero()
      ? 'text-muted'
      : d.isNegative()
        ? 'text-loss'
        : 'text-profit'
    : undefined

  return (
    <span className={cn('tnum', colour, stale && 'opacity-50', className)}>
      {signed ? signedMoney(value) : money(value)}
    </span>
  )
}
