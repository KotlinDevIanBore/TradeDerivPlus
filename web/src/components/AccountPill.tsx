import { cn } from '@/lib/cn'
import type { Account } from '@/types'

/**
 * Demo and real must be unmistakable at a glance, on every screen. Amber for
 * demo, green for real, and the account number alongside so a user with both
 * always knows which one they are about to spend from.
 */
export function AccountPill({
  account,
  showNumber = true,
  className,
}: {
  account: Pick<Account, 'account_type' | 'account_number'>
  showNumber?: boolean
  className?: string
}) {
  const isDemo = account.account_type === 'demo'

  return (
    <span className={cn('inline-flex items-center gap-1.5', className)}>
      <span
        className={cn(
          'rounded-pill border px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider',
          isDemo ? 'border-demo/50 text-demo' : 'border-real/50 text-real',
        )}
      >
        {account.account_type}
      </span>
      {showNumber ? (
        <span className="text-meta text-muted tnum">{account.account_number}</span>
      ) : null}
    </span>
  )
}
