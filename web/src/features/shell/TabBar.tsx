import { cn } from '@/lib/cn'
import { PANEL_ITEMS } from './panelItems'
import { PANELS, usePanel } from './usePanel'

/**
 * The phone's way into the four panels: one toggle each, icon only.
 *
 * It is not a set of destinations any more, because there is nowhere to go --
 * the surface is the page and the panels open over it. So these are the same
 * toggles the desktop top bar has, moved to where a thumb is, and the bar
 * carries no Trade item: trade is what is behind it.
 *
 * Deposit, Withdraw and Log out are not panels and live in the drawer, which
 * is the hamburger's whole job now.
 */
export function TabBar() {
  const { panel, toggle } = usePanel()

  return (
    <nav
      aria-label="Bottom"
      className={cn(
        'fixed inset-x-0 bottom-0 z-30 h-tabbar border-t border-hairline',
        'bg-bg/90 backdrop-blur-lg lg:hidden',
      )}
    >
      <ul className="flex h-full">
        {PANELS.map((name) => {
          const { label, icon: Icon } = PANEL_ITEMS[name]
          const active = panel === name

          return (
            <li key={name} className="flex-1">
              <button
                type="button"
                onClick={() => toggle(name)}
                aria-pressed={active}
                aria-label={label}
                className={cn(
                  'flex h-full w-full items-center justify-center transition-colors',
                  active ? 'text-accent' : 'text-muted hover:text-text',
                )}
              >
                <Icon className="h-6 w-6" aria-hidden />
              </button>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}
