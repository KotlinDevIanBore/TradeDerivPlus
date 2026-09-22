import { Link, useLocation } from 'react-router-dom'
import { Briefcase, CandlestickChart, Home, Receipt, type LucideIcon } from 'lucide-react'
import { cn } from '@/lib/cn'
import { MAIN_SURFACE, usePanel } from './usePanel'

interface Tab {
  to: string
  label: string
  icon: LucideIcon
}

/**
 * Bottom tab bar on mobile. Desktop navigation lives in the top bar, so this
 * is the only bar of its kind and carries its own landmark name.
 *
 * It goes away when Home is dissolved and Trade becomes the app's surface;
 * until then it is how a phone reaches what still exists. The tabs that are
 * panels rather than screens arrive at the surface with their panel open, and
 * highlight on the panel rather than on the path.
 */
export function TabBar() {
  const { pathname } = useLocation()
  const { panel, href } = usePanel()

  const tabs: (Tab & { active: boolean })[] = [
    { to: '/', label: 'Home', icon: Home, active: pathname === '/' },
    {
      to: MAIN_SURFACE,
      label: 'Trade',
      icon: CandlestickChart,
      active: pathname === MAIN_SURFACE && panel === null,
    },
    {
      to: href('positions'),
      label: 'Positions',
      icon: Briefcase,
      active: panel === 'positions',
    },
    {
      to: href('history'),
      label: 'History',
      icon: Receipt,
      active: panel === 'history',
    },
  ]

  return (
    <nav
      aria-label="Bottom"
      className={cn(
        'fixed inset-x-0 bottom-0 z-30 h-tabbar border-t border-hairline',
        'bg-bg/90 backdrop-blur-lg lg:hidden',
      )}
    >
      <ul className="flex h-full">
        {tabs.map(({ to, label, icon: Icon, active }) => (
          <li key={label} className="flex-1">
            <Link
              to={to}
              aria-current={active ? 'page' : undefined}
              className={cn(
                'flex h-full flex-col items-center justify-center gap-1 transition-colors',
                active ? 'text-accent' : 'text-muted hover:text-text',
              )}
            >
              <Icon className="h-6 w-6" aria-hidden />
              <span className="text-[11px] font-medium">{label}</span>
            </Link>
          </li>
        ))}
      </ul>
    </nav>
  )
}
