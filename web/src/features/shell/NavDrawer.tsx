import { Link } from 'react-router-dom'
import {
  ArrowDownToLine,
  ArrowUpFromLine,
  Bot,
  Briefcase,
  History,
  LogOut,
  User as UserIcon,
  type LucideIcon,
} from 'lucide-react'
import { Sheet } from '@/components/ui/Sheet'
import { useLogout } from '@/hooks/useAuth'
import { cn } from '@/lib/cn'
import { overlay } from './overlays'
import { usePanel } from './usePanel'

const row =
  'flex w-full items-center gap-3 rounded-xl px-3 py-3 text-body font-medium transition-colors'

/**
 * Somewhere to go. Panels share a path and differ by query string, so what is
 * current is passed in rather than read off the URL -- NavLink would light
 * every panel entry at once.
 */
function Destination({
  to,
  label,
  icon: Icon,
  active,
  onNavigate,
}: {
  to: string
  label: string
  icon: LucideIcon
  active: boolean
  onNavigate: () => void
}) {
  return (
    <li>
      <Link
        to={to}
        onClick={onNavigate}
        aria-current={active ? 'page' : undefined}
        className={cn(row, active ? 'bg-accent-subtle text-accent' : 'text-text hover:bg-surface')}
      >
        <Icon className="h-5 w-5 shrink-0 text-muted" aria-hidden />
        {label}
      </Link>
    </li>
  )
}

function Action({
  label,
  icon: Icon,
  onClick,
}: {
  label: string
  icon: LucideIcon
  onClick: () => void
}) {
  return (
    <li>
      <button type="button" onClick={onClick} className={cn(row, 'text-text hover:bg-surface')}>
        <Icon className="h-5 w-5 shrink-0 text-muted" aria-hidden />
        {label}
      </button>
    </li>
  )
}

/**
 * The phone's way to everything that is not on the bottom bar. Money is at the
 * top because it is what people open this for; the destinations below it are
 * named for what they are becoming, the same as the desktop row.
 *
 * Deposit and Withdraw raise the shell's sheets rather than navigating, so the
 * drawer closes into the flow instead of into a screen.
 */
export function NavDrawer({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const logout = useLogout()
  const { panel, href } = usePanel()
  const close = () => onOpenChange(false)

  return (
    <Sheet open={open} onOpenChange={onOpenChange} side="left" title="Menu">
      {/* The sheet's body is the scroll area; this makes it a column so the
          way out can sit at the foot of the drawer rather than under the
          last destination. */}
      <div className="flex min-h-full flex-col">
        <nav aria-label="Menu">
          <ul className="-mx-1 space-y-0.5">
            <Action label="Deposit" icon={ArrowDownToLine} onClick={overlay.deposit} />
            <Action label="Withdraw" icon={ArrowUpFromLine} onClick={overlay.withdraw} />
            <Destination
              to={href('positions')}
              label="Positions"
              icon={Briefcase}
              active={panel === 'positions'}
              onNavigate={close}
            />
            <Destination
              to={href('history')}
              label="History"
              icon={History}
              active={panel === 'history'}
              onNavigate={close}
            />
            <Destination
              to={href('signals')}
              label="Signals"
              icon={Bot}
              active={panel === 'signals'}
              onNavigate={close}
            />
            <Destination
              to={href('account')}
              label="Account"
              icon={UserIcon}
              active={panel === 'account'}
              onNavigate={close}
            />
          </ul>
        </nav>

        <div className="mt-auto border-t border-hairline pt-3">
          <button
            type="button"
            onClick={() => {
              close()
              logout()
            }}
            className={cn(row, '-mx-1 text-accent hover:bg-accent-subtle')}
          >
            <LogOut className="h-5 w-5 shrink-0" aria-hidden />
            Log out
          </button>
        </div>
      </div>
    </Sheet>
  )
}
