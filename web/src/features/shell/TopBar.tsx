import { Link } from 'react-router-dom'
import { ArrowDownToLine, ChevronDown, Menu, TrendingUp } from 'lucide-react'
import { AccountPill } from '@/components/AccountPill'
import { ConnectionDot } from '@/components/ConnectionDot'
import { Money } from '@/components/Money'
import { Button } from '@/components/ui/Button'
import { Skeleton } from '@/components/ui/Skeleton'
import { useMe } from '@/hooks/useQueries'
import { cn } from '@/lib/cn'
import type { Account, Wallet } from '@/types'
import { chromePanels, PANEL_ITEMS } from './panelItems'
import { MAIN_SURFACE, usePanel } from './usePanel'

const AccountIcon = PANEL_ITEMS.account.icon

/** One treatment for every toggle in the bar. */
const item = (active: boolean) =>
  cn(
    'flex items-center gap-2 rounded-lg px-3 py-2 text-body font-medium transition-colors',
    active ? 'bg-accent-subtle text-accent' : 'text-muted hover:bg-surface hover:text-text',
  )

export function TopBar({
  account,
  wallet,
  isLoading,
  onOpenMenu,
  onOpenSwitcher,
  onDeposit,
}: {
  account: Account | null
  wallet?: Wallet
  isLoading: boolean
  onOpenMenu: () => void
  onOpenSwitcher: () => void
  onDeposit: () => void
}) {
  const panel = usePanel()
  const me = useMe()
  // Account sits beside the pill, not in the main row.
  const row = chromePanels(Boolean(me.data?.digit_contracts_enabled)).filter(
    (name) => name !== 'account',
  )

  return (
    <header className="sticky top-0 z-30 border-b border-hairline bg-bg/90 backdrop-blur-lg">
      <div className="flex h-topbar items-center gap-2 px-gutter lg:gap-4 lg:px-6">
        <button
          type="button"
          onClick={onOpenMenu}
          aria-label="Open menu"
          className="-ml-1.5 rounded-lg p-1.5 text-muted transition-colors hover:bg-surface hover:text-text lg:hidden"
        >
          <Menu className="h-6 w-6" aria-hidden />
        </button>

        {/* The way back to a clean surface, which is what the Trade item in
            the row used to be for. */}
        <Link
          to={MAIN_SURFACE}
          className="flex shrink-0 items-center gap-2"
          aria-label="TradeDerivPlus home"
        >
          <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-accent">
            <TrendingUp className="h-4 w-4 text-white" aria-hidden />
          </span>
          <span className="hidden text-body font-bold tracking-tight sm:inline">TradeDerivPlus</span>
        </Link>

        <nav aria-label="Main" className="hidden lg:block">
          <ul className="flex items-center gap-1">
            {row.map((name) => {
              const { label, icon: Icon } = PANEL_ITEMS[name]

              return (
                <li key={name}>
                  <button
                    type="button"
                    onClick={() => panel.toggle(name)}
                    aria-pressed={panel.panel === name}
                    className={item(panel.panel === name)}
                  >
                    <Icon className="h-4 w-4" aria-hidden />
                    {label}
                  </button>
                </li>
              )
            })}
          </ul>
        </nav>

        {/* Centred on a phone, alongside the actions on a desktop. */}
        <div className="flex min-w-0 flex-1 justify-center lg:justify-end">
          {isLoading ? (
            <Skeleton className="h-7 w-32 rounded-pill" />
          ) : account ? (
            <button
              type="button"
              onClick={onOpenSwitcher}
              className="flex min-w-0 items-center gap-1.5 rounded-pill px-2 py-1 transition-colors hover:bg-surface"
              aria-label="Switch account"
            >
              <AccountPill account={account} showNumber={false} />
              {/* The account number does not fit beside a balance on a phone,
                  and the balance is what a trader is checking. The number is
                  still in the account switcher and on Profile. */}
              <span className="hidden text-meta text-muted tnum lg:inline">
                {account.account_number}
              </span>
              {wallet ? (
                <Money value={wallet.total} className="text-body font-semibold" />
              ) : (
                <Skeleton className="h-4 w-16" />
              )}
              <ChevronDown className="h-4 w-4 shrink-0 text-muted" aria-hidden />
            </button>
          ) : (
            <span className="text-meta text-faint">—</span>
          )}
        </div>

        <div className="flex shrink-0 items-center gap-2 lg:gap-3">
          <ConnectionDot hideWhenIdle />

          {/* The word fits from 400px up, measured with a five-figure balance
              in the pill beside it. Below that the icon carries it, and the
              label stays on the button for anyone who cannot see either. */}
          <Button size="sm" onClick={onDeposit} aria-label="Deposit">
            <ArrowDownToLine className="h-4 w-4 min-[400px]:hidden" aria-hidden />
            <span className="hidden min-[400px]:inline">Deposit</span>
          </Button>

          {/* Account is a panel like the others, but it belongs beside the
              account pill rather than in the row. */}
          <button
            type="button"
            onClick={() => panel.toggle('account')}
            aria-pressed={panel.panel === 'account'}
            aria-label={PANEL_ITEMS.account.label}
            className={cn(
              'hidden rounded-lg p-1.5 transition-colors lg:block',
              panel.panel === 'account'
                ? 'bg-accent-subtle text-accent'
                : 'text-muted hover:bg-surface hover:text-text',
            )}
          >
            <AccountIcon className="h-5 w-5" aria-hidden />
          </button>
        </div>
      </div>
    </header>
  )
}
