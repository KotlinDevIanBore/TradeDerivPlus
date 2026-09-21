import { useState } from 'react'
import { Navigate, Outlet, useLocation } from 'react-router-dom'
import { AlertCircle, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { EmptyState } from '@/components/EmptyState'
import { toast } from '@/components/ui/Toast'
import { useAccountStream, type AccountEvent, type TradeClosedEvent } from '@/hooks/useAccountStream'
import { usePriceFeed } from '@/hooks/usePriceFeed'
import { useSignalFeed, useSignalStream } from '@/hooks/useSignalStream'
import { useCreateAccount, useMe } from '@/hooks/useQueries'
import { money, signedMoney } from '@/lib/format'
import { useIsAuthenticated } from '@/store/session'
import { AccountSwitcher } from './AccountSwitcher'
import { Sidebar, TabBar } from './TabBar'
import { TopBar } from './TopBar'
import { useActiveAccount } from './useActiveAccount'

/**
 * The persistent frame around every authenticated screen. It owns account
 * context, navigation, the signal socket and the account socket, so an event
 * reaches the user wherever they are in the app.
 */
export function AppShell() {
  const isAuthenticated = useIsAuthenticated()
  const location = useLocation()
  const [switcherOpen, setSwitcherOpen] = useState(false)

  const { account, accountId, accounts, isLoading, error, refetch } = useActiveAccount()
  const createAccount = useCreateAccount()
  useMe()

  // The price and signal sockets live here, for the whole session. Screens say
  // which symbols they need with useWatchSymbols; none of them owns a
  // connection, so none of them can close one by unmounting.
  usePriceFeed()
  useSignalFeed()

  // A new signal raises a toast wherever the user is.
  useSignalStream((signal) => {
    toast.info(
      `New ${signal.direction.toUpperCase()} signal on ${signal.symbol_code}`,
      `${Number(signal.confidence).toFixed(0)}% confidence`,
    )
  })

  // Deposit settlements and auto-closed positions surface the same way.
  useAccountStream(accountId, (event: AccountEvent) => {
    if (event.type === 'transaction') {
      const tx = event.payload as { status: string; type: string; amount?: string; reason?: string }
      if (tx.type === 'deposit' && tx.status === 'completed') {
        toast.success('Deposit received', `${money(tx.amount)} added to your balance`)
      }
      if (tx.type === 'deposit' && tx.status === 'failed') {
        toast.error('Deposit failed', tx.reason)
      }
    }

    if (event.type === 'trade_closed') {
      const { trade, reason } = event.payload as TradeClosedEvent
      if (reason === 'manual') return // the Portfolio screen already reported it
      toast.info(
        `${trade.symbol_code} closed at ${reason === 'stop_loss' ? 'stop loss' : 'take profit'}`,
        `${signedMoney(trade.pnl)} realised`,
      )
    }
  })

  if (!isAuthenticated) {
    const next = encodeURIComponent(location.pathname + location.search)
    return <Navigate to={`/login?next=${next}`} replace />
  }

  // Should not happen after signup, but an account-less session must still
  // offer a way forward rather than showing an empty app.
  if (!isLoading && !error && accounts.length === 0) {
    return (
      <div className="flex min-h-full items-center justify-center px-gutter">
        <EmptyState
          title="Create your first account"
          hint="Start with a demo account funded with practice money."
          action={
            <Button
              loading={createAccount.isPending}
              onClick={() => createAccount.mutate('demo')}
            >
              Create demo account
            </Button>
          }
        />
      </div>
    )
  }

  return (
    <div className="flex min-h-full flex-col">
      <TopBar account={account} isLoading={isLoading} onOpenSwitcher={() => setSwitcherOpen(true)} />

      {error ? (
        <div className="flex items-center gap-2 border-b border-accent/30 bg-accent-subtle px-gutter py-2 lg:px-6">
          <AlertCircle className="h-4 w-4 shrink-0 text-accent" aria-hidden />
          <p className="flex-1 text-meta text-text">Couldn't load your accounts</p>
          <button
            type="button"
            onClick={() => refetch()}
            className="flex items-center gap-1 text-meta font-semibold text-accent hover:underline"
          >
            <RefreshCw className="h-3 w-3" aria-hidden />
            Retry
          </button>
        </div>
      ) : null}

      <div className="mx-auto flex w-full max-w-[1200px] flex-1">
        <Sidebar />
        <main className="min-w-0 flex-1 pb-[calc(theme(spacing.tabbar)+16px)] lg:pb-8">
          <Outlet />
        </main>
      </div>

      <TabBar />

      <AccountSwitcher
        open={switcherOpen}
        onOpenChange={setSwitcherOpen}
        accounts={accounts}
        activeAccountId={accountId}
        isLoading={isLoading}
      />
    </div>
  )
}
