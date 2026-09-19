import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { ChevronRight } from 'lucide-react'
import { ErrorState } from '@/components/ErrorState'
import { Skeleton } from '@/components/ui/Skeleton'
import {
  SignalCard,
  SignalCardEmpty,
  SignalCardSkeleton,
} from '@/features/signals/SignalCard'
import { AccountSwitcher } from '@/features/shell/AccountSwitcher'
import { DepositSheet } from '@/features/wallet/DepositSheet'
import { WithdrawSheet } from '@/features/wallet/WithdrawSheet'
import { useActiveAccount } from '@/features/shell/useActiveAccount'
import { usePriceStream } from '@/hooks/usePriceStream'
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

  const [depositOpen, setDepositOpen] = useState(false)
  const [switcherOpen, setSwitcherOpen] = useState(false)
  const [withdrawOpen, setWithdrawOpen] = useState(false)

  const wallet = useWallet(accountId)
  const signals = useSignals(undefined, 1)
  const openTrades = useTrades(accountId, 'open', 3)
  const transactions = useTransactions(accountId, undefined, 5)

  // Keep prices live for the positions shown in the summary.
  const symbols = useMemo(
    () => [...new Set((openTrades.data?.items ?? []).map((t) => t.symbol_code))],
    [openTrades.data],
  )
  usePriceStream(symbols)
  useRelativeTime()

  const latestSignal = signals.data?.items?.[0]
  const realAccount = accounts.find((a) => a.account_type === 'real') ?? null
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
          onDeposit={() => setDepositOpen(true)}
          onWithdraw={() => setWithdrawOpen(true)}
          canWithdraw={canWithdraw}
          withdrawHint="Verify your identity to withdraw"
        />

        <AccountStrip
          accounts={accounts}
          activeAccountId={accountId}
          onSelect={setActiveAccount}
          onCreate={() => setSwitcherOpen(true)}
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
            onDeposit={() => setDepositOpen(true)}
          />
        )}
      </div>

      <AccountSwitcher
        open={switcherOpen}
        onOpenChange={setSwitcherOpen}
        accounts={accounts}
        activeAccountId={accountId}
        isLoading={false}
      />

      <DepositSheet
        open={depositOpen}
        onOpenChange={setDepositOpen}
        account={account}
        realAccount={realAccount}
        onSwitchToReal={() => {
          if (realAccount) setActiveAccount(realAccount.id)
          setDepositOpen(false)
        }}
      />

      <WithdrawSheet
        open={withdrawOpen}
        onOpenChange={setWithdrawOpen}
        account={account}
        wallet={wallet.data}
      />
    </div>
  )
}
