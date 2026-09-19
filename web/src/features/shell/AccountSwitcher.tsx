import { Check, Plus } from 'lucide-react'
import { AccountPill } from '@/components/AccountPill'
import { Money } from '@/components/Money'
import { Button } from '@/components/ui/Button'
import { Sheet } from '@/components/ui/Sheet'
import { Skeleton } from '@/components/ui/Skeleton'
import { toast } from '@/components/ui/Toast'
import { useCreateAccount, useWallet } from '@/hooks/useQueries'
import { cn } from '@/lib/cn'
import { useSessionStore } from '@/store/session'
import type { Account } from '@/types'

function AccountRow({
  account,
  active,
  onSelect,
}: {
  account: Account
  active: boolean
  onSelect: () => void
}) {
  const wallet = useWallet(account.id)

  return (
    <button
      type="button"
      onClick={onSelect}
      className={cn(
        'flex w-full items-center justify-between rounded-xl border px-4 py-3 text-left transition-colors',
        active ? 'border-accent bg-accent-subtle' : 'border-border bg-surface hover:bg-elevated',
      )}
    >
      <span className="min-w-0">
        <AccountPill account={account} />
        <span className="mt-1.5 block text-card font-semibold">
          {wallet.isLoading ? (
            <Skeleton className="h-5 w-24" />
          ) : (
            <Money value={wallet.data?.total} />
          )}
        </span>
      </span>
      {active ? <Check className="h-5 w-5 shrink-0 text-accent" aria-hidden /> : null}
    </button>
  )
}

/**
 * Switch account or create the one type the user does not have yet. Switching
 * re-keys every account-scoped query, so balances, positions and the ledger all
 * follow the selection.
 */
export function AccountSwitcher({
  open,
  onOpenChange,
  accounts,
  activeAccountId,
  isLoading,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  accounts: Account[]
  activeAccountId: string | null
  isLoading: boolean
}) {
  const setActiveAccount = useSessionStore((s) => s.setActiveAccount)
  const createAccount = useCreateAccount()

  const hasDemo = accounts.some((a) => a.account_type === 'demo')
  const hasReal = accounts.some((a) => a.account_type === 'real')

  const create = (type: 'demo' | 'real') =>
    createAccount.mutate(type, {
      onSuccess: (account) => {
        setActiveAccount(account.id)
        toast.success(`${type === 'demo' ? 'Demo' : 'Real'} account created`)
        onOpenChange(false)
      },
      onError: () => toast.error("Couldn't create account", 'Try again in a moment.'),
    })

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title="Your accounts" className="sm:max-h-[60vh]">
      <div className="space-y-2">
        {isLoading ? (
          <>
            <Skeleton className="h-[74px] w-full rounded-xl" />
            <Skeleton className="h-[74px] w-full rounded-xl" />
          </>
        ) : (
          accounts.map((account) => (
            <AccountRow
              key={account.id}
              account={account}
              active={account.id === activeAccountId}
              onSelect={() => {
                setActiveAccount(account.id)
                onOpenChange(false)
              }}
            />
          ))
        )}
      </div>

      {(!hasReal || !hasDemo) && !isLoading ? (
        <div className="mt-4 space-y-2">
          {!hasReal ? (
            <Button
              variant="outline"
              block
              loading={createAccount.isPending}
              onClick={() => create('real')}
            >
              <Plus className="h-4 w-4" aria-hidden />
              Create real account
            </Button>
          ) : null}
          {!hasDemo ? (
            <Button
              variant="outline"
              block
              loading={createAccount.isPending}
              onClick={() => create('demo')}
            >
              <Plus className="h-4 w-4" aria-hidden />
              Create demo account
            </Button>
          ) : null}
        </div>
      ) : null}

      <p className="mt-4 text-meta text-faint">
        Demo funds are practice money. They cannot be withdrawn.
      </p>
    </Sheet>
  )
}
