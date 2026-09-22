import { Link, NavLink } from 'react-router-dom'
import {
  ArrowDownToLine,
  Bot,
  Briefcase,
  CandlestickChart,
  ChevronDown,
  History,
  Menu,
  TrendingUp,
  User as UserIcon,
  type LucideIcon,
} from 'lucide-react'
import { AccountPill } from '@/components/AccountPill'
import { ConnectionDot } from '@/components/ConnectionDot'
import { Money } from '@/components/Money'
import { Button } from '@/components/ui/Button'
import { Skeleton } from '@/components/ui/Skeleton'
import { cn } from '@/lib/cn'
import type { Account, Wallet } from '@/types'
import { MAIN_SURFACE, usePanel, type PanelName } from './usePanel'

/**
 * A row item is either somewhere to go or a panel to toggle, and the two are
 * different enough to be different elements: a panel opens beside what you are
 * looking at, so it is a button that says pressed, not a link that says page.
 */
type NavItem = { label: string; icon: LucideIcon } & (
  | { to: string; panel?: never }
  | { panel: PanelName; to?: never }
)

/**
 * Desktop destinations, in the row rather than down a sidebar.
 *
 * Trade is here only until it becomes the surface everything else sits on, at
 * which point the logo is the way back to it and this entry goes away.
 */
const items: NavItem[] = [
  { panel: 'positions', label: 'Positions', icon: Briefcase },
  { to: MAIN_SURFACE, label: 'Trade', icon: CandlestickChart },
  { panel: 'history', label: 'History', icon: History },
  { to: '/signals', label: 'Signals', icon: Bot },
]

/** One treatment for the row, whether the item is a link or a panel toggle. */
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

        <Link to="/" className="flex shrink-0 items-center gap-2" aria-label="TradeDerivPlus home">
          <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-accent">
            <TrendingUp className="h-4 w-4 text-white" aria-hidden />
          </span>
          <span className="hidden text-body font-bold tracking-tight sm:inline">TradeDerivPlus</span>
        </Link>

        <nav aria-label="Main" className="hidden lg:block">
          <ul className="flex items-center gap-1">
            {items.map(({ to, panel: name, label, icon: Icon }) => (
              <li key={label}>
                {name ? (
                  <button
                    type="button"
                    onClick={() => panel.toggle(name)}
                    aria-pressed={panel.panel === name}
                    className={item(panel.panel === name)}
                  >
                    <Icon className="h-4 w-4" aria-hidden />
                    {label}
                  </button>
                ) : (
                  <NavLink
                    to={to!}
                    className={({ isActive }) =>
                      // Trade is the surface the panels sit on, so it stops
                      // claiming to be the active thing while one is open.
                      // Two lit items read as two places to be.
                      item(isActive && !(to === MAIN_SURFACE && panel.panel !== null))
                    }
                  >
                    <Icon className="h-4 w-4" aria-hidden />
                    {label}
                  </NavLink>
                )}
              </li>
            ))}
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

          <Link
            to="/profile"
            className="hidden rounded-lg p-1.5 text-muted transition-colors hover:bg-surface hover:text-text lg:block"
            aria-label="Profile"
          >
            <UserIcon className="h-5 w-5" />
          </Link>
        </div>
      </div>
    </header>
  )
}
