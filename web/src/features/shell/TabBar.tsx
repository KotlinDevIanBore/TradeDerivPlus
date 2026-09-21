import { NavLink } from 'react-router-dom'
import { CandlestickChart, Home, Wallet, Briefcase, type LucideIcon } from 'lucide-react'
import { cn } from '@/lib/cn'

interface Tab {
  to: string
  label: string
  icon: LucideIcon
}

const tabs: Tab[] = [
  { to: '/', label: 'Home', icon: Home },
  { to: '/trade', label: 'Trade', icon: CandlestickChart },
  { to: '/portfolio', label: 'Portfolio', icon: Briefcase },
  { to: '/wallet', label: 'Wallet', icon: Wallet },
]

/**
 * Bottom tab bar on mobile. The desktop sidebar renders the same four links,
 * so the two carry distinct landmark names: both are in the DOM at once and
 * only one is ever on screen.
 */
export function TabBar() {
  return (
    <nav
      aria-label="Bottom"
      className={cn(
        'fixed inset-x-0 bottom-0 z-30 h-tabbar border-t border-hairline',
        'bg-bg/90 backdrop-blur-lg lg:hidden',
      )}
    >
      <ul className="flex h-full">
        {tabs.map(({ to, label, icon: Icon }) => (
          <li key={to} className="flex-1">
            <NavLink
              to={to}
              end={to === '/'}
              className={({ isActive }) =>
                cn(
                  'flex h-full flex-col items-center justify-center gap-1 transition-colors',
                  isActive ? 'text-accent' : 'text-muted hover:text-text',
                )
              }
            >
              <Icon className="h-6 w-6" aria-hidden />
              <span className="text-[11px] font-medium">{label}</span>
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  )
}

/** Desktop sidebar: the same destinations, laid out vertically. */
export function Sidebar() {
  return (
    <nav
      aria-label="Main"
      className="hidden w-[220px] shrink-0 border-r border-hairline px-3 py-6 lg:block"
    >
      <ul className="space-y-1">
        {tabs.map(({ to, label, icon: Icon }) => (
          <li key={to}>
            <NavLink
              to={to}
              end={to === '/'}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-3 rounded-xl px-3 py-2.5 text-body font-medium transition-colors',
                  isActive ? 'bg-accent-subtle text-accent' : 'text-muted hover:bg-surface hover:text-text',
                )
              }
            >
              <Icon className="h-5 w-5" aria-hidden />
              {label}
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  )
}
