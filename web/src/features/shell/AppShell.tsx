import { Navigate, Outlet, useLocation } from 'react-router-dom'
import { AlertCircle, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { EmptyState } from '@/components/EmptyState'
import { Panel } from '@/components/ui/Panel'
import { toast } from '@/components/ui/Toast'
import { useAccountStream, type AccountEvent, type TradeClosedEvent } from '@/hooks/useAccountStream'
import { usePriceFeed } from '@/hooks/usePriceFeed'
import { useSignalFeed, useSignalStream } from '@/hooks/useSignalStream'
import { useCreateAccount, useMe, useWallet } from '@/hooks/useQueries'
import { money, signedMoney } from '@/lib/format'
import { AccountPanel } from '@/features/profile/AccountPanel'
import { PositionsPanel } from '@/features/portfolio/PositionsPanel'
import { SignalsPanel } from '@/features/signals/SignalsPanel'
import { DepositSheet } from '@/features/wallet/DepositSheet'
import { HistoryPanel } from '@/features/wallet/HistoryPanel'
import { WithdrawSheet } from '@/features/wallet/WithdrawSheet'
import { useIsAuthenticated, useSessionStore } from '@/store/session'
import { AccountSwitcher } from './AccountSwitcher'
import { NavDrawer } from './NavDrawer'
import { overlay, useOverlayOpen } from './overlays'
import { PANEL_ITEMS } from './panelItems'
import { TabBar } from './TabBar'
import { TopBar } from './TopBar'
import { useActiveAccount } from './useActiveAccount'
import { usePanel, type PanelName } from './usePanel'

/**
 * What is inside each panel. One place, so adding a panel is an entry here, a
 * name in PANELS and a label in PANEL_ITEMS -- which is also where the title
 * comes from, so a panel's header cannot drift from what the chrome calls it.
 */
const panels: Record<PanelName, React.ReactNode> = {
  positions: <PositionsPanel />,
  history: <HistoryPanel />,
  signals: <SignalsPanel />,
  account: <AccountPanel />,
}

/**
 * The persistent frame around every authenticated screen. It owns account
 * context, navigation, the signal socket and the account socket, so an event
 * reaches the user wherever they are in the app.
 */
export function AppShell() {
  const isAuthenticated = useIsAuthenticated()
  const location = useLocation()

  const { account, accountId, accounts, isLoading, error, refetch } = useActiveAccount()
  const createAccount = useCreateAccount()
  useMe()

  // The shell needs the balance for the account pill, and the withdraw sheet
  // needs it too. Same query key the screens use, so this is not a second
  // request -- it is the same one, asked for one level higher.
  const wallet = useWallet(accountId)
  const realAccount = accounts.find((a) => a.account_type === 'real') ?? null
  const setActiveAccount = useSessionStore((s) => s.setActiveAccount)

  const menuOpen = useOverlayOpen('menu')
  const { panel, close: closePanel } = usePanel()

  const accountsOpen = useOverlayOpen('accounts')
  const depositOpen = useOverlayOpen('deposit')
  const withdrawOpen = useOverlayOpen('withdraw')
  // Radix reports both directions; only the closing one is ours to act on,
  // since every opening goes through the overlay API.
  const onDismiss = (open: boolean) => {
    if (!open) overlay.close()
  }

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
      <TopBar
        account={account}
        wallet={wallet.data}
        isLoading={isLoading}
        onOpenMenu={overlay.menu}
        onOpenSwitcher={overlay.accounts}
        onDeposit={overlay.deposit}
      />

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

      <div className="flex w-full flex-1">
        {/* A column beside the chart on a desktop, a full-screen sheet on a
            phone. One panel is open at a time and it is mounted once, here, so
            it outlives whichever screen is under it -- and so its content
            keeps the symbols it watches registered with the shell's one price
            feed. */}
        <Panel
          open={panel !== null}
          onClose={closePanel}
          title={panel ? PANEL_ITEMS[panel].label : ''}
        >
          {panel ? panels[panel] : null}
        </Panel>

        {/* The surface takes the whole window. The 1200px reading width that
            used to apply to everything else went with the screens that needed
            it: a chart inside one is a widget, and a panel beside it made that
            worse. */}
        <main className="min-w-0 flex-1 pb-[calc(theme(spacing.tabbar)+16px)] lg:pb-8">
          <Outlet />
        </main>
      </div>

      <TabBar />

      <NavDrawer open={menuOpen} onOpenChange={onDismiss} />

      <AccountSwitcher
        open={accountsOpen}
        onOpenChange={onDismiss}
        accounts={accounts}
        activeAccountId={accountId}
        isLoading={isLoading}
      />

      <DepositSheet
        open={depositOpen}
        onOpenChange={onDismiss}
        account={account}
        realAccount={realAccount}
        onSwitchToReal={() => {
          if (realAccount) setActiveAccount(realAccount.id)
          overlay.close()
        }}
      />

      <WithdrawSheet
        open={withdrawOpen}
        onOpenChange={onDismiss}
        account={account}
        wallet={wallet.data}
      />
    </div>
  )
}
