import { ArrowDownToLine, ArrowUpFromLine, RefreshCw } from 'lucide-react'
import { AccountPill } from '@/components/AccountPill'
import { Money } from '@/components/Money'
import { Button } from '@/components/ui/Button'
import { Skeleton } from '@/components/ui/Skeleton'
import { cn } from '@/lib/cn'
import { money } from '@/lib/format'
import type { Account, Wallet } from '@/types'

/**
 * The hero balance. It is the single biggest thing on Home, and it never
 * depends on anything else loading: the deposit button works even if the
 * balance read failed, because depositing does not require knowing the balance.
 */
export function BalanceCard({
  account,
  wallet,
  isLoading,
  isError,
  onRetry,
  onDeposit,
  onWithdraw,
  canWithdraw,
  withdrawHint,
  className,
}: {
  account: Account | null
  wallet?: Wallet
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  onDeposit: () => void
  onWithdraw: () => void
  canWithdraw: boolean
  withdrawHint?: string
  className?: string
}) {
  const isDemo = account?.account_type === 'demo'

  return (
    <section
      className={cn(
        'rounded-card border bg-surface p-5',
        // Demo gets an amber edge so practice money is never mistaken for real.
        isDemo ? 'border-border border-l-2 border-l-demo' : 'border-border',
        className,
      )}
      aria-label="Balance"
    >
      <div className="flex items-start justify-between gap-3">
        <p className="text-meta text-muted">Total balance</p>
        {account ? <AccountPill account={account} /> : null}
      </div>

      <div className="mt-2" aria-live="polite">
        {isLoading ? (
          <Skeleton className="h-10 w-48" />
        ) : isError ? (
          <div className="flex items-center gap-3">
            <span className="text-hero font-bold tnum text-faint">—</span>
            <button
              type="button"
              onClick={onRetry}
              className="flex items-center gap-1 text-meta font-semibold text-accent hover:underline"
            >
              <RefreshCw className="h-3 w-3" aria-hidden />
              Retry
            </button>
          </div>
        ) : (
          <p className="text-hero font-bold tracking-tight">
            <Money value={wallet?.total} />
          </p>
        )}
      </div>

      {!isLoading && !isError && wallet ? (
        <p className="mt-1.5 text-meta text-muted tnum">
          Real {money(wallet.real_balance)} · Bonus {money(wallet.bonus_balance)}
        </p>
      ) : (
        <div className="mt-1.5 h-4" />
      )}

      <div className="mt-5 flex gap-2">
        <Button block onClick={onDeposit}>
          <ArrowDownToLine className="h-4 w-4" aria-hidden />
          {isDemo ? 'Top up demo' : 'Deposit'}
        </Button>

        {!isDemo ? (
          <Button variant="outline" block onClick={onWithdraw} disabled={!canWithdraw}>
            <ArrowUpFromLine className="h-4 w-4" aria-hidden />
            Withdraw
          </Button>
        ) : null}
      </div>

      {!isDemo && !canWithdraw && withdrawHint ? (
        <p className="mt-2 text-meta text-muted">{withdrawHint}</p>
      ) : null}
    </section>
  )
}
