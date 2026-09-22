import { useMemo, useState } from 'react'
import { Receipt } from 'lucide-react'
import { EmptyState } from '@/components/EmptyState'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/Button'
import { Skeleton } from '@/components/ui/Skeleton'
import { overlay } from '@/features/shell/overlays'
import { useActiveAccount } from '@/features/shell/useActiveAccount'
import { useTransactions, useWallet } from '@/hooks/useQueries'
import { useRelativeTime } from '@/hooks/useRelativeTime'
import { cn } from '@/lib/cn'
import { useSessionStore } from '@/store/session'
import type { Transaction, TxType } from '@/types'
import { BalanceCard } from './BalanceCard'
import { TransactionDetail } from './TransactionDetail'
import { TransactionRow } from './TransactionRow'

const FILTERS: { label: string; types?: TxType[] }[] = [
  { label: 'All' },
  { label: 'Deposits', types: ['deposit'] },
  { label: 'Withdrawals', types: ['withdrawal'] },
  { label: 'Trades', types: ['trade_open', 'trade_close'] },
]

/**
 * The money that has moved: the balance it adds up to, and every deposit,
 * withdrawal and trade behind it.
 *
 * The filter is component state rather than a URL parameter. Unlike the
 * Positions panel's tab, nothing links to a filtered history -- it is a thing
 * you narrow while looking, not a place you send someone.
 */
export function HistoryPanel() {
  const { account, accountId, isDemo } = useActiveAccount()
  const user = useSessionStore((s) => s.user)

  const [filterIndex, setFilterIndex] = useState(0)
  const [detail, setDetail] = useState<Transaction | null>(null)

  const wallet = useWallet(accountId)
  const filter = FILTERS[filterIndex]
  const transactions = useTransactions(accountId, filter.types, 50)
  useRelativeTime()

  const canWithdraw = user?.kyc_status === 'verified'

  // Pending deposits are pinned to the top: they are what a user comes back for.
  const { pending, rest } = useMemo(() => {
    const items = transactions.data?.items ?? []
    return {
      pending: items.filter((t) => t.type === 'deposit' && t.status === 'pending'),
      rest: items.filter((t) => !(t.type === 'deposit' && t.status === 'pending')),
    }
  }, [transactions.data])

  return (
    <div>
      <BalanceCard
        account={account}
        wallet={wallet.data}
        isLoading={wallet.isLoading}
        isError={wallet.isError}
        onRetry={() => wallet.refetch()}
        onDeposit={overlay.deposit}
        onWithdraw={overlay.withdraw}
        canWithdraw={canWithdraw}
        withdrawHint="Verify your identity to withdraw"
      />

      {/* The chips scroll sideways: four of them do not fit across a panel. */}
      <div className="no-scrollbar mt-4 flex gap-2 overflow-x-auto">
        {FILTERS.map((f, i) => (
          <button
            key={f.label}
            type="button"
            onClick={() => setFilterIndex(i)}
            className={cn(
              'shrink-0 rounded-pill border px-3 py-1.5 text-meta font-medium transition-colors',
              i === filterIndex
                ? 'border-accent bg-accent-subtle text-text'
                : 'border-border text-muted hover:text-text',
            )}
          >
            {f.label}
          </button>
        ))}
      </div>

      <section className="mt-3" aria-label="Transactions">
        {transactions.isLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-16 w-full rounded-xl" />
            ))}
          </div>
        ) : transactions.isError ? (
          <ErrorState error={transactions.error} onRetry={() => transactions.refetch()} />
        ) : pending.length === 0 && rest.length === 0 ? (
          <EmptyState
            icon={Receipt}
            title={filterIndex === 0 ? 'No transactions yet' : `No ${filter.label.toLowerCase()} yet`}
            hint={filterIndex === 0 ? 'Fund your account to start trading.' : undefined}
            action={
              filterIndex === 0 && !isDemo ? (
                <Button variant="outline" size="sm" onClick={overlay.deposit}>
                  Deposit
                </Button>
              ) : undefined
            }
          />
        ) : (
          // The rows bring their own side padding, so the list cancels the
          // panel's to sit edge to edge: a row's hover and a pending deposit's
          // amber edge both want the full width.
          <ul className="-mx-5 divide-y divide-hairline lg:-mx-4">
            {pending.map((t) => (
              <li key={t.id}>
                <TransactionRow transaction={t} onSelect={() => setDetail(t)} pinned />
              </li>
            ))}
            {rest.map((t) => (
              <li key={t.id}>
                <TransactionRow transaction={t} onSelect={() => setDetail(t)} />
              </li>
            ))}
          </ul>
        )}
      </section>

      <TransactionDetail transaction={detail} onClose={() => setDetail(null)} />
    </div>
  )
}
