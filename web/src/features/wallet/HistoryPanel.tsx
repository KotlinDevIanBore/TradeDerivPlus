import { useMemo, useState } from 'react'
import { History as HistoryIcon, Receipt } from 'lucide-react'
import { EmptyState } from '@/components/EmptyState'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/Button'
import { Skeleton } from '@/components/ui/Skeleton'
import { DigitSettledRow } from '@/features/portfolio/DigitContractRows'
import { overlay } from '@/features/shell/overlays'
import { useActiveAccount } from '@/features/shell/useActiveAccount'
import { useMe, useOpenDigits, useTransactions, useWallet } from '@/hooks/useQueries'
import { useRelativeTime } from '@/hooks/useRelativeTime'
import { cn } from '@/lib/cn'
import { dayLabel } from '@/lib/format'
import { useSessionStore } from '@/store/session'
import type { DigitContract, Transaction, TxType } from '@/types'
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
 * withdrawal and trade behind it — or, when digit contracts are on, settled
 * digit contracts instead of the CFD transaction list.
 */
export function HistoryPanel() {
  const me = useMe()
  if (me.data?.digit_contracts_enabled) {
    return <DigitHistoryPanel />
  }
  return <CfdHistoryPanel />
}

function DigitHistoryPanel() {
  const { account, accountId } = useActiveAccount()
  const user = useSessionStore((s) => s.user)
  const wallet = useWallet(accountId)
  const settled = useOpenDigits(accountId, 'settled')
  useRelativeTime()

  const byDay = useMemo(() => {
    const groups = new Map<string, DigitContract[]>()
    for (const c of settled.data?.items ?? []) {
      const key = dayLabel(c.settled_at ?? c.opened_at)
      groups.set(key, [...(groups.get(key) ?? []), c])
    }
    return [...groups.entries()]
  }, [settled.data])

  // Both gates: the platform's and this user's. The platform's comes first in
  // the hint, because it is the one they cannot do anything about.
  const withdrawalsOn = user?.withdrawals_enabled !== false
  const canWithdraw = withdrawalsOn && user?.kyc_status === 'verified'
  const withdrawHint = !withdrawalsOn
    ? 'Withdrawals are not available yet'
    : 'Verify your identity to withdraw'

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
        withdrawHint={withdrawHint}
      />

      <section className="mt-4" aria-label="Settled digit contracts">
        <h2 className="mb-2 text-meta font-semibold uppercase tracking-wider text-faint">
          Settled contracts
        </h2>
        {settled.isLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-16 w-full rounded-xl" />
            ))}
          </div>
        ) : settled.isError ? (
          <ErrorState error={settled.error} onRetry={() => settled.refetch()} />
        ) : byDay.length === 0 ? (
          <EmptyState
            icon={HistoryIcon}
            title="No settled contracts yet"
            hint="Wins, losses and voids show up here after the next tick."
          />
        ) : (
          <div>
            {byDay.map(([day, contracts]) => (
              <section key={day} className="mb-4">
                <h3 className="sticky top-0 z-10 bg-bg/90 py-2 text-meta font-semibold uppercase tracking-wider text-faint backdrop-blur">
                  {day}
                </h3>
                <ul className="-mx-5 divide-y divide-hairline lg:-mx-4">
                  {contracts.map((c) => (
                    <li key={c.id}>
                      <DigitSettledRow contract={c} />
                    </li>
                  ))}
                </ul>
              </section>
            ))}
          </div>
        )}
      </section>
    </div>
  )
}

function CfdHistoryPanel() {
  const { account, accountId, isDemo } = useActiveAccount()
  const user = useSessionStore((s) => s.user)

  const [filterIndex, setFilterIndex] = useState(0)
  const [detail, setDetail] = useState<Transaction | null>(null)

  const wallet = useWallet(accountId)
  const filter = FILTERS[filterIndex]
  const transactions = useTransactions(accountId, filter.types, 50)
  useRelativeTime()

  // Both gates: the platform's and this user's. The platform's comes first in
  // the hint, because it is the one they cannot do anything about.
  const withdrawalsOn = user?.withdrawals_enabled !== false
  const canWithdraw = withdrawalsOn && user?.kyc_status === 'verified'
  const withdrawHint = !withdrawalsOn
    ? 'Withdrawals are not available yet'
    : 'Verify your identity to withdraw'

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
        withdrawHint={withdrawHint}
      />

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
