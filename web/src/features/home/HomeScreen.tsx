import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { ChevronRight } from 'lucide-react'
import { ErrorState } from '@/components/ErrorState'
import { Skeleton } from '@/components/ui/Skeleton'
import {
  SignalCard,
  SignalCardEmpty,
  SignalCardSkeleton,
} from '@/features/signals/SignalCard'
import { overlay } from '@/features/shell/overlays'
import { useActiveAccount } from '@/features/shell/useActiveAccount'
import { useWatchSymbols } from '@/hooks/usePriceFeed'
import { useSignals, useTransactions, useTrades, useWallet } from '@/hooks/useQueries'
import { useRelativeTime } from '@/hooks/useRelativeTime'
import { useSessionStore } from '@/store/session'
import { AccountStrip } from './AccountStrip'
import { BalanceCard } from './BalanceCard'
import { OpenPositionsSummary } from './OpenPositionsSummary'
import { RecentActivity } from './RecentActivity'

/**
 * The daily landing screen: how much money there is, which account it is on,
 * and the latest signal so a user can act in one tap.
 *
 * Every card loads independently. One failing never blanks the page.
 */
export function HomeScreen() {
  const { account, accountId, accounts } = useActiveAccount()
  const setActiveAccount = useSessionStore((s) => s.setActiveAccount)
  const user = useSessionStore((s) => s.user)

  const wallet = useWallet(accountId)
  const signals = useSignals(undefined, 1)
  const openTrades = useTrades(accountId, 'open', 3)
  const transactions = useTransactions(accountId, undefined, 5)

  // Keep prices live for the positions shown in the summary.
  const symbols = useMemo(
    () => [...new Set((openTrades.data?.items ?? []).map((t) => t.symbol_code))],
    [openTrades.data],
  )
  useWatchSymbols(symbols)
  useRelativeTime()

  const latestSignal = signals.data?.items?.[0]
  const canWithdraw = user?.kyc_status === 'verified'

  return (
    <div className="space-y-3 px-gutter py-4 lg:grid lg:grid-cols-2 lg:gap-3 lg:space-y-0 lg:px-6 lg:py-6">
      <div className="space-y-3">
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

        <AccountStrip
          accounts={accounts}
          activeAccountId={accountId}
          onSelect={setActiveAccount}
          onCreate={overlay.accounts}
        />

        <section aria-label="Latest signal">
          <div className="mb-2 flex items-center justify-between">
            <h2 className="text-card font-semibold">Latest signal</h2>
            <Link
              to="/signals"
              className="flex items-center gap-0.5 text-meta font-medium text-accent hover:underline"
            >
              See all
              <ChevronRight className="h-3.5 w-3.5" aria-hidden />
            </Link>
          </div>

          {signals.isLoading ? (
            <SignalCardSkeleton />
          ) : signals.isError ? (
            <div className="rounded-card border border-border bg-surface">
              <ErrorState error={signals.error} onRetry={() => signals.refetch()} compact />
            </div>
          ) : latestSignal ? (
            <SignalCard signal={latestSignal} />
          ) : (
            <SignalCardEmpty />
          )}
        </section>
      </div>

      <div className="space-y-3">
        {openTrades.isLoading ? (
          <Skeleton className="h-[140px] w-full rounded-card" />
        ) : openTrades.isError ? (
          // Without this the block renders as empty, which reads as "you have no
          // positions" when the truth is that we could not find out.
          <div className="rounded-card border border-border bg-surface">
            <ErrorState error={openTrades.error} onRetry={() => openTrades.refetch()} compact />
          </div>
        ) : (
          <OpenPositionsSummary trades={openTrades.data?.items ?? []} />
        )}

        {transactions.isLoading ? (
          <Skeleton className="h-[220px] w-full rounded-card" />
        ) : transactions.isError ? (
          <div className="rounded-card border border-border bg-surface">
            <ErrorState error={transactions.error} onRetry={() => transactions.refetch()} compact />
          </div>
        ) : (
          <RecentActivity
            transactions={transactions.data?.items ?? []}
            onDeposit={overlay.deposit}
          />
        )}
      </div>
    </div>
  )
}
