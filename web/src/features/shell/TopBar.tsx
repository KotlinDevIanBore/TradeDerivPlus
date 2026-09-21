import { Link } from 'react-router-dom'
import { ChevronDown, TrendingUp, User as UserIcon } from 'lucide-react'
import { AccountPill } from '@/components/AccountPill'
import { ConnectionDot } from '@/components/ConnectionDot'
import { Skeleton } from '@/components/ui/Skeleton'
import type { Account } from '@/types'

export function TopBar({
  account,
  isLoading,
  onOpenSwitcher,
}: {
  account: Account | null
  isLoading: boolean
  onOpenSwitcher: () => void
}) {
  return (
    <header className="sticky top-0 z-30 border-b border-hairline bg-bg/90 backdrop-blur-lg">
      <div className="flex h-topbar items-center justify-between gap-3 px-gutter lg:px-6">
        <Link to="/" className="flex shrink-0 items-center gap-2" aria-label="TradeDerivPlus home">
          <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-accent">
            <TrendingUp className="h-4 w-4 text-white" aria-hidden />
          </span>
          <span className="hidden text-body font-bold tracking-tight sm:inline">TradeDerivPlus</span>
        </Link>

        {isLoading ? (
          <Skeleton className="h-7 w-28 rounded-pill" />
        ) : account ? (
          <button
            type="button"
            onClick={onOpenSwitcher}
            className="flex items-center gap-1.5 rounded-pill px-2 py-1 transition-colors hover:bg-surface"
            aria-label="Switch account"
          >
            <AccountPill account={account} />
            <ChevronDown className="h-4 w-4 text-muted" aria-hidden />
          </button>
        ) : (
          <span className="text-meta text-faint">—</span>
        )}

        <div className="flex shrink-0 items-center gap-3">
          <ConnectionDot hideWhenIdle />
          <Link
            to="/profile"
            className="rounded-lg p-1.5 text-muted transition-colors hover:bg-surface hover:text-text"
            aria-label="Profile"
          >
            <UserIcon className="h-5 w-5" />
          </Link>
        </div>
      </div>
    </header>
  )
}
