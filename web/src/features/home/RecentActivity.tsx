import { Link } from 'react-router-dom'
import { ChevronRight, Receipt } from 'lucide-react'
import { EmptyState } from '@/components/EmptyState'
import { Button } from '@/components/ui/Button'
import { TransactionRow } from '@/features/wallet/TransactionRow'
import type { Transaction } from '@/types'

export function RecentActivity({
  transactions,
  onDeposit,
}: {
  transactions: Transaction[]
  onDeposit: () => void
}) {
  return (
    <section className="rounded-card border border-border bg-surface py-2" aria-label="Recent activity">
      <div className="flex items-center justify-between px-5 py-2">
        <h2 className="text-card font-semibold">Recent activity</h2>
        {transactions.length > 0 ? (
          <Link
            to="/wallet"
            className="flex items-center gap-0.5 text-meta font-medium text-accent hover:underline"
          >
            See all
            <ChevronRight className="h-3.5 w-3.5" aria-hidden />
          </Link>
        ) : null}
      </div>

      {transactions.length === 0 ? (
        <EmptyState
          icon={Receipt}
          title="No activity yet"
          hint="Your deposits and trades will appear here."
          action={
            <Button variant="outline" size="sm" onClick={onDeposit}>
              Make your first deposit
            </Button>
          }
        />
      ) : (
        <ul className="divide-y divide-hairline">
          {transactions.slice(0, 5).map((t) => (
            <li key={t.id}>
              <TransactionRow transaction={t} />
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
