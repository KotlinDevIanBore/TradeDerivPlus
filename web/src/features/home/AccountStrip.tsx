import { Plus } from 'lucide-react'
import { cn } from '@/lib/cn'
import type { Account } from '@/types'

/**
 * Horizontally scrollable account chips. A one-tap switch for the common case
 * of hopping between demo and real without opening the sheet.
 */
export function AccountStrip({
  accounts,
  activeAccountId,
  onSelect,
  onCreate,
}: {
  accounts: Account[]
  activeAccountId: string | null
  onSelect: (id: string) => void
  onCreate: () => void
}) {
  if (accounts.length === 0) return null

  return (
    <div className="no-scrollbar -mx-gutter flex gap-2 overflow-x-auto px-gutter lg:mx-0 lg:px-0">
      {accounts.map((account) => {
        const active = account.id === activeAccountId
        const isDemo = account.account_type === 'demo'

        return (
          <button
            key={account.id}
            type="button"
            onClick={() => onSelect(account.id)}
            className={cn(
              'flex shrink-0 items-center gap-1.5 rounded-pill border px-3 py-1.5 text-meta font-medium transition-colors',
              active
                ? 'border-accent bg-accent-subtle text-text'
                : 'border-border bg-surface text-muted hover:text-text',
            )}
          >
            <span className={cn('uppercase tracking-wider', isDemo ? 'text-demo' : 'text-real')}>
              {account.account_type}
            </span>
            <span className="tnum">{account.account_number}</span>
          </button>
        )
      })}

      {accounts.length < 2 ? (
        <button
          type="button"
          onClick={onCreate}
          className="flex shrink-0 items-center gap-1 rounded-pill border border-dashed border-border px-3 py-1.5 text-meta text-muted transition-colors hover:text-text"
        >
          <Plus className="h-3.5 w-3.5" aria-hidden />
          New account
        </button>
      ) : null}
    </div>
  )
}
