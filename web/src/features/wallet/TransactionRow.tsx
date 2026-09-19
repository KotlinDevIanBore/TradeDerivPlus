import {
  ArrowDownLeft,
  ArrowUpRight,
  Gift,
  SlidersHorizontal,
  TrendingDown,
  TrendingUp,
  type LucideIcon,
} from 'lucide-react'
import { Money } from '@/components/Money'
import { StatusBadge } from '@/components/StatusBadge'
import { cn } from '@/lib/cn'
import { relativeTime, transactionTitle } from '@/lib/format'
import { dec } from '@/lib/money'
import type { Transaction } from '@/types'

const icons: Record<string, LucideIcon> = {
  deposit: ArrowDownLeft,
  withdrawal: ArrowUpRight,
  trade_open: TrendingUp,
  trade_close: TrendingDown,
  bonus_credit: Gift,
  admin_adjustment: SlidersHorizontal,
}

const iconTone: Record<string, string> = {
  deposit: 'text-profit',
  withdrawal: 'text-muted',
  trade_open: 'text-muted',
  trade_close: 'text-muted',
  bonus_credit: 'text-warning',
  admin_adjustment: 'text-warning',
}

export function TransactionRow({
  transaction,
  onSelect,
  pinned,
}: {
  transaction: Transaction
  onSelect?: () => void
  /** Pending deposits are pinned to the top of the ledger with an amber edge. */
  pinned?: boolean
}) {
  const Icon = icons[transaction.type] ?? ArrowDownLeft
  const amount = dec(transaction.amount)
  const isPendingDeposit = transaction.type === 'deposit' && transaction.status === 'pending'

  return (
    <button
      type="button"
      onClick={onSelect}
      disabled={!onSelect}
      className={cn(
        'flex w-full items-center gap-3 px-gutter py-3 text-left transition-colors lg:px-4',
        onSelect && 'hover:bg-surface',
        pinned && 'border-l-2 border-l-warning bg-warning/[0.04]',
      )}
    >
      <span
        className={cn(
          'flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-elevated',
          iconTone[transaction.type],
        )}
      >
        <Icon className="h-4 w-4" aria-hidden />
      </span>

      <span className="min-w-0 flex-1">
        <span className="block truncate text-body text-text">
          {transactionTitle(transaction.type, transaction.metadata)}
        </span>
        <span className="mt-0.5 flex items-center gap-2">
          <span className="text-meta text-muted">
            {isPendingDeposit ? 'Waiting for M-Pesa' : relativeTime(transaction.created_at)}
          </span>
          {transaction.status !== 'completed' ? (
            <StatusBadge status={transaction.status} />
          ) : null}
        </span>
      </span>

      <Money
        value={transaction.amount}
        signed={!amount.isZero()}
        className="shrink-0 text-body font-medium"
      />
    </button>
  )
}
