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
 * Bottom tab bar on mobile. Desktop navigation lives in the top bar, so this
 * is the only bar of its kind and carries its own landmark name.
 *
 * It goes away when Home is dissolved and Trade becomes the app's surface;
 * until then it is how a phone reaches the four screens that still exist.
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
